package motor

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// registro: commits do lote e envios ao PnT, na ordem em que aconteceram.
type registro struct {
	mu sync.Mutex
	l  []string
}

func (r *registro) add(s string) { r.mu.Lock(); r.l = append(r.l, s); r.mu.Unlock() }
func (r *registro) lista() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.l...)
}

type envioTeste struct {
	reg *registro
	seq int
}

func (e *envioTeste) Enviar(m *quickfix.Message) (int, error) {
	tipo, _ := m.Header.GetString(quickfix.Tag(35))
	cl, _ := m.Body.GetString(quickfix.Tag(11))
	e.seq++
	e.reg.add("envio " + tipo + " " + cl)
	return e.seq, nil
}
func (e *envioTeste) Logado() bool { return true }

type repoTeste struct {
	*repo.Memoria
	reg *registro
}

func (r repoTeste) GravarLote(l *repo.Lote) error {
	r.reg.add(fmt.Sprintf("commit %d", l.Linhas()))
	return r.Memoria.GravarLote(l)
}

type notifTeste struct{}

func (notifTeste) Notificar(string, string) {}

func novoMotorTeste(t *testing.T, extra map[string]string) (*Motor, *registro, *repo.Memoria, *relogio.Controlado) {
	t.Helper()
	reg := &registro{}
	mem := repo.NovaMemoria()
	_ = mem.DefinirUsuario(7, "hml", `{"Robo":{"CORSeqId":85,"CCONroConta":"12345"}}`)
	_ = mem.GravarHabilitacao(dominio.Habilitacao{UID: 7, Habilitado: true, Por: "teste", Em: time.Now()})
	rel := relogio.NovoControlado(time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC)) // 10:00 em Brasília
	pr := preco.NovaFalsa(rel)
	for _, kv := range [][2]string{{"ticker", "WINZ26"}, {"preco", "130000"}, {"bid", "130000"}, {"ask", "130005"}, {"status84", "0"}, {"idade", "100ms"}} {
		if err := pr.Definir(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	// limites por uid (v1.10 L) desligados: os testes mandam várias entradas do mesmo uid
	v := map[string]string{"ORDENS_AMBIENTE": "HML", "ORDENS_CONTAS_HML": "12345", "LIMITE_ENTRADAS_MIN_UID": "0", "LIMITE_INTERVALO_UID_S": "0"}
	for k, x := range extra {
		v[k] = x
	}
	m := Novo(config.Novo(v), rel, repoTeste{mem, reg}, pr, notifTeste{})
	m.Usar(&envioTeste{reg: reg})
	if err := m.Iniciar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Parar)
	return m, reg, mem, rel
}

func ptr[T any](v T) *T { return &v }

// v1.10 B: "gravar antes de enviar" — a 1031 só sai depois do commit do lote que tem a ordem.
func TestEnvioDepoisDoCommit(t *testing.T) {
	m, reg, mem, _ := novoMotorTeste(t, nil)
	r := m.Pedir(Pedido{OperacaoID: ptr(int64(1)), UID: ptr(7), Papel: "ENTRADA", Lado: "C", Qtd: ptr(1), StopPts: ptr(int64(500)), Motivo: "SINAL", TsDecisao: "t"})
	if r["aceito"] != true {
		t.Fatalf("entrada: %v", r)
	}
	l := reg.lista()
	i := indice(l, "envio D KM-E-1-1")
	if i < 1 || !strings.HasPrefix(l[i-1], "commit") {
		t.Fatalf("a 1031 tem de sair logo depois de um commit: %v", l)
	}
	// e a ordem estava no banco quando saiu
	_, ords, _ := mem.CarregarDia("2026-10-15")
	if len(ords) != 1 || ords[0].ClOrdBase != "KM-E-1-1" || ords[0].ID == 0 {
		t.Fatalf("ordem gravada: %+v", ords)
	}
}

func indice(l []string, s string) int {
	for i, x := range l {
		if x == s {
			return i
		}
	}
	return -1
}

func msgTeste(cl string) *quickfix.Message {
	q := quickfix.NewMessage()
	q.Header.SetString(quickfix.Tag(35), "D")
	q.Body.SetString(quickfix.Tag(11), cl)
	return q
}

