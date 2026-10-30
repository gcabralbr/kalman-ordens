package flexscan

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Resposta de um pedido REST. Status 0 = "não sei" (timeout, conexão caída): o pedido pode ou não
// ter chegado (SPEC-FLEXSCAN-v1 §4.3).
type Resposta struct {
	Status int
	Corpo  []byte
	Erro   error
}

// Incerta: timeout, conexão caída ou 5xx — o efeito do pedido não é conhecido.
func (r Resposta) Incerta() bool { return r.Status == 0 || r.Status >= 500 }

// ErroAPI: o corpo de erro da Flexscan ({code, title, description}).
type ErroAPI struct {
	Codigo    int    `json:"code"`
	Titulo    string `json:"title"`
	Descricao string `json:"description"`
}

func (r Resposta) ErroAPI() ErroAPI {
	var e ErroAPI
	_ = json.Unmarshal(r.Corpo, &e)
	return e
}

// TextoErro: o motivo legível de uma recusa (description, title ou o corpo cru).
func (r Resposta) TextoErro() string {
	if r.Erro != nil {
		return r.Erro.Error()
	}
	e := r.ErroAPI()
	partes := []string{}
	if e.Titulo != "" {
		partes = append(partes, e.Titulo)
	}
	if e.Descricao != "" {
		partes = append(partes, e.Descricao)
	}
	if len(partes) == 0 {
		return strings.TrimSpace(cortar(string(r.Corpo), 300))
	}
	return strings.Join(partes, ": ")
}

