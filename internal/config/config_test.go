package config

import (
	"testing"
	"time"
)

// v1.10 L: os padrões dos limites — globais (soma de todos os usuários) para rajadas de 190, por
// uid para o laço de reentrada, e o repreço espaçado depois de SAIDA_ESGOTOU.
func TestPadroesLimites(t *testing.T) {
	p := Novo(nil).Parametros()
	globais := map[string][2]int{
		"LIMITE_POSICAO_MAX": {p.PosicaoMax, 200}, "LIMITE_ENTRADAS_MIN": {p.EntradasPorMinuto, 400},
		"LIMITE_MENSAGENS_MIN": {p.MensagensPorMinuto, 3000}, "LIMITE_ENTRADAS_DIA": {p.EntradasPorDia, 3000},
		"LIMITE_RECUSAS_PARADA": {p.RecusasParaParada, 50}, "LIMITE_ENTRADAS_MIN_UID": {p.EntradasPorMinutoUID, 3},
	}
	for k, v := range globais {
		if v[0] != v[1] {
			t.Errorf("%s = %d, esperava %d", k, v[0], v[1])
		}
	}
	durs := map[string][2]time.Duration{
		"LIMITE_INTERVALO_ENTRADAS_S": {p.IntervaloEntradas, 0}, "LIMITE_INTERVALO_UID_S": {p.IntervaloUID, 30 * time.Second},
		"LIMITE_VIRADA_S": {p.JanelaVirada, 10 * time.Second}, "REPRECO_ESGOTADO_S": {p.ReprecoEsgotado, 10 * time.Second},
	}
	for k, v := range durs {
		if v[0] != v[1] {
			t.Errorf("%s = %s, esperava %s", k, v[0], v[1])
		}
	}
	if q := Novo(map[string]string{"LIMITE_ENTRADAS_MIN_UID": "0", "LIMITE_INTERVALO_UID_S": "0"}).Parametros(); q.EntradasPorMinutoUID != 0 || q.IntervaloUID != 0 {
		t.Errorf("0 desliga os limites por uid: %d %s", q.EntradasPorMinutoUID, q.IntervaloUID)
	}
}

func TestIntervalo(t *testing.T) {
	i, f := Novo(nil).Intervalo("FONTE_JANELA", "09:00-18:25")
	if i != 9*time.Hour || f != 18*time.Hour+25*time.Minute {
		t.Fatalf("padrão: %s %s", i, f)
	}
	i, f = Novo(map[string]string{"FONTE_JANELA": "08:55 - 17:00"}).Intervalo("FONTE_JANELA", "09:00-18:25")
	if i != 8*time.Hour+55*time.Minute || f != 17*time.Hour {
		t.Fatalf("configurado: %s %s", i, f)
	}
	for _, ruim := range []string{"09:00", "18:00-09:00", "x-y"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%q devia recusar", ruim)
				}
			}()
			Novo(map[string]string{"FONTE_JANELA": ruim}).Intervalo("FONTE_JANELA", "09:00-18:25")
		}()
	}
}
