package loopback

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// O detector detecta: um listener em todas as interfaces é apontado; um em 127.0.0.1, não.
func TestDetector(t *testing.T) {
	if len(EnderecosExternos()) == 0 {
		t.Skip("máquina sem IP externo")
	}
	ruim, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ruim.Close()
	bom, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bom.Close()
	if len(Expostas(ruim.Addr().(*net.TCPAddr).Port)) == 0 {
		t.Fatal("listener em 0.0.0.0 não foi detectado")
	}
	if r := Expostas(bom.Addr().(*net.TCPAddr).Port); len(r) > 0 {
		t.Fatalf("listener em 127.0.0.1 apontado como exposto: %v", r)
	}
}

// Varredura do código: todo listener do repositório é em 127.0.0.1 literal. Exceções conferidas
// à mão ficam na lista; um listener novo que não caia nas regras faz este teste falhar.
var (
	reListen   = regexp.MustCompile(`net\.Listen\(\s*"tcp"\s*,\s*([^)]*)\)`)
	reProibido = regexp.MustCompile(`ListenAndServe|net\.ListenPacket|net\.ListenTCP|NewUnstartedServer|httptest\.serve|SocketAcceptHost`)
	excecoes   = map[string]string{
		// endereço do serviço: validado como loopback antes (main.go, soLoopback)
		"cmd/kalman-ordens/main.go": `net.Listen("tcp", endereco)`,
	}
	// SocketAcceptHost só é aceito nas configurações globais (a sessão ignora a chave).
	reAcceptHost = regexp.MustCompile(`GlobalSettings\(\)\.Set\(config\.SocketAcceptHost,\s*"127\.0\.0\.1"\)`)
)

func TestListenersDoRepositorioEmLoopback(t *testing.T) {
	raiz := filepath.Join("..", "..")
	err := filepath.WalkDir(raiz, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "referencia") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, filepath.Join("loopback", "loopback_test.go")) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(raiz, p)
		rel = filepath.ToSlash(rel)
		for i, linha := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(linha), "//") {
				continue
			}
			if m := reListen.FindStringSubmatch(linha); m != nil {
				arg := strings.TrimSpace(m[1])
				literal := strings.HasPrefix(arg, `"127.0.0.1:`) || strings.HasPrefix(arg, `fmt.Sprintf("127.0.0.1:`)
				excecao := excecoes[rel] != "" && strings.Contains(linha, excecoes[rel])
				if !literal && !excecao {
					t.Errorf("%s:%d: listener sem 127.0.0.1 literal: %s", rel, i+1, strings.TrimSpace(linha))
				}
			}
			if reProibido.MatchString(linha) && !reAcceptHost.MatchString(linha) {
				t.Errorf("%s:%d: listener fora das regras de loopback: %s", rel, i+1, strings.TrimSpace(linha))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
