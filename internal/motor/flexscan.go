package motor

import (
	"log/slog"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// OuvinteFlexscan é o lado do motor que a ponte Flexscan chama (SPEC-FLEXSCAN-v1 §3): a mesma forma
// do pnt.Ouvinte da sessão FIX — logou, caiu, mensagem de aplicação (os ExecutionReports e 35=9
// sintéticos) —, mais os alertas e o registro dos eventos REST e dos webhooks, tudo pela fila do
// motor (P0, como as mensagens do PnT).
type OuvinteFlexscan struct{ m *Motor }

// OuvinteFlexscan: o ouvinte da ponte.
func (m *Motor) OuvinteFlexscan() *OuvinteFlexscan { return &OuvinteFlexscan{m} }

// Logou: a ponte obteve token (no boot ou depois de perder a sessão). Concilia, como o logon FIX.
func (o *OuvinteFlexscan) Logou() {
	m := o.m
	m.entrarFIX(func() {
		if m.flxJaLogou {
			m.al.Emitir("SESSAO_VOLTOU", "SESSAO_VOLTOU:FLEXSCAN", "[flexscan] sessão voltou (token)", 0)
		}
		m.flxJaLogou = true
		m.al.Resolver("SESSAO_CAIU:FLEXSCAN")
		m.conciliando = true
		m.massTentativas = 0
		m.pedirMassStatus()
	})
}

// Caiu: a ponte ficou sem token (login falhou, 401 repetido). As ordens da Flexscan continuam na
// bolsa; entradas novas desse conector são recusadas (SEM_SESSAO) até voltar.
func (o *OuvinteFlexscan) Caiu(motivo string) {
	m := o.m
	m.entrarFIX(func() {
		m.al.Emitir("SESSAO_CAIU", "SESSAO_CAIU:FLEXSCAN", "[flexscan] sem sessão: "+motivo, 0)
	})
}

// Recebeu: um ExecutionReport (35=8) ou um 35=9 sintético da ponte.
func (o *OuvinteFlexscan) Recebeu(msg pnt.Mensagem) {
	m := o.m
	metricas.FixMensagens.Inc("recebida", msg.Tipo)
	m.marcarChegada(msg.Texto(11), msg.Texto(41))
	if msg.Tipo == "8" && msg.Texto(150) == "I" {
		m.chegadaMass.Store(m.rel.Agora().UnixNano())
	}
	m.entrarFIX(func() { m.processadas++; m.processarFlexscan(msg) })
}

// Alertar / Resolver: os alertas da ponte (FLEXSCAN_*), pelo catálogo de sempre.
func (o *OuvinteFlexscan) Alertar(codigo, chave, texto string) {
	o.m.entrarFIX(func() { o.m.al.Emitir(codigo, chave, texto, 0) })
}

func (o *OuvinteFlexscan) Resolver(chave string) {
	o.m.entrarFIX(func() { o.m.al.Resolver(chave) })
}

// Registrar: um evento REST ou webhook no kalmanordemevento (no lote da volta), já mascarado.
func (o *OuvinteFlexscan) Registrar(ev repo.EventoFIX) {
	m := o.m
	m.entrarFIX(func() {
		if ev.Em.IsZero() {
			ev.Em = m.agora()
		}
		if ev.OrderID != "" {
			if or := m.por37[ev.OrderID]; or != nil {
				ev.OrdemID, ev.Modulo = or.ID, or.Modulo
			}
		}
		m.v.eventos = append(m.v.eventos, ev)
	})
}

// processarFlexscan: como o processar do PnT, sem o disjuntor de bolsa e sem o túnel (são do PnT).
func (m *Motor) processarFlexscan(msg pnt.Mensagem) {
	switch msg.Tipo {
	case "8":
		m.execReport(msg)
	case "9":
		m.cancelRejeitado(msg)
	case "j":
		m.businessReject(msg) // o mass status de conta sem ordens (380=101)
	default:
		slog.Debug("mensagem da ponte ignorada", "tipo", msg.Tipo, "conector", dominio.ConectorFlexscan)
	}
}
