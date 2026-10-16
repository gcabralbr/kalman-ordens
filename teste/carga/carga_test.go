// Package carga: teste de carga do serviço (SPEC v1.9 C). Roda o binário real (kalman-ordens
// servir) como processo próprio contra o MariaDB de teste e o PnT falso com um respondedor
// automático; rajadas de N POST /ordens de ENTRADA no mesmo segundo (usuários distintos), com
// fills, 1014 e saídas. Só roda com KALMAN_CARGA (ex.: KALMAN_CARGA=190,1000,4000) e
// KALMAN_TESTE_MARIADB; grava o resultado em JSON (KALMAN_CARGA_SAIDA, padrão: diretório temporário).
//
//	KALMAN_CARGA=190,1000,4000 KALMAN_TESTE_MARIADB=... go test -run TestCarga -timeout 60m -v ./teste/carga
//
// KALMAN_CARGA_TAXA: FIX_TAXA_MSG_S e FIX_RAJADA_MSG do serviço (vazio = o padrão); KALMAN_CARGA_SUFIXO
// entra no nome do JSON. KALMAN_CARGA_SINAIS=1: as rajadas vão pelo POST /sinais (SPEC v1.10 §6) —
// um sinal de entradas e um de saídas, em partes de 1.000 mandadas juntas — e o resultado confere a
// ordem de chegada ao PnT contra a rajada (deslocamento áureo).
package carga

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt/falso"
)

const (
	uidBase = 100000
	opBase  = 900100000
	simbolo = "WINZ26"
)

func TestCarga(t *testing.T) {
	lista := os.Getenv("KALMAN_CARGA")
	dsn := os.Getenv("KALMAN_TESTE_MARIADB")
	if lista == "" || dsn == "" {
		t.Skip("KALMAN_CARGA ou KALMAN_TESTE_MARIADB vazio")
	}
	bin := filepath.Join(t.TempDir(), "kalman-ordens")
	build := exec.Command("go", "build", "-o", bin, "./cmd/kalman-ordens")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	saida := os.Getenv("KALMAN_CARGA_SAIDA")
	if saida == "" {
		saida = t.TempDir()
	}
	for _, s := range strings.Split(lista, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n <= 0 {
			t.Fatalf("KALMAN_CARGA: %q", s)
		}
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			r := rodada(t, bin, dsn, n)
			b, _ := json.MarshalIndent(r, "", "  ")
			arq := filepath.Join(saida, fmt.Sprintf("carga-%d%s.json", n, os.Getenv("KALMAN_CARGA_SUFIXO")))
			if err := os.WriteFile(arq, b, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("resultado em %s\n%s", arq, b)
		})
	}
}

// ---------------------------------------------------------------- resultado

// Resultado de uma rodada (tudo em milissegundos, exceto onde dito).
type Resultado struct {
	N                   int                `json:"n"`
	ConciliacaoLogonMs  float64            `json:"conciliacao_logon_ms"`
	AFsNoLogon          int64              `json:"afs_no_logon"`
	Entradas            Fase               `json:"entradas"`
	Saidas              Fase               `json:"saidas"`
	ProtecaoFillAte1014 Quantis            `json:"protecao_fill_ate_1014_ms"`
	SaidaCancelAte1031  Quantis            `json:"saida_39_4_ate_1031_ms"`
	MsgsPorSegundo      MsgsSessao         `json:"msgs_por_segundo"`
	Servico             Recursos           `json:"servico"`
	MariaDB             Recursos           `json:"mariadb"`
	Sistema             CPUSistema         `json:"sistema"`
	BacklogFalsoMax     int                `json:"backlog_falso_max"`
	Metricas            map[string]Quantis `json:"metricas_ms"`     // histogramas do /metrics (quantis aproximados pelos baldes)
	BancoTotalSeg       float64            `json:"banco_total_seg"` // soma de banco_segundos
	BancoPorOperacao    map[string]Quantis `json:"banco_por_operacao_ms"`
	FilaMax             float64            `json:"fila_tamanho_max"`
	Alertas             map[string]float64 `json:"alertas"`
	Recusas             map[string]float64 `json:"recusas"`
	Respostas           map[string]int     `json:"respostas"`        // status/código dos POST
	Sinais              map[string]any     `json:"sinais,omitempty"` // KALMAN_CARGA_SINAIS: k, deslocamento e ordem de chegada
	EntradasExpiradas   int                `json:"entradas_expiradas_na_fila"`
	Erros               []string           `json:"erros,omitempty"`
}

// Fase: uma rajada de N POST.
type Fase struct {
	Post         Quantis `json:"post_ms"`         // duração do POST (cliente)
	PostAteFIX   Quantis `json:"post_ate_fix_ms"` // início do POST → a 1ª mensagem chega ao PnT falso
	TodasNoPnTMs float64 `json:"todas_no_pnt_ms"` // início da rajada → a última 1ª mensagem no PnT
	ConcluidaMs  float64 `json:"concluida_ms"`    // início da rajada → todas protegidas (entradas) / zeradas (saídas)
	Aceitos      int     `json:"aceitos"`
	Concluidas   int     `json:"concluidas"`
}

type Quantis struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

