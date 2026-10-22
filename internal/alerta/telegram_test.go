package alerta

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// servidor falso da API do Telegram, com respostas roteirizadas.
type apiFalsa struct {
	mu       sync.Mutex
	status   func(n int, texto string) (int, string) // n = número da chamada
	chamadas int
	textos   []string
	trava    chan struct{}
}

func (a *apiFalsa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if a.trava != nil {
		<-a.trava
	}
	a.mu.Lock()
	a.chamadas++
	st, corpo := 200, `{"ok":true}`
	if a.status != nil {
		st, corpo = a.status(a.chamadas, b.Text)
	}
	if st == 200 {
		a.textos = append(a.textos, b.Text)
	}
	a.mu.Unlock()
	w.WriteHeader(st)
	_, _ = w.Write([]byte(corpo))
}

func (a *apiFalsa) entregues() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.textos...)
}

func novo(t *testing.T, a *apiFalsa) *Telegram {
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	tg := NovoTelegram("TOKEN", "123")
	tg.URLBase = srv.URL
	tg.Timeout = 300 * time.Millisecond
	tg.Esperas = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond}
	tg.Iniciar()
	t.Cleanup(tg.Parar)
	return tg
}

func esperar(t *testing.T, oque string, cond func() bool) {
	t.Helper()
	fim := time.Now().Add(5 * time.Second)
	for time.Now().Before(fim) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tempo esgotado: %s", oque)
}

func TestEnviaEmOrdem(t *testing.T) {
	a := &apiFalsa{}
	tg := novo(t, a)
	tg.Notificar("PARADA", "um")
	tg.Notificar("STOP_FURADO", "dois")
	esperar(t, "dois entregues", func() bool { return len(a.entregues()) == 2 })
	got := a.entregues()
	if !strings.Contains(got[0], "PARADA: um") || !strings.Contains(got[1], "CRÍTICO STOP_FURADO: dois") {
		t.Fatalf("entregues: %v", got)
	}
}

// Telegram fora do ar por várias tentativas: o crítico chega quando volta; o não crítico desiste.
func TestCriticoNaoSePerde(t *testing.T) {
	fora := true
	var mu sync.Mutex
	a := &apiFalsa{status: func(int, string) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if fora {
			return 502, "bad gateway"
		}
		return 200, `{"ok":true}`
	}}
	tg := novo(t, a)
	tg.Notificar("SEM_PROTECAO", "posição sem proteção")
	tg.Notificar("CONCILIACAO", "resumo")
	esperar(t, "não crítico descartado", func() bool { return tg.Descartados() == 1 })
	time.Sleep(200 * time.Millisecond) // muitas tentativas a mais com o crítico
	if tg.Pendentes() != 1 {
		t.Fatalf("o crítico tinha de continuar na fila: %d pendentes", tg.Pendentes())
	}
	mu.Lock()
	fora = false
	mu.Unlock()
	esperar(t, "crítico entregue", func() bool { return len(a.entregues()) == 1 })
	if !strings.Contains(a.entregues()[0], "SEM_PROTECAO") {
		t.Fatalf("entregue: %v", a.entregues())
	}
}

// Notificar não bloqueia nem com a API pendurada.
func TestNotificarNaoBloqueia(t *testing.T) {
	a := &apiFalsa{trava: make(chan struct{})}
	tg := novo(t, a)
	t.Cleanup(func() { close(a.trava) })
	inicio := time.Now()
	for i := 0; i < 2000; i++ {
		tg.Notificar("PARADA", "x")
	}
	if d := time.Since(inicio); d > 200*time.Millisecond {
		t.Fatalf("Notificar levou %s com a API pendurada", d)
	}
	if tg.Pendentes() > tg.MaxFila+1 {
		t.Fatalf("fila de não críticos sem limite: %d", tg.Pendentes())
	}
}

