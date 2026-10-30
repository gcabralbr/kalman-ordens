package motor

import (
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"

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
	// SPEC-FLEXSCAN-v1: cada conta pelo conector dela; o conector sem sessão não é perguntado e as
	// ordens dele não são julgadas nesta conciliação (contam como não verificadas)
	m.massSemSessao = map[string]bool{}
	var contas []contaConector
	for _, c := range m.contasMassStatus() {
		if !m.logado(c.con) {
			m.massSemSessao[c.con] = true
			continue
		}
		contas = append(contas, c)
	}
	if len(contas) == 0 {
		slog.Info("mass status: nenhuma conta (sem operação no dia nem usuário habilitado)")
	}
	for _, cc := range contas {
		c := cc.conta
		contadorMass++
		id := fmt.Sprintf("KM-MS-%d-%d", m.agora().UnixMilli(), contadorMass)
		m.massReqIDs[id] = true
		m.massEstado[id] = &massConta{conta: c}
		m.enfileirarEnvio(envioPendente{msg: pnt.MassStatus(c, id), clordid: id, prio: prioSaida, con: cc.con,
			// o silêncio conta do envio real do último AF, não da decisão (v1.10 P3)
			aoEnviar: func() { m.massUltima = m.agora() },
			aoFalhar: func() { m.massProxima = m.agora().Add(m.p.RetentativaProtecao) }})
	}
}

// contaConector: uma conta e o conector por onde ela é perguntada.
type contaConector struct{ conta, con string }

