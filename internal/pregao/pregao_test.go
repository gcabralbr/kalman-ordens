package pregao

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiaDePregao(t *testing.T) {
	fuso := time.FixedZone("BRT", -3*3600)
	arq := filepath.Join(t.TempDir(), "feriados.json")
	if err := os.WriteFile(arq, []byte(`{"2026-11-20":"Consciencia Negra","2026-12-25":"Natal"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Novo(arq, fuso)
	d := func(s string) time.Time { x, _ := time.ParseInLocation("2006-01-02 15:04", s, fuso); return x }
	casos := []struct {
		quando string
		pregao bool
		motivo string
	}{
		{"2026-10-23 09:00", true, ""},         // sexta
		{"2026-10-24 09:00", false, "sabado"},  // 24/10: o FONTE_DEGRADADA alertou o dia todo
		{"2026-10-25 09:00", false, "domingo"},
		{"2026-10-26 09:00", true, ""},
		{"2026-11-20 09:00", false, "feriado: Consciencia Negra"},
		{"2026-12-25 23:59", false, "feriado: Natal"},
	}
	for _, k := range casos {
		p, m := c.DiaDePregao(d(k.quando))
		if p != k.pregao || m != k.motivo {
			t.Errorf("%s: %v %q, esperado %v %q", k.quando, p, m, k.pregao, k.motivo)
		}
	}
	// sem o arquivo, ou ilegível: seg–sex
	for _, a := range []string{filepath.Join(t.TempDir(), "nao-existe.json"), arq} {
		if a == arq {
			os.WriteFile(arq, []byte("{x"), 0o644)
		}
		c := Novo(a, fuso)
		if p, _ := c.DiaDePregao(d("2026-11-20 09:00")); !p {
			t.Errorf("%s: sem feriados legíveis a sexta vale", a)
		}
		if p, _ := c.DiaDePregao(d("2026-10-24 09:00")); p {
			t.Errorf("%s: sábado nunca é pregão", a)
		}
	}
}