// 4xx permanente não é retentado; 429 é.
func TestRecusas(t *testing.T) {
	a := &apiFalsa{status: func(n int, texto string) (int, string) {
		if strings.Contains(texto, "ruim") {
			return 400, `{"ok":false,"description":"Bad Request: message text is empty"}`
		}
		if n == 2 {
			return 429, `{"ok":false,"parameters":{"retry_after":0}}`
		}
		return 200, `{"ok":true}`
	}}
	tg := novo(t, a)
	tg.Notificar("SEM_PROTECAO", "ruim")
	tg.Notificar("SEM_PROTECAO", "bom")
	esperar(t, "bom entregue depois do 429", func() bool { return len(a.entregues()) == 1 })
	if tg.Descartados() != 1 {
		t.Fatalf("descartados = %d", tg.Descartados())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.chamadas != 3 { // ruim (400), bom (429), bom (200)
		t.Fatalf("chamadas = %d", a.chamadas)
	}
}

// O token nunca aparece no log (o erro de rede do net/http traz a URL com o token).
func TestTokenForaDoLog(t *testing.T) {
	var buf bytes.Buffer
	antes := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(antes)
	tg := NovoTelegram("SEGREDO123", "1")
	tg.URLBase = "http://127.0.0.1:1" // nada escuta: erro de conexão
	tg.Timeout = 200 * time.Millisecond
	tg.Esperas = []time.Duration{10 * time.Millisecond}
	tg.Iniciar()
	tg.Notificar("PARADA", "x")
	esperar(t, "desistir", func() bool { return tg.Descartados() == 1 })
	tg.Parar()
	if strings.Contains(buf.String(), "SEGREDO123") {
		t.Fatalf("token no log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "***") {
		t.Fatalf("erro de envio não registrado: %s", buf.String())
	}
}

// Webhook (o bot local da .53): GET ?msg=, com a máquina e o serviço no texto; entrega com "error" na
// resposta do bot é retentada; 400 é recusa permanente.
func TestWebhook(t *testing.T) {
	var mu sync.Mutex
	var msgs, chats []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		n++
		m := r.URL.Query().Get("msg")
		if r.Method != http.MethodGet || r.URL.Path != "/telegram/send_alert" {
			t.Errorf("pedido %s %s", r.Method, r.URL.Path)
		}
		switch {
		case strings.Contains(m, "ruim"):
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"Parâmetro msg é obrigatório"}`))
		case n == 2:
			_, _ = w.Write([]byte(`[{"chat_id":"689","error":"Bad Gateway"}]`))
		default:
			msgs = append(msgs, m)
			chats = append(chats, r.URL.Query().Get("chat_id"))
			_, _ = w.Write([]byte(`[{"chat_id":"689","status":"ok"}]`))
		}
	}))
	defer srv.Close()
	tg := NovoTelegram("", "")
	tg.Webhook = srv.URL + "/telegram/send_alert?chat_id=689"
	tg.Servidor = ".53"
	tg.Timeout = 300 * time.Millisecond
	tg.Esperas = []time.Duration{20 * time.Millisecond}
	tg.Iniciar()
	defer tg.Parar()
	tg.Notificar("SEM_PROTECAO", "ruim")
	tg.Notificar("FONTE_DEGRADADA", "sem preço & book")
	esperar(t, "entregue depois do erro do bot", func() bool { return tg.Enviados() == 1 })
	mu.Lock()
	defer mu.Unlock()
	if len(msgs) != 1 || msgs[0] != "[.53 kalman-ordens] CRÍTICO FONTE_DEGRADADA: sem preço & book" || chats[0] != "689" {
		t.Fatalf("msgs = %q, chats = %q", msgs, chats)
	}
	if tg.Descartados() != 1 || n != 3 { // ruim (400), bom (error do bot), bom (ok)
		t.Fatalf("descartados = %d, chamadas = %d", tg.Descartados(), n)
	}
}

func TestPrefixoSemServidor(t *testing.T) {
	a := &apiFalsa{}
	tg := novo(t, a)
	tg.Notificar("PARADA", "x")
	esperar(t, "entregue", func() bool { return len(a.entregues()) == 1 })
	if !strings.HasPrefix(a.entregues()[0], "[kalman-ordens] ") {
		t.Fatalf("texto = %q", a.entregues()[0])
	}
}
