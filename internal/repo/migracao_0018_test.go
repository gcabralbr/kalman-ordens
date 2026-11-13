package repo

import (
	"testing"
	"time"
)

// v1.34: a 0018 sobre o banco da .53 (até a 0017): a linha antiga de kalmanordemsessao fica com o
// ambiente, o conector e o canal NULL (lidos vazios); a nova grava os três, pelo RegistrarSessao e
// pelo lote; aplicada de novo, não falha.
func TestMigracao0018SessaoConector(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r := NovoMariaDBTeste(t)
	for _, q := range []string{"ALTER TABLE kalmanordemsessao DROP INDEX ix_kses_em_amb, DROP COLUMN ambiente, DROP COLUMN conector, DROP COLUMN canal",
		"DELETE FROM kalmanordem_migracao WHERE versao=18"} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	em := time.Date(2026, 11, 13, 14, 0, 0, 0, time.UTC)
	if _, err := r.db.Exec("INSERT INTO kalmanordemsessao (tipo, texto, em) VALUES ('LOGON', 'antiga', ?)", em); err != nil {
		t.Fatal(err)
	}
	aplicadas, err := r.MigrarRelatando()
	if err != nil || len(aplicadas) != 1 || aplicadas[0] != "0018_sessao_conector.sql" {
		t.Fatalf("aplicadas %v, %v", aplicadas, err)
	}
	if err := r.RegistrarSessao(EventoSessao{Tipo: "ALERTA", Texto: "nova", Em: em.Add(time.Second), Ambiente: "REAL", Conector: "PNT"}); err != nil {
		t.Fatal(err)
	}
	if err := r.GravarLote(&Lote{Sessao: []EventoSessao{{Tipo: "ALERTA", Texto: "lote", Em: em.Add(2 * time.Second), Ambiente: "SIMULADOR", Conector: "FLEXSCAN", Canal: "BTG"}}}); err != nil {
		t.Fatal(err)
	}
	ss, err := r.SessaoEntre(em.Add(-time.Hour), em.Add(time.Hour))
	if err != nil || len(ss) != 3 || ss[0].Ambiente != "" || ss[0].Conector != "" || ss[1].Ambiente != "REAL" || ss[1].Conector != "PNT" ||
		ss[2].Ambiente != "SIMULADOR" || ss[2].Conector != "FLEXSCAN" || ss[2].Canal != "BTG" {
		t.Fatalf("SessaoEntre: %+v %v", ss, err)
	}
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=18"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarRelatando(); err != nil {
		t.Fatalf("0018 de novo: %v", err)
	}
}
