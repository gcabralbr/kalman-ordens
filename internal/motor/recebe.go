package motor

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

var timeZero time.Time

func ehNosso(cl string) bool { return strings.HasPrefix(cl, "KM-") }

func (m *Motor) processar(msg pnt.Mensagem) {
	switch msg.Tipo {
	case "8":
		m.execReport(msg)
	case "9":
		m.cancelRejeitado(msg)
	case "j":
		m.businessReject(msg)
	default:
		slog.Debug("mensagem ignorada", "tipo", msg.Tipo)
	}
}

func (m *Motor) registrarRecebida(o *ordem, msg pnt.Mensagem) bool {
	ev := repo.EventoFIX{
		Direcao: "REC", MsgType: msg.Tipo, ExecType: msg.Texto(150), OrdStatus: msg.Texto(39),
		ClOrdID: msg.Texto(11), OrderID: msg.Texto(37), ExecID: msg.Texto(17), Texto: msg.Texto(58),
		Bruta: msg.Bruta, Em: m.agora(),
	}
	for _, t := range []int{103, 102, 380, 373} {
		if v := msg.Texto(t); v != "" {
			ev.Codigo = v
			break
		}
	}
	if q, ok := msg.Inteiro(32); ok {
		ev.LastQty = &q
	}
	if q, ok := msg.Inteiro(14); ok {
		ev.CumQty = &q
	}
	if d, ok := msg.Decimal(31); ok {
		ev.LastPx = &d
	}
	if d, ok := msg.Decimal(6); ok {
		ev.AvgPx = &d
	}
	if o != nil {
		ev.OrdemID = o.ID
	}
	novo, err := m.repo.RegistrarEvento(ev)
	if err != nil {
		slog.Error("registrar evento", "erro", err)
		return true
	}
	return novo
}

func (m *Motor) execReport(msg pnt.Mensagem) {
	cl, orig, id37 := msg.Texto(11), msg.Texto(41), msg.Texto(37)
	if msg.Texto(150) == "I" && m.conciliando && m.massReqIDs[msg.Texto(584)] {
		m.massUltima = m.agora()
		if ehNosso(cl) || ehNosso(orig) {
			c := msg
			m.massRespostas = append(m.massRespostas, &c)
		} else {
			m.massRespostas = append(m.massRespostas, nil) // ordem de outro (PT) na mesma conta
		}
		return
	}
	if !ehNosso(cl) && !ehNosso(orig) && m.por37[id37] == nil {
		return // ordem do PT (ou de outro) na mesma conta
	}
	o := m.localizar(id37, cl, orig)
	if o == nil {
		m.registrarRecebida(nil, msg)
		m.al.Emitir("DESCONHECIDA", "", fmt.Sprintf("ER de ordem KM sem registro: 11=%s 37=%s", cl, id37), 0)
		return
	}
	if !m.registrarRecebida(o, msg) {
		slog.Info("ExecID repetido, ignorado", "execid", msg.Texto(17), "clordid", o.ClOrdAtual)
		return
	}
	m.aplicarExecReport(m.donoDa[o], o, msg)
}

func (m *Motor) confirmar(op *operacao, o *ordem) {
	if o.Estado == dominio.Enviando || o.Estado == dominio.SemConfirmacao || o.Estado == dominio.Desconhecida {
		era := o.Estado
		o.Estado = dominio.Pendente
		if era == dominio.SemConfirmacao && !m.haSemConfirmacaoNa(op) {
			m.al.Resolver(chaveOp("SEM_CONFIRMACAO", op))
		}
	}
}

func (m *Motor) haSemConfirmacaoNa(op *operacao) bool {
	for _, o := range op.ordens {
		if o.Estado == dominio.SemConfirmacao {
			return true
		}
	}
	return false
}

