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
	m.observarFonteDe(m.precos, "FONTE_DEGRADADA", "", &m.fonteRuimDesde, &m.fonteMotivo)
	if m.precosFlx != nil {
		// SPEC-FLEXSCAN-v1: a fonte própria do conector Flexscan (PRECOS_URL_FLEXSCAN), alerta à parte
		m.observarFonteDe(m.precosFlx, "FONTE_DEGRADADA:FLEXSCAN", "[flexscan] ", &m.fonteFlxRuimDesde, &m.fonteFlxMotivo)
	}
}

// observarFonteDe: uma fonte de preço; chave do alerta FONTE_DEGRADADA e prefixo do texto por fonte.
func (m *Motor) observarFonteDe(f preco.Fonte, chave, prefixo string, ruimDesde *time.Time, motivoFonte *string) {
	agora := m.agora()
	h := m.horaDoDia()
	motivo := ""
	// 24/10: só em dia de pregão (no sábado, com o pnt-md-hml parado e o 9090 sem preço, alertou o dia
	// todo); fora dele, como fora da janela: nada alerta e o que estiver aberto se resolve
	if pregao, _ := m.pregao.DiaDePregao(agora); pregao && h >= m.p.FonteJanelaInicio && h < m.p.FonteJanelaFim {
		motivo = m.motivoFonteRuim(f.Ler(), agora)
	}
	if motivo == "" {
		*ruimDesde, *motivoFonte = time.Time{}, ""
		m.al.Resolver(chave)
		return
	}
	if ruimDesde.IsZero() {
		*ruimDesde = agora
	}
	*motivoFonte = motivo
	if d, ok := f.(detalheFonte); ok {
		if x := d.MotivoFonte(); x != "" {
			*motivoFonte = motivo + "; fonte: " + x
		}
	}
	if dur := agora.Sub(*ruimDesde); dur >= m.p.FonteDegradada && !m.al.Ativo(chave) {
		m.al.Emitir("FONTE_DEGRADADA", chave, fmt.Sprintf("%sfonte de preco degradada ha %s: %s; entradas recusadas", prefixo,
			dur.Truncate(time.Second), *motivoFonte), 0)
	}
}

// saudeFonte: o bloco "fonte" do /saude.
func (m *Motor) saudeFonte() map[string]any {
	return m.saudeFonteDe("FONTE_DEGRADADA", m.fonteRuimDesde, m.fonteMotivo)
}

// saudeFonteFlx: o mesmo bloco para a fonte própria da Flexscan.
func (m *Motor) saudeFonteFlx() map[string]any {
	return m.saudeFonteDe("FONTE_DEGRADADA:FLEXSCAN", m.fonteFlxRuimDesde, m.fonteFlxMotivo)
}

func (m *Motor) saudeFonteDe(chave string, ruimDesde time.Time, motivo string) map[string]any {
	var desde any
	if !ruimDesde.IsZero() {
		desde = ruimDesde.Format(time.RFC3339)
	}
	pregao, porque := m.pregao.DiaDePregao(m.agora())
	r := map[string]any{
		"degradada":     m.al.Ativo(chave),
		"ruim_desde":    desde,
		"motivo":        motivo,
		"janela":        fmt.Sprintf("%s-%s", hhmm(m.p.FonteJanelaInicio), hhmm(m.p.FonteJanelaFim)),
		"dia_de_pregao": pregao,
	}
	if !pregao {
		r["sem_pregao"] = porque
	}
	return r
}

func hhmm(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}
