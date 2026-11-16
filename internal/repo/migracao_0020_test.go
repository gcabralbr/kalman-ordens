package repo

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// v1.36: a 0020 sobre o banco da .53 (até a 0019): a linha REAL em CONTA_DIVERGENTE volta a APROVADO
// (habilitado=1, sem o GQ visto), com uma linha MIGRACAO no histórico; APROVADO, SEM_APROVACAO e o
// SIMULADOR ficam intocados (o histórico: a linha da gravação e a da 0020); aplicada de novo, não muda nada nem duplica o histórico.
func TestMigracao0020ContaLivre(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r := NovoMariaDBTeste(t)
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=20"); err != nil {
		t.Fatal(err)
	}
	em := time.Date(2026, 11, 16, 15, 46, 0, 0, time.UTC)
	div := dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal, PosicaoMax: 1, QtdMaxOrdem: 1, Conectores: []string{"PNT"},
		Por: "servico", Motivo: "cadastro do GQ diferente do aprovado", Em: em, AprovacaoReal: dominio.AprovacaoReal{Estado: dominio.EstadoContaDivergente,
			Corretora: 85, Conta: "4582351", Versao: 6, AprovadoPor: "admin:1450", AprovadoEm: em, AprovadoMotivo: "fase 1",
			GQConector: "PNT", GQCorretora: 85, GQConta: "1234567", DivergenteDesde: em, Origem: dominio.OrigemServico}}
	apr := div
	apr.UID, apr.Habilitado, apr.Estado, apr.GQConector, apr.GQCorretora, apr.GQConta, apr.DivergenteDesde = 35, true, dominio.EstadoAprovado, "", 0, "", time.Time{}
	sem := dominio.Habilitacao{UID: 5725, Ambiente: dominio.AmbienteReal, Por: "migracao-0019", Motivo: "D5", Em: em,
		AprovacaoReal: dominio.AprovacaoReal{Estado: dominio.EstadoSemAprovacao, Versao: 1, Origem: dominio.OrigemMigracao}}
	sim := dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteSimulador, Habilitado: true, Conectores: []string{"FLEXSCAN"}, Por: "george", Motivo: "x", Em: em}
	for _, h := range []dominio.Habilitacao{div, apr, sem, sim} {
		if err := r.GravarHabilitacao(h); err != nil {
			t.Fatal(err)
		}
	}
	aplicadas, err := r.MigrarRelatando()
	if err != nil || len(aplicadas) != 1 || aplicadas[0] != "0020_conta_livre.sql" {
		t.Fatalf("aplicadas %v, %v", aplicadas, err)
	}
	conferir := func(quando string) {
		t.Helper()
		h, ok, err := r.Habilitacao(1450, dominio.AmbienteReal)
		if err != nil || !ok || !h.Habilitado || h.Estado != dominio.EstadoAprovado || h.Versao != 7 || h.Origem != dominio.OrigemMigracao ||
			h.Por != "migracao-0020" || h.GQConta != "" || !h.DivergenteDesde.IsZero() || h.AprovadoMotivo != "fase 1" {
			t.Fatalf("%s: 1450: %+v %v %v", quando, h, ok, err)
		}
		hist, err := r.HistoricoHabilitacao(1450, dominio.AmbienteReal, 10)
		if err != nil || len(hist) != 2 || hist[0].Evento != dominio.EventoMigracao || hist[0].EstadoAntes != dominio.EstadoContaDivergente ||
			hist[0].Estado != dominio.EstadoAprovado || !hist[0].Habilitado || hist[0].Versao != 7 {
			t.Fatalf("%s: histórico 1450: %+v %v", quando, hist, err)
		}
		for _, x := range []dominio.Habilitacao{apr, sem, sim} {
			h, ok, err := r.Habilitacao(x.UID, x.Ambiente)
			if err != nil || !ok || h.Estado != x.Estado || h.Versao != x.Versao || h.Por != x.Por {
				t.Fatalf("%s: %d %s mexida: %+v", quando, x.UID, x.Ambiente, h)
			}
		}
	}
	conferir("0020")
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=20"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarRelatando(); err != nil {
		t.Fatalf("0020 de novo: %v", err)
	}
	conferir("0020 de novo")
}