func (m *Motor) aplicarExecReport(op *operacao, o *ordem, msg pnt.Mensagem) {
	et, st := msg.Texto(150), msg.Texto(39)
	if id := msg.Texto(37); id != "" && id != "NONE" && o.OrderID == "" {
		o.OrderID = id
		m.por37[id] = o
	}
	if s := msg.Texto(198); s != "" {
		o.SecID = s
	}
	estavaEncerrada := o.Estado.Final() && o.Estado != dominio.FalhaEnvio
	if et != "I" {
		m.confirmar(op, o)
	}
	m.observarErro(o, st, msg)
	if o.Papel == dominio.Protecao && o.emErroDesde.IsZero() {
		m.al.Resolver(chaveOp("PROTECAO_EM_ERRO_PRESA", op)) // a 1014 saiu do erro
	}

	cum, temCum := msg.Inteiro(14)
	last, temLast := msg.Inteiro(32)
	if !temCum && et == "F" && temLast {
		cum, temCum = o.QtdExec+last, true
	}
	if temCum && cum > o.QtdExec {
		avg, _ := msg.Decimal(6)
		lastPx, _ := msg.Decimal(31)
		m.aplicarFill(op, o, cum, avg, last, lastPx, estavaEncerrada)
	}

	switch et {
	case "A":
		if o.Estado == dominio.Pendente || o.Estado == dominio.Enviando {
			o.Estado = dominio.Pendente
			if o.pendenteDesde.IsZero() {
				o.pendenteDesde = m.agora()
			}
		}
	case "0":
		if !o.Estado.Final() {
			o.Estado = ativaOuParcial(o)
		}
		if o.Papel == dominio.Protecao && !op.Estado.Final() {
			m.protecaoAceita(op, o)
		}
	case "5":
		m.replaceConfirmado(op, o, msg.Texto(11))
	case "D":
		// Restated (ex.: reativação depois do estado de erro, F14): só atualiza o estado, sem efeito
		if !o.Estado.Final() && (st == "0" || st == "1") {
			o.Estado = ativaOuParcial(o)
		}
	case "4":
		limparPendencia(o)
		o.Estado = dominio.Cancelada
		if !estavaEncerrada {
			m.ordemEncerrada(op, o, "CANCELADA")
		}
	case "C":
		limparPendencia(o)
		o.Estado = dominio.Expirada
		if !estavaEncerrada {
			m.ordemEncerrada(op, o, "EXPIRADA")
		}
	case "8":
		texto, cod := msg.Texto(58), msg.Texto(103)
		limparPendencia(o)
		o.Estado = dominio.Rejeitada
		o.MotivoRejeicao = strings.TrimSpace("[" + cod + "] " + texto)
		if !estavaEncerrada {
			m.ordemRejeitada(op, o, texto, cod)
		}
	case "I":
		m.statusDaOrdem(op, o, st, estavaEncerrada)
	default:
		if (o.Estado == dominio.Pendente) && (st == "0" || st == "1") {
			o.Estado = ativaOuParcial(o)
		}
	}
	if st == "2" && o.QtdExec < o.Qtd {
		// ex.: mass status de HML 2025 com 39=2 e 14=0: sem CumQty não há como saber a posição
		m.marcarDivergente(op, fmt.Sprintf("39=2 sem CumQty correspondente (%d/%d) em %s", o.QtdExec, o.Qtd, o.ClOrdAtual))
	} else if st == "2" && o.Estado != dominio.Executada && !estavaEncerrada {
		limparPendencia(o)
		o.Estado = dominio.Executada
		m.ordemEncerrada(op, o, "EXECUTADA")
	} else if st == "1" && (o.Estado == dominio.Ativa || o.Estado == dominio.Pendente) {
		o.Estado = dominio.Parcial
	}
	m.salvarOrdem(o)
	m.salvarOp(op)
}

