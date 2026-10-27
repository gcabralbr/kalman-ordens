package motor

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/pnt"
)

func (m *Motor) pedirSaida(p Pedido) (Resposta, *recusa) {
	// a saída não depende da habilitação atual nem da conta atual do GQ: usa o que a entrada
	// gravou (v1.2 U4). Só confere que o uid é o da operação.
	if p.UID == nil {
		return nil, simples("CAMPO_INVALIDO", "uid ausente")
	}
	if p.OperacaoID == nil {
		return nil, simples("CAMPO_INVALIDO", "operacao_id ausente")
	}
	if r := motivoLongo(p.Motivo, dominio.MaxMotivoSaida); r != nil {
		return nil, r
	}
	lado, ok := dominio.LadoDaLetra(p.Lado)
	if !ok {
		return nil, simples("CAMPO_INVALIDO", "lado deve ser C ou V")
	}
	op := m.operacaoDo(p)
	if op == nil || op.Estado.Final() {
		// v1.19: a saída do gestor que chega depois do ZERADA pela stop é corrida, não problema
		if op == nil || (op.OrigemSaida != "STOP_SERVIDOR" && op.OrigemSaida != "STOP_FURADO") {
			m.al.Emitir("SEM_POSICAO", "", fmt.Sprintf("SAIDA sem posição: operação %d", *p.OperacaoID), *p.OperacaoID)
		}
		return nil, simples("SEM_POSICAO", "operação sem posição real")
	}
	if *p.UID != op.UID {
		return nil, simples("CAMPO_INVALIDO", fmt.Sprintf("uid %d diferente do da operação (%d)", *p.UID, op.UID))
	}
	m.conferirConta(op)
	if !op.SaidaPedidaEm.IsZero() || op.saidaAposEntrada {
		// saída já iniciada pelo serviço (stop, parada, zeragem...): o pedido do gestor é aceito
		return Resposta{"aceito": true, "operacao_id": op.OperacaoID, "ja_em_saida": true, "origem": op.OrigemSaida}, nil
	}
	pos := op.posicao()
	entradaViva := op.viva(dominio.Entrada) != nil
	if pos == 0 && !entradaViva {
		m.al.Emitir("SEM_POSICAO", "", fmt.Sprintf("SAIDA sem posição: operação %d", op.OperacaoID), op.OperacaoID)
		return nil, simples("SEM_POSICAO", "posição real zero")
	}
	// reduce-only: o lado da saída tem de reduzir a posição (ou a entrada que está trabalhando)
	sinal := pos
	if sinal == 0 {
		sinal = int(op.Lado)
	}
	if lado != dominio.ParaZerar(sinal) {
		m.al.Emitir("LADO_INCOMPATIVEL", "", fmt.Sprintf("SAIDA com lado %s aumentaria a posição %d: operação %d", p.Lado, pos, op.OperacaoID), op.OperacaoID)
		return nil, simples("LADO_INCOMPATIVEL", "a saída aumentaria a posição")
	}
	// B5.d: ticker divergente e último preço do símbolo certo antigo → não envia, 1014 fica
	if m.divergenteOp(op) && !m.ultimoCertoValido(op) {
		m.al.Emitir("SAIDA_BLOQUEADA_PRECO_ANTIGO", chaveOp("SAIDA_BLOQUEADA", op),
			fmt.Sprintf("saída da operação %d não enviada: ticker divergente e último preço de %s com mais de %s", op.OperacaoID, op.Simbolo, m.p.IdadeMaxPrecoDivergente), op.OperacaoID)
		return nil, simples("SIMBOLO_DIVERGENTE", "ticker divergente e sem preço recente do símbolo certo")
	}
	if p.Qtd != nil && *p.Qtd < abs(pos) {
		slog.Warn("saída parcial tratada como total na v1", "op", op.OperacaoID, "qtd", *p.Qtd, "posicao", pos)
	}
	if m.sinalAtual != nil {
		// sinal (v1.10 §6): a saída começa quando ele fechar, na ordem da rajada
		motivo := p.Motivo
		m.adiarSaida(op, func() { m.iniciarSaida(op, motivo, "GESTOR") })
	} else {
		m.iniciarSaida(op, p.Motivo, "GESTOR")
	}
	return Resposta{"aceito": true, "operacao_id": op.OperacaoID, "posicao": op.posicao()}, nil
}

