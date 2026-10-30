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
func (m *Motor) tetoAmbiente(amb string, uid int) *recusa { return m.teto(amb, uid, true) }

// teto: as verificações 3 e 4; alertar falso no /usuarios/{uid}/destino (consulta, não tentativa).
func (m *Motor) teto(amb string, uid int, alertar bool) *recusa {
	if !m.p.Amb.Permitidos[amb] {
		texto := fmt.Sprintf("%s nao permitido nesta maquina (AMBIENTES_PERMITIDOS)", amb)
		if amb == dominio.AmbienteReal && alertar {
			m.al.Emitir("AMBIENTE_NAO_PERMITIDO", "AMBIENTE_NAO_PERMITIDO:"+strconv.Itoa(uid), fmt.Sprintf("entrada REAL do uid %d recusada: %s", uid, texto), 0)
		}
		return simples("AMBIENTE_NAO_PERMITIDO", texto)
	}
	if amb != m.amb {
		// o roteador manda cada ambiente à sua instância; aqui só por defeito
		return simples("AMBIENTE_NAO_PERMITIDO", fmt.Sprintf("%s sem instancia nesta maquina", amb))
	}
	if amb == dominio.AmbienteReal && !m.p.Amb.UIDReal(uid) {
		if !alertar {
			return simples("REAL_UID_NAO_PERMITIDO", fmt.Sprintf("uid %d fora de REAL_UIDS nesta maquina", uid))
		}
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
// cada ambiente pedido, para o Kalman decidir na ativação do dia entre o paper e as ordens. v1.27:
// roda na fila do motor, com as mesmas funções e o mesmo cache da entrada, na mesma ordem
// (destinoDaEntrada): o código é o que a entrada daria agora. Nunca devolve a credencial do GQ.
func (m *Motor) DestinoDoUsuario(uid int, ambientes []string) (Resposta, error) {
	r := map[string]any{}
	m.Fazer(func() {
		for _, amb := range ambientes {
			r[amb] = m.destinoDaEntrada(uid, amb)
		}
	})
	return Resposta{"uid": uid, "ambientes": r}, nil
}

// destinoDaEntrada: as verificações da entrada que dependem só da configuração (o teto da máquina,
// a habilitação do ambiente, o conector ligado, a coluna do GQ com a trava), na ordem relativa do
// pedirEntrada e com os mesmos códigos e motivos, sem alerta. Ficam de fora as do pedido (campos,
// DUPLICADA) e as do momento (parada, sessão, conciliação, disjuntor, símbolo, limites, preço).
// destino, conector e conta aparecem sempre que a coluna resolve, mesmo com um código (a tela
// mostra para onde iria).
func (m *Motor) destinoDaEntrada(uid int, amb string) map[string]any {
	d := map[string]any{"destino": nil, "conector": nil, "corretora": nil, "corretora_codigo": nil, "conta": nil,
		"habilitado": false, "permitido": false, "pode_enviar": false, "codigo": nil, "motivo": nil, "rotulo": nil}
	falhar := func(r *recusa) {
		if r != nil && d["codigo"] == nil {
			d["codigo"], d["motivo"] = r.codigo, r.motivo
		}
	}
	d["permitido"] = m.p.Amb.Permitidos[amb] && (amb != dominio.AmbienteReal || m.p.Amb.UIDReal(uid))
	// §4.1 itens 3 e 4
	falhar(m.teto(amb, uid, false))
	// item 5: a habilitação do ambiente (verificarHabilitacao)
	h, ok, err := m.habilitacaoDo(uid, amb)
	switch {
	case err != nil:
		falhar(simples("CONFIG_USUARIO_INDISPONIVEL", "habilitação ilegível"))
	case !ok || !h.Habilitado:
		falhar(m.naoHabilitado(uid, amb))
	}
	d["habilitado"] = err == nil && ok && h.Habilitado
	// o conector desligado antes da coluna (como na entrada)
	falhar(m.pntDesligadoAntes(h))
	// itens 6 a 8: a coluna do GQ, com a trava (contaDoUsuario, o mesmo da entrada)
	res, f := m.contaDoUsuario(uid, amb)
	if f != nil && f.Codigo == "CONECTOR_NAO_HABILITADO" && !(err == nil && ok) {
		// sem a linha da habilitação, a tela ainda mostra o que o GQ aponta (o código já é o da habilitação)
		if hml, prd, achou, e := m.configUsuarioDo(uid); e == nil && achou {
			texto := hml
			if amb == dominio.AmbienteReal {
				texto = prd
			}
			if r2, f2 := usuario.ResolverCom(m.regras(amb), texto, []string{dominio.ConectorPnT, dominio.ConectorFlexscan}); f2 == nil {
				res, f = r2, nil
			}
		}
	}
	switch {
	case f.Paper():
		d["destino"], d["rotulo"] = "PAPER", "Simulador · paper"
		falhar(simples(f.Codigo, f.Motivo))
	case f != nil:
		falhar(simples(f.Codigo, f.Motivo))
	default:
		d["destino"], d["conector"], d["corretora"], d["corretora_codigo"], d["conta"] = "CONECTOR", res.Conector, usuario.Corretora(res.Destino), res.Destino, res.Conta
		d["rotulo"] = rotuloDestino(amb, res)
		// o conector da coluna desligado nesta instância (item 7)
		if res.Conector == dominio.ConectorPnT && m.p.PnTDesligado {
			falhar(conectorDesligado())
		}
	}
	d["pode_enviar"] = d["destino"] == "CONECTOR" && d["codigo"] == nil
	return d
}

// rotuloDestino: o texto da tela do Kalman ("Simulador · Flexscan", "REAL · Flexscan · Capital 715124").
func rotuloDestino(amb string, res usuario.Resolvido) string {
	con := map[string]string{dominio.ConectorFlexscan: "Flexscan", dominio.ConectorPnT: "PnT"}[res.Conector]
	if amb == dominio.AmbienteReal {
		return fmt.Sprintf("REAL · %s · %s %s", con, usuario.Corretora(res.Destino), res.Conta)
	}
	return "Simulador · " + con
}
