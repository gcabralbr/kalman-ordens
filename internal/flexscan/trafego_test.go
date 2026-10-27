package flexscan

import (
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// v1.21: o buffer circular do tráfego (as últimas 2.000), desde e limite, e o resumo (ASCII, uma
// linha, até 200 caracteres).
func TestTrafegoBufferDesdeLimite(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 28, 10, 0, 0, 0, relogio.Fuso))
	tr := novoTrafego(rel)
	for i := 0; i < CapacidadeTrafego+5; i++ {
		tr.linha(TrafegoPOST, "ENV", 202, time.Duration(i)*time.Millisecond, "KM-E-1-1", "id", "x")
	}
	ls, ult, s := tr.Consultar(0, 0)
	if ult != CapacidadeTrafego+5 || len(ls) != LimitePadraoTrafego || ls[0].Seq != 6 {
		t.Fatalf("ult %d, %d linhas, primeira %d", ult, len(ls), ls[0].Seq)
	}
	if ls, _, _ = tr.Consultar(0, 5000); len(ls) != CapacidadeTrafego {
		t.Fatalf("limite acima do máximo: %d", len(ls))
	}
	if ls, _, _ = tr.Consultar(ult-3, 0); len(ls) != 3 || ls[0].Seq != ult-2 {
		t.Fatalf("desde: %v", ls)
	}
	if s["msgs_min"] != CapacidadeTrafego || s["latencia_post_max_ms"] != int64(CapacidadeTrafego+4) {
		t.Fatalf("saude: %v", s)
	}
	tr.linha(TrafegoWebhook, "REC", -1, -1, "", "", "áéí\n"+strings.Repeat("x", 300))
	ls, _, _ = tr.Consultar(ult, 0)
	l := ls[0]
	if l.StatusHTTP != nil || l.DuracaoMs != nil || l.ClOrdID != nil || l.FlexscanID != nil || len(l.Resumo) != 200 || !strings.HasPrefix(l.Resumo, "aei x") {
		t.Fatalf("linha: %+v", l)
	}
}

// A conferência do cadastro do webhook: 10 min por padrão, FLEXSCAN_WEBHOOK_CONFERIR_MIN muda.
func TestConferenciaWebhookPadrao(t *testing.T) {
	if c := ConfigDe(config.Novo(map[string]string{})); c.ConfereWebhook != 10*time.Minute {
		t.Fatalf("padrão: %s", c.ConfereWebhook)
	}
	if c := ConfigDe(config.Novo(map[string]string{"FLEXSCAN_WEBHOOK_CONFERIR_MIN": "3"})); c.ConfereWebhook != 3*time.Minute {
		t.Fatalf("FLEXSCAN_WEBHOOK_CONFERIR_MIN=3: %s", c.ConfereWebhook)
	}
	var c Config
	c.padroes()
	if c.ConfereWebhook != 10*time.Minute {
		t.Fatalf("padroes: %s", c.ConfereWebhook)
	}
}
