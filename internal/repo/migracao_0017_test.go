package repo

import (
	"testing"
	"time"
)

// v1.30: a 0017 sobre o banco da .53 (até a 0016): o canal (a corretora) no evento da ponte, pelo
// RegistrarEvento e pelo lote; o tráfego do dia lido por canal (vazio = a ponte única, canal NULL);
// aplicada de novo, não falha.
func TestMigracao0017Canal(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r := NovoMariaDBTeste(t)
	for _, q := range []string{"ALTER TABLE kalmanordemevento DROP COLUMN canal", "DELETE FROM kalmanordem_migracao WHERE versao=17"} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	aplicadas, err := r.MigrarRelatando()
	if err != nil || len(aplicadas) != 1 || aplicadas[0] != "0017_canal_evento.sql" {
		t.Fatalf("aplicadas %v, %v", aplicadas, err)
	}
	em := time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC)
	if _, err := r.RegistrarEvento(EventoFIX{Direcao: "ENV", MsgType: "FLX-POST", ClOrdID: "KM-E-1-1", Ambiente: "REAL", Canal: "CMC", Em: em}); err != nil {
		t.Fatal(err)
	}
	l := &Lote{Eventos: []EventoFIX{
		{Direcao: "ENV", MsgType: "FLX-POST", ClOrdID: "KM-E-2-1", Ambiente: "REAL", Canal: "BTG", Em: em},
		{Direcao: "ENV", MsgType: "FLX-POST", ClOrdID: "KM-E-3-1", Ambiente: "REAL", Em: em},
	}}
	if err := r.GravarLote(l); err != nil {
		t.Fatal(err)
	}
	for canal, quer := range map[string]string{"CMC": "KM-E-1-1", "BTG": "KM-E-2-1", "": "KM-E-3-1"} {
		evs, err := r.EventosFlexscanEntre(em.Add(-time.Hour), em.Add(time.Hour), 100, "REAL", canal)
		if err != nil || len(evs) != 1 || evs[0].ClOrdID != quer {
			t.Fatalf("canal %q: %+v %v", canal, evs, err)
		}
	}
	if _, err := r.db.Exec("DELETE FROM kalmanordem_migracao WHERE versao=17"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarRelatando(); err != nil {
		t.Fatalf("0017 de novo: %v", err)
	}
}