// observarErro (v1.1 F14): 39=7 é o estado de erro do PnT. Com 20856=Y a ordem volta sozinha
// (150=D/39=0) em milissegundos — no log real de HML, 0 a 16 ms. O motor só registra; age se
// não voltar em ESPERA_ERRO_S (tratarErros, tick.go). Qualquer outro 39 encerra o estado de erro.
func (m *Motor) observarErro(o *ordem, st string, msg pnt.Mensagem) {
	if st == "7" {
		if o.emErroDesde.IsZero() {
			o.emErroDesde = m.agora()
			o.erroTexto = strings.TrimSpace(msg.Texto(58))
			o.erroTratado = false
			slog.Warn("ordem em estado de erro no PnT (39=7)", "clordid", o.ClOrdAtual, "texto", o.erroTexto)
		}
		return
	}
	if !o.emErroDesde.IsZero() && st != "" {
		slog.Info("ordem saiu do estado de erro", "clordid", o.ClOrdAtual, "39", st, "150", msg.Texto(150), "depois_de", m.agora().Sub(o.emErroDesde))
		o.emErroDesde, o.erroTratado = timeZero, false
	}
}

func ativaOuParcial(o *ordem) dominio.EstadoOrdem {
	if o.QtdExec > 0 {
		return dominio.Parcial
	}
	return dominio.Ativa
}

// statusDaOrdem: 150=I (mass status) — concilia o estado.
func (m *Motor) statusDaOrdem(op *operacao, o *ordem, st string, estavaEncerrada bool) {
	viva := st == "0" || st == "1" || st == "A" || st == "E" || st == "6" || st == "7"
	final := st == "4" || st == "8" || st == "C"
	switch {
	case viva && estavaEncerrada:
		m.al.Emitir("CONCILIACAO", "", fmt.Sprintf("ordem viva no PnT e encerrada na tabela, atualizando: %s", o.ClOrdAtual), op.OperacaoID)
		o.Estado = ativaOuParcial(o)
	case viva:
		if o.Estado == dominio.Enviando || o.Estado == dominio.SemConfirmacao || o.Estado == dominio.Desconhecida || o.Estado == dominio.Pendente {
			m.confirmar(op, o)
			if st == "A" {
				o.Estado = dominio.Pendente
			} else {
				o.Estado = ativaOuParcial(o)
			}
			if o.Papel == dominio.Protecao && st != "A" && !op.Estado.Final() {
				m.protecaoAceita(op, o)
			}
		}
	case final && !estavaEncerrada:
		m.confirmar(op, o)
		m.al.Emitir("CONCILIACAO", "", fmt.Sprintf("ordem encerrada no PnT (39=%s) e viva na tabela: %s", st, o.ClOrdAtual), op.OperacaoID)
		limparPendencia(o)
		motivo := map[string]string{"4": "CANCELADA", "8": "REJEITADA", "C": "EXPIRADA"}[st]
		o.Estado = map[string]dominio.EstadoOrdem{"4": dominio.Cancelada, "8": dominio.Rejeitada, "C": dominio.Expirada}[st]
		m.ordemEncerrada(op, o, motivo)
	case st == "2":
		m.confirmar(op, o)
	}
}

