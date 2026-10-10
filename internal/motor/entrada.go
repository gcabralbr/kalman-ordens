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

// pedirEntrada: precedência de docs/ALERTAS.md — uid → campos → DUPLICADA → PARADA → DIVERGENTE →
// SEM_SESSAO → SEM_CONFIRMACAO → CONCILIANDO → SIMBOLO_DIVERGENTE → limites → preço.
func (m *Motor) pedirEntrada(p Pedido) (Resposta, *recusa) {
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
	if p.Qtd == nil || *p.Qtd <= 0 {
		return nil, simples("CAMPO_INVALIDO", "qtd inválida")
	}
	if p.StopPts == nil || *p.StopPts <= 0 {
		return nil, simples("SEM_STOP", "stop_pts ausente na ENTRADA: sem stop, não há ordem")
	}
	if m.porOp[*p.OperacaoID] != nil {
		// a idempotência (repetido) já tratou o reenvio; aqui só chega operação sem pedido guardado
		return nil, simples("DUPLICADA", "operação já recebida")
	}
	if existe, _ := m.repo.OperacaoExiste(*p.OperacaoID); existe {
		return nil, simples("DUPLICADA", "operação já recebida (outro dia)")
	}
	if m.parada != nil {
		return nil, simples("PARADA", "serviço em parada "+m.parada.Modo+": "+m.parada.Motivo)
	}
	if m.divergente {
		return nil, simples("DIVERGENTE", "estado divergente; requer retomada manual")
	}
	if m.fix == nil || !m.fix.Logado() {
		return nil, simples("SEM_SESSAO", "sessão FIX não logada")
	}
	if m.haSemConfirmacao() {
		return nil, simples("SEM_CONFIRMACAO", "há ordem sem confirmação do PnT")
	}
	if m.conciliando {
		return nil, simples("CONCILIANDO", "conciliação em andamento")
	}
	l := m.precos.Ler()
	if l.Situacao == preco.OK && l.Ticker != m.p.Simbolo {
		return nil, simples("SIMBOLO_DIVERGENTE", fmt.Sprintf("marketdata em %s, serviço em %s", l.Ticker, m.p.Simbolo))
	}
	if r := m.verificarLimites(*p.Qtd, l); r != nil {
		return nil, r
	}
	if r := m.verificarPrecoEntrada(l); r != nil {
		return nil, r
	}

	agora := m.agora()
	op := &operacao{Operacao: dominio.Operacao{
		Data: m.dia, OperacaoID: *p.OperacaoID, UID: *p.UID, Lado: lado, QtdPedida: *p.Qtd, StopPts: *p.StopPts,
		Estado: dominio.Entrando, MotivoEntrada: p.Motivo, TsDecisao: p.TsDecisao, CriadaEm: agora,
		ContadorN: map[string]int{},
	}}
	m.salvarOp(op)
	m.ops = append(m.ops, op)
	m.porOp[op.OperacaoID] = op

	var ref decimal.Decimal
	if lado == dominio.Compra {
		ref = l.Ask.Add(decimal.NewFromInt(m.p.ColarEntrada))
	} else {
		ref = l.Bid.Sub(decimal.NewFromInt(m.p.ColarEntrada))
	}
	o := m.novaOrdem(op, dominio.Entrada, lado, *p.Qtd, pnt.EstrategiaAgendada)
	o.Preco = m.agressivo(lado, ref)
	o.MotivoGestor = p.Motivo
	m.salvarOrdem(o)
	m.indexar(op, o)

	m.ultimaEntrada = agora
	m.entradasRec = append(m.entradasRec, agora)
	m.entradasHoje++

	if !m.enviarNova(op, o) {
		o.Estado = dominio.FalhaEnvio
		m.salvarOrdem(o)
		op.Estado = dominio.EncerradaSemEntrada
		op.EncerrouEntrada = true
		m.salvarOp(op)
		return nil, simples("ENVIO_FALHOU", "a ordem não saiu para o PnT")
	}
	slog.Info("ENTRADA enviada", "clordid", o.ClOrdAtual, "preco", o.Preco)
	return Resposta{"aceito": true, "operacao_id": op.OperacaoID, "clordid": o.ClOrdAtual, "preco": o.Preco}, nil
}

func (m *Motor) verificarUID(p Pedido) *recusa {
	if m.p.UIDReal == 0 || p.UID == nil || *p.UID != m.p.UIDReal {
		uid := "?"
		if p.UID != nil {
			uid = fmt.Sprint(*p.UID)
		}
		return simples("UID_NAO_HABILITADO", "uid "+uid+" não habilitado para ordens reais")
	}
	return nil
}

