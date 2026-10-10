package alerta

import (
	"os"
	"regexp"
	"testing"
)

// O catálogo em Go é o mesmo de docs/ALERTAS.md (código e crítico).
func TestCatalogoIgualAoDocumento(t *testing.T) {
	doc, err := os.ReadFile("../../docs/ALERTAS.md")
	if err != nil {
		t.Fatal(err)
	}
	// só a tabela de alertas (antes da seção de recusas)
	secao := regexp.MustCompile(`(?s)## Alertas(.*?)## Códigos de recusa`).FindSubmatch(doc)
	if secao == nil {
		t.Fatal("seção de alertas não encontrada")
	}
	linhas := regexp.MustCompile("(?m)^\\| `([A-Z_0-9]+)` \\|.*\\| (sim|não) \\|$").FindAllSubmatch(secao[1], -1)
	vistos := map[string]bool{}
	for _, l := range linhas {
		cod, cr := string(l[1]), string(l[2]) == "sim"
		vistos[cod] = true
		got, ok := Catalogo[cod]
		if !ok {
			t.Errorf("%s está no ALERTAS.md e falta no catálogo Go", cod)
		} else if got != cr {
			t.Errorf("%s: crítico no documento=%v, no Go=%v", cod, cr, got)
		}
	}
	for cod := range Catalogo {
		if !vistos[cod] {
			t.Errorf("%s está no catálogo Go e falta no ALERTAS.md", cod)
		}
	}
}
