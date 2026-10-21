package preco

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

const fixtures = "../../cenarios/referencia/"

var lida = time.Date(2026, 10, 13, 13, 15, 3, 0, time.UTC)

func fixture(t *testing.T, nome string) []byte {
	t.Helper()
	b, err := os.ReadFile(fixtures + nome)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func dd(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// Fixture real (saída do curl na .51).
func TestExemploReal(t *testing.T) {
	l := Interpretar(200, fixture(t, "precos-atual-exemplo.json"), "WINFUT", lida)
	if l.Situacao != OK || l.Ticker != "WINZ26" || l.Status84 != 0 {
		t.Fatalf("leitura: %+v", l)
	}
	if !l.Preco.Equal(dd("170370")) || !l.Bid.Equal(dd("170365")) || !l.Ask.Equal(dd("170370")) {
		t.Fatalf("preços: %v %v %v", l.Preco, l.Bid, l.Ask)
	}
	if !l.IdadeSeg.Equal(dd("0.4")) {
		t.Fatalf("idade_seg: %v", l.IdadeSeg)
	}
	ts, _ := time.Parse(time.RFC3339, "2026-10-13T10:15:02-03:00")
	if !l.TS.Equal(ts) {
		t.Fatalf("ts: %v", l.TS)
	}
	// idade efetiva = idade_seg + tempo desde a leitura
	if d := l.Idade(lida.Add(2 * time.Second)); d != 2400*time.Millisecond {
		t.Fatalf("idade efetiva: %s", d)
	}
}

// Fixture real de sábado: corpo {} com 200 = sem dado de hoje.
func TestVazioReal(t *testing.T) {
	if l := Interpretar(200, fixture(t, "precos-atual-vazio.json"), "WINFUT", lida); l.Situacao != Vazio {
		t.Fatalf("situação: %v", l.Situacao)
	}
}

func TestCasos(t *testing.T) {
	casos := []struct {
		nome   string
		status int
		corpo  string
		quer   Situacao
		check  func(Leitura) bool
	}{
		{"404 motor desabilitado", 404, `not found`, Indisponivel, nil},
		{"400 ativo desconhecido", 400, `{"erro":"ativo"}`, Indisponivel, nil},
		{"500", 500, `x`, SemLeitura, nil},
		{"json quebrado", 200, `{"WINFUT":`, SemLeitura, nil},
		{"outro ativo só", 200, `{"WDOFUT":{"ticker":"WDOX26","preco":5000}}`, Vazio, nil},
		{"leilão: bid/ask sem preco", 200, `{"WINFUT":{"ticker":"WINZ26","bid":130000.0,"ask":130005.0,"status84":2,"ts":"2026-10-13T09:00:01-03:00","idade_seg":0.2}}`, OK,
			func(l Leitura) bool { return l.Preco == nil && l.Bid != nil && l.Ask != nil && l.Status84 == 2 }},
		{"só preco", 200, `{"WINFUT":{"ticker":"WINZ26","preco":130000.0,"status84":0,"ts":"2026-10-13T10:00:00-03:00","idade_seg":1.5}}`, OK,
			func(l Leitura) bool {
				return l.Preco != nil && l.Bid == nil && l.Ask == nil && l.IdadeSeg.Equal(dd("1.5"))
			}},
		{"ticker do próximo vencimento", 200, `{"WINFUT":{"ticker":"WING27","preco":130900.0,"bid":130895.0,"ask":130905.0,"status84":0,"ts":"2026-10-13T10:00:00-03:00","idade_seg":0.1}}`, OK,
			func(l Leitura) bool { return l.Ticker == "WING27" }},
		{"preço fracionário", 200, `{"WINFUT":{"ticker":"WINZ26","preco":130002.5,"bid":130000.0,"ask":130005.0,"status84":0,"ts":"2026-10-13T10:00:00-03:00","idade_seg":0}}`, OK,
			func(l Leitura) bool { return l.Preco.Equal(dd("130002.5")) }},
	}
	for _, c := range casos {
		l := Interpretar(c.status, []byte(c.corpo), "WINFUT", lida)
		if l.Situacao != c.quer || (c.check != nil && !c.check(l)) {
			t.Errorf("%s: %+v", c.nome, l)
		}
	}
}

// O poller real contra um servidor HTTP: consulta a URL certa, guarda a última leitura, e uma falha
// de rede vira SemLeitura (nunca o preço antigo).
func TestPollerHTTP(t *testing.T) {
	var mu sync.Mutex
	corpo, status := string(fixture(t, "precos-atual-exemplo.json")), 200
	var caminho string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		caminho = r.URL.RequestURI()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(corpo))
	}))
	defer srv.Close()
	h := NovaHTTP(srv.URL, "WINFUT", relogio.Real{})
	h.Iniciar(20 * time.Millisecond)
	defer h.Parar()

	esperar(t, func() bool { return h.Ler().Situacao == OK })
	mu.Lock()
	if caminho != "/precos/atual?ativo=WINFUT" {
		t.Fatalf("caminho: %s", caminho)
	}
	status, corpo = 404, "x"
	mu.Unlock()
	esperar(t, func() bool { return h.Ler().Situacao == Indisponivel })
	srv.Close()
	esperar(t, func() bool { return h.Ler().Situacao == SemLeitura })
	if h.Ler().Preco != nil {
		t.Fatal("falha de rede não pode manter o preço antigo")
	}
}