// intervaloRepreco: TIMEOUT_SAIDA_S entre repreços; depois de esgotar MAX_REPRECO_SAIDA
// (SAIDA_ESGOTOU, alerta crítico mantido), REPRECO_ESGOTADO_S — uma saída presa não ocupa a sessão
// (v1.10 L; HML 16/10: 43 replaces em 80 s).
func (m *Motor) intervaloRepreco(op *operacao) time.Duration {
	if op.repreco > m.p.MaxRepreco && m.p.ReprecoEsgotado > m.p.TimeoutSaida {
		return m.p.ReprecoEsgotado
	}
	return m.p.TimeoutSaida
}

// iniciarSaida começa a Parte 3.5. Idempotente.
func (m *Motor) iniciarSaida(op *operacao, motivo, origem string) {
	if op.Estado.Final() || op.Estado.Saindo() || op.saidaAposEntrada {
		return
	}
	op.MotivoSaida, op.OrigemSaida, op.SaidaPedidaEm = motivo, origem, m.agora()
	if e := op.viva(dominio.Entrada); e != nil {
		m.cancelar(e)
		if op.Estado.Final() {
			return // a entrada nunca saiu (estava na fila da taxa): a operação já encerrou sem entrada
		}
		if op.posicao() == 0 {
			op.saidaAposEntrada = true
			m.salvarOp(op)
			return
		}
	}
	m.prosseguirSaida(op)
}

// prosseguirSaida: passo 2 em diante — cancela a 1014 (se houver) ou vai direto à saída.
func (m *Motor) prosseguirSaida(op *operacao) {
	if p := op.viva(dominio.Protecao); p != nil {
		if m.iniciarSaidaPorReplace(op, p) {
			return // v1.11: o replace da 1014 é a saída
		}
		m.mudar(op, dominio.CancelandoProtecao)
		op.cancelProtEm = m.agora()
		m.cancelar(p)
		return
	}
	m.mudar(op, dominio.Saindo)
	m.enviarSaida(op)
}

func (m *Motor) enviarSaida(op *operacao) {
	pos := op.posicao()
	if pos == 0 {
		m.zerada(op)
		return
	}
	lado := dominio.ParaZerar(pos)
	k := 1
	if op.continuarRepreco {
		// a anterior foi cancelada por estado de erro (F14): segue do repreço seguinte
		op.continuarRepreco = false
		op.repreco++
		if op.repreco > m.p.MaxRepreco {
			m.al.Emitir("SAIDA_ESGOTOU", chaveOp("SAIDA_ESGOTOU", op),
				fmt.Sprintf("saída da operação %d esgotou %d repreços; continua reapregoando", op.OperacaoID, m.p.MaxRepreco), op.OperacaoID)
		}
		k = min(op.repreco, m.p.MaxRepreco) + 1
	} else {
		op.repreco = 0
	}
	px, divergente, degradado, ok := m.precoSaida(op, lado, k)
	if !ok {
		if divergente {
			m.al.Emitir("SAIDA_BLOQUEADA_PRECO_ANTIGO", chaveOp("SAIDA_BLOQUEADA", op),
				fmt.Sprintf("saída da operação %d não enviada: ticker divergente e sem preço recente de %s", op.OperacaoID, op.Simbolo), op.OperacaoID)
			op.congelada = true
			return
		}
		m.al.Emitir("SAIDA_SEM_PRECO", chaveOp("SAIDA_SEM_PRECO", op), fmt.Sprintf("sem preço para a saída da operação %d", op.OperacaoID), op.OperacaoID)
		op.proximaSaidaEm = m.agora().Add(m.p.TimeoutSaida)
		return
	}
	m.al.Resolver(chaveOp("SAIDA_SEM_PRECO", op))
	if m.emLeilao(op) {
		m.avisarSaidaEmLeilao(op, px)
	} else if degradado {
		m.al.Emitir("SAIDA_PRECO_DEGRADADO", "", fmt.Sprintf("saída da operação %d com preço velho ou sem bid/ask", op.OperacaoID), op.OperacaoID)
	}
	o := m.novaOrdem(op, dominio.Saida, lado, abs(pos), pnt.EstrategiaAgendada)
	o.Preco = px
	o.MotivoGestor = op.MotivoSaida
	m.salvarOrdem(o)
	m.indexar(op, o)
	op.saidaDivergente = divergente
	op.proximoRepreco = m.agora().Add(m.intervaloRepreco(op))
	m.enviarNova(op, o, func() {
		o.Estado = dominio.FalhaEnvio
		m.salvarOrdem(o)
		op.proximaSaidaEm = m.agora().Add(m.p.TimeoutSaida)
	})
	slog.Info("SAIDA decidida", "clordid", o.ClOrdAtual, "preco", px, "divergente", divergente)
}

