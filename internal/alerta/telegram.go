package alerta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Telegram envia os alertas direto para a API do Telegram (sem serviço intermediário) ou, com Webhook,
// pelo GET ?msg= do bot local da máquina (o mesmo KALMAN_ALERT_WEBHOOK_URL do Kalman; na .53,
// ALERTA_WEBHOOK_URL). O texto leva a máquina e o serviço: "[<Servidor> kalman-ordens] ...".
//
// Notificar só enfileira: nunca bloqueia o motor. Uma goroutine envia com timeout curto; falha vai
// para o log e é retentada com espera crescente. Alerta crítico nunca é descartado; não crítico
// desiste depois de TentativasNaoCritico, ou quando a fila enche. Erro 4xx permanente não é
// retentado (o texto não vai passar nunca); 429 respeita o retry_after do Telegram.
type Telegram struct {
	URLBase              string // https://api.telegram.org
	Token, Chat          string
	Webhook              string          // vazio = API do Telegram (Token/Chat)
	Servidor             string          // a máquina no prefixo do texto (vazio = só o serviço)
	Timeout              time.Duration   // por envio
	Esperas              []time.Duration // entre tentativas; a última se repete
	TentativasNaoCritico int
	MaxFila              int // não críticos acima disto são descartados (o mais antigo)

	cliente               *http.Client
	mu                    sync.Mutex
	fila                  []*envio
	aviso                 chan struct{}
	parar                 chan struct{}
	feito                 chan struct{}
	enviados, descartados int
}

type envio struct {
	texto      string
	critico    bool
	tentativas int
	proxima    time.Time
}

// NovoTelegram com os padrões de produção.
func NovoTelegram(token, chat string) *Telegram {
	return &Telegram{
		URLBase: "https://api.telegram.org", Token: token, Chat: chat,
		Timeout: 3 * time.Second, TentativasNaoCritico: 3, MaxFila: 1000,
		Esperas: []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, time.Minute},
	}
}

// Iniciar sobe a goroutine de envio.
func (t *Telegram) Iniciar() {
	t.cliente = &http.Client{Timeout: t.Timeout}
	t.aviso = make(chan struct{}, 1)
	t.parar = make(chan struct{})
	t.feito = make(chan struct{})
	go t.laco()
}

// Parar encerra a goroutine (o que estiver na fila fica no log).
func (t *Telegram) Parar() {
	close(t.parar)
	<-t.feito
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range t.fila {
		slog.Warn("telegram: não enviado ao parar", "texto", e.texto)
	}
}

// Notificar enfileira (nunca bloqueia).
func (t *Telegram) Notificar(codigo, texto string) {
	cr := Catalogo[codigo]
	prefixo := ""
	if cr {
		prefixo = "CRÍTICO "
	}
	rotulo := "kalman-ordens"
	if t.Servidor != "" {
		rotulo = t.Servidor + " " + rotulo
	}
	e := &envio{texto: fmt.Sprintf("[%s] %s%s: %s", rotulo, prefixo, codigo, texto), critico: cr}
	t.mu.Lock()
	t.fila = append(t.fila, e)
	if n := t.naoCriticos(); n > t.MaxFila {
		t.descartarNaoCriticoMaisAntigo()
	}
	t.mu.Unlock()
	select {
	case t.aviso <- struct{}{}:
	default:
	}
}

// Pendentes e contadores (saúde e testes).
func (t *Telegram) Pendentes() int { t.mu.Lock(); defer t.mu.Unlock(); return len(t.fila) }
func (t *Telegram) Enviados() int  { t.mu.Lock(); defer t.mu.Unlock(); return t.enviados }
func (t *Telegram) Descartados() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.descartados
}

func (t *Telegram) naoCriticos() int {
	n := 0
	for _, e := range t.fila {
		if !e.critico {
			n++
		}
	}
	return n
}

func (t *Telegram) descartarNaoCriticoMaisAntigo() {
	for i, e := range t.fila {
		if !e.critico {
			slog.Warn("telegram: fila cheia, descartando alerta não crítico", "texto", e.texto)
			t.fila = append(t.fila[:i], t.fila[i+1:]...)
			t.descartados++
			return
		}
	}
}

