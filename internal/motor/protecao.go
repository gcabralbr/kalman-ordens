package motor

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// ajustarProtecao cria a 1014 ou faz o replace para a posição atual (Parte 3.4).
func (m *Motor) ajustarProtecao(op *operacao) {
	if op.Estado.Saindo() || op.Estado.Final() || op.saidaAposEntrada {
		return
	}
	pos := op.posicao()
	if pos == 0 {
		return
	}
	agora := m.agora()
	if !op.retentativaProtEm.IsZero() && agora.Before(op.retentativaProtEm) {
		return
	}
	op.retentativaProtEm = timeZero
	lado := dominio.ParaZerar(pos)
	qtd := abs(pos)
	// comprado: stop de venda abaixo; gatilho arredonda para cima (mais perto), limite para baixo
	stop, lim := m.calcularStop(op, lado)
	p := op.viva(dominio.Protecao)
	if p == nil {
		if op.semProtecaoDesde.IsZero() {
			op.semProtecaoDesde = agora
		}
		o := m.novaOrdem(op, dominio.Protecao, lado, qtd, pnt.EstrategiaStop)
		o.Stop, o.StopLim, o.Preco = stop, lim, lim
		m.salvarOrdem(o)
		m.indexar(op, o)
		if !m.enviarNova(op, o) {
			o.Estado = dominio.FalhaEnvio
			m.salvarOrdem(o)
			op.retentativaProtEm = agora.Add(m.p.RetentativaProtecao)
			return
		}
		slog.Info("PROTEÇÃO enviada", "clordid", o.ClOrdAtual, "stop", stop, "limite", lim, "qtd", qtd)
		return
	}
	if p.pend != semPendencia || p.Estado == dominio.Enviando || p.Estado == dominio.SemConfirmacao || p.Estado == dominio.Pendente {
		// um pedido por vez: manda só o estado final quando o atual responder
		op.protecaoDesatualizada = true
		return
	}
	op.protecaoDesatualizada = false
	if p.Qtd != qtd || p.Stop != stop || p.Lado != lado {
		m.replace(p, qtd, lim, stop, lim)
	}
}

func (m *Motor) protecaoAceita(op *operacao, p *ordem) {
	op.semProtecaoDesde = timeZero
	m.al.Resolver(chaveOp("SEM_PROTECAO", op))
	m.al.Resolver(chaveOp("PROTECAO_RECRIADA", op))
	if op.Estado == dominio.PosicionadaSemProtecao || op.Estado == dominio.Entrando {
		m.mudar(op, dominio.Protegida)
	}
	m.publicar("PROTEGIDA", op, map[string]any{"preco_stop": p.Stop, "preco_limite": p.StopLim, "qtd": p.Qtd})
	if op.zerarAposProtecao {
		// ENTRADA_TARDIA (A3): protegida, agora zera
		op.zerarAposProtecao = false
		m.iniciarSaida(op, "ENTRADA_TARDIA", "ENTRADA_TARDIA")
		return
	}
	if op.protecaoDesatualizada {
		m.ajustarProtecao(op)
	}
}

func (m *Motor) protecaoRejeitada(op *operacao, texto string) {
	if p := op.ultimaProtecao(); p != nil && p.recriada && !op.zerarAposProtecao {
		m.falhaProtecao(op, "1014 recriada rejeitada ("+texto+")")
		return
	}
	if op.zerarAposProtecao {
		op.zerarAposProtecao = false
		m.iniciarSaida(op, "ENTRADA_TARDIA", "ENTRADA_TARDIA")
		return
	}
	op.rejeicoesProtecao++
	if op.rejeicoesProtecao < 2 {
		m.al.Emitir("PROTECAO_REJEITADA", "", fmt.Sprintf("1014 rejeitada (%s), nova tentativa: operação %d", texto, op.OperacaoID), op.OperacaoID)
		op.retentativaProtEm = m.agora().Add(m.p.RetentativaProtecao)
		return
	}
	m.al.Emitir("PROTECAO_REJEITADA", "", fmt.Sprintf("1014 rejeitada de novo (%s): zerando a operação %d", texto, op.OperacaoID), op.OperacaoID)
	m.iniciarSaida(op, "PROTECAO_REJEITADA", "PROTECAO_REJEITADA")
}

// verificarInvariante (v1.1 P5): posição ≠ 0 e nenhuma ordem de saída viva ⇒ 1014 viva.
// Verificada a cada 1 s; violada por mais de 2 s → recria a 1014 + alerta crítico PROTECAO_RECRIADA.
// Pega qualquer caminho que deixe a posição sem proteção, previsto ou não.
func (m *Motor) verificarInvariante(agora time.Time) {
	for _, op := range m.ops {
		violada := !op.Estado.Final() && op.posicao() != 0 && op.viva(dominio.Saida) == nil && op.viva(dominio.Protecao) == nil
		switch {
		case !violada:
			op.violacaoDesde = timeZero
		case op.violacaoDesde.IsZero():
			op.violacaoDesde = agora
		case agora.Sub(op.violacaoDesde) > 2*time.Second:
			op.violacaoDesde = timeZero
			m.falhaProtecao(op, "posição sem 1014 e sem saída viva há mais de 2 s")
		}
	}
}