// precoSaida: bid/ask ∓ COLAR_SAIDA × k (B2). Sem bid/ask, o preco. Posição em contrato que não é
// mais o vigente: último preco do símbolo da posição com o colar máximo (B5.c). ok=false: não há
// preço utilizável.
func (m *Motor) precoSaida(op *operacao, lado dominio.Lado, k int) (px int64, divergente, degradado, ok bool) {
	if m.divergenteOp(op) {
		if !m.ultimoCertoValido(op) {
			return 0, true, false, false
		}
		ultimo := m.ultimos[m.chaveUltimo(op.conector(), op.Simbolo)].px
		colar := decimal.NewFromInt(m.p.ColarSaida * int64(m.p.MaxRepreco+1))
		if lado == dominio.Venda {
			return m.abaixo(ultimo.Sub(colar)), true, false, true
		}
		return m.acima(ultimo.Add(colar)), true, false, true
	}
	l := m.precosDo(op.conector()).Ler()
	if l.Situacao != preco.OK {
		return 0, false, false, false
	}
	if l.Status84 != 0 {
		px, ok := m.precoSaidaLeilao(op, l, lado)
		return px, false, false, ok
	}
	ref := l.Ask
	if lado == dominio.Venda {
		ref = l.Bid
	}
	if ref == nil {
		ref, degradado = l.Preco, true
	}
	if ref == nil {
		return 0, false, false, false
	}
	if l.Idade(m.agora()) > m.p.IdadeMaxPreco {
		degradado = true
	}
	colar := decimal.NewFromInt(m.p.ColarSaida * int64(k))
	if lado == dominio.Venda {
		return m.abaixo(ref.Sub(colar)), false, degradado, true
	}
	return m.acima(ref.Add(colar)), false, degradado, true
}

// emLeilao (v1.18): a fonte da operação diz leilão (status84 != 0). Com ticker divergente vale o
// caminho da B5.c.
func (m *Motor) emLeilao(op *operacao) bool {
	l := m.precosDo(op.conector()).Ler()
	return l.Situacao == preco.OK && l.Status84 != 0 && !m.divergenteOp(op)
}