func (m *Motor) verificarLimites(qtd int, l preco.Leitura) *recusa {
	agora := m.agora()
	h := m.horaDoDia()
	if h < m.p.JanelaInicio || h >= m.p.JanelaFim {
		return limite("FORA_JANELA", "fora da janela de entradas")
	}
	if qtd > m.p.QtdMaxOrdem {
		return limite("QTD_MAXIMA", fmt.Sprintf("qtd %d > %d", qtd, m.p.QtdMaxOrdem))
	}
	comprometido := abs(m.posicaoTotal())
	for _, op := range m.ops {
		if e := op.viva(dominio.Entrada); e != nil {
			comprometido += e.Qtd - e.QtdExec
		}
	}
	if comprometido+qtd > m.p.PosicaoMax {
		return limite("POSICAO_MAXIMA", fmt.Sprintf("posição + entradas pendentes = %d, máximo %d", comprometido, m.p.PosicaoMax))
	}
	if !m.ultimaEntrada.IsZero() && agora.Sub(m.ultimaEntrada) < m.p.IntervaloEntradas {
		return limite("INTERVALO_MINIMO", fmt.Sprintf("última entrada há %s", agora.Sub(m.ultimaEntrada)))
	}
	m.entradasRec = aparar(m.entradasRec, agora.Add(-time.Minute))
	if len(m.entradasRec) >= m.p.EntradasPorMinuto {
		return limite("ENTRADAS_POR_MINUTO", "limite de entradas por minuto")
	}
	m.enviosRec = aparar(m.enviosRec, agora.Add(-time.Minute))
	if len(m.enviosRec) >= m.p.MensagensPorMinuto {
		m.al.Emitir("LIMITE", "", fmt.Sprintf("limite de mensagens por minuto (%d)", len(m.enviosRec)), 0)
		return limite("ORDENS_POR_MINUTO", "limite de mensagens por minuto")
	}
	if m.entradasHoje >= m.p.EntradasPorDia {
		m.al.Emitir("LIMITE", "", fmt.Sprintf("limite de entradas por dia (%d)", m.entradasHoje), 0)
		return limite("ENTRADAS_POR_DIA", "limite de entradas por dia")
	}
	if ultimo := m.precoConfiavel(l); ultimo != nil {
		r := m.resultadoDoDia(ultimo)
		if r.LessThanOrEqual(decimal.NewFromInt(-m.p.PerdaDiaPts)) {
			m.ativarParada("BLOQUEAR", "auto", "perda do dia "+r.String()+" pts")
			return limite("PERDA_DIARIA", "perda do dia "+r.String()+" pts")
		}
	}
	return nil
}

// verificarPrecoEntrada (SPEC v1.1 B1), na ordem de docs/referencia/precos-atual.md.
func (m *Motor) verificarPrecoEntrada(l preco.Leitura) *recusa {
	if l.Situacao != preco.OK {
		return limite("SEM_PRECO", "sem preço do marketdata")
	}
	if l.Status84 != 0 {
		return limite("LEILAO", fmt.Sprintf("status84 = %d", l.Status84))
	}
	if l.Preco == nil || l.Bid == nil || l.Ask == nil {
		return limite("SEM_PRECO", "falta preco, bid ou ask")
	}
	if l.Idade(m.agora()) > m.p.IdadeMaxPreco {
		return limite("PRECO_VELHO", fmt.Sprintf("idade %s", l.Idade(m.agora())))
	}
	if !l.Bid.LessThan(*l.Ask) {
		return limite("BOOK_INVALIDO", "bid >= ask")
	}
	meio := l.Bid.Add(*l.Ask).Div(decimal.NewFromInt(2))
	if meio.Sub(*l.Preco).Abs().GreaterThan(decimal.NewFromInt(m.p.BookDescoladoPts)) {
		return limite("BOOK_DESCOLADO", "|meio − preco| = "+meio.Sub(*l.Preco).Abs().String())
	}
	return nil
}

// precoConfiavel: o preco do símbolo certo, fresco; nil se não houver.
func (m *Motor) precoConfiavel(l preco.Leitura) *decimal.Decimal {
	if l.Situacao != preco.OK || l.Ticker != m.p.Simbolo || l.Preco == nil || l.Idade(m.agora()) > m.p.IdadeMaxPreco {
		return nil
	}
	return l.Preco
}

func (m *Motor) recusar(p Pedido, r *recusa) Resposta {
	op := "?"
	if p.OperacaoID != nil {
		op = fmt.Sprint(*p.OperacaoID)
	}
	slog.Info("pedido recusado", "papel", p.Papel, "operacao", op, "codigo", r.codigo, "motivo", r.motivo)
	if r.porLimite {
		agora := m.agora()
		m.recusasLimite = append(aparar(m.recusasLimite, agora.Add(-time.Minute)), agora)
		if len(m.recusasLimite) >= m.p.RecusasParaParada && m.parada == nil {
			m.ativarParada("BLOQUEAR", "auto", fmt.Sprintf("%d recusas por limite em 1 min (última: %s)", len(m.recusasLimite), r.codigo))
		}
	}
	return Resposta{"aceito": false, "codigo": r.codigo, "motivo": r.motivo}
}

func (m *Motor) novaOrdem(op *operacao, papel dominio.Papel, lado dominio.Lado, qtd, estrategia int) *ordem {
	cl := op.clordid(papel, op.proximoN(papel))
	m.seqOrdens++
	return &ordem{seq: m.seqOrdens, Ordem: dominio.Ordem{
		Data: op.Data, UID: op.UID, OperacaoID: op.OperacaoID, Papel: papel, ClOrdBase: cl, ClOrdAtual: cl,
		Estrategia: estrategia, Lado: lado, Qtd: qtd, Estado: dominio.Enviando, CriadaEm: m.agora(),
	}}
}

// enviarNova manda o NewOrderSingle de uma ordem já gravada (gravar antes de enviar).
func (m *Motor) enviarNova(op *operacao, o *ordem) bool {
	msg := pnt.NovaOrdem(m.id, pnt.Ordem{
		ClOrdID: o.ClOrdAtual, Simbolo: m.p.Simbolo, Lado: o.Lado.FIX(), Qtd: o.Qtd, Preco: o.Preco,
		Estrategia: o.Estrategia, Stop: o.Stop, StopLim: o.StopLim,
	}, m.agora())
	m.salvarOp(op)
	if !m.enviar(o, msg, o.ClOrdAtual) {
		return false
	}
	o.enviada = m.agora()
	return true
}

func (m *Motor) haSemConfirmacao() bool {
	for _, op := range m.ops {
		for _, o := range op.ordens {
			if o.Estado == dominio.SemConfirmacao {
				return true
			}
		}
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
