// kalman-ordens: ordens reais do Kalman no PnT (DSA, FIX 4.4).
//
// Comandos em ajuda, abaixo. Sem comando, o binário só mostra a ajuda e sai com erro: o serviço
// sobe apenas com "servir" explícito (o ExecStart da unidade).
//
// Configuração por variáveis de ambiente (EnvironmentFile=/etc/kalman-ordens/service.env).
// parar/zerar/retomar/saude só falam com o HTTP local (HTTP_ENDERECO), para uso por SSH;
// migrar e usuario falam direto com o banco (não há endpoint que altere habilitação) e, sem
// DB_DSN no ambiente, leem o service.env.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/api"
	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/motor"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// versao é o commit do build (scripts/build.sh: -ldflags "-X main.versao=...").
var versao = "dev"

const ajuda = `uso: kalman-ordens <comando> [opções]

  servir                          sobe o serviço (o ExecStart da unidade systemd)
  migrar [-env ARQ]               aplica as migrações pendentes do banco e sai (sem FIX nem HTTP)
  usuario listar                  habilitação por uid, direto no banco
  usuario conferir    -uid N
  usuario habilitar   -uid N -motivo X [-posicao-max N] [-qtd-max N]   (sem a flag, mantém o limite atual)
  usuario desabilitar -uid N -motivo X
  historico operacao -id N [-modulo M] [-cru]   linha do tempo da operação (JSON, direto no banco)
  historico dia [-dia D] [-uid N] [-modulo M]   operações do dia
  historico sessao [-dia D]                     logon, logout, queda, reenvios, conciliações, alertas gerais
  quarentena listar                             linhas que o banco recusou por erro de dado (v1.13), pendentes
  quarentena resolver -id N -por UID -motivo X  marca a linha como revisada; sem pendentes, retomar libera
  expurgar [-meses N] [-simular]                apaga o que passou da retenção (timer mensal; EXPURGO_DB_DSN)
  parar   -motivo X               parada BLOQUEAR (HTTP local)
  zerar   -motivo X               parada ZERAR (HTTP local)
  retomar -motivo X               retira a parada (sempre manual)
  saude                           GET /saude
  teste-posicao -conta C [-espera S]   bancada da HML: RequestForPositions (35=AN) pela sessão do serviço; imprime a resposta crua
  versao                          commit embutido no binário
`

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	os.Exit(executar(os.Args[1:], os.Stdout, os.Stderr))
}

// executar devolve o código de saída. Sem comando: ajuda e 2, sem ler configuração nem abrir
// conexão — o binário chamado à toa não pode virar o serviço.
func executar(args []string, saida, erros io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(erros, "kalman-ordens: falta o comando (o serviço só sobe com \"servir\")\n\n"+ajuda)
		return 2
	}
	cmd, args := args[0], args[1:]
	var err error
	switch cmd {
	case "servir":
		ctx, parar := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer parar()
		err = servir(ctx, config.DoAmbiente())
	case "migrar":
		err = cliMigrar(config.DoAmbiente(), args, saida)
	case "teste-posicao":
		err = testePosicao(config.DoAmbiente(), args)
	case "parar", "zerar", "retomar", "saude":
		err = cli(config.DoAmbiente(), cmd, args)
	case "usuario":
		err = cliUsuario(config.DoAmbiente(), args, saida)
	case "historico":
		err = cliHistorico(config.DoAmbiente(), args, saida)
	case "quarentena":
		err = cliQuarentena(config.DoAmbiente(), args, saida)
	case "expurgar":
		err = cliExpurgar(config.DoAmbiente(), args, saida, time.Now())
	case "versao":
		fmt.Fprintln(saida, versao)
	case "ajuda", "help", "-h", "--help":
		fmt.Fprint(saida, ajuda)
	default:
		fmt.Fprintf(erros, "kalman-ordens: comando desconhecido %q\n\n%s", cmd, ajuda)
		return 2
	}
	if err != nil {
		fmt.Fprintln(erros, "kalman-ordens:", err)
		return 1
	}
	return 0
}

