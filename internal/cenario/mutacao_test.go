package cenario

import (
	"slices"
	"strings"
	"testing"
	"time"

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
		{"23", "reenvio não marcado como repetido", mutar("resposta", "repetido=true", "repetido=null")},
		{"24", "503 virou 200", mutar("resposta", "_status=503", "_status=200")},
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
			if err := Executar(c, t.TempDir(), Memoria); err == nil {
				t.Fatal("o executor aceitou um roteiro mutado")
			} else {
				t.Logf("reprovado como devia: %v", strings.SplitN(err.Error(), "\n", 2)[0])
			}
		})
	}
}

// v1.33 (SPEC-ORDENS-v1.33 §1): o recebe de uma mensagem que não vem falha assim que o serviço para
// ("o serviço parou sem mandar"), não depois de um prazo no relógio real — e o roteiro que espera
// o 503 com o motor travado continua vendo o 503 (os roteiros 24 e 62, sem HTTP_ESPERA_S curto).
func TestRecebeSemMensagemFalhaNaHora(t *testing.T) {
	cs, err := roteiro.LerDiretorio("../../cenarios")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Numero != "72" {
			continue
		}
		// até o primeiro nada (as três entradas da rajada já recebidas) e um recebe que não vem
		k := slices.IndexFunc(c.Passos, func(p roteiro.Passo) bool { return p.Verbo == "nada" })
		if k < 0 {
			t.Fatal("o 72 sem nada")
		}
		falso := c.Passos[k-1]
		falso.Origem = "inserido"
		falso.Campos = append([]roteiro.Campo(nil), falso.Campos...)
		for i := range falso.Campos {
			if falso.Campos[i].Tag == 11 {
				falso.Campos[i].Valor = "KM-E-7299-1"
			}
		}
		c.Passos = append(append([]roteiro.Passo(nil), c.Passos[:k+1]...), falso)
		dir := t.TempDir()
		t0 := time.Now()
		err := Executar(c, dir, Memoria)
		if err == nil || !strings.HasPrefix(err.Error(), "inserido: recebe:") || !strings.Contains(err.Error(), "o serviço parou sem mandar") {
			t.Fatalf("esperava a falha do recebe inserido pelo serviço parado: %v", err)
		}
		if d := time.Since(t0); d > esperaServico/3 {
			t.Fatalf("o roteiro levou %s: o recebe não falhou na hora", d)
		}
		return
	}
	t.Fatal("roteiro 72 não encontrado")
}
