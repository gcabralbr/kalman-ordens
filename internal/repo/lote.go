package repo

import (
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// Lote (v1.10 B): tudo o que uma volta do motor gravou, numa transação só. O motor atribui os IDs
// de operação e ordem (MaioresIDs na subida) para os eventos já apontarem para a ordem; operação e
// ordem são gravadas inteiras (upsert pelo ID), só as que mudaram. A deduplicação de fill (37, 17)
// é do motor, em memória (ChavesFillDoDia); no banco, a chave única continua e a linha repetida é
// ignorada.
type Lote struct {
	// novas: INSERT (uma colisão na chave única é erro, nunca a atualização de outra linha);
	// as já gravadas: upsert pelo ID
	NovasOperacoes []dominio.Operacao
	Operacoes      []dominio.Operacao
	NovasOrdens    []dominio.Ordem
	Ordens         []dominio.Ordem
	Eventos        []EventoFIX
	Publicados     []PublicadoNovo
	Estados        []EstadoNovo
	Sessao         []EventoSessao
	Pedidos        []PedidoHTTP
}

// PublicadoNovo: um evento para o gestor (o seq vem do banco).
type PublicadoNovo struct {
	Tipo, Modulo string
	OperacaoID   int64
	Dados        []byte
	Em           time.Time
}

// EstadoNovo: kalmanordemestado (chave → valor).
type EstadoNovo struct {
	Chave, Valor string
	Em           time.Time
}

// Vazio: nada a gravar.
func (l *Lote) Vazio() bool { return l.Linhas() == 0 }

// Linhas: total de linhas do lote (métrica).
func (l *Lote) Linhas() int {
	return len(l.NovasOperacoes) + len(l.Operacoes) + len(l.NovasOrdens) + len(l.Ordens) + len(l.Eventos) + len(l.Publicados) + len(l.Estados) + len(l.Sessao) + len(l.Pedidos)
}

// ChaveFill: a chave da deduplicação de execução (v1.3 H1).
func ChaveFill(orderID, execID string) string { return orderID + "\x00" + execID }

// ---------------------------------------------------------------- memória

func (m *Memoria) GravarLote(l *Lote) error {
	for _, os := range [][]dominio.Operacao{l.NovasOperacoes, l.Operacoes} {
		for _, o := range os {
			o.Modulo = modulo(o.Modulo)
			m.upsertOp(o)
		}
	}
	for _, os := range [][]dominio.Ordem{l.NovasOrdens, l.Ordens} {
		for _, o := range os {
			o.Modulo = modulo(o.Modulo)
			m.upsertOrdem(o)
		}
	}
	for _, e := range l.Eventos {
		if _, err := m.RegistrarEvento(e); err != nil {
			return err
		}
	}
	for _, p := range l.Publicados {
		if _, err := m.Publicar(p.Tipo, p.Modulo, p.OperacaoID, p.Dados, p.Em); err != nil {
			return err
		}
	}
	for _, e := range l.Estados {
		_ = m.GravarEstado(e.Chave, e.Valor, e.Em)
	}
	for _, s := range l.Sessao {
		_ = m.RegistrarSessao(s)
	}
	for i := range l.Pedidos {
		_ = m.RegistrarPedido(&l.Pedidos[i])
	}
	return nil
}

func (m *Memoria) upsertOp(o dominio.Operacao) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID > m.proxOp {
		m.proxOp = o.ID
	}
	for i := range m.ops {
		if m.ops[i].ID == o.ID {
			m.ops[i] = o.Copia()
			return
		}
	}
	m.ops = append(m.ops, o.Copia())
}

func (m *Memoria) upsertOrdem(o dominio.Ordem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID > m.proxOrdem {
		m.proxOrdem = o.ID
	}
	for i := range m.ordens {
		if m.ordens[i].ID == o.ID {
			m.ordens[i] = o
			return
		}
	}
	m.ordens = append(m.ordens, o)
}

func (m *Memoria) MaioresIDs() (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proxOp, m.proxOrdem, nil
}

func (m *Memoria) ChavesFillDoDia(de, ate time.Time) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := map[string]bool{}
	for _, e := range m.eventos {
		if e.OrderID != "" && e.ExecID != "" && !e.Em.Before(de) && e.Em.Before(ate) {
			r[ChaveFill(e.OrderID, e.ExecID)] = true
		}
	}
	return r, nil
}

func (m *Memoria) OperacoesExistentes(mod string, ids []int64) (map[int64]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	quer := map[int64]bool{}
	for _, id := range ids {
		quer[id] = true
	}
	out := map[int64]bool{}
	for _, x := range m.ops {
		if x.Modulo == modulo(mod) && quer[x.OperacaoID] {
			out[x.OperacaoID] = true
		}
	}
	return out, nil
}

func (m *Memoria) ConfigUsuarios(uids []int) (map[int][2]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := map[int][2]string{}
	for _, uid := range uids {
		if u := m.usuarios[uid]; u != nil {
			r[uid] = *u
		}
	}
	return r, nil
}