func obrigatorias(cfg config.Config, chaves ...string) error {
	var faltam []string
	for _, k := range chaves {
		if cfg.Texto(k, "") == "" {
			faltam = append(faltam, k)
		}
	}
	if len(faltam) > 0 {
		return fmt.Errorf("configuração ausente: %v", faltam)
	}
	return nil
}

// servir roda até ctx terminar (SIGTERM/SIGINT em produção).
func servir(ctx context.Context, cfg config.Config) error {
	motor.Versao = versao
	slog.Info("kalman-ordens subindo", "versao", versao)
	if err := obrigatorias(cfg, "DB_DSN"); err != nil {
		return err
	}
	// SPEC-AMBIENTES-v1 §3: o teto da máquina (AMBIENTES_PERMITIDOS, ou a equivalência
	// ORDENS_AMBIENTE da transição); incoerente = não sobe
	amb, avisos, err := cfg.LerAmbientes()
	if err != nil {
		return err
	}
	for _, a := range avisos {
		slog.Warn("configuração", "aviso", a)
	}
	slog.Info("ambientes", "permitidos", amb.Lista(), "padrao", amb.Padrao, "ordens_ambiente", amb.Legado,
		"real_uids", amb.RealUIDs, "real_uids_todos", amb.RealUIDsTodos, "real_contas", amb.RealContas, "real_contas_todas", amb.RealContasTodas)
	// v1.2: conta, símbolo e uid vêm da operação e da tabela de habilitação; um service.env antigo
	// não pode ficar valendo em silêncio
	var removidas []string
	for _, k := range []string{"ORDENS_CONTA", "ORDENS_ONBEHALF", "ORDENS_SIMBOLO", "ORDENS_VENCIMENTO", "ORDENS_UID_REAL"} {
		if cfg.Texto(k, "") != "" {
			removidas = append(removidas, k)
		}
	}
	if len(removidas) > 0 {
		return fmt.Errorf("chaves removidas na v1.2 ainda no service.env: %v (conta e símbolo vêm da operação; habilitação: kalman-ordens usuario habilitar; deploy/LEIA.md)", removidas)
	}
	endereco := cfg.Texto("HTTP_ENDERECO", "127.0.0.1:9092")
	if err := soLoopback(endereco); err != nil {
		return err
	}
	// v1.26: as instâncias por ambiente, com os conectores conferidos antes do banco
	plano, err := planejarInstancias(cfg, amb)
	if err != nil {
		return err
	}

	r, err := repo.AbrirMariaDB(cfg.Texto("DB_DSN", ""))
	if err != nil {
		return err
	}
	defer r.Fechar()
	// v1.13: o que foi aplicado vai ao journal (19/10: a 0009 entrou no servir sem deixar rastro)
	aplicadas, err := r.MigrarRelatando()
	for _, n := range aplicadas {
		slog.Info("migração aplicada", "migracao", n)
	}
	if err != nil {
		return fmt.Errorf("migração: %w", err)
	}
	if len(aplicadas) == 0 {
		slog.Info("migrações: nenhuma pendente")
	}
	if t := cfg.Texto("ACUSUARIO_TABELA", ""); t != "" { // só em teste
		if err := r.UsarTabelaUsuario(t); err != nil {
			return err
		}
	}
	if hs, err := r.Habilitacoes(); err == nil {
		var uids []int
		for _, h := range hs {
			if h.Habilitado {
				uids = append(uids, h.UID)
			}
		}
		slog.Info("habilitação", "ambientes", amb.Lista(), "contas_hml", cfg.Texto("ORDENS_CONTAS_HML", ""), "uids_habilitados", uids)
	}

	tg := alerta.NovoTelegram(cfg.Texto("TELEGRAM_TOKEN", ""), cfg.Texto("TELEGRAM_CHAT", ""))
	tg.Webhook = cfg.Texto("ALERTA_WEBHOOK_URL", "")      // .53: o bot local (GET ?msg=); vence o TELEGRAM_*
	tg.Servidor = cfg.Texto("ALERTA_SERVIDOR", maquina()) // a máquina no texto: "[.53 kalman-ordens] ..."
	tg.Iniciar()
	defer tg.Parar()

	rel := relogio.Real{}
	insts, err := montarInstancias(plano, cfg, amb, rel, r, tg)
	if err != nil {
		return err
	}
	defer func() {
		for _, f := range insts.fontes {
			f.Parar()
		}
	}()
	sv := insts.servico
	// a janela do FONTE_DEGRADADA só vale em dia de pregão (24/10): sem os feriados, vale seg–sex
	if n, err := sv.Motores()[0].ConferirFeriados(); err != nil {
		slog.Warn("feriados ilegíveis: o FONTE_DEGRADADA vale de seg a sex, inclusive feriados", "arquivo", cfg.Texto("FERIADOS_ARQ", "/var/lib/kalman/feriados.json"), "erro", err)
	} else {
		slog.Info("feriados", "dias", n)
	}
	var srvWebhook *http.Server
	if len(insts.receptores) > 0 {
		end := cfg.Texto("FLEXSCAN_WEBHOOK_ENDERECO", "")
		if end == endereco {
			return fmt.Errorf("FLEXSCAN_WEBHOOK_ENDERECO igual ao HTTP_ENDERECO: o webhook tem listener próprio")
		}
		if err := ipExplicito(end); err != nil {
			return err
		}
		// só as rotas do webhook (uma por ambiente, pelo prefixo): o mux do admin nunca é servido
		// fora do loopback (SPEC §5.1)
		srvWebhook = &http.Server{Addr: end, Handler: insts.receptores, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
			WriteTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10}
		lw, err := net.Listen("tcp", end)
		if err != nil {
			return fmt.Errorf("FLEXSCAN_WEBHOOK_ENDERECO %q: %w", end, err)
		}
		go func() {
			if err := srvWebhook.Serve(lw); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("webhook flexscan", "erro", err)
			}
		}()
	}
	// Parte 7.3: estado do dia carregado antes de logar.
	if err := sv.Iniciar(); err != nil {
		return err
	}
	defer sv.Parar()

	srv := &http.Server{Addr: endereco, Handler: api.Handler(sv, cfg.Segundos("HTTP_ESPERA_S", 5)), ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", endereco)
	if err != nil {
		return err
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "erro", err)
		}
	}()

	for _, in := range insts.lista {
		if in.sessao != nil {
			if err := in.sessao.Iniciar(); err != nil {
				return err
			}
		}
		if in.ponte != nil {
			in.ponte.Iniciar()
			slog.Info("flexscan: conector ligado", "ambiente", in.amb, "url", in.flx.URL, "usuario", in.flx.Usuario, "webhook", in.webhook)
		}
	}

	fim := make(chan struct{})
	go func() {
		t := time.NewTicker(cfg.Segundos("TICK_S", 0.1))
		defer t.Stop()
		for {
			select {
			case <-fim:
				return
			case <-t.C:
				sv.Tick()
				for _, in := range insts.lista {
					if in.ponte != nil {
						in.ponte.Tick()
					}
				}
			}
		}
	}()

	pronto(endereco)
	var resumo []string
	for _, in := range insts.lista {
		resumo = append(resumo, in.resumo())
	}
	slog.Info("kalman-ordens no ar", "versao", versao, "http", endereco, "ambientes", amb.Lista(), "instancias", resumo)

	<-ctx.Done()
	// Parar o processo NÃO cancela a 1014: a posição continua protegida no PnT (Parte 6).
	slog.Info("desligando: logout FIX, nada é cancelado no PnT")
	close(fim)
	for _, in := range insts.lista {
		if in.ponte != nil {
			// as ordens da Flexscan ficam na bolsa (a stop-limite é da B3)
			in.ponte.Parar()
		}
	}
	if srvWebhook != nil {
		ctxW, cancelW := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srvWebhook.Shutdown(ctxW)
		cancelW()
	}
	for _, in := range insts.lista {
		if in.sessao != nil {
			in.sessao.Parar()
		}
	}
	ctxFim, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxFim)
	return nil
}