func (t *Telegram) laco() {
	defer close(t.feito)
	for {
		e, espera := t.proximo()
		if e == nil {
			select {
			case <-t.parar:
				return
			case <-t.aviso:
			case <-time.After(espera):
			}
			continue
		}
		ok, permanente, retry := t.enviar(e.texto)
		t.mu.Lock()
		switch {
		case ok:
			t.remover(e)
			t.enviados++
		case permanente:
			slog.Error("telegram: recusa permanente, descartado", "texto", e.texto)
			t.remover(e)
			t.descartados++
		default:
			e.tentativas++
			if !e.critico && e.tentativas >= t.TentativasNaoCritico {
				slog.Error("telegram: desistindo de alerta não crítico", "tentativas", e.tentativas, "texto", e.texto)
				t.remover(e)
				t.descartados++
			} else {
				i := e.tentativas - 1
				if i >= len(t.Esperas) {
					i = len(t.Esperas) - 1
				}
				d := t.Esperas[i]
				if retry > d {
					d = retry
				}
				e.proxima = time.Now().Add(d)
			}
		}
		t.mu.Unlock()
		select {
		case <-t.parar:
			return
		default:
		}
	}
}

// proximo: o primeiro da fila que já pode ir (ordem de chegada); senão, quanto esperar.
func (t *Telegram) proximo() (*envio, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	agora := time.Now()
	espera := time.Minute
	for _, e := range t.fila {
		if !agora.Before(e.proxima) {
			return e, 0
		}
		if d := e.proxima.Sub(agora); d < espera {
			espera = d
		}
	}
	return nil, espera
}

func (t *Telegram) remover(e *envio) {
	for i, x := range t.fila {
		if x == e {
			t.fila = append(t.fila[:i], t.fila[i+1:]...)
			return
		}
	}
}

// enviar: ok; permanente (4xx que não vai passar); retry (429 retry_after).
func (t *Telegram) enviar(texto string) (ok, permanente bool, retry time.Duration) {
	if t.Webhook != "" {
		return t.enviarWebhook(texto)
	}
	if t.Token == "" || t.Chat == "" {
		slog.Warn("telegram sem token/chat; alerta só no log", "texto", texto)
		return true, false, 0
	}
	corpo, _ := json.Marshal(map[string]any{"chat_id": t.Chat, "text": texto, "disable_web_page_preview": true})
	resp, err := t.cliente.Post(t.URLBase+"/bot"+t.Token+"/sendMessage", "application/json", bytes.NewReader(corpo))
	if err != nil {
		// o erro do net/http traz a URL, que contém o token: nunca vai para o log
		slog.Warn("telegram: falha de envio, vai retentar", "erro", strings.ReplaceAll(err.Error(), t.Token, "***"))
		return false, false, 0
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusOK:
		return true, false, 0
	case resp.StatusCode == http.StatusTooManyRequests:
		var r struct {
			Parameters struct {
				RetryAfter int `json:"retry_after"`
			} `json:"parameters"`
		}
		_ = json.Unmarshal(b, &r)
		slog.Warn("telegram: 429, vai retentar", "retry_after", r.Parameters.RetryAfter)
		return false, false, time.Duration(r.Parameters.RetryAfter) * time.Second
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		slog.Error("telegram: recusa", "status", resp.StatusCode, "corpo", string(b))
		return false, true, 0
	default:
		slog.Warn("telegram: erro do servidor, vai retentar", "status", resp.StatusCode)
		return false, false, 0
	}
}

// enviarWebhook: GET <Webhook>&msg=<texto>. O bot responde 200 com a lista de entregas por chat; uma
// entrega com "error" (o Telegram recusou ou não respondeu) é retentada como falha passageira.
func (t *Telegram) enviarWebhook(texto string) (ok, permanente bool, retry time.Duration) {
	sep := "?"
	if strings.Contains(t.Webhook, "?") {
		sep = "&"
	}
	resp, err := t.cliente.Get(t.Webhook + sep + url.Values{"msg": {texto}}.Encode())
	if err != nil {
		slog.Warn("webhook: falha de envio, vai retentar", "erro", err.Error())
		return false, false, 0
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusOK:
		var r []map[string]any
		if json.Unmarshal(b, &r) == nil {
			for _, x := range r {
				if _, erro := x["error"]; erro {
					slog.Warn("webhook: o bot não entregou, vai retentar", "corpo", string(b))
					return false, false, 0
				}
			}
		}
		return true, false, 0
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		slog.Error("webhook: recusa", "status", resp.StatusCode, "corpo", string(b))
		return false, true, 0
	default:
		slog.Warn("webhook: erro do servidor, vai retentar", "status", resp.StatusCode)
		return false, false, 0
	}
}