func cortar(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// cliente: a API da Flexscan com token. Um login por vez (os pedidos esperam o mesmo); 401 → um
// login e um reenvio só daquele pedido; o segundo 401 é devolvido (SPEC §2 item 2).
type cliente struct {
	url, usuario, senha string
	http                *http.Client

	mu       sync.Mutex
	token    string
	expira   time.Time // exp do JWT (zero: desconhecido)
	obtido   time.Time
	logando  chan struct{} // login em curso (nil: nenhum)
	errLogin error

	taxa   *balde
	guarda *guardaLogin
	// v1.21: chamado a cada login que esta chamada fez (ou que a guarda barrou), com o motivo, o
	// resultado e a duração; nunca com usuário, senha ou token
	aoLogin func(motivo string, err error, dur time.Duration)
	// v1.28: o relógio do token (o do serviço; o virtual nos roteiros)
	agora func() time.Time
	// v1.28 §3: pedidos seguidos sem resposta (erro de rede ou 502/503/504); zera com qualquer
	// outra resposta
	semResposta atomic.Int32
}

func novoCliente(url, usuario, senha string, timeout time.Duration, reqPorS float64, rajada int, guarda *guardaLogin) *cliente {
	return &cliente{url: strings.TrimRight(url, "/"), usuario: usuario, senha: senha,
		http: &http.Client{Timeout: timeout}, taxa: novoBalde(reqPorS, rajada), guarda: guarda, agora: time.Now}
}

// ErroLogin: o login falhou; Definitivo = não adianta tentar de novo sem intervenção (senha
// bloqueada, inválida ou expirada, usuário inativo, MFA).
type ErroLogin struct {
	Motivo     string
	Definitivo bool
	Status     int // v1.21: o HTTP da recusa (0 = sem resposta ou não tentado)
}

func (e *ErroLogin) Error() string { return e.Motivo }

// codigos de login definitivos (LoginError da OpenAPI): 3400 senha bloqueada, 3401 teste expirado,
// 3402 senha inválida, 3403 usuário não encontrado, 3404 inativo, 3405 senha expirada, 3421 só
// portal, 3433 vários usuários, 3434 acesso suspeito.
var loginDefinitivo = map[int]bool{3400: true, 3401: true, 3402: true, 3403: true, 3404: true, 3405: true, 3421: true, 3433: true, 3434: true}

// login: um por vez; quem chega durante um login espera o resultado dele. Cada ida à rede passa pela
// guarda (limite entre restarts) e vai para o log com o motivo (partida, renovação, 401...).
func (c *cliente) login(ctx context.Context, motivo string) error {
	c.mu.Lock()
	if ch := c.logando; ch != nil {
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.errLogin
	}
	ch := make(chan struct{})
	c.logando = ch
	c.mu.Unlock()

	var tok string
	var exp time.Time
	var err error
	t0 := time.Now()
	if e := c.guarda.pode(time.Now()); e != nil {
		err = e
		slog.Warn("flexscan: login não tentado", "motivo", motivo, "porque", e.Motivo)
	} else {
		tok, exp, err = c.fazerLogin(ctx)
		c.guarda.resultado(err, time.Now())
		if err != nil {
			slog.Warn("flexscan: login recusado", "motivo", motivo, "usuario", c.usuario, "erro", err)
		} else {
			slog.Info("flexscan: login", "motivo", motivo, "usuario", c.usuario, "expira", exp)
		}
	}

	c.mu.Lock()
	if err == nil {
		c.token, c.expira, c.obtido = tok, exp, c.agora()
	}
	c.errLogin = err
	c.logando = nil
	close(ch)
	aoLogin := c.aoLogin
	c.mu.Unlock()
	if aoLogin != nil {
		aoLogin(motivo, err, time.Since(t0))
	}
	return err
}

func (c *cliente) fazerLogin(ctx context.Context) (string, time.Time, error) {
	corpo, _ := json.Marshal(map[string]string{"user": c.usuario, "psswd": c.senha})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/connect/login", bytes.NewReader(corpo))
	if err != nil {
		return "", time.Time{}, &ErroLogin{Motivo: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", time.Time{}, &ErroLogin{Motivo: "login sem resposta: " + semURL(err)}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		r := Resposta{Status: resp.StatusCode, Corpo: b}
		e := r.ErroAPI()
		return "", time.Time{}, &ErroLogin{Motivo: fmt.Sprintf("login %d: %s", resp.StatusCode, r.TextoErro()), Definitivo: loginDefinitivo[e.Codigo], Status: resp.StatusCode}
	}
	var lr struct {
		Token  string `json:"auth_token"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(b, &lr)
	if lr.Token == "" {
		if lr.Status == "notification_sent" {
			return "", time.Time{}, &ErroLogin{Motivo: "login pediu confirmação por push (MFA): o usuário de API não pode ter MFA", Definitivo: true}
		}
		return "", time.Time{}, &ErroLogin{Motivo: "login sem auth_token"}
	}
	return lr.Token, expiracaoJWT(lr.Token), nil
}

// expiracaoJWT: o exp do token (lido sem validar a assinatura; só para renovar antes de vencer).
func expiracaoJWT(tok string) time.Time {
	partes := strings.Split(tok, ".")
	if len(partes) != 3 {
		return time.Time{}
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(partes[1], "="))
	if err != nil {
		return time.Time{}
	}
	var c struct {
		Exp json.Number `json:"exp"`
	}
	if json.Unmarshal(b, &c) != nil {
		return time.Time{}
	}
	if n, err := c.Exp.Int64(); err == nil && n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

// temToken: há token e ele não venceu (pelo exp; sem exp, pela vida máxima).
func (c *cliente) temToken(vidaMax time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == "" {
		return false
	}
	if !c.expira.IsZero() {
		return c.agora().Before(c.expira)
	}
	return c.agora().Sub(c.obtido) < vidaMax
}

// renovarEm: quando renovar (exp − antes; sem exp, obtido + vida − antes).
func (c *cliente) renovarEm(antes, vida time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == "" {
		return time.Time{}
	}
	if !c.expira.IsZero() {
		return c.expira.Add(-antes)
	}
	return c.obtido.Add(vida - antes)
}

func (c *cliente) esquecerToken(tok string) {
	c.mu.Lock()
	if c.token == tok {
		c.token = ""
	}
	c.mu.Unlock()
}

func (c *cliente) tokenAtual() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// pedir: um pedido autenticado. 401 → login e um reenvio; o segundo 401 volta como está.
func (c *cliente) pedir(ctx context.Context, metodo, caminho string, corpo any) Resposta {
	var b []byte
	if corpo != nil {
		b, _ = json.Marshal(corpo)
	}
	for tentativa := 0; tentativa < 2; tentativa++ {
		c.taxa.esperar()
		tok := c.tokenAtual()
		if tok == "" {
			if err := c.login(ctx, "sem token"); err != nil {
				return Resposta{Status: http.StatusUnauthorized, Erro: err}
			}
			tok = c.tokenAtual()
		}
		r := c.um(ctx, metodo, caminho, b, tok)
		if r.Status != http.StatusUnauthorized || tentativa == 1 {
			return r
		}
		c.esquecerToken(tok)
		if err := c.login(ctx, "401"); err != nil {
			return Resposta{Status: http.StatusUnauthorized, Erro: err}
		}
	}
	return Resposta{Status: http.StatusUnauthorized}
}

func (c *cliente) um(ctx context.Context, metodo, caminho string, corpo []byte, tok string) Resposta {
	var rd io.Reader
	if corpo != nil {
		rd = bytes.NewReader(corpo)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, c.url+caminho, rd)
	if err != nil {
		return Resposta{Erro: err}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.semResposta.Add(1)
		return Resposta{Erro: errors.New(semURL(err))}
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		c.semResposta.Add(1)
	default:
		c.semResposta.Store(0)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Resposta{Erro: errors.New(semURL(err))}
	}
	return Resposta{Status: resp.StatusCode, Corpo: b}
}

// semURL: o erro do net/http sem a URL (que não tem segredo, mas polui o texto do alerta).
func semURL(err error) string {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) {
		if in := ue.Unwrap(); in != nil {
			return in.Error()
		}
	}
	return err.Error()
}

// balde: limite de pedidos por segundo do nosso lado (FLEXSCAN_REQ_POR_S, rajada), até a Flexscan
// dizer o dela [FX-1].
type balde struct {
	mu     sync.Mutex
	porS   float64
	rajada float64
	tokens float64
	em     time.Time
}

func novoBalde(porS float64, rajada int) *balde {
	if rajada < 1 {
		rajada = 1
	}
	return &balde{porS: porS, rajada: float64(rajada), tokens: float64(rajada), em: time.Now()}
}

func (b *balde) esperar() {
	if b.porS <= 0 {
		return
	}
	for {
		b.mu.Lock()
		agora := time.Now()
		b.tokens += agora.Sub(b.em).Seconds() * b.porS
		if b.tokens > b.rajada {
			b.tokens = b.rajada
		}
		b.em = agora
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return
		}
		falta := time.Duration((1 - b.tokens) / b.porS * float64(time.Second))
		b.mu.Unlock()
		time.Sleep(falta)
	}
}
