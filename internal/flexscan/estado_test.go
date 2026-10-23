package flexscan

import (
	"os"
	"path/filepath"
	"testing"
)

func lerArq(t *testing.T, nome string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", nome))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func consulta(t *testing.T, nome string) Estado {
	t.Helper()
	var r map[string]any
	if err := decodificar(lerArq(t, nome), &r); err != nil {
		t.Fatal(err)
	}
	return LerConsulta(r)
}

func retratos(t *testing.T, nome string) []Estado {
	t.Helper()
	es, err := LerWebhook(lerArq(t, nome))
	if err != nil {
		t.Fatal(err)
	}
	return es
}

// O 5 de cada lado (SPEC-FLEXSCAN-v1 §0 item 5): na consulta é REPLACED (viva), no webhook é
// FINISHED (executada).
func TestCincoDeCadaLado(t *testing.T) {
	c := consulta(t, "consulta-04-get-pos-put.json")
	if c.Bruto != "5" || c.Fase != FaseViva || c.Edicao != EdicaoAceita || c.Preco.String() != "205375" {
		t.Fatalf("consulta 5: %+v", c)
	}
	w := retratos(t, "webhook-006.json")[0]
	if w.Bruto != "5" || w.Fase != FaseExecutada || w.Exec != 1 || w.Medio.String() != "207460" {
		t.Fatalf("retrato 5: %+v", w)
	}
}

func TestLerConsulta(t *testing.T) {
	e := consulta(t, "consulta-08-get-entrada.json")
	if e.ID != "801692fd-0918-4c56-be2a-c4a4620a286b" || e.Nome != "KM-TESTE-E-2" || e.Conta != "S-1117150" || e.Simbolo != "WINZ26" ||
		e.Lado != "1" || e.Tipo != "2" || e.Qtd != 1 || e.Exec != 1 || e.Fase != FaseExecutada || e.TradeID != "33" ||
		e.Medio.String() != "207760" || e.UltPx.String() != "207760" || e.UltQtd != 1 {
		t.Fatalf("executada: %+v", e)
	}
	// stopPrice e avgPrice vêm como texto
	p := consulta(t, "consulta-11-get-protecao.json")
	if p.Tipo != "4" || p.Stop.String() != "207260" || p.Preco.String() != "207160" || p.Fase != FaseViva || p.Edicao != SemSinal {
		t.Fatalf("stop-limite: %+v", p)
	}
	// edição recusada: o estado do último aceito (5) com clxRejReason
	r := consulta(t, "consulta-15-get-apos-ultrapassado.json")
	if r.Fase != FaseViva || r.Edicao != EdicaoRecusada || r.Rejeicao != "4" || r.Stop.String() != "207460" {
		t.Fatalf("edição recusada: %+v", r)
	}
	if c := consulta(t, "consulta-06-get-pos-delete.json"); c.Fase != FaseCancelada {
		t.Fatalf("cancelada: %+v", c)
	}
}

func TestLerRetrato(t *testing.T) {
	a := retratos(t, "webhook-003.json")[0]
	if a.ID != "487f91bd-e4ad-46d6-846d-cb2e2c5d2a81" || a.Nome != "KM-WH-E-1" || a.Fase != FaseViva || a.Conta != "S-1117150" ||
		a.Simbolo != "WINZ26" || a.Lado != "1" || a.Tipo != "2" || a.Qtd != 1 || a.Preco.String() != "204440" || a.Usuario != "flx_gammaquant" {
		t.Fatalf("aceita: %+v", a)
	}
	if a.Quando.Format("15:04:05") != "14:41:50" { // TimeStamp em Brasília → UTC
		t.Fatalf("horário: %s", a.Quando)
	}
	if e := retratos(t, "webhook-004.json")[0]; e.Edicao != EdicaoAceita || e.Fase != FaseViva || e.Preco.String() != "204490" {
		t.Fatalf("editada: %+v", e)
	}
	if e := retratos(t, "webhook-005.json")[0]; e.Fase != FaseCancelada {
		t.Fatalf("cancelada: %+v", e)
	}
	if e := retratos(t, "webhook-013.json")[0]; e.Edicao != EdicaoRecusada || e.Fase != FaseViva || e.Stop.String() != "204965" {
		t.Fatalf("edição recusada: %+v", e)
	}
	if e := retratos(t, "webhook-017.json")[0]; e.Fase != FaseExecutada || e.Tipo != "4" || e.Exec != 1 || e.UltPx.String() != "207940" {
		t.Fatalf("stop executado: %+v", e)
	}
	// 009: dois itens, fora da ordem cronológica
	l := retratos(t, "webhook-009.json")
	if len(l) != 2 || l[0].Nome != "KM-WH-S-2" || l[0].Fase != FaseExecutada || l[1].Nome != "KM-WH-P-2" || l[1].Fase != FaseCancelada {
		t.Fatalf("lista 009: %+v", l)
	}
}

func TestLerWebhookFormas(t *testing.T) {
	if _, err := LerWebhook([]byte(`"x"`)); err == nil {
		t.Fatal("texto solto aceito")
	}
	if _, err := LerWebhook([]byte(`[{"teste":"ponta-a-ponta"}]`)); err == nil {
		t.Fatal("item sem ClOrdID aceito")
	}
	es, err := LerWebhook([]byte(`{"ClOrdID":"a","OrdStatus":"3","Name":"KM-E-1-1"}`))
	if err != nil || len(es) != 1 || es[0].Fase != FaseViva {
		t.Fatalf("objeto solto: %v %+v", err, es)
	}
	es, err = LerWebhook([]byte(`{"records":[{"ClOrdID":"a","OrdStatus":"7"}]}`))
	if err != nil || len(es) != 1 || es[0].Fase != FaseCancelada {
		t.Fatalf("records: %v %+v", err, es)
	}
	es, err = LerWebhook([]byte(`[{"ClOrdID":"a","OrdStatus":"3","StrategyCode":"twap"}]`))
	if err != nil || len(es) != 0 {
		t.Fatalf("outra estratégia: %v %+v", err, es)
	}
}
