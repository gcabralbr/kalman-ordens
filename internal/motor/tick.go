package motor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

func (m *Motor) tick() {
	if m.hoje() != m.dia {
		m.virarDia()
	}
	m.al.Tick()
	m.tickSinais()
	m.fecharConsultaSePronta(true)
	m.recarregarUsuarios(m.agora())
	m.atualizarMetricas()
	agora := m.agora()
	m.observarPreco()
	logado := m.fix != nil && m.fix.Logado()
	if !logado && !m.presaDesde.IsZero() && agora.Sub(m.presaDesde) >= m.p.AlertaSessaoPresa {
		m.al.Emitir("SESSAO_PRESA_NO_PNT", "SESSAO_PRESA_NO_PNT", fmt.Sprintf("o PnT recusa o logon há %s (%s): ainda segura a sessão anterior; posição %d, 1014 no PnT; reconectando com espera crescente",
			agora.Sub(m.presaDesde).Truncate(time.Second), m.presaTexto, m.posicaoTotal()), 0)
	}

	if m.conciliando && logado {
		switch {
		case !m.massProxima.IsZero():
			if !agora.Before(m.massProxima) {
				m.pedirMassStatus()
			}
		case !m.massCom911 && agora.Sub(m.ultimoSinalMass()) >= m.p.SilencioMassStatus:
			// PnT sem 911/912 (ou nenhuma conta): fecha no silêncio, como sempre
			m.conciliar() // e segue: as ações de tempo já rodam com o estado conciliado
		case m.massCom911 && agora.Sub(m.ultimoSinalMass()) >= m.p.EsperaMaxMassStatus:
			// v1.10 C6: com 911/912, só fecha incompleta depois da espera máxima; as contas que não
			// responderam não são julgadas
			m.conciliar()
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
		if !agora.Before(m.proxConfere) {
			m.proxConfere = agora.Add(m.p.ConfereConta)
			for _, op := range m.ops {
				m.conferirConta(op)
			}
		}
	}
}

// observarPreco (v1.2 S1–S2): o ticker do /precos/atual é o contrato vigente (o das entradas);
// guarda o último preco de cada ticker; posição num contrato que deixou de ser o vigente →
// alerta crítico (B5.b), a 1014 continua.
func (m *Motor) observarPreco() {
	l := m.precos.Ler()
	if l.Situacao != preco.OK || l.Ticker == "" {
		return
	}
	if l.Preco != nil {
		em := l.TS
		if em.IsZero() {
			em = m.agora().Add(-l.Idade(m.agora()))
		}
		m.ultimos[l.Ticker] = ultimoPreco{px: *l.Preco, em: em}
	}
	if l.Ticker != m.vigente {
		if m.vigente != "" {
			m.al.Emitir("CONTRATO_VIGENTE", "", fmt.Sprintf("contrato vigente no marketdata: %s → %s (entradas novas vão em %s)", m.vigente, l.Ticker, l.Ticker), 0)
		}
		m.vigente = l.Ticker
	}
	for _, op := range m.ops {
		if op.Estado.Final() || op.posicao() == 0 {
			continue
		}
		if m.divergenteOp(op) {
			m.al.Emitir("SIMBOLO_DIVERGENTE_COM_POSICAO", chaveOp("SIMBOLO", op),
				fmt.Sprintf("marketdata em %s, posição %d da operação %d em %s: a 1014 continua; a saída usa %s", m.vigente, op.posicao(), op.OperacaoID, op.Simbolo, op.Simbolo), op.OperacaoID)
		} else {
			m.al.Resolver(chaveOp("SIMBOLO", op))
		}
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
		// cancel sem resposta: sem 39=6 em ESPERA_STOP_S; com o 39=6, sem 39=4 em
		// ESPERA_ENCERRAMENTO_S depois dele (v1.3 F2)
		// (v1.10 P3) uma resposta que já chegou à sessão e ainda está na fila do motor conta: antes
		// do 39=6, qualquer chegada desde o envio; depois dele, só uma chegada posterior (o 39=4)
		limite, desde := m.p.EsperaStop, o.pendDesde
		chegou := m.chegouDesde(desde, o.clPend)
		if !o.cancelPendenteEm.IsZero() {
			limite, desde = m.p.EsperaEncerramento, o.cancelPendenteEm
			chegou = m.chegouDepois(desde, o.clPend)
		}
		if o.pend == pendCancel && !desde.IsZero() && agora.Sub(desde) >= limite && !chegou {
			m.al.Emitir("CANCEL_SEM_RESPOSTA", chaveOp("CANCEL_SEM_RESPOSTA", op), fmt.Sprintf("cancelamento sem resposta: %s", o.ClOrdAtual), op.OperacaoID)
		}
	}
}

// verificarSemConfirmacao (F6): NewOrderSingle sem nenhuma resposta em PRAZO_CONFIRMACAO_S.
func (m *Motor) verificarSemConfirmacao(agora time.Time) bool {
	achou := false
	for _, op := range m.ops {
		for _, o := range op.ordens {
			// a resposta que já chegou à sessão (ainda na fila do motor) conta (v1.10 P3)
			if o.Estado != dominio.Enviando || o.enviada.IsZero() || agora.Sub(o.enviada) < m.p.PrazoConfirmacao ||
				m.chegouDesde(o.enviada, o.ClOrdAtual, o.ClOrdBase) {
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
	m.tratarErros(op, agora)
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

// tratarErros (v1.1 F14): ordem em estado de erro (39=7) que não voltou em ESPERA_ERRO_S.
// 1014 = proteção ausente: cancela e, confirmado o cancelamento, recria sob o limite da P8.
// Entrada: cancela. Saída: cancela e a saída seguinte sai com o próximo repreço. O texto 58 do
// 39=7 vai sempre no alerta.
func (m *Motor) tratarErros(op *operacao, agora time.Time) {
	for _, o := range op.ordens {
		if o.emErroDesde.IsZero() || o.erroTratado || !o.viva() || agora.Sub(o.emErroDesde) < m.p.EsperaErro {
			continue
		}
		o.erroTratado = true
		texto := ""
		if o.erroTexto != "" {
			texto = " (" + o.erroTexto + ")"
		}
		switch o.Papel {
		case dominio.Protecao:
			o.canceladaPorErro = true
			m.al.Emitir("PROTECAO_EM_ERRO", chaveOp("PROTECAO_EM_ERRO", op),
				fmt.Sprintf("1014 %s em estado de erro no PnT%s há %s sem reativação: cancelando e recriando, operação %d", o.ClOrdAtual, texto, m.p.EsperaErro, op.OperacaoID), op.OperacaoID)
		case dominio.Entrada:
			m.al.Emitir("ENTRADA_EM_ERRO", "",
				fmt.Sprintf("entrada %s em estado de erro no PnT%s há %s sem reativação: cancelando, operação %d", o.ClOrdAtual, texto, m.p.EsperaErro, op.OperacaoID), op.OperacaoID)
		case dominio.Saida:
			op.continuarRepreco = true
			m.al.Emitir("SAIDA_EM_ERRO", "",
				fmt.Sprintf("saída %s em estado de erro no PnT%s há %s sem reativação: cancelando; a saída segue com o próximo repreço, operação %d", o.ClOrdAtual, texto, m.p.EsperaErro, op.OperacaoID), op.OperacaoID)
		}
		m.salvarOp(op)
		m.cancelar(o)
	}
}

// verificarFuro (Parte 3.6). Gatilho explícito (v1.3 J2): a 1014 disparou no PnT (39=B com
// 20807>0) e nenhum fill chegou em ESPERA_STOP_S. Reserva: último além do limite da 1014 por
// mais de ESPERA_STOP_S (o disparo pode não ter sido visto, ex.: num restart).
func (m *Motor) verificarFuro(op *operacao, agora time.Time) {
	p := op.viva(dominio.Protecao)
	if p != nil && !p.disparadaEm.IsZero() && p.QtdExec == p.qtdNoDisparo && op.posicao() != 0 && agora.Sub(p.disparadaEm) >= m.p.EsperaStop {
		m.stopFurado(op, p, agora, fmt.Sprintf("stop furado: a 1014 %s disparou no PnT há %s (39=B) e nenhum fill chegou, posição %d, operação %d",
			p.ClOrdAtual, agora.Sub(p.disparadaEm).Truncate(100*time.Millisecond), op.posicao(), op.OperacaoID))
		return
	}
	px := m.precoConfiavel(m.precos.Ler(), op.Simbolo)
	if m.divergenteOp(op) || p == nil || px == nil || p.StopLim == 0 || op.posicao() == 0 {
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
	m.stopFurado(op, p, agora, fmt.Sprintf("stop furado: último %s além do limite %d com posição %d, operação %d", px, p.StopLim, op.posicao(), op.OperacaoID))
}

// stopFurado: cancela a 1014 e sai com a 1031 (Parte 3.6).
func (m *Motor) stopFurado(op *operacao, p *ordem, agora time.Time, texto string) {
	op.furado = true
	m.al.Emitir("STOP_FURADO", chaveOp("STOP_FURADO", op), texto, op.OperacaoID)
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
	m.gravarEstado("zeragem_feita", m.dia)
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
	if m.parada != nil {
		return
	}
	px, ok := m.precoDoDia(m.precos.Ler())
	if !ok {
		return
	}
	r := m.resultadoDoDia(px)
	if r.LessThanOrEqual(decimal.NewFromInt(-m.p.PerdaDiaPts)) {
		m.ativarParada("BLOQUEAR", "auto", "perda do dia "+r.String()+" pts")
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
		m.gravarEstado("parada", string(b))
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

// atualizarMetricas: os medidores de estado do /metrics (v1.9 M).
// verParada: a cópia da parada que a consulta ao vivo lê (outra goroutine).
func (m *Motor) verParada() {
	if m.parada == nil {
		m.paradaVista.Store(nil)
		return
	}
	p := *m.parada
	m.paradaVista.Store(&p)
}

func (m *Motor) atualizarMetricas() {
	vivas, abertas := 0, 0
	for _, op := range m.ops {
		if !op.Estado.Final() {
			abertas++
		}
		for _, o := range op.ordens {
			if o.viva() {
				vivas++
			}
		}
	}
	metricas.DefinirEstado(vivas, abertas, m.posicaoTotal(), m.fix != nil && m.fix.Logado())
	m.verParada()
}