func esperar(t *testing.T, cond func() bool) {
	t.Helper()
	fim := time.Now().Add(3 * time.Second)
	for time.Now().Before(fim) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("tempo esgotado")
}

// O pnt-md-hml (tools/pnt-md-hml: o /precos/atual da HML a partir do WB_MD da PnT) responde no
// mesmo contrato do marketdata: a fixture é uma resposta real dele (com a gravação de 16/10 como
// fonte), lida pelo mesmo parser.
func TestContratoPntMdHml(t *testing.T) {
	corpo, err := os.ReadFile(filepath.Join("..", "..", "cenarios", "referencia", "precos-atual-pnt-md-hml.json"))
	if err != nil {
		t.Fatal(err)
	}
	l := Interpretar(200, corpo, "WINFUT", time.Now())
	if l.Situacao != OK || l.Ticker != "WINZ26" || l.Status84 != 0 || l.Preco == nil || l.Bid == nil || l.Ask == nil {
		t.Fatalf("leitura %+v", l)
	}
	if !l.Bid.LessThan(*l.Ask) {
		t.Fatalf("bid %s >= ask %s", l.Bid, l.Ask)
	}
	if v := Interpretar(200, []byte("{}"), "WINFUT", time.Now()); v.Situacao == OK {
		t.Fatalf("{} devia ser sem preço: %+v", v)
	}
}

// FONTE_DEGRADADA: o motivo vem do /saude do pnt-md-hml quando ele está indisponível.
func TestMotivoFonte(t *testing.T) {
	var mu sync.Mutex
	corpo := `{"disponivel": false, "motivo": "book degenerado: Aggregated=false", "book_motivo": "Aggregated=false", "reconexoes_seguidas": 2}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Write([]byte(corpo))
	}))
	defer srv.Close()
	h := NovaHTTP(srv.URL, "WINFUT", relogio.Real{})
	h.ComSaude(srv.URL + "/saude")
	h.consultarSaude()
	if m := h.MotivoFonte(); m != "book degenerado: Aggregated=false; 2 reconexao(oes) seguida(s)" {
		t.Fatalf("motivo %q", m)
	}
	mu.Lock()
	corpo = `{"disponivel": true, "motivo": ""}`
	mu.Unlock()
	h.consultarSaude()
	if m := h.MotivoFonte(); m != "" {
		t.Fatalf("disponível: motivo %q", m)
	}
	srv.Close()
	h.consultarSaude()
	if m := h.MotivoFonte(); m != "/saude da fonte sem resposta" {
		t.Fatalf("sem resposta: motivo %q", m)
	}
}