// janelaRecriacao: uma 1014 recriada que dura isto zera o contador de tentativas (P8).
const janelaRecriacao = 30 * time.Second

// falhaProtecao (P8): a 1014 caiu sem pedido nosso (cancelada/expirada pelo PnT), a recriada foi
// rejeitada, ou a invariante achou a posição sem proteção. Recria na hora; até 2 recriações com
// menos de 30 s entre a falha e a recriação anterior; a terceira falha → PROTECAO_INSTAVEL e zera a
// posição (como a 3.4: posição sem proteção possível não fica aberta). Sem laço cria/cancela.
func (m *Motor) falhaProtecao(op *operacao, motivo string) {
	agora := m.agora()
	if !op.ultimaRecriacao.IsZero() && agora.Sub(op.ultimaRecriacao) >= janelaRecriacao {
		op.recriacoes = 0
	}
	if op.recriacoes >= 2 {
		op.recriacoes = 0
		m.al.Emitir("PROTECAO_INSTAVEL", chaveOp("PROTECAO_INSTAVEL", op),
			fmt.Sprintf("1014 instável (%s depois de 2 recriações em menos de %s): zerando a operação %d", motivo, janelaRecriacao, op.OperacaoID), op.OperacaoID)
		m.al.Resolver(chaveOp("PROTECAO_RECRIADA", op))
		m.iniciarSaida(op, "PROTECAO_INSTAVEL", "PROTECAO_INSTAVEL")
		return
	}
	op.recriacoes++
	op.ultimaRecriacao = agora
	m.recriarProtecao(op, motivo)
}

// recriarProtecao manda uma 1014 nova com o mesmo gatilho e limite da última (não depende do
// feed); sem 1014 anterior, calcula sobre o preço médio da entrada.
func (m *Motor) recriarProtecao(op *operacao, motivo string) {
	pos := op.posicao()
	lado := dominio.ParaZerar(pos)
	var ultima *ordem
	for _, o := range op.ordens {
		if o.Papel == dominio.Protecao && o.Stop != 0 && o.Lado == lado {
			ultima = o
		}
	}
	var stop, lim int64
	if ultima != nil {
		stop, lim = ultima.Stop, ultima.StopLim
	} else {
		stop, lim = m.calcularStop(op, lado)
	}
	o := m.novaOrdem(op, dominio.Protecao, lado, abs(pos), pnt.EstrategiaStop)
	o.Stop, o.StopLim, o.Preco = stop, lim, lim
	o.recriada = true
	m.salvarOrdem(o)
	m.indexar(op, o)
	m.al.Resolver(chaveOp("PROTECAO_RECRIADA", op)) // uma recriação nova reabre o alerta
	m.al.Emitir("PROTECAO_RECRIADA", chaveOp("PROTECAO_RECRIADA", op),
		fmt.Sprintf("%s: 1014 recriada (tentativa %d; gatilho %d, limite %d), posição %d, operação %d", motivo, op.recriacoes, stop, lim, pos, op.OperacaoID), op.OperacaoID)
	if !m.enviarNova(op, o) {
		o.Estado = dominio.FalhaEnvio
		m.salvarOrdem(o)
		return
	}
	slog.Info("PROTEÇÃO recriada", "clordid", o.ClOrdAtual, "stop", stop, "limite", lim, "qtd", o.Qtd)
}

// calcularStop: gatilho = médio ∓ stop_pts (arredondado para mais perto), limite = gatilho ∓ folga.
func (m *Motor) calcularStop(op *operacao, lado dominio.Lado) (stop, lim int64) {
	medio, _ := op.medio(true)
	stopPts := decimal.NewFromInt(op.StopPts)
	folga := decimal.NewFromInt(m.p.FolgaStop)
	if lado == dominio.Venda {
		stop = m.acima(medio.Sub(stopPts))
		return stop, m.abaixo(decimal.NewFromInt(stop).Sub(folga))
	}
	stop = m.abaixo(medio.Add(stopPts))
	return stop, m.acima(decimal.NewFromInt(stop).Add(folga))
}

// ultimaProtecao: a 1014 mais recente da operação (viva ou não).
func (op *operacao) ultimaProtecao() *ordem {
	var r *ordem
	for _, o := range op.ordens {
		if o.Papel == dominio.Protecao {
			r = o
		}
	}
	return r
}
