package alerta

import "testing"

// v1.4 L1: o Kalman grava recusas e alertas em latin1; o "−" do BOOK_DESCOLADO quebrou a gravação.
func TestASCII(t *testing.T) {
	casos := map[string]string{
		"|meio − preco| = 160":                     "|meio - preco| = 160",
		"sessão FIX caiu; posição não confirmada":  "sessao FIX caiu; posicao nao confirmada",
		"AÇÃO → saída ≠ entrada, gatilho ≥ 5 × 2…": "ACAO -> saida != entrada, gatilho >= 5 x 2...",
		"médio ∓ stop — já":                        "medio -/+ stop - ja",
		"sem acento":                               "sem acento",
		"emoji 🚨 e ☃":                              "emoji ? e ?",
	}
	for de, para := range casos {
		if got := ASCII(de); got != para {
			t.Errorf("ASCII(%q) = %q, esperado %q", de, got, para)
		}
	}
}

// O alerta sai em ASCII no Telegram e no evento ALERTA.
func TestEmitirEmASCII(t *testing.T) {
	var pub string
	n := &notifTeste{}
	a := Novo(n, func(_, texto string, _ int64) { pub = texto }, nil, 0)
	a.Emitir("RECUSA_PNT", "", "recusa: |meio − preco| não", 0)
	if pub != "recusa: |meio - preco| nao" || n.ultimo != pub {
		t.Fatalf("publicado %q, Telegram %q", pub, n.ultimo)
	}
}

type notifTeste struct{ ultimo string }

func (n *notifTeste) Notificar(_, texto string) { n.ultimo = texto }
