package main

import (
	"bytes"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
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
	if !strings.Contains(saida.String(), "aplicada: 0001_inicial.sql") || !strings.Contains(saida.String(), "aplicada: 0002_") || !strings.Contains(saida.String(), "aplicada: 0003_execid_por_ordem.sql") || !strings.Contains(saida.String(), "aplicada: 0004_modulo.sql") || !strings.Contains(saida.String(), "aplicada: 0005_stop_ajustado.sql") || !strings.Contains(saida.String(), "aplicada: 0006_historico.sql") || !strings.Contains(saida.String(), "aplicada: 0007_pedidos.sql") {
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

// historico (v1.5 R): o mesmo conteúdo das rotas da tela de admin, direto do banco; recusa com o
// schema atrás do binário.
func TestHistoricoCLI(t *testing.T) {
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
	cfg := config.Novo(map[string]string{"DB_DSN": dsn})
	var saida bytes.Buffer
	if err := cliHistorico(cfg, []string{"sessao", "-dia", "2026-10-15"}, &saida); err == nil || !strings.Contains(err.Error(), "rode kalman-ordens migrar") {
		t.Fatalf("historico com schema vazio: %v", err)
	}
	if err := r.Migrar(); err != nil {
		t.Fatal(err)
	}
	em := time.Date(2026, 10, 15, 13, 0, 3, 0, time.UTC) // 10:00:03 em Brasília
	op := &dominio.Operacao{Data: "2026-10-15", OperacaoID: 900000061, UID: 1450, Lado: dominio.Venda, QtdPedida: 1, StopPts: 500,
		Estado: dominio.Protegida, CriadaEm: em, ContadorN: map[string]int{}}
	if err := r.SalvarOperacao(op); err != nil {
		t.Fatal(err)
	}
	o := &dominio.Ordem{Data: "2026-10-15", OperacaoID: op.OperacaoID, UID: 1450, Papel: dominio.Entrada, ClOrdBase: "KM-E-900000061-1",
		ClOrdAtual: "KM-E-900000061-1", Lado: dominio.Venda, Qtd: 1, Estado: dominio.Ativa, CriadaEm: em}
	if err := r.SalvarOrdem(o); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegistrarEvento(repo.EventoFIX{OrdemID: o.ID, Direcao: "ENV", MsgType: "D", ClOrdID: o.ClOrdAtual, Bruta: "8=FIX.4.4\x0135=D\x0111=KM-E-900000061-1\x01", Em: em}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegistrarSessao(repo.EventoSessao{Tipo: "LOGON", Texto: "logon aceito", Em: em.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	casos := []struct {
		args  []string
		quer  []string
		nunca string
	}{
		{[]string{"operacao", "-id", "900000061"}, []string{`"operacao_id": 900000061`, `"msgtype": "D"`, `"em": "2026-10-15T10:00:03.000-03:00"`, `"eventos": []`}, "fix_raw"},
		{[]string{"operacao", "-id", "900000061", "-cru"}, []string{`"fix_raw": "8=FIX.4.4|35=D|11=KM-E-900000061-1|"`}, ""},
		{[]string{"dia", "-dia", "2026-10-15", "-uid", "1450"}, []string{`"operacao_id": 900000061`, `"lado": "V"`, `"zerada_em": null`, `"alertas_n": 0`}, ""},
		{[]string{"dia", "-dia", "2026-10-15", "-uid", "1451"}, []string{"[]"}, "operacao_id"},
		{[]string{"sessao", "-dia", "2026-10-15"}, []string{`"tipo": "LOGON"`, `"em": "2026-10-15T09:00:03.000-03:00"`}, ""},
	}
	for _, c := range casos {
		saida.Reset()
		if err := cliHistorico(cfg, c.args, &saida); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		for _, q := range c.quer {
			if !strings.Contains(saida.String(), q) {
				t.Errorf("%v: falta %s em %s", c.args, q, saida.String())
			}
		}
		if c.nunca != "" && strings.Contains(saida.String(), c.nunca) {
			t.Errorf("%v: não devia ter %s: %s", c.args, c.nunca, saida.String())
		}
	}
	if err := cliHistorico(cfg, []string{"operacao", "-id", "1"}, &saida); err == nil {
		t.Fatal("historico de operação inexistente devia falhar")
	}
	if err := cliHistorico(cfg, []string{"dia", "-dia", "15/10/2026"}, &saida); err == nil {
		t.Fatal("dia inválido devia falhar")
	}
}

// expurgar (v1.6 E1): apaga só o que passou da retenção nas quatro tabelas, em lotes; o resumo
// (operação, ordem) fica; recusa prazo abaixo do mínimo e sem EXPURGO_DB_DSN.
func TestExpurgarCLI(t *testing.T) {
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
	agora := time.Date(2026, 10, 15, 21, 0, 0, 0, time.UTC) // 18:00 em Brasília
	// corte com 12 meses: 2025-10-15 00:00 em Brasília
	velho, novo := time.Date(2025, 10, 15, 2, 59, 0, 0, time.UTC), time.Date(2025, 10, 15, 3, 0, 0, 0, time.UTC)
	op := &dominio.Operacao{Data: "2025-10-14", OperacaoID: 900000077, UID: 7, Lado: dominio.Compra, QtdPedida: 1, StopPts: 500,
		Estado: dominio.Zerada, CriadaEm: velho, ContadorN: map[string]int{}}
	if err := r.SalvarOperacao(op); err != nil {
		t.Fatal(err)
	}
	o := &dominio.Ordem{Data: "2025-10-14", OperacaoID: op.OperacaoID, Papel: dominio.Entrada, ClOrdBase: "KM-E-900000077-1",
		ClOrdAtual: "KM-E-900000077-1", Lado: dominio.Compra, Qtd: 1, Estado: dominio.Executada, CriadaEm: velho}
	if err := r.SalvarOrdem(o); err != nil {
		t.Fatal(err)
	}
	for i, em := range []time.Time{velho, velho, velho, novo} {
		if _, err := r.RegistrarEvento(repo.EventoFIX{OrdemID: o.ID, Direcao: "ENV", MsgType: "D", Em: em}); err != nil {
			t.Fatal(i, err)
		}
		if _, err := r.Publicar("ALERTA", "", 0, []byte(`{}`), em); err != nil {
			t.Fatal(err)
		}
		if err := r.RegistrarSessao(repo.EventoSessao{Tipo: "LOGON", Texto: "x", Em: em}); err != nil {
			t.Fatal(err)
		}
		if err := r.RegistrarPedido(&repo.PedidoHTTP{Data: "2025-10-14", Rota: "ORDENS", HTTPStatus: 200, Corpo: "{}", Resposta: "{}", Em: em}); err != nil {
			t.Fatal(err)
		}
	}

	var saida bytes.Buffer
	if err := cliExpurgar(config.Novo(map[string]string{}), []string{"-env", "/nao/existe"}, &saida, agora); err == nil {
		t.Fatal("sem EXPURGO_DB_DSN devia falhar")
	}
	cfg := config.Novo(map[string]string{"EXPURGO_DB_DSN": dsn, "EXPURGO_MESES": "12"})
	if err := cliExpurgar(cfg, []string{"-meses", "2"}, &saida, agora); err == nil || !strings.Contains(err.Error(), "mínimo") {
		t.Fatalf("2 meses devia ser recusado: %v", err)
	}
	if err := cliExpurgar(config.Novo(map[string]string{"EXPURGO_DB_DSN": dsn, "EXPURGO_MESES": "1"}), nil, &saida, agora); err == nil {
		t.Fatal("EXPURGO_MESES=1 devia ser recusado")
	}
	saida.Reset()
	if err := cliExpurgar(cfg, []string{"-simular"}, &saida, agora); err != nil {
		t.Fatal(err)
	}
	for _, tab := range repo.TabelasExpurgo {
		if !strings.Contains(saida.String(), tab+": 3 linha(s) a apagar (simulação), anteriores a 2025-10-15 (12 meses)") {
			t.Fatalf("simular: %q", saida.String())
		}
	}
	saida.Reset()
	if err := cliExpurgar(cfg, nil, &saida, agora); err != nil {
		t.Fatal(err)
	}
	for _, tab := range repo.TabelasExpurgo {
		if !strings.Contains(saida.String(), tab+": 3 linha(s) apagadas") {
			t.Fatalf("expurgar: %q", saida.String())
		}
	}
	saida.Reset()
	if err := cliExpurgar(cfg, []string{"-simular"}, &saida, agora); err != nil || strings.Count(saida.String(), ": 0 linha(s)") != 4 {
		t.Fatalf("depois do expurgo: %q %v", saida.String(), err)
	}
	// o que está dentro do prazo e o resumo ficam
	if evs, _ := r.EventosFIXDasOrdens([]int64{o.ID}); len(evs) != 1 || !evs[0].Em.Equal(novo) {
		t.Fatalf("eventos depois do expurgo: %+v", evs)
	}
	if ss, _ := r.SessaoEntre(velho.Add(-time.Hour), agora); len(ss) != 1 {
		t.Fatalf("sessão depois do expurgo: %d", len(ss))
	}
	if ps, _ := r.PedidosDoDia("2025-10-14", 0, ""); len(ps) != 1 {
		t.Fatalf("pedidos depois do expurgo: %d", len(ps))
	}
	if ps, _ := r.PublicadosEntre(velho.Add(-time.Hour), agora, ""); len(ps) != 1 {
		t.Fatalf("publicados depois do expurgo: %d", len(ps))
	}
	if d, ok, _ := r.DiaDaOperacao("", op.OperacaoID); !ok || d != "2025-10-14" {
		t.Fatal("o resumo da operação não pode ser expurgado")
	}
}
