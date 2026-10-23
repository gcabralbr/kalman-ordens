package repo

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// 23/10: a .53 aplicou a 0011 do binário 77550906a194, sem a coluna kalmanordemoperacao.conector. O
// binário seguinte aplica só a 0012 (a coluna, com IF NOT EXISTS) e grava a operação com o conector.
func TestMigracao0012NoBancoDa53(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r := NovoMariaDBTeste(t)
	// o estado da .53: tudo até a 0011, sem a coluna nem a 0012
	for _, q := range []string{"ALTER TABLE kalmanordemoperacao DROP COLUMN conector", "DELETE FROM kalmanordem_migracao WHERE versao=12"} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	aplicadas, err := r.MigrarRelatando()
	if err != nil || len(aplicadas) != 1 || aplicadas[0] != "0012_conector_operacao.sql" {
		t.Fatalf("aplicadas %v, %v", aplicadas, err)
	}
	op := &dominio.Operacao{Data: "2026-10-23", OperacaoID: 1, UID: 5725, Conta: "S-1117150", Destino: 85, Conector: dominio.ConectorFlexscan,
		Simbolo: "WINZ26", Lado: dominio.Compra, QtdPedida: 1, StopPts: 500, Estado: dominio.Entrando, CriadaEm: time.Now()}
	if err := r.SalvarOperacao(op); err != nil {
		t.Fatal(err)
	}
	ops, _, err := r.CarregarDia("2026-10-23")
	if err != nil || len(ops) != 1 || ops[0].Conector != "FLEXSCAN" || ops[0].Conta != "S-1117150" {
		t.Fatalf("operação: %+v %v", ops, err)
	}
	// a 0012 de novo (banco que já tinha a coluna): não falha
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=12"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarRelatando(); err != nil {
		t.Fatalf("0012 com a coluna já criada: %v", err)
	}
}
