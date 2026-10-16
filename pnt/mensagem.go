// Package pnt é a camada PnT: sessão FIX com o PnT DSA, montagem e leitura de mensagens.
// Não conhece quem a usa (Kalman, pair trade): só ClOrdID, 37, conta e mensagens.
package pnt

import (
	"strconv"
	"strings"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/shopspring/decimal"
)

// Campo é um par tag=valor, na ordem do fio.
type Campo struct {
	Tag   int
	Valor string
}

// Mensagem é uma mensagem FIX recebida ou enviada, com as tags na ordem do fio.
type Mensagem struct {
	Tipo   string
	Campos []Campo
	Bruta  string // com | no lugar de SOH
}

// Ler interpreta uma mensagem FIX crua (SOH como separador).
func Ler(bruta string) Mensagem {
	m := Mensagem{Bruta: strings.ReplaceAll(bruta, "\x01", "|")}
	for _, p := range strings.Split(strings.TrimSuffix(bruta, "\x01"), "\x01") {
		i := strings.IndexByte(p, '=')
		if i <= 0 {
			continue
		}
		tag, err := strconv.Atoi(p[:i])
		if err != nil {
			continue
		}
		m.Campos = append(m.Campos, Campo{tag, p[i+1:]})
		if tag == 35 {
			m.Tipo = p[i+1:]
		}
	}
	return m
}

// Texto devolve a primeira ocorrência da tag (vazio se ausente).
func (m Mensagem) Texto(tag int) string {
	v, _ := m.Valor(tag)
	return v
}

// Valor devolve a primeira ocorrência da tag.
func (m Mensagem) Valor(tag int) (string, bool) {
	for _, c := range m.Campos {
		if c.Tag == tag {
			return c.Valor, true
		}
	}
	return "", false
}

// Inteiro lê uma quantidade. Aceita "1" e "1.0"; recusa fração.
func (m Mensagem) Inteiro(tag int) (int, bool) {
	v, ok := m.Valor(tag)
	if !ok || v == "" {
		return 0, false
	}
	d, err := decimal.NewFromString(v)
	if err != nil || !d.Equal(d.Truncate(0)) {
		return 0, false
	}
	return int(d.IntPart()), true
}

