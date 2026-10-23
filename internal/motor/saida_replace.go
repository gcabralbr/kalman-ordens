package motor

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// Saída por replace da 1014 (v1.11, SAIDA_POR_REPLACE). HML 19/10: o cancel da 1014 levou 3,69 s
// até o 39=4 (op 900000005) e o replace de ajuste foi aceito em 468 ms (op 900000007). Em vez de
// cancelar a 1014 e mandar a 1031, a saída que não é por stop troca o gatilho da 1014 para além do
// preço (dispara na hora) com o limite da saída de hoje:
//
//   - venda (sai de compra): gatilho = max(bid, último) + SAIDA_REPLACE_ALEM_PTS; compra: espelhado;
//     limite = bid − COLAR_SAIDA × k / ask + COLAR_SAIDA × k (k = degrau, como o repreço);
//   - 150=5 sem disparo (150=I/39=B) em SAIDA_REPLACE_TIMEOUT_S: replace no degrau seguinte; passado
//     o último (MAX_REPRECO_SAIDA + 1), cancel + nova ordem (o caminho de antes);
//   - disparada sem fill em ESPERA_STOP_S: o caminho do stop furado (cancel; rejeitado → 1031);
//   - replace recusado: com a 1014 disparada/terminal (ou "in time"), espera os fills como no cancel
//     rejeitado (AGUARDANDO_STOP); outro motivo, cancel + nova ordem na hora;
//   - contrato com o Kalman inalterado: estado publicado SAINDO; o fill sai com a origem e o motivo do
//     pedido; sem PROTEGIDA, PROTECAO_AJUSTADA nem PROTECAO_DISPARADA. Eventos novos, que o Kalman
//     ignora: SAIDA_POR_REPLACE (cada replace) e SAIDA_REPLACE_FALHOU (motivo e caminho seguinte).
//
// A marca fica só em memória: depois de um restart, a operação em SAINDO com a 1014 viva e sem 1031
// segue pelo caminho de antes (tickSaida cancela a 1014 e manda a 1031).
type saidaReplace struct {
	degrau    int       // k do limite (1 = o colar da primeira saída)
	enviadoEm time.Time // o replace atual saiu
	aceitoEm  time.Time // 150=5 do replace atual (zero: pendente)
	gatilho   int64
	limite    int64
}

// saidaPorReplacePossivel: a 1014 viva e aceita, sem pedido pendente, não disparada nem em erro,
// cobrindo a posição; sem entrada viva, sem ticker divergente, com o disjuntor fechado.
func (m *Motor) saidaPorReplacePossivel(op *operacao, p *ordem) bool {
	if !m.p.SaidaPorReplace || p == nil || op.furado || op.conector() != dominio.ConectorPnT {
		// a Flexscan recusa o gatilho ultrapassado (sondagem de 23/10, razão 4): saída por cancel + limite
		return false
	}
	if p.Estado != dominio.Ativa || p.pend != semPendencia || p.cancelarApos || !p.disparadaEm.IsZero() ||
		!p.emErroDesde.IsZero() || p.retida || p.QtdExec != 0 || p.replaces >= m.p.Replaces1014Max {
		return false
	}
	pos := op.posicao()
	return pos != 0 && p.Qtd == abs(pos) && p.Lado == dominio.ParaZerar(pos) &&
		op.viva(dominio.Entrada) == nil && !m.divergenteOp(op) && m.bolsa.desde.IsZero()
}

// precoSaidaReplace: gatilho além do preço e limite do degrau k. ok=false sem preço utilizável.
func (m *Motor) precoSaidaReplace(op *operacao, lado dominio.Lado, k int) (gatilho, limite int64, ok bool) {
	l := m.precos.Ler()
	if l.Situacao != preco.OK || l.Ticker != op.Simbolo || l.Idade(m.agora()) > m.p.IdadeMaxPreco {
		return 0, 0, false
	}
	alem := decimal.NewFromInt(m.p.SaidaReplaceAlemPts)
	var ref *decimal.Decimal
	if lado == dominio.Venda {
		ref = maior(l.Bid, l.Preco)
	} else {
		ref = menor(l.Ask, l.Preco)
	}
	if ref == nil {
		return 0, 0, false
	}
	lim, _, degradado, okLim := m.precoSaida(op, lado, k)
	if !okLim || degradado {
		return 0, 0, false
	}
	if lado == dominio.Venda {
		return m.acima(ref.Add(alem)), lim, true
	}
	return m.abaixo(ref.Sub(alem)), lim, true
}

func maior(a, b *decimal.Decimal) *decimal.Decimal {
	switch {
	case a == nil:
		return b
	case b == nil || a.GreaterThan(*b):
		return a
	}
	return b
}

func menor(a, b *decimal.Decimal) *decimal.Decimal {
	switch {
	case a == nil:
		return b
	case b == nil || a.LessThan(*b):
		return a
	}
	return b
}

// iniciarSaidaPorReplace: o primeiro replace. false = não foi possível (segue o caminho de antes).
func (m *Motor) iniciarSaidaPorReplace(op *operacao, p *ordem) bool {
	if !m.saidaPorReplacePossivel(op, p) {
		return false
	}
	g, lim, ok := m.precoSaidaReplace(op, p.Lado, 1)
	if !ok {
		return false
	}
	op.saidaRep = &saidaReplace{degrau: 1}
	m.mudar(op, dominio.Saindo)
	m.replaceDeSaida(op, p, g, lim)
	return true
}

