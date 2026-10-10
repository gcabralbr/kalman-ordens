package motor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

func (m *Motor) tick() {
	if m.hoje() != m.dia {
		m.virarDia()
	}
	m.al.Tick()
	agora := m.agora()
	m.observarPreco()
	logado := m.fix != nil && m.fix.Logado()

	if m.conciliando && logado {
		switch {
		case !m.massProxima.IsZero():
			if !agora.Before(m.massProxima) {
				m.pedirMassStatus()
			}
		case agora.Sub(m.massUltima) >= m.p.SilencioMassStatus:
			m.conciliar() // e segue: as ações de tempo já rodam com o estado conciliado
		}
	}
	podeEnviar := logado && !m.conciliando

	if !m.conciliando {
		for _, op := range m.ops {
			if !op.encerramentoEm.IsZero() && !agora.Before(op.encerramentoEm) {
				m.encerrarEntrada(op, op.motivoEncerramento, true)
			}
		}
	}
	for _, op := range m.ops {
		if !op.Estado.Final() {
			m.alertasDeTempo(op, agora)
		}
	}
	if podeEnviar {
		if m.verificarSemConfirmacao(agora) {
			return
		}
		for _, op := range m.ops {
			if !op.Estado.Final() {
				m.acoesDeTempo(op, agora)
			}
		}
		if !agora.Before(m.proxInvariante) {
			m.verificarInvariante(agora)
			m.proxInvariante = agora.Add(time.Second)
		}
		m.verificarZeragem()
		m.verificarPerdaDoDia()
	}
	m.verificarVencimento()
}

// observarPreco: estado de ticker divergente (B3/B5) e o último preco do símbolo certo.
func (m *Motor) observarPreco() {
	l := m.precos.Ler()
	if l.Situacao != preco.OK || l.Ticker == "" {
		return
	}
	if l.Ticker == m.p.Simbolo {
		if m.simboloDiv {
			m.simboloDiv = false
			m.al.Resolver("SIMBOLO")
		}
		if l.Preco != nil {
			m.ultimoCerto, m.temUltimo = *l.Preco, true
			m.ultimoCertoT = l.TS
			if m.ultimoCertoT.IsZero() {
				m.ultimoCertoT = m.agora().Add(-l.Idade(m.agora()))
			}
		}
		return
	}
	if !m.simboloDiv {
		m.simboloDiv = true
		codigo, texto := "SIMBOLO_DIVERGENTE", fmt.Sprintf("marketdata em %s, serviço em %s: entradas bloqueadas", l.Ticker, m.p.Simbolo)
		if m.posicaoTotal() != 0 {
			codigo = "SIMBOLO_DIVERGENTE_COM_POSICAO"
			texto += fmt.Sprintf("; posição %d aberta, a 1014 continua", m.posicaoTotal())
		}
		m.al.Emitir(codigo, "SIMBOLO", texto, 0)
	}
}

func (m *Motor) alertasDeTempo(op *operacao, agora time.Time) {
	if op.Estado == dominio.PosicionadaSemProtecao && !op.semProtecaoDesde.IsZero() && agora.Sub(op.semProtecaoDesde) >= m.p.AlertaSemProtecao {
		m.al.Emitir("SEM_PROTECAO", chaveOp("SEM_PROTECAO", op), fmt.Sprintf("posição %d sem proteção: operação %d", op.posicao(), op.OperacaoID), op.OperacaoID)
	}
	for _, o := range op.ordens {
		if o.Estado == dominio.Pendente && !o.pendenteDesde.IsZero() && !o.alertouPendente && agora.Sub(o.pendenteDesde) > m.p.AlertaPendente {
			o.alertouPendente = true
			m.al.Emitir("PENDENTE_39A", "", fmt.Sprintf("ordem em 39=A há mais de %s: %s", m.p.AlertaPendente, o.ClOrdAtual), op.OperacaoID)
		}
		if o.pend == pendCancel && !o.pendDesde.IsZero() && agora.Sub(o.pendDesde) >= m.p.EsperaStop {
			m.al.Emitir("CANCEL_SEM_RESPOSTA", chaveOp("CANCEL_SEM_RESPOSTA", op), fmt.Sprintf("cancelamento sem resposta: %s", o.ClOrdAtual), op.OperacaoID)
		}
	}
}

