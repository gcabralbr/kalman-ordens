package dicionario

import (
	"fmt"
	"slices"
	"strings"
)

// Original é o dicionário recebido do PnT, guardado byte a byte (não editar). A cópia do serviço
// (dsa_FIX44.xml) é Derivar(Original): `go generate ./pnt/dicionario`. Ver LEIA.md.
const Original = "dsa_FIX44.pnt-2026-10-13.xml"

// Campos do corpo de 8 e 9 que o original declara obrigatórios e a cópia relaxa (SPEC v1.1 F8).
// Se o dicionário do PnT mudar essa lista, Derivar falha: a diferença precisa ser revista.
var obrigatoriosRelaxados = map[string][]string{
	"8": {"OrderID", "ExecID", "ExecType", "OrdStatus", "Instrument", "Side", "LeavesQty", "CumQty", "AvgPx"},
	"9": {"OrderID", "ClOrdID", "OrigClOrdID", "OrdStatus", "CxlRejResponseTo"},
}

// Tags da 1014 (RoE 2025), ausentes do dicionário do PnT: declaradas e incluídas (opcionais) no
// corpo de NewOrderSingle (D) e OrderCancelReplaceRequest (G).
var tags1014 = []struct{ numero, nome, tipo string }{
	{"20828", "PriceStopLoss", "PRICE"},
	{"20829", "PriceSendOrderLoss", "PRICE"},
	{"20830", "PriceStartGain", "PRICE"},
	{"20831", "PriceSendOrderGain", "PRICE"},
	{"20832", "PriceInitOrderLossInc", "PRICE"},
	{"20833", "OrderLossIncMoney", "PRICE"},
	{"20834", "OrderLossIncPerc", "FLOAT"},
	{"20835", "CountSameBrokerBuySellTrades", "BOOLEAN"},
}

// Derivar aplica ao dicionário do PnT as diferenças da cópia do serviço (LEIA.md), e só elas:
//  1. type="FIX" na raiz;
//  2. corpo de 8 e 9 sem obrigatórios (obrigatoriosRelaxados);
//  3. valor 9 no enum de MassStatusReqType (585);
//  4. tags da 1014 (tags1014) declaradas e no corpo de D e G;
//  5. no componente InstrumentLeg, a 20798 referida pelo nome declarado (LegLastShares) e não
//     por LegLastQty, que não existe em <fields>: sem isso o QuickFIX/Go não carrega o arquivo.
//
// Cada passo confere a premissa sobre o original e falha se ela não vale mais (dicionário novo
// que já traz a diferença ou mudou o trecho): nada é aplicado em silêncio.
func Derivar(pnt []byte) ([]byte, error) {
	s := string(pnt)
	if strings.Contains(s, "\r") {
		return nil, fmt.Errorf("dicionário do PnT com CR: rever Derivar")
	}
	if strings.Count(s, "<fix major=") != 1 {
		return nil, fmt.Errorf("raiz <fix major=...> não encontrada (ou já tem type)")
	}
	s = strings.Replace(s, "<fix major=", `<fix type="FIX" major=`, 1)

	linhas := strings.Split(s, "\n")
	for _, tipo := range []string{"8", "9"} {
		var err error
		if linhas, err = relaxar(linhas, tipo); err != nil {
			return nil, err
		}
	}

	i, err := achar(linhas, `<field number="585" name="MassStatusReqType"`)
	if err != nil {
		return nil, err
	}
	fim := i
	for !strings.Contains(linhas[fim], "</field>") {
		if strings.Contains(linhas[fim], `enum="9"`) {
			return nil, fmt.Errorf("585 já tem o valor 9 no dicionário do PnT: tirar a diferença 3")
		}
		fim++
	}
	if !strings.Contains(linhas[fim-1], `<value enum="8" description="STATUS_FOR_ORDERS_FOR_A_PARTYID"/>`) {
		return nil, fmt.Errorf("585: último valor não é o 8")
	}
	linhas = slices.Insert(linhas, fim, recuo(linhas[fim-1])+`<value enum="9" description="STATUS_FOR_ORDERS_FOR_AN_ACCOUNT"/>`)

	var decl, uso []string
	for _, t := range tags1014 {
		if strings.Contains(s, `number="`+t.numero+`"`) || strings.Contains(s, `name="`+t.nome+`"`) {
			return nil, fmt.Errorf("tag %s (%s) já está no dicionário do PnT: tirar da diferença 4", t.numero, t.nome)
		}
		decl = append(decl, fmt.Sprintf(`<field number="%s" name="%s" type="%s"/>`, t.numero, t.nome, t.tipo))
		uso = append(uso, fmt.Sprintf(`<field name="%s" required="N"/>`, t.nome))
	}
	// declaração em ordem numérica: logo antes da 20836, que vem depois da 20827 no original
	if i, err = achar(linhas, `<field number="20836" `); err != nil {
		return nil, err
	}
	if !strings.Contains(linhas[i-1], `<field number="20827" `) {
		return nil, fmt.Errorf("a 20836 não vem logo depois da 20827")
	}
	linhas = inserir(linhas, i, recuo(linhas[i]), decl)
	for _, tipo := range []string{"D", "G"} {
		ini, fim, err := mensagem(linhas, tipo)
		if err != nil {
			return nil, err
		}
		linhas = inserir(linhas, fim, recuo(linhas[ini+1]), uso)
	}

	if i, err = achar(linhas, `name="LegLastQty"`); err != nil {
		return nil, fmt.Errorf("diferença 5: %w", err)
	}
	if !strings.Contains(linhas[i], `<field number="20798" name="LegLastQty" type="QTY"/>`) {
		return nil, fmt.Errorf("diferença 5: LegLastQty fora do InstrumentLeg: %q", linhas[i])
	}
	if _, err := achar(linhas, `<field number="20798" name="LegLastShares" type="INT"/>`); err != nil {
		return nil, fmt.Errorf("diferença 5: %w", err)
	}
	linhas[i] = strings.Replace(linhas[i], `name="LegLastQty" type="QTY"`, `name="LegLastShares" type="INT"`, 1)
	return []byte(strings.Join(linhas, "\n")), nil
}