func (m *Motor) replaceDeSaida(op *operacao, p *ordem, gatilho, limite int64) {
	r := op.saidaRep
	r.enviadoEm, r.aceitoEm, r.gatilho, r.limite = m.agora(), timeZero, gatilho, limite
	m.replace(p, p.Qtd, limite, gatilho, limite)
	slog.Info("saída por replace da 1014", "op", op.OperacaoID, "clordid", p.ClOrdAtual, "gatilho", gatilho, "limite", limite, "degrau", r.degrau)
	m.publicar("SAIDA_POR_REPLACE", op, map[string]any{"clordid": p.ClOrdAtual, "preco_stop": gatilho, "preco_limite": limite,
		"degrau": r.degrau, "motivo": op.MotivoSaida})
}

// saidaReplaceFalhou: volta ao caminho de antes (cancel da 1014 + 1031), com o motivo.
func (m *Motor) saidaReplaceFalhou(op *operacao, motivo, seguinte string) {
	op.saidaRep = nil
	slog.Warn("saída por replace falhou", "op", op.OperacaoID, "motivo", motivo, "seguinte", seguinte)
	m.publicar("SAIDA_REPLACE_FALHOU", op, map[string]any{"motivo": motivo, "seguinte": seguinte})
}

// replaceDeSaidaConfirmado: 150=5 do replace de saída.
func (m *Motor) replaceDeSaidaConfirmado(op *operacao) {
	if r := op.saidaRep; r != nil && r.aceitoEm.IsZero() {
		r.aceitoEm = m.agora()
	}
}

// replaceDeSaidaRecusado: 35=9 do replace de saída. A 1014 já disparou ou terminou (ou o texto diz
// que chegou tarde): espera os fills, como no cancel rejeitado. Outro motivo: cancel + 1031 agora.
func (m *Motor) replaceDeSaidaRecusado(op *operacao, p *ordem, msg pnt.Mensagem, texto string) {
	st := msg.Texto(39)
	if strings.Contains(strings.ToLower(texto), "in time") || st == "B" || st == "2" || st == "4" || st == "8" ||
		!p.disparadaEm.IsZero() || !p.viva() {
		m.saidaReplaceFalhou(op, "replace recusado com a 1014 disparada ou terminal: "+texto, "AGUARDANDO_STOP")
		m.mudar(op, dominio.AguardandoStop)
		op.aguardandoStopDesde = m.agora()
		return
	}
	m.saidaReplaceFalhou(op, "replace recusado: "+texto, "CANCEL_E_NOVA")
	m.mudar(op, dominio.CancelandoProtecao)
	op.cancelProtEm = m.agora()
	m.cancelar(p)
}

// tickSaidaReplace: o tempo da saída por replace (dentro de SAINDO).
func (m *Motor) tickSaidaReplace(op *operacao) {
	r := op.saidaRep
	p := op.viva(dominio.Protecao)
	if p == nil {
		// a 1014 acabou sem zerar (cancelada/expirada pelo PnT): segue com a 1031
		m.saidaReplaceFalhou(op, "a 1014 encerrou sem zerar a posição", "NOVA_ORDEM")
		op.proximaSaidaEm = m.agora()
		m.tickSaida(op)
		return
	}
	if p.pend != semPendencia || r.aceitoEm.IsZero() {
		return
	}
	agora := m.agora()
	if !p.disparadaEm.IsZero() {
		if p.QtdExec == p.qtdNoDisparo && agora.Sub(p.disparadaEm) >= m.p.EsperaStop {
			// disparou e não executou: o caminho do stop furado (cancel; se rejeitado, 1031)
			m.saidaReplaceFalhou(op, fmt.Sprintf("1014 disparada há %s sem fill", agora.Sub(p.disparadaEm).Truncate(100*time.Millisecond)), "STOP_FURADO")
			m.al.Emitir("STOP_FURADO", chaveOp("STOP_FURADO", op),
				fmt.Sprintf("saída por replace: a 1014 %s disparou e nenhum fill chegou em %s, posição %d, operação %d", p.ClOrdAtual, m.p.EsperaStop, op.posicao(), op.OperacaoID), op.OperacaoID)
			op.furado = true
			m.mudar(op, dominio.CancelandoProtecao)
			op.cancelProtEm = agora
			m.cancelar(p)
		}
		return
	}
	if agora.Sub(r.aceitoEm) < m.p.SaidaReplaceTimeout {
		return
	}
	if r.degrau > m.p.MaxRepreco || p.replaces >= m.p.Replaces1014Max {
		m.saidaReplaceFalhou(op, fmt.Sprintf("a 1014 não disparou em %d replace(s)", r.degrau), "CANCEL_E_NOVA")
		m.mudar(op, dominio.CancelandoProtecao)
		op.cancelProtEm = agora
		m.cancelar(p)
		return
	}
	g, lim, ok := m.precoSaidaReplace(op, p.Lado, r.degrau+1)
	if !ok {
		m.saidaReplaceFalhou(op, "sem preço para o próximo replace", "CANCEL_E_NOVA")
		m.mudar(op, dominio.CancelandoProtecao)
		op.cancelProtEm = agora
		m.cancelar(p)
		return
	}
	r.degrau++
	m.replaceDeSaida(op, p, g, lim)
}
