package motor

import "testing"

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
