package motor

import (
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/pnt"
)

func (m *Motor) pedirSaida(p Pedido) (Resposta, *recusa) {
	if r := m.verificarUID(p); r != nil {
		return nil, r
	}
	if p.OperacaoID == nil {
		return nil, simples("CAMPO_INVALIDO", "operacao_id ausente")
	}
	lado, ok := dominio.LadoDaLetra(p.Lado)
	if !ok {
		return nil, simples("CAMPO_INVALIDO", "lado deve ser C ou V")
	}
	op := m.porOp[*p.OperacaoID]
	if op == nil || op.Estado.Final() {
		if op == nil || op.OrigemSaida != "STOP_SERVIDOR" {
			m.al.Emitir("SEM_POSICAO", "", fmt.Sprintf("SAIDA sem posição: operação %d", *p.OperacaoID), *p.OperacaoID)
		}
		return nil, simples("SEM_POSICAO", "operação sem posição real")
	}
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
	if m.simboloDiv && !m.ultimoCertoValido() {
		m.al.Emitir("SAIDA_BLOQUEADA_PRECO_ANTIGO", chaveOp("SAIDA_BLOQUEADA", op),
			fmt.Sprintf("saída da operação %d não enviada: ticker divergente e último preço de %s com mais de %s", op.OperacaoID, m.p.Simbolo, m.p.IdadeMaxPrecoDivergente), op.OperacaoID)
		return nil, simples("SIMBOLO_DIVERGENTE", "ticker divergente e sem preço recente do símbolo certo")
	}
	if p.Qtd != nil && *p.Qtd < abs(pos) {
		slog.Warn("saída parcial tratada como total na v1", "op", op.OperacaoID, "qtd", *p.Qtd, "posicao", pos)
	}
	m.iniciarSaida(op, p.Motivo, "GESTOR")
	return Resposta{"aceito": true, "operacao_id": op.OperacaoID, "posicao": op.posicao()}, nil
}

