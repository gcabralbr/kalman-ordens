package config

import (
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// SPEC-AMBIENTES-v1 §3.1: o teto da máquina. Padrão só o SIMULADOR; a equivalência ORDENS_AMBIENTE;
// as duas juntas, o padrão REAL e o REAL sem limites ou conector REAL não sobem. v1.35: REAL_UIDS e
// REAL_CONTAS não existem mais — com valor, com ou sem o REAL permitido, o serviço não sobe.
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
		a.Real.QtdOrdem != 2 || a.Permitidos[dominio.AmbienteSimulador] {
		t.Fatalf("legado PRODUCAO: %+v %v", a, err)
	}
	if a, _, err = ler(map[string]string{"AMBIENTES_PERMITIDOS": "SIM", "AMBIENTE_PADRAO": "simulador"}); err != nil || a.Padrao != dominio.AmbienteSimulador || a.PnTNoSimulador() {
		t.Fatalf("padrão da transição: %+v %v", a, err)
	}
	_, avisos, _ = ler(map[string]string{"REAL_LIMITE_QTD_ORDEM": "1"})
	if len(avisos) != 1 || !strings.Contains(avisos[0], "REAL_LIMITE_QTD_ORDEM ignorada") {
		t.Fatalf("aviso: %v", avisos)
	}
	reais := map[string]string{"AMBIENTES_PERMITIDOS": "SIM,REAL",
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
		// v1.35 (SPEC-REAL-PELA-TELA-F1 D4): as chaves extintas, com e sem o REAL permitido, e no legado
		{func() map[string]string { m := sem(""); m["REAL_FIX_HOST"] = "h"; m["REAL_UIDS"] = "1450"; return m }(), "tire do service.env: REAL_UIDS"},
		{func() map[string]string { m := sem(""); m["REAL_FIX_HOST"] = "h"; m["REAL_CONTAS"] = "*"; return m }(), "tire do service.env: REAL_CONTAS"},
		{map[string]string{"REAL_UIDS": "1450", "REAL_CONTAS": "4582351"}, "tire do service.env: REAL_UIDS, REAL_CONTAS"},
		{map[string]string{"ORDENS_AMBIENTE": "PRODUCAO", "REAL_UIDS": "*"}, "a aprovacao do REAL e pela tela"},
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
	// só espaço não é valor (a linha vazia de um env editado)
	if _, _, err := ler(map[string]string{"REAL_UIDS": "  "}); err != nil {
		t.Fatalf("REAL_UIDS em branco: %v", err)
	}
}

// v1.35 (SPEC-TECNICA-REAL-PELA-TELA-F1 §5.3): os REAL_LIMITE_* lidos também com o REAL fora de
// AMBIENTES_PERMITIDOS (a aprovação confere os limites do uid com os da instância); os padrões de
// sempre (POSICAO_MAX = QTD_ORDEM; ENTRADAS_MIN = LIMITE_ENTRADAS_MIN); ausente = zero.
func TestLimitesReal(t *testing.T) {
	l := Novo(map[string]string{"REAL_LIMITE_QTD_ORDEM": "1", "REAL_LIMITE_PERDA_DIA_PTS": "600", "REAL_LIMITE_ENTRADAS_DIA": "30",
		"REAL_LIMITE_ENTRADAS_MIN": "2"}).LimitesReal()
	if l.QtdOrdem != 1 || l.PosicaoMax != 1 || l.PerdaDiaPts != 600 || l.EntradasDia != 30 || l.EntradasMin != 2 {
		t.Fatalf("limites: %+v", l)
	}
	if l := Novo(map[string]string{"LIMITE_ENTRADAS_MIN": "7"}).LimitesReal(); l.QtdOrdem != 0 || l.PosicaoMax != 0 || l.EntradasMin != 7 {
		t.Fatalf("ausentes: %+v", l)
	}
}

// v1.26 (SPEC-AMBIENTES-v1 §2.1): a visão de cada instância. O REAL lê as chaves do conector só das
// REAL_ (credencial de um ambiente nunca vale no outro) e os limites REAL; o SIMULADOR nunca tem a
// sessão FIX; a equivalência ORDENS_AMBIENTE é a configuração inteira.
func TestVisao(t *testing.T) {
	base := map[string]string{"AMBIENTES_PERMITIDOS": "SIM,REAL",
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

// v1.31.1: o robô e o OMS do PnT (20858/20859) são da sessão do ambiente: no REAL, só das REAL_; a
// sem prefixo não vaza para o REAL, e a REAL_ não aparece no SIMULADOR.
func TestVisaoRoboPnT(t *testing.T) {
	base := map[string]string{"AMBIENTES_PERMITIDOS": "SIM,REAL",
		"REAL_LIMITE_QTD_ORDEM": "1", "REAL_LIMITE_PERDA_DIA_PTS": "500", "REAL_LIMITE_ENTRADAS_DIA": "3",
		"REAL_FIX_HOST": "prd", "REAL_FIX_PORTA": "1", "REAL_FIX_SENDER_COMP_ID": "GAMMAQ2",
		"REAL_PNT_ROBOT_ID": "BTG_ALGO", "REAL_PNT_ENTERING_FIRM": "BTGDAYTRADE"}
	c := Novo(base)
	a, _, err := c.LerAmbientes()
	if err != nil {
		t.Fatal(err)
	}
	real, _ := c.Visao(a, dominio.AmbienteReal)
	if p := real.Parametros(); p.RobotID != "BTG_ALGO" || p.EnteringFirm != "BTGDAYTRADE" {
		t.Fatalf("REAL: RobotID %q EnteringFirm %q", p.RobotID, p.EnteringFirm)
	}
	sim, _ := c.Visao(a, dominio.AmbienteSimulador)
	if p := sim.Parametros(); p.RobotID != "" || p.EnteringFirm != "" {
		t.Fatalf("SIMULADOR com o robô do REAL: %q %q", p.RobotID, p.EnteringFirm)
	}
	delete(base, "REAL_PNT_ROBOT_ID")
	delete(base, "REAL_PNT_ENTERING_FIRM")
	base["PNT_ROBOT_ID"], base["PNT_ENTERING_FIRM"] = "OUTRO", "OUTRO"
	c = Novo(base)
	if real, _ = c.Visao(a, dominio.AmbienteReal); real.Parametros().RobotID != "" || real.Parametros().EnteringFirm != "" {
		t.Fatalf("a sem prefixo vazou para o REAL: %v", real.v)
	}
}
