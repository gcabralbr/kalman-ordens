// Package aovivo é o monitor ao vivo da sessão FIX (contrato oficial da tela de admin do Kalman):
// as últimas mensagens que passaram no fio — de aplicação e de sessão, nos dois sentidos — num
// buffer em memória, e o estado da sessão para GET /sessao/ao-vivo. Nada vai ao banco.
//
// As mensagens chegam pela camada de sessão (pnt.ConfigSessao.Espelho), na goroutine da
// biblioteca FIX; a consulta vem das goroutines HTTP. Tudo sob um mutex curto: o registro só copia
// alguns campos e o texto mascarado.
package aovivo

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// Capacidade: as últimas 2.000 mensagens.
const Capacidade = 2000

// LimitePadrao e LimiteMax da consulta (?limite=).
const (
	LimitePadrao = 500
	LimiteMax    = Capacidade
)

// CamposPrincipais: as tags copiadas para "campos".
var CamposPrincipais = []int{11, 37, 39, 150, 54, 38, 44, 31, 32, 58}

const formatoEm = "2006-01-02T15:04:05.000-07:00"

// Mensagem: uma mensagem do buffer, no formato do contrato.
type Mensagem struct {
	Seq        int64             `json:"seq"`
	Em         string            `json:"em"`
	Direcao    string            `json:"direcao"` // ENV (do serviço ao PnT) | REC (do PnT)
	MsgType    string            `json:"msgtype"`
	Campos     map[string]string `json:"campos"`
	OperacaoID *int64            `json:"operacao_id"`
	UID        *int              `json:"uid"`
	FixRaw     string            `json:"fix_raw"`

	em       time.Time
	seqFIX   int
	clordid  string
	ordStat  string
	execTipo string
}

type chaveOp struct {
	modulo string
	id     int64
}

// Monitor: o buffer e o estado da sessão visto no fio.
type Monitor struct {
	rel  relogio.Relogio
	boot time.Time // subida do processo (a tela detecta o restart e o buffer novo)

	mu         sync.Mutex
	buf        []Mensagem // circular
	ini        int        // índice da mais antiga
	n          int
	seq        int64
	ultimaRec  time.Time
	ultimaEnv  time.Time
	seqEntrada int
	seqSaida   int
	logonEm    time.Time          // último 35=A recebido (logon aceito)
	porSegundo map[int64]*Segundo // ritmo: os últimos JanelaRitmo segundos
	donos      map[chaveOp]int    // (modulo, operacao_id) → uid, informado pelo motor
}

// Novo monitor com o relógio do serviço (o dos roteiros, nos testes).
func Novo(rel relogio.Relogio) *Monitor {
	return &Monitor{rel: rel, boot: rel.Agora(), buf: make([]Mensagem, Capacidade), porSegundo: map[int64]*Segundo{}, donos: map[chaveOp]int{}}
}

// Associar: o uid da operação (o motor chama ao criar ou carregar cada operação).
func (m *Monitor) Associar(modulo string, operacaoID int64, uid int) {
	m.mu.Lock()
	m.donos[chaveOp{modulo, operacaoID}] = uid
	m.mu.Unlock()
}

// reClOrd: KM-<papel>-<op>-<n> (KALMAN) ou KM-<MODULO>-<papel>-<op>-<n> (v1.3 M3).
var reClOrd = regexp.MustCompile(`^KM-(?:([A-Z][A-Z0-9]{1,7})-)?([EPS])-(\d+)-\d+$`)

