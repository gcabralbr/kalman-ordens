package aovivo

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

func fix(campos ...string) []byte {
	return []byte("8=FIX.4.4\x019=10\x01" + strings.Join(campos, "\x01") + "\x0110=000\x01")
}

func TestMonitor(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC))
	m := Novo(rel)
	m.Associar("KALMAN", 7501, 7)
	m.Registrar(false, fix("35=A", "34=1", "553=KALMAN", "554=segredo", "98=0"))
	m.Registrar(true, fix("35=A", "34=1", "98=0"))
	rel.Avancar(time.Second)
	m.Registrar(false, fix("35=D", "34=2", "11=KM-E-7501-1", "54=1", "38=1", "44=130020"))
	rel.Avancar(40 * time.Millisecond)
	m.Registrar(true, fix("35=8", "34=2", "11=KM-E-7501-1", "37=dsa.1", "150=0", "39=0", "54=1", "38=1", "44=130020"))
	m.Registrar(true, fix("35=8", "34=3", "11=KM-PT-E-9-1", "150=0", "39=0"))     // outro módulo, uid desconhecido
	m.Registrar(true, fix("35=8", "34=4", "11=OUTRO-1", "150=0", "39=0", "58=x")) // ordem que não é KM

	r := m.Consultar(0, 0, Estado{Logada: true, Fila: 3})
	l := r["mensagens"].([]Mensagem)
	if len(l) != 6 || r["ultimo_seq"] != int64(6) {
		t.Fatalf("mensagens %d, ultimo %v", len(l), r["ultimo_seq"])
	}
	if l[0].Direcao != "ENV" || l[0].MsgType != "A" || strings.Contains(l[0].FixRaw, "segredo") || !strings.Contains(l[0].FixRaw, "554=***|") {
		t.Fatalf("logon: %+v", l[0])
	}
	if l[0].Em != "2026-10-16T10:00:00.000-03:00" || l[1].Direcao != "REC" {
		t.Fatalf("em/direcao: %+v %+v", l[0], l[1])
	}
	d := l[2]
	if d.MsgType != "D" || *d.OperacaoID != 7501 || *d.UID != 7 || d.Campos["11"] != "KM-E-7501-1" || d.Campos["44"] != "130020" || len(d.Campos) != 4 {
		t.Fatalf("35=D: %+v", d)
	}
	if l[4].OperacaoID == nil || *l[4].OperacaoID != 9 || l[4].UID != nil || l[5].OperacaoID != nil || l[5].Campos["58"] != "x" {
		t.Fatalf("operação: %+v / %+v", l[4], l[5])
	}
	s := r["saude"].(map[string]any)
	if s["boot"] != "2026-10-16T10:00:00.000-03:00" || s["logada"] != true || s["logada_desde"] != "2026-10-16T10:00:00.000-03:00" || s["seq_entrada"] != 4 || s["seq_saida"] != 2 ||
		s["msgs_min"] != 6 || s["fila"] != 3 || s["parada"] != nil || s["latencia_aceite_p50_ms"] != 40.0 || s["latencia_aceite_max_ms"] != 40.0 ||
		s["ultima_enviada_em"] != "2026-10-16T10:00:01.000-03:00" || s["ultima_recebida_em"] != "2026-10-16T10:00:01.040-03:00" {
		t.Fatalf("saúde: %v", s)
	}
	if r := m.Consultar(4, 0, Estado{}); len(r["mensagens"].([]Mensagem)) != 2 || r["saude"].(map[string]any)["logada_desde"] != nil {
		t.Fatalf("desde 4 / deslogada: %v", r)
	}
	if r := m.Consultar(0, 2, Estado{}); len(r["mensagens"].([]Mensagem)) != 2 || r["mensagens"].([]Mensagem)[1].Seq != 2 {
		t.Fatalf("limite: %v", r)
	}
	// um minuto depois, msgs_min zera
	rel.Avancar(61 * time.Second)
	if s := m.Consultar(0, 0, Estado{})["saude"].(map[string]any); s["msgs_min"] != 0 {
		t.Fatalf("msgs_min: %v", s["msgs_min"])
	}
}

