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
		// v1.29: a configuração atual (habilitação e GQ), sem o cache: o /destino só é chamado ao abrir
		// a tela e ao ligar "Enviar ordens"; o que leu atualiza o cache da entrada
		if err := m.lerUsuarioAgora(uid); err != nil {
			for _, amb := range ambientes {
				r[amb] = destinoIndisponivel(err)
			}
			return
		}
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
		"habilitado": false, "permitido": false, "pode_enviar": false, "codigo": nil, "motivo": nil, "rotulo": nil, "canal": nil}
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
		// v1.30: o guarda-chuva da corretora (no modo por corretora) e a conta na lista dele
		falhar(m.recusaCorretora(res.Conector, res.Destino, res.Conta))
		// v1.28 §3: o canal do conector (a entrada sem sessão é SEM_SESSAO)
		chave := m.chaveDe(res.Conector, res.Destino)
		est, motivo, _ := m.canal(chave)
		d["canal"] = est
		if est != canalNoAr {
			falhar(simples("SEM_SESSAO", fmt.Sprintf("conector %s sem sessão: %s", nomeCanal(chave), motivo)))
		}
	}
	// v1.29 (SPEC-AMBIENTES D2 revisada): no SIMULADOR todo usuário é paper; a máquina que não roda o
	// SIMULADOR (a .51, só REAL) mostra o paper como paper e recusa com clareza a configuração de
	// SIMULADOR que aponta para um conector
	if amb == dominio.AmbienteSimulador && !m.p.Amb.Permitidos[amb] {
		if hml, _, achou, e := m.configUsuarioDo(uid); e == nil && achou {
			// só para classificar a coluna: sem a trava das contas de simulador (esta máquina não as tem)
			rg := usuario.Regras{Ambiente: amb, Flexscan: true, PnTNoSimulador: true}
			r2, f2 := usuario.ResolverCom(rg, hml, []string{dominio.ConectorPnT, dominio.ConectorFlexscan})
			switch {
			case f2.Paper():
				d["destino"], d["rotulo"], d["codigo"], d["motivo"] = "PAPER", "Simulador · paper", f2.Codigo, f2.Motivo
			case f2 == nil || f2.Codigo == "CONTA_AMBIENTE":
				d["destino"] = "CONECTOR"
				if f2 == nil {
					d["conector"], d["corretora"], d["corretora_codigo"], d["conta"] = r2.Conector, usuario.Corretora(r2.Destino), r2.Destino, r2.Conta
				}
				d["motivo"] = "no SIMULADOR todo usuario e paper: esta maquina nao roda o SIMULADOR e o simulador da Flexscan e so para os testes da .53; ponha o destino do usuario no paper do GQ"
			}
		}
	}
	d["pode_enviar"] = d["destino"] == "CONECTOR" && d["codigo"] == nil
	return d
}

// destinoIndisponivel: a configuração do usuário não pôde ser lida (banco).
func destinoIndisponivel(err error) map[string]any {
	return map[string]any{"destino": nil, "conector": nil, "corretora": nil, "corretora_codigo": nil, "conta": nil,
		"habilitado": false, "permitido": false, "pode_enviar": false, "codigo": "CONFIG_USUARIO_INDISPONIVEL",
		"motivo": "configuracao do usuario ilegivel: " + err.Error(), "rotulo": nil, "canal": nil}
}

// rotuloDestino: o texto da tela do Kalman ("Simulador · Flexscan", "REAL · Flexscan · Capital 715124").
func rotuloDestino(amb string, res usuario.Resolvido) string {
	con := map[string]string{dominio.ConectorFlexscan: "Flexscan", dominio.ConectorPnT: "PnT"}[res.Conector]
	if amb == dominio.AmbienteReal {
		return fmt.Sprintf("REAL · %s · %s %s", con, usuario.Corretora(res.Destino), res.Conta)
	}
	return "Simulador · " + con
}
