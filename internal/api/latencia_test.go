package api

import (
	"testing"
	"time"
)

// v1.22 §3: p50 e máximo das últimas janelaLatencia; o máximo do dia com a hora, zerado na virada.
func TestLatencias(t *testing.T) {
	l := novasLatencias()
	dia := time.Date(2026, 10, 28, 10, 0, 0, 0, time.Local)
	for i := 1; i <= janelaLatencia+100; i++ {
		d := time.Duration(i) * time.Millisecond
		if i == 50 {
			d = 2200 * time.Millisecond // o de 10:45:59, fora da janela no fim
		}
		l.observar("STOP", d, 200, dia.Add(time.Duration(i)*time.Second))
	}
	r := l.resumo(dia)["STOP"].(map[string]any)
	if r["n"] != janelaLatencia || r["max_ms"] != float64(janelaLatencia+100) || r["p50_ms"] != float64(351) {
		t.Fatalf("janela: %v", r)
	}
	if r["max_dia_ms"] != float64(2200) || r["max_dia_em"] != dia.Add(50*time.Second).Format("2006-01-02T15:04:05.000-07:00") {
		t.Fatalf("máximo do dia: %v", r)
	}
	r = l.resumo(dia.Add(24 * time.Hour))["STOP"].(map[string]any)
	if r["max_dia_ms"] != nil {
		t.Fatalf("outro dia: %v", r)
	}
	l.observar("STOP", time.Millisecond, 200, dia.Add(24*time.Hour))
	if r = l.resumo(dia.Add(24 * time.Hour))["STOP"].(map[string]any); r["max_dia_ms"] != float64(1) {
		t.Fatalf("virada: %v", r)
	}
}