// Registrar: uma mensagem que passou no fio (entrada = do PnT). Chamado pela camada de sessão.
func (m *Monitor) Registrar(entrada bool, bruta []byte) {
	agora := m.rel.Agora()
	msg := pnt.Ler(string(bruta))
	x := Mensagem{Direcao: "ENV", MsgType: msg.Texto(35), Campos: map[string]string{}, em: agora,
		FixRaw:  strings.ReplaceAll(pnt.MascararTexto(string(bruta)), "\x01", "|"),
		clordid: msg.Texto(11), ordStat: msg.Texto(39), execTipo: msg.Texto(150)}
	if entrada {
		x.Direcao = "REC"
	}
	x.seqFIX, _ = msg.Inteiro(34)
	for _, t := range CamposPrincipais {
		if v, ok := msg.Valor(t); ok {
			x.Campos[strconv.Itoa(t)] = v
		}
	}
	modulo, op, ehKM := operacaoDo(msg.Texto(11))
	if !ehKM {
		modulo, op, ehKM = operacaoDo(msg.Texto(41))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if ehKM {
		x.OperacaoID = &op
		if uid, ok := m.donos[chaveOp{modulo, op}]; ok {
			x.UID = &uid
		}
	}
	m.seq++
	x.Seq = m.seq
	x.Em = agora.In(relogio.Fuso).Format(formatoEm)
	if entrada {
		m.ultimaRec = agora
		if x.seqFIX > 0 {
			m.seqEntrada = x.seqFIX
		}
		if x.MsgType == "A" {
			m.logonEm = agora
		}
	} else {
		m.ultimaEnv = agora
		if x.seqFIX > 0 {
			m.seqSaida = x.seqFIX
		}
	}
	seg := agora.Unix()
	b := m.porSegundo[seg]
	if b == nil {
		b = &Segundo{}
		m.porSegundo[seg] = b
		if len(m.porSegundo) > JanelaRitmo+10 {
			for s := range m.porSegundo {
				if s <= seg-JanelaRitmo {
					delete(m.porSegundo, s)
				}
			}
		}
	}
	b.contar(entrada, x.MsgType, x.execTipo, papelDo(msg.Texto(11)))
	if m.n < Capacidade {
		m.buf[(m.ini+m.n)%Capacidade] = x
		m.n++
	} else {
		m.buf[m.ini] = x
		m.ini = (m.ini + 1) % Capacidade
	}
}

func operacaoDo(cl string) (string, int64, bool) {
	p := reClOrd.FindStringSubmatch(cl)
	if p == nil {
		return "", 0, false
	}
	id, err := strconv.ParseInt(p[3], 10, 64)
	if err != nil {
		return "", 0, false
	}
	modulo := p[1]
	if modulo == "" {
		modulo = "KALMAN"
	}
	return modulo, id, true
}

// Estado: o que a sessão não sabe pelo fio (vem do serviço).
type Estado struct {
	Logada bool
	Fila   int
	Parada any // a parada ativa (objeto do GET /saude) ou nil
	// BolsaIndisponivelDesde: o disjuntor de bolsa indisponível (SPEC v1.10 §9); zero = fora dele
	BolsaIndisponivelDesde time.Time
}

// Consultar: as mensagens com seq > desde (no máximo limite, as mais antigas primeiro), o último
// seq e a saúde. Se desde é anterior ao buffer, começa na mais antiga que ainda está nele.
func (m *Monitor) Consultar(desde int64, limite int, e Estado) map[string]any {
	if limite <= 0 {
		limite = LimitePadrao
	}
	limite = min(limite, LimiteMax)
	agora := m.rel.Agora()
	m.mu.Lock()
	defer m.mu.Unlock()
	lista := make([]Mensagem, 0, min(limite, m.n))
	for i := 0; i < m.n && len(lista) < limite; i++ {
		x := m.buf[(m.ini+i)%Capacidade]
		if x.Seq > desde {
			lista = append(lista, x)
		}
	}
	minuto := 0
	for s, b := range m.porSegundo {
		if s > agora.Unix()-60 {
			minuto += b.total
		}
	}
	p50, max := m.latenciaAceite()
	saude := map[string]any{
		"boot": emOuNulo(m.boot), "logada": e.Logada, "logada_desde": nil, "ultima_recebida_em": emOuNulo(m.ultimaRec), "ultima_enviada_em": emOuNulo(m.ultimaEnv),
		"seq_entrada": m.seqEntrada, "seq_saida": m.seqSaida, "msgs_min": minuto, "fila": e.Fila, "parada": e.Parada,
		"latencia_aceite_p50_ms": p50, "latencia_aceite_max_ms": max,
		"bolsa_indisponivel_desde": emOuNulo(e.BolsaIndisponivelDesde),
	}
	if e.Logada {
		saude["logada_desde"] = emOuNulo(m.logonEm)
	}
	return map[string]any{"mensagens": lista, "ultimo_seq": m.seq, "saude": saude, "ritmo": m.ritmo(agora)}
}

// latenciaAceite: no buffer, de cada NewOrderSingle (35=D) enviada até a primeira resposta do PnT
// que a aceita (35=8 com 39 = 0, 1 ou 2 para o mesmo 11). nil sem par no buffer. Com m.mu.
func (m *Monitor) latenciaAceite() (any, any) {
	enviadas := map[string]time.Time{}
	var ms []float64
	for i := 0; i < m.n; i++ {
		x := &m.buf[(m.ini+i)%Capacidade]
		switch {
		case x.Direcao == "ENV" && x.MsgType == "D" && x.clordid != "":
			enviadas[x.clordid] = x.em
		case x.Direcao == "REC" && x.MsgType == "8" && (x.ordStat == "0" || x.ordStat == "1" || x.ordStat == "2"):
			if t, ok := enviadas[x.clordid]; ok {
				ms = append(ms, float64(x.em.Sub(t))/float64(time.Millisecond))
				delete(enviadas, x.clordid)
			}
		}
	}
	if len(ms) == 0 {
		return nil, nil
	}
	sort.Float64s(ms)
	r := func(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
	return r(ms[(len(ms)-1)/2]), r(ms[len(ms)-1])
}

func emOuNulo(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.In(relogio.Fuso).Format(formatoEm)
}

// ---------------------------------------------------------------- ritmo

// JanelaRitmo: o ritmo cobre os últimos 300 s, um item por segundo.
const JanelaRitmo = 300

// Segundo: as mensagens de um segundo, por tipo (contrato do ritmo).
type Segundo struct {
	T            string    `json:"t"`
	Enviadas     Enviadas  `json:"enviadas"`
	Recebidas    Recebidas `json:"recebidas"`
	Sessao       Sessao    `json:"sessao"`
	HeartbeatEnv int       `json:"heartbeat_env"`
	HeartbeatRec int       `json:"heartbeat_rec"`
	total        int
}

// Enviadas: 35=D pelo papel do ClOrdID (E, P, S), 35=F, 35=G, 35=AF.
type Enviadas struct {
	Entrada  int `json:"entrada"`
	Protecao int `json:"protecao"`
	Saida    int `json:"saida"`
	Cancel   int `json:"cancel"`
	Replace  int `json:"replace"`
	AF       int `json:"af"`
}

// Recebidas: 35=8 pelo 150 (A, 0, F, 4, 5, 8, 7; 6/E/I/D = status; o resto = outras), 35=9, 35=3,
// 35=j.
type Recebidas struct {
	AceiteA      int `json:"aceite_A"`
	Aceite0      int `json:"aceite_0"`
	Fill         int `json:"fill"`
	Cancelada    int `json:"cancelada"`
	ReplaceOK    int `json:"replace_ok"`
	Rejeicao     int `json:"rejeicao"`
	Erro         int `json:"erro"`
	Status       int `json:"status"`
	Outras       int `json:"outras"`
	CancelReject int `json:"cancel_reject"`
	RejectSessao int `json:"reject_sessao"`
	BmrJ         int `json:"bmr_j"`
}

// Sessao: mensagens de sessão (fora o heartbeat), cada uma {env, rec}.
type Sessao struct {
	TestRequest Sentidos `json:"test_request"`
	Resend      Sentidos `json:"resend"`
	SeqReset    Sentidos `json:"seq_reset"`
	Logon       Sentidos `json:"logon"`
	Logout      Sentidos `json:"logout"`
}

// Sentidos: enviadas pelo serviço (env) e recebidas do PnT (rec).
type Sentidos struct {
	Env int `json:"env"`
	Rec int `json:"rec"`
}

func (s *Sentidos) contar(entrada bool) {
	if entrada {
		s.Rec++
	} else {
		s.Env++
	}
}

func papelDo(cl string) string {
	if p := reClOrd.FindStringSubmatch(cl); p != nil {
		return p[2]
	}
	return ""
}

func (b *Segundo) contar(entrada bool, tipo, exec, papel string) {
	b.total++
	ses := &b.Sessao
	switch tipo {
	case "0":
		if entrada {
			b.HeartbeatRec++
		} else {
			b.HeartbeatEnv++
		}
		return
	case "1":
		ses.TestRequest.contar(entrada)
		return
	case "2":
		ses.Resend.contar(entrada)
		return
	case "4":
		ses.SeqReset.contar(entrada)
		return
	case "A":
		ses.Logon.contar(entrada)
		return
	case "5":
		ses.Logout.contar(entrada)
		return
	}
	if !entrada {
		e := &b.Enviadas
		switch tipo {
		case "D":
			switch papel {
			case "E":
				e.Entrada++
			case "P":
				e.Protecao++
			case "S":
				e.Saida++
			}
		case "F":
			e.Cancel++
		case "G":
			e.Replace++
		case "AF":
			e.AF++
		}
		return
	}
	r := &b.Recebidas
	switch tipo {
	case "9":
		r.CancelReject++
	case "3":
		r.RejectSessao++
	case "j":
		r.BmrJ++
	case "8":
		switch exec {
		case "A":
			r.AceiteA++
		case "0":
			r.Aceite0++
		case "F":
			r.Fill++
		case "4":
			r.Cancelada++
		case "5":
			r.ReplaceOK++
		case "8":
			r.Rejeicao++
		case "7":
			r.Erro++
		case "6", "E", "I", "D":
			r.Status++
		default:
			r.Outras++
		}
	}
}

// ritmo: os últimos JanelaRitmo segundos até o atual, do mais antigo ao mais novo (segundo sem
// mensagem = zeros). Com m.mu.
func (m *Monitor) ritmo(agora time.Time) []Segundo {
	fim := agora.Unix()
	r := make([]Segundo, 0, JanelaRitmo)
	for s := fim - JanelaRitmo + 1; s <= fim; s++ {
		var x Segundo
		if b := m.porSegundo[s]; b != nil {
			x = *b
		}
		x.T = time.Unix(s, 0).In(relogio.Fuso).Format(formatoEm)
		r = append(r, x)
	}
	return r
}

// UltimoSeq: o seq da última mensagem registrada (bancada: o ponto de partida).
func (m *Monitor) UltimoSeq() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seq
}

// Desde: as mensagens do buffer com seq > desde (todas, em ordem).
func (m *Monitor) Desde(desde int64) []Mensagem {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r []Mensagem
	for i := 0; i < m.n; i++ {
		if x := m.buf[(m.ini+i)%Capacidade]; x.Seq > desde {
			r = append(r, x)
		}
	}
	return r
}
