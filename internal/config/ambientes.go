package config

import (
	"fmt"
	"strings"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// Ambientes (SPEC-AMBIENTES-v1 §3): o teto da máquina. O ambiente de cada ordem (o do login do
// usuário no GQ) escolhe a coluna do GQ; a máquina só diz quais ambientes aceita e com que limites.
// v1.35: quem opera no REAL, e em que conta, é a aprovação do admin pela tela
// (SPEC-TECNICA-REAL-PELA-TELA-F1), não uma lista do env.
type Ambientes struct {
	Permitidos map[string]bool // SIMULADOR, REAL
	// Padrao: o ambiente do pedido sem o campo (só na transição; vazio = AMBIENTE_AUSENTE)
	Padrao string
	// Legado: ORDENS_AMBIENTE (HML ou PRODUCAO), a equivalência da transição (§3.1); os conectores
	// configurados são os do ambiente dela (HML: o PnT também no SIMULADOR, como antes)
	Legado string
}

// Os valores de ORDENS_AMBIENTE (legado).
const (
	LegadoHML      = "HML"
	LegadoProducao = "PRODUCAO"
)

// chavesLimiteReal (v1.36, SPEC-TECNICA-v1.36-CONTA-LIVRE §2, D7): o REAL não tem limites de
// negócio; no env, estas chaves são ignoradas com um aviso (não impedem a subida: a volta para a
// v1.35, que as exige com o REAL permitido, é só a troca do binário).
var chavesLimiteReal = []string{"REAL_LIMITE_QTD_ORDEM", "REAL_LIMITE_POSICAO_MAX", "REAL_LIMITE_PERDA_DIA_PTS",
	"REAL_LIMITE_ENTRADAS_DIA", "REAL_LIMITE_ENTRADAS_MIN"}

// chavesExtintas (v1.35, SPEC-REAL-PELA-TELA-F1 D4): com valor, o serviço não sobe (uma só fonte de
// verdade para quem opera no REAL: a aprovação pela tela).
var chavesExtintas = []string{"REAL_UIDS", "REAL_CONTAS"}

// LerAmbientes lê o teto da máquina e confere a coerência; erro = o serviço não sobe. avisos: o que
// fica ignorado (as chaves REAL_* sem o REAL permitido).
func (c Config) LerAmbientes() (a Ambientes, avisos []string, err error) {
	a.Permitidos = map[string]bool{}
	var extintas []string
	for _, k := range chavesExtintas {
		if strings.TrimSpace(c.Texto(k, "")) != "" {
			extintas = append(extintas, k)
		}
	}
	if len(extintas) > 0 {
		return a, nil, fmt.Errorf("%s nao existem mais (v1.35, SPEC-REAL-PELA-TELA-F1 D4): a aprovacao do REAL e pela tela; tire do service.env: %s",
			strings.Join(chavesExtintas, "/"), strings.Join(extintas, ", "))
	}
	legado := strings.ToUpper(strings.TrimSpace(c.Texto("ORDENS_AMBIENTE", "")))
	permitidos := strings.TrimSpace(c.Texto("AMBIENTES_PERMITIDOS", ""))
	if legado != "" && permitidos != "" {
		return a, nil, fmt.Errorf("ORDENS_AMBIENTE e AMBIENTES_PERMITIDOS juntas: duas fontes para a mesma coisa (SPEC-AMBIENTES-v1 §3.1); use só AMBIENTES_PERMITIDOS")
	}
	if legado != "" {
		// a equivalência da transição: HML = só o SIMULADOR, com o padrão SIMULADOR; PRODUCAO = só o
		// REAL, com o padrão REAL (v1.35: com a aprovação, como o REAL; v1.36: sem limites de negócio)
		switch legado {
		case LegadoHML:
			a.Permitidos[dominio.AmbienteSimulador] = true
			a.Padrao = dominio.AmbienteSimulador
		case LegadoProducao:
			a.Permitidos[dominio.AmbienteReal] = true
			a.Padrao = dominio.AmbienteReal
		default:
			return a, nil, fmt.Errorf("ORDENS_AMBIENTE=%q: HML ou PRODUCAO", c.Texto("ORDENS_AMBIENTE", ""))
		}
		a.Legado = legado
		if c.Texto("AMBIENTE_PADRAO", "") != "" {
			return a, nil, fmt.Errorf("AMBIENTE_PADRAO com ORDENS_AMBIENTE: o padrão do legado é o ambiente dele")
		}
		return a, avisosLimiteReal(c), nil
	}

	if permitidos == "" {
		permitidos = "SIM"
	}
	for _, x := range strings.FieldsFunc(permitidos, separador) {
		amb, err := dominio.LerAmbiente(x)
		if err != nil {
			return a, nil, fmt.Errorf("AMBIENTES_PERMITIDOS=%q: %v", permitidos, err)
		}
		a.Permitidos[amb] = true
	}
	if p := c.Texto("AMBIENTE_PADRAO", ""); p != "" {
		amb, err := dominio.LerAmbiente(p)
		if err != nil {
			return a, nil, fmt.Errorf("AMBIENTE_PADRAO=%q: %v", p, err)
		}
		if amb == dominio.AmbienteReal {
			return a, nil, fmt.Errorf("AMBIENTE_PADRAO=REAL: o pedido sem ambiente nunca vira REAL (só SIMULADOR, na transição)")
		}
		if !a.Permitidos[amb] {
			return a, nil, fmt.Errorf("AMBIENTE_PADRAO=%s fora de AMBIENTES_PERMITIDOS", amb)
		}
		a.Padrao = amb
	}
	if !a.Permitidos[dominio.AmbienteReal] {
		return a, avisosLimiteReal(c), nil
	}
	// v1.26: os conectores do REAL são os das chaves REAL_ (Visao); sem nenhum, o REAL não teria por
	// onde mandar ordem
	if c.Texto("REAL_FLEXSCAN_URL", "") == "" && c.Texto("REAL_FLEXSCAN_CORRETORAS", "") == "" && (c.Texto("REAL_FIX_HOST", "") == "" || c.Bool("REAL_PNT_DESLIGADO", false)) {
		return a, nil, fmt.Errorf("REAL em AMBIENTES_PERMITIDOS sem conector REAL: REAL_FLEXSCAN_URL, REAL_FLEXSCAN_CORRETORAS ou REAL_FIX_HOST (SPEC-AMBIENTES-v1 §2.1)")
	}
	return a, avisosLimiteReal(c), nil
}

// avisosLimiteReal (v1.36 §2): os REAL_LIMITE_* preenchidos ficam ignorados, com ou sem o REAL.
func avisosLimiteReal(c Config) []string {
	var r []string
	for _, k := range chavesLimiteReal {
		if c.Texto(k, "") != "" {
			r = append(r, k+" ignorada: sem limites de negocio no REAL (v1.36, D7)")
		}
	}
	return r
}

func separador(r rune) bool { return r == ',' || r == ' ' || r == ';' }

// PnTNoSimulador: o PnT só opera no REAL (a HML dele é D-1); a exceção é a equivalência
// ORDENS_AMBIENTE=HML da transição, que mantém o comportamento de antes (os roteiros).
func (a Ambientes) PnTNoSimulador() bool { return a.Legado == LegadoHML }

// Lista: os permitidos em ordem (SIMULADOR, REAL), para o /saude e o log.
func (a Ambientes) Lista() []string {
	var r []string
	for _, x := range []string{dominio.AmbienteSimulador, dominio.AmbienteReal} {
		if a.Permitidos[x] {
			r = append(r, x)
		}
	}
	return r
}
