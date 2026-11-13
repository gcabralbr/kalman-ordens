package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/config"
)

// v1.34: o fio de um dia do log de arquivo do QuickFIX: só o dia de Brasília (o log é UTC), a senha
// mascarada, a direção pelo 49, sem heartbeat se pedido, o filtro de ClOrdID e os eventos juntos.
func TestCliFio(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "log"), 0o700); err != nil {
		t.Fatal(err)
	}
	soh := func(s string) string { return strings.ReplaceAll(s, "|", "\x01") }
	msgs := strings.Join([]string{
		"2026/11/13 02:00:00.000000 " + soh("8=FIX.4.4|35=0|49=GAMMAQ2|56=PNT_DSA_BTG2|"), // 12/11 23:00 em Brasília
		"2026/11/13 12:31:42.100000 " + soh("8=FIX.4.4|35=A|49=GAMMAQ2|56=PNT_DSA_BTG2|553=GEORGEG|554=segredo|"),
		"2026/11/13 12:31:42.300000 " + soh("8=FIX.4.4|35=A|49=PNT_DSA_BTG2|56=GAMMAQ2|"),
		"2026/11/13 12:32:12.000000 " + soh("8=FIX.4.4|35=0|49=GAMMAQ2|56=PNT_DSA_BTG2|"),
		"2026/11/13 14:40:14.827000 " + soh("8=FIX.4.4|35=8|49=PNT_DSA_BTG2|56=GAMMAQ2|11=KM-P-234754-2|39=B|"),
		"linha sem hora",
		"2026/11/14 03:00:00.000000 " + soh("8=FIX.4.4|35=0|49=GAMMAQ2|56=PNT_DSA_BTG2|"), // 14/11 00:00 em Brasília
	}, "\n") + "\n"
	pre := filepath.Join(dir, "log", "FIX.4.4-GAMMAQ2-PNT_DSA_BTG2")
	if err := os.WriteFile(pre+".messages.current.log", []byte(msgs), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pre+".event.current.log", []byte("2026/11/13 12:31:42.200000 Sending logon request\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rodar := func(args ...string) []string {
		var b bytes.Buffer
		if err := cliFio(config.Novo(nil), append([]string{"-dia", "2026-11-13", "-dir", dir}, args...), &b); err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(b.String()), "\n")
	}
	l := rodar()
	if len(l) != 4 || !strings.HasPrefix(l[0], "2026-11-13 09:31:42.100000 ENV 8=FIX.4.4|35=A|") || !strings.Contains(l[0], "|554=***|") ||
		strings.Contains(strings.Join(l, "\n"), "segredo") || !strings.Contains(l[1], " REC 8=FIX.4.4|35=A|49=PNT_DSA_BTG2|") ||
		!strings.Contains(l[2], " ENV ") || !strings.HasPrefix(l[3], "2026-11-13 11:40:14.827000 REC ") {
		t.Fatalf("fio:\n%s", strings.Join(l, "\n"))
	}
	if l := rodar("-sem-heartbeat"); len(l) != 3 {
		t.Fatalf("sem heartbeat:\n%s", strings.Join(l, "\n"))
	}
	if l := rodar("-clordid", "KM-P-234754"); len(l) != 1 || !strings.Contains(l[0], "39=B") {
		t.Fatalf("clordid:\n%s", strings.Join(l, "\n"))
	}
	if l := rodar("-eventos", "-sem-heartbeat"); len(l) != 4 || l[1] != "2026-11-13 09:31:42.200000 EVT Sending logon request" {
		t.Fatalf("eventos:\n%s", strings.Join(l, "\n"))
	}
	var b bytes.Buffer
	if err := cliFio(config.Novo(nil), []string{"-dir", t.TempDir()}, &b); err == nil || !strings.Contains(err.Error(), "nenhum log") {
		t.Fatalf("sem log: %v", err)
	}
}