// O buffer guarda as últimas Capacidade; desde anterior a ele começa na mais antiga que restou.
func TestMonitorCircular(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC))
	m := Novo(rel)
	for i := 1; i <= Capacidade+150; i++ {
		m.Registrar(i%2 == 0, fix("35=0", fmt.Sprintf("34=%d", i)))
	}
	r := m.Consultar(0, LimiteMax, Estado{})
	l := r["mensagens"].([]Mensagem)
	if len(l) != Capacidade || l[0].Seq != 151 || l[len(l)-1].Seq != int64(Capacidade+150) || r["ultimo_seq"] != int64(Capacidade+150) {
		t.Fatalf("buffer: %d, %d..%d", len(l), l[0].Seq, l[len(l)-1].Seq)
	}
	if l := m.Consultar(0, 0, Estado{})["mensagens"].([]Mensagem); len(l) != LimitePadrao || l[0].Seq != 151 {
		t.Fatalf("limite padrão: %d", len(l))
	}
	if l := m.Consultar(int64(Capacidade+150), 0, Estado{})["mensagens"].([]Mensagem); len(l) != 0 {
		t.Fatalf("nada novo: %d", len(l))
	}
	if l := m.Consultar(0, 99999, Estado{})["mensagens"].([]Mensagem); len(l) != LimiteMax {
		t.Fatalf("limite máximo: %d", len(l))
	}
}

// Ritmo: 300 itens, um por segundo, até o atual; cada mensagem num contador (nada some).
func TestRitmo(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC))
	m := Novo(rel)
	env := func(c ...string) { m.Registrar(false, fix(c...)) }
	rec := func(c ...string) { m.Registrar(true, fix(c...)) }
	env("35=A")
	rec("35=A")
	rel.Avancar(2 * time.Second)
	env("35=D", "11=KM-E-1-1")
	env("35=D", "11=KM-PT-P-2-1")
	env("35=D", "11=KM-S-3-1")
	env("35=F", "11=KM-P-2-2")
	env("35=G", "11=KM-P-2-3")
	env("35=AF")
	env("35=0")
	env("35=1")
	env("35=2")
	env("35=4")
	env("35=5")
	for _, e := range []string{"A", "0", "F", "4", "5", "8", "7", "6", "E", "I", "D", "C"} {
		rec("35=8", "150="+e)
	}
	rec("35=9")
	rec("35=3")
	rec("35=j")
	rec("35=0")
	rec("35=1")
	rec("35=2")
	rec("35=4")
	rec("35=5")
	r := m.Consultar(0, 0, Estado{})["ritmo"].([]Segundo)
	if len(r) != JanelaRitmo || r[len(r)-1].T != "2026-10-16T10:00:02.000-03:00" || r[0].T != "2026-10-16T09:55:03.000-03:00" {
		t.Fatalf("janela: %d itens, %s..%s", len(r), r[0].T, r[len(r)-1].T)
	}
	s0, s2 := r[len(r)-3], r[len(r)-1]
	if s0.Sessao.Logon != (Sentidos{Env: 1, Rec: 1}) || s0.total != 2 {
		t.Fatalf("logon: %+v", s0)
	}
	e := s2.Enviadas
	if e != (Enviadas{Entrada: 1, Protecao: 1, Saida: 1, Cancel: 1, Replace: 1, AF: 1}) || s2.HeartbeatEnv != 1 || s2.HeartbeatRec != 1 {
		t.Fatalf("enviadas: %+v", s2)
	}
	if s2.Recebidas != (Recebidas{AceiteA: 1, Aceite0: 1, Fill: 1, Cancelada: 1, ReplaceOK: 1, Rejeicao: 1, Erro: 1, Status: 4, Outras: 1,
		CancelReject: 1, RejectSessao: 1, BmrJ: 1}) {
		t.Fatalf("recebidas: %+v", s2.Recebidas)
	}
	um := Sentidos{Env: 1, Rec: 1}
	if (s2.Sessao != Sessao{TestRequest: um, Resend: um, SeqReset: um, Logout: um}) {
		t.Fatalf("sessão: %+v", s2.Sessao)
	}
	// 300 s depois, o segundo do logon saiu da janela
	rel.Avancar(299 * time.Second)
	r = m.Consultar(0, 0, Estado{})["ritmo"].([]Segundo)
	if r[0].Enviadas.Entrada != 1 || r[0].T != "2026-10-16T10:00:02.000-03:00" {
		t.Fatalf("janela depois: %+v", r[0])
	}
	for _, x := range r[1:] {
		if x.total != 0 {
			t.Fatalf("segundo sem mensagem: %+v", x)
		}
	}
}
