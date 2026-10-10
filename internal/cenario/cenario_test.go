package cenario

import (
	"os"
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/roteiro"
)

// TestRoteiros roda cenarios/*.fix no backend em memória. ROTEIRO=01,16b roda só esses.
func TestRoteiros(t *testing.T) { rodar(t, Memoria) }

// TestRoteirosMariaDB roda os mesmos roteiros sobre o MariaDB de teste (container
// mariadb:10.11.15). Critério da F0: os dois backends verdes.
// KALMAN_TESTE_MARIADB=kalman:kalman@tcp(127.0.0.1:33306)/kalmanordens_teste
func TestRoteirosMariaDB(t *testing.T) {
	dsn := os.Getenv("KALMAN_TESTE_MARIADB")
	if dsn == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio: roteiros sobre MariaDB não rodaram")
	}
	rodar(t, MariaDB(dsn))
}

func rodar(t *testing.T, b Backend) {
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
			if err := Executar(c, dir, b); err != nil {
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
