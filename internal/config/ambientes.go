package config

import (
	"fmt"
	"strconv"
	"strings"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// Ambientes (SPEC-AMBIENTES-v1 §3): o teto da máquina. O ambiente de cada ordem (o do login do
// usuário no GQ) escolhe a coluna do GQ; a máquina só diz quais ambientes aceita, para quem o REAL
// vale e com que limites.
type Ambientes struct {
	Permitidos map[string]bool // SIMULADOR, REAL
	// Padrao: o ambiente do pedido sem o campo (só na transição; vazio = AMBIENTE_AUSENTE)
	Padrao string
	// Legado: ORDENS_AMBIENTE (HML ou PRODUCAO), a equivalência da transição (§3.1); os conectores
	// configurados são os do ambiente dela (HML: o PnT também no SIMULADOR, como antes)
	Legado string
	// REAL_UIDS e REAL_CONTAS: lista ou "*" (Todos); vazio = ninguém
	RealUIDs        []int
	RealUIDsTodos   bool
	RealContas      []string
	RealContasTodas bool
	// limites próprios do REAL (§3.1); no legado PRODUCAO, os globais
	Real LimitesReal
}

// LimitesReal: os limites do REAL, separados dos do SIMULADOR (LIMITE_*).
type LimitesReal struct {
	QtdOrdem, EntradasDia, PosicaoMax, EntradasMin int
	PerdaDiaPts                                    int64
}

// Os valores de ORDENS_AMBIENTE (legado).
const (
	LegadoHML      = "HML"
	LegadoProducao = "PRODUCAO"
)

// chaves REAL obrigatórias quando o REAL é permitido (§3.1): o REAL nunca sobe por um padrão
var chavesReal = []string{"REAL_LIMITE_QTD_ORDEM", "REAL_LIMITE_PERDA_DIA_PTS", "REAL_LIMITE_ENTRADAS_DIA"}

// LerAmbientes lê o teto da máquina e confere a coerência; erro = o serviço não sobe. avisos: o que
// fica ignorado (as chaves REAL_* sem o REAL permitido).
func (c Config) LerAmbientes() (a Ambientes, avisos []string, err error) {
	a.Permitidos = map[string]bool{}
	legado := strings.ToUpper(strings.TrimSpace(c.Texto("ORDENS_AMBIENTE", "")))
	permitidos := strings.TrimSpace(c.Texto("AMBIENTES_PERMITIDOS", ""))
	if legado != "" && permitidos != "" {
		return a, nil, fmt.Errorf("ORDENS_AMBIENTE e AMBIENTES_PERMITIDOS juntas: duas fontes para a mesma coisa (SPEC-AMBIENTES-v1 §3.1); use só AMBIENTES_PERMITIDOS")
	}
	if legado != "" {
		// a equivalência da transição: HML = só o SIMULADOR, com o padrão SIMULADOR; PRODUCAO = só o
		// REAL, com o padrão REAL, os limites globais e o REAL para todos (a trava cruzada de antes)
		switch legado {
		case LegadoHML:
			a.Permitidos[dominio.AmbienteSimulador] = true
			a.Padrao = dominio.AmbienteSimulador
		case LegadoProducao:
			a.Permitidos[dominio.AmbienteReal] = true
			a.Padrao = dominio.AmbienteReal
			a.RealUIDsTodos, a.RealContasTodas = true, true
			a.Real = LimitesReal{QtdOrdem: c.Inteiro("LIMITE_QTD_ORDEM", 1), EntradasDia: c.Inteiro("LIMITE_ENTRADAS_DIA", 3000),
				PosicaoMax: c.Inteiro("LIMITE_POSICAO_MAX", 200), EntradasMin: c.Inteiro("LIMITE_ENTRADAS_MIN", 400),
				PerdaDiaPts: c.Pontos("LIMITE_PERDA_DIA_PTS", 1500)}
		default:
			return a, nil, fmt.Errorf("ORDENS_AMBIENTE=%q: HML ou PRODUCAO", c.Texto("ORDENS_AMBIENTE", ""))
		}
		a.Legado = legado
		if c.Texto("AMBIENTE_PADRAO", "") != "" {
			return a, nil, fmt.Errorf("AMBIENTE_PADRAO com ORDENS_AMBIENTE: o padrão do legado é o ambiente dele")
		}
		return a, avisosReal(c, a), nil
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
		return a, avisosReal(c, a), nil
	}

	// o REAL permitido: listas e limites obrigatórios, nunca por um padrão
	var faltam []string
	for _, k := range append(append([]string(nil), chavesReal...), "REAL_UIDS", "REAL_CONTAS") {
		if strings.TrimSpace(c.Texto(k, "")) == "" {
			faltam = append(faltam, k)
		}
	}
	if len(faltam) > 0 {
		return a, nil, fmt.Errorf("REAL em AMBIENTES_PERMITIDOS sem %s (SPEC-AMBIENTES-v1 §3.1)", strings.Join(faltam, ", "))
	}
	if a.RealUIDs, a.RealUIDsTodos, err = lerUIDs(c.Texto("REAL_UIDS", "")); err != nil {
		return a, nil, err
	}
	a.RealContas, a.RealContasTodas = lerTodosOuLista(c.Texto("REAL_CONTAS", ""))
	a.Real = LimitesReal{QtdOrdem: c.Inteiro("REAL_LIMITE_QTD_ORDEM", 0), EntradasDia: c.Inteiro("REAL_LIMITE_ENTRADAS_DIA", 0),
		PerdaDiaPts: c.Pontos("REAL_LIMITE_PERDA_DIA_PTS", 0)}
	a.Real.PosicaoMax = c.Inteiro("REAL_LIMITE_POSICAO_MAX", a.Real.QtdOrdem)
	a.Real.EntradasMin = c.Inteiro("REAL_LIMITE_ENTRADAS_MIN", c.Inteiro("LIMITE_ENTRADAS_MIN", 400))
	if a.Real.QtdOrdem <= 0 || a.Real.EntradasDia <= 0 || a.Real.PerdaDiaPts <= 0 || a.Real.PosicaoMax <= 0 || a.Real.EntradasMin <= 0 {
		return a, nil, fmt.Errorf("limites REAL: todos maiores que zero (%+v)", a.Real)
	}
	// v1.26: os conectores do REAL são os das chaves REAL_ (Visao); sem nenhum, o REAL não teria por
	// onde mandar ordem
	if c.Texto("REAL_FLEXSCAN_URL", "") == "" && (c.Texto("REAL_FIX_HOST", "") == "" || c.Bool("REAL_PNT_DESLIGADO", false)) {
		return a, nil, fmt.Errorf("REAL em AMBIENTES_PERMITIDOS sem conector REAL: REAL_FLEXSCAN_URL ou REAL_FIX_HOST (SPEC-AMBIENTES-v1 §2.1)")
	}
	return a, nil, nil
}

// avisosReal: as chaves REAL_* preenchidas numa máquina sem o REAL permitido ficam ignoradas.
func avisosReal(c Config, a Ambientes) []string {
	if a.Permitidos[dominio.AmbienteReal] {
		return nil
	}
	var r []string
	for _, k := range append(append([]string(nil), chavesReal...), "REAL_UIDS", "REAL_CONTAS", "REAL_LIMITE_POSICAO_MAX", "REAL_LIMITE_ENTRADAS_MIN") {
		if c.Texto(k, "") != "" {
			r = append(r, k+" ignorada: REAL fora de AMBIENTES_PERMITIDOS")
		}
	}
	return r
}

func separador(r rune) bool { return r == ',' || r == ' ' || r == ';' }

// lerTodosOuLista: "*" = todos; senão a lista.
func lerTodosOuLista(s string) ([]string, bool) {
	if strings.TrimSpace(s) == "*" {
		return nil, true
	}
	return strings.FieldsFunc(s, separador), false
}

func lerUIDs(s string) ([]int, bool, error) {
	l, todos := lerTodosOuLista(s)
	var r []int
	for _, x := range l {
		n, err := strconv.Atoi(x)
		if err != nil || n <= 0 {
			return nil, false, fmt.Errorf("REAL_UIDS: %q não é um uid", x)
		}
		r = append(r, n)
	}
	return r, todos, nil
}

// UIDReal: o uid pode operar no REAL nesta máquina (REAL_UIDS).
func (a Ambientes) UIDReal(uid int) bool {
	if a.RealUIDsTodos {
		return true
	}
	for _, u := range a.RealUIDs {
		if u == uid {
			return true
		}
	}
	return false
}

// ContaReal: a conta pode operar no REAL nesta máquina (REAL_CONTAS).
func (a Ambientes) ContaReal(conta string) bool {
	if a.RealContasTodas {
		return true
	}
	for _, c := range a.RealContas {
		if c == conta {
			return true
		}
	}
	return false
}

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
