package motor

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/preco"
)

// Proteção contra falha de fonte (21/10, item 2): FONTE_DEGRADADA, crítico, quando a fonte de preço
// fica ruim por mais de FONTE_DEGRADADA_S (30) dentro de FONTE_JANELA (09:00-18:25); resolvido quando
// volta (ou quando a janela fecha). Ruim = sem resposta, {} (o pnt-md-hml responde {} com book
// degenerado ou atraso acima da base), 404/400; em negociação, sem bid/ask, book cruzado ou preço
// mais velho que PRECO_IDADE_MAX_S. Leilão (status84 != 0) com preço, bid ou ask não é falha.
//
// v1.17 (26/10): mercado travado não é falha da fonte. Em negociação (status84 0), bid = ask ou book
// de um lado só → MERCADO_TRAVADO, crítico, no mesmo prazo e janela; o preço parado (sem negócio) não
// conta como preço velho enquanto durar. 26/10 09:17: o WIN parou no limite de baixa (177.215,
// −14,90%) só com ofertas de venda; a 9090 mostrou bid = ask = 177.215 (o bid preso no último valor)
// e o FONTE_DEGRADADA:FLEXSCAN dizia "book cruzado".

// estadoFonte: o que se observa de uma fonte (a do PnT ou a própria da Flexscan).
type estadoFonte struct {
	chave, chaveTravado, prefixo string
	ruimDesde                    time.Time
	motivo                       string
	travadoDesde                 time.Time
	travadoMotivo                string
	travadoPresos                int
}

// travamento: o mercado em negociação parado num preço, com o book de um lado só ou bid = ask.
type travamento struct {
	px     decimal.Decimal
	texto  string // o que a fonte mostra
	presos int    // a posição que não sai: +1 comprada (sem compradores), -1 vendida (sem vendedores), 0 = a fonte não diz o lado (bid = ask)
}

// mercadoTravado: nil = não travado. Num pregão contínuo bid = ask não persiste (os dois
// executariam): ou o mercado está no limite de oscilação com um lado só e o outro preso no último
// valor (26/10), ou a fonte está errada — os dois casos são críticos.
func mercadoTravado(l preco.Leitura) *travamento {
	if l.Situacao != preco.OK || l.Status84 != 0 {
		return nil
	}
	switch {
	case l.Bid != nil && l.Ask != nil && l.Bid.Equal(*l.Ask):
		return &travamento{px: *l.Ask, texto: fmt.Sprintf("bid = ask = %s", l.Ask)}
	case l.Bid == nil && l.Ask != nil:
		return &travamento{px: *l.Ask, texto: fmt.Sprintf("so ofertas de venda (ask %s, sem bid)", l.Ask), presos: 1}
	case l.Ask == nil && l.Bid != nil:
		return &travamento{px: *l.Bid, texto: fmt.Sprintf("so ofertas de compra (bid %s, sem ask)", l.Bid), presos: -1}
	}
	return nil
}

// motivo: o texto do MERCADO_TRAVADO.
func (t *travamento) motivo(l preco.Leitura, agora time.Time) string {
	s := fmt.Sprintf("mercado travado em %s: %s", t.px, t.texto)
	switch t.presos {
	case 1:
		s += "; sem compradores (limite de baixa?)"
	case -1:
		s += "; sem vendedores (limite de alta?)"
	default:
		s += " (limite de oscilacao? a fonte nao diz o lado)"
	}
	return s + fmt.Sprintf("; fonte sem mudanca ha %.0f s", l.Idade(agora).Seconds())
}

// motivoFonteRuim: "" = fonte boa.
func (m *Motor) motivoFonteRuim(l preco.Leitura, agora time.Time) string {
	if mercadoTravado(l) != nil {
		return "" // não é a fonte: é o mercado (MERCADO_TRAVADO)
	}
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
	m.observarFonteDe(m.precos, &m.fonte)
	if m.precosFlx != nil {
		// SPEC-FLEXSCAN-v1: a fonte própria do conector Flexscan (PRECOS_URL_FLEXSCAN), alerta à parte
		m.observarFonteDe(m.precosFlx, &m.fonteFlx)
	}
}