// v1.10 P2: dentro da volta, o envio sai por prioridade (1014 > cancel/saída > entrada), mantendo a
// ordem de decisão dentro da classe.
func TestEnvioPorPrioridade(t *testing.T) {
	m, reg, _, _ := novoMotorTeste(t, nil)
	m.Fazer(func() {
		for _, x := range []struct {
			cl   string
			prio int
		}{{"E1", prioEntrada}, {"S1", prioSaida}, {"P1", prioProtecao}, {"E2", prioEntrada}, {"P2", prioProtecao}, {"S2", prioSaida}} {
			m.enfileirarEnvio(envioPendente{msg: msgTeste(x.cl), clordid: x.cl, prio: x.prio})
		}
	})
	var envios []string
	for _, s := range reg.lista() {
		if strings.HasPrefix(s, "envio") {
			envios = append(envios, strings.TrimPrefix(s, "envio D "))
		}
	}
	if strings.Join(envios, ",") != "P1,P2,S1,S2,E1,E2" {
		t.Fatalf("ordem de envio: %v", envios)
	}
}

// v1.10 P1: entrada do motor por prioridade estrita — P0 (PnT, tick) antes de P1 (saídas) antes de
// P2 (entradas), qualquer que seja a ordem de chegada.
func TestFilasPorPrioridade(t *testing.T) {
	m, _, _, _ := novoMotorTeste(t, nil)
	trava := make(chan struct{})
	m.Enfileirar(func() { <-trava })
	var ordem []string
	var mu sync.Mutex
	marca := func(s string) func() { return func() { mu.Lock(); ordem = append(ordem, s); mu.Unlock() } }
	m.entrar("http_entrada", marca("entrada1"))
	m.entrar("http_saida", marca("saida1"))
	m.entrar("fix", marca("fix1"))
	m.entrar("http_entrada", marca("entrada2"))
	m.entrar("tick", marca("tick1"))
	m.entrar("http_saida", marca("saida2"))
	close(trava)
	m.Fazer(func() {})
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ordem, ",") != "fix1,tick1,saida1,saida2,entrada1,entrada2" {
		t.Fatalf("ordem de tratamento: %v", ordem)
	}
}

// v1.10 B: habilitação em memória, relida a cada CACHE_USUARIOS_S; a mudança feita direto no banco
// (a CLI) só vale depois da recarga; a rota do admin invalida na hora.
func TestCacheUsuarios(t *testing.T) {
	m, _, mem, rel := novoMotorTeste(t, map[string]string{"CACHE_USUARIOS_S": "30"})
	hab := func() bool {
		var h dominio.Habilitacao
		m.Fazer(func() { h, _, _ = m.habilitacaoDo(7) })
		return h.Habilitado
	}
	if !hab() {
		t.Fatal("uid 7 devia estar habilitado")
	}
	// a CLI desabilita direto no banco: o cache ainda não sabe
	_ = mem.GravarHabilitacao(dominio.Habilitacao{UID: 7, Habilitado: false, Por: "cli", Em: time.Now()})
	if !hab() {
		t.Fatal("antes da recarga o cache devia continuar com a habilitação antiga")
	}
	rel.Avancar(31 * time.Second)
	m.Tick() // dispara a recarga em segundo plano
	fim := time.Now().Add(2 * time.Second)
	for hab() && time.Now().Before(fim) {
		time.Sleep(10 * time.Millisecond)
	}
	if hab() {
		t.Fatal("depois da recarga o uid 7 devia estar desabilitado")
	}
	// a rota do admin: invalida na hora
	_ = mem.GravarHabilitacao(dominio.Habilitacao{UID: 7, Habilitado: true, Por: "cli", Em: time.Now()})
	r := <-m.AlterarHabilitacaoAssincrono(7, HabilitacaoAdmin{Habilitado: ptr(false), Motivo: "x", Por: ptr(1)})
	if r["codigo"] != "BLOQUEADO" || hab() {
		t.Fatalf("admin: %v, habilitado=%v", r, hab())
	}
}

