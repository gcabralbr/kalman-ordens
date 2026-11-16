package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// v1.35 (SPEC-TECNICA-REAL-PELA-TELA-F1 §6): a CLI não habilita nem desabilita o REAL (é a aprovação
// pela tela); a revogação de emergência só restringe, entra no histórico com origem CLI e o usuário
// do sistema, e não faz nada sem aprovação; o listar mostra o estado das linhas REAL.
func TestUsuarioRealPelaCLI(t *testing.T) {
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
	if err := r.Migrar(); err != nil {
		t.Fatal(err)
	}
	if err := r.PrepararUsuariosTeste(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Novo(map[string]string{"DB_DSN": dsn, "ACUSUARIO_TABELA": "acusuario_teste"})
	t.Setenv("SUDO_USER", "george")
	var saida bytes.Buffer
	for _, acao := range []string{"habilitar", "desabilitar"} {
		err := cliUsuario(cfg, []string{acao, "-uid", "1450", "-ambiente", "REAL", "-motivo", "x", "-conectores", "pnt"}, &saida)
		if err == nil || !strings.Contains(err.Error(), "use a tela") || !strings.Contains(err.Error(), "usuario revogar -uid 1450") {
			t.Fatalf("%s REAL: %v", acao, err)
		}
	}
	if _, ok, _ := r.Habilitacao(1450, dominio.AmbienteReal); ok {
		t.Fatal("a CLI gravou a linha REAL")
	}

	saida.Reset()
	if err := cliUsuario(cfg, []string{"revogar", "-uid", "1450", "-motivo", "emergencia"}, &saida); err != nil || !strings.Contains(saida.String(), "estado=SEM_APROVACAO: nada a revogar") {
		t.Fatalf("revogar sem aprovação: %q %v", saida.String(), err)
	}
	em := time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)
	if err := r.GravarHabilitacao(dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal, Habilitado: true, QtdMaxOrdem: 1, PosicaoMax: 1,
		Conectores: []string{"PNT"}, Por: "admin:1450", Motivo: "fase 1", Em: em,
		AprovacaoReal: dominio.AprovacaoReal{Estado: dominio.EstadoAprovado, Corretora: 85, Conta: "4582351", Versao: 1, Origem: dominio.OrigemTela}}); err != nil {
		t.Fatal(err)
	}
	saida.Reset()
	if err := cliUsuario(cfg, []string{"revogar", "-uid", "1450", "-motivo", "ok"}, &saida); err == nil || !strings.Contains(err.Error(), "motivo") {
		t.Fatalf("motivo curto: %v", err)
	}
	if err := cliUsuario(cfg, []string{"revogar", "-uid", "1450", "-motivo", "tela fora do ar"}, &saida); err != nil ||
		!strings.Contains(saida.String(), "APROVADO -> REVOGADO versao=2 origem=CLI por=george") {
		t.Fatalf("revogar: %q %v", saida.String(), err)
	}
	h, _, _ := r.Habilitacao(1450, dominio.AmbienteReal)
	if h.Estado != dominio.EstadoRevogado || h.Habilitado || h.Origem != dominio.OrigemCLI || h.Por != "george" || h.Conta != "4582351" {
		t.Fatalf("linha: %+v", h)
	}
	hist, _ := r.HistoricoHabilitacao(1450, dominio.AmbienteReal, 5)
	if len(hist) != 2 || hist[0].Evento != dominio.EventoRevogar || hist[0].EstadoAntes != dominio.EstadoAprovado || hist[0].Origem != dominio.OrigemCLI {
		t.Fatalf("histórico: %+v", hist)
	}
	saida.Reset()
	if err := cliUsuario(cfg, []string{"revogar", "-uid", "1450", "-motivo", "de novo"}, &saida); err != nil || !strings.Contains(saida.String(), "estado=REVOGADO: nada a revogar") {
		t.Fatalf("revogar de novo: %q %v", saida.String(), err)
	}
	saida.Reset()
	if err := cliUsuario(cfg, []string{"listar"}, &saida); err != nil ||
		!strings.Contains(saida.String(), "uid=1450 ambiente=REAL habilitado=false posicao_max=1 qtd_max=1 conectores=PNT estado=REVOGADO corretora=85 conta=4582351 versao=2 origem=CLI por=george") {
		t.Fatalf("listar: %q %v", saida.String(), err)
	}
}
