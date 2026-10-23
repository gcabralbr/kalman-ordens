package flexscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 23/10: o limite de logins vale entre restarts (uma guarda nova sobre o mesmo arquivo), o erro
// definitivo trava a mesma credencial e a senha trocada libera; a senha nunca vai para o arquivo.
func TestGuardaDeLoginEntreRestarts(t *testing.T) {
	arq := filepath.Join(t.TempDir(), "flexscan-login.json")
	t0 := time.Date(2026, 10, 23, 17, 6, 0, 0, time.UTC)
	for i := 0; i < 5; i++ { // cinco "partidas", uma tentativa cada
		g := novaGuarda(arq, 5, 10*time.Minute, "flx", "senha-1")
		if e := g.pode(t0.Add(time.Duration(i) * 5 * time.Second)); e != nil {
			t.Fatalf("tentativa %d recusada: %v", i+1, e)
		}
	}
	g := novaGuarda(arq, 5, 10*time.Minute, "flx", "senha-1")
	if e := g.pode(t0.Add(30 * time.Second)); e == nil || e.Definitivo || !strings.Contains(e.Motivo, "limite de 5 logins") {
		t.Fatalf("sexta partida em 30 s: %v", e)
	}
	if e := g.pode(t0.Add(11 * time.Minute)); e != nil {
		t.Fatalf("depois da janela: %v", e)
	}
	// erro definitivo: trava a credencial, inclusive depois de um restart
	g.resultado(&ErroLogin{Motivo: "login 403: User password blocked", Definitivo: true}, t0.Add(11*time.Minute))
	if e := novaGuarda(arq, 5, 10*time.Minute, "flx", "senha-1").pode(t0.Add(30 * time.Minute)); e == nil || !e.Definitivo || !strings.Contains(e.Motivo, "bloqueado") {
		t.Fatalf("bloqueio depois do restart: %v", e)
	}
	// senha trocada: libera; o sucesso limpa o bloqueio
	g2 := novaGuarda(arq, 5, 10*time.Minute, "flx", "senha-2")
	if e := g2.pode(t0.Add(31 * time.Minute)); e != nil {
		t.Fatalf("senha trocada: %v", e)
	}
	g2.resultado(nil, t0.Add(31*time.Minute))
	b, _ := os.ReadFile(arq)
	if strings.Contains(string(b), "senha-") || strings.Contains(string(b), "bloqueio") {
		t.Fatalf("arquivo: %s", b)
	}
	if st, _ := os.Stat(arq); st.Mode().Perm() != 0o600 {
		t.Fatalf("permissão %v", st.Mode().Perm())
	}
}

// Sem conseguir gravar a tentativa, não tenta (o limite entre restarts não valeria).
func TestGuardaSemGravarNaoTenta(t *testing.T) {
	g := novaGuarda(filepath.Join(t.TempDir(), "nao", "existe", "login.json"), 5, time.Minute, "flx", "s")
	if e := g.pode(time.Now()); e == nil || !e.Definitivo {
		t.Fatalf("sem gravar: %v", e)
	}
}