// aplicarFill: posição pelo maior CumQty de cada ordem (F7). Fill repetido não muda nada.
func (m *Motor) aplicarFill(op *operacao, o *ordem, cum int, avg decimal.Decimal, lastQty int, lastPx decimal.Decimal, estavaEncerrada bool) {
	antes := o.QtdExec
	delta := cum - antes
	var px decimal.Decimal
	switch {
	case lastQty == delta && lastPx.IsPositive():
		px = lastPx
	case avg.IsPositive() && antes > 0 && o.PrecoMedio.IsPositive():
		px = avg.Mul(decimal.NewFromInt(int64(cum))).Sub(o.PrecoMedio.Mul(decimal.NewFromInt(int64(antes)))).Div(decimal.NewFromInt(int64(delta))).Round(4)
	case avg.IsPositive():
		px = avg
	default:
		px = lastPx
	}
	if avg.IsPositive() {
		o.PrecoMedio = avg
	} else if px.IsPositive() {
		o.PrecoMedio = o.PrecoMedio.Mul(decimal.NewFromInt(int64(antes))).Add(px.Mul(decimal.NewFromInt(int64(delta)))).Div(decimal.NewFromInt(int64(cum))).Round(4)
	}
	o.QtdExec = cum
	slog.Info("FILL", "clordid", o.ClOrdAtual, "orderid", o.OrderID, "delta", delta, "preco", px, "cum", cum)
	if !px.IsPositive() {
		m.al.Emitir("RECUSA_PNT", "", fmt.Sprintf("fill sem preço em %s", o.ClOrdAtual), op.OperacaoID)
	}
	if cum > o.Qtd {
		m.marcarDivergente(op, fmt.Sprintf("execução acima da quantidade da ordem %s (%d/%d)", o.ClOrdAtual, cum, o.Qtd))
	}

	if o.Papel == dominio.Entrada {
		m.fillEntrada(op, o, delta, px, estavaEncerrada)
		return
	}

	origem := op.OrigemSaida
	if o.Papel == dominio.Protecao {
		origem = "STOP_SERVIDOR"
		op.OrigemSaida = "STOP_SERVIDOR"
		if op.MotivoSaida == "" {
			op.MotivoSaida = "STOP_SERVIDOR"
		}
		if op.SaidaPedidaEm.IsZero() {
			op.SaidaPedidaEm = m.agora()
		}
	}
	if origem == "" {
		origem = "GESTOR"
	}
	if estavaEncerrada {
		m.al.Emitir("FILL_ORDEM_ENCERRADA", "", fmt.Sprintf("fill em ordem já encerrada, aceito: %s +%d", o.ClOrdAtual, delta), op.OperacaoID)
	}
	m.publicar("SAIDA_EXECUTADA", op, map[string]any{"qtd": delta, "preco": px, "origem": origem})
	pos := op.posicao()
	switch {
	case pos == 0:
		m.zerada(op)
	case pos*int(op.Lado) < 0:
		m.marcarDivergente(op, fmt.Sprintf("posição invertida (%d) na operação %d", pos, op.OperacaoID))
		for _, x := range op.ordens {
			if x.Papel != dominio.Entrada && x.viva() {
				m.cancelar(x)
			}
		}
		m.mudar(op, dominio.Saindo)
		op.proximaSaidaEm = m.agora().Add(m.p.RetentativaProtecao)
	}
}

func (m *Motor) fillEntrada(op *operacao, o *ordem, delta int, px decimal.Decimal, estavaEncerrada bool) {
	qe := op.qtdEntrada()
	pm, _ := op.medio(true)
	if op.EncerrouEntrada {
		// A3: fill depois da espera de encerramento → ENTRADA_TARDIA: protege e zera; o gestor só registra
		m.al.Emitir("ENTRADA_TARDIA", "", fmt.Sprintf("fill de entrada depois do encerramento: %s +%d; protegendo e zerando", o.ClOrdAtual, delta), op.OperacaoID)
		m.publicar("ENTRADA_EXECUTADA", op, map[string]any{"qtd": delta, "preco": px, "qtd_total": qe, "preco_medio": pm, "tardio": true})
		if op.Estado.Final() {
			op.Estado = dominio.PosicionadaSemProtecao
			op.SaidaPedidaEm, op.OrigemSaida, op.MotivoSaida = timeZero, "", ""
			op.semProtecaoDesde = m.agora()
			op.zerarAposProtecao = true
			m.salvarOp(op)
		}
		m.ajustarProtecao(op)
		return
	}
	if estavaEncerrada {
		m.al.Emitir("FILL_ORDEM_ENCERRADA", "", fmt.Sprintf("fill em ordem já encerrada, aceito: %s +%d", o.ClOrdAtual, delta), op.OperacaoID)
	}
	m.publicar("ENTRADA_EXECUTADA", op, map[string]any{"qtd": delta, "preco": px, "qtd_total": qe, "preco_medio": pm, "tardio": false})
	if op.saidaAposEntrada {
		op.saidaAposEntrada = false
		m.prosseguirSaida(op)
		return
	}
	if op.Estado.Saindo() {
		return
	}
	if op.Estado == dominio.Entrando || op.Estado == dominio.EncerradaSemEntrada {
		m.mudar(op, dominio.PosicionadaSemProtecao)
		op.semProtecaoDesde = m.agora()
	}
	m.ajustarProtecao(op)
}

