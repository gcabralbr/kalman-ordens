// Package repo é a persistência do serviço (Parte 8). Interface + implementação em memória
// (roteiros e testes). A de MariaDB implementa a mesma interface.
package repo

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// EventoFIX é uma linha de kalmanordemevento.
type EventoFIX struct {
	ID        int64 // preenchido na leitura
	OrdemID   int64
	Modulo    string // o da ordem; vazio = KALMAN
	Direcao   string // ENV | REC
	MsgType   string
	ExecType  string
	OrdStatus string
	ClOrdID   string
	OrderID   string
	ExecID    string // único por OrderID quando os dois estão presentes (v1.3 H1)
	// Ambiente (v1.26, 0015): o da instância que trocou a mensagem; vazio = SIMULADOR
	Ambiente string
	LastQty  *int
	LastPx   *decimal.Decimal
	CumQty   *int
	AvgPx    *decimal.Decimal
	Texto    string
	Codigo   string
	Bruta    string
	Em       time.Time
}

// Publicado é um evento para o gestor (GET /eventos).
type Publicado struct {
	Seq        int64           `json:"seq"`
	Tipo       string          `json:"tipo"`
	Modulo     string          `json:"modulo"`
	OperacaoID int64           `json:"operacao_id,omitempty"`
	Dados      json.RawMessage `json:"dados"`
	Em         time.Time       `json:"em"`
}

// Repositorio.
type Repositorio interface {
	// SalvarOperacao insere (ID == 0, preenche o ID) ou atualiza.
	SalvarOperacao(*dominio.Operacao) error
	OperacaoExiste(modulo string, operacaoID int64) (bool, error)
	// SalvarOrdem insere (ID == 0) ou atualiza.
	SalvarOrdem(*dominio.Ordem) error
	// RegistrarEvento devolve novo=false se o par (OrderID, ExecID) já existe (fill repetido na
	// mesma ordem). O mesmo ExecID em outra ordem é outro evento (v1.3 H1).
	RegistrarEvento(EventoFIX) (novo bool, err error)
	EnviosDesde(t time.Time, ambiente string) (int, error)
	// Publicar: modulo vazio = KALMAN (eventos do serviço, alertas sem operação).
	Publicar(tipo, modulo string, operacaoID int64, dados []byte, em time.Time) (int64, error)
	// Publicados: modulo vazio = todos.
	Publicados(desde int64, limite int, modulo string) ([]Publicado, error)
	LerEstado(chave string) (string, bool, error)
	GravarEstado(chave, valor string, em time.Time) error
	// CarregarDia: operações e ordens do dia, em ordem de criação.
	CarregarDia(data string) ([]dominio.Operacao, []dominio.Ordem, error)

	// Habilitação por (uid, ambiente) (v1.2 U5, SPEC-AMBIENTES-v1 §4.2): lida a cada entrada, gravada
	// só pela CLI (e pelos roteiros). Ambiente vazio = SIMULADOR.
	Habilitacao(uid int, ambiente string) (dominio.Habilitacao, bool, error)
	Habilitacoes() ([]dominio.Habilitacao, error)
	// GravarHabilitacao substitui a atual e acrescenta ao histórico.
	GravarHabilitacao(dominio.Habilitacao) error
	// AlterarHabilitado (v1.8 H2, admin): troca só habilitado, por, motivo e em, preservando os
	// limites, com o histórico; atômico. Sem linha, ou já no valor pedido, não grava nada.
	// Devolve a habilitação de antes e se havia linha.
	AlterarHabilitado(uid int, ambiente string, habilitado bool, por, motivo string, em time.Time) (antes dominio.Habilitacao, existia bool, err error)

	// Histórico (v1.5 R): consultas de qualquer dia, para a tela de admin e a CLI.
	// DiaDaOperacao: o dia (pregão) da operação (modulo vazio = KALMAN).
	DiaDaOperacao(modulo string, operacaoID int64) (string, bool, error)
	EventosFIXDasOrdens(ordemIDs []int64) ([]EventoFIX, error)
	// EventosFlexscanEntre (28/10): os eventos da ponte Flexscan (msgtype FLX-*) em [de, ate), os
	// limite mais recentes, em ordem; o tráfego da aba "Flexscan ao vivo" recarregado na partida.
	EventosFlexscanEntre(de, ate time.Time, limite int, ambiente string) ([]EventoFIX, error)
	PublicadosDaOperacao(modulo string, operacaoID int64) ([]Publicado, error)
	// PublicadosEntre: [de, ate); tipo vazio = todos.
	PublicadosEntre(de, ate time.Time, tipo string) ([]Publicado, error)
	// Eventos da sessão FIX e gerais (v1.5 R3).
	RegistrarSessao(EventoSessao) error
	SessaoEntre(de, ate time.Time) ([]EventoSessao, error)
	// Pedidos HTTP (v1.6 P1). PedidosDoDia: uid 0 = todos; modulo vazio = todos.
	RegistrarPedido(*PedidoHTTP) error
	PedidosDoDia(data string, uid int, modulo string) ([]PedidoHTTP, error)
	PedidosDaOperacao(modulo string, operacaoID int64) ([]PedidoHTTP, error)

	// Lote (v1.10 B): o motor grava uma volta inteira numa transação; IDs de operação e ordem são
	// atribuídos por ele (a partir de MaioresIDs); a deduplicação de fill é dele (ChavesFillDoDia).
	GravarLote(*Lote) error
	MaioresIDs() (operacao, ordem int64, err error)
	ChavesFillDoDia(de, ate time.Time, ambiente string) (map[string]bool, error)
	// ConfigUsuarios: a configuração de vários uids de uma vez (cache do motor); uid sem linha não vem.
	ConfigUsuarios(uids []int) (map[int][2]string, error)
	// OperacoesExistentes: dos operacao_id dados, os que já existem no módulo (qualquer dia) —
	// o POST /sinais confere a fatia inteira numa consulta (v1.10 §6).
	OperacoesExistentes(modulo string, ids []int64) (map[int64]bool, error)

	// Quarentena do lote (v1.13): a linha recusada por erro de dado; pendente = não resolvida.
	Quarentenar(Quarentena) (int64, error)
	QuarentenaPendente() ([]Quarentena, error)
	ResolverQuarentena(id int64, por, resolucao string, em time.Time) (bool, error)

	// ConfigUsuario lê a configuração do usuário no GQ (dbecotools.acusuario, v1.2 U1):
	// usutxtconfig (HML) e usutxtconfigprd (PRODUCAO). achou=false: uid sem linha.
	ConfigUsuario(uid int) (hml, prd string, achou bool, err error)
}

