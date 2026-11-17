package motor

import (
	"strconv"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// v1.36.2 §3.1: os textos reais de 17/11 (234777, 234778, 234779) casam; o 35=9 da corrida e o
// texto do robô ao aceitar não.
func TestTextoDaRecusaDaCorretora(t *testing.T) {
	for _, s := range []string{
		"This strategy was changed to Error state, because offer RealOrder Id: 178312978 is Rejected.",
		"This strategy was changed to Error state, because offer RealOrder Id: 178313833 is Rejected.",
		"this strategy was CHANGED TO ERROR STATE, because offer RealOrder Id: 178314346 is rejected",
	} {
		if !reRecusaCorretora.MatchString(s) {
			t.Errorf("não casou: %q", s)
		}
	}
	for _, s := range []string{
		"Order cannot be canceled. It has ended already (VO status: Cancelled);",
		"NEW DSA Order request sent to Robot",
		"",
	} {
		if reRecusaCorretora.MatchString(s) {
			t.Errorf("casou: %q", s)
		}
	}
}

// v1.36.2 §3.2: o 35=9 que diz que a ordem já terminou (39 terminal ou 102=1).
func TestCancelRecusadoPorJaTerminar(t *testing.T) {
	msg := func(campos map[int]string) pnt.Mensagem {
		b := "35=9\x01"
		for k, v := range campos {
			b += strconv.Itoa(k) + "=" + v + "\x01"
		}
		return pnt.Ler(b)
	}
	for _, c := range []struct {
		campos map[int]string
		quer   bool
	}{
		{map[int]string{39: "4", 102: "1"}, true}, // 17/11, a 234779
		{map[int]string{39: "2", 102: "0"}, true},
		{map[int]string{39: "8"}, true},
		{map[int]string{39: "C"}, true},
		{map[int]string{39: "0", 102: "1"}, true},
		{map[int]string{39: "0", 102: "0"}, false}, // viva: o caminho de antes
		{map[int]string{39: "1"}, false},
	} {
		if got := jaTerminou(msg(c.campos)); got != c.quer {
			t.Errorf("%v: %v", c.campos, got)
		}
	}
}

// v1.36.2 §4.2: as recusas da corretora do dia, por uid, do banco: só entradas REAL com o
// motivo_rejeicao; a última pela hora da última mensagem; o motivo pelo estado da ordem.
func TestRecusasCorretoraHoje(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC))
	r := repo.NovaMemoria()
	m := &Motor{amb: dominio.AmbienteSimulador, rel: rel, repo: r}
	s := NovoServico(m)
	dia := m.hoje()
	op := func(id int64, uid int, amb string) {
		t.Helper()
		if err := r.SalvarOperacao(&dominio.Operacao{Modulo: "KALMAN", OperacaoID: id, UID: uid, Ambiente: amb, Data: dia}); err != nil {
			t.Fatal(err)
		}
	}
	ordem := func(opID int64, papel dominio.Papel, estado dominio.EstadoOrdem, motivo string) int64 {
		t.Helper()
		o := &dominio.Ordem{Modulo: "KALMAN", OperacaoID: opID, Papel: papel, Estado: estado, MotivoRejeicao: motivo,
			ClOrdAtual: "KM-E-" + strconv.FormatInt(opID, 10) + "-1", Data: dia, CriadaEm: rel.Agora()}
		if err := r.SalvarOrdem(o); err != nil {
			t.Fatal(err)
		}
		return o.ID
	}
	const erroRobo = "This strategy was changed to Error state, because offer RealOrder Id: 178314346 is Rejected."
	op(234777, 1450, dominio.AmbienteReal)
	op(234779, 1450, dominio.AmbienteReal)
	op(234780, 1450, dominio.AmbienteReal)
	op(234781, 1450, dominio.AmbienteReal)
	op(500, 1450, dominio.AmbienteSimulador)
	op(600, 5725, dominio.AmbienteReal)
	a := ordem(234777, dominio.Entrada, dominio.Cancelada, "This strategy was changed to Error state, because offer RealOrder Id: 178312978 is Rejected.")
	b := ordem(234779, dominio.Entrada, dominio.Cancelada, erroRobo)
	ordem(234780, dominio.Entrada, dominio.Cancelada, "")                      // cancelada sem texto: não conta
	ordem(234781, dominio.Protecao, dominio.Cancelada, erroRobo)               // 1014: não conta
	ordem(500, dominio.Entrada, dominio.Cancelada, erroRobo)                   // SIMULADOR: não conta
	c := ordem(600, dominio.Entrada, dominio.Rejeitada, "[1] Robot not found") // 39=8 do 5725
	ev := func(ordemID int64, em time.Time) {
		t.Helper()
		if _, err := r.RegistrarEvento(repo.EventoFIX{OrdemID: ordemID, Em: em, Direcao: "REC", MsgType: "8"}); err != nil {
			t.Fatal(err)
		}
	}
	ev(a, rel.Agora().Add(10*time.Second))
	ev(b, rel.Agora().Add(20*time.Second))
	ev(c, rel.Agora())

	todos, err := s.recusasCorretoraHoje(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(todos[1450]) != 2 || len(todos[5725]) != 1 {
		t.Fatalf("contagem: 1450=%d 5725=%d", len(todos[1450]), len(todos[5725]))
	}
	so, _ := s.recusasCorretoraHoje(1450)
	f := s.recusasDaFicha(so[1450])
	u, _ := f["ultima"].(map[string]any)
	if f["quantidade"] != 2 || u == nil || u["operacao_id"] != int64(234779) || u["motivo"] != "REJEITADA_CORRETORA" || u["texto"] != erroRobo ||
		u["clordid"] != "KM-E-234779-1" || u["em"] != emBR(rel.Agora().Add(20*time.Second)) {
		t.Fatalf("ficha do 1450: %v", f)
	}
	f = s.recusasDaFicha(todos[5725])
	if u, _ := f["ultima"].(map[string]any); u["motivo"] != "REJEITADA" || u["texto"] != "[1] Robot not found" {
		t.Fatalf("ficha do 5725: %v", f)
	}
	if f := s.recusasDaFicha(nil); f["quantidade"] != 0 || f["ultima"] != nil {
		t.Fatalf("sem recusa: %v", f)
	}
}