func (m *Motor) replaceConfirmado(op *operacao, o *ordem, clNovo string) {
	if clNovo != "" {
		o.ClOrdAtual = clNovo
		m.porClOrd[clNovo] = o
	}
	if o.pend == pendReplace {
		o.Qtd = o.qtdPend
		if o.precoPend != 0 {
			o.Preco = o.precoPend
		}
		if o.stopPend != 0 {
			o.Stop, o.StopLim = o.stopPend, o.stopLimPend
		}
	}
	cancelarDepois := o.cancelarApos
	o.cancelarApos = false
	limparPendencia(o)
	if o.Papel == dominio.Protecao {
		op.rejeicoesProtecao = 0
		m.publicar("PROTEGIDA", op, map[string]any{"preco_stop": o.Stop, "preco_limite": o.StopLim, "qtd": o.Qtd})
	}
	if cancelarDepois {
		m.cancelar(o)
	} else if o.Papel == dominio.Protecao && op.protecaoDesatualizada {
		m.ajustarProtecao(op)
	}
}

// ordemEncerrada: executada, cancelada ou expirada.
func (m *Motor) ordemEncerrada(op *operacao, o *ordem, motivo string) {
	o.cancelarApos = false
	switch o.Papel {
	case dominio.Entrada:
		m.encerrarEntrada(op, motivo, motivo == "EXECUTADA")
	case dominio.Protecao:
		switch {
		case op.Estado == dominio.CancelandoProtecao:
			m.al.Resolver(chaveOp("CANCEL_SEM_RESPOSTA", op))
			m.mudar(op, dominio.Saindo)
			m.enviarSaida(op)
		case !op.Estado.Saindo() && !op.Estado.Final() && op.posicao() != 0:
			// 1014 encerrada sem pedido nosso (cancelada ou expirada pelo PnT): recria na hora, com o
			// limite de tentativas (v1.1 P8)
			causa := "1014 " + strings.ToLower(motivo) + " pelo PnT sem pedido nosso"
			if o.canceladaPorErro {
				// cancelada por nós porque ficou em erro (F14): proteção ausente, mesma regra
				causa = "1014 em estado de erro no PnT, cancelada"
			}
			slog.Warn("1014 encerrada com posição aberta: recriando", "op", op.OperacaoID, "motivo", causa, "clordid", o.ClOrdAtual)
			m.mudar(op, dominio.PosicionadaSemProtecao)
			op.semProtecaoDesde = m.agora()
			m.falhaProtecao(op, causa)
		}
	case dominio.Saida:
		if op.Estado == dominio.Saindo && op.posicao() != 0 {
			op.proximaSaidaEm = m.agora()
		}
	}
}

func (m *Motor) ordemRejeitada(op *operacao, o *ordem, texto, codigo string) {
	m.publicar("RECUSADA", op, map[string]any{"papel": o.Papel.String(), "motivo": texto, "codigo": codigo, "clordid": o.ClOrdAtual})
	m.al.Emitir("RECUSA_PNT", "", fmt.Sprintf("recusa do PnT %s: [%s] %s", o.ClOrdAtual, codigo, texto), op.OperacaoID)
	switch o.Papel {
	case dominio.Entrada:
		m.encerrarEntrada(op, "REJEITADA", true)
	case dominio.Protecao:
		m.protecaoRejeitada(op, texto)
	case dominio.Saida:
		op.proximaSaidaEm = m.agora().Add(m.p.RetentativaProtecao)
	}
}