// observarFonteDe: uma fonte de preço — FONTE_DEGRADADA e MERCADO_TRAVADO, com chave e prefixo do
// texto por fonte.
func (m *Motor) observarFonteDe(f preco.Fonte, e *estadoFonte) {
	agora := m.agora()
	h := m.horaDoDia()
	motivo, travado := "", ""
	// 24/10: só em dia de pregão (no sábado, com o pnt-md-hml parado e o 9090 sem preço, alertou o dia
	// todo); fora dele, como fora da janela: nada alerta e o que estiver aberto se resolve
	if pregao, _ := m.pregao.DiaDePregao(agora); pregao && h >= m.p.FonteJanelaInicio && h < m.p.FonteJanelaFim {
		l := f.Ler()
		motivo = m.motivoFonteRuim(l, agora)
		if t := mercadoTravado(l); t != nil {
			travado = t.motivo(l, agora)
			e.travadoPresos = t.presos
		}
	}
	m.observarTravado(f, e, travado, agora)
	if motivo == "" {
		e.ruimDesde, e.motivo = time.Time{}, ""
		m.al.Resolver(e.chave)
		return
	}
	if e.ruimDesde.IsZero() {
		e.ruimDesde = agora
	}
	e.motivo = motivo
	if d, ok := f.(detalheFonte); ok {
		if x := d.MotivoFonte(); x != "" {
			e.motivo = motivo + "; fonte: " + x
		}
	}
	if dur := agora.Sub(e.ruimDesde); dur >= m.p.FonteDegradada && !m.al.Ativo(e.chave) {
		m.al.Emitir("FONTE_DEGRADADA", e.chave, fmt.Sprintf("%sfonte de preco degradada ha %s: %s; entradas recusadas", e.prefixo,
			dur.Truncate(time.Second), e.motivo), 0)
	}
}

// observarTravado: MERCADO_TRAVADO depois de FONTE_DEGRADADA_S; o texto lista as posições desta
// fonte — no lado travado nem o stop nem a saída executam até o mercado destravar.
func (m *Motor) observarTravado(f preco.Fonte, e *estadoFonte, motivo string, agora time.Time) {
	if motivo == "" {
		e.travadoDesde, e.travadoMotivo = time.Time{}, ""
		m.al.Resolver(e.chaveTravado)
		return
	}
	if e.travadoDesde.IsZero() {
		e.travadoDesde = agora
	}
	e.travadoMotivo = motivo
	dur := agora.Sub(e.travadoDesde)
	if dur < m.p.FonteDegradada || m.al.Ativo(e.chaveTravado) {
		return
	}
	var pos []string
	for _, op := range m.ops {
		if op.Estado.Final() || op.posicao() == 0 || m.precosDo(op.conector()) != f {
			continue
		}
		lado := "comprada"
		if op.posicao() < 0 {
			lado = "vendida"
		}
		pos = append(pos, fmt.Sprintf("operacao %d %s %d", op.OperacaoID, lado, abs(op.posicao())))
	}
	texto := fmt.Sprintf("%s%s (travado ha %s); entradas recusadas", e.prefixo, motivo, dur.Truncate(time.Second))
	if len(pos) == 0 {
		texto += "; sem posicao"
	} else {
		presa := "a posicao do lado travado (comprada se o limite e de baixa, vendida se de alta)"
		switch e.travadoPresos {
		case 1:
			presa = "a posicao comprada"
		case -1:
			presa = "a posicao vendida"
		}
		texto += fmt.Sprintf("; %s; %s nao sai: nem o stop nem a saida executam ate o mercado destravar", strings.Join(pos, ", "), presa)
	}
	m.al.Emitir("MERCADO_TRAVADO", e.chaveTravado, texto, 0)
}

// saudeFonte: o bloco "fonte" do /saude.
func (m *Motor) saudeFonte() map[string]any { return m.saudeFonteDe(&m.fonte) }

// saudeFonteFlx: o mesmo bloco para a fonte própria da Flexscan.
func (m *Motor) saudeFonteFlx() map[string]any { return m.saudeFonteDe(&m.fonteFlx) }

func (m *Motor) saudeFonteDe(e *estadoFonte) map[string]any {
	var desde, travadoDesde any
	if !e.ruimDesde.IsZero() {
		desde = e.ruimDesde.Format(time.RFC3339)
	}
	if !e.travadoDesde.IsZero() {
		travadoDesde = e.travadoDesde.Format(time.RFC3339)
	}
	pregao, porque := m.pregao.DiaDePregao(m.agora())
	r := map[string]any{
		"degradada":      m.al.Ativo(e.chave),
		"ruim_desde":     desde,
		"motivo":         e.motivo,
		"janela":         fmt.Sprintf("%s-%s", hhmm(m.p.FonteJanelaInicio), hhmm(m.p.FonteJanelaFim)),
		"dia_de_pregao":  pregao,
		"travado":        m.al.Ativo(e.chaveTravado),
		"travado_desde":  travadoDesde,
		"travado_motivo": e.travadoMotivo,
	}
	if !pregao {
		r["sem_pregao"] = porque
	}
	return r
}

func hhmm(d time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}
