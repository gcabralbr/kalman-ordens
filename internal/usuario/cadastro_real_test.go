package usuario

import (
	"strconv"
	"strings"
	"testing"
)

// v1.36 §3.1: as causas do cadastro inutilizável no REAL.
func TestCadastroReal(t *testing.T) {
	soPnT := RegrasReal{PnT: true, ContasHML: []string{"99999"}}
	ambos := RegrasReal{PnT: true, Flexscan: true, ContasHMLFlx: []string{"S-1117150"}}
	porCorretora := RegrasReal{PnT: true, Flexscan: true, Corretoras: map[int]string{88: "CMC", 77: "XP"},
		Contas: map[int][]string{88: {"715124"}, 77: {"7001"}}}
	cad := func(conector string, cor int, conta string) string {
		c := ""
		if conector != "" {
			c = `"Conector":"` + conector + `",`
		}
		return `{"Robo":{` + c + `"CORSeqId":` + strconv.Itoa(cor) + `,"CCONroConta":"` + conta + `"}}`
	}
	casos := []struct {
		nome, texto string
		rg          RegrasReal
		causa       string // "" = utilizável
		trecho      string // no motivo
	}{
		{"1450 pelo PnT na BTG", cad("PNT", 85, "4582351"), soPnT, "", ""},
		{"JSON ilegivel", `{"Robo":`, soPnT, CausaIlegivel, "ilegivel"},
		{"vazio", ``, soPnT, CausaIlegivel, "vazio"},
		{"conta vazia", cad("PNT", 85, ""), soPnT, CausaConta, "conta vazia"},
		{"sem Robo.Conector (P1)", cad("", 85, "4582351"), soPnT, CausaConector, "sem Robo.Conector"},
		{"PnT so BTG", cad("PNT", 88, "4582351"), soPnT, CausaCorretora, "so BTG"},
		{"PnT sem sessao na maquina", cad("PNT", 85, "4582351"), RegrasReal{Flexscan: true}, CausaConector, "PNT nao atendido"},
		{"Flexscan sem ponte", cad("FLEXSCAN", 88, "715124"), soPnT, CausaConector, "FLEXSCAN nao atendido"},
		{"conector desconhecido", cad("XYZ", 85, "4582351"), soPnT, CausaConector, "desconhecido"},
		{"corretora 107", cad("FLEXSCAN", 107, "715124"), ambos, CausaCorretora, "107"},
		{"9999 no REAL", cad("FLEXSCAN", 9999, "QUANT"), ambos, CausaCorretora, "9999"},
		{"conta de simulador no REAL", cad("PNT", 85, "99999"), soPnT, CausaConta, "99999"},
		{"conta PnT com letra", cad("PNT", 85, "45A"), soPnT, CausaConta, "45A"},
		{"Flexscan unica na Capital", cad("FLEXSCAN", 88, "715124"), ambos, "", ""},
		{"guarda-chuva: conta na lista", cad("FLEXSCAN", 88, "715124"), porCorretora, "", ""},
		{"guarda-chuva: corretora declarada", cad("FLEXSCAN", 77, "7001"), porCorretora, "", ""},
		{"guarda-chuva: conta fora (P3)", cad("FLEXSCAN", 88, "715999"), porCorretora, CausaConta, "fora do guarda-chuva"},
		{"guarda-chuva: BTG sem credencial", cad("FLEXSCAN", 85, "9001"), porCorretora, CausaCorretora, "sem credencial"},
		{"guarda-chuva nao vale para o PnT", cad("PNT", 85, "4582351"), porCorretora, "", ""},
	}
	for _, c := range casos {
		r, f := CadastroReal(c.texto, c.rg)
		switch {
		case c.causa == "" && f != nil:
			t.Errorf("%s: falha inesperada %v", c.nome, f)
		case c.causa == "" && r.Conta == "":
			t.Errorf("%s: sem conta resolvida: %+v", c.nome, r)
		case c.causa != "" && f == nil:
			t.Errorf("%s: esperava %s, resolveu %+v", c.nome, c.causa, r)
		case c.causa != "" && (f.Codigo != c.causa || !strings.Contains(f.Motivo, c.trecho)):
			t.Errorf("%s: %s %q, esperava %s com %q", c.nome, f.Codigo, f.Motivo, c.causa, c.trecho)
		}
	}
}
