// Package flexscan é o conector Flexscan (REST) do kalman-ordens: a ponte que se apresenta ao motor
// como a sessão FIX (recebe a NewOrderSingle, o Replace, o Cancel e o mass status que o motor já
// monta; devolve ExecutionReports e 35=9 sintéticos), o receptor do webhook e o cliente da API.
// Desenho e regras: docs/SPEC-FLEXSCAN-v1.md.
package flexscan

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Fase normalizada de uma ordem (SPEC-FLEXSCAN-v1 §4.1, §4.2): as duas fontes falam vocabulários
// diferentes — o 5 da consulta é REPLACED (viva), o 5 do webhook é FINISHED (executada).
type Fase int

const (
	FaseDesconhecida Fase = iota
	FasePendente          // aceita pela API, ainda sem a bolsa
	FaseViva              // na bolsa (inclui editada e edição recusada)
	FaseCancelando        // retrato 6 (CANCELING)
	FaseExecutada         // terminal
	FaseCancelada         // terminal
	FaseRejeitada         // terminal
	FaseExpirada          // terminal
	FaseInesperada        // retrato 4, 8, 9 (UNBALANCED, SUSPENDING, SUSPENDED)
)

// Terminal: executada, cancelada, rejeitada ou expirada (não voltam a viva, §4.3 regra 2).
func (f Fase) Terminal() bool {
	return f == FaseExecutada || f == FaseCancelada || f == FaseRejeitada || f == FaseExpirada
}

func (f Fase) String() string {
	return [...]string{"DESCONHECIDA", "PENDENTE", "VIVA", "CANCELANDO", "EXECUTADA", "CANCELADA", "REJEITADA", "EXPIRADA", "INESPERADA"}[f]
}

// Edicao: o sinal de uma edição (PUT) no estado.
type Edicao int

const (
	SemSinal Edicao = iota
	EdicaoAceita
	EdicaoRecusada
)

// Estado é uma ordem como uma das fontes a viu, já normalizada.
type Estado struct {
	Fonte    string // "consulta" ou "webhook"
	ID       string // o id da Flexscan (o ClOrdID do retrato, o clordid das execuções)
	Nome     string // o name (o nosso ClOrdBase, KM-...)
	Conta    string
	Simbolo  string
	Lado     string // "1" compra, "2" venda
	Tipo     string // "2" limite, "4" stop-limite
	Usuario  string // UserName (só no retrato)
	Qtd      int
	Exec     int
	Medio    decimal.Decimal
	Preco    decimal.Decimal // limite atual (pode ser o ajustado ao túnel pela Flexscan)
	Stop     decimal.Decimal // gatilho atual (stop-limite)
	UltPx    decimal.Decimal // último negócio (0 se não há)
	UltQtd   int
	TradeID  string // uniqueTradeId (só na consulta)
	Fase     Fase
	Bruto    string // orderStatus / OrdStatus como veio
	Texto    string
	Rejeicao string // clxRejReason (consulta)
	Edicao   Edicao
	Quando   time.Time // transactTime (consulta, UTC) / TimeStamp (retrato, Brasília → UTC); só registro e ordem da edição
}

var fuso = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()

// numero lê um número que pode vir como número JSON ou texto ("207460.00000").
func numero(v any) (decimal.Decimal, bool) {
	switch x := v.(type) {
	case nil:
		return decimal.Zero, false
	case float64:
		return decimal.NewFromFloat(x), true
	case json.Number:
		d, err := decimal.NewFromString(x.String())
		return d, err == nil
	case string:
		if strings.TrimSpace(x) == "" {
			return decimal.Zero, false
		}
		d, err := decimal.NewFromString(strings.TrimSpace(x))
		return d, err == nil
	}
	return decimal.Zero, false
}

func inteiro(v any) int {
	d, ok := numero(v)
	if !ok {
		return 0
	}
	return int(d.IntPart())
}

func texto(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func decodificar(b []byte, v any) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	return d.Decode(v)
}

// faseConsulta: orderStatus do order_record (GET /orders/{id}).
func faseConsulta(s string) Fase {
	switch s {
	case "0", "1", "5": // 5 = REPLACED: viva com os valores novos
		return FaseViva
	case "2":
		return FaseExecutada
	case "4":
		return FaseCancelada
	case "8":
		return FaseRejeitada
	case "C":
		return FaseExpirada
	case "A", "6", "E": // pendentes do FIX, por via das dúvidas
		return FasePendente
	}
	return FaseDesconhecida
}

// faseRetrato: OrdStatus do retrato de estratégia (Status_t).
func faseRetrato(s string) Fase {
	switch s {
	case "0", "1", "2", "10":
		return FasePendente
	case "3":
		return FaseViva
	case "5": // FINISHED: executada
		return FaseExecutada
	case "6":
		return FaseCancelando
	case "7":
		return FaseCancelada
	case "4", "8", "9":
		return FaseInesperada
	}
	return FaseDesconhecida
}

