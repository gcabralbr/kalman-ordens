package flexscan

import (
	"testing"
	"time"
)

// v1.22 §2: o webhook da ordem que chega antes do 202 (o POST leva ~0,3 s, o webhook também) não
// abre a espera; o que chega depois a fecha; sem o receptor, nada.
func TestEsperaWebhook(t *testing.T) {
	p := &Ponte{cfg: Config{Webhook: true, WebhookURL: "https://kalman.exemplo/flexscan/webhook/s", EsperaWebhook: 5 * time.Second}}
	t0 := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	o := &ordemP{nome: "KM-P-1-1"}

	p.webhookDaOrdemLocked(o, t0.Add(300*time.Millisecond)) // o webhook antes do 202
	p.esperarWebhookLocked(o, t0, esperaAcao, "POST")       // o 202 processado depois; desde = o envio
	if !o.whEsperaDesde.IsZero() {
		t.Fatalf("o webhook já tinha chegado: a espera não abre")
	}
	p.esperarWebhookLocked(o, t0.Add(time.Second), esperaAcao, "PUT")
	if !o.whEsperaDesde.Equal(t0.Add(time.Second)) {
		t.Fatalf("PUT depois do último webhook: a espera abre")
	}
	p.webhookDaOrdemLocked(o, t0.Add(1400*time.Millisecond))
	if !o.whEsperaDesde.IsZero() {
		t.Fatalf("o webhook fecha a espera")
	}
	p.cfg.Webhook = false
	p.esperarWebhookLocked(o, t0.Add(2*time.Second), esperaAcao, "DELETE")
	if !o.whEsperaDesde.IsZero() {
		t.Fatalf("sem receptor: não espera")
	}
	// v1.32: com o receptor e sem a URL do cadastro (o REAL de teste da .53, D5), também não
	p.cfg.Webhook, p.cfg.WebhookURL = true, ""
	p.esperarWebhookLocked(o, t0.Add(3*time.Second), esperaAcao, "POST")
	if !o.whEsperaDesde.IsZero() {
		t.Fatalf("sem a FLEXSCAN_WEBHOOK_URL: não espera")
	}
}
