// kalman-ordens: ordens reais do Kalman no PnT (DSA, FIX 4.4).
//
//	kalman-ordens servir            sobe o serviço (systemd)
//	kalman-ordens parar  -motivo X  parada BLOQUEAR (origem cli)
//	kalman-ordens zerar  -motivo X  parada ZERAR (origem cli)
//	kalman-ordens retomar -motivo X retira a parada (sempre manual)
//	kalman-ordens saude             GET /saude
//	kalman-ordens versao            commit embutido no binário (scripts/build.sh)
//
// Configuração por variáveis de ambiente (EnvironmentFile=/etc/kalman-ordens/service.env).
// Os subcomandos de CLI só falam com o HTTP local (HTTP_ENDERECO), para uso por SSH.
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
	"syscall"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/api"
	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/motor"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// versao é o commit do build (scripts/build.sh: -ldflags "-X main.versao=...").
var versao = "dev"

func main() {
	cmd := "servir"
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	cfg := config.DoAmbiente()
	var err error
	switch cmd {
	case "servir":
		ctx, parar := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer parar()
		err = servir(ctx, cfg)
	case "parar", "zerar", "retomar", "saude":
		err = cli(cfg, cmd, args)
	case "versao":
		fmt.Println(versao)
	default:
		err = fmt.Errorf("comando desconhecido %q (servir | parar | zerar | retomar | saude | versao)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kalman-ordens:", err)
		os.Exit(1)
	}
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
	if err := obrigatorias(cfg, "DB_DSN", "FIX_SENDER_COMP_ID", "FIX_HOST", "FIX_PORTA", "ORDENS_CONTA", "ORDENS_SIMBOLO"); err != nil {
		return err
	}
	endereco := cfg.Texto("HTTP_ENDERECO", "127.0.0.1:9092")
	if err := soLoopback(endereco); err != nil {
		return err
	}

	r, err := repo.AbrirMariaDB(cfg.Texto("DB_DSN", ""))
	if err != nil {
		return err
	}
	defer r.Fechar()
	if err := r.Migrar(); err != nil {
		return fmt.Errorf("migração: %w", err)
	}

	tg := alerta.NovoTelegram(cfg.Texto("TELEGRAM_TOKEN", ""), cfg.Texto("TELEGRAM_CHAT", ""))
	tg.Iniciar()
	defer tg.Parar()

	rel := relogio.Real{}
	pr := preco.NovaHTTP(cfg.Texto("PRECOS_URL", "http://172.16.110.51:9090"), cfg.Texto("PRECOS_ATIVO", "WINFUT"), rel)
	pr.Iniciar(200 * time.Millisecond)
	defer pr.Parar()

	m := motor.Novo(cfg, rel, r, pr, tg)
	s, err := pnt.NovaSessao(pnt.ConfigSessao{
		SenderCompID: cfg.Texto("FIX_SENDER_COMP_ID", ""),
		TargetCompID: cfg.Texto("FIX_TARGET_COMP_ID", "PNT_DSA"),
		Host:         cfg.Texto("FIX_HOST", ""),
		Porta:        cfg.Inteiro("FIX_PORTA", 0),
		Usuario:      cfg.Texto("FIX_USUARIO", ""),
		Senha:        cfg.Texto("FIX_SENHA", ""),
		Dir:          cfg.Texto("FIX_DIR", "/var/lib/kalman-ordens/fix"),
		Inicio:       cfg.Texto("FIX_INICIO", "08:30:00"),
		Fim:          cfg.Texto("FIX_FIM", "17:55:00"),
		HeartBtInt:   cfg.Inteiro("FIX_HEARTBEAT", 30),
		ReconexaoS:   cfg.Inteiro("FIX_RECONEXAO_S", 5),
		ResetOnLogon: cfg.Bool("FIX_RESET_ON_LOGON", false),
	}, m)
	if err != nil {
		return err
	}
	m.Usar(s)
	// Parte 7.3: estado do dia carregado antes de logar.
	if err := m.Iniciar(); err != nil {
		return err
	}
	defer m.Parar()

	srv := &http.Server{Addr: endereco, Handler: api.Handler(m, cfg.Segundos("HTTP_ESPERA_S", 5)), ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", endereco)
	if err != nil {
		return err
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http", "erro", err)
		}
	}()

	if err := s.Iniciar(); err != nil {
		return err
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
				m.Tick()
			}
		}
	}()

	pronto(endereco)
	slog.Info("kalman-ordens no ar", "versao", versao, "http", endereco, "fix", cfg.Texto("FIX_HOST", "")+":"+cfg.Texto("FIX_PORTA", ""),
		"simbolo", cfg.Texto("ORDENS_SIMBOLO", ""), "uid_real", cfg.Texto("ORDENS_UID_REAL", "(nenhum)"))

	<-ctx.Done()
	// Parar o processo NÃO cancela a 1014: a posição continua protegida no PnT (Parte 6).
	slog.Info("desligando: logout FIX, nada é cancelado no PnT")
	close(fim)
	s.Parar()
	ctxFim, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxFim)
	return nil
}

// pronto é chamado quando o HTTP já escuta (gancho para testes).
var pronto = func(string) {}

// soLoopback: o HTTP só pode escutar em 127.0.0.1/::1 (Parte 1).
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
