package motor

import (
	"errors"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/flexscan"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// v1.21.1: a operação de um webhook com vários retratos só quando todos são dela.
func TestMesmaOperacao(t *testing.T) {
	casos := []struct {
		cls    []string
		modulo string
		op     int64
		ok     bool
	}{
		{[]string{"KM-E-234339-1", "KM-P-234339-1"}, "KALMAN", 234339, true},
		{[]string{"KM-S-11501-1", "KM-P-11501-3"}, "KALMAN", 11501, true},
		{[]string{"KM-E-1-1", "KM-P-2-1"}, "", 0, false},
		{[]string{"KM-E-1-1", "KM-SCALP-E-1-1"}, "", 0, false},
		{[]string{"KM-E-1-1", "OUTRO-1"}, "", 0, false},
		{nil, "", 0, false},
	}
	for _, c := range casos {
		mod, op, ok := mesmaOperacao(c.cls)
		if ok != c.ok || (ok && (mod != c.modulo || op != c.op)) {
			t.Errorf("mesmaOperacao(%v) = %q %d %v, esperava %q %d %v", c.cls, mod, op, ok, c.modulo, c.op, c.ok)
		}
	}
}

// v1.23: o tráfego de um dia passado, do registro — só os eventos daquele dia, numerados 1..N, com a
// operação pelo ClOrdID e o dono pelas operações do dia; sem a Flexscan, ok=false; dia inválido, ErrDia.
func TestTrafegoFlexscanDoDia(t *testing.T) {
	mem := repo.NovaMemoria()
	m := &Motor{repo: repo.Medir(mem)}
	if _, ok, _ := m.TrafegoFlexscanDoDia("2026-10-28"); ok {
		t.Fatal("sem a Flexscan: ok=true")
	}
	m.flx = &envioTeste{reg: &registro{}}
	if _, _, err := m.TrafegoFlexscanDoDia("28/10/2026"); !errors.Is(err, ErrDia) {
		t.Fatalf("dia inválido: %v", err)
	}
	if err := mem.SalvarOperacao(&dominio.Operacao{Data: "2026-10-28", Modulo: "KALMAN", OperacaoID: 234376,
		UID: 5725, Lado: dominio.Compra, Estado: dominio.Zerada, CriadaEm: time.Now()}); err != nil {
		t.Fatal(err)
	}
	brt := func(d, h, mi, s int) time.Time { return time.Date(2026, 10, d, h, mi, s, 0, relogio.Fuso) }
	for _, e := range []repo.EventoFIX{
		{Direcao: "ENV", MsgType: "FLX-LOGIN", Texto: "ontem", Codigo: "200", Em: brt(27, 17, 0, 0)},
		{Direcao: "ENV", MsgType: "FLX-LOGIN", Texto: "partida OK", Codigo: "200", Em: brt(28, 8, 45, 0)},
		{Direcao: "ENV", MsgType: "FLX-POST", ClOrdID: "KM-E-234376-1", Bruta: "POST /orders qtty=1 side=1 symbol=WINZ26", Em: brt(28, 9, 5, 0)},
		{Direcao: "REC", MsgType: "FLX-202", ClOrdID: "KM-E-234376-1", Texto: "POST ", Bruta: `{"id":"abc"}`, Em: brt(28, 9, 5, 1)},
		{Direcao: "ENV", MsgType: "FLX-LOGIN", Texto: "amanha", Codigo: "200", Em: brt(29, 0, 0, 0)},
	} {
		if _, err := mem.RegistrarEvento(e); err != nil {
			t.Fatal(err)
		}
	}
	r, ok, err := m.TrafegoFlexscanDoDia("2026-10-28")
	if !ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	ls := r["mensagens"].([]flexscan.LinhaTrafego)
	if len(ls) != 2 || r["ultimo_seq"] != 2 || r["dia"] != "2026-10-28" || r["cortado"] != false {
		t.Fatalf("resposta: %v", r)
	}
	post := ls[1]
	if ls[0].Tipo != flexscan.TrafegoLogin || ls[0].Seq != 1 || post.Seq != 2 || post.Tipo != flexscan.TrafegoPOST ||
		post.OperacaoID == nil || *post.OperacaoID != 234376 || post.UID == nil || *post.UID != 5725 {
		t.Fatalf("linhas: %+v", ls)
	}
	if r, _, _ := m.TrafegoFlexscanDoDia("2026-10-20"); len(r["mensagens"].([]flexscan.LinhaTrafego)) != 0 {
		t.Fatalf("dia sem eventos: %v", r)
	}
}
