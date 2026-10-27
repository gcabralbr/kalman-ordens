package flexscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-FLEXSCAN-v1 §2 item 8: as contas são as mesmas do GX. A ponte não tem código para o
// cancelamento em massa por conta (/algos/commands), para apagar todos os webhooks do usuário
// (DELETE /webhooks) nem para cadastrar removendo os anteriores (remove_previous). v1.21: a
// conferência e o recadastro do nosso webhook ficam só no webhook_cadastro.go, só com GET e POST.
func TestSemChamadasProibidas(t *testing.T) {
	proibidas := []string{"/algos/commands", "remove_previous", "/algos", "/webhooks"}
	cadastro, err := os.ReadFile("webhook_cadastro.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"MethodDelete", `"DELETE"`, "MethodPut", "remove"} {
		if strings.Contains(string(cadastro), p) {
			t.Errorf("webhook_cadastro.go: %q (só GET e POST, sem remover cadastros)", p)
		}
	}
	err = filepath.WalkDir(".", func(caminho string, d os.DirEntry, err error) error {
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
				if strings.Contains(l, p) && !((strings.HasPrefix(caminho, "falso") || caminho == "webhook_cadastro.go") && p == "/webhooks") {
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
