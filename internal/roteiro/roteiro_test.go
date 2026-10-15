package roteiro

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const dirCenarios = "../../cenarios"

// Todos os roteiros do repositório são lidos sem erro.
func TestTodosOsRoteirosLeem(t *testing.T) {
	cs, err := LerDiretorio(dirCenarios)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 81 {
		t.Fatalf("esperava 81 roteiros, li %d", len(cs))
	}
	for _, c := range cs {
		if len(c.Passos) == 0 {
			t.Errorf("%s: sem passos", c.Arquivo)
		}
		t.Logf("%-40s %3d passos  %s", c.Arquivo, len(c.Passos), c.Titulo)
	}
}

// resposta só depois de um pedido HTTP; códigos de alerta e de recusa existem no catálogo.
func TestCoerencia(t *testing.T) {
	cat, err := os.ReadFile(filepath.Join(dirCenarios, "..", "docs", "ALERTAS.md"))
	if err != nil {
		t.Fatal(err)
	}
	codigos := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([A-Z_0-9]+)`").FindAllStringSubmatch(string(cat), -1) {
		codigos[m[1]] = true
	}
	cs, err := LerDiretorio(dirCenarios)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		pedido := false
		for _, p := range c.Passos {
			switch p.Verbo {
			case "entrada", "saida", "stop", "http", "http_aguarda":
				pedido = true
			case "resposta":
				if !pedido {
					t.Errorf("%s: resposta sem pedido antes", p.Origem)
				}
				for _, a := range p.Args {
					if v, ok := strings.CutPrefix(a, "codigo="); ok && !codigos[v] {
						t.Errorf("%s: código de recusa %s fora do ALERTAS.md", p.Origem, v)
					}
				}
			case "alerta", "sem_alerta":
				if !codigos[p.Args[0]] {
					t.Errorf("%s: código de alerta %s fora do ALERTAS.md", p.Origem, p.Args[0])
				}
			}
		}
	}
}

// Erros de roteiro aparecem com arquivo e linha.
func TestErros(t *testing.T) {
	dir := t.TempDir()
	comum := filepath.Join(dir, "_comum.fix")
	os.WriteFile(comum, []byte("modelo ER 35=8 1=12345\n"), 0o644)
	casos := map[string]string{
		"captura antes":    "cenario 01 x\nhora 2026-10-13 10:00:00\nenvia @ER 584=$MS\n",
		"verbo":            "cenario 01 x\nhora 2026-10-13 10:00:00\npula 3s\n",
		"duracao":          "cenario 01 x\nhora 2026-10-13 10:00:00\navanca tres\n",
		"modelo":           "cenario 01 x\nhora 2026-10-13 10:00:00\nenvia @NAO 11=1\n",
		"aspas":            "cenario 01 x\nhora 2026-10-13 10:00:00\nenvia 35=8 58=\"aberta\n",
		"sem hora":         "cenario 01 x\nnada\n",
		"captura em envia": "cenario 01 x\nhora 2026-10-13 10:00:00\nenvia 35=8 11=>X\n",
	}
	for nome, txt := range casos {
		a := filepath.Join(dir, "01-x.fix")
		os.WriteFile(a, []byte(txt), 0o644)
		if _, err := Ler(comum, a); err == nil {
			t.Errorf("%s: devia falhar", nome)
		} else if !strings.Contains(err.Error(), "01-x.fix") {
			t.Errorf("%s: erro sem arquivo: %v", nome, err)
		}
	}
}

// Modelo + sobrescrita mantém a posição do modelo; parâmetros de bloco são substituídos.
func TestModeloEBloco(t *testing.T) {
	dir := t.TempDir()
	comum := filepath.Join(dir, "_comum.fix")
	os.WriteFile(comum, []byte("modelo ER 35=8 1=12345 40=2\nbloco B OP\n  envia @ER 11=KM-E-$OP-1 40=1\nfim\n"), 0o644)
	a := filepath.Join(dir, "01-x.fix")
	os.WriteFile(a, []byte("cenario 01 x\nhora 2026-10-13 10:00:00\nexecuta B OP=77\n"), 0o644)
	c, err := Ler(comum, a)
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, f := range c.Passos[0].Campos {
		got += " " + f.Valor
	}
	if got != " 8 12345 1 KM-E-77-1" {
		t.Fatalf("campos: %q", got)
	}
}