// precoSaidaLeilao (v1.18, 26/10): em leilão bid/ask e preco não são referência (o preco é o último
// negócio; o teórico não está na fonte). A saída vai com limite largo, SAIDA_LEILAO_PCT sobre o último
// negócio (venda abaixo, compra acima): o leilão executa no preço de reabertura, não no limite. Nunca a
// mercado. Sem preco na leitura (leilão de abertura), o último guardado do símbolo.
func (m *Motor) precoSaidaLeilao(op *operacao, l preco.Leitura, lado dominio.Lado) (int64, bool) {
	ref := l.Preco
	if ref == nil {
		u, ok := m.ultimos[m.chaveUltimo(op.conector(), op.Simbolo)]
		if !ok {
			return 0, false
		}
		ref = &u.px
	}
	pct := decimal.NewFromInt(m.p.SaidaLeilaoPct).Div(decimal.NewFromInt(100))
	if lado == dominio.Venda {
		return m.abaixo(ref.Mul(decimal.NewFromInt(1).Sub(pct))), true
	}
	return m.acima(ref.Mul(decimal.NewFromInt(1).Add(pct))), true
}

// avisarSaidaEmLeilao: SAIDA_EM_LEILAO, crítico, uma vez por leilão; resolve quando o status84 volta
// a 0 (repreço normal) ou a operação zera.
func (m *Motor) avisarSaidaEmLeilao(op *operacao, px int64) {
	op.saidaEmLeilao = true
	m.al.Emitir("SAIDA_EM_LEILAO", chaveOp("SAIDA_EM_LEILAO", op),
		fmt.Sprintf("saída da operação %d em leilão (status84 %d): limite %d, %d%% sobre o último negócio; sem repreço até o leilão acabar",
			op.OperacaoID, m.precosDo(op.conector()).Ler().Status84, px, m.p.SaidaLeilaoPct), op.OperacaoID)
}

// ultimoCertoValido: há último preco do símbolo da posição com até IDADE_MAX_PRECO_DIVERGENTE_S.
func (m *Motor) ultimoCertoValido(op *operacao) bool {
	u, ok := m.ultimos[m.chaveUltimo(op.conector(), op.Simbolo)]
	return ok && m.agora().Sub(u.em) <= m.p.IdadeMaxPrecoDivergente
}

func (m *Motor) zerada(op *operacao) {
	if op.Estado.Final() {
		return
	}
	for _, o := range op.ordens {
		// a que acabou de executar por completo (39=2 ainda não aplicado) não se cancela
		if o.viva() && o.QtdExec < o.Qtd {
			m.cancelar(o)
		}
	}
	op.PnL = op.pnlRealizado()
	op.zeradaEm = m.agora()
	m.mudar(op, dominio.Zerada)
	for _, k := range []string{"SEM_PROTECAO", "STOP_FURADO", "SAIDA_ESGOTOU", "SAIDA_EM_LEILAO", "CANCEL_SEM_RESPOSTA", "SAIDA_SEM_PRECO", "SAIDA_SEM_FILL", "SAIDA_DIVERGENTE", "SAIDA_BLOQUEADA", "PROTECAO_RECRIADA", "PROTECAO_INSTAVEL", "PROTECAO_EM_ERRO", "PROTECAO_EM_ERRO_PRESA", "CONTA_ALTERADA", "SIMBOLO"} {
		m.al.Resolver(chaveOp(k, op))
	}
	ps, _ := op.medio(false)
	d := map[string]any{"preco_medio_saida": ps, "pnl_pts": op.PnL, "origem": op.OrigemSaida, "motivo": op.MotivoSaida}
	if op.OrigemSaida == "STOP_SERVIDOR" {
		d["stop_origem"] = op.stopOrigem() // v1.19
	}
	m.publicar("ZERADA", op, d)
}

