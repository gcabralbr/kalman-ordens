package motor

import (
	"fmt"
	"strconv"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// SPEC-AMBIENTES-v1: o ambiente do login do usuário no GQ (SIMULADOR × REAL) vem em cada ordem e
// escolhe a coluna do GQ; a máquina é o teto (config.Ambientes). v1.26: cada ambiente é uma
// instância do motor com os seus conectores (servico.go); m.amb é o desta.

// ambienteDoPedido (§1.3, §4.1 itens 1 e 2): o campo, ou o AMBIENTE_PADRAO da transição.
func (m *Motor) ambienteDoPedido(bruto string) (string, *recusa) {
	if bruto == "" {
		if m.p.Amb.Padrao == "" {
			return "", simples("AMBIENTE_AUSENTE", "ambiente ausente (SIMULADOR ou REAL)")
		}
		return m.p.Amb.Padrao, nil
	}
	amb, err := dominio.LerAmbiente(bruto)
	if err != nil {
		return "", simples("AMBIENTE_INVALIDO", fmt.Sprintf("ambiente %q invalido (SIMULADOR ou REAL)", bruto))
	}
	return amb, nil
}

// tetoAmbiente (§4.1 itens 3 e 4): o ambiente permitido nesta máquina e, no REAL, o uid em
// REAL_UIDS. O REAL recusado é crítico (alguém tentou o REAL onde ele não vale).
func (m *Motor) tetoAmbiente(amb string, uid int) *recusa {
	if !m.p.Amb.Permitidos[amb] {
		texto := fmt.Sprintf("%s nao permitido nesta maquina (AMBIENTES_PERMITIDOS)", amb)
		if amb == dominio.AmbienteReal {
			m.al.Emitir("AMBIENTE_NAO_PERMITIDO", "AMBIENTE_NAO_PERMITIDO:"+strconv.Itoa(uid), fmt.Sprintf("entrada REAL do uid %d recusada: %s", uid, texto), 0)
		}
		return simples("AMBIENTE_NAO_PERMITIDO", texto)
	}
	if amb != m.amb {
		// o roteador manda cada ambiente à sua instância; aqui só por defeito
		return simples("AMBIENTE_NAO_PERMITIDO", fmt.Sprintf("%s sem instancia nesta maquina", amb))
	}
	if amb == dominio.AmbienteReal && !m.p.Amb.UIDReal(uid) {
		m.al.Emitir("REAL_UID_NAO_PERMITIDO", "REAL_UID_NAO_PERMITIDO:"+strconv.Itoa(uid), fmt.Sprintf("entrada REAL do uid %d recusada: fora de REAL_UIDS", uid), 0)
		return simples("REAL_UID_NAO_PERMITIDO", fmt.Sprintf("uid %d fora de REAL_UIDS nesta maquina", uid))
	}
	return nil
}

// regras: a resolução da coluna do GQ no ambiente da ordem.
func (m *Motor) regras(amb string) usuario.Regras {
	return usuario.Regras{Ambiente: amb, ContasHML: m.p.ContasHML, ContasHMLFlx: m.p.ContasHMLFlx, Flexscan: m.p.Flexscan,
		PnTNoSimulador: m.p.Amb.PnTNoSimulador(), RealContas: m.p.Amb.RealContas, RealContasTodas: m.p.Amb.RealContasTodas}
}

// ambienteConectores: o ambiente dos conectores desta instância (v1.26: um motor por ambiente).
func (m *Motor) ambienteConectores() string { return m.amb }

// Ambiente: o ambiente desta instância.
func (m *Motor) Ambiente() string { return m.amb }

// comPrefixo: o texto do alerta da instância REAL leva o ambiente (o Telegram é um só).
type comPrefixo struct {
	n       alerta.Notificador
	prefixo string
}

func (c comPrefixo) Notificar(codigo, texto string) { c.n.Notificar(codigo, c.prefixo+texto) }

// conferirAmbientePedido (§1.2): a saída e o ajuste do stop usam o ambiente gravado; um pedido com
// outro é executado assim mesmo (recusar a saída deixaria a posição aberta), com o crítico.
func (m *Motor) conferirAmbientePedido(op *operacao, bruto, oque string) {
	if bruto == "" {
		return
	}
	gravado := dominio.AmbienteDe(op.Ambiente)
	if amb, err := dominio.LerAmbiente(bruto); err == nil && amb == gravado {
		return
	}
	m.al.Emitir("AMBIENTE_PEDIDO_DIVERGENTE", chaveOp("AMBIENTE_PEDIDO_DIVERGENTE", op),
		fmt.Sprintf("%s da operacao %d com ambiente %q; a operacao e do %s: executado no %s", oque, op.OperacaoID, bruto, gravado, gravado), op.OperacaoID)
}

// DestinoDoUsuario (GET /usuarios/{uid}/destino, SPEC-AMBIENTES-v1 §4.3): o destino resolvido em
// cada ambiente pedido, para o Kalman decidir na ativação do dia entre o paper e as ordens. Lê o
// banco direto (fora da fila do motor) e nunca devolve a credencial do GQ.
func (m *Motor) DestinoDoUsuario(uid int, ambientes []string) (Resposta, error) {
	hml, prd, achou, err := m.repo.ConfigUsuario(uid)
	if err != nil {
		return nil, err
	}
	r := map[string]any{}
	for _, amb := range ambientes {
		d := map[string]any{"destino": nil, "conector": nil, "corretora": nil, "corretora_codigo": nil, "conta": nil,
			"habilitado": false, "permitido": false, "pode_enviar": false, "codigo": nil, "motivo": nil, "rotulo": nil}
		h, ok, err := m.repo.Habilitacao(uid, amb)
		if err != nil {
			return nil, err
		}
		d["habilitado"] = ok && h.Habilitado
		d["permitido"] = m.p.Amb.Permitidos[amb] && (amb != dominio.AmbienteReal || m.p.Amb.UIDReal(uid))
		falhar := func(codigo, motivo string) {
			if d["codigo"] == nil {
				d["codigo"], d["motivo"] = codigo, motivo
			}
		}
		switch {
		case !m.p.Amb.Permitidos[amb]:
			falhar("AMBIENTE_NAO_PERMITIDO", amb+" nao permitido nesta maquina")
		case amb == dominio.AmbienteReal && !m.p.Amb.UIDReal(uid):
			falhar("REAL_UID_NAO_PERMITIDO", fmt.Sprintf("uid %d fora de REAL_UIDS nesta maquina", uid))
		}
		texto := hml
		if amb == dominio.AmbienteReal {
			texto = prd
		}
		var res usuario.Resolvido
		var f *usuario.Falha
		if !achou {
			f = &usuario.Falha{Codigo: "CONFIG_USUARIO_INVALIDA", Motivo: fmt.Sprintf("uid %d sem linha em acusuario", uid)}
		} else {
			res, f = usuario.ResolverCom(m.regras(amb), texto, h.Conectores)
		}
		switch {
		case f.Paper():
			d["destino"], d["rotulo"] = "PAPER", "Simulador · paper"
		case f != nil:
			falhar(f.Codigo, f.Motivo)
		default:
			d["destino"], d["conector"], d["corretora"], d["corretora_codigo"], d["conta"] = "CONECTOR", res.Conector, usuario.Corretora(res.Destino), res.Destino, res.Conta
			d["rotulo"] = rotuloDestino(amb, res)
		}
		if f == nil && !(ok && h.Habilitado) {
			falhar("UID_NAO_HABILITADO", fmt.Sprintf("uid %d nao habilitado no %s", uid, amb))
		}
		d["pode_enviar"] = d["destino"] == "CONECTOR" && d["codigo"] == nil
		r[amb] = d
	}
	return Resposta{"uid": uid, "ambientes": r}, nil
}

// rotuloDestino: o texto da tela do Kalman ("Simulador · Flexscan", "REAL · Flexscan · Capital 715124").
func rotuloDestino(amb string, res usuario.Resolvido) string {
	con := map[string]string{dominio.ConectorFlexscan: "Flexscan", dominio.ConectorPnT: "PnT"}[res.Conector]
	if amb == dominio.AmbienteReal {
		return fmt.Sprintf("REAL · %s · %s %s", con, usuario.Corretora(res.Destino), res.Conta)
	}
	return "Simulador · " + con
}
