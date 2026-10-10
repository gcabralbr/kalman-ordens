package cenario

import (
	"os"
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/roteiro"
)

// TestRoteiros roda cenarios/*.fix. ROTEIRO=01,16b roda só esses.
func TestRoteiros(t *testing.T) {
	cs, err := roteiro.LerDiretorio("../../cenarios")
	if err != nil {
		t.Fatal(err)
	}
	filtro := os.Getenv("ROTEIRO")
	for _, c := range cs {
		c := c
		if filtro != "" && !contem(strings.Split(filtro, ","), c.Numero) {
			continue
		}
		t.Run(c.Arquivo, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cenario-"+c.Numero)
			if err != nil {
				t.Fatal(err)
			}
			if err := Executar(c, dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func contem(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
