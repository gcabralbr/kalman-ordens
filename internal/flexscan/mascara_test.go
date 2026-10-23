package flexscan

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quickfixgo/quickfix"

	"gammaquant.com.br/kalman-ordens/internal/flexscan/falso"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// ouvinteTeste guarda tudo o que a ponte manda ao motor.
type ouvinteTeste struct {
	mu      sync.Mutex
	logou   int
	msgs    []pnt.Mensagem
	alertas []string
	eventos []repo.EventoFIX
}

func (o *ouvinteTeste) Logou() { o.mu.Lock(); o.logou++; o.mu.Unlock() }
func (o *ouvinteTeste) Caiu(m string) {
	o.mu.Lock()
	o.alertas = append(o.alertas, "CAIU "+m)
	o.mu.Unlock()
}
func (o *ouvinteTeste) Recebeu(m pnt.Mensagem) {
	o.mu.Lock()
	o.msgs = append(o.msgs, m)
	o.mu.Unlock()
}
func (o *ouvinteTeste) Alertar(c, k, t string) {
	o.mu.Lock()
	o.alertas = append(o.alertas, c+" "+k+" "+t)
	o.mu.Unlock()
}
func (o *ouvinteTeste) Resolver(string) {}
func (o *ouvinteTeste) Registrar(e repo.EventoFIX) {
	o.mu.Lock()
	o.eventos = append(o.eventos, e)
	o.mu.Unlock()
}

func (o *ouvinteTeste) tudo() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var b strings.Builder
	for _, m := range o.msgs {
		b.WriteString(m.Bruta + "\n")
	}
	for _, a := range o.alertas {
		b.WriteString(a + "\n")
	}
	for _, e := range o.eventos {
		fmt.Fprintf(&b, "%+v\n", e)
	}
	return b.String()
}

func esperar(t *testing.T, cond func() bool) {
	t.Helper()
	limite := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(limite) {
			t.Fatal("condição não atingida")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// SPEC-FLEXSCAN-v1 §5.3: o segredo do webhook, a senha e o token nunca são gravados (eventos,
// alertas, mensagens ao motor) nem saem no log.
func TestSegredoSenhaTokenForaDoRegistro(t *testing.T) {
	var log bytes.Buffer
	antigo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(antigo)

	const senha = "senha-secreta-do-teste-xyz"
	const segredo = "segredo-secreto-do-teste-0123456789abcdef"
	rel := relogio.NovoControlado(time.Date(2026, 10, 23, 10, 0, 0, 0, relogio.Fuso))
	f := falso.Novo("flx_teste", senha, rel.Agora)
	defer f.Parar()
	ouv := &ouvinteTeste{}
	p := Nova(Config{URL: f.URL(), Usuario: "flx_teste", Senha: senha, Timeout: time.Second, Webhook: true}, ouv, rel)
	p.Iniciar()
	defer p.Parar()
	esperar(t, p.Logado)

	// uma ordem, o webhook dela com o segredo, um 401 com novo login e um segredo errado
	m := quickfix.NewMessage()
	m.Header.SetString(quickfix.Tag(35), "D")
	for tag, v := range map[int]string{11: "KM-E-1-1", 1: "S-1117150", 55: "WINZ26", 54: "1", 38: "1", 44: "130020", 847: "1031"} {
		m.Body.SetString(quickfix.Tag(tag), v)
	}
	if _, err := p.Enviar(m); err != nil {
		t.Fatal(err)
	}
	esperar(t, func() bool { return f.PorNome("KM-E-1-1") != "" && !p.Ocupada() })
	id := f.PorNome("KM-E-1-1")
	rec, err := NovoReceptor(p, "/flexscan/webhook/", segredo)
	if err != nil {
		t.Fatal(err)
	}
	for _, caminho := range []string{"/flexscan/webhook/" + segredo, "/flexscan/webhook/" + segredo + "x"} {
		w := httptest.NewRecorder()
		rec.ServeHTTP(w, httptest.NewRequest(http.MethodPost, caminho, bytes.NewReader(f.CorpoWebhook(id))))
	}
	w := httptest.NewRecorder()
	rec.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/flexscan/webhook/"+segredo, nil))
	if w.Code != http.StatusNotFound || w.Body.Len() != 0 {
		t.Fatalf("GET com o segredo: %d %q", w.Code, w.Body.String())
	}
	tok := p.cli.tokenAtual()
	f.InvalidarTokens()
	p.enfileirar(prioConsulta, "", func(ctx context.Context) { p.consultar(ctx, p.porNome["KM-E-1-1"], "teste") })
	esperar(t, func() bool { return f.Logins() >= 2 && !p.Ocupada() })

	tudo := ouv.tudo() + log.String()
	for nome, s := range map[string]string{"segredo": segredo, "senha": senha, "token": tok, "token novo": p.cli.tokenAtual()} {
		if s != "" && strings.Contains(tudo, s) {
			t.Errorf("%s apareceu no registro ou no log", nome)
		}
	}
	if !strings.Contains(tudo, "FLX-WH") || !strings.Contains(tudo, "FLX-POST") {
		t.Fatalf("o registro não tem os eventos esperados:\n%s", tudo)
	}
	if rec.Recusas()["caminho"] != 1 || rec.Recusas()["metodo"] != 1 {
		t.Fatalf("recusas: %v", rec.Recusas())
	}
}

func TestSegredoCurto(t *testing.T) {
	if _, err := NovoReceptor(nil, "/flexscan/webhook/", "curto"); err == nil {
		t.Fatal("segredo curto aceito")
	}
	if _, err := NovoReceptor(nil, "/flexscan/webhook/", strings.Repeat("a", 31)+"/"); err == nil {
		t.Fatal("segredo com / aceito")
	}
}