// encerrarEntrada: ENTRADA_ENCERRADA imediato (executada, rejeitada, desconhecida) ou depois de
// ESPERA_ENCERRAMENTO_S (cancelada, expirada) — A3.
func (m *Motor) encerrarEntrada(op *operacao, motivo string, imediato bool) {
	if op.viva(dominio.Entrada) != nil || op.EncerrouEntrada {
		return
	}
	if !imediato {
		if op.encerramentoEm.IsZero() {
			op.encerramentoEm = m.agora().Add(m.p.EsperaEncerramento)
			op.motivoEncerramento = motivo
		}
		return
	}
	op.EncerrouEntrada = true
	op.encerramentoEm = timeZero
	pm, _ := op.medio(true)
	m.publicar("ENTRADA_ENCERRADA", op, map[string]any{"qtd_final": op.qtdEntrada(), "preco_medio": pm, "motivo": motivo})
	switch {
	case op.qtdEntrada() == 0 && op.posicao() == 0:
		op.saidaAposEntrada = false
		if !op.Estado.Final() {
			m.mudar(op, dominio.EncerradaSemEntrada)
		}
	case op.saidaAposEntrada:
		op.saidaAposEntrada = false
		m.prosseguirSaida(op)
	}
	m.salvarOp(op)
}

func (m *Motor) cancelRejeitado(msg pnt.Mensagem) {
	o := m.localizar(msg.Texto(37), msg.Texto(11), msg.Texto(41))
	if o == nil {
		if ehNosso(msg.Texto(11)) || ehNosso(msg.Texto(41)) {
			m.al.Emitir("RECUSA_PNT", "", "35=9 de ordem KM desconhecida: 11="+msg.Texto(11), 0)
		}
		return
	}
	m.registrarRecebida(o, msg)
	op := m.donoDa[o]
	era := o.pend
	cancelarDepois := o.cancelarApos
	o.cancelarApos = false
	limparPendencia(o)
	m.confirmar(op, o)
	texto := strings.TrimSpace("[" + msg.Texto(102) + "] " + msg.Texto(58))
	eraCancel := era == pendCancel || (era == semPendencia && msg.Texto(434) == "1")
	if eraCancel {
		switch {
		case o.Papel == dominio.Protecao && op.Estado == dominio.CancelandoProtecao:
			m.al.Resolver(chaveOp("CANCEL_SEM_RESPOSTA", op))
			if op.furado {
				m.mudar(op, dominio.Saindo)
				m.enviarSaida(op)
			} else {
				// a 1014 provavelmente já disparou: não manda a saída, espera os fills
				m.al.Emitir("CANCEL_STOP_REJEITADO", "", fmt.Sprintf("cancel da 1014 rejeitado (%s): aguardando fills do stop, operação %d", texto, op.OperacaoID), op.OperacaoID)
				m.mudar(op, dominio.AguardandoStop)
				op.aguardandoStopDesde = m.agora()
			}
		case o.Papel == dominio.Protecao && o.canceladaPorErro:
			m.cancelProtecaoEmErroRejeitado(op, o, msg, texto)
		default:
			m.al.Emitir("RECUSA_PNT", "", fmt.Sprintf("cancel rejeitado (%s): %s", texto, o.ClOrdAtual), op.OperacaoID)
		}
	} else {
		m.al.Emitir("RECUSA_PNT", "", fmt.Sprintf("replace rejeitado (%s): %s", texto, o.ClOrdAtual), op.OperacaoID)
		if o.Papel == dominio.Protecao {
			m.protecaoRejeitada(op, texto)
		}
		if cancelarDepois {
			m.cancelar(o)
		}
	}
	m.salvarOrdem(o)
	m.salvarOp(op)
}