// Decimal lê um preço.
func (m Mensagem) Decimal(tag int) (decimal.Decimal, bool) {
	v, ok := m.Valor(tag)
	if !ok || v == "" {
		return decimal.Zero, false
	}
	d, err := decimal.NewFromString(v)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

// PossDup diz se veio com PossDupFlag=Y (reenvio).
func (m Mensagem) PossDup() bool { return m.Texto(43) == "Y" }

// ------------------------------------------------------------ montagem

// Estratégias do PnT usadas.
const (
	EstrategiaAgendada = 1031 // limite DAY
	EstrategiaStop     = 1014 // stop loss / take profit
)

// Lados (54).
const (
	Compra = '1'
	Venda  = '2'
)

// Identificacao vai em toda ordem. [PnT-3]: se o usuário novo exigir PartyIDs, é aqui.
type Identificacao struct {
	Conta            string // 1, com dígito
	Bolsa            string // 207
	OnBehalfOfSubID  string // 116 (cabeçalho), opcional
	SenderLocationID string // 142 (cabeçalho), opcional
}

// Ordem é uma ordem single-leg da DSA (1031 ou 1014).
type Ordem struct {
	ClOrdID    string
	Simbolo    string
	Lado       byte // Compra ou Venda
	Qtd        int
	Preco      int64 // 44
	Estrategia int   // 847
	Stop       int64 // 20828 (só 1014)
	StopLim    int64 // 20829 (só 1014)
}

// NovaOrdem monta o NewOrderSingle (35=D). Para 1014, o 44 é igual ao 20829 (exemplo do RoE).
// 20856=Y (ActivateErrorStatusStrategy) em toda ordem nova: a ordem que entra em estado de erro
// (39=7) é reativada sozinha pelo PnT (150=D/39=0), como no log real de HML (SPEC v1.1 F14). O RoE
// só lista a tag na Sniper e no multileg; a rodada 1 da F1 confirma se a HML aceita em 1031/1014.
func NovaOrdem(id Identificacao, o Ordem, agora time.Time) *quickfix.Message {
	m := base(id, "D")
	corpoOrdem(m, id, o, agora)
	m.Body.SetString(quickfix.Tag(20856), "Y")
	return m
}

// Replace monta o OrderCancelReplaceRequest (35=G): a ordem inteira com os valores novos,
// o 41 verdadeiro (ClOrdID corrente) e o 37.
func Replace(id Identificacao, o Ordem, origClOrdID, orderID string, agora time.Time) *quickfix.Message {
	m := base(id, "G")
	corpoOrdem(m, id, o, agora)
	m.Body.SetString(quickfix.Tag(41), origClOrdID)
	if orderID != "" {
		m.Body.SetString(quickfix.Tag(37), orderID)
	}
	return m
}

// Cancelamento monta o OrderCancelRequest (35=F) só com tags que a mensagem tem no dicionário:
// 1, 11, 37, 38, 41, 54, 55, 60, 207 (SPEC v1.1 F4).
func Cancelamento(id Identificacao, clOrdID, origClOrdID, orderID, simbolo string, lado byte, qtd int, agora time.Time) *quickfix.Message {
	m := base(id, "F")
	m.Body.SetString(quickfix.Tag(1), id.Conta)
	m.Body.SetString(quickfix.Tag(11), clOrdID)
	if orderID != "" {
		m.Body.SetString(quickfix.Tag(37), orderID)
	}
	m.Body.SetInt(quickfix.Tag(38), qtd)
	m.Body.SetString(quickfix.Tag(41), origClOrdID)
	m.Body.SetString(quickfix.Tag(54), string(lado))
	m.Body.SetString(quickfix.Tag(55), simbolo)
	m.Body.SetString(quickfix.Tag(60), utc(agora))
	if id.Bolsa != "" {
		m.Body.SetString(quickfix.Tag(207), id.Bolsa)
	}
	return m
}

// MassStatus monta o OrderMassStatusRequest (35=AF) por conta (585=9).
func MassStatus(conta, reqID string) *quickfix.Message {
	m := quickfix.NewMessage()
	m.Header.SetString(quickfix.Tag(35), "AF")
	m.Body.SetString(quickfix.Tag(584), reqID)
	m.Body.SetInt(quickfix.Tag(585), 9)
	m.Body.SetString(quickfix.Tag(1), conta)
	return m
}

// PedidoPosicoes: RequestForPositions (35=AN), bancada da HML (16/10: o PnT responde?). 724=0
// (posições), 581=1, 715 = dia do pregão (AAAAMMDD), Parties (obrigatório no dicionário) com a
// conta: 448=conta, 447=D, 452=3 (CLIENT_ID; o dicionário da DSA não tem o 24).
func PedidoPosicoes(id Identificacao, reqID, diaPregao string, agora time.Time) *quickfix.Message {
	m := base(id, "AN")
	m.Body.SetString(quickfix.Tag(710), reqID)
	m.Body.SetInt(quickfix.Tag(724), 0)
	g := quickfix.NewRepeatingGroup(quickfix.Tag(453), quickfix.GroupTemplate{
		quickfix.GroupElement(quickfix.Tag(448)), quickfix.GroupElement(quickfix.Tag(447)), quickfix.GroupElement(quickfix.Tag(452))})
	p := g.Add()
	p.SetString(quickfix.Tag(448), id.Conta)
	p.SetString(quickfix.Tag(447), "D")
	p.SetInt(quickfix.Tag(452), 3)
	m.Body.SetGroup(g)
	m.Body.SetString(quickfix.Tag(1), id.Conta)
	m.Body.SetInt(quickfix.Tag(581), 1)
	m.Body.SetString(quickfix.Tag(715), diaPregao)
	m.Body.SetString(quickfix.Tag(60), utc(agora))
	return m
}

func base(id Identificacao, tipo string) *quickfix.Message {
	m := quickfix.NewMessage()
	m.Header.SetString(quickfix.Tag(35), tipo)
	if id.OnBehalfOfSubID != "" {
		m.Header.SetString(quickfix.Tag(116), id.OnBehalfOfSubID)
	}
	if id.SenderLocationID != "" {
		m.Header.SetString(quickfix.Tag(142), id.SenderLocationID)
	}
	return m
}

func corpoOrdem(m *quickfix.Message, id Identificacao, o Ordem, agora time.Time) {
	m.Body.SetString(quickfix.Tag(1), id.Conta)
	m.Body.SetString(quickfix.Tag(11), o.ClOrdID)
	m.Body.SetString(quickfix.Tag(21), "4")
	m.Body.SetInt(quickfix.Tag(38), o.Qtd)
	m.Body.SetString(quickfix.Tag(40), "2")
	m.Body.SetString(quickfix.Tag(54), string(o.Lado))
	m.Body.SetString(quickfix.Tag(55), o.Simbolo)
	m.Body.SetString(quickfix.Tag(59), "0")
	m.Body.SetString(quickfix.Tag(60), utc(agora))
	if id.Bolsa != "" {
		m.Body.SetString(quickfix.Tag(207), id.Bolsa)
	}
	m.Body.SetInt(quickfix.Tag(847), o.Estrategia)
	if o.Estrategia == EstrategiaStop {
		// [PnT-6] sempre 40=2 com folga
		m.Body.SetString(quickfix.Tag(20828), strconv.FormatInt(o.Stop, 10))
		m.Body.SetString(quickfix.Tag(20829), strconv.FormatInt(o.StopLim, 10))
		m.Body.SetString(quickfix.Tag(44), strconv.FormatInt(o.StopLim, 10))
	} else {
		m.Body.SetString(quickfix.Tag(44), strconv.FormatInt(o.Preco, 10))
	}
}

func utc(t time.Time) string { return t.UTC().Format("20060102-15:04:05.000") }
