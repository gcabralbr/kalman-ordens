package config

import (
	"strconv"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// SPEC-AMBIENTES-v1 §2.1 (v1.26): cada ambiente é uma instância do motor com os seus conectores. A
// instância lê a configuração por uma visão: no REAL, as chaves do conector vêm das REAL_ (as sem
// prefixo continuam do SIMULADOR); o resto (tempos, ajustes, a fonte de preço) é compartilhado.

// chavesDoAmbiente: no REAL, só das REAL_, nunca das sem prefixo (credencial e sessão de um
// ambiente não vazam para o outro). Os padrões dos caminhos em disco também são outros.
var chavesDoAmbiente = []string{
	"FLEXSCAN_URL", "FLEXSCAN_USUARIO", "FLEXSCAN_SENHA", "FLEXSCAN_WEBHOOK_SEGREDO", "FLEXSCAN_WEBHOOK_PREFIXO",
	"FLEXSCAN_WEBHOOK_URL", "FLEXSCAN_ESTADO_LOGIN",
	"FIX_SENDER_COMP_ID", "FIX_TARGET_COMP_ID", "FIX_HOST", "FIX_PORTA", "FIX_USUARIO", "FIX_SENHA", "FIX_DIR",
	"FIX_INICIO", "FIX_FIM", "FIX_RESET_ON_LOGON",
}

// padroesReal: os caminhos do REAL, separados dos do SIMULADOR quando a chave REAL_ não vem.
var padroesReal = map[string]string{
	"FLEXSCAN_WEBHOOK_PREFIXO": "/flexscan/webhook-real/",
	"FLEXSCAN_ESTADO_LOGIN":    "/var/lib/kalman-ordens/flexscan-login-real.json",
	"FIX_DIR":                  "/var/lib/kalman-ordens/fix-real",
}

// chavesComRecuo: no REAL, a REAL_ se houver; senão a sem prefixo (a mesma bolsa, a mesma fonte).
var chavesComRecuo = []string{
	"ORDENS_BOLSA", "ORDENS_SENDER_LOCATION", "FLEXSCAN_BOLSA",
	"FLEXSCAN_JANELA", "FLEXSCAN_RENOVAR_ABERTURA", "FLEXSCAN_CONFERIR_ABERTURA", // v1.28
	"PRECOS_URL_FLEXSCAN", "PRECOS_SAUDE_URL_FLEXSCAN", "PRECOS_URL_PNT", "PRECOS_SAUDE_URL_PNT",
}

// chavesFIX: a sessão FIX; o PnT só opera no REAL (fora da equivalência HML).
var chavesFIX = []string{"FIX_SENDER_COMP_ID", "FIX_HOST", "FIX_PORTA", "FIX_USUARIO", "FIX_SENHA"}

// Visao: a configuração que a instância do ambiente lê, e os avisos (chaves ignoradas). Na
// equivalência ORDENS_AMBIENTE (um ambiente só, os conectores de sempre) é a própria.
func (c Config) Visao(a Ambientes, amb string) (Config, []string) {
	if a.Legado != "" {
		return c, nil
	}
	v := Novo(c.v)
	var avisos []string
	if amb != dominio.AmbienteReal {
		// o SIMULADOR: o PnT não tem uso (a HML dele é D-1); a sessão FIX nunca sobe nele
		for _, k := range chavesFIX {
			if v.v[k] != "" {
				avisos = append(avisos, k+" ignorada: o PnT só opera no REAL (REAL_"+k+")")
			}
			delete(v.v, k)
		}
		v.v["PNT_DESLIGADO"] = "1"
		return v, avisos
	}
	for _, k := range chavesDoAmbiente {
		delete(v.v, k)
		if x := c.Texto("REAL_"+k, padroesReal[k]); x != "" {
			v.v[k] = x
		}
	}
	for _, k := range chavesComRecuo {
		if x := c.Texto("REAL_"+k, ""); x != "" {
			v.v[k] = x
		}
	}
	// o PnT do REAL existe com REAL_FIX_HOST; REAL_PNT_DESLIGADO o desliga
	desl := c.Texto("REAL_FIX_HOST", "") == "" || c.Bool("REAL_PNT_DESLIGADO", false)
	v.v["PNT_DESLIGADO"] = map[bool]string{true: "1", false: "0"}[desl]
	// os limites do REAL (§3.1) no lugar dos LIMITE_ do SIMULADOR
	for k, n := range map[string]int{"LIMITE_QTD_ORDEM": a.Real.QtdOrdem, "LIMITE_ENTRADAS_DIA": a.Real.EntradasDia,
		"LIMITE_POSICAO_MAX": a.Real.PosicaoMax, "LIMITE_ENTRADAS_MIN": a.Real.EntradasMin, "LIMITE_PERDA_DIA_PTS": int(a.Real.PerdaDiaPts)} {
		v.v[k] = strconv.Itoa(n)
	}
	// o FLEXSCAN_CONTAS_HML e o ORDENS_CONTAS_HML continuam: são a trava do §4.1 item 8, nos dois sentidos
	return v, nil
}
