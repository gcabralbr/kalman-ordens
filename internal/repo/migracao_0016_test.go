package repo

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// v1.28 §3: a 0016 sobre o banco da .53 (até a 0015): a suspensão da operação gravada pelo
// SalvarOperacao e pelo lote, relida na partida, e limpa (NULL); aplicada de novo, não falha.
func TestMigracao0016Suspensa(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r := NovoMariaDBTeste(t)
	for _, q := range []string{"ALTER TABLE kalmanordemoperacao DROP COLUMN suspensa_em, DROP COLUMN suspensa_motivo",
		"DELETE FROM kalmanordem_migracao WHERE versao=16"} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	aplicadas, err := r.MigrarRelatando()
	if err != nil || len(aplicadas) != 1 || aplicadas[0] != "0016_operacao_suspensa.sql" {
		t.Fatalf("aplicadas %v, %v", aplicadas, err)
	}
	em := time.Date(2026, 10, 30, 10, 30, 33, 0, time.UTC)
	op := &dominio.Operacao{Data: "2026-10-30", OperacaoID: 12101, UID: 7, Conta: "12345", Destino: 85, Conector: dominio.ConectorPnT,
		Simbolo: "WINZ26", Lado: dominio.Compra, QtdPedida: 1, StopPts: 500, Estado: dominio.Protegida, CriadaEm: time.Now(),
		SuspensaEm: em, SuspensaMotivo: "PnT fora do ar desde 10:30:03: a sessão FIX caiu"}
	if err := r.SalvarOperacao(op); err != nil {
		t.Fatal(err)
	}
	ops, _, err := r.CarregarDia("2026-10-30")
	if err != nil || len(ops) != 1 || !ops[0].SuspensaEm.Equal(em) || ops[0].SuspensaMotivo != op.SuspensaMotivo {
		t.Fatalf("operação: %+v %v", ops, err)
	}
	// o lote (o caminho do motor) limpa a suspensão e grava o estado novo
	l := &Lote{Operacoes: []dominio.Operacao{ops[0]}}
	l.Operacoes[0].SuspensaEm, l.Operacoes[0].SuspensaMotivo, l.Operacoes[0].Estado = time.Time{}, "", dominio.EncerradaFora
	if err := r.GravarLote(l); err != nil {
		t.Fatal(err)
	}
	if ops, _, _ = r.CarregarDia("2026-10-30"); !ops[0].SuspensaEm.IsZero() || ops[0].SuspensaMotivo != "" || ops[0].Estado != dominio.EncerradaFora {
		t.Fatalf("pelo lote: %+v", ops[0])
	}
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=16"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarRelatando(); err != nil {
		t.Fatalf("0016 de novo: %v", err)
	}
}