// iniciarSaida começa a Parte 3.5. Idempotente.
func (m *Motor) iniciarSaida(op *operacao, motivo, origem string) {
	if op.Estado.Final() || op.Estado.Saindo() || op.saidaAposEntrada {
		return
	}
	op.MotivoSaida, op.OrigemSaida, op.SaidaPedidaEm = motivo, origem, m.agora()
	if e := op.viva(dominio.Entrada); e != nil {
		m.cancelar(e)
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
	px, divergente, degradado, ok := m.precoSaida(lado, k)
	if !ok {
		if divergente {
			m.al.Emitir("SAIDA_BLOQUEADA_PRECO_ANTIGO", chaveOp("SAIDA_BLOQUEADA", op),
				fmt.Sprintf("saída da operação %d não enviada: ticker divergente e sem preço recente de %s", op.OperacaoID, m.p.Simbolo), op.OperacaoID)
			op.congelada = true
			return
		}
		m.al.Emitir("SAIDA_SEM_PRECO", chaveOp("SAIDA_SEM_PRECO", op), fmt.Sprintf("sem preço para a saída da operação %d", op.OperacaoID), op.OperacaoID)
		op.proximaSaidaEm = m.agora().Add(m.p.TimeoutSaida)
		return
	}
	m.al.Resolver(chaveOp("SAIDA_SEM_PRECO", op))
	if degradado {
		m.al.Emitir("SAIDA_PRECO_DEGRADADO", "", fmt.Sprintf("saída da operação %d com preço velho ou sem bid/ask", op.OperacaoID), op.OperacaoID)
	}
	o := m.novaOrdem(op, dominio.Saida, lado, abs(pos), pnt.EstrategiaAgendada)
	o.Preco = px
	o.MotivoGestor = op.MotivoSaida
	m.salvarOrdem(o)
	m.indexar(op, o)
	op.saidaDivergente = divergente
	op.proximoRepreco = m.agora().Add(m.p.TimeoutSaida)
	if !m.enviarNova(op, o) {
		o.Estado = dominio.FalhaEnvio
		m.salvarOrdem(o)
		op.proximaSaidaEm = m.agora().Add(m.p.TimeoutSaida)
		return
	}
	slog.Info("SAIDA enviada", "clordid", o.ClOrdAtual, "preco", px, "divergente", divergente)
}

// precoSaida: bid/ask ∓ COLAR_SAIDA × k (B2). Sem bid/ask, o preco. Ticker divergente: último
// preco do símbolo certo com o colar máximo (B5.c). ok=false: não há preço utilizável.
func (m *Motor) precoSaida(lado dominio.Lado, k int) (px int64, divergente, degradado, ok bool) {
	if m.simboloDiv {
		if !m.ultimoCertoValido() {
			return 0, true, false, false
		}
		colar := decimal.NewFromInt(m.p.ColarSaida * int64(m.p.MaxRepreco+1))
		if lado == dominio.Venda {
			return m.abaixo(m.ultimoCerto.Sub(colar)), true, false, true
		}
		return m.acima(m.ultimoCerto.Add(colar)), true, false, true
	}
	l := m.precos.Ler()
	if l.Situacao != preco.OK {
		return 0, false, false, false
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

func (m *Motor) ultimoCertoValido() bool {
	return m.temUltimo && m.agora().Sub(m.ultimoCertoT) <= m.p.IdadeMaxPrecoDivergente
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
	m.mudar(op, dominio.Zerada)
	for _, k := range []string{"SEM_PROTECAO", "STOP_FURADO", "SAIDA_ESGOTOU", "CANCEL_SEM_RESPOSTA", "SAIDA_SEM_PRECO", "SAIDA_DIVERGENTE", "SAIDA_BLOQUEADA", "PROTECAO_RECRIADA", "PROTECAO_INSTAVEL", "PROTECAO_EM_ERRO"} {
		m.al.Resolver(chaveOp(k, op))
	}
	ps, _ := op.medio(false)
	m.publicar("ZERADA", op, map[string]any{"preco_medio_saida": ps, "pnl_pts": op.PnL, "origem": op.OrigemSaida, "motivo": op.MotivoSaida})
}

// tickSaida: repreço (A5) e nova saída quando a anterior acabou sem zerar.
func (m *Motor) tickSaida(op *operacao) {
	if op.posicao() == 0 {
		m.zerada(op)
		return
	}
	if op.congelada {
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
				if _, _, _, ok := m.precoSaida(dominio.ParaZerar(op.posicao()), 1); !ok {
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
	if s.pend != semPendencia || s.Estado == dominio.Enviando || s.Estado == dominio.SemConfirmacao || !s.emErroDesde.IsZero() {
		return // em erro (F14): não reapregoa; tratarErros cancela se não voltar
	}
	if op.proximoRepreco.IsZero() || agora.Before(op.proximoRepreco) {
		return
	}
	if op.saidaDivergente || m.simboloDiv {
		// B5.c: sem repreço; decisão humana. A saída parada é cancelada para a 1014 poder voltar
		// (invariante de proteção, v1.1 P5) sem risco de duas saídas executarem.
		m.al.Emitir("SAIDA_DIVERGENTE_NAO_EXECUTOU", chaveOp("SAIDA_DIVERGENTE", op),
			fmt.Sprintf("saída da operação %d com ticker divergente não executou: cancelada, 1014 será recriada; decisão humana", op.OperacaoID), op.OperacaoID)
		op.congelada = true
		m.cancelar(s)
		return
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
	op.proximoRepreco = agora.Add(m.p.TimeoutSaida)
	px, _, _, ok := m.precoSaida(s.Lado, k+1)
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
	cl := op.clordid(o.Papel, op.proximoN(o.Papel))
	o.cancelamentos++
	o.ultimoCancel = m.agora()
	o.pend, o.clPend, o.pendDesde = pendCancel, cl, m.agora()
	m.porClOrd[cl] = o
	msg := pnt.Cancelamento(m.id, cl, o.ClOrdAtual, o.OrderID, m.p.Simbolo, o.Lado.FIX(), o.Qtd, m.agora())
	m.salvarOp(op)
	if !m.enviar(o, msg, cl) {
		o.pend, o.clPend = semPendencia, ""
		return
	}
	slog.Info("CANCEL enviado", "ordem", o.ClOrdAtual, "clordid", cl)
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
	novo := pnt.Ordem{ClOrdID: cl, Simbolo: m.p.Simbolo, Lado: o.Lado.FIX(), Qtd: qtd, Preco: px, Estrategia: o.Estrategia, Stop: stop, StopLim: stopLim}
	msg := pnt.Replace(m.id, novo, o.ClOrdAtual, o.OrderID, m.agora())
	m.salvarOp(op)
	if !m.enviar(o, msg, cl) {
		limparPendencia(o)
		return
	}
	slog.Info("REPLACE enviado", "ordem", o.ClOrdAtual, "clordid", cl, "qtd", qtd, "preco", px, "stop", stop)
}

func limparPendencia(o *ordem) {
	o.pend, o.clPend = semPendencia, ""
	o.qtdPend, o.precoPend, o.stopPend, o.stopLimPend = 0, 0, 0, 0
}
