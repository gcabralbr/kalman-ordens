// Package preco lê a cotação do símbolo operado no marketdata (/precos/atual, contrato em
// docs/referencia/precos-atual.md). O motor nunca espera HTTP: lê a última leitura guardada.
package preco

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// Situação da última leitura.
type Situacao int

const (
	SemLeitura Situacao = iota // nada lido ainda, erro de rede, timeout
	OK
	Vazio        // 200 com {} ou sem o ativo (sem dado de hoje)
	Indisponivel // 404 (motor desabilitado) ou 400 (ativo desconhecido)
)

// Leitura é o que o marketdata devolveu. Campos ausentes no JSON ficam nil.
type Leitura struct {
	Situacao Situacao
	Ticker   string
	Preco    *decimal.Decimal
	Bid      *decimal.Decimal
	Ask      *decimal.Decimal
	Status84 int
	TS       time.Time
	IdadeSeg decimal.Decimal // idade informada pelo marketdata
	LidaEm   time.Time       // quando o serviço leu (relógio do serviço)
}

// Idade efetiva: idade_seg + tempo desde a leitura.
func (l Leitura) Idade(agora time.Time) time.Duration {
	base := time.Duration(l.IdadeSeg.Mul(decimal.NewFromInt(int64(time.Second))).IntPart())
	return base + agora.Sub(l.LidaEm)
}

// Fonte devolve a última leitura.
type Fonte interface {
	Ler() Leitura
}

// HTTP consulta o marketdata a cada intervalo, numa goroutine própria.
type HTTP struct {
	url      string
	conceito string
	rel      relogio.Relogio
	cliente  *http.Client
	mu       sync.Mutex
	ultima   Leitura
	parar    chan struct{}
	// opcional (FONTE_DEGRADADA): o /saude da fonte (pnt-md-hml), lido a cada 5 s, dá o motivo
	saudeURL string
	motivo   string
}

// ComSaude liga a leitura do /saude da fonte (o do pnt-md-hml; vazio = desligado). Antes de Iniciar.
func (h *HTTP) ComSaude(url string) { h.saudeURL = url }

// MotivoFonte: o motivo que a própria fonte dá para estar indisponível ("" = disponível ou sem /saude).
func (h *HTTP) MotivoFonte() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.motivo
}

