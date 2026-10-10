package motor

import (
	"fmt"
	"log/slog"

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
	medio, _ := op.medio(true)
	stopPts := decimal.NewFromInt(op.StopPts)
	folga := decimal.NewFromInt(m.p.FolgaStop)
	var stop, lim int64
	if lado == dominio.Venda {
		// comprado: stop de venda abaixo; gatilho arredonda para cima (mais perto), limite para baixo
		stop = m.acima(medio.Sub(stopPts))
		lim = m.abaixo(decimal.NewFromInt(stop).Sub(folga))
	} else {
		stop = m.abaixo(medio.Add(stopPts))
		lim = m.acima(decimal.NewFromInt(stop).Add(folga))
	}
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