// tickSaida: repreço (A5) e nova saída quando a anterior acabou sem zerar.
// esperarFimDaSaida (v1.3 I1): o replace da saída foi recusado com a ordem já terminal no PnT.
// Sem repreço; o fill ou o 39=4 encerram a ordem pelo fluxo normal. Passado
// ESPERA_FILL_POS_RECUSA_S, mass status na hora (conciliar decide); depois de um mass status sem
// estado coerente (SAIDA_SEM_FILL), a saída fica suspensa: decisão humana.
func (m *Motor) esperarFimDaSaida(op *operacao, s *ordem, agora time.Time) {
	if s.saidaNoMass || s.saidaSemFill || agora.Sub(s.recusaTerminalEm) < m.p.EsperaFillPosRecusa {
		return
	}
	s.saidaNoMass = true
	slog.Warn("saída com replace recusado (ordem terminal no PnT) sem fill nem 39=4: mass status",
		"op", op.OperacaoID, "clordid", s.ClOrdAtual, "39", s.recusaTerminal39, "espera", m.p.EsperaFillPosRecusa)
	if !m.conciliando {
		m.conciliando = true
		m.massTentativas = 0
		m.pedirMassStatus()
	}
}

func (m *Motor) tickSaida(op *operacao) {
	if op.posicao() == 0 {
		m.zerada(op)
		return
	}
	if op.congelada {
		return
	}
	if op.saidaRep != nil {
		m.tickSaidaReplace(op)
		return
	}
	agora := m.agora()
	s := op.viva(dominio.Saida)
	if s == nil {
		if op.proximaSaidaEm.IsZero() || !agora.Before(op.proximaSaidaEm) {
			if p := op.viva(dominio.Protecao); p != nil {
				// 1014 recriada pela invariante enquanto a saída não podia sair: com preço, cancela a 1014
				// e sai (Parte 3.5); sem preço, a posição continua protegida e tenta de novo depois
				if p.pend != semPendencia || p.Estado == dominio.Enviando || p.Estado == dominio.Pendente || p.Estado == dominio.SemConfirmacao {
					return
				}
				if _, _, _, ok := m.precoSaida(op, dominio.ParaZerar(op.posicao()), 1); !ok {
					op.proximaSaidaEm = agora.Add(m.p.TimeoutSaida)
					return
				}
				m.mudar(op, dominio.CancelandoProtecao)
				op.cancelProtEm = agora
				m.cancelar(p)
				return
			}
			for _, o := range op.ordens {
				if o.Papel == dominio.Saida && o.viva() {
					return
				}
			}
			op.proximaSaidaEm = agora
			m.enviarSaida(op)
		}
		return
	}
	if !s.recusaTerminalEm.IsZero() {
		m.esperarFimDaSaida(op, s, agora)
		return
	}
	if s.pend != semPendencia || s.Estado == dominio.Enviando || s.Estado == dominio.SemConfirmacao || !s.emErroDesde.IsZero() {
		return // em erro (F14): não reapregoa; tratarErros cancela se não voltar
	}
	if op.proximoRepreco.IsZero() || agora.Before(op.proximoRepreco) {
		return
	}
	if op.saidaDivergente || m.divergenteOp(op) {
		// B5.c: sem repreço; decisão humana. A saída parada é cancelada para a 1014 poder voltar
		// (invariante de proteção, v1.1 P5) sem risco de duas saídas executarem.
		m.al.Emitir("SAIDA_DIVERGENTE_NAO_EXECUTOU", chaveOp("SAIDA_DIVERGENTE", op),
			fmt.Sprintf("saída da operação %d com ticker divergente não executou: cancelada, 1014 será recriada; decisão humana", op.OperacaoID), op.OperacaoID)
		op.congelada = true
		m.cancelar(s)
		return
	}
	if m.emLeilao(op) {
		// v1.18: em leilão a saída fica no limite largo (uma troca, se ela veio de antes do leilão);
		// sem repreço nem contagem até o status84 voltar a 0
		op.proximoRepreco = agora.Add(m.intervaloRepreco(op))
		if px, _, _, ok := m.precoSaida(op, s.Lado, 1); ok && (!op.saidaEmLeilao || px != s.Preco) {
			if !op.saidaEmLeilao {
				m.avisarSaidaEmLeilao(op, px)
			}
			if px != s.Preco {
				m.replace(s, s.QtdExec+abs(op.posicao()), px, 0, 0)
			}
		}
		return
	}
	if op.saidaEmLeilao {
		op.saidaEmLeilao = false
		m.al.Resolver(chaveOp("SAIDA_EM_LEILAO", op))
	}
	op.repreco++
	if op.repreco > m.p.MaxRepreco {
		m.al.Emitir("SAIDA_ESGOTOU", chaveOp("SAIDA_ESGOTOU", op),
			fmt.Sprintf("saída da operação %d esgotou %d repreços; continua reapregoando", op.OperacaoID, m.p.MaxRepreco), op.OperacaoID)
	}
	k := op.repreco
	if k > m.p.MaxRepreco {
		k = m.p.MaxRepreco
	}
	op.proximoRepreco = agora.Add(m.intervaloRepreco(op))
	px, _, _, ok := m.precoSaida(op, s.Lado, k+1)
	if !ok {
		m.al.Emitir("SAIDA_SEM_PRECO", chaveOp("SAIDA_SEM_PRECO", op), fmt.Sprintf("sem preço para reapregoar a saída da operação %d", op.OperacaoID), op.OperacaoID)
		return
	}
	m.replace(s, s.QtdExec+abs(op.posicao()), px, 0, 0)
}