// LerConsulta interpreta um order_record (GET /orders/{id}, ou um item de /orders/filter).
func LerConsulta(r map[string]any) Estado {
	e := Estado{Fonte: "consulta", ID: texto(r["id"]), Nome: texto(r["name"]), Conta: texto(r["account"]), Simbolo: texto(r["symbol"]),
		Lado: texto(r["side"]), Tipo: texto(r["orderType"]), Qtd: inteiro(r["quantity"]), Exec: inteiro(r["quantityExecuted"]),
		UltQtd: inteiro(r["lastQuantity"]), TradeID: texto(r["uniqueTradeId"]), Bruto: texto(r["orderStatus"]),
		Texto: texto(r["text"]), Rejeicao: texto(r["clxRejReason"])}
	e.Medio, _ = numero(r["avgPrice"])
	e.Preco, _ = numero(r["price"])
	e.Stop, _ = numero(r["stopPrice"])
	e.UltPx, _ = numero(r["lastPrice"])
	e.Fase = faseConsulta(e.Bruto)
	if t, err := time.Parse("20060102-15:04:05.000", texto(r["sendingTime"])); err == nil {
		e.Quando = t
	} else if t, err := time.Parse("20060102-15:04:05", texto(r["transactTime"])); err == nil {
		e.Quando = t
	}
	if e.Rejeicao != "" && e.Rejeicao != "0" {
		e.Edicao = EdicaoRecusada // o sinal só vale comparado ao de antes do PUT (ponte)
	} else if e.Bruto == "5" {
		e.Edicao = EdicaoAceita
	}
	return e
}

// LerRetrato interpreta um item do webhook (retrato da estratégia sorder).
func LerRetrato(r map[string]any) Estado {
	e := Estado{Fonte: "webhook", ID: texto(r["ClOrdID"]), Nome: texto(r["Name"]), Usuario: texto(r["UserName"]),
		Qtd: inteiro(r["TargetQtty"]), Exec: inteiro(r["ExecutedQtty"]), Bruto: texto(r["OrdStatus"]), Texto: texto(r["Text"])}
	e.Medio, _ = numero(r["AvgPrice"])
	if cp, ok := r["CustomParameters"].(map[string]any); ok {
		e.Preco, _ = numero(cp["PriceLimit"])
		e.Stop, _ = numero(cp["StopPrice"])
	}
	if legs, ok := r["Legs"].([]any); ok && len(legs) > 0 {
		if l, ok := legs[0].(map[string]any); ok {
			e.Conta, e.Simbolo, e.Lado, e.Tipo = texto(l["ILegAllocAccount"]), texto(l["LegSymbol"]), texto(l["LegSide"]), texto(l["LegOrdType"])
			e.UltPx, _ = numero(l["LegLastPx"])
			e.UltQtd = inteiro(l["LegLastQty"])
			if e.Qtd == 0 {
				e.Qtd = inteiro(l["LegQuantity"])
			}
		}
	}
	e.Fase = faseRetrato(e.Bruto)
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", texto(r["TimeStamp"]), fuso); err == nil {
		e.Quando = t.UTC()
	}
	switch {
	case strings.HasPrefix(e.Texto, "Edicao rejeitada"), strings.HasPrefix(e.Texto, "Edição rejeitada"):
		e.Edicao = EdicaoRecusada
	case strings.HasPrefix(e.Texto, "Editada"):
		e.Edicao = EdicaoAceita
	}
	if e.Fase == FaseDesconhecida && strings.Contains(strings.ToLower(e.Texto), "rejeit") {
		e.Fase = FaseRejeitada
	}
	return e
}

// LerWebhook: o corpo do webhook — lista de retratos (o observado), um retrato solto ou {records: [...]}.
func LerWebhook(corpo []byte) ([]Estado, error) {
	var v any
	if err := decodificar(corpo, &v); err != nil {
		return nil, err
	}
	var itens []any
	switch x := v.(type) {
	case []any:
		itens = x
	case map[string]any:
		if rs, ok := x["records"].([]any); ok {
			itens = rs
		} else {
			itens = []any{x}
		}
	default:
		return nil, fmt.Errorf("corpo não é lista nem objeto")
	}
	var r []Estado
	for _, it := range itens {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item não é objeto")
		}
		if sc := texto(m["StrategyCode"]); sc != "" && sc != "sorder" {
			continue // outra estratégia do usuário: não é nossa
		}
		e := LerRetrato(m)
		if e.ID == "" {
			return nil, fmt.Errorf("retrato sem ClOrdID")
		}
		r = append(r, e)
	}
	return r, nil
}
