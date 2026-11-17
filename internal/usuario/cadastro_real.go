package usuario

import (
	"fmt"
	"strings"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// v1.36 (SPEC-TECNICA-v1.36-CONTA-LIVRE §3.1): no REAL, conta, corretora e conector vêm do cadastro
// do GQ a cada entrada, sem comparação com nada aprovado. O cadastro é inutilizável quando o sistema
// não o atende. Uma regra só (CadastroReal) para a entrada, o /destino, a ficha e o verificador: a
// recusa e o alerta nunca discordam.

// As causas do cadastro inutilizável (o campo `causa` da recusa REAL_CADASTRO_INUTILIZAVEL).
const (
	CausaIlegivel    = "CADASTRO_ILEGIVEL"
	CausaSemCadastro = "SEM_CADASTRO"
	CausaConector    = "CONECTOR_NAO_ATENDIDO"
	CausaCorretora   = "CORRETORA_NAO_ATENDIDA"
	CausaConta       = "CONTA_INVALIDA"
)

// RegrasReal: o que a máquina atende no REAL.
type RegrasReal struct {
	PnT          bool             // a sessão FIX do REAL (REAL_FIX_HOST sem REAL_PNT_DESLIGADO)
	Flexscan     bool             // alguma Flexscan do REAL (a ponte única ou por corretora)
	Corretoras   map[int]string   // REAL_FLEXSCAN_CORRETORAS (CORSeqId → NOME); nil = a ponte única
	Contas       map[int][]string // as contas de cada guarda-chuva (REAL_FLEXSCAN_<NOME>_CONTAS)
	ContasHML    []string         // contas de simulador do PnT: nunca no REAL
	ContasHMLFlx []string         // contas de simulador da Flexscan: nunca no REAL
}

// PorCorretora: a Flexscan do REAL é por corretora (um guarda-chuva por corretora).
func (rg RegrasReal) PorCorretora() bool { return len(rg.Corretoras) > 0 }

// inutilizavel: a falha com a causa no Codigo e o motivo em português claro (ASCII no fio).
func inutilizavel(causa, formato string, a ...any) *Falha {
	return &Falha{Codigo: causa, Motivo: fmt.Sprintf(formato, a...)}
}

// CadastroReal: o cadastro do GQ (usutxtconfigprd) resolvido para uma entrada REAL. Falha.Codigo é a
// causa (CausaIlegivel, CausaConector, CausaCorretora, CausaConta); quem chama recusa com
// REAL_CADASTRO_INUTILIZAVEL.
func CadastroReal(texto string, rg RegrasReal) (Resolvido, *Falha) {
	cad, f := LerCadastro(texto)
	if f != nil {
		if strings.HasPrefix(f.Motivo, "sem conta") {
			return Resolvido{}, inutilizavel(CausaConta, "conta vazia (%s)", f.Motivo)
		}
		return Resolvido{}, inutilizavel(CausaIlegivel, "cadastro ilegivel (%s)", f.Motivo)
	}
	// o conector é obrigatório (P1): o GX grava o Robo.Conector desde 23/10
	switch cad.Conector {
	case "":
		return Resolvido{}, inutilizavel(CausaConector, "sem Robo.Conector")
	case dominio.ConectorPnT:
		if !rg.PnT {
			return Resolvido{}, inutilizavel(CausaConector, "conector PNT nao atendido nesta maquina")
		}
		if cad.Corretora != DestinoBTG {
			return Resolvido{}, inutilizavel(CausaCorretora, "corretora %d nao atendida pelo PnT (so BTG, 85)", cad.Corretora)
		}
	case dominio.ConectorFlexscan:
		if !rg.Flexscan {
			return Resolvido{}, inutilizavel(CausaConector, "conector FLEXSCAN nao atendido nesta maquina")
		}
	default:
		return Resolvido{}, inutilizavel(CausaConector, "conector %s desconhecido", cad.Conector)
	}
	regras := Regras{Ambiente: dominio.AmbienteReal, ContasHML: rg.ContasHML, ContasHMLFlx: rg.ContasHMLFlx, Flexscan: rg.Flexscan, Corretoras: rg.Corretoras}
	r, f := ResolverCom(regras, texto, []string{cad.Conector})
	if f != nil {
		switch f.Codigo {
		case "DESTINO_INVALIDO":
			return Resolvido{}, inutilizavel(CausaCorretora, "corretora %d nao atendida", cad.Corretora)
		case "CONTA_INVALIDA", "CONTA_AMBIENTE":
			return Resolvido{}, inutilizavel(CausaConta, "conta %s invalida no REAL (%s)", cad.Conta, f.Motivo)
		case "CONECTOR_NAO_HABILITADO", "CONECTOR_INDEFINIDO", "CONECTOR_NAO_CONFIGURADO", "CONECTOR_AMBIENTE":
			return Resolvido{}, inutilizavel(CausaConector, "%s", f.Motivo)
		}
		return Resolvido{}, inutilizavel(CausaIlegivel, "%s", f.Motivo)
	}
	// a Flexscan por corretora: o guarda-chuva da corretora e a conta na lista dele (P3)
	if r.Conector == dominio.ConectorFlexscan && rg.PorCorretora() {
		contas, ok := rg.Contas[r.Destino]
		if !ok {
			return Resolvido{}, inutilizavel(CausaCorretora, "corretora %d sem credencial Flexscan nesta maquina", r.Destino)
		}
		if !contem(contas, r.Conta) {
			return Resolvido{}, inutilizavel(CausaConta, "conta %s fora do guarda-chuva Flexscan da %s", r.Conta, NomeCorretora(r.Destino, rg.Corretoras))
		}
	}
	return r, nil
}
