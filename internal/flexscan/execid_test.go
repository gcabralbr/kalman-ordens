package flexscan

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// v1.36.3 §2 (op 1123094048, 23/11): o ExecID do 150=8 do POST recusado (sem id da Flexscan) não
// pode repetir depois de um restart. Duas pontes (duas partidas), uma recusa em cada: o contador
// começa de novo nas duas, a marca de partida não.
func TestExecIDNaoRepeteEntrePartidas(t *testing.T) {
	antes, depois := &Ponte{partida: marcaDePartida()}, &Ponte{partida: marcaDePartida()}
	if antes.partida == depois.partida {
		t.Fatalf("a marca de partida repetiu: %s", antes.partida)
	}
	recusa := func(p *Ponte, nome string) string {
		o := &ordemP{nome: nome, clAtual: nome}
		return p.erTexto(o, "8", "8", "[flexscan 400] Invalid client document!", "0").Texto(17)
	}
	a := recusa(antes, "KM-BOLETA-E-1123093633-1")
	b := recusa(depois, "KM-BOLETA-E-1123094048-1")
	if a == b {
		t.Fatalf("ExecID repetido entre partidas: %s", a)
	}
	for _, ex := range []string{a, b} {
		if !strings.HasPrefix(ex, "NONE:8:8:0:") || len(ex) > 80 {
			t.Fatalf("ExecID fora do formato: %q", ex)
		}
	}
	if c := recusa(depois, "KM-BOLETA-E-1123094049-1"); c == b {
		t.Fatalf("ExecID repetido na mesma partida: %s", c)
	}
}

// O fill continua determinístico (id:F:exec): o motor descarta por ele o mesmo fill vindo do
// webhook e da consulta, também entre partidas.
func TestExecIDDoFillDeterministico(t *testing.T) {
	fill := func(p *Ponte) string {
		o := &ordemP{nome: "KM-P-1-1", clAtual: "KM-P-1-1", id: "abc", qtd: 1, tipo: "4", aceitaEntregue: true}
		e := Estado{Fonte: "consulta", ID: "abc", Nome: "KM-P-1-1", Qtd: 1, Exec: 1, Medio: decimal.NewFromInt(130000),
			UltPx: decimal.NewFromInt(130000), UltQtd: 1, Fase: FaseExecutada}
		msgs, _ := p.fundirLocked(o, e, "consulta")
		for _, m := range msgs {
			if m.Texto(150) == "F" {
				return m.Texto(17)
			}
		}
		t.Fatalf("sem o 150=F: %v", msgs)
		return ""
	}
	nova := func() *Ponte { return &Ponte{partida: marcaDePartida(), ordens: map[string]*ordemP{}} }
	if a, b := fill(nova()), fill(nova()); a != b || a != "abc:F:1" {
		t.Fatalf("o ExecID do fill mudou entre partidas: %s, %s", a, b)
	}
}
