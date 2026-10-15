package motor

import (
	"fmt"
	"log/slog"
	"sort"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/pnt"
)

func (m *Motor) aoLogar() {
	m.logonsTratados++
	if m.jaLogou {
		m.al.Emitir("SESSAO_VOLTOU", "", "sessão FIX voltou", 0)
	}
	m.jaLogou = true
	m.presaDesde, m.presaTexto = timeZero, ""
	m.al.Resolver("SESSAO_PRESA_NO_PNT")
	m.al.Resolver("FIX_STORE_ILEGIVEL")
	m.conciliando = true
	m.massTentativas = 0
	m.pedirMassStatus()
}

var contadorMass int

// pedirMassStatus: um AF (585=9) por conta (v1.2 U6) — as contas das operações do dia e as dos
// usuários habilitados (resolvidas no ambiente, com a trava cruzada). Sem conta: nada a pedir, a
// conciliação fecha no silêncio.
func (m *Motor) pedirMassStatus() {
	m.massReqIDs = map[string]bool{}
	m.massEstado, m.massCom911 = map[string]*massConta{}, false
	m.massRespostas, m.massChegadas = nil, nil
	m.massCorte = m.seqOrdens
	m.massUltima = m.agora()
	m.massProxima = timeZero
	contas := m.contasMassStatus()
	if len(contas) == 0 {
		slog.Info("mass status: nenhuma conta (sem operação no dia nem usuário habilitado)")
	}
	for _, c := range contas {
		contadorMass++
		id := fmt.Sprintf("KM-MS-%d-%d", m.agora().UnixMilli(), contadorMass)
		m.massReqIDs[id] = true
		m.massEstado[id] = &massConta{conta: c}
		m.enfileirarEnvio(envioPendente{msg: pnt.MassStatus(c, id), clordid: id, prio: prioSaida,
			// o silêncio conta do envio real do último AF, não da decisão (v1.10 P3)
			aoEnviar: func() { m.massUltima = m.agora() },
			aoFalhar: func() { m.massProxima = m.agora().Add(m.p.RetentativaProtecao) }})
	}
}

func (m *Motor) contasMassStatus() []string {
	// v1.10 C5: só as contas com ordem viva ou posição (pelo banco, carregado no motor) — não todos
	// os habilitados; sem nenhuma, nada é pedido e a conciliação fecha no silêncio
	vistas := map[string]bool{}
	var r []string
	for _, op := range m.ops {
		if op.Conta == "" || vistas[op.Conta] {
			continue
		}
		viva := op.posicao() != 0
		for _, o := range op.ordens {
			if o.viva() {
				viva = true
			}
		}
		if viva {
			vistas[op.Conta] = true
			r = append(r, op.Conta)
		}
	}
	return r
}

// contasAdmin (GET /pnt/ordens, v1.7 C1): as contas das operações do dia e dos usuários
// habilitados (do cache).
func (m *Motor) contasAdmin() []string {
	vistas := map[string]bool{}
	var r []string
	add := func(c string) {
		if c != "" && !vistas[c] {
			vistas[c] = true
			r = append(r, c)
		}
	}
	for _, op := range m.ops {
		add(op.Conta)
	}
	var uids []int
	for uid, h := range m.usuarios.hab {
		if h.Habilitado {
			uids = append(uids, uid)
		}
	}
	sort.Ints(uids)
	for _, uid := range uids {
		if res, f := m.contaDoUsuario(uid); f == nil {
			add(res.Conta)
		} else {
			slog.Warn("consulta do admin: conta do uid habilitado não resolvida", "uid", uid, "codigo", f.Codigo, "motivo", f.Motivo)
		}
	}
	return r
}

// conciliar: Parte 7.2, passos 2 a 5.
// massConta (v1.10 C6): a resposta ao AF de uma conta — respondida com o 912=Y, com o 911
// atingido ou com um 35=j ligado pelo 379.
type massConta struct {
	conta            string
	recebidas, total int
	feita            bool
}

// respostaMass: uma resposta (150=I) do AF reqID chegou ao motor.
func (m *Motor) respostaMass(reqID string, msg pnt.Mensagem) {
	st := m.massEstado[reqID]
	if st == nil {
		return
	}
	st.recebidas++
	if t, ok := msg.Inteiro(911); ok {
		st.total, m.massCom911 = t, true
	}
	if v := msg.Texto(912); v != "" {
		m.massCom911 = true
		if v == "Y" {
			st.feita = true
		}
	}
	if st.total > 0 && st.recebidas >= st.total {
		st.feita = true
	}
	m.fecharMassSeCompleto()
}

// massCompleta: todas as contas pedidas responderam.
func (m *Motor) massCompleta() bool {
	if len(m.massEstado) == 0 {
		return false
	}
	for _, st := range m.massEstado {
		if !st.feita {
			return false
		}
	}
	return true
}

// fecharMassSeCompleto: com todas as contas respondidas, a conciliação fecha na hora (v1.10 C6).
func (m *Motor) fecharMassSeCompleto() {
	if m.conciliando && m.massCompleta() {
		m.conciliar()
	}
}

