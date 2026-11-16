package repo

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// v1.35: a 0019 sobre o banco da .53 (até a 0018): toda linha REAL (habilitada ou não) vira
// SEM_APROVACAO com habilitado=0 e uma linha MIGRACAO no histórico (o estado de antes:
// HABILITADO_V134 ou DESABILITADO_V134); o SIMULADOR fica intocado; aplicada de novo, não muda nada
// nem duplica o histórico.
func TestMigracao0019AprovacaoReal(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r := NovoMariaDBTeste(t)
	for _, q := range []string{
		`ALTER TABLE kalmanordemusuario DROP COLUMN estado, DROP COLUMN corretora, DROP COLUMN conta, DROP COLUMN versao,
			DROP COLUMN aprovado_por, DROP COLUMN aprovado_em, DROP COLUMN aprovado_motivo, DROP COLUMN gq_conector,
			DROP COLUMN gq_corretora, DROP COLUMN gq_conta, DROP COLUMN divergente_desde, DROP COLUMN origem`,
		`ALTER TABLE kalmanordemusuariohist DROP INDEX ix_kouh_uid_amb, DROP COLUMN estado, DROP COLUMN corretora, DROP COLUMN conta,
			DROP COLUMN versao, DROP COLUMN aprovado_por, DROP COLUMN aprovado_em, DROP COLUMN aprovado_motivo, DROP COLUMN gq_conector,
			DROP COLUMN gq_corretora, DROP COLUMN gq_conta, DROP COLUMN divergente_desde, DROP COLUMN origem, DROP COLUMN evento,
			DROP COLUMN estado_antes`,
		"DELETE FROM kalmanordem_migracao WHERE versao=19",
	} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	em := time.Date(2026, 11, 13, 14, 42, 0, 0, time.UTC)
	// o banco da .53: o 1450 desabilitado no REAL desde 13/11, o 5725 habilitado no REAL (Flexscan),
	// os dois habilitados no SIMULADOR
	for _, l := range []struct {
		uid      int
		amb      string
		hab      int
		cons     string
		pos, qtd int
	}{{1450, "REAL", 0, "PNT", 1, 1}, {5725, "REAL", 1, "FLEXSCAN", 1, 1}, {1450, "SIMULADOR", 1, "FLEXSCAN", 0, 0}, {5725, "SIMULADOR", 1, "FLEXSCAN", 0, 0}} {
		if _, err := r.db.Exec(`INSERT INTO kalmanordemusuario (uid, ambiente, habilitado, posicao_max, qtd_max_ordem, conectores, alterado_por, motivo, alterado_em)
			VALUES (?,?,?,?,?,?,'george','antes da 0019',?)`, l.uid, l.amb, l.hab, l.pos, l.qtd, l.cons, em); err != nil {
			t.Fatal(err)
		}
	}
	aplicadas, err := r.MigrarRelatando()
	if err != nil || len(aplicadas) != 1 || aplicadas[0] != "0019_aprovacao_real.sql" {
		t.Fatalf("aplicadas %v, %v", aplicadas, err)
	}
	conferir := func(quando string) {
		t.Helper()
		for _, uid := range []int{1450, 5725} {
			h, ok, err := r.Habilitacao(uid, dominio.AmbienteReal)
			if err != nil || !ok || h.Habilitado || h.Estado != dominio.EstadoSemAprovacao || h.Origem != dominio.OrigemMigracao ||
				h.Versao != 1 || h.Por != "migracao-0019" || h.PosicaoMax != 1 || h.Conta != "" {
				t.Fatalf("%s: REAL %d: %+v %v %v", quando, uid, h, ok, err)
			}
			s, ok, err := r.Habilitacao(uid, dominio.AmbienteSimulador)
			if err != nil || !ok || !s.Habilitado || s.Estado != "" || s.Versao != 0 || s.Por != "george" {
				t.Fatalf("%s: SIMULADOR %d: %+v %v %v", quando, uid, s, ok, err)
			}
			hist, err := r.HistoricoHabilitacao(uid, dominio.AmbienteReal, 10)
			antes := map[int]string{1450: "DESABILITADO_V134", 5725: "HABILITADO_V134"}[uid]
			if err != nil || len(hist) != 1 || hist[0].Evento != dominio.EventoMigracao || hist[0].EstadoAntes != antes ||
				hist[0].Estado != dominio.EstadoSemAprovacao || hist[0].Habilitado {
				t.Fatalf("%s: histórico REAL %d: %+v %v", quando, uid, hist, err)
			}
		}
	}
	conferir("0019")
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=19"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarRelatando(); err != nil {
		t.Fatalf("0019 de novo: %v", err)
	}
	conferir("0019 de novo")
}
