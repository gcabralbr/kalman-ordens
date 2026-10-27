package motor

import (
	"fmt"
	"log/slog"
	"regexp"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/preco"
)

// AjusteStop é o corpo do POST /operacoes/{id}/stop (v1.4 K1): o novo stop em preço (preco_stop)
// OU em pontos a partir do preço médio da entrada (stop_pts), um dos dois. Modulo: opcional
// (ausente = KALMAN); uid: opcional, se vier tem de ser o da operação.
type AjusteStop struct {
	Modulo    string   `json:"modulo,omitempty"`
	UID       *int     `json:"uid,omitempty"`
	PrecoStop *float64 `json:"preco_stop,omitempty"`
	StopPts   *int64   `json:"stop_pts,omitempty"`
	// Origem (v1.19): quem moveu o stop (TRSP, STOP_MANUAL...); ausente = GESTOR
	Origem string `json:"origem,omitempty"`
}

// Origem da stop na corretora (v1.19). INICIAL: o stop_pts da entrada; GESTOR: ajuste sem origem.
const (
	OrigemStopInicial = "INICIAL"
	OrigemStopGestor  = "GESTOR"
)

// reOrigemStop: 2 a 20 de A–Z, 0–9 e _, começando por letra.
var reOrigemStop = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,19}$`)

// stopOrigem: a origem da stop confirmada na corretora (vazia, das operações anteriores = INICIAL).
func (op *operacao) stopOrigem() string {
	if op.StopOrigem == "" {
		return OrigemStopInicial
	}
	return op.StopOrigem
}

// origemDoCalculo: a origem do gatilho que o calcularStop dá agora (o ajustado ou o da entrada).
func (op *operacao) origemDoCalculo() string {
	switch {
	case op.StopAjustado == 0:
		return OrigemStopInicial
	case op.StopAjustadoOrigem != "":
		return op.StopAjustadoOrigem
	}
	return OrigemStopGestor // ajuste anterior à v1.19
}

// Situação do ajuste aceito.
const (
	AjusteEnviado    = "ENVIADO"    // 35=G da 1014 enviado agora
	AjusteAguardando = "AGUARDANDO" // há um pedido da 1014 sem resposta: vai ao confirmar (só o último)
	AjusteIgnorado   = "IGNORADO"   // menos de AJUSTE_DEGRAU_MIN_PTS do que está no PnT: nada enviado
)

// AjustarStopAssincrono enfileira o ajuste; o resultado chega no canal (quem desiste de esperar
// não cancela nada, como no POST /ordens).
func (m *Motor) AjustarStopAssincrono(id int64, a AjusteStop) <-chan Resposta {
	ch := make(chan Resposta, 1)
	m.entrarPedido("STOP", func() {
		r, rec := m.ajustarStop(id, a)
		if rec != nil {
			r = m.recusar(Pedido{Modulo: a.Modulo, OperacaoID: &id, Papel: "STOP"}, rec)
		}
		m.aposVolta(func() { ch <- r })
	})
	return ch
}

// ajustarStop (v1.4 K1): recalcula gatilho e limite (mesma folga) e faz o replace da 1014. O
// último pedido vale (fica na operação); um replace por vez — com um pedido da 1014 sem resposta,
// sai só o último quando ele responder; menos de AJUSTE_DEGRAU_MIN_PTS do gatilho que está no
// PnT, nada sai.
func (m *Motor) ajustarStop(id int64, a AjusteStop) (Resposta, *recusa) {
	if a.Modulo != "" && !dominio.ModuloValido(a.Modulo) {
		return nil, simples("CAMPO_INVALIDO", fmt.Sprintf("modulo %q inválido (2 a 8 letras maiúsculas e dígitos)", a.Modulo))
	}
	if (a.PrecoStop == nil) == (a.StopPts == nil) {
		return nil, simples("CAMPO_INVALIDO", "informe preco_stop ou stop_pts (um dos dois)")
	}
	origem := a.Origem
	switch {
	case origem == "":
		origem = OrigemStopGestor
	case origem == OrigemStopInicial:
		return nil, simples("CAMPO_INVALIDO", "origem INICIAL e do servico")
	case !reOrigemStop.MatchString(origem):
		return nil, simples("CAMPO_INVALIDO", "origem invalida (2 a 20 de A-Z, 0-9 e _, comecando por letra)")
	}
	op := m.operacaoDo(Pedido{Modulo: a.Modulo, OperacaoID: &id})
	if op == nil || op.Estado.Final() || op.posicao() == 0 {
		return nil, simples("SEM_POSICAO", "operação sem posição real")
	}
	if a.UID != nil && *a.UID != op.UID {
		return nil, simples("CAMPO_INVALIDO", fmt.Sprintf("uid %d diferente do da operação (%d)", *a.UID, op.UID))
	}
	if op.Estado.Saindo() || !op.SaidaPedidaEm.IsZero() || op.saidaAposEntrada {
		return nil, simples("SEM_PROTECAO", "operação em saída")
	}
	p := op.viva(dominio.Protecao)
	if p == nil {
		return nil, simples("SEM_PROTECAO", "sem 1014 viva")
	}
	if !p.disparadaEm.IsZero() {
		return nil, simples("PROTECAO_DISPARADA", "a 1014 já disparou no PnT")
	}
	if p.replaces >= m.p.Replaces1014Max-m.p.Replaces1014Reserva {
		// v1.13: a reserva fica para a saída por replace; o trailing segue pelo TRSP do gestor
		return nil, simples("PROTECAO_LIMITE_REPLACES", fmt.Sprintf("a 1014 já teve %d de %d replaces (reserva de %d para a saída)",
			p.replaces, m.p.Replaces1014Max, m.p.Replaces1014Reserva))
	}
	pos := op.posicao()
	lado := dominio.ParaZerar(pos)
	var stop int64
	if a.PrecoStop != nil {
		if *a.PrecoStop <= 0 {
			return nil, simples("CAMPO_INVALIDO", "preco_stop deve ser positivo")
		}
		// ao tick, para o lado mais perto do mercado (como o gatilho calculado)
		if lado == dominio.Venda {
			stop = m.acima(decimal.NewFromFloat(*a.PrecoStop))
		} else {
			stop = m.abaixo(decimal.NewFromFloat(*a.PrecoStop))
		}
	} else {
		stop = m.gatilhoPorPontos(op, lado, *a.StopPts)
	}
	l := m.precosDo(op.conector()).Ler()
	if l.Situacao != preco.OK || l.Ticker != op.Simbolo || l.Bid == nil || l.Ask == nil || l.Idade(m.agora()) > m.p.IdadeMaxPreco {
		return nil, simples("SEM_PRECO", "sem bid/ask recente de "+op.Simbolo+" para validar o stop")
	}
	// comprado: o stop de venda tem de ficar abaixo do bid; vendido: o de compra, acima do ask.
	// Além disso a 1014 dispararia na hora: quem sai, nesse caso, é o gestor.
	if lado == dominio.Venda && !decimal.NewFromInt(stop).LessThan(*l.Bid) {
		return nil, simples("STOP_ALEM_DO_PRECO", fmt.Sprintf("stop %d não está abaixo do bid %s (posição comprada)", stop, l.Bid))
	}
	if lado == dominio.Compra && !decimal.NewFromInt(stop).GreaterThan(*l.Ask) {
		return nil, simples("STOP_ALEM_DO_PRECO", fmt.Sprintf("stop %d não está acima do ask %s (posição vendida)", stop, l.Ask))
	}
	op.StopAjustado, op.StopAjustadoOrigem = stop, origem
	m.salvarOp(op)
	_, lim := m.calcularStop(op, lado)
	situacao := AjusteEnviado
	switch {
	case protecaoOcupada(p):
		situacao = AjusteAguardando
	case abs64(stop-p.Stop) < m.p.AjusteDegrauMin:
		situacao = AjusteIgnorado
	}
	slog.Info("ajuste da proteção", "op", op.OperacaoID, "modulo", op.Modulo, "stop", stop, "limite", lim, "no_pnt", p.Stop, "situacao", situacao, "origem", origem)
	if situacao != AjusteIgnorado {
		m.ajustarProtecao(op)
	}
	return Resposta{"aceito": true, "modulo": op.Modulo, "operacao_id": op.OperacaoID, "situacao": situacao,
		"preco_stop": stop, "preco_limite": lim, "preco_stop_pnt": p.Stop, "preco_limite_pnt": p.StopLim}, nil
}

// protecaoOcupada: a 1014 tem um pedido sem resposta (envio, replace, cancel) ou está em erro —
// nada novo sai para ela agora (um pedido por vez).
func protecaoOcupada(p *ordem) bool {
	return p.pend != semPendencia || p.Estado == dominio.Enviando || p.Estado == dominio.SemConfirmacao ||
		p.Estado == dominio.Pendente || !p.emErroDesde.IsZero()
}

// gatilhoPorPontos: médio da entrada ∓ pts, arredondado para o lado mais perto do mercado.
func (m *Motor) gatilhoPorPontos(op *operacao, lado dominio.Lado, pts int64) int64 {
	medio, _ := op.medio(true)
	if lado == dominio.Venda {
		return m.acima(medio.Sub(decimal.NewFromInt(pts)))
	}
	return m.abaixo(medio.Add(decimal.NewFromInt(pts)))
}

// ajusteRecusado (v1.4 K1): o PnT recusou o replace de ajuste da 1014. O stop que está no PnT
// fica (vira o pedido da operação, para nada tentar de novo) e o pedido guardado, se houver, cai;
// o gestor recebe PROTECAO_AJUSTE_RECUSADO com o motivo e decide se pede de novo.
func (m *Motor) ajusteRecusado(op *operacao, p *ordem, pedido int64, texto string) {
	op.StopAjustado, op.StopAjustadoOrigem = p.Stop, op.stopOrigem()
	op.protecaoDesatualizada = false
	m.salvarOp(op)
	slog.Warn("ajuste da proteção recusado pelo PnT", "op", op.OperacaoID, "pedido", pedido, "mantido", p.Stop, "texto", texto)
	m.publicar("PROTECAO_AJUSTE_RECUSADO", op, map[string]any{"motivo": texto, "preco_stop_pedido": pedido,
		"preco_stop": p.Stop, "preco_limite": p.StopLim, "qtd": p.Qtd})
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
