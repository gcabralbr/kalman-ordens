package flexscan

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
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

// 28/10: o tráfego recarregado do kalmanordemevento — pedido e resposta pareados (duração, id do
// POST), timeout sem duração, consulta 404, evento antigo (msgtype "FLX-") e resposta sem pedido
// ignorados, login gravado; no máximo a capacidade do buffer, as mais recentes.
func TestLinhasDoRegistro(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	em := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	evs := []repo.EventoFIX{
		{Direcao: "REC", MsgType: "FLX-", ClOrdID: "KM-E-1-1", Em: em(0)}, // antes da 0013
		{Direcao: "ENV", MsgType: msgLogin, Texto: "partida OK", Codigo: "200", Em: em(1)},
		{Direcao: "ENV", MsgType: "FLX-POST", ClOrdID: "KM-E-1-1", Bruta: "POST /orders account=S-1 price=130020 qtty=1 side=1 symbol=WINZ26", Em: em(10)},
		{Direcao: "REC", MsgType: "FLX-202", ClOrdID: "KM-E-1-1", Texto: "POST ", Bruta: `{"id":"abc"}`, Em: em(71)},
		{Direcao: "REC", MsgType: "FLX-404", ClOrdID: "KM-E-1-1", OrderID: "abc", Texto: "GET Order not found", Em: em(300)},
		{Direcao: "ENV", MsgType: "FLX-DELETE", ClOrdID: "KM-E-1-2", OrderID: "abc", Bruta: "DELETE /orders/abc", Em: em(400)},
		{Direcao: "REC", MsgType: "FLX-SEMRESPOSTA", ClOrdID: "KM-E-1-2", OrderID: "abc", Texto: "DELETE timeout", Em: em(3400)},
		{Direcao: "REC", MsgType: "FLX-202", ClOrdID: "KM-E-9-9", Texto: "PUT ", Em: em(3500)}, // sem pedido
	}
	ls := LinhasDoRegistro(evs)
	if len(ls) != 4 {
		t.Fatalf("%d linhas: %+v", len(ls), ls)
	}
	login, post, cons, del := ls[0], ls[1], ls[2], ls[3]
	if login.Tipo != TrafegoLogin || *login.StatusHTTP != 200 || login.Resumo != "partida OK" {
		t.Fatalf("login: %+v", login)
	}
	if post.Tipo != TrafegoPOST || *post.StatusHTTP != 202 || *post.DuracaoMs != 61 || *post.FlexscanID != "abc" ||
		post.Resumo != "C 1 WINZ26 limite 130020 conta S-1" || post.Em != "2026-10-28T09:00:00.010-03:00" {
		t.Fatalf("post: %+v", post)
	}
	if cons.Tipo != TrafegoConsulta || *cons.StatusHTTP != 404 || cons.Resumo != "consulta -> Order not found" {
		t.Fatalf("consulta: %+v", cons)
	}
	if del.Tipo != TrafegoDELETE || del.StatusHTTP != nil || del.DuracaoMs != nil || del.Resumo != "cancelamento -> sem resposta: timeout" {
		t.Fatalf("delete: %+v", del)
	}
	// o buffer: as mais recentes, numeradas a partir de 1
	var muitos []repo.EventoFIX
	for i := 0; i < CapacidadeTrafego+10; i++ {
		muitos = append(muitos, repo.EventoFIX{Direcao: "ENV", MsgType: msgLogin, Texto: strconv.Itoa(i), Codigo: "200", Em: em(i)})
	}
	p := &Ponte{traf: novoTrafego(relogio.NovoControlado(t0))}
	if n := p.CarregarTrafego(muitos); n != CapacidadeTrafego {
		t.Fatalf("carregadas: %d", n)
	}
	ls, ult, s := p.traf.Consultar(0, 1)
	if ult != CapacidadeTrafego || ls[0].Seq != 1 || ls[0].Resumo != "10" || s["recarregadas"] != CapacidadeTrafego {
		t.Fatalf("buffer: ult %d %+v %v", ult, ls, s["recarregadas"])
	}
}

// v1.23: a virada do dia — depois de um restart o buffer tinha o dia recarregado e acumulava os
// seguintes. Na meia-noite (relogio.Fuso) as linhas de antes saem, recarregadas volta a zero, o seq
// segue e saude.dia muda; a recarga só aceita as linhas do dia corrente.
func TestTrafegoViradaDoDia(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 28, 23, 50, 0, 0, relogio.Fuso))
	p := &Ponte{traf: novoTrafego(rel)}
	ontem := time.Date(2026, 10, 27, 17, 0, 0, 0, relogio.Fuso)
	hoje := time.Date(2026, 10, 28, 9, 0, 0, 0, relogio.Fuso)
	evs := []repo.EventoFIX{
		{Direcao: "ENV", MsgType: msgLogin, Texto: "ontem", Codigo: "200", Em: ontem},
		{Direcao: "ENV", MsgType: msgLogin, Texto: "a", Codigo: "200", Em: hoje},
		{Direcao: "ENV", MsgType: msgLogin, Texto: "b", Codigo: "200", Em: hoje.Add(time.Hour)},
	}
	if n := p.CarregarTrafego(evs); n != 2 {
		t.Fatalf("recarregadas: %d", n)
	}
	tr := p.traf
	tr.linha(TrafegoPOST, "ENV", 202, time.Millisecond, "KM-E-1-1", "id", "28")
	ls, ult, s := tr.Consultar(0, 0)
	if len(ls) != 3 || ult != 3 || s["recarregadas"] != 2 || s["dia"] != "2026-10-28" || s["latencia_post_max_ms"] != int64(1) {
		t.Fatalf("antes da virada: %d linhas, ult %d, %v", len(ls), ult, s)
	}
	// meia-noite: a consulta sozinha já vira (sem linha nova)
	rel.Avancar(10 * time.Minute)
	ls, ult, s = tr.Consultar(0, 0)
	if len(ls) != 0 || ult != 3 || s["recarregadas"] != 0 || s["dia"] != "2026-10-29" || s["latencia_post_max_ms"] != nil {
		t.Fatalf("na virada: %d linhas, ult %d, %v", len(ls), ult, s)
	}
	tr.linha(TrafegoPOST, "ENV", 202, 2*time.Millisecond, "KM-E-2-1", "id", "29")
	if ls, ult, _ = tr.Consultar(0, 0); len(ls) != 1 || ls[0].Seq != 4 || ult != 4 || ls[0].Resumo != "29" {
		t.Fatalf("depois: %+v ult %d", ls, ult)
	}
	// a linha nova também vira, sem consulta no meio; o buffer cheio esvazia inteiro
	for i := 0; i < CapacidadeTrafego+3; i++ {
		tr.linha(TrafegoWebhook, "REC", 200, -1, "", "", "x")
	}
	rel.Avancar(24 * time.Hour)
	tr.linha(TrafegoWebhook, "REC", 200, -1, "", "", "30")
	tr.mu.Lock()
	n := tr.n
	tr.mu.Unlock()
	if ls, _, s = tr.Consultar(0, 0); n != 1 || len(ls) != 1 || ls[0].Resumo != "30" || s["dia"] != "2026-10-30" {
		t.Fatalf("buffer cheio na virada: n %d %+v %v", n, ls, s["dia"])
	}
}
