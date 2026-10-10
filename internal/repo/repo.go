// Package repo é a persistência do serviço (Parte 8). Interface + implementação em memória
// (roteiros e testes). A de MariaDB implementa a mesma interface.
package repo

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// EventoFIX é uma linha de kalmanordemevento.
type EventoFIX struct {
	OrdemID   int64
	Direcao   string // ENV | REC
	MsgType   string
	ExecType  string
	OrdStatus string
	ClOrdID   string
	OrderID   string
	ExecID    string // único quando presente
	LastQty   *int
	LastPx    *decimal.Decimal
	CumQty    *int
	AvgPx     *decimal.Decimal
	Texto     string
	Codigo    string
	Bruta     string
	Em        time.Time
}

// Publicado é um evento para o gestor (GET /eventos).
type Publicado struct {
	Seq        int64           `json:"seq"`
	Tipo       string          `json:"tipo"`
	OperacaoID int64           `json:"operacao_id,omitempty"`
	Dados      json.RawMessage `json:"dados"`
	Em         time.Time       `json:"em"`
}

// Repositorio.
type Repositorio interface {
	// SalvarOperacao insere (ID == 0, preenche o ID) ou atualiza.
	SalvarOperacao(*dominio.Operacao) error
	OperacaoExiste(operacaoID int64) (bool, error)
	// SalvarOrdem insere (ID == 0) ou atualiza.
	SalvarOrdem(*dominio.Ordem) error
	// RegistrarEvento devolve novo=false se o ExecID já existe (fill repetido).
	RegistrarEvento(EventoFIX) (novo bool, err error)
	EnviosDesde(t time.Time) (int, error)
	Publicar(tipo string, operacaoID int64, dados []byte, em time.Time) (int64, error)
	Publicados(desde int64, limite int) ([]Publicado, error)
	LerEstado(chave string) (string, bool, error)
	GravarEstado(chave, valor string, em time.Time) error
	// CarregarDia: operações e ordens do dia, em ordem de criação.
	CarregarDia(data string) ([]dominio.Operacao, []dominio.Ordem, error)
}

// ErrDuplicada: operacao_id já existe.
var ErrDuplicada = errors.New("repo: operação duplicada")

// Memoria guarda cópias (o que o motor tem em memória não vaza para cá).
type Memoria struct {
	mu        sync.Mutex
	ops       []dominio.Operacao
	ordens    []dominio.Ordem
	eventos   []EventoFIX
	execIDs   map[string]bool
	pubs      []Publicado
	estado    map[string]string
	proxOp    int64
	proxOrdem int64
}

func NovaMemoria() *Memoria {
	return &Memoria{execIDs: map[string]bool{}, estado: map[string]string{}}
}

func (m *Memoria) SalvarOperacao(o *dominio.Operacao) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID == 0 {
		for _, x := range m.ops {
			if x.OperacaoID == o.OperacaoID {
				return ErrDuplicada
			}
		}
		m.proxOp++
		o.ID = m.proxOp
		m.ops = append(m.ops, o.Copia())
		return nil
	}
	for i := range m.ops {
		if m.ops[i].ID == o.ID {
			m.ops[i] = o.Copia()
			return nil
		}
	}
	return errors.New("repo: operação inexistente")
}

func (m *Memoria) OperacaoExiste(id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.ops {
		if x.OperacaoID == id {
			return true, nil
		}
	}
	return false, nil
}

func (m *Memoria) SalvarOrdem(o *dominio.Ordem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID == 0 {
		m.proxOrdem++
		o.ID = m.proxOrdem
		m.ordens = append(m.ordens, *o)
		return nil
	}
	for i := range m.ordens {
		if m.ordens[i].ID == o.ID {
			m.ordens[i] = *o
			return nil
		}
	}
	return errors.New("repo: ordem inexistente")
}

func (m *Memoria) RegistrarEvento(e EventoFIX) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ExecID != "" {
		if m.execIDs[e.ExecID] {
			return false, nil
		}
		m.execIDs[e.ExecID] = true
	}
	m.eventos = append(m.eventos, e)
	return true, nil
}

func (m *Memoria) EnviosDesde(t time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.eventos {
		if e.Direcao == "ENV" && (e.MsgType == "D" || e.MsgType == "F" || e.MsgType == "G") && !e.Em.Before(t) {
			n++
		}
	}
	return n, nil
}

func (m *Memoria) Publicar(tipo string, op int64, dados []byte, em time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := Publicado{Seq: int64(len(m.pubs) + 1), Tipo: tipo, OperacaoID: op, Dados: append(json.RawMessage(nil), dados...), Em: em}
	m.pubs = append(m.pubs, p)
	return p.Seq, nil
}

func (m *Memoria) Publicados(desde int64, limite int) ([]Publicado, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []Publicado
	for _, p := range m.pubs {
		if p.Seq > desde {
			r = append(r, p)
			if len(r) == limite {
				break
			}
		}
	}
	return r, nil
}

func (m *Memoria) LerEstado(k string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.estado[k]
	return v, ok, nil
}

func (m *Memoria) GravarEstado(k, v string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.estado[k] = v
	return nil
}

func (m *Memoria) CarregarDia(data string) ([]dominio.Operacao, []dominio.Ordem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ops []dominio.Operacao
	for _, o := range m.ops {
		if o.Data == data {
			ops = append(ops, o.Copia())
		}
	}
	var ords []dominio.Ordem
	for _, o := range m.ordens {
		if o.Data == data {
			ords = append(ords, o)
		}
	}
	sort.SliceStable(ords, func(i, j int) bool { return ords[i].ID < ords[j].ID })
	return ops, ords, nil
}

// Eventos devolve os eventos FIX gravados (testes).
func (m *Memoria) Eventos() []EventoFIX {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]EventoFIX(nil), m.eventos...)
}
