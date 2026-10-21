package motor

import (
	"fmt"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/preco"
)

// Proteção contra falha de fonte (21/10, item 2): FONTE_DEGRADADA, crítico, quando a fonte de preço
// fica ruim por mais de FONTE_DEGRADADA_S (30) dentro de FONTE_JANELA (09:00-18:25); resolvido quando
// volta (ou quando a janela fecha). Ruim = sem resposta, {} (o pnt-md-hml responde {} com book
// degenerado ou atraso acima da base), 404/400; em negociação, sem bid/ask, book cruzado ou preço
// mais velho que PRECO_IDADE_MAX_S. Leilão (status84 != 0) com preço, bid ou ask não é falha.

// motivoFonteRuim: "" = fonte boa.
func (m *Motor) motivoFonteRuim(l preco.Leitura, agora time.Time) string {
	switch l.Situacao {
	case preco.SemLeitura:
		return "sem resposta do marketdata"
	case preco.Vazio:
		return "marketdata sem preco ({})"
	case preco.Indisponivel:
		return "marketdata indisponivel (404/400)"
	}
	if l.Status84 != 0 {
		if l.Preco != nil || l.Bid != nil || l.Ask != nil {
			return ""
		}
		return fmt.Sprintf("leilao (status84 %d) sem preco", l.Status84)
	}
	if l.Bid == nil || l.Ask == nil {
		return "sem bid/ask"
	}
	if l.Bid.GreaterThanOrEqual(*l.Ask) {
		return fmt.Sprintf("book cruzado (bid %s >= ask %s)", l.Bid, l.Ask)
	}
	if id := l.Idade(agora); id > m.p.IdadeMaxPreco {
		return fmt.Sprintf("preco com %.1f s (maximo %s)", id.Seconds(), m.p.IdadeMaxPreco)
	}
	return ""
}

// detalheFonte: o motivo informado pela própria fonte (o /saude do pnt-md-hml, PRECOS_SAUDE_URL), se houver.
type detalheFonte interface{ MotivoFonte() string }

func (m *Motor) observarFonte() {
	agora := m.agora()
	h := m.horaDoDia()
	motivo := ""
	if h >= m.p.FonteJanelaInicio && h < m.p.FonteJanelaFim {
		motivo = m.motivoFonteRuim(m.precos.Ler(), agora)
	}
	if motivo == "" {
		m.fonteRuimDesde, m.fonteMotivo = time.Time{}, ""
		m.al.Resolver("FONTE_DEGRADADA")
		return
	}
	if m.fonteRuimDesde.IsZero() {
		m.fonteRuimDesde = agora
	}
	m.fonteMotivo = motivo
	if d, ok := m.precos.(detalheFonte); ok {
		if x := d.MotivoFonte(); x != "" {
			m.fonteMotivo = motivo + "; fonte: " + x
		}
	}
	if dur := agora.Sub(m.fonteRuimDesde); dur >= m.p.FonteDegradada && !m.al.Ativo("FONTE_DEGRADADA") {
		m.al.Emitir("FONTE_DEGRADADA", "FONTE_DEGRADADA", fmt.Sprintf("fonte de preco degradada ha %s: %s; entradas recusadas",
			dur.Truncate(time.Second), m.fonteMotivo), 0)
	}
}

// saudeFonte: o bloco "fonte" do /saude.
func (m *Motor) saudeFonte() map[string]any {
	var desde any
	if !m.fonteRuimDesde.IsZero() {
		desde = m.fonteRuimDesde.Format(time.RFC3339)
	}
	return map[string]any{
		"degradada":  m.al.Ativo("FONTE_DEGRADADA"),
		"ruim_desde": desde,
		"motivo":     m.fonteMotivo,
		"janela":     fmt.Sprintf("%s-%s", hhmm(m.p.FonteJanelaInicio), hhmm(m.p.FonteJanelaFim)),
	}
}

func hhmm(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}