// verificarSemConfirmacao (F6): NewOrderSingle sem nenhuma resposta em PRAZO_CONFIRMACAO_S.
func (m *Motor) verificarSemConfirmacao(agora time.Time) bool {
	achou := false
	for _, op := range m.ops {
		for _, o := range op.ordens {
			if o.Estado != dominio.Enviando || o.enviada.IsZero() || agora.Sub(o.enviada) < m.p.PrazoConfirmacao {
				continue
			}
			o.Estado = dominio.SemConfirmacao
			m.salvarOrdem(o)
			achou = true
			m.al.Emitir("SEM_CONFIRMACAO", chaveOp("SEM_CONFIRMACAO", op), fmt.Sprintf("ordem %s sem resposta do PnT em %s", o.ClOrdAtual, m.p.PrazoConfirmacao), op.OperacaoID)
		}
	}
	if achou && !m.conciliando {
		m.conciliando = true
		m.massTentativas = 0
		m.pedirMassStatus()
	}
	return achou
}

func (m *Motor) acoesDeTempo(op *operacao, agora time.Time) {
	if e := op.viva(dominio.Entrada); e != nil && e.Estado != dominio.Enviando && e.Estado != dominio.SemConfirmacao &&
		!e.enviada.IsZero() && e.pend == semPendencia && agora.Sub(e.enviada) >= m.p.TimeoutEntrada {
		switch {
		case e.cancelamentos == 0:
			slog.Info("timeout da entrada: cancelando o restante", "clordid", e.ClOrdAtual)
			m.cancelar(e)
		case e.cancelamentos < 3 && agora.Sub(e.ultimoCancel) >= m.p.RetentativaProtecao:
			m.cancelar(e)
		case e.cancelamentos >= 3:
			m.al.Emitir("CANCEL_SEM_RESPOSTA", chaveOp("CANCEL_SEM_RESPOSTA", op), "a entrada não cancela (3 tentativas): "+e.ClOrdAtual, op.OperacaoID)
		}
	}
	if !op.retentativaProtEm.IsZero() && !agora.Before(op.retentativaProtEm) {
		m.ajustarProtecao(op)
	}
	switch op.Estado {
	case dominio.Protegida:
		m.verificarFuro(op, agora)
	case dominio.CancelandoProtecao:
		p := op.viva(dominio.Protecao)
		switch {
		case p == nil:
			m.mudar(op, dominio.Saindo)
			m.enviarSaida(op)
		case p.pend == semPendencia && !p.cancelarApos:
			// cancel que não saiu ou foi rejeitado por 35=3/35=j: tenta de novo, com intervalo
			if p.ultimoCancel.IsZero() || agora.Sub(p.ultimoCancel) >= m.p.RetentativaProtecao {
				op.cancelProtEm = agora
				m.cancelar(p)
			}
		}
	case dominio.AguardandoStop:
		if agora.Sub(op.aguardandoStopDesde) >= m.p.EsperaStop {
			slog.Info("stop não zerou; seguindo com a saída", "op", op.OperacaoID)
			m.mudar(op, dominio.Saindo)
			m.enviarSaida(op)
		}
	case dominio.Saindo:
		m.tickSaida(op)
	}
}

// verificarFuro (Parte 3.6): último além do limite da 1014 por mais de ESPERA_STOP_S.
func (m *Motor) verificarFuro(op *operacao, agora time.Time) {
	p := op.viva(dominio.Protecao)
	px := m.precoConfiavel(m.precos.Ler())
	if m.simboloDiv || p == nil || px == nil || p.StopLim == 0 || op.posicao() == 0 {
		op.furoDesde = timeZero
		return
	}
	lim := decimal.NewFromInt(p.StopLim)
	furou := (p.Lado == dominio.Venda && px.LessThan(lim)) || (p.Lado == dominio.Compra && px.GreaterThan(lim))
	if !furou {
		op.furoDesde = timeZero
		return
	}
	if op.furoDesde.IsZero() {
		op.furoDesde = agora
		return
	}
	if agora.Sub(op.furoDesde) < m.p.EsperaStop {
		return
	}
	op.furado = true
	m.al.Emitir("STOP_FURADO", chaveOp("STOP_FURADO", op), fmt.Sprintf("stop furado: último %s além do limite %d com posição %d, operação %d", px, p.StopLim, op.posicao(), op.OperacaoID), op.OperacaoID)
	op.MotivoSaida, op.OrigemSaida, op.SaidaPedidaEm = "STOP_FURADO", "STOP_FURADO", agora
	m.mudar(op, dominio.CancelandoProtecao)
	op.cancelProtEm = agora
	m.cancelar(p)
}

