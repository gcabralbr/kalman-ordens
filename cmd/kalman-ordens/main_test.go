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
	limpo.Fechar()

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
		"ORDENS_CONTA": "12345", "ORDENS_SIMBOLO": "WINZ26", "ORDENS_UID_REAL": "7",
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
	if af, err := f.Proxima(5 * time.Second); err != nil || af.Tipo != "AF" {
		t.Fatalf("esperava o mass status do logon: %v %s", err, af.Bruta)
	}
	time.Sleep(1200 * time.Millisecond) // silêncio de 0,5 s + ticks reais

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

	// a senha foi no logon e não está em nenhum arquivo do FIX_DIR (log e store) nem no log do serviço
	if !senhaNoFio(f) {
		t.Fatal("o logon não levou a senha")
	}
	_ = filepath.WalkDir(dirFix, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), senhaFumaca) {
				t.Errorf("senha em texto puro em %s", p)
			}
		}
		return nil
	})
	if strings.Contains(logServico.String(), senhaFumaca) {
		t.Error("senha em texto puro no log do serviço")
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
