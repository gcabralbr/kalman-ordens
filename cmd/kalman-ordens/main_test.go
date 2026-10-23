package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt/falso"
	"gammaquant.com.br/kalman-ordens/pnt/loopback"
)

// Fumaça do executável de ponta a ponta, com relógio real: MariaDB (container), HTTP real na
// porta, sessão FIX contra o PnT falso, marketdata falso no contrato do /precos/atual. Entrada pelo
// HTTP, CLI parar, desligamento por contexto (SIGTERM) sem cancelar a 1014.
func TestServirDePontaAPonta(t *testing.T) {
	dsn := os.Getenv("KALMAN_TESTE_MARIADB")
	if dsn == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	limpo, err := repo.AbrirMariaDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := limpo.LimparTeste(); err != nil {
		t.Fatal(err)
	}
	// como na instalação da .53: migrar pela CLI, depois o uid 7 habilitado pela CLI, com a conta
	// 12345 (HML) escolhida no GQ
	if err := cliMigrar(config.Novo(map[string]string{"DB_DSN": dsn}), nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := limpo.PrepararUsuariosTeste(); err != nil {
		t.Fatal(err)
	}
	if err := limpo.DefinirUsuario(7, "hml", `{"Robo":{"CORSeqId":85,"CCONroConta":"12345"}}`); err != nil {
		t.Fatal(err)
	}
	limpo.Fechar()
	var saidaCLI bytes.Buffer
	cfgCLI := config.Novo(map[string]string{"DB_DSN": dsn, "ACUSUARIO_TABELA": "acusuario_teste", "ORDENS_AMBIENTE": "HML", "ORDENS_CONTAS_HML": "12345"})
	if err := cliUsuario(cfgCLI, []string{"habilitar", "-uid", "7", "-motivo", "fumaça"}, &saidaCLI); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saidaCLI.String(), "destino=85 conector=PNT conta=12345") {
		t.Fatalf("usuario habilitar: %s", saidaCLI.String())
	}

	f, err := falso.Novo(falso.Config{Contraparte: "KALMAN_FUMACA"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Iniciar(); err != nil {
		t.Fatal(err)
	}
	defer f.Parar()

	md := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts := time.Now().In(time.FixedZone("BRT", -3*3600)).Format(time.RFC3339)
		fmt.Fprintf(w, `{"WINFUT":{"ticker":"WINZ26","preco":170370.0,"bid":170365.0,"ask":170370.0,"status84":0,"ts":%q,"idade_seg":0.1}}`, ts)
	}))
	defer md.Close()

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	endereco := ln.Addr().String()
	ln.Close()
	dirFix, _ := os.MkdirTemp("", "fumaca-fix") // o FileLog do QuickFIX/Go não fecha: não apagar no Windows
	cfg := config.Novo(map[string]string{
		"DB_DSN": dsn, "FIX_SENDER_COMP_ID": "KALMAN_FUMACA", "FIX_HOST": "127.0.0.1", "FIX_PORTA": strconv.Itoa(f.Porta()),
		"FIX_DIR": dirFix, "FIX_USUARIO": "USUARIO_FUMACA", "FIX_SENHA": senhaFumaca, "FIX_INICIO": "00:00:00", "FIX_FIM": "00:00:00", "FIX_RECONEXAO_S": "1", "FIX_HEARTBEAT": "5",
		"ORDENS_AMBIENTE": "HML", "ORDENS_CONTAS_HML": "12345", "ACUSUARIO_TABELA": "acusuario_teste",
		"JANELA_INICIO": "00:00:00", "JANELA_FIM": "23:59:59", "HORA_ZERAGEM": "23:59:59",
		"HTTP_ENDERECO": endereco, "PRECOS_URL": md.URL, "MASS_STATUS_SILENCIO_S": "0.5",
	})

	// todo log do serviço (slog) também é conferido contra a senha
	var logServico bufferSeguro
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, &logServico), nil)))
	defer slog.SetDefault(anterior)

	ok := make(chan struct{})
	pronto = func(string) { close(ok) }
	ctx, desligar := context.WithCancel(context.Background())
	fimServir := make(chan error, 1)
	go func() { fimServir <- servir(ctx, cfg) }()
	select {
	case <-ok:
	case err := <-fimServir:
		t.Fatalf("servir terminou: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("servir não ficou pronto")
	}
	if err := f.EsperarLogon(0, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	// nenhum listener (API do serviço, PnT falso, marketdata falso) fora do loopback
	_, portaHTTP, _ := net.SplitHostPort(endereco)
	pHTTP, _ := strconv.Atoi(portaHTTP)
	pMD := md.Listener.Addr().(*net.TCPAddr).Port
	loopback.Exigir(t, append(f.PortasEscuta(), pHTTP, pMD)...)
	// sem ordem viva nem posição, o logon não pede mass status (v1.10 C5): a conciliação fecha no
	// silêncio
	if m, err := f.Proxima(1500 * time.Millisecond); err == nil {
		t.Fatalf("logon sem ordens não devia mandar nada: %s", m.Bruta)
	}

	base := "http://" + endereco
	corpo := `{"operacao_id":9001,"uid":7,"papel":"ENTRADA","lado":"C","qtd":1,"stop_pts":500,"motivo":"SINAL","ts_decisao":"x"}`
	var r map[string]any
	postar(t, base+"/ordens", corpo, &r)
	if r["aceito"] != true {
		t.Fatalf("entrada: %v", r)
	}
	d, err := f.Proxima(5 * time.Second)
	if err != nil || d.Validacao != nil {
		t.Fatalf("D: %v %v", err, d.Validacao)
	}
	if v, _ := d.Valor(44); v != "170385" { // ask 170370 + 15
		t.Fatalf("44=%s\n%s", v, d.Bruta)
	}
	// v1.2: conta da operação no 1 e no 116, símbolo = ticker do marketdata
	for tag, quer := range map[int]string{1: "12345", 116: "12345", 55: "WINZ26", 20856: "Y"} {
		if v, _ := d.Valor(tag); v != quer {
			t.Fatalf("%d=%s, esperado %s\n%s", tag, v, quer, d.Bruta)
		}
	}
	c := func(tag int, v string) falso.Campo { return falso.Campo{Tag: tag, Valor: v} }
	er := func(extra ...falso.Campo) []falso.Campo {
		base := []falso.Campo{c(35, "8"), c(1, "12345"), c(55, "WINZ26"), c(207, "XBMF"), c(40, "2"), c(59, "0")}
		return append(base, extra...)
	}
	_ = f.Enviar(er(c(11, "KM-E-9001-1"), c(37, "dsa.1"), c(150, "0"), c(39, "0"), c(54, "1"), c(38, "1"), c(14, "0"), c(151, "1"), c(6, "0")))
	_ = f.Enviar(er(c(11, "KM-E-9001-1"), c(37, "dsa.1"), c(150, "F"), c(39, "2"), c(54, "1"), c(38, "1"), c(32, "1"), c(31, "170380"), c(14, "1"), c(151, "0"), c(6, "170380")))
	p, err := f.Proxima(5 * time.Second)
	if err != nil || p.Validacao != nil {
		t.Fatalf("1014: %v %v", err, p.Validacao)
	}
	if v, _ := p.Valor(20828); v != "169880" {
		t.Fatalf("gatilho %s\n%s", v, p.Bruta)
	}
	_ = f.Enviar(er(c(11, "KM-P-9001-1"), c(37, "dsa.2"), c(150, "0"), c(39, "0"), c(54, "2"), c(38, "1"), c(14, "0"), c(151, "1"), c(6, "0")))
	time.Sleep(300 * time.Millisecond)

	// CLI parar (o mesmo caminho do SSH)
	if err := cli(cfg, "parar", []string{"-motivo", "fumaca"}); err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	obter(t, base+"/saude", &s)
	if s["versao"] != "dev" {
		t.Fatalf("versão no /saude: %v", s["versao"])
	}
	if pa, _ := s["parada"].(map[string]any); pa == nil || pa["modo"] != "BLOQUEAR" || pa["origem"] != "cli" || s["posicao"] != float64(1) {
		t.Fatalf("saúde: %v", s)
	}

	// SIGTERM: logout, nada cancelado no PnT
	desligar()
	select {
	case err := <-fimServir:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("servir não desligou")
	}
	if err := f.EsperarQueda(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	for _, m := range f.Pendentes() {
		if m.Tipo == "F" {
			t.Fatalf("desligar cancelou ordem no PnT: %s", m.Bruta)
		}
	}

	// a senha foi no logon e não está no log FIX nem no log do serviço; o store guarda o logon como
	// foi ao fio, para o reenvio (SPEC v1.3 A1), protegido pelo diretório 700/600
	if !senhaNoFio(f) {
		t.Fatal("o logon não levou a senha")
	}
	mascarados := 0
	_ = filepath.WalkDir(filepath.Join(dirFix, "log"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			if strings.Contains(string(b), senhaFumaca) {
				t.Errorf("senha em texto puro em %s", p)
			}
			if strings.Contains(string(b), "554=***") {
				mascarados++
			}
		}
		return nil
	})
	if mascarados == 0 {
		t.Error("o log FIX não tem o logon (mascarado): a conferência da senha não provou nada")
	}
	if strings.Contains(logServico.String(), senhaFumaca) {
		t.Error("senha em texto puro no log do serviço")
	}
	// v1.3 A2: a janela de sincronia fica registrada no log do serviço e no log de eventos FIX
	if !strings.Contains(logServico.String(), "recebidas_na_janela=") {
		t.Error("o log do serviço não registra a janela de sincronia do logon")
	}
	sincronia := false
	_ = filepath.WalkDir(filepath.Join(dirFix, "log"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "ms de sincronia") {
				sincronia = true
			}
		}
		return nil
	})
	if !sincronia {
		t.Error("o log de eventos FIX não registra a janela de sincronia do logon")
	}

	// o estado ficou no banco
	db, err := repo.AbrirMariaDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Fechar()
	ops, ords, err := db.CarregarDia(time.Now().In(time.FixedZone("BRT", -3*3600)).Format("2006-01-02"))
	if err != nil || len(ops) != 1 || ops[0].Estado != dominio.Protegida || len(ords) != 2 {
		t.Fatalf("banco: %+v %d ordens %v", ops, len(ords), err)
	}
	if v, ok, _ := db.LerEstado("parada"); !ok || !bytes.Contains([]byte(v), []byte("BLOQUEAR")) {
		t.Fatalf("parada não persistida: %q", v)
	}
}