func (m *Motor) verificarZeragem() {
	if m.zeragemFeita || m.horaDoDia() < m.p.HoraZeragem {
		return
	}
	m.zeragemFeita = true
	_ = m.repo.GravarEstado("zeragem_feita", m.dia, m.agora())
	algo := false
	for _, op := range m.ops {
		if !op.Estado.Final() && (op.posicao() != 0 || op.viva(dominio.Entrada) != nil) {
			algo = true
		}
	}
	if algo {
		m.al.Emitir("ZERAGEM", "", fmt.Sprintf("HORA_ZERAGEM com posição %d: zerando (anomalia)", m.posicaoTotal()), 0)
		m.ativarParadaCom("ZERAR", "auto", "hora de zeragem", "ZERAGEM")
	}
}

func (m *Motor) verificarPerdaDoDia() {
	if m.parada != nil || m.simboloDiv {
		return
	}
	px := m.precoConfiavel(m.precos.Ler())
	if px == nil {
		return
	}
	r := m.resultadoDoDia(px)
	if r.LessThanOrEqual(decimal.NewFromInt(-m.p.PerdaDiaPts)) {
		m.ativarParada("BLOQUEAR", "auto", "perda do dia "+r.String()+" pts")
	}
}

func (m *Motor) verificarVencimento() {
	if m.p.Vencimento == "" || m.avisoVencDia == m.dia {
		return
	}
	m.avisoVencDia = m.dia
	venc, err := time.ParseInLocation("2006-01-02", m.p.Vencimento, relogio.Fuso)
	if err != nil {
		return
	}
	hoje, _ := time.ParseInLocation("2006-01-02", m.dia, relogio.Fuso)
	pregoes := 0
	for d := hoje.AddDate(0, 0, 1); !d.After(venc); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			pregoes++
		}
	}
	if !hoje.Before(venc) || pregoes <= m.p.PregoesAvisoVencimento {
		m.al.Emitir("VENCIMENTO", "", fmt.Sprintf("vencimento de %s em %s (%d pregão(ões))", m.p.Simbolo, m.p.Vencimento, pregoes), 0)
	}
}

func (m *Motor) virarDia() {
	for _, op := range m.ops {
		if !op.Estado.Final() {
			m.al.Emitir("VIRADA_COM_OPERACAO", "VIRADA_COM_OPERACAO", "virada de dia com operação não encerrada; estado mantido", op.OperacaoID)
			m.dia = m.hoje()
			return
		}
	}
	if err := m.carregarDia(); err != nil {
		slog.Error("virada de dia", "erro", err)
	}
}

func (m *Motor) ativarParada(modo, origem, motivo string) {
	m.ativarParadaCom(modo, origem, motivo, "PARADA")
}

func (m *Motor) ativarParadaCom(modo, origem, motivo, origemSaida string) {
	if m.parada != nil && m.parada.Modo == "ZERAR" {
		modo = "ZERAR"
	}
	m.parada = &Parada{Modo: modo, Origem: origem, Motivo: motivo, Desde: m.agora().In(relogio.Fuso).Format(time.RFC3339)}
	if b, err := json.Marshal(m.parada); err == nil {
		_ = m.repo.GravarEstado("parada", string(b), m.agora())
	}
	m.al.Emitir("PARADA", "", fmt.Sprintf("PARADA %s (%s): %s", modo, origem, motivo), 0)
	for _, op := range m.ops {
		if op.Estado.Final() {
			continue
		}
		if e := op.viva(dominio.Entrada); e != nil {
			m.cancelar(e)
		}
		if modo == "ZERAR" {
			m.iniciarSaida(op, origemSaida, origemSaida)
		}
	}
}