// UsuariosDeTeste: os roteiros e testes definem a configuração do GQ (no MariaDB, numa tabela
// do schema de teste, nunca no dbecotools).
type UsuariosDeTeste interface {
	DefinirUsuario(uid int, campo, valor string) error
}

// ErrDuplicada: operacao_id já existe.
var ErrDuplicada = errors.New("repo: operação duplicada")

// Memoria guarda cópias (o que o motor tem em memória não vaza para cá).
type Memoria struct {
	mu         sync.Mutex
	ops        []dominio.Operacao
	ordens     []dominio.Ordem
	eventos    []EventoFIX
	execIDs    map[string]bool
	pubs       []Publicado
	estado     map[string]string
	proxOp     int64
	proxOrdem  int64
	habil      map[chaveHabil]dominio.Habilitacao
	hist       []dominio.Habilitacao
	usuarios   map[int]*[2]string // uid → {usutxtconfig, usutxtconfigprd}
	sessao     []EventoSessao
	pedidos    []PedidoHTTP
	proxEvento int64
	quarentena []Quarentena
}

func NovaMemoria() *Memoria {
	return &Memoria{execIDs: map[string]bool{}, estado: map[string]string{}, habil: map[chaveHabil]dominio.Habilitacao{}, usuarios: map[int]*[2]string{}}
}

// chaveHabil: a habilitação é por (uid, ambiente).
type chaveHabil struct {
	uid      int
	ambiente string
}

func (m *Memoria) Habilitacao(uid int, ambiente string) (dominio.Habilitacao, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.habil[chaveHabil{uid, dominio.AmbienteDe(ambiente)}]
	if !ok {
		h = dominio.Habilitacao{UID: uid, Ambiente: dominio.AmbienteDe(ambiente)}
	}
	return h, ok, nil
}

func (m *Memoria) Habilitacoes() ([]dominio.Habilitacao, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []dominio.Habilitacao
	for _, h := range m.habil {
		r = append(r, h)
	}
	sort.Slice(r, func(i, j int) bool {
		if r[i].UID != r[j].UID {
			return r[i].UID < r[j].UID
		}
		return r[i].Ambiente > r[j].Ambiente // SIMULADOR antes do REAL
	})
	return r, nil
}