func postar(t *testing.T, url, corpo string, v any) {
	t.Helper()
	r, err := http.Post(url, "application/json", bytes.NewBufferString(corpo))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	_ = json.NewDecoder(r.Body).Decode(v)
}

func obter(t *testing.T, url string, v any) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	_ = json.NewDecoder(r.Body).Decode(v)
}

const senhaFumaca = "Senha-Fumaca-91c2"

func senhaNoFio(f *falso.Falso) bool {
	for _, m := range f.Sessao() {
		if m.Tipo == "A" && strings.Contains(m.Bruta, "|554="+senhaFumaca+"|") {
			return true
		}
	}
	return false
}

type bufferSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *bufferSeguro) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *bufferSeguro) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// v1.2: um service.env antigo (conta, símbolo, uid real) não sobe em silêncio; ambiente obrigatório.
func TestServirRecusaConfigAntiga(t *testing.T) {
	base := map[string]string{"DB_DSN": "x", "FIX_SENDER_COMP_ID": "X", "FIX_HOST": "127.0.0.1", "FIX_PORTA": "1",
		"ORDENS_AMBIENTE": "HML", "ORDENS_CONTAS_HML": "99999"}
	for _, c := range []struct{ chave, valor, erro string }{
		{"ORDENS_UID_REAL", "7", "removidas na v1.2"},
		{"ORDENS_CONTA", "99999", "removidas na v1.2"},
		{"ORDENS_SIMBOLO", "WINV26", "removidas na v1.2"},
		{"ORDENS_AMBIENTE", "PROD", "HML ou PRODUCAO"},
		{"ORDENS_CONTAS_HML", "", "ORDENS_CONTAS_HML"},
	} {
		v := map[string]string{}
		for k, x := range base {
			v[k] = x
		}
		v[c.chave] = c.valor
		err := servir(context.Background(), config.Novo(v))
		if err == nil || !strings.Contains(err.Error(), c.erro) {
			t.Errorf("%s=%q: %v", c.chave, c.valor, err)
		}
	}
}