// cancelProtecaoEmErroRejeitado (v1.1 F17): o cancel da 1014 em erro voltou 35=9.
// "Não encontrada" (37=NONE ou "Robot Order not found") → a 1014 não existe mais: recria sob o
// limite da P8. Outro motivo → mass status agora e o estado real decide (conciliar).
func (m *Motor) cancelProtecaoEmErroRejeitado(op *operacao, o *ordem, msg pnt.Mensagem, texto string) {
	if msg.Texto(37) == "NONE" || strings.Contains(msg.Texto(58), "Robot Order not found") {
		o.Estado = dominio.Desconhecida
		m.salvarOrdem(o)
		m.recriarProtecaoAusente(op, "1014 em erro: cancel rejeitado, ordem não encontrada no PnT "+texto)
		return
	}
	m.al.Emitir("RECUSA_PNT", "", fmt.Sprintf("cancel da 1014 em erro rejeitado %s: %s; mass status para decidir", texto, o.ClOrdAtual), op.OperacaoID)
	o.decidirNoMass = true
	if !m.conciliando && m.fix != nil && m.fix.Logado() {
		m.conciliando = true
		m.massTentativas = 0
		m.pedirMassStatus()
	}
}

// recriarProtecaoAusente: a 1014 não existe mais no PnT → recria (mesmo gatilho e limite) sob o
// limite de tentativas da P8.
func (m *Motor) recriarProtecaoAusente(op *operacao, causa string) {
	if op.Estado.Saindo() || op.Estado.Final() || op.posicao() == 0 {
		return
	}
	slog.Warn("1014 ausente no PnT com posição aberta: recriando", "op", op.OperacaoID, "motivo", causa)
	m.mudar(op, dominio.PosicionadaSemProtecao)
	op.semProtecaoDesde = m.agora()
	m.falhaProtecao(op, causa)
}

// decidirProtecaoEmErro (F17), depois do mass status: terminal já foi recriada (ordemEncerrada,
// P8); ainda viva em 39=7 → não recria (pode reativar sozinha pelo 20856) e não zera: alerta
// crítico repetido + BLOQUEAR, decisão humana; reativada → a proteção voltou.
func (m *Motor) decidirProtecaoEmErro(op *operacao, o *ordem) {
	switch {
	case !o.viva():
	case !o.emErroDesde.IsZero():
		texto := ""
		if o.erroTexto != "" {
			texto = " (" + o.erroTexto + ")"
		}
		m.al.Emitir("PROTECAO_EM_ERRO_PRESA", chaveOp("PROTECAO_EM_ERRO_PRESA", op),
			fmt.Sprintf("1014 %s continua em estado de erro no PnT%s e o cancel foi rejeitado: não recriada (pode reativar sozinha), posição %d mantida; entradas bloqueadas, decisão humana, operação %d", o.ClOrdAtual, texto, op.posicao(), op.OperacaoID), op.OperacaoID)
		m.ativarParada("BLOQUEAR", "auto", fmt.Sprintf("1014 em erro sem cancel possível, operação %d", op.OperacaoID))
	default:
		m.al.Resolver(chaveOp("PROTECAO_EM_ERRO", op))
	}
}

func (m *Motor) businessReject(msg pnt.Mensagem) {
	ref, refID, razao := msg.Texto(372), msg.Texto(379), msg.Texto(380)
	if ref == "AF" || m.massReqIDs[refID] {
		m.registrarRecebida(nil, msg)
		if razao == "4" {
			esperas := []time.Duration{1, 2, 5, 10, 10, 30}
			i := m.massTentativas
			if i >= len(esperas) {
				i = len(esperas) - 1
			}
			m.massProxima = m.agora().Add(esperas[i] * time.Second)
			m.massTentativas++
			slog.Info("mass status: sistema subindo (380=4)", "nova_tentativa_em", esperas[i]*time.Second)
			return
		}
		m.conciliando = false
		m.al.Emitir("MASS_STATUS_REJEITADO", "MASS_STATUS_REJEITADO", fmt.Sprintf("mass status rejeitado (380=%s): %s", razao, msg.Texto(58)), 0)
		m.ativarParada("BLOQUEAR", "auto", "mass status rejeitado")
		return
	}
	m.rejeicaoGenerica(msg, refID, fmt.Sprintf("35=j [380=%s] %s", razao, msg.Texto(58)))
}