// ---------------------------------------------------------------- cancel e replace (um por vez)

func (m *Motor) cancelar(o *ordem) {
	if !o.viva() || o.pend == pendCancel {
		return
	}
	if o.pend == pendReplace {
		o.cancelarApos = true
		return
	}
	op := m.donoDa[o]
	if m.retirarDaFila(o) {
		// a NewOrderSingle nunca saiu (esperava a taxa ou estava nesta volta): não há o que cancelar
		// no PnT; encerra aqui
		o.Estado = dominio.Cancelada
		m.salvarOrdem(o)
		slog.Info("ordem retirada da fila de envio (nunca foi ao PnT)", "clordid", o.ClOrdAtual)
		if o.Papel == dominio.Entrada {
			m.encerrarEntrada(op, "CANCELADA", true)
		} else {
			m.ordemEncerrada(op, o, "CANCELADA")
		}
		return
	}
	cl := op.clordid(o.Papel, op.proximoN(o.Papel))
	o.cancelamentos++
	o.ultimoCancel = m.agora()
	o.pend, o.clPend, o.pendDesde = pendCancel, cl, m.agora()
	m.porClOrd[cl] = o
	msg := pnt.Cancelamento(m.idDe(op), cl, o.ClOrdAtual, o.OrderID, op.Simbolo, o.Lado.FIX(), o.Qtd, m.agora())
	m.salvarOp(op)
	m.enfileirarEnvio(envioPendente{o: o, msg: msg, clordid: cl, prio: prioSaida,
		aoEnviar: func() { o.pendDesde, o.ultimoCancel = m.agora(), m.agora() },
		aoFalhar: func() {
			if o.clPend == cl {
				o.pend, o.clPend = semPendencia, ""
			}
		}})
	slog.Info("CANCEL decidido", "ordem", o.ClOrdAtual, "clordid", cl)
}