// relaxar troca required="Y" por "N" nos filhos diretos do corpo da mensagem (não dentro de
// grupos) e confere que os trocados são exatamente obrigatoriosRelaxados[tipo].
func relaxar(linhas []string, tipo string) ([]string, error) {
	ini, fim, err := mensagem(linhas, tipo)
	if err != nil {
		return nil, err
	}
	var trocados []string
	nivel := 0
	for i := ini + 1; i < fim; i++ {
		l := strings.TrimSpace(linhas[i])
		switch {
		case strings.HasPrefix(l, "</group>"):
			nivel--
			continue
		case strings.HasPrefix(l, "<group ") && !strings.HasSuffix(l, "/>"):
			nivel++
			if nivel > 1 {
				continue
			}
		case nivel > 0:
			continue
		}
		if strings.Contains(l, `required="Y"`) {
			linhas[i] = strings.Replace(linhas[i], `required="Y"`, `required="N"`, 1)
			trocados = append(trocados, atributo(l, "name"))
		}
	}
	if !slices.Equal(trocados, obrigatoriosRelaxados[tipo]) {
		return nil, fmt.Errorf("obrigatórios do corpo de 35=%s mudaram: %v (esperados %v)", tipo, trocados, obrigatoriosRelaxados[tipo])
	}
	return linhas, nil
}

func mensagem(linhas []string, tipo string) (ini, fim int, err error) {
	if ini, err = achar(linhas, `msgtype="`+tipo+`"`); err != nil {
		return 0, 0, err
	}
	for fim = ini; !strings.Contains(linhas[fim], "</message>"); fim++ {
	}
	return ini, fim, nil
}

// achar devolve a única linha que contém trecho.
func achar(linhas []string, trecho string) (int, error) {
	r := -1
	for i, l := range linhas {
		if strings.Contains(l, trecho) {
			if r >= 0 {
				return 0, fmt.Errorf("%q aparece mais de uma vez", trecho)
			}
			r = i
		}
	}
	if r < 0 {
		return 0, fmt.Errorf("%q não encontrado", trecho)
	}
	return r, nil
}

func inserir(linhas []string, i int, recuo string, novas []string) []string {
	r := make([]string, len(novas))
	for k, l := range novas {
		r[k] = recuo + l
	}
	return slices.Insert(linhas, i, r...)
}

func recuo(l string) string { return l[:len(l)-len(strings.TrimLeft(l, " \t"))] }

func atributo(l, nome string) string {
	_, v, _ := strings.Cut(l, nome+`="`)
	v, _, _ = strings.Cut(v, `"`)
	return v
}