// rejeicaoSessao: 35=3 do PnT, ligado à ordem pelo 45.
func (m *Motor) rejeicaoSessao(msg pnt.Mensagem) {
	m.rejeicaoGenerica(msg, "", fmt.Sprintf("35=3 [373=%s tag %s] %s", msg.Texto(373), msg.Texto(371), msg.Texto(58)))
}

func (m *Motor) rejeicaoGenerica(msg pnt.Mensagem, refID, texto string) {
	cl := refID
	if cl == "" {
		if sq, ok := msg.Inteiro(45); ok {
			cl = m.clordPorSeq[sq]
		}
	}
	o := m.porClOrd[cl]
	m.registrarRecebida(o, msg)
	if o == nil {
		m.al.Emitir("RECUSA_PNT", "", "rejeição sem ordem identificada: "+texto, 0)
		return
	}
	op := m.donoDa[o]
	m.confirmar(op, o)
	if o.pend != semPendencia && cl == o.clPend {
		eraCancel := o.pend == pendCancel
		limparPendencia(o)
		m.al.Emitir("RECUSA_PNT", "", "pedido rejeitado "+texto+": "+o.ClOrdAtual, op.OperacaoID)
		if o.Papel == dominio.Protecao && !eraCancel {
			m.protecaoRejeitada(op, texto)
		}
	} else if o.viva() {
		o.Estado = dominio.Rejeitada
		o.MotivoRejeicao = texto
		m.ordemRejeitada(op, o, texto, "")
	}
	m.salvarOrdem(o)
	m.salvarOp(op)
}

// rejeicaoEnviada: a nossa biblioteca mandou 35=3 — a mensagem do PnT foi descartada (F9).
func (m *Motor) rejeicaoEnviada(msg pnt.Mensagem) {
	_, _ = m.repo.RegistrarEvento(repo.EventoFIX{Direcao: "ENV", MsgType: "3", Texto: msg.Texto(58), Codigo: msg.Texto(373), Bruta: msg.Bruta, Em: m.agora()})
	m.al.Emitir("SESSAO_REJEITOU_MENSAGEM", "SESSAO_REJEITOU_MENSAGEM",
		fmt.Sprintf("a sessão rejeitou (35=3) a mensagem %s do PnT: %s", msg.Texto(45), msg.Texto(58)), 0)
	if !m.conciliando && m.fix != nil && m.fix.Logado() {
		m.conciliando = true
		m.massTentativas = 0
		m.pedirMassStatus()
	}
}

// mensagemIlegivel: a biblioteca descartou bytes do PnT que não conseguiu ler (F11). Pode ter sido
// um fill: alerta crítico e mass status na hora, sem esperar o heartbeat revelar o buraco.
func (m *Motor) mensagemIlegivel(texto string) {
	_, _ = m.repo.RegistrarEvento(repo.EventoFIX{Direcao: "REC", MsgType: "?", Texto: cortarTexto(texto, 1000), Bruta: texto, Em: m.agora()})
	m.al.Emitir("MENSAGEM_ILEGIVEL", "MENSAGEM_ILEGIVEL", "a sessão descartou uma mensagem ilegível do PnT: "+cortarTexto(texto, 300), 0)
	if !m.conciliando && m.fix != nil && m.fix.Logado() {
		m.conciliando = true
		m.massTentativas = 0
		m.pedirMassStatus()
	}
}

func cortarTexto(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (m *Motor) marcarDivergente(op *operacao, motivo string) {
	op.Divergente = true
	m.divergente = true
	m.al.Emitir("DIVERGENTE", "DIVERGENTE", "DIVERGENTE: "+motivo, op.OperacaoID)
	if m.parada == nil {
		m.ativarParada("BLOQUEAR", "auto", "divergência: "+motivo)
	}
}
