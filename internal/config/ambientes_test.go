package config

import (
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// SPEC-AMBIENTES-v1 §3.1: o teto da máquina. Padrão só o SIMULADOR; a equivalência ORDENS_AMBIENTE;
// as duas juntas, o padrão REAL e o REAL sem listas, limites ou conector REAL não sobem.
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
		// v1.26: completo, mas sem conector REAL (o PnT desligado não conta)
		{reais, "sem conector REAL"},
		{func() map[string]string {
			m := sem("")
			m["REAL_FIX_HOST"] = "h"
			m["REAL_PNT_DESLIGADO"] = "1"
			return m
		}(), "sem conector REAL"},
	}
	for _, c := range erros {
		if _, _, err := ler(c.v); err == nil || !strings.Contains(err.Error(), c.quer) {
			t.Errorf("%v: esperava erro com %q, veio %v", c.v, c.quer, err)
		}
	}
	for _, k := range []string{"REAL_FLEXSCAN_URL", "REAL_FIX_HOST"} {
		m := sem("")
		m[k] = "x"
		if a, _, err := ler(m); err != nil || !a.Permitidos[dominio.AmbienteReal] || a.Real.PosicaoMax != 1 || a.Real.EntradasMin != 400 {
			t.Fatalf("REAL com %s: %+v %v", k, a, err)
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

// v1.26 (SPEC-AMBIENTES-v1 §2.1): a visão de cada instância. O REAL lê as chaves do conector só das
// REAL_ (credencial de um ambiente nunca vale no outro) e os limites REAL; o SIMULADOR nunca tem a
// sessão FIX; a equivalência ORDENS_AMBIENTE é a configuração inteira.
func TestVisao(t *testing.T) {
	base := map[string]string{"AMBIENTES_PERMITIDOS": "SIM,REAL", "REAL_UIDS": "5725", "REAL_CONTAS": "715124",
		"REAL_LIMITE_QTD_ORDEM": "2", "REAL_LIMITE_PERDA_DIA_PTS": "300", "REAL_LIMITE_ENTRADAS_DIA": "10",
		"FLEXSCAN_URL": "https://apisim", "FLEXSCAN_USUARIO": "sim", "FLEXSCAN_SENHA": "s1", "FLEXSCAN_WEBHOOK_SEGREDO": "segsim",
		"REAL_FLEXSCAN_URL": "https://api", "REAL_FLEXSCAN_USUARIO": "real", "REAL_FLEXSCAN_SENHA": "s2",
		"FIX_HOST": "hml", "FIX_PORTA": "1", "LIMITE_QTD_ORDEM": "5", "PRECOS_URL_FLEXSCAN": "http://p", "FLEXSCAN_CONTAS_HML": "S-1"}
	c := Novo(base)
	a, _, err := c.LerAmbientes()
	if err != nil {
		t.Fatal(err)
	}
	sim, avisos := c.Visao(a, dominio.AmbienteSimulador)
	if sim.Texto("FIX_HOST", "") != "" || !sim.Bool("PNT_DESLIGADO", false) || sim.Texto("FLEXSCAN_URL", "") != "https://apisim" || sim.Inteiro("LIMITE_QTD_ORDEM", 0) != 5 {
		t.Fatalf("SIMULADOR: %v", sim.v)
	}
	if len(avisos) != 2 || !strings.Contains(avisos[0], "REAL_FIX_") {
		t.Fatalf("avisos do SIMULADOR: %v", avisos)
	}
	real, _ := c.Visao(a, dominio.AmbienteReal)
	quer := map[string]string{"FLEXSCAN_URL": "https://api", "FLEXSCAN_USUARIO": "real", "FLEXSCAN_SENHA": "s2", "FLEXSCAN_WEBHOOK_SEGREDO": "",
		"FLEXSCAN_WEBHOOK_PREFIXO": "/flexscan/webhook-real/", "FLEXSCAN_ESTADO_LOGIN": "/var/lib/kalman-ordens/flexscan-login-real.json",
		"FIX_HOST": "", "FIX_DIR": "/var/lib/kalman-ordens/fix-real", "PNT_DESLIGADO": "1", "LIMITE_QTD_ORDEM": "2", "LIMITE_POSICAO_MAX": "2",
		"LIMITE_PERDA_DIA_PTS": "300", "PRECOS_URL_FLEXSCAN": "http://p", "FLEXSCAN_CONTAS_HML": "S-1"}
	for k, v := range quer {
		if real.Texto(k, "") != v {
			t.Errorf("REAL %s = %q, quer %q", k, real.Texto(k, ""), v)
		}
	}
	if p := real.Parametros(); p.QtdMaxOrdem != 2 || p.PerdaDiaPts != 300 || p.EntradasPorDia != 10 || !p.PnTDesligado || !p.Flexscan {
		t.Fatalf("parâmetros do REAL: %+v", p)
	}
	base["REAL_FIX_HOST"] = "prd"
	c = Novo(base)
	if real, _ = c.Visao(a, dominio.AmbienteReal); real.Bool("PNT_DESLIGADO", true) || real.Texto("FIX_HOST", "") != "prd" {
		t.Fatalf("REAL com o PnT: %v", real.v)
	}
	leg := Novo(map[string]string{"ORDENS_AMBIENTE": "HML", "FIX_HOST": "hml"})
	al, _, _ := leg.LerAmbientes()
	if v, avisos := leg.Visao(al, dominio.AmbienteSimulador); v.Texto("FIX_HOST", "") != "hml" || avisos != nil {
		t.Fatalf("equivalência HML: %v %v", v.v, avisos)
	}
}