type MsgsSessao struct {
	RecebidasPeloPnTPico  int `json:"recebidas_pelo_pnt_pico"` // mensagens de aplicação do serviço, maior segundo
	EnviadasPeloPnTPico   int `json:"enviadas_pelo_pnt_pico"`
	RecebidasPeloPnTTotal int `json:"recebidas_pelo_pnt_total"`
	EnviadasPeloPnTTotal  int `json:"enviadas_pelo_pnt_total"`
}

type Recursos struct {
	CPUMediaNucleos float64 `json:"cpu_media_nucleos"` // na janela das rajadas
	CPUPicoNucleos  float64 `json:"cpu_pico_nucleos"`  // maior intervalo de 200 ms
	CPUSegundos     float64 `json:"cpu_segundos"`
	RSSMaxMB        float64 `json:"rss_max_mb"`
}

type CPUSistema struct {
	Nucleos    int     `json:"nucleos"`
	OciosoPct  float64 `json:"ocioso_pct"`
	IOWaitPct  float64 `json:"iowait_pct"`
	UsuarioPct float64 `json:"usuario_pct"`
	SistemaPct float64 `json:"sistema_pct"`
}

func quantis(xs []float64) Quantis {
	if len(xs) == 0 {
		return Quantis{}
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	q := func(p float64) float64 {
		return arred(s[int(math.Min(float64(len(s)-1), math.Ceil(p*float64(len(s)))-1))])
	}
	return Quantis{N: len(s), P50: q(0.5), P95: q(0.95), P99: q(0.99), Max: arred(s[len(s)-1])}
}

func arred(v float64) float64 { return math.Round(v*10) / 10 }

func ms(d time.Duration) float64 { return arred(float64(d) / float64(time.Millisecond)) }

// ---------------------------------------------------------------- rodada

func rodada(t *testing.T, bin, dsn string, n int) Resultado {
	res := Resultado{N: n, Respostas: map[string]int{}}
	if os.Getenv("KALMAN_CARGA_SINAIS") == "1" {
		res.Sinais = map[string]any{}
	}
	dir := t.TempDir()

	// banco: schema de teste limpo, N usuários (conta própria cada um) habilitados
	r, err := repo.AbrirMariaDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.LimparTeste(); err != nil {
		t.Fatal(err)
	}
	if err := r.Migrar(); err != nil {
		t.Fatal(err)
	}
	if err := r.PrepararUsuariosTeste(); err != nil {
		t.Fatal(err)
	}
	contas := make([]string, n)
	for i := 0; i < n; i++ {
		uid := uidBase + i
		contas[i] = strconv.Itoa(200000 + i)
		cfg := fmt.Sprintf(`{"AmbienteDef":"BTG_Simulador","Robo":{"CORSeqId":85,"CCONroConta":"%s"}}`, contas[i])
		if err := r.DefinirUsuario(uid, "hml", cfg); err != nil {
			t.Fatal(err)
		}
		if err := r.GravarHabilitacao(dominio.Habilitacao{UID: uid, Habilitado: true, Por: "carga", Motivo: "carga", Em: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	r.Fechar()

	// preço
	precos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"WINFUT":{"ticker":%q,"preco":130000,"bid":130000,"ask":130005,"status84":0,"ts":%q,"idade_seg":0.1}}`,
			simbolo, time.Now().Format(time.RFC3339))
	}))
	defer precos.Close()

	// PnT falso + respondedor
	pnt, err := falso.Novo(falso.Config{Contraparte: "KALMAN", Dir: filepath.Join(dir, "falso")})
	if err != nil {
		t.Fatal(err)
	}
	if err := pnt.Iniciar(); err != nil {
		t.Fatal(err)
	}
	defer pnt.Parar()
	resp := novoRespondedor(pnt)
	go resp.rodar()
	defer resp.parar()

	// serviço (processo próprio)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endereco := ln.Addr().String()
	ln.Close()
	env := append(os.Environ(),
		"DB_DSN="+dsn, "ACUSUARIO_TABELA=acusuario_teste", "ORDENS_AMBIENTE=HML", "ORDENS_CONTAS_HML="+strings.Join(contas, ","),
		"ORDENS_SENDER_LOCATION=127.0.0.1",
		"FIX_HOST=127.0.0.1", "FIX_PORTA="+strconv.Itoa(pnt.Porta()), "FIX_SENDER_COMP_ID=KALMAN", "FIX_TARGET_COMP_ID=PNT_DSA",
		"FIX_DIR="+filepath.Join(dir, "fix"), "FIX_INICIO=00:00:00", "FIX_FIM=00:00:00", "FIX_HEARTBEAT=30", "FIX_RECONEXAO_S=1",
		"HTTP_ENDERECO="+endereco, "PRECOS_URL="+precos.URL, "TELEGRAM_TOKEN=", "TELEGRAM_CHAT=",
		// limites abertos: o teste mede a capacidade, não os limites de risco
		"LIMITE_POSICAO_MAX=1000000", "LIMITE_QTD_ORDEM=1", "LIMITE_INTERVALO_ENTRADAS_S=0", "LIMITE_ENTRADAS_MIN=1000000",
		"LIMITE_MENSAGENS_MIN=100000000", "LIMITE_ENTRADAS_DIA=1000000", "LIMITE_PERDA_DIA_PTS=1000000000", "LIMITE_RECUSAS_PARADA=1000000",
		"JANELA_INICIO=00:00:00", "JANELA_FIM=23:59:59", "HORA_ZERAGEM=23:59:59", "DEBUG_PPROF=1",
	)
	// KALMAN_CARGA_TAXA: o limitador de taxa da sessão (v1.10); vazio = o padrão do serviço
	if v := os.Getenv("KALMAN_CARGA_TAXA"); v != "" {
		env = append(env, "FIX_TAXA_MSG_S="+v, "FIX_RAJADA_MSG="+v)
	}
	if v := os.Getenv("KALMAN_CARGA_STORE_SYNC"); v != "" {
		env = append(env, "FIX_STORE_SYNC="+v)
	}
	logArq, err := os.Create(filepath.Join(dir, "servico.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logArq.Close()
	cmd := exec.Command(bin, "servir")
	cmd.Env, cmd.Stdout, cmd.Stderr = env, logArq, logArq
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()
	base := "http://" + endereco
	cli := &http.Client{Timeout: 120 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: n + 10, MaxConnsPerHost: 0}}

	// logon e conciliação (um AF por conta)
	t0 := time.Now()
	if err := esperar(func() bool {
		s := saude(cli, base)
		return s != nil && s["sessao_logada"] == true && s["conciliando"] == false
	}, 5*time.Minute); err != nil {
		t.Fatalf("logon/conciliação: %v (log em %s)", err, logArq.Name())
	}
	res.ConciliacaoLogonMs = ms(time.Since(t0))
	res.AFsNoLogon = resp.afs.Load()

	amostra := novoAmostrador(cmd.Process.Pid)
	go amostra.rodar()

	// perfil de CPU do serviço durante a rajada de entradas (KALMAN_CARGA_PERFIL=diretório)
	if dirPerfil := os.Getenv("KALMAN_CARGA_PERFIL"); dirPerfil != "" {
		go func() {
			r, err := cli.Get(base + "/debug/pprof/profile?seconds=8")
			if err != nil {
				return
			}
			defer r.Body.Close()
			if f, err := os.Create(filepath.Join(dirPerfil, fmt.Sprintf("cpu-%d.pprof", n))); err == nil {
				_, _ = io.Copy(f, r.Body)
				f.Close()
			}
		}()
	}
	// --- rajada de entradas
	// concluída = todas as N protegidas: o motor processa também os pedidos que voltaram 503
	// (v1.10 P5: com a taxa padrão, parte das entradas expira na fila — conta como resolvida)
	var expiradas float64
	ultimaLeitura := time.Time{}
	res.Entradas = rajada(t, cli, base, n, "ENTRADA", resp, res.Respostas, res.Sinais, func() bool {
		if resp.protegidas.Load() >= int64(n) {
			return true
		}
		if time.Since(ultimaLeitura) > 500*time.Millisecond {
			ultimaLeitura, expiradas = time.Now(), contador(cli, base, "kalman_ordens_entradas_expiradas_fila_total")
		}
		return resp.protegidas.Load()+int64(expiradas) >= int64(n)
	})
	res.Entradas.Concluidas = int(resp.protegidas.Load())
	res.EntradasExpiradas = int(contador(cli, base, "kalman_ordens_entradas_expiradas_fila_total"))
	time.Sleep(2 * time.Second)

	// --- rajada de saídas (das posições abertas; as expiradas recebem SEM_POSICAO)
	abertas := resp.protegidas.Load()
	res.Saidas = rajada(t, cli, base, n, "SAIDA", resp, res.Respostas, res.Sinais, func() bool {
		return resp.zeradas.Load() >= abertas
	})
	res.Saidas.Concluidas = int(resp.zeradas.Load())
	time.Sleep(1 * time.Second)
	amostra.parar()

	res.ProtecaoFillAte1014 = quantis(resp.fillAte1014())
	res.SaidaCancelAte1031 = quantis(resp.cancelAte1031())
	res.MsgsPorSegundo = resp.picos()
	res.BacklogFalsoMax = int(resp.backlogMax.Load())
	res.Servico, res.MariaDB, res.Sistema = amostra.resultado()
	coletarMetricas(cli, base, &res)
	if s := saude(cli, base); s != nil && s["posicao"] != float64(0) {
		res.Erros = append(res.Erros, fmt.Sprintf("posição final %v", s["posicao"]))
	}
	// o log do serviço fica ao lado do JSON (KALMAN_CARGA_LOG=1)
	if os.Getenv("KALMAN_CARGA_LOG") == "1" {
		if b, err := os.ReadFile(logArq.Name()); err == nil {
			_ = os.WriteFile(filepath.Join(os.Getenv("KALMAN_CARGA_SAIDA"), fmt.Sprintf("servico-%d%s.log", n, os.Getenv("KALMAN_CARGA_SUFIXO"))), b, 0o644)
		}
	}
	return res
}

// rajada: N POST no mesmo instante (cada um na sua goroutine, liberados juntos), depois espera
// `pronto` (até 5 min). Com sinais != nil, um POST /sinais por parte de 1.000 (todas juntas).
func rajada(t *testing.T, cli *http.Client, base string, n int, papel string, resp *respondedor, respostas map[string]int, sinais map[string]any, pronto func() bool) Fase {
	var f Fase
	durs := make([]float64, n)
	inicios := make([]time.Time, n)
	codigos := make([]string, n)
	var wg sync.WaitGroup
	largada := make(chan struct{})
	if sinais != nil {
		lancarSinal(cli, base, n, papel, &wg, largada, durs, inicios, codigos, sinais)
	}
	for i := 0; i < n && sinais == nil; i++ {
		corpo := fmt.Sprintf(`{"operacao_id":%d,"uid":%d,"papel":%q,"lado":"C","qtd":1,"stop_pts":500,"motivo":"SINAL","ts_decisao":"carga"}`,
			opBase+i, uidBase+i, papel)
		if papel == "SAIDA" {
			corpo = fmt.Sprintf(`{"operacao_id":%d,"uid":%d,"papel":"SAIDA","lado":"V","qtd":1,"motivo":"SINAL_OPOSTO","ts_decisao":"carga-s"}`,
				opBase+i, uidBase+i)
		}
		wg.Add(1)
		go func(i int, corpo string) {
			defer wg.Done()
			<-largada
			inicios[i] = time.Now()
			r, err := cli.Post(base+"/ordens", "application/json", strings.NewReader(corpo))
			durs[i] = ms(time.Since(inicios[i]))
			if err != nil {
				codigos[i] = "erro:" + err.Error()
				return
			}
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			r.Body.Close()
			c := fmt.Sprintf("%d", r.StatusCode)
			if v["aceito"] == true {
				c += " aceito"
			} else {
				c += fmt.Sprintf(" %v", v["codigo"])
			}
			codigos[i] = c
		}(i, corpo)
	}
	t0 := time.Now()
	resp.marcarRajada(papel, t0)
	close(largada)
	wg.Wait()
	for _, c := range codigos {
		respostas[papel+" "+c]++
		if strings.HasSuffix(c, "aceito") {
			f.Aceitos++
		}
	}
	if papel == "ENTRADA" {
		resp.definirEntradasAceitas(f.Aceitos)
	}
	if err := esperar(pronto, 5*time.Minute); err != nil {
		t.Errorf("%s: não concluiu em 5 min", papel)
	}
	f.ConcluidaMs = ms(time.Since(t0))
	f.Post = quantis(durs)
	var ateFIX []float64
	ultima := t0
	for i := 0; i < n; i++ {
		if c := resp.primeira(papel, int64(opBase+i)); !c.IsZero() {
			ateFIX = append(ateFIX, ms(c.Sub(inicios[i])))
			if c.After(ultima) {
				ultima = c
			}
		}
	}
	f.PostAteFIX = quantis(ateFIX)
	f.TodasNoPnTMs = ms(ultima.Sub(t0))
	if sinais != nil {
		conferirRajada(papel, resp, sinais, codigos)
	}
	return f
}

const itensPorParte = 1000

// lancarSinal: o sinal de N itens em partes de 1.000, cada parte na sua goroutine (liberadas
// juntas pela largada). A duração e o código de cada item são os da parte dele.
func lancarSinal(cli *http.Client, base string, n int, papel string, wg *sync.WaitGroup, largada chan struct{}, durs []float64, inicios []time.Time, codigos []string, sinais map[string]any) {
	partes := (n + itensPorParte - 1) / itensPorParte
	id := fmt.Sprintf("CARGA-%s-%d", papel, n)
	var mu sync.Mutex
	for p := 0; p < partes; p++ {
		de, ate := p*itensPorParte, min(n, (p+1)*itensPorParte)
		var itens []string
		for i := de; i < ate; i++ {
			if papel == "SAIDA" {
				itens = append(itens, fmt.Sprintf(`{"operacao_id":%d,"uid":%d,"lado":"V","motivo":"SINAL_OPOSTO"}`, opBase+i, uidBase+i))
			} else {
				itens = append(itens, fmt.Sprintf(`{"operacao_id":%d,"uid":%d,"lado":"C","qtd":1,"stop_pts":500,"motivo":"SINAL"}`, opBase+i, uidBase+i))
			}
		}
		lista := "[" + strings.Join(itens, ",") + "]"
		ent, sai := lista, "[]"
		if papel == "SAIDA" {
			ent, sai = "[]", lista
		}
		corpo := fmt.Sprintf(`{"sinal_id":%q,"ts_decisao":"carga","parte":%d,"partes":%d,"entradas":%s,"saidas":%s}`, id, p+1, partes, ent, sai)
		wg.Add(1)
		go func(de, ate int, corpo string) {
			defer wg.Done()
			<-largada
			t0 := time.Now()
			r, err := cli.Post(base+"/sinais", "application/json", strings.NewReader(corpo))
			d := ms(time.Since(t0))
			for i := de; i < ate; i++ {
				inicios[i], durs[i] = t0, d
			}
			if err != nil {
				for i := de; i < ate; i++ {
					codigos[i] = "erro:" + err.Error()
				}
				return
			}
			var v struct {
				Codigo       any              `json:"codigo"`
				Fechado      bool             `json:"fechado"`
				K            any              `json:"k"`
				Deslocamento any              `json:"deslocamento"`
				Resultados   []map[string]any `json:"resultados"`
			}
			_ = json.NewDecoder(r.Body).Decode(&v)
			r.Body.Close()
			if v.Fechado {
				mu.Lock()
				sinais[papel+"_k"], sinais[papel+"_deslocamento"] = v.K, v.Deslocamento
				mu.Unlock()
			}
			for j, i := 0, de; i < ate; i, j = i+1, j+1 {
				c := fmt.Sprintf("%d %v", r.StatusCode, v.Codigo)
				if j < len(v.Resultados) {
					if v.Resultados[j]["aceito"] == true {
						c = fmt.Sprintf("%d aceito", r.StatusCode)
					} else {
						c = fmt.Sprintf("%d %v", r.StatusCode, v.Resultados[j]["codigo"])
					}
				}
				codigos[i] = c
			}
		}(de, ate, corpo)
	}
}

// conferirRajada: a ordem em que as primeiras mensagens chegaram ao PnT falso contra a da rajada
// (as aceitas por uid crescente = i crescente, a partir do deslocamento): fora_de_ordem = chegadas
// com posição menor que a da anterior (com a mesma prioridade e a taxa, devia ser 0; as expiradas
// na fila só faltam).
func conferirRajada(papel string, resp *respondedor, sinais map[string]any, codigos []string) {
	rank := map[int64]int{}
	for i, c := range codigos {
		if strings.HasSuffix(c, "aceito") {
			rank[int64(opBase+i)] = len(rank)
		}
	}
	n := len(rank)
	d, ok := sinais[papel+"_deslocamento"].(float64)
	if !ok {
		sinais[papel+"_erro"] = "sem deslocamento (o sinal não fechou numa resposta)"
		return
	}
	resp.mu.Lock()
	chegada := append([]int64(nil), resp.ordemChegada[papel]...)
	resp.mu.Unlock()
	fora, ant := 0, -1
	for _, op := range chegada {
		r, ok := rank[op]
		if !ok || n == 0 {
			fora++
			continue
		}
		pos := ((r-int(d))%n + n) % n
		if pos < ant {
			fora++
		}
		ant = pos
	}
	sinais[papel+"_chegadas"], sinais[papel+"_fora_de_ordem"] = len(chegada), fora
	if len(chegada) > 0 {
		sinais[papel+"_primeira_op_i"] = chegada[0] - opBase
	}
}

// contador: o valor de uma série sem rótulos do /metrics (0 se ausente).
func contador(cli *http.Client, base, nome string) float64 {
	r, err := cli.Get(base + "/metrics")
	if err != nil {
		return 0
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, nome+" "); ok {
			f, _ := strconv.ParseFloat(v, 64)
			return f
		}
	}
	return 0
}

func esperar(f func() bool, limite time.Duration) error {
	fim := time.Now().Add(limite)
	for time.Now().Before(fim) {
		if f() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("tempo esgotado (%s)", limite)
}

func saude(cli *http.Client, base string) map[string]any {
	r, err := cli.Get(base + "/saude")
	if err != nil {
		return nil
	}
	defer r.Body.Close()
	var v map[string]any
	if json.NewDecoder(r.Body).Decode(&v) != nil {
		return nil
	}
	return v
}

// ---------------------------------------------------------------- respondedor (o PnT)

type ordemFalsa struct {
	id37, cl, lado, estrat string
	conta                  string
	qtd                    string
	preco                  string
	stop, lim              string
	executada, encerrada   bool
}

type respondedor struct {
	pnt        *falso.Falso
	fim        chan struct{}
	mu         sync.Mutex
	ordens     map[string]*ordemFalsa // por ClOrdID (base e atual)
	todas      []*ordemFalsa          // em ordem de criação (mass status)
	seq        int
	afs        atomic.Int64
	protegidas atomic.Int64
	zeradas    atomic.Int64
	aceitasEnt atomic.Int64
	backlogMax atomic.Int64
	// tempos por operação
	rajadaEm       map[string]time.Time
	primeiraMsg    map[string]map[int64]time.Time // papel → op → 1ª mensagem recebida do serviço
	fillEntrada    map[int64]time.Time
	d1014          map[int64]time.Time
	cancel4        map[int64]time.Time
	dSaida         map[int64]time.Time
	recebidasSeg   map[int64]int
	enviadasSeg    map[int64]int
	totRec, totEnv int
	zeradasOp      map[int64]bool
	ordemChegada   map[string][]int64 // papel → operações na ordem da 1ª mensagem recebida
}

func novoRespondedor(p *falso.Falso) *respondedor {
	return &respondedor{pnt: p, fim: make(chan struct{}), ordens: map[string]*ordemFalsa{}, rajadaEm: map[string]time.Time{},
		primeiraMsg: map[string]map[int64]time.Time{"ENTRADA": {}, "SAIDA": {}}, fillEntrada: map[int64]time.Time{},
		d1014: map[int64]time.Time{}, cancel4: map[int64]time.Time{}, dSaida: map[int64]time.Time{},
		recebidasSeg: map[int64]int{}, enviadasSeg: map[int64]int{}, zeradasOp: map[int64]bool{}, ordemChegada: map[string][]int64{}}
}

func (r *respondedor) parar() { close(r.fim) }

func (r *respondedor) marcarRajada(papel string, t time.Time) {
	r.mu.Lock()
	r.rajadaEm[papel] = t
	r.mu.Unlock()
}

func (r *respondedor) definirEntradasAceitas(n int) { r.aceitasEnt.Store(int64(n)) }
func (r *respondedor) entradasAceitas() int64       { return r.aceitasEnt.Load() }

func (r *respondedor) primeira(papel string, op int64) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.primeiraMsg[papel][op]
}

// opDoCl: KM-<papel>-<op>-<n>.
func opDoCl(cl string) (string, int64) {
	p := strings.Split(cl, "-")
	if len(p) != 4 {
		return "", 0
	}
	op, _ := strconv.ParseInt(p[2], 10, 64)
	return p[1], op
}

func (r *respondedor) rodar() {
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-r.fim:
				return
			case <-t.C:
				if b := int64(len(r.pnt.Pendentes())); b > r.backlogMax.Load() {
					r.backlogMax.Store(b)
				}
			}
		}
	}()
	for {
		select {
		case <-r.fim:
			return
		default:
		}
		m, err := r.pnt.Proxima(200 * time.Millisecond)
		if err != nil {
			continue
		}
		r.tratar(m)
	}
}

func (r *respondedor) enviar(campos ...falso.Campo) {
	if err := r.pnt.Enviar(campos); err == nil {
		r.mu.Lock()
		r.enviadasSeg[time.Now().Unix()]++
		r.totEnv++
		r.mu.Unlock()
	}
}

func c(tag int, v string) falso.Campo { return falso.Campo{Tag: tag, Valor: v} }

func (r *respondedor) er(o *ordemFalsa, cl, orig, et, st, cum, avg string, extra ...falso.Campo) {
	campos := []falso.Campo{c(35, "8"), c(1, o.conta), c(55, simbolo), c(207, "XBMF"), c(40, "2"), c(59, "0"), c(11, cl)}
	if orig != "" {
		campos = append(campos, c(41, orig))
	}
	campos = append(campos, c(37, o.id37), c(150, et), c(39, st), c(54, o.lado), c(38, o.qtd), c(44, o.preco), c(847, o.estrat), c(14, cum))
	leaves := "1"
	if st == "2" || st == "4" {
		leaves = "0"
	}
	campos = append(campos, c(151, leaves), c(6, avg))
	campos = append(campos, extra...)
	r.enviar(campos...)
}

func (r *respondedor) tratar(m falso.Recebida) {
	agora := time.Now()
	v := func(t int) string { s, _ := m.Valor(t); return s }
	r.mu.Lock()
	r.recebidasSeg[agora.Unix()]++
	r.totRec++
	r.mu.Unlock()
	switch m.Tipo {
	case "AF":
		// como o PnT: todas as ordens do dia da conta, com o estado atual; sem ordens, 35=j 380=101
		r.afs.Add(1)
		r.mu.Lock()
		var daConta []*ordemFalsa
		for _, o := range r.todas {
			if o.conta == v(1) {
				daConta = append(daConta, o)
			}
		}
		estados := make([][3]string, len(daConta)) // 39, 14, 6
		for i, o := range daConta {
			switch {
			case o.executada:
				estados[i] = [3]string{"2", o.qtd, o.preco}
			case o.encerrada:
				estados[i] = [3]string{"4", "0", "0"}
			default:
				estados[i] = [3]string{"0", "0", "0"}
			}
		}
		r.mu.Unlock()
		if len(daConta) == 0 {
			r.enviar(c(35, "j"), c(372, "AF"), c(379, v(584)), c(380, "101"), c(58, "No orders found."))
			return
		}
		for i, o := range daConta {
			ultima := "N"
			if i == len(daConta)-1 {
				ultima = "Y"
			}
			r.er(o, o.cl, "", "I", estados[i][0], estados[i][1], estados[i][2], c(584, v(584)), c(911, strconv.Itoa(len(daConta))), c(912, ultima))
		}
	case "D":
		cl := v(11)
		papel, op := opDoCl(cl)
		r.mu.Lock()
		r.seq++
		o := &ordemFalsa{id37: fmt.Sprintf("dsa.%d", r.seq), cl: cl, conta: v(1), lado: v(54), estrat: v(847), qtd: v(38), preco: v(44), stop: v(20828), lim: v(20829)}
		r.ordens[cl] = o
		r.todas = append(r.todas, o)
		switch papel {
		case "E":
			if _, ja := r.primeiraMsg["ENTRADA"][op]; !ja {
				r.primeiraMsg["ENTRADA"][op] = agora
				r.ordemChegada["ENTRADA"] = append(r.ordemChegada["ENTRADA"], op)
			}
		case "P":
			if _, ja := r.d1014[op]; !ja {
				r.d1014[op] = agora
				r.protegidas.Add(1) // operações com 1014 (a recriada não conta de novo)
			}
		case "S":
			r.dSaida[op] = agora
		}
		r.mu.Unlock()
		if o.estrat == "1014" {
			r.er(o, cl, "", "0", "0", "0", "0", c(20828, o.stop), c(20829, o.lim))
			return
		}
		r.er(o, cl, "", "0", "0", "0", "0")
		px := o.preco
		r.er(o, cl, "", "F", "2", o.qtd, px, c(32, o.qtd), c(31, px))
		r.mu.Lock()
		o.executada, o.encerrada = true, true
		if papel == "E" {
			r.fillEntrada[op] = time.Now()
		}
		r.mu.Unlock()
		if papel == "S" {
			r.mu.Lock()
			if !r.zeradasOp[op] {
				r.zeradasOp[op] = true
				r.zeradas.Add(1)
			}
			r.mu.Unlock()
		}
	case "F":
		cl, orig := v(11), v(41)
		papel, op := opDoCl(orig)
		r.mu.Lock()
		o := r.ordens[orig]
		if papel == "P" {
			if _, ja := r.primeiraMsg["SAIDA"][op]; !ja {
				r.primeiraMsg["SAIDA"][op] = agora
				r.ordemChegada["SAIDA"] = append(r.ordemChegada["SAIDA"], op)
			}
		}
		r.mu.Unlock()
		if o == nil {
			return
		}
		if o.encerrada {
			// já executada: o cancel é recusado (35=9 com o 39 terminal)
			r.enviar(c(35, "9"), c(11, cl), c(41, orig), c(37, o.id37), c(39, "2"), c(434, "1"), c(58, "order already filled"))
			return
		}
		r.mu.Lock()
		o.encerrada = true
		r.ordens[cl] = o
		r.cancel4[op] = time.Now()
		r.mu.Unlock()
		r.er(o, cl, orig, "4", "4", "0", "0")
	case "G":
		cl, orig := v(11), v(41)
		r.mu.Lock()
		o := r.ordens[orig]
		if o != nil {
			r.ordens[cl] = o
		}
		r.mu.Unlock()
		if o != nil {
			o.stop, o.lim, o.preco = v(20828), v(20829), v(44)
			r.er(o, cl, orig, "5", "5", "0", "0")
		}
	}
}

func (r *respondedor) fillAte1014() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var xs []float64
	for op, f := range r.fillEntrada {
		if d, ok := r.d1014[op]; ok {
			xs = append(xs, ms(d.Sub(f)))
		}
	}
	return xs
}

func (r *respondedor) cancelAte1031() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var xs []float64
	for op, f := range r.cancel4 {
		if d, ok := r.dSaida[op]; ok && d.After(f) {
			xs = append(xs, ms(d.Sub(f)))
		}
	}
	return xs
}

func (r *respondedor) picos() MsgsSessao {
	r.mu.Lock()
	defer r.mu.Unlock()
	var m MsgsSessao
	for _, v := range r.recebidasSeg {
		m.RecebidasPeloPnTPico = max(m.RecebidasPeloPnTPico, v)
	}
	for _, v := range r.enviadasSeg {
		m.EnviadasPeloPnTPico = max(m.EnviadasPeloPnTPico, v)
	}
	m.RecebidasPeloPnTTotal, m.EnviadasPeloPnTTotal = r.totRec, r.totEnv
	return m
}

// ---------------------------------------------------------------- CPU e memória

type amostrador struct {
	pid, pidDB                     int
	fim                            chan struct{}
	pronto                         chan struct{}
	mu                             sync.Mutex
	inicio                         time.Time
	cpu0, cpuDB0, cpuAnt, cpuDBAnt float64
	picoSvc, picoDB                float64
	rssMax, rssDBMax               float64
	sis0, sis1                     [4]float64 // user, system, idle, iowait (ticks)
	fimEm                          time.Time
	cpu1, cpuDB1                   float64
}

func novoAmostrador(pid int) *amostrador {
	a := &amostrador{pid: pid, fim: make(chan struct{}), pronto: make(chan struct{})}
	if out, err := exec.Command("pgrep", "-x", "mariadbd").Output(); err == nil {
		a.pidDB, _ = strconv.Atoi(strings.Fields(string(out))[0])
	}
	return a
}

const ticks = 100.0 // USER_HZ

func cpuProc(pid int) (float64, float64) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0
	}
	s := string(b)
	campos := strings.Fields(s[strings.LastIndex(s, ")")+2:])
	u, _ := strconv.ParseFloat(campos[11], 64)
	st, _ := strconv.ParseFloat(campos[12], 64)
	rss := 0.0
	if st2, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(st2))
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "VmRSS:") {
				kb, _ := strconv.ParseFloat(strings.Fields(sc.Text())[1], 64)
				rss = kb / 1024
			}
		}
	}
	return (u + st) / ticks, rss
}

func cpuSistema() [4]float64 {
	b, _ := os.ReadFile("/proc/stat")
	l := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	var v [4]float64
	idx := map[int]int{1: 0, 3: 1, 4: 2, 5: 3} // user, system, idle, iowait
	for i, j := range idx {
		x, _ := strconv.ParseFloat(l[i], 64)
		v[j] = x
	}
	nice, _ := strconv.ParseFloat(l[2], 64)
	v[0] += nice
	return v
}

func (a *amostrador) rodar() {
	defer close(a.pronto)
	a.inicio = time.Now()
	a.cpu0, _ = cpuProc(a.pid)
	if a.pidDB != 0 {
		a.cpuDB0, _ = cpuProc(a.pidDB)
	}
	a.cpuAnt, a.cpuDBAnt = a.cpu0, a.cpuDB0
	a.sis0 = cpuSistema()
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-a.fim:
			a.fimEm = time.Now()
			a.cpu1, _ = cpuProc(a.pid)
			if a.pidDB != 0 {
				a.cpuDB1, _ = cpuProc(a.pidDB)
			}
			a.sis1 = cpuSistema()
			return
		case <-t.C:
			c, rss := cpuProc(a.pid)
			a.picoSvc = math.Max(a.picoSvc, (c-a.cpuAnt)/0.2)
			a.rssMax = math.Max(a.rssMax, rss)
			a.cpuAnt = c
			if a.pidDB != 0 {
				c, rss := cpuProc(a.pidDB)
				a.picoDB = math.Max(a.picoDB, (c-a.cpuDBAnt)/0.2)
				a.rssDBMax = math.Max(a.rssDBMax, rss)
				a.cpuDBAnt = c
			}
		}
	}
}

func (a *amostrador) parar() { close(a.fim); <-a.pronto }

func (a *amostrador) resultado() (Recursos, Recursos, CPUSistema) {
	dur := a.fimEm.Sub(a.inicio).Seconds()
	svc := Recursos{CPUSegundos: arred(a.cpu1 - a.cpu0), CPUMediaNucleos: arred2((a.cpu1 - a.cpu0) / dur), CPUPicoNucleos: arred2(a.picoSvc), RSSMaxMB: arred(a.rssMax)}
	db := Recursos{CPUSegundos: arred(a.cpuDB1 - a.cpuDB0), CPUMediaNucleos: arred2((a.cpuDB1 - a.cpuDB0) / dur), CPUPicoNucleos: arred2(a.picoDB), RSSMaxMB: arred(a.rssDBMax)}
	var d [4]float64
	tot := 0.0
	for i := range d {
		d[i] = a.sis1[i] - a.sis0[i]
		tot += d[i]
	}
	pct := func(x float64) float64 { return arred(100 * x / tot) }
	return svc, db, CPUSistema{Nucleos: numCPU(), UsuarioPct: pct(d[0]), SistemaPct: pct(d[1]), OciosoPct: pct(d[2]), IOWaitPct: pct(d[3])}
}

func arred2(v float64) float64 { return math.Round(v*100) / 100 }

func numCPU() int {
	b, _ := os.ReadFile("/proc/cpuinfo")
	return strings.Count(string(b), "processor\t:")
}

// ---------------------------------------------------------------- /metrics

func coletarMetricas(cli *http.Client, base string, res *Resultado) {
	r, err := cli.Get(base + "/metrics")
	if err != nil {
		res.Erros = append(res.Erros, "metrics: "+err.Error())
		return
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	type hist struct {
		baldes []float64
		cont   []float64
		n      float64
		soma   float64
	}
	hs := map[string]*hist{}
	res.Alertas, res.Recusas, res.Metricas, res.BancoPorOperacao = map[string]float64{}, map[string]float64{}, map[string]Quantis{}, map[string]Quantis{}
	for _, l := range strings.Split(string(b), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		i := strings.LastIndex(l, " ")
		serie, vt := l[:i], l[i+1:]
		v, _ := strconv.ParseFloat(vt, 64)
		nome, rot, _ := strings.Cut(serie, "{")
		rot = strings.TrimSuffix(rot, "}")
		le := ""
		var outros []string
		for _, p := range strings.Split(rot, "\",") {
			k, x, ok := strings.Cut(p, "=")
			if !ok {
				continue
			}
			x = strings.Trim(x, "\"")
			if k == "le" {
				le = x
			} else {
				outros = append(outros, k+"="+x)
			}
		}
		chave := strings.TrimPrefix(nome, "kalman_ordens_")
		chave = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(chave, "_bucket"), "_sum"), "_count")
		if len(outros) > 0 {
			chave += "{" + strings.Join(outros, ",") + "}"
		}
		switch {
		case strings.HasSuffix(nome, "_bucket"):
			h := hs[chave]
			if h == nil {
				h = &hist{}
				hs[chave] = h
			}
			if le != "+Inf" {
				x, _ := strconv.ParseFloat(le, 64)
				h.baldes = append(h.baldes, x)
				h.cont = append(h.cont, v)
			}
		case strings.HasSuffix(nome, "_count") && hs[chave] != nil:
			hs[chave].n = v
		case strings.HasSuffix(nome, "_sum") && hs[chave] != nil:
			hs[chave].soma = v
		case nome == "kalman_ordens_alertas_total":
			res.Alertas[strings.Join(outros, ",")] = v
		case nome == "kalman_ordens_recusas_total":
			res.Recusas[strings.Join(outros, ",")] = v
		case nome == "kalman_ordens_fila_tamanho":
			res.FilaMax = v
		}
	}
	for k, h := range hs {
		if h.n == 0 {
			continue
		}
		q := func(p float64) float64 {
			alvo := p * h.n
			for i, c := range h.cont {
				if c >= alvo {
					return arred(h.baldes[i] * 1000)
				}
			}
			return -1 // além do último balde
		}
		qq := Quantis{N: int(h.n), P50: q(0.5), P95: q(0.95), P99: q(0.99), Max: arred(h.soma / h.n * 1000)} // Max aqui = média
		if strings.HasPrefix(k, "banco_segundos") {
			res.BancoTotalSeg += h.soma
			res.BancoPorOperacao[strings.TrimPrefix(k, "banco_segundos")] = qq
			continue
		}
		res.Metricas[k] = qq
	}
	res.BancoTotalSeg = arred(res.BancoTotalSeg)
}
