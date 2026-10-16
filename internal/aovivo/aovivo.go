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
	rel relogio.Relogio

	mu         sync.Mutex
	buf        []Mensagem // circular
	ini        int        // índice da mais antiga
	n          int
	seq        int64
	ultimaRec  time.Time
	ultimaEnv  time.Time
	seqEntrada int
	seqSaida   int
	logonEm    time.Time // último 35=A recebido (logon aceito)
	porSegundo map[int64]int
	donos      map[chaveOp]int // (modulo, operacao_id) → uid, informado pelo motor
}

// Novo monitor com o relógio do serviço (o dos roteiros, nos testes).
func Novo(rel relogio.Relogio) *Monitor {
	return &Monitor{rel: rel, buf: make([]Mensagem, Capacidade), porSegundo: map[int64]int{}, donos: map[chaveOp]int{}}
}

// Associar: o uid da operação (o motor chama ao criar ou carregar cada operação).
func (m *Monitor) Associar(modulo string, operacaoID int64, uid int) {
	m.mu.Lock()
	m.donos[chaveOp{modulo, operacaoID}] = uid
	m.mu.Unlock()
}

// reClOrd: KM-<papel>-<op>-<n> (KALMAN) ou KM-<MODULO>-<papel>-<op>-<n> (v1.3 M3).
var reClOrd = regexp.MustCompile(`^KM-(?:([A-Z][A-Z0-9]{1,7})-)?[EPS]-(\d+)-\d+$`)

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
	m.porSegundo[seg]++
	if len(m.porSegundo) > 120 {
		for s := range m.porSegundo {
			if s < seg-60 {
				delete(m.porSegundo, s)
			}
		}
	}
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
	id, err := strconv.ParseInt(p[2], 10, 64)
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
	for s, c := range m.porSegundo {
		if s > agora.Unix()-60 {
			minuto += c
		}
	}
	p50, max := m.latenciaAceite()
	saude := map[string]any{
		"logada": e.Logada, "logada_desde": nil, "ultima_recebida_em": emOuNulo(m.ultimaRec), "ultima_enviada_em": emOuNulo(m.ultimaEnv),
		"seq_entrada": m.seqEntrada, "seq_saida": m.seqSaida, "msgs_min": minuto, "fila": e.Fila, "parada": e.Parada,
		"latencia_aceite_p50_ms": p50, "latencia_aceite_max_ms": max,
	}
	if e.Logada {
		saude["logada_desde"] = emOuNulo(m.logonEm)
	}
	return map[string]any{"mensagens": lista, "ultimo_seq": m.seq, "saude": saude}
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