// resultadoDoDia com agregados (v1.10) = a soma direta de realizado + aberto por operação, também
// depois de um fill novo.
func TestResultadoDoDiaAgregado(t *testing.T) {
	m := &Motor{}
	dir := func(ultimo decimal.Decimal) decimal.Decimal {
		r := decimal.Zero
		for _, op := range m.ops {
			r = r.Add(op.pnlRealizado()).Add(op.pnlAberto(ultimo))
		}
		return r
	}
	nova := func(lado dominio.Lado, entradas [][2]string, saidas [][2]string) *operacao {
		op := &operacao{Operacao: dominio.Operacao{Lado: lado}}
		for _, e := range entradas {
			q, _ := strconv.Atoi(e[0])
			op.ordens = append(op.ordens, &ordem{Ordem: dominio.Ordem{Papel: dominio.Entrada, Lado: lado, QtdExec: q, PrecoMedio: decimal.RequireFromString(e[1])}})
		}
		for _, s := range saidas {
			q, _ := strconv.Atoi(s[0])
			op.ordens = append(op.ordens, &ordem{Ordem: dominio.Ordem{Papel: dominio.Saida, Lado: -lado, QtdExec: q, PrecoMedio: decimal.RequireFromString(s[1])}})
		}
		return op
	}
	m.ops = []*operacao{
		nova(dominio.Compra, [][2]string{{"2", "130010"}, {"1", "130025.5"}}, [][2]string{{"1", "130100"}}),
		nova(dominio.Venda, [][2]string{{"1", "129990"}}, nil),
		nova(dominio.Compra, [][2]string{{"1", "130000"}}, [][2]string{{"1", "129800"}}),
		nova(dominio.Venda, nil, nil),
	}
	for _, u := range []string{"130000", "131234.5", "128000"} {
		ultimo := decimal.RequireFromString(u)
		if got, want := m.resultadoDoDia(precoDia{&ultimo, &ultimo}), dir(ultimo); !got.Equal(want) {
			t.Fatalf("último %s: agregado %s, direto %s", u, got, want)
		}
	}
	// um fill muda a posição: a parte da operação sai e volta (como em aplicarFill)
	m.somarContribuicao(m.ops[1], -1)
	m.ops[1].ordens[0].QtdExec = 3
	m.somarContribuicao(m.ops[1], 1)
	ultimo := decimal.RequireFromString("130500")
	if got, want := m.resultadoDoDia(precoDia{&ultimo, &ultimo}), dir(ultimo); !got.Equal(want) {
		t.Fatalf("depois do fill: agregado %s, direto %s", got, want)
	}
	// e bate com o recálculo inteiro
	m.versaoFills++
	if got, want := m.resultadoDoDia(precoDia{&ultimo, &ultimo}), dir(ultimo); !got.Equal(want) {
		t.Fatalf("recalculado: %s, direto %s", got, want)
	}
	if got := m.resultadoDoDia(precoDia{}); !got.Equal(m.ops[0].pnlRealizado().Add(m.ops[2].pnlRealizado())) {
		t.Fatalf("sem preço: %s", got)
	}
	// SPEC-FLEXSCAN-v1: a operação da Flexscan (destino 88) é avaliada pelo preço dela
	m.ops[1].Conector = dominio.ConectorFlexscan
	m.versaoFills++
	pnt, flx := decimal.RequireFromString("130500"), decimal.RequireFromString("131500")
	// = tudo a 130500 + (131500 − 130500) × a posição da operação da Flexscan
	want := dir(pnt).Add(flx.Sub(pnt).Mul(decimal.NewFromInt(int64(m.ops[1].posicao()))))
	if got := m.resultadoDoDia(precoDia{&pnt, &flx}); !got.Equal(want) {
		t.Fatalf("por conector: %s, esperado %s", got, want)
	}
}

// v1.10 §6: deslocamento ⌊k·φ·N⌋ mod N — dentro de [0, N) e espalhado (k seguidos não repetem o
// mesmo início em N pequeno).
func TestDeslocamento(t *testing.T) {
	casos := []struct {
		k    int64
		n, d int
	}{{1, 3, 1}, {2, 1, 0}, {1, 2, 1}, {2, 2, 0}, {3, 4000, 3416}, {1873, 1000, 577}, {5, 0, 0}}
	for _, c := range casos {
		if got := Deslocamento(c.k, c.n); got != c.d {
			t.Errorf("Deslocamento(%d, %d) = %d, esperava %d", c.k, c.n, got, c.d)
		}
	}
	vistos := map[int]bool{}
	for k := int64(1); k <= 10; k++ {
		d := Deslocamento(k, 10)
		if d < 0 || d >= 10 {
			t.Fatalf("fora da faixa: %d", d)
		}
		vistos[d] = true
	}
	if len(vistos) < 8 {
		t.Fatalf("10 sinais seguidos com N = 10 deviam começar em quase 10 lugares diferentes: %v", vistos)
	}
}

