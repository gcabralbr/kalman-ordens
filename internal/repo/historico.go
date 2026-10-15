package repo

import (
	"time"
)

// EventoSessao é uma linha de kalmanordemsessao (v1.5 R3).
type EventoSessao struct {
	Tipo  string // LOGON | LOGOUT | QUEDA | LOGON_RECUSADO | RESEND | CONCILIACAO | ALERTA
	Texto string
	Em    time.Time
}

func (m *Memoria) RegistrarSessao(e EventoSessao) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessao = append(m.sessao, e)
	return nil
}

func (m *Memoria) SessaoEntre(de, ate time.Time) ([]EventoSessao, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []EventoSessao
	for _, e := range m.sessao {
		if !e.Em.Before(de) && e.Em.Before(ate) {
			r = append(r, e)
		}
	}
	return r, nil
}

func (m *Memoria) DiaDaOperacao(mod string, op int64) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.ops {
		if o.Modulo == modulo(mod) && o.OperacaoID == op {
			return o.Data, true, nil
		}
	}
	return "", false, nil
}

func (m *Memoria) EventosFIXDasOrdens(ids []int64) ([]EventoFIX, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	quer := map[int64]bool{}
	for _, id := range ids {
		quer[id] = true
	}
	var r []EventoFIX
	for _, e := range m.eventos {
		if e.OrdemID != 0 && quer[e.OrdemID] {
			r = append(r, e)
		}
	}
	return r, nil
}

func (m *Memoria) PublicadosDaOperacao(mod string, op int64) ([]Publicado, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []Publicado
	for _, p := range m.pubs {
		if p.Modulo == modulo(mod) && p.OperacaoID == op {
			r = append(r, p)
		}
	}
	return r, nil
}

func (m *Memoria) PublicadosEntre(de, ate time.Time, tipo string) ([]Publicado, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []Publicado
	for _, p := range m.pubs {
		if (tipo == "" || p.Tipo == tipo) && !p.Em.Before(de) && p.Em.Before(ate) {
			r = append(r, p)
		}
	}
	return r, nil
}

// PedidoHTTP é uma linha de kalmanordempedido (v1.6 P1): um POST /ordens ou POST
// /operacoes/{id}/stop como chegou (corpo cru) e como foi respondido.
type PedidoHTTP struct {
	ID         int64
	Data       string // dia do pregão
	Modulo     string
	OperacaoID int64  // 0 = sem (corpo ilegível)
	UID        int    // 0 = sem
	Rota       string // ORDENS | STOP
	Papel      string // ENTRADA | SAIDA (ORDENS)
	HTTPStatus int
	Aceito     bool
	Codigo     string
	Corpo      string
	Resposta   string
	Em         time.Time
}

func (m *Memoria) RegistrarPedido(p *PedidoHTTP) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.Modulo = modulo(p.Modulo)
	p.ID = int64(len(m.pedidos) + 1)
	m.pedidos = append(m.pedidos, *p)
	return nil
}

func (m *Memoria) PedidosDoDia(data string, uid int, mod string) ([]PedidoHTTP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []PedidoHTTP
	for _, p := range m.pedidos {
		if p.Data == data && (uid == 0 || p.UID == uid) && (mod == "" || p.Modulo == mod) {
			r = append(r, p)
		}
	}
	return r, nil
}

func (m *Memoria) PedidosDaOperacao(mod string, op int64) ([]PedidoHTTP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []PedidoHTTP
	for _, p := range m.pedidos {
		if p.Modulo == modulo(mod) && p.OperacaoID == op {
			r = append(r, p)
		}
	}
	return r, nil
}