func (h *HTTP) consultarSaude() {
	m := ""
	r, err := h.cliente.Get(h.saudeURL)
	if err != nil {
		m = "/saude da fonte sem resposta"
	} else {
		defer r.Body.Close()
		var s struct {
			Disponivel      *bool  `json:"disponivel"`
			Motivo          string `json:"motivo"`
			BookMotivo      string `json:"book_motivo"`
			ReconexaoAlerta bool   `json:"reconexao_alerta"`
			Reconexoes      int    `json:"reconexoes_seguidas"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&s) == nil && s.Disponivel != nil && !*s.Disponivel {
			m = s.Motivo
			if s.BookMotivo != "" && !strings.Contains(m, s.BookMotivo) {
				m += " (" + s.BookMotivo + ")"
			}
			if s.Reconexoes > 0 {
				m += fmt.Sprintf("; %d reconexao(oes) seguida(s)", s.Reconexoes)
			}
		}
	}
	h.mu.Lock()
	h.motivo = m
	h.mu.Unlock()
}

// NovaHTTP: base = http://172.16.110.51:9090; conceito = WINFUT.
func NovaHTTP(base, conceito string, rel relogio.Relogio) *HTTP {
	return &HTTP{
		url:      fmt.Sprintf("%s/precos/atual?ativo=%s", base, conceito),
		conceito: conceito,
		rel:      rel,
		cliente:  &http.Client{Timeout: 500 * time.Millisecond},
		parar:    make(chan struct{}),
	}
}

func (h *HTTP) Iniciar(intervalo time.Duration) {
	if h.saudeURL != "" {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				h.consultarSaude()
				select {
				case <-h.parar:
					return
				case <-t.C:
				}
			}
		}()
	}
	go func() {
		t := time.NewTicker(intervalo)
		defer t.Stop()
		for {
			h.consultar()
			select {
			case <-h.parar:
				return
			case <-t.C:
			}
		}
	}()
}

func (h *HTTP) Parar() { close(h.parar) }

func (h *HTTP) Ler() Leitura {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ultima
}

func (h *HTTP) consultar() {
	l := Leitura{LidaEm: h.rel.Agora()}
	r, err := h.cliente.Get(h.url)
	if err != nil {
		h.guardar(l)
		return
	}
	defer r.Body.Close()
	corpo, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	l = Interpretar(r.StatusCode, corpo, h.conceito, l.LidaEm)
	h.guardar(l)
}

func (h *HTTP) guardar(l Leitura) {
	h.mu.Lock()
	h.ultima = l
	h.mu.Unlock()
}

type payload struct {
	Ticker   string   `json:"ticker"`
	Preco    *float64 `json:"preco"`
	Bid      *float64 `json:"bid"`
	Ask      *float64 `json:"ask"`
	Status84 int      `json:"status84"`
	TS       string   `json:"ts"`
	IdadeSeg float64  `json:"idade_seg"`
}

// Interpretar converte a resposta HTTP numa leitura (exportado para testes e fixtures).
func Interpretar(status int, corpo []byte, conceito string, lidaEm time.Time) Leitura {
	l := Leitura{LidaEm: lidaEm}
	switch {
	case status == 404 || status == 400:
		l.Situacao = Indisponivel
		return l
	case status != 200:
		return l
	}
	var m map[string]payload
	if err := json.Unmarshal(corpo, &m); err != nil {
		return l
	}
	p, ok := m[conceito]
	if !ok {
		l.Situacao = Vazio
		return l
	}
	l.Situacao = OK
	l.Ticker = p.Ticker
	l.Status84 = p.Status84
	l.Preco, l.Bid, l.Ask = dec(p.Preco), dec(p.Bid), dec(p.Ask)
	l.IdadeSeg = decimal.NewFromFloat(p.IdadeSeg)
	if t, err := time.Parse(time.RFC3339, p.TS); err == nil {
		l.TS = t
	}
	return l
}

func dec(f *float64) *decimal.Decimal {
	if f == nil {
		return nil
	}
	d := decimal.NewFromFloat(*f)
	return &d
}

// Falsa é a fonte controlada pelo executor de roteiros (verbo preco).
type Falsa struct {
	mu       sync.Mutex
	rel      relogio.Relogio
	situacao Situacao
	ticker   string
	preco    *decimal.Decimal
	bid      *decimal.Decimal
	ask      *decimal.Decimal
	status84 int
	idade    time.Duration
}

func NovaFalsa(rel relogio.Relogio) *Falsa { return &Falsa{rel: rel, situacao: SemLeitura} }

// Definir aplica campos do verbo preco. valor "-" omite o campo.
func (f *Falsa) Definir(campo, valor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	num := func() (*decimal.Decimal, error) {
		if valor == "-" {
			return nil, nil
		}
		d, err := decimal.NewFromString(valor)
		return &d, err
	}
	var err error
	switch campo {
	case "ticker":
		f.ticker = valor
	case "preco":
		f.preco, err = num()
	case "bid":
		f.bid, err = num()
	case "ask":
		f.ask, err = num()
	case "status84":
		f.status84, err = atoi(valor)
	case "idade":
		f.idade, err = time.ParseDuration(valor)
	case "http":
		var n int
		n, err = atoi(valor)
		if n == 404 || n == 400 {
			f.situacao = Indisponivel
			return err
		}
	default:
		return fmt.Errorf("campo de preço desconhecido %q", campo)
	}
	f.situacao = OK
	return err
}

// Vazio: corpo {}.
func (f *Falsa) Vazio() { f.mu.Lock(); f.situacao = Vazio; f.mu.Unlock() }

// Normal: volta a responder com os últimos campos.
func (f *Falsa) Normal() { f.mu.Lock(); f.situacao = OK; f.mu.Unlock() }

func (f *Falsa) Ler() Leitura {
	f.mu.Lock()
	defer f.mu.Unlock()
	agora := f.rel.Agora()
	l := Leitura{Situacao: f.situacao, LidaEm: agora}
	if f.situacao != OK {
		return l
	}
	l.Ticker, l.Preco, l.Bid, l.Ask, l.Status84 = f.ticker, f.preco, f.bid, f.ask, f.status84
	l.IdadeSeg = decimal.NewFromFloat(f.idade.Seconds())
	l.TS = agora.Add(-f.idade)
	return l
}

func atoi(s string) (int, error) {
	var n int
	_, err := fmt.Sscan(s, &n)
	return n, err
}