// v1.10 §6: sinal em duas partes — nada sai antes do fechamento; depois, as entradas saem na ordem
// da rajada, e a validade na fila da taxa conta do fechamento (não da chegada da parte).
func TestSinalEnvioNoFechamento(t *testing.T) {
	m, reg, _, rel := novoMotorTeste(t, map[string]string{"LIMITE_POSICAO_MAX": "10", "LIMITE_INTERVALO_ENTRADAS_S": "0",
		"LIMITE_ENTRADAS_MIN": "10", "LIMITE_MENSAGENS_MIN": "1000", "ENTRADA_TTL_FILA_S": "1", "FIX_TAXA_MSG_S": "1000", "SINAL_ESPERA_PARTES_MS": "5000"})
	item := func(op int64) []byte {
		return []byte(fmt.Sprintf(`{"operacao_id":%d,"uid":7,"lado":"C","qtd":1,"stop_pts":500,"motivo":"SINAL"}`, op))
	}
	parte := func(n int, ops ...int64) PedidoSinal {
		p := PedidoSinal{SinalID: "S", TsDecisao: "t", Parte: ptr(n), Partes: ptr(2)}
		for _, op := range ops {
			p.Entradas = append(p.Entradas, item(op))
		}
		return p
	}
	r := m.Sinal(parte(1, 11, 12))
	if r["fechado"] != false || len(reg.lista()) == 0 {
		t.Fatalf("parte 1: %v", r)
	}
	for _, s := range reg.lista() {
		if strings.HasPrefix(s, "envio") {
			t.Fatalf("nada sai antes do fechamento: %v", reg.lista())
		}
	}
	rel.Avancar(1200 * time.Millisecond) // contada da chegada da parte 1, a validade (1 s) já teria vencido
	r = m.Sinal(parte(2, 13))
	if r["fechado"] != true || r["k"] != int64(1) || r["deslocamento"] != 1 {
		t.Fatalf("parte 2: %v", r)
	}
	var envios []string
	for _, s := range reg.lista() {
		if strings.HasPrefix(s, "envio") {
			envios = append(envios, strings.TrimPrefix(s, "envio D "))
		}
	}
	if strings.Join(envios, ",") != "KM-E-12-1,KM-E-13-1,KM-E-11-1" {
		t.Fatalf("ordem da rajada: %v", envios)
	}
}

// v1.10 §6: uma parte maior que a fatia (tamanhoFatia) é tratada em várias voltas e responde uma
// vez, com a rajada inteira na ordem certa.
func TestSinalParteEmFatias(t *testing.T) {
	m, reg, _, _ := novoMotorTeste(t, map[string]string{"LIMITE_POSICAO_MAX": "10000", "LIMITE_INTERVALO_ENTRADAS_S": "0",
		"LIMITE_ENTRADAS_MIN": "10000", "LIMITE_MENSAGENS_MIN": "100000", "LIMITE_ENTRADAS_DIA": "10000", "FIX_TAXA_MSG_S": "100000",
		"FIX_RAJADA_MSG": "100000"})
	n := 2*tamanhoFatia + 100
	p := PedidoSinal{SinalID: "G", TsDecisao: "t", Parte: ptr(1), Partes: ptr(1)}
	for i := 1; i <= n; i++ {
		p.Entradas = append(p.Entradas, []byte(fmt.Sprintf(`{"operacao_id":%d,"uid":7,"lado":"C","qtd":1,"stop_pts":500,"motivo":"SINAL"}`, i)))
	}
	r := m.Sinal(p)
	res := r["resultados"].([]Resposta)
	if r["fechado"] != true || len(res) != n {
		t.Fatalf("resposta: fechado=%v resultados=%d", r["fechado"], len(res))
	}
	desl := Deslocamento(1, n)
	for i, x := range res {
		if x["aceito"] != true || x["posicao"] != ((i-desl)%n+n)%n+1 {
			t.Fatalf("item %d: %v (deslocamento %d)", i+1, x, desl)
		}
	}
	// no máximo maxEnviosPorVolta por volta: o resto sai nas seguintes (agendadas pelo motor)
	var envios []string
	for fim := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		envios = nil
		for _, s := range reg.lista() {
			if strings.HasPrefix(s, "envio") {
				envios = append(envios, s)
			}
		}
		if len(envios) >= n || time.Now().After(fim) {
			break
		}
	}
	if len(envios) != n {
		t.Fatalf("envios: %d de %d", len(envios), n)
	}
	for i, e := range envios {
		if want := fmt.Sprintf("envio D KM-E-%d-1", (desl+i)%n+1); e != want {
			t.Fatalf("envio %d: %q, esperava %q (a ordem da rajada atravessa as voltas)", i, e, want)
		}
	}
}
