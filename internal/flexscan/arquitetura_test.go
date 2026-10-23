package flexscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-FLEXSCAN-v1 §2 item 8: as contas são as mesmas do GX. A ponte não tem código para o
// cancelamento em massa por conta (/algos/commands), para apagar todos os webhooks do usuário
// (DELETE /webhooks) nem para cadastrar removendo os anteriores (remove_previous).
func TestSemChamadasProibidas(t *testing.T) {
	proibidas := []string{"/algos/commands", "remove_previous", "/algos", "/webhooks"}
	err := filepath.WalkDir(".", func(caminho string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(caminho, ".go") || strings.HasSuffix(caminho, "_test.go") {
			return err
		}
		b, err := os.ReadFile(caminho)
		if err != nil {
			return err
		}
		for _, linha := range strings.Split(string(b), "\n") {
			l := strings.TrimSpace(linha)
			if strings.HasPrefix(l, "//") {
				continue
			}
			for _, p := range proibidas {
				// o falso responde GET /webhooks (a CLI de listagem, fora do mínimo útil)
				if strings.Contains(l, p) && !(strings.HasPrefix(caminho, "falso") && p == "/webhooks") {
					t.Errorf("%s: %q em %q", caminho, p, l)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
