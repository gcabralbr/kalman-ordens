package motor

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// v1.34 §3.5: a página do fio não parte a linha do POST da Flexscan (pedido e resposta): termina
// antes do pedido sem resposta, que volta inteiro na próxima; o PnT sai mensagem a mensagem.
func TestHistoricoFioPaginaSemPartirPedido(t *testing.T) {
	r := repo.NovaMemoria()
	em := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	for i, e := range []repo.EventoFIX{
		{Direcao: "ENV", MsgType: "D", ClOrdID: "KM-E-1-1", Bruta: "8=FIX.4.4\x0135=D\x01554=segredo\x01"},
		{Direcao: "ENV", MsgType: "FLX-POST", ClOrdID: "KM-E-2-1", Bruta: "{}"},
		{Direcao: "REC", MsgType: "FLX-202", ClOrdID: "KM-E-2-1", OrderID: "f1", Texto: "POST aceito"},
		{Direcao: "REC", MsgType: "8", ClOrdID: "KM-E-1-1", OrdStatus: "0"},
	} {
		e.Ambiente, e.Em = "REAL", em.Add(time.Duration(i)*time.Second)
		if _, err := r.RegistrarEvento(e); err != nil {
			t.Fatal(err)
		}
	}
	// página de 2: o D e o POST sem resposta: sai só o D, e a próxima começa no POST
	p1, err := HistoricoFio(r, "2026-11-13", repo.FiltroFio{Ambiente: "REAL", Limite: 2})
	if err != nil {
		t.Fatal(err)
	}
	ms := p1["mensagens"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["msgtype"] != "D" || p1["ultimo_seq"] != int64(1) || p1["mais"] != true {
		t.Fatalf("página 1: %+v", p1)
	}
	if raw := ms[0].(map[string]any)["fix_raw"]; raw != "8=FIX.4.4|35=D|554=***|" {
		t.Fatalf("fix_raw sem máscara: %v", raw)
	}
	p2, _ := HistoricoFio(r, "2026-11-13", repo.FiltroFio{Ambiente: "REAL", Limite: 2, DesdeID: 1})
	ms = p2["mensagens"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["conector"] != "FLEXSCAN" || ms[0].(map[string]any)["seq"] != int64(3) || p2["ultimo_seq"] != int64(3) {
		t.Fatalf("página 2: %+v", p2)
	}
	p3, _ := HistoricoFio(r, "2026-11-13", repo.FiltroFio{Ambiente: "REAL", Limite: 2, DesdeID: 3})
	ms = p3["mensagens"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["msgtype"] != "8" || p3["mais"] != false {
		t.Fatalf("página 3: %+v", p3)
	}
}

// v1.34 §3.2: o conector do evento da sessão gravado pela origem; nas linhas antigas, inferido.
func TestConectorDaSessao(t *testing.T) {
	for _, c := range []struct {
		e        repo.EventoSessao
		quer     string
		inferido bool
	}{
		{repo.EventoSessao{Tipo: "ALERTA", Texto: "SESSAO_CAIU: [flexscan] sem sessao", Conector: "FLEXSCAN"}, "FLEXSCAN", false},
		{repo.EventoSessao{Tipo: "LOGON", Texto: "logon"}, "PNT", true},
		{repo.EventoSessao{Tipo: "ALERTA", Texto: "SESSAO_CAIU: [flexscan] sem sessao: token"}, "FLEXSCAN", true},
		{repo.EventoSessao{Tipo: "ALERTA", Texto: "SESSAO_CAIU: sessao FIX caiu"}, "PNT", true},
		{repo.EventoSessao{Tipo: "ALERTA", Texto: "FLEXSCAN_WEBHOOK_AUSENTE: webhook"}, "FLEXSCAN", true},
		{repo.EventoSessao{Tipo: "ALERTA", Texto: "RESOLVIDO: SESSAO_PRESA_NO_PNT"}, "PNT", true},
		{repo.EventoSessao{Tipo: "CONCILIACAO", Texto: "CONCILIACAO: 0 ordem(ns)"}, "SERVICO", true},
	} {
		if got, inf := conectorDaSessao(c.e); got != c.quer || inf != c.inferido {
			t.Errorf("%+v: %s %v, quer %s %v", c.e, got, inf, c.quer, c.inferido)
		}
	}
	if ConectorDoAlerta("RESOLVIDO", "SESSAO_CAIU: SESSAO_CAIU") != "PNT" || ConectorDoAlerta("LIMITE", "x") != "SERVICO" {
		t.Fatal("ConectorDoAlerta")
	}
}