func (m *Motor) replace(o *ordem, qtd int, px, stop, stopLim int64) {
	if !o.viva() || o.pend != semPendencia {
		return
	}
	op := m.donoDa[o]
	cl := op.clordid(o.Papel, op.proximoN(o.Papel))
	o.pend, o.clPend, o.pendDesde = pendReplace, cl, m.agora()
	o.qtdPend, o.precoPend, o.stopPend, o.stopLimPend = qtd, px, stop, stopLim
	m.porClOrd[cl] = o
	if o.Papel == dominio.Protecao {
		o.replaces++
		m.avisarReservaReplaces(op, o)
	}
	novo := pnt.Ordem{ClOrdID: cl, Simbolo: op.Simbolo, Lado: o.Lado.FIX(), Qtd: qtd, Preco: px, Estrategia: o.Estrategia, Stop: stop, StopLim: stopLim,
		IgnorarTunel: m.ignorarTunel(o)}
	msg := pnt.Replace(m.idDe(op), novo, o.ClOrdAtual, o.OrderID, m.agora())
	m.salvarOp(op)
	prio := prioSaida
	if o.Papel == dominio.Protecao {
		prio = prioProtecao
	}
	m.enfileirarEnvio(envioPendente{o: o, msg: msg, clordid: cl, prio: prio,
		aoEnviar: func() { o.pendDesde = m.agora() },
		aoFalhar: func() {
			if o.Papel == dominio.Protecao {
				o.replaces-- // não saiu
			}
			if o.clPend == cl {
				limparPendencia(o)
			}
		}})
	slog.Info("REPLACE decidido", "ordem", o.ClOrdAtual, "clordid", cl, "qtd", qtd, "preco", px, "stop", stop)
}

// avisarReservaReplaces (v1.13): a 1014 chegou à reserva (REPLACES_1014_MAX − REPLACES_1014_RESERVA):
// daqui em diante o ajuste do gestor é recusado (PROTECAO_LIMITE_REPLACES) e o trailing segue pelo
// TRSP do gestor (a saída por replace cabe na reserva).
func (m *Motor) avisarReservaReplaces(op *operacao, o *ordem) {
	if o.reservaAvisada || o.replaces < m.p.Replaces1014Max-m.p.Replaces1014Reserva {
		return
	}
	o.reservaAvisada = true
	m.publicar("PROTECAO_RESERVA_REPLACES", op, map[string]any{"clordid": o.ClOrdAtual, "replaces": o.replaces, "max": m.p.Replaces1014Max})
	m.al.Emitir("PROTECAO_RESERVA_REPLACES", "", fmt.Sprintf("1014 %s com %d de %d replaces: ajustes do stop recusados daqui em diante (a saída por replace segue na reserva), operação %d",
		o.ClOrdAtual, o.replaces, m.p.Replaces1014Max, op.OperacaoID), op.OperacaoID)
}

func limparPendencia(o *ordem) {
	o.pend, o.clPend, o.cancelPendenteEm = semPendencia, "", timeZero
	o.qtdPend, o.precoPend, o.stopPend, o.stopLimPend = 0, 0, 0, 0
	o.origemPend = ""
}

// conferirConta (v1.2 U4): a configuração do usuário no GQ mudou com posição aberta? A posição
// segue pela conta gravada na entrada; o alerta é crítico e se resolve ao zerar ou se a
// configuração voltar. Leitura ilegível do acusuario não conta como mudança.
func (m *Motor) conferirConta(op *operacao) {
	if op.Conta == "" || op.Estado.Final() || op.posicao() == 0 {
		return
	}
	r, f := m.contaDoUsuario(op.UID)
	if f != nil && f.Codigo == "CONFIG_USUARIO_INDISPONIVEL" {
		return
	}
	chave := chaveOp("CONTA_ALTERADA", op)
	if f == nil && r.Conta == op.Conta && r.Destino == op.Destino && r.Conector == op.conector() {
		m.al.Resolver(chave)
		return
	}
	agora := fmt.Sprintf("%s (destino %d, conector %s)", r.Conta, r.Destino, r.Conector)
	if f != nil {
		agora = f.Codigo + " (" + f.Motivo + ")"
	}
	m.al.Emitir("CONTA_ALTERADA_COM_POSICAO", chave,
		fmt.Sprintf("a configuração do uid %d no GQ mudou com posição %d aberta (operação %d): agora %s; a posição segue pela conta %s (destino %d, conector %s)",
			op.UID, op.posicao(), op.OperacaoID, agora, op.Conta, op.Destino, op.conector()), op.OperacaoID)
}