func (m *Motor) conciliar() {
	m.conciliando = false
	// v1.10 C6: com o 911/912 em uso, as ordens das contas que não terminaram de responder não são
	// julgadas (resposta lenta não vira DESCONHECIDA)
	semResposta := map[string]bool{}
	if m.massCom911 {
		for _, st := range m.massEstado {
			if !st.feita {
				semResposta[st.conta] = true
			}
		}
	}
	naoJulgadas := 0
	vistas := map[*ordem]bool{}
	ignoradas, divergencias := 0, 0
	for i, r := range m.massRespostas {
		if r == nil {
			ignoradas++
			continue
		}
		chegou := m.agora()
		if i < len(m.massChegadas) {
			chegou = m.massChegadas[i]
		}
		o := m.localizar(r.Texto(37), r.Texto(11), r.Texto(41))
		if o == nil {
			divergencias++
			m.registrarRecebidaEm(nil, *r, chegou)
			m.al.Emitir("DESCONHECIDA", "", fmt.Sprintf("ordem KM no PnT sem registro: 11=%s 37=%s", r.Texto(11), r.Texto(37)), 0)
			continue
		}
		vistas[o] = true
		if m.registrarRecebidaEm(o, *r, chegou) {
			m.aplicarExecReport(m.donoDa[o], o, *r)
		}
	}
	for _, op := range m.ops {
		for _, o := range op.ordens {
			// só julga as que já existiam quando o mass status foi pedido
			if !o.viva() || vistas[o] || o.seq > m.massCorte || o.saidaNoMass {
				continue // a saída com recusa terminal é decidida abaixo (v1.3 I1)
			}
			if semResposta[o.Conta] {
				naoJulgadas++
				continue
			}
			if o.decidirNoMass {
				// 1014 em erro com cancel rejeitado e ausente no mass status: não existe mais (F17)
				o.decidirNoMass = false
				o.Estado = dominio.Desconhecida
				limparPendencia(o)
				m.salvarOrdem(o)
				m.recriarProtecaoAusente(op, "1014 em erro ausente no mass status")
				continue
			}
			// [PnT-13] o mass status traz só as ativas: viva aqui e ausente lá
			divergencias++
			op.Divergente = true
			m.al.Emitir("DESCONHECIDA", "", fmt.Sprintf("ordem viva na tabela e ausente no PnT: %s", o.ClOrdAtual), op.OperacaoID)
			o.Estado = dominio.Desconhecida
			limparPendencia(o)
			m.salvarOrdem(o)
			if o.Papel == dominio.Entrada {
				m.encerrarEntrada(op, "DESCONHECIDA", true)
			}
		}
	}
	for _, op := range m.ops {
		for _, o := range op.ordens {
			if o.decidirNoMass {
				o.decidirNoMass = false
				m.decidirProtecaoEmErro(op, o)
			}
		}
	}
	// v1.3 I1: a saída que o PnT deu como terminal (35=9 do replace) continua viva depois do mass
	// status (viva lá, ou ausente): sem resposta coerente, alerta crítico e a saída fica suspensa
	for _, op := range m.ops {
		for _, o := range op.ordens {
			if !o.saidaNoMass {
				continue
			}
			o.saidaNoMass = false
			if !o.viva() {
				continue
			}
			o.saidaSemFill = true
			m.al.Emitir("SAIDA_SEM_FILL", chaveOp("SAIDA_SEM_FILL", op),
				fmt.Sprintf("saída %s: o PnT recusou o replace dizendo que a ordem está terminal (39=%s), mas não veio fill nem 39=4 e o mass status não confirma (%s); posição %d, saída suspensa, decisão humana, operação %d",
					o.ClOrdAtual, o.recusaTerminal39, map[bool]string{true: "a ordem aparece viva", false: "a ordem não aparece"}[vistas[o]], op.posicao(), op.OperacaoID), op.OperacaoID)
		}
	}
	// posição ≠ 0 sem 1014 viva → cria a 1014 (7.2.4)
	for _, op := range m.ops {
		if op.Estado.Final() || op.Estado.Saindo() || op.posicao() == 0 || op.viva(dominio.Protecao) != nil {
			continue
		}
		m.mudar(op, dominio.PosicionadaSemProtecao)
		op.semProtecaoDesde = m.agora()
		m.ajustarProtecao(op)
	}
	for _, op := range m.ops {
		m.al.Resolver(chaveOp("SEM_CONFIRMACAO", op))
		m.salvarOp(op)
	}
	m.al.Resolver("SESSAO_REJEITOU_MENSAGEM")
	m.al.Resolver("MENSAGEM_ILEGIVEL")
	m.al.Resolver("MASS_STATUS_REJEITADO")
	if divergencias > 0 {
		m.divergente = true
		m.al.Emitir("DIVERGENTE", "DIVERGENTE", fmt.Sprintf("conciliação com %d divergência(s)", divergencias), 0)
		m.ativarParada("BLOQUEAR", "auto", fmt.Sprintf("conciliação com %d divergência(s)", divergencias))
	}
	texto := fmt.Sprintf("%d ordem(ns) KM, %d de outros (ignoradas), %d divergência(s); posição %d", len(vistas), ignoradas, divergencias, m.posicaoTotal())
	if len(semResposta) > 0 {
		contas := make([]string, 0, len(semResposta))
		for c := range semResposta {
			contas = append(contas, c)
		}
		sort.Strings(contas)
		texto += fmt.Sprintf("; contas sem resposta completa em %s: %v (%d ordem(ns) viva(s) não verificada(s))", m.p.EsperaMaxMassStatus, contas, naoJulgadas)
		slog.Warn("conciliação fechada sem a resposta completa de todas as contas", "contas", contas, "nao_verificadas", naoJulgadas)
	}
	m.al.Emitir("CONCILIACAO", "", texto, 0)
	slog.Info("conciliação", "resumo", texto)
}
