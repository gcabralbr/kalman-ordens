package cenario

import (
	"os"
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/roteiro"
)

// O executor tem de reprovar quando o roteiro espera outra coisa: cada mutação num roteiro que
// passa precisa fazer o cenário falhar no passo mutado.
func TestExecutorReprovaMutacoes(t *testing.T) {
	cs, err := roteiro.LerDiretorio("../../cenarios")
	if err != nil {
		t.Fatal(err)
	}
	porNumero := map[string]roteiro.Cenario{}
	for _, c := range cs {
		porNumero[c.Numero] = c
	}
	type mutacao struct {
		cenario, nome string
		mudar         func(c *roteiro.Cenario) bool
	}
	mutar := func(verbo, de, para string) func(c *roteiro.Cenario) bool {
		return func(c *roteiro.Cenario) bool {
			for i, p := range c.Passos {
				if p.Verbo != verbo {
					continue
				}
				for j, cp := range p.Campos {
					if cp.Valor == de {
						c.Passos[i].Campos = append([]roteiro.Campo(nil), p.Campos...)
						c.Passos[i].Campos[j].Valor = para
						return true
					}
				}
				for j, a := range p.Args {
					if a == de {
						c.Passos[i].Args = append([]string(nil), p.Args...)
						c.Passos[i].Args[j] = para
						return true
					}
				}
			}
			return false
		}
	}
	ms := []mutacao{
		{"01", "preço da entrada", mutar("recebe", "130020", "130025")},
		{"01", "gatilho da 1014", mutar("recebe", "129510", "129505")},
		{"01", "pnl do ZERADA", mutar("evento", "pnl_pts=90", "pnl_pts=95")},
		{"07", "código de alerta", mutar("alerta", "STOP_FURADO", "SAIDA_ESGOTOU")},
		{"05", "código de recusa", mutar("resposta", "codigo=SEM_POSICAO", "codigo=DUPLICADA")},
		{"02", "quantidade do replace", mutar("recebe", "KM-P-1002-2", "KM-P-1002-9")},
		{"13", "nada com mensagem pendente", func(c *roteiro.Cenario) bool {
			// tira o recebe do cancel: o 'nada' seguinte tem de pegar o 35=F
			for i, p := range c.Passos {
				if p.Verbo == "recebe" && len(p.Campos) > 0 && strings.Contains(p.Campos[1].Valor, "KM-E-1301-2") {
					c.Passos = append(c.Passos[:i:i], c.Passos[i+2:]...) // remove recebe e o envia do 39=4
					return true
				}
			}
			return false
		}},
	}
	for _, mu := range ms {
		mu := mu
		t.Run(mu.cenario+" "+mu.nome, func(t *testing.T) {
			c := porNumero[mu.cenario]
			c.Passos = append([]roteiro.Passo(nil), c.Passos...)
			if !mu.mudar(&c) {
				t.Fatal("mutação não se aplicou ao roteiro")
			}
			dir, _ := os.MkdirTemp("", "mutacao")
			if err := Executar(c, dir); err == nil {
				t.Fatal("o executor aceitou um roteiro mutado")
			} else {
				t.Logf("reprovado como devia: %v", strings.SplitN(err.Error(), "\n", 2)[0])
			}
		})
	}
}