func (m *Motor) contasMassStatus() []contaConector {
	// v1.10 C5: só as contas com ordem viva ou posição (pelo banco, carregado no motor) — não todos
	// os habilitados; sem nenhuma, nada é pedido e a conciliação fecha no silêncio
	vistas := map[contaConector]bool{}
	var r []contaConector
	for _, op := range m.ops {
		cc := contaConector{op.Conta, op.conector()}
		if op.Conta == "" || vistas[cc] {
			continue
		}
		viva := op.posicao() != 0
		for _, o := range op.ordens {
			if o.viva() {
				viva = true
			}
		}
		if viva {
			vistas[cc] = true
			r = append(r, cc)
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
		if op.conector() == dominio.ConectorPnT { // a consulta é do PnT (GET /pnt/ordens)
			add(op.Conta)
		}
	}
	// os habilitados no ambiente dos conectores (v1.25: um conjunto só)
	amb := m.ambienteConectores()
	var uids []int
	for k, h := range m.usuarios.hab {
		if h.Habilitado && k.amb == amb {
			uids = append(uids, k.uid)
		}
	}
	sort.Ints(uids)
	for _, uid := range uids {
		if res, f := m.contaDoUsuario(uid, amb); f == nil {
			if res.Conector == dominio.ConectorPnT {
				add(res.Conta)
			}
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
	// v1.22: primeiro as ordens que a corretora tem e o banco não (enviadas antes da gravação, o
	// processo caiu): reconstituídas e aplicadas antes das outras, para o fill da saída zerar a
	// operação antes de a 1014 cancelada parecer "cancelada sem pedido nosso"
	reconstituidas := map[int]*ordem{}
	for i, r := range m.massRespostas {
		if r != nil && m.localizar(r.Texto(37), r.Texto(11), r.Texto(41)) == nil {
			if o := m.reconstituir(*r); o != nil {
				reconstituidas[i] = o
			}
		}
	}
	ordemAplicar := make([]int, 0, len(m.massRespostas))
	for i := range m.massRespostas {
		if reconstituidas[i] != nil {
			ordemAplicar = append(ordemAplicar, i)
		}
	}
	for i := range m.massRespostas {
		if reconstituidas[i] == nil {
			ordemAplicar = append(ordemAplicar, i)
		}
	}
	for _, i := range ordemAplicar {
		r := m.massRespostas[i]
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
			m.al.Emitir("DESCONHECIDA", "", fmt.Sprintf("ordem KM na corretora sem registro (operacao desconhecida ou encerrada): 11=%s 37=%s", r.Texto(11), r.Texto(37)), 0)
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
			if !op.SuspensaEm.IsZero() {
				continue // v1.28 §3: a operação suspensa não é julgada (marcarAusentesDaVolta)
			}
			if !o.viva() || vistas[o] || o.seq > m.massCorte || o.saidaNoMass {
				continue // a saída com recusa terminal é decidida abaixo (v1.3 I1)
			}
			if semResposta[o.Conta] || m.massSemSessao[op.conector()] {
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
			m.al.Emitir("DESCONHECIDA", "", fmt.Sprintf("ordem viva na tabela e ausente na %s: %s", op.corretora(), o.ClOrdAtual), op.OperacaoID)
			o.Estado = dominio.Desconhecida
			limparPendencia(o)
			m.salvarOrdem(o)
			if o.Papel == dominio.Entrada {
				m.encerrarEntrada(op, "DESCONHECIDA", true)
			}
		}
	}
	// v1.28 §3: as ordens da operação suspensa que a corretora não tem (sem julgar); a confirmação de
	// posição 0 esperando um cancel de ordem que sumiu termina aqui
	for _, op := range m.ops {
		if op.Estado.Final() || op.SuspensaEm.IsZero() {
			continue
		}
		m.marcarAusentesDaVolta(op, vistas, !semResposta[op.Conta] && !m.massSemSessao[op.conector()])
		if op.encerrandoFora {
			for _, o := range op.ordens {
				if o.viva() && o.ausenteNaVolta {
					m.canceladaFora(o)
				}
			}
			m.terminarForaSePronto(op)
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
				fmt.Sprintf("saída %s: a %s recusou o replace dizendo que a ordem está terminal (39=%s), mas não veio fill nem 39=4 e o mass status não confirma (%s); posição %d, saída suspensa, decisão humana, operação %d",
					o.ClOrdAtual, op.corretora(), o.recusaTerminal39, map[bool]string{true: "a ordem aparece viva", false: "a ordem não aparece"}[vistas[o]], op.posicao(), op.OperacaoID), op.OperacaoID)
		}
	}
	// posição ≠ 0 sem 1014 viva → cria a 1014 (7.2.4)
	for _, op := range m.ops {
		if op.Estado.Final() || op.Estado.Saindo() || op.posicao() == 0 || op.viva(dominio.Protecao) != nil || !op.SuspensaEm.IsZero() {
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
	m.avaliarSuspensas(semResposta)
}

// reconstituir (v1.22): a 1014 e a saída saem antes da gravação do lote; se o processo cai entre o
// envio e a gravação, a ordem fica na corretora sem a linha no banco. O ClOrdID diz a operação e o
// papel: com a operação conhecida e não encerrada, a ordem é recriada com o que o mass status traz
// (lado, quantidade, preço, gatilho 20828/20829, id), o contador de ClOrdID da operação passa do número dela e
// sai ORDEM_RECONSTITUIDA (crítico); o ExecutionReport é aplicado depois, como o de qualquer ordem
// (fill incluído). Sem a operação, encerrada ou com o ClOrdID fora do formato: nil (DESCONHECIDA,
// como antes).
func (m *Motor) reconstituir(r pnt.Mensagem) *ordem {
	cl := r.Texto(11)
	g := reClOrdN.FindStringSubmatch(cl)
	if g == nil {
		return nil
	}
	modulo := g[1]
	if modulo == "" {
		modulo = dominio.ModuloPadrao
	}
	opid, _ := strconv.ParseInt(g[3], 10, 64)
	n, _ := strconv.Atoi(g[4])
	op := m.porOp[chaveOperacao{modulo, opid}]
	if op == nil || op.Estado.Final() {
		return nil
	}
	papel := dominio.Papel(g[2][0])
	lado := dominio.Compra
	if r.Texto(54) == "2" {
		lado = dominio.Venda
	}
	qtd, _ := r.Inteiro(38)
	estrategia := pnt.EstrategiaAgendada
	if papel == dominio.Protecao {
		estrategia = pnt.EstrategiaStop
	}
	if papel == dominio.Entrada {
		for _, x := range op.ordens {
			if x.Papel == dominio.Entrada {
				estrategia = x.Estrategia
			}
		}
	}
	m.seqOrdens++
	o := &ordem{seq: m.seqOrdens, Ordem: dominio.Ordem{
		ID: m.novoIDOrdem(), Data: op.Data, Modulo: op.Modulo, UID: op.UID, OperacaoID: op.OperacaoID, Papel: papel, Conta: op.Conta, Simbolo: op.Simbolo,
		ClOrdBase: cl, ClOrdAtual: cl, Estrategia: estrategia, Lado: lado, Qtd: qtd, Estado: dominio.Enviando, CriadaEm: m.agora(),
	}}
	if px, ok := r.Decimal(44); ok {
		o.Preco = px.Round(0).IntPart()
	}
	if px, ok := r.Decimal(20828); ok { // gatilho da 1014 (PnT; a ponte da Flexscan põe o mesmo)
		o.Stop, o.StopLim = px.Round(0).IntPart(), o.Preco
	}
	if px, ok := r.Decimal(20829); ok {
		o.StopLim = px.Round(0).IntPart()
	}
	if op.ContadorN == nil {
		op.ContadorN = map[string]int{}
	}
	if n > op.ContadorN[papel.String()] {
		op.ContadorN[papel.String()] = n // o próximo ClOrdID do papel não repete o dela
	}
	m.indexar(op, o)
	m.salvarOrdem(o)
	if papel == dominio.Saida && !op.Estado.Saindo() && faseViva(r.Texto(39)) {
		// a saída viva na corretora: a operação está saindo (a 1014 cancelada pela saída não é
		// "cancelada sem pedido nosso")
		m.mudar(op, dominio.Saindo)
	}
	m.salvarOp(op)
	slog.Warn("conciliação: ordem da corretora sem linha no banco, reconstituída", "op", op.OperacaoID, "clordid", cl, "id", r.Texto(37))
	m.al.Emitir("ORDEM_RECONSTITUIDA", chaveOp("ORDEM_RECONSTITUIDA:"+cl, op),
		fmt.Sprintf("ordem %s na %s sem registro no banco (enviada e nao gravada: o processo caiu no meio?): reconstituida pela operacao %d (%s %d, 39=%s); conferir",
			cl, op.corretora(), op.OperacaoID, lado.Letra(), qtd, r.Texto(39)), op.OperacaoID)
	return o
}

// reClOrdN: o ClOrdID do motor com o número (KM-[MOD-]P-<op>-<n>).
var reClOrdN = regexp.MustCompile(`^KM-(?:([A-Z][A-Z0-9]{1,7})-)?([EPS])-(\d+)-(\d+)$`)

// faseViva: o 39 de uma ordem que ainda trabalha (nova, parcial, substituída, pendentes).
func faseViva(st string) bool {
	switch st {
	case "0", "1", "5", "6", "A", "E":
		return true
	}
	return false
}
