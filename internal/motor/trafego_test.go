package motor

import "testing"

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