func (m *Memoria) GravarHabilitacao(h dominio.Habilitacao) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h.Ambiente = dominio.AmbienteDe(h.Ambiente)
	m.habil[chaveHabil{h.UID, h.Ambiente}] = h
	m.hist = append(m.hist, h)
	return nil
}

func (m *Memoria) AlterarHabilitado(uid int, ambiente string, habilitado bool, por, motivo string, em time.Time) (dominio.Habilitacao, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := chaveHabil{uid, dominio.AmbienteDe(ambiente)}
	antes, ok := m.habil[k]
	if !ok || antes.Habilitado == habilitado {
		return antes, ok, nil
	}
	h := antes
	h.Habilitado, h.Por, h.Motivo, h.Em = habilitado, por, motivo, em
	m.habil[k] = h
	m.hist = append(m.hist, h)
	return antes, true, nil
}

func (m *Memoria) ConfigUsuario(uid int) (string, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.usuarios[uid]
	if u == nil {
		return "", "", false, nil
	}
	return u[0], u[1], true, nil
}

// DefinirUsuario: campo "hml" (usutxtconfig), "prd" (usutxtconfigprd) ou "ausente" (apaga a linha).
func (m *Memoria) DefinirUsuario(uid int, campo, valor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if campo == "ausente" {
		delete(m.usuarios, uid)
		return nil
	}
	u := m.usuarios[uid]
	if u == nil {
		u = &[2]string{}
		m.usuarios[uid] = u
	}
	switch campo {
	case "hml":
		u[0] = valor
	case "prd":
		u[1] = valor
	default:
		return errors.New("repo: campo do usuário deve ser hml, prd ou ausente")
	}
	return nil
}

func (m *Memoria) SalvarOperacao(o *dominio.Operacao) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID == 0 {
		o.Modulo = modulo(o.Modulo) // como o padrão da coluna
		for _, x := range m.ops {
			if x.Modulo == o.Modulo && x.OperacaoID == o.OperacaoID {
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

func (m *Memoria) OperacaoExiste(mod string, id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.ops {
		if x.Modulo == modulo(mod) && x.OperacaoID == id {
			return true, nil
		}
	}
	return false, nil
}

func (m *Memoria) SalvarOrdem(o *dominio.Ordem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o.ID == 0 {
		o.Modulo = modulo(o.Modulo)
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
	// como a chave única (orderid37, execid) do MariaDB: vazio em qualquer dos dois não conflita
	if e.ExecID != "" && e.OrderID != "" {
		chave := e.OrderID + "\x00" + e.ExecID + "\x00" + dominio.AmbienteDe(e.Ambiente)
		if m.execIDs[chave] {
			return false, nil
		}
		m.execIDs[chave] = true
	}
	m.proxEvento++
	e.ID = m.proxEvento
	m.eventos = append(m.eventos, e)
	return true, nil
}

func (m *Memoria) EnviosDesde(t time.Time, amb string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.eventos {
		if dominio.AmbienteDe(e.Ambiente) == dominio.AmbienteDe(amb) && e.Direcao == "ENV" && (e.MsgType == "D" || e.MsgType == "F" || e.MsgType == "G") && !e.Em.Before(t) {
			n++
		}
	}
	return n, nil
}

func (m *Memoria) Publicar(tipo, mod string, op int64, dados []byte, em time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := Publicado{Seq: int64(len(m.pubs) + 1), Tipo: tipo, Modulo: modulo(mod), OperacaoID: op, Dados: append(json.RawMessage(nil), dados...), Em: em}
	m.pubs = append(m.pubs, p)
	return p.Seq, nil
}

func (m *Memoria) Publicados(desde int64, limite int, mod string) ([]Publicado, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []Publicado
	for _, p := range m.pubs {
		if p.Seq > desde && (mod == "" || p.Modulo == mod) {
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
			c := o.Copia()
			c.Ambiente = dominio.AmbienteDe(c.Ambiente) // o padrão da coluna (0014)
			ops = append(ops, c)
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

func (m *Memoria) EventosFlexscanEntre(de, ate time.Time, limite int, amb string) ([]EventoFIX, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var es []EventoFIX
	for _, e := range m.eventos {
		if dominio.AmbienteDe(e.Ambiente) == dominio.AmbienteDe(amb) && strings.HasPrefix(e.MsgType, "FLX-") && !e.Em.Before(de) && e.Em.Before(ate) {
			es = append(es, e)
		}
	}
	if len(es) > limite {
		es = es[len(es)-limite:]
	}
	return es, nil
}
