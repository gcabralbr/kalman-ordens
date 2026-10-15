package metricas

import (
	"bytes"
	"strings"
	"testing"
)

// O texto segue o formato de exposição do Prometheus: HELP/TYPE, baldes acumulados com "le",
// +Inf, _sum e _count; rótulos escapados.
func TestFormato(t *testing.T) {
	h := NovoHistograma("teste_h_segundos", "ajuda h", "papel")
	h.Observar(0.0004, "ENTRADA")
	h.Observar(0.003, "ENTRADA")
	h.Observar(100, "ENTRADA")
	c := NovoContador("teste_c_total", "ajuda c", "codigo")
	c.Inc(`A"B`)
	c.Somar(2, `A"B`)
	var b bytes.Buffer
	Escrever(&b)
	s := b.String()
	for _, quer := range []string{
		"# HELP kalman_ordens_teste_h_segundos ajuda h\n# TYPE kalman_ordens_teste_h_segundos histogram\n",
		`kalman_ordens_teste_h_segundos_bucket{papel="ENTRADA",le="0.0005"} 1` + "\n",
		`kalman_ordens_teste_h_segundos_bucket{papel="ENTRADA",le="0.0025"} 1` + "\n",
		`kalman_ordens_teste_h_segundos_bucket{papel="ENTRADA",le="0.005"} 2` + "\n",
		`kalman_ordens_teste_h_segundos_bucket{papel="ENTRADA",le="60"} 2` + "\n",
		`kalman_ordens_teste_h_segundos_bucket{papel="ENTRADA",le="+Inf"} 3` + "\n",
		`kalman_ordens_teste_h_segundos_sum{papel="ENTRADA"} 100.0034` + "\n",
		`kalman_ordens_teste_h_segundos_count{papel="ENTRADA"} 3` + "\n",
		"# TYPE kalman_ordens_teste_c_total counter\n",
		`kalman_ordens_teste_c_total{codigo="A\"B"} 3` + "\n",
		"# TYPE kalman_ordens_fila_capacidade gauge\n",
	} {
		if !strings.Contains(s, quer) {
			t.Errorf("falta %q", quer)
		}
	}
	if h.Contagem("ENTRADA") != 3 || c.Valor(`A"B`) != 3 {
		t.Fatal("leitura")
	}
}
