package repo

import (
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// v1.19: a 0013 sobre o banco da .53 (até a 0012): a origem do stop na operação e o msgtype com mais
// de 4 caracteres (FLX-POST, FLX-SEMRESPOSTA...). Aplicada de novo, não falha.
func TestMigracao0013StopOrigemEMsgtype(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r := NovoMariaDBTeste(t)
	for _, q := range []string{"ALTER TABLE kalmanordemoperacao DROP COLUMN stop_origem, DROP COLUMN stop_ajustado_origem",
		"ALTER TABLE kalmanordemevento MODIFY msgtype VARCHAR(4) NOT NULL", "DELETE FROM kalmanordem_migracao WHERE versao=13"} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	aplicadas, err := r.MigrarRelatando()
	if err != nil || len(aplicadas) != 1 || aplicadas[0] != "0013_stop_origem.sql" {
		t.Fatalf("aplicadas %v, %v", aplicadas, err)
	}
	op := &dominio.Operacao{Data: "2026-10-27", OperacaoID: 234335, UID: 5725, Conta: "S-1117150", Destino: 85, Conector: dominio.ConectorFlexscan,
		Simbolo: "WINZ26", Lado: dominio.Compra, QtdPedida: 1, StopPts: 500, Estado: dominio.Protegida, CriadaEm: time.Now(),
		StopAjustado: 178420, StopOrigem: "TRSP", StopAjustadoOrigem: "STOP_MANUAL"}
	if err := r.SalvarOperacao(op); err != nil {
		t.Fatal(err)
	}
	op.StopOrigem = "STOP_MANUAL"
	if err := r.SalvarOperacao(op); err != nil {
		t.Fatal(err)
	}
	ops, _, err := r.CarregarDia("2026-10-27")
	if err != nil || len(ops) != 1 || ops[0].StopOrigem != "STOP_MANUAL" || ops[0].StopAjustadoOrigem != "STOP_MANUAL" {
		t.Fatalf("operação: %+v %v", ops, err)
	}
	// o lote (o caminho do motor) grava e atualiza as duas colunas
	l := &Lote{Operacoes: []dominio.Operacao{ops[0]}}
	l.Operacoes[0].StopOrigem = "TRSP"
	l.Eventos = []EventoFIX{{Direcao: "REC", MsgType: "FLX-SEMRESPOSTA", Texto: "POST", Em: time.Now()}}
	if err := r.GravarLote(l); err != nil {
		t.Fatal(err)
	}
	if ops, _, _ = r.CarregarDia("2026-10-27"); ops[0].StopOrigem != "TRSP" {
		t.Fatalf("stop_origem pelo lote: %q", ops[0].StopOrigem)
	}
	var mt string
	if err := r.db.QueryRow("SELECT msgtype FROM kalmanordemevento ORDER BY id DESC LIMIT 1").Scan(&mt); err != nil || mt != "FLX-SEMRESPOSTA" {
		t.Fatalf("msgtype %q %v", mt, err)
	}
	// a 0013 de novo: não falha
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=13"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarRelatando(); err != nil {
		t.Fatalf("0013 de novo: %v", err)
	}
}