// pronto é chamado quando o HTTP já escuta (gancho para testes).
var pronto = func(string) {}

// soLoopback: o HTTP só pode escutar em 127.0.0.1/::1 (Parte 1).
// maquina: o nome curto do host (o padrão do ALERTA_SERVIDOR).
func maquina() string {
	h, _ := os.Hostname()
	if i := strings.IndexByte(h, '.'); i > 0 {
		h = h[:i]
	}
	return h
}

// ipExplicito: o receptor do webhook escuta num IP dado (ex.: 172.16.110.53:9098), nunca em todas as
// interfaces (0.0.0.0, ::, ou sem host).
func ipExplicito(endereco string) error {
	host, _, err := net.SplitHostPort(endereco)
	if err != nil {
		return fmt.Errorf("FLEXSCAN_WEBHOOK_ENDERECO %q: %w", endereco, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return fmt.Errorf("FLEXSCAN_WEBHOOK_ENDERECO %q: use o IP da interface (ex.: 172.16.110.53:9098), não todas", endereco)
	}
	return nil
}

func soLoopback(endereco string) error {
	host, _, err := net.SplitHostPort(endereco)
	if err != nil {
		return fmt.Errorf("HTTP_ENDERECO %q: %w", endereco, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("HTTP_ENDERECO %q: o serviço só escuta em loopback (127.0.0.1)", endereco)
	}
	return nil
}

func cli(cfg config.Config, cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	motivo := fs.String("motivo", "", "motivo (obrigatório em parar, zerar e retomar)")
	endereco := fs.String("endereco", cfg.Texto("HTTP_ENDERECO", "127.0.0.1:9092"), "HTTP local do serviço")
	if err := fs.Parse(args); err != nil {
		return err
	}
	base := "http://" + *endereco
	cliente := &http.Client{Timeout: 10 * time.Second}
	var resp *http.Response
	var err error
	switch cmd {
	case "saude":
		resp, err = cliente.Get(base + "/saude")
	default:
		if *motivo == "" {
			return fmt.Errorf("%s exige -motivo", cmd)
		}
		caminho, corpo := "/parada", map[string]string{"origem": "cli", "motivo": *motivo}
		switch cmd {
		case "parar":
			corpo["modo"] = "BLOQUEAR"
		case "zerar":
			corpo["modo"] = "ZERAR"
		case "retomar":
			caminho = "/retomar"
		}
		b, _ := json.Marshal(corpo)
		resp, err = cliente.Post(base+caminho, "application/json", bytes.NewReader(b))
	}
	if err != nil {
		return fmt.Errorf("serviço não respondeu em %s: %w", *endereco, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var v any
	if json.Unmarshal(b, &v) == nil {
		b, _ = json.MarshalIndent(v, "", "  ")
	}
	fmt.Println(string(b))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// testePosicao (bancada da HML, 16/10): o PnT responde a RequestForPositions (35=AN)? Pede ao
// serviço em execução (a sessão FIX é dele) e imprime o que voltou, cru.
func testePosicao(cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("teste-posicao", flag.ContinueOnError)
	conta := fs.String("conta", "", "conta (tag 1), obrigatória")
	espera := fs.Float64("espera", 5, "segundos esperando as respostas do PnT (até 30)")
	endereco := fs.String("endereco", cfg.Texto("HTTP_ENDERECO", "127.0.0.1:9092"), "HTTP local do serviço")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *conta == "" {
		return fmt.Errorf("teste-posicao exige -conta")
	}
	b, _ := json.Marshal(map[string]any{"conta": *conta, "espera_s": *espera})
	cliente := &http.Client{Timeout: time.Duration(*espera+15) * time.Second}
	resp, err := cliente.Post("http://"+*endereco+"/bancada/posicoes", "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("serviço não respondeu em %s: %w", *endereco, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var v any
	if json.Unmarshal(out, &v) == nil {
		out, _ = json.MarshalIndent(v, "", "  ")
	}
	fmt.Println(string(out))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
