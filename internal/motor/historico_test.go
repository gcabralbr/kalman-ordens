package motor

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// fix_raw do histórico (cru=1): senha mascarada e SOH legível.
func TestFixLegivel(t *testing.T) {
	bruta := "8=FIX.4.4\x019=70\x0135=A\x01553=GAMMAQ2\x01554=segredo123\x01925=nova456\x0110=000\x01"
	if got := FixLegivel(bruta); got != "8=FIX.4.4|9=70|35=A|553=GAMMAQ2|554=***|925=***|10=000|" {
		t.Fatalf("FixLegivel = %q", got)
	}
}

func TestDia(t *testing.T) {
	de, ate, err := Dia("2026-10-15")
	if err != nil || de.Format("2006-01-02T15:04:05Z07:00") != "2026-10-15T00:00:00-03:00" || ate.Sub(de).Hours() != 24 {
		t.Fatalf("Dia: %v %v %v", de, ate, err)
	}
	for _, d := range []string{"", "15/10/2026", "2026-13-01"} {
		if _, _, err := Dia(d); err == nil {
			t.Errorf("Dia(%q) aceitou", d)
		}
	}
}

// v1.20: stop_origem nulo = desconhecido (operação anterior à v1.19, sem a coluna preenchida); só
// a operação com a origem gravada a traz.
func TestStopOrigemDesconhecidaENula(t *testing.T) {
	r := repo.NovaMemoria()
	for _, o := range []dominio.Operacao{
		{Data: "2026-10-27", Modulo: "KALMAN", OperacaoID: 234335, UID: 5725, Lado: dominio.Compra, StopAjustado: 178420, Estado: dominio.Zerada, CriadaEm: time.Now()},
		{Data: "2026-10-27", Modulo: "KALMAN", OperacaoID: 234400, UID: 5725, Lado: dominio.Compra, StopOrigem: "TRSP", Estado: dominio.Zerada, CriadaEm: time.Now()},
	} {
		if err := r.SalvarOperacao(&o); err != nil {
			t.Fatal(err)
		}
	}
	l, err := OperacoesDoDia(r, "2026-10-27", 0, "")
	if err != nil || len(l) != 2 {
		t.Fatalf("OperacoesDoDia: %v %v", l, err)
	}
	if l[0]["stop_origem"] != nil || l[1]["stop_origem"] != "TRSP" {
		t.Fatalf("stop_origem: %v, %v", l[0]["stop_origem"], l[1]["stop_origem"])
	}
	op := &operacao{Operacao: dominio.Operacao{StopAjustado: 178420}}
	if op.origemDoCalculo() != "" || op.stopOrigem() != nil {
		t.Fatalf("ajuste sem origem gravada: %q %v", op.origemDoCalculo(), op.stopOrigem())
	}
}
