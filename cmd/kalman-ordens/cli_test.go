package main

import (
	"bytes"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// Sem comando: ajuda e erro, sem subir o serviço nem abrir conexão — mesmo com banco e FIX no
// ambiente (como no systemd). O banco e a sessão apontam para um listener que conta conexões.
func TestSemComandoSoAjuda(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conexoes := make(chan struct{}, 10)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conexoes <- struct{}{}
			c.Close()
		}
	}()
	end := ln.Addr().String()
	host, porta, _ := net.SplitHostPort(end)
	t.Setenv("DB_DSN", "kalman:kalman@tcp("+end+")/kalmanordens")
	t.Setenv("FIX_HOST", host)
	t.Setenv("FIX_PORTA", porta)
	t.Setenv("FIX_SENDER_COMP_ID", "GAMMAQ2")
	t.Setenv("ORDENS_AMBIENTE", "HML")
	t.Setenv("ORDENS_CONTAS_HML", "99999")

	for _, args := range [][]string{nil, {"servr"}} {
		var saida, erros bytes.Buffer
		inicio := time.Now()
		if code := executar(args, &saida, &erros); code != 2 {
			t.Fatalf("%v: código %d, esperado 2", args, code)
		}
		if time.Since(inicio) > time.Second {
			t.Fatalf("%v: demorou %s (subiu alguma coisa?)", args, time.Since(inicio))
		}
		if !strings.Contains(erros.String(), "uso: kalman-ordens") || !strings.Contains(erros.String(), "servir") {
			t.Fatalf("%v: sem ajuda: %q", args, erros.String())
		}
		if saida.Len() != 0 {
			t.Fatalf("%v: saída inesperada: %q", args, saida.String())
		}
	}
	select {
	case <-conexoes:
		t.Fatal("o binário sem comando abriu conexão")
	case <-time.After(300 * time.Millisecond):
	}
	var saida bytes.Buffer
	if code := executar([]string{"ajuda"}, &saida, &bytes.Buffer{}); code != 0 || !strings.Contains(saida.String(), "migrar") {
		t.Fatalf("ajuda: %d %q", code, saida.String())
	}
}

// Instalação: com o schema vazio, a CLI de usuário recusa com mensagem clara (sem criar nada);
// migrar aplica tudo; migrar de novo não faz nada; depois a CLI funciona.
func TestMigrarEUsuarioComSchemaVazio(t *testing.T) {
	dsn := os.Getenv("KALMAN_TESTE_MARIADB")
	if dsn == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r, err := repo.AbrirMariaDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Fechar()
	if err := r.LimparTeste(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Novo(map[string]string{"DB_DSN": dsn, "ORDENS_AMBIENTE": "HML", "ORDENS_CONTAS_HML": "99999", "ACUSUARIO_TABELA": "acusuario_teste"})

	var saida bytes.Buffer
	err = cliUsuario(cfg, []string{"conferir", "-uid", "1450"}, &saida)
	if err == nil || !strings.Contains(err.Error(), "rode kalman-ordens migrar") || !strings.Contains(err.Error(), "0002_") {
		t.Fatalf("usuario conferir com schema vazio: %v", err)
	}
	err = cliUsuario(cfg, []string{"habilitar", "-uid", "1450", "-motivo", "x"}, &saida)
	if err == nil || !strings.Contains(err.Error(), "rode kalman-ordens migrar") {
		t.Fatalf("usuario habilitar com schema vazio: %v", err)
	}
	if pend, _ := r.MigracoesPendentes(); len(pend) < 2 {
		t.Fatalf("a CLI de usuário não pode migrar: pendentes %d", len(pend))
	}

	saida.Reset()
	if err := cliMigrar(cfg, nil, &saida); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saida.String(), "aplicada: 0001_inicial.sql") || !strings.Contains(saida.String(), "aplicada: 0002_") {
		t.Fatalf("primeiro migrar: %q", saida.String())
	}
	saida.Reset()
	if err := cliMigrar(cfg, nil, &saida); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saida.String(), "aplicada") || !strings.Contains(saida.String(), "nenhuma migração pendente") {
		t.Fatalf("segundo migrar devia não fazer nada: %q", saida.String())
	}
	if pend, err := r.MigracoesPendentes(); err != nil || len(pend) != 0 {
		t.Fatalf("pendentes depois de migrar: %d %v", len(pend), err)
	}

	if err := r.PrepararUsuariosTeste(); err != nil {
		t.Fatal(err)
	}
	saida.Reset()
	if err := cliUsuario(cfg, []string{"conferir", "-uid", "1450"}, &saida); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saida.String(), "uid sem linha") {
		t.Fatalf("conferir depois de migrar: %q", saida.String())
	}
}
