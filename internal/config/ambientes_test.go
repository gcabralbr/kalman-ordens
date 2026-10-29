package config

import (
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// SPEC-AMBIENTES-v1 §3.1: o teto da máquina. Padrão só o SIMULADOR; a equivalência ORDENS_AMBIENTE;
// as duas juntas, o padrão REAL e o REAL sem listas e limites não sobem; na v1.25, o REAL permitido
// ainda não sobe (sem conectores REAL).
func TestLerAmbientes(t *testing.T) {
	ler := func(v map[string]string) (Ambientes, []string, error) { return Novo(v).LerAmbientes() }

	a, avisos, err := ler(map[string]string{})
	if err != nil || !a.Permitidos[dominio.AmbienteSimulador] || a.Permitidos[dominio.AmbienteReal] || a.Padrao != "" || len(avisos) != 0 {
		t.Fatalf("padrão: %+v %v %v", a, avisos, err)
	}
	if a, _, err = ler(map[string]string{"ORDENS_AMBIENTE": "HML"}); err != nil || a.Padrao != dominio.AmbienteSimulador || !a.PnTNoSimulador() || a.Legado != LegadoHML {
		t.Fatalf("legado HML: %+v %v", a, err)
	}
	if a, _, err = ler(map[string]string{"ORDENS_AMBIENTE": "PRODUCAO", "LIMITE_QTD_ORDEM": "2"}); err != nil || a.Padrao != dominio.AmbienteReal ||
		!a.UIDReal(1) || !a.ContaReal("x") || a.Real.QtdOrdem != 2 || a.Permitidos[dominio.AmbienteSimulador] {
		t.Fatalf("legado PRODUCAO: %+v %v", a, err)
	}
	if a, _, err = ler(map[string]string{"AMBIENTES_PERMITIDOS": "SIM", "AMBIENTE_PADRAO": "simulador"}); err != nil || a.Padrao != dominio.AmbienteSimulador || a.PnTNoSimulador() {
		t.Fatalf("padrão da transição: %+v %v", a, err)
	}
	_, avisos, _ = ler(map[string]string{"REAL_UIDS": "5725"})
	if len(avisos) != 1 || !strings.Contains(avisos[0], "REAL_UIDS ignorada") {
		t.Fatalf("aviso: %v", avisos)
	}
	reais := map[string]string{"AMBIENTES_PERMITIDOS": "SIM,REAL", "REAL_UIDS": "5725", "REAL_CONTAS": "715124",
		"REAL_LIMITE_QTD_ORDEM": "1", "REAL_LIMITE_PERDA_DIA_PTS": "300", "REAL_LIMITE_ENTRADAS_DIA": "10"}
	sem := func(k string) map[string]string {
		m := map[string]string{}
		for kk, v := range reais {
			if kk != k {
				m[kk] = v
			}
		}
		return m
	}
	erros := []struct {
		v    map[string]string
		quer string
	}{
		{map[string]string{"ORDENS_AMBIENTE": "HML", "AMBIENTES_PERMITIDOS": "SIM"}, "duas fontes"},
		{map[string]string{"ORDENS_AMBIENTE": "PROD"}, "HML ou PRODUCAO"},
		{map[string]string{"AMBIENTES_PERMITIDOS": "SIM,XPTO"}, "AMBIENTES_PERMITIDOS"},
		{map[string]string{"AMBIENTE_PADRAO": "REAL", "AMBIENTES_PERMITIDOS": "SIM,REAL"}, "nunca vira REAL"},
		{map[string]string{"AMBIENTE_PADRAO": "SIM", "AMBIENTES_PERMITIDOS": "REAL"}, "fora de AMBIENTES_PERMITIDOS"},
		{map[string]string{"ORDENS_AMBIENTE": "HML", "AMBIENTE_PADRAO": "SIM"}, "AMBIENTE_PADRAO com ORDENS_AMBIENTE"},
		{sem("REAL_LIMITE_QTD_ORDEM"), "REAL_LIMITE_QTD_ORDEM"},
		{sem("REAL_LIMITE_PERDA_DIA_PTS"), "REAL_LIMITE_PERDA_DIA_PTS"},
		{sem("REAL_LIMITE_ENTRADAS_DIA"), "REAL_LIMITE_ENTRADAS_DIA"},
		{sem("REAL_UIDS"), "REAL_UIDS"},
		{sem("REAL_CONTAS"), "REAL_CONTAS"},
		{func() map[string]string { m := sem(""); m["REAL_UIDS"] = "abc"; return m }(), "não é um uid"},
		{func() map[string]string { m := sem(""); m["REAL_LIMITE_QTD_ORDEM"] = "0"; return m }(), "maiores que zero"},
		// v1.25: completo, mas sem conectores REAL
		{reais, "nenhum conector REAL"},
	}
	for _, c := range erros {
		if _, _, err := ler(c.v); err == nil || !strings.Contains(err.Error(), c.quer) {
			t.Errorf("%v: esperava erro com %q, veio %v", c.v, c.quer, err)
		}
	}
	// as listas REAL: "*" e os uids
	a = Ambientes{RealUIDs: []int{5725}, RealContas: []string{"715124"}}
	if !a.UIDReal(5725) || a.UIDReal(1) || !a.ContaReal("715124") || a.ContaReal("1") {
		t.Fatalf("listas: %+v", a)
	}
	if u, todos, err := lerUIDs("*"); err != nil || !todos || u != nil {
		t.Fatalf("*: %v %v %v", u, todos, err)
	}
}
