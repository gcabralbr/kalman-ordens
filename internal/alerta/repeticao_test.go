package alerta

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

type notifLista struct{ textos []string }

func (n *notifLista) Notificar(codigo, texto string) { n.textos = append(n.textos, codigo+": "+texto) }

// 22/10: o crítico repete a cada 30 s nos primeiros 2 min e depois a cada 5 min (o FONTE_DEGRADADA da
// abertura mandou 4 mensagens em 1m45; uma falha de uma hora mandaria 120).
func TestRepeticaoEspaca(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 22, 9, 0, 30, 0, time.UTC))
	n := &notifLista{}
	a := Novo(n, nil, rel, 30*time.Second)
	a.Espacar(2*time.Minute, 5*time.Minute)
	a.Emitir("FONTE_DEGRADADA", "", "fonte ruim", 0)
	var repeticoes []int // segundos desde a emissão
	for s := 1; s <= 20*60; s++ {
		rel.Avancar(time.Second)
		antes := len(n.textos)
		a.Tick()
		if len(n.textos) > antes {
			repeticoes = append(repeticoes, s)
		}
	}
	esperado := []int{30, 60, 90, 120, 420, 720, 1020}
	if len(repeticoes) != len(esperado) {
		t.Fatalf("repetições em %v, esperado %v", repeticoes, esperado)
	}
	for i := range esperado {
		if repeticoes[i] != esperado[i] {
			t.Fatalf("repetições em %v, esperado %v", repeticoes, esperado)
		}
	}
	if n.textos[1] != "FONTE_DEGRADADA: fonte ruim (continua)" {
		t.Fatalf("texto %q", n.textos[1])
	}
	// uma emissão nova depois de resolvido volta para os 30 s
	a.Resolver("FONTE_DEGRADADA")
	a.Emitir("FONTE_DEGRADADA", "", "fonte ruim de novo", 0)
	k := len(n.textos)
	rel.Avancar(30 * time.Second)
	a.Tick()
	if len(n.textos) != k+1 {
		t.Fatalf("depois de resolver, a repetição não voltou aos 30 s")
	}
}

// Sem Espacar (os testes antigos, o zero): sempre a repetição curta.
func TestRepeticaoSemEspacar(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC))
	n := &notifLista{}
	a := Novo(n, nil, rel, 30*time.Second)
	a.Emitir("FONTE_DEGRADADA", "", "x", 0)
	for i := 0; i < 10*60; i++ {
		rel.Avancar(time.Second)
		a.Tick()
	}
	if len(n.textos) != 1+20 {
		t.Fatalf("%d mensagens em 10 min, esperado 21", len(n.textos))
	}
}

// RESOLVIDO: o código uma vez só quando a chave é o próprio código; com chave própria, os dois.
func TestTextoDoResolvido(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC))
	n := &notifLista{}
	a := Novo(n, nil, rel, 30*time.Second)
	a.Emitir("FONTE_DEGRADADA", "", "x", 0)
	a.Resolver("FONTE_DEGRADADA")
	a.Emitir("SEM_PROTECAO", "op:900000001", "y", 900000001)
	a.Resolver("op:900000001")
	if n.textos[1] != "RESOLVIDO: FONTE_DEGRADADA" || n.textos[3] != "RESOLVIDO: SEM_PROTECAO: op:900000001" {
		t.Fatalf("textos %q", n.textos)
	}
}
