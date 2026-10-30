package motor

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// O conector da operação fora do ar no meio dela (SPEC-ORDENS v1.28 §3, 30/10). A operação usa
// sempre o caminho gravado na entrada (conector, ambiente, corretora e conta): com o canal dela fora
// (FIX caiu, Flexscan sem token ou sem resposta, conector desligado), nada sai por ela e não há saída
// automática por outro conector — a stop do canal original continua viva na corretora.
//   - Na hora: a saída, o ajuste do stop e as ações do admin são recusados (CONECTOR_FORA).
//   - Fora há mais de CONECTOR_FORA_TOLERANCIA_S, com posição ou ordem viva: a operação fica SUSPENSA
//     (gravada: sobrevive a um restart) e o alerta crítico CONECTOR_FORA_COM_POSICAO traz o que o
//     usuário precisa para agir à mão.
//   - Na volta, a conciliação não julga as ordens da operação suspensa (nada de DESCONHECIDA nem de
//     recriar a stop): sugere o caso (OPERACAO_SUSPENSA) e espera o admin confirmar a posição na
//     corretora (POST /operacoes/{id}/confirmar-posicao): 0 → ENCERRADA_FORA sem ordem (a stop do
//     serviço ainda viva lá é cancelada: a única ordem); a mesma do sistema → retoma; outra →
//     DIVERGENTE com os números. Nunca fecha sozinha.

// Estados do canal (o /saude e o /destino).
const (
	canalNoAr      = "NO_AR"
	canalFora      = "FORA"
	canalDesligado = "DESLIGADO"
	canalDormindo  = "DORMINDO" // a Flexscan fora da janela (v1.28 §1): sem sessão, sem ser queda
)

// estadoCanal: desde quando o conector está fora do ar.
type estadoCanal struct {
	foraDesde time.Time
	motivo    string
}

// canal: o estado do conector nesta instância, o motivo e desde quando (FORA).
func (m *Motor) canal(con string) (estado, motivo string, desde time.Time) {
	switch con {
	case dominio.ConectorPnT:
		if m.p.PnTDesligado || m.fix == nil {
			return canalDesligado, "o PnT está desligado nesta máquina (PNT_DESLIGADO)", timeZero
		}
	case dominio.ConectorFlexscan:
		if m.flx == nil {
			return canalDesligado, "a Flexscan não está configurada nesta instância (FLEXSCAN_URL)", timeZero
		}
	}
	if m.logado(con) {
		return canalNoAr, "", timeZero
	}
	if con == dominio.ConectorFlexscan {
		if d, ok := m.flx.(interface{ Dormindo() bool }); ok && d.Dormindo() {
			return canalDormindo, "fora da janela da Flexscan: loga na abertura", timeZero
		}
	}
	motivo, desde = m.quedas[con], m.agora()
	if c := m.canais[con]; c != nil {
		desde = c.foraDesde
		if motivo == "" {
			motivo = c.motivo
		}
	}
	if motivo == "" {
		motivo = "sem sessão"
	}
	return canalFora, motivo, desde
}

// canalForaDoAr: nada pode sair pelo conector (fora ou desligado; dormindo, o envio acorda a ponte).
func (m *Motor) canalForaDoAr(con string) bool {
	e, _, _ := m.canal(con)
	return e == canalFora || e == canalDesligado
}

func nomeConector(con string) string {
	if con == dominio.ConectorFlexscan {
		return "Flexscan"
	}
	return "PnT"
}

// registrarQueda: o motivo da queda do conector (Caiu da sessão FIX ou da ponte).
func (m *Motor) registrarQueda(con, motivo string) { m.quedas[con] = motivo }

// recusaCanal: a saída, o ajuste do stop e as ações do admin com o canal da operação fora do ar, ou
// com a operação suspensa: recusa na hora, nada fica guardado.
func (m *Motor) recusaCanal(op *operacao) *recusa {
	if est, motivo, _ := m.canal(op.conector()); est == canalFora || est == canalDesligado {
		return simples("CONECTOR_FORA", fmt.Sprintf("o conector %s da operação está fora do ar (%s): nada sai por ele nem por outro conector; para sair já, pela corretora (home broker, Profit, mesa)",
			nomeConector(op.conector()), motivo))
	}
	if !op.SuspensaEm.IsZero() {
		return simples("OPERACAO_SUSPENSA", "a operação está suspensa desde a queda do conector ("+op.SuspensaMotivo+"): aguardando o admin confirmar a posição na corretora")
	}
	return nil
}

// vivaNaCorretora: a ordem chegou à corretora e não terminou.
func (o *ordem) vivaNaCorretora() bool { return o.viva() && o.Estado != dominio.Enviando }

func (op *operacao) temVivaNaCorretora() bool {
	for _, o := range op.ordens {
		if o.vivaNaCorretora() {
			return true
		}
	}
	return false
}

// tickCanais: o estado de cada conector e a suspensão das operações com o canal fora há mais da
// tolerância (a toda volta do relógio, com ou sem sessão).
func (m *Motor) tickCanais(agora time.Time) {
	for _, con := range conectores {
		if m.logado(con) {
			if c := m.canais[con]; c != nil {
				slog.Info("conector de volta", "conector", con, "fora_por", agora.Sub(c.foraDesde))
				delete(m.canais, con)
			}
			delete(m.quedas, con)
			continue
		}
		if m.canais[con] == nil {
			m.canais[con] = &estadoCanal{foraDesde: agora, motivo: m.quedas[con]}
		}
	}
	for _, op := range m.ops {
		if op.Estado.Final() {
			continue
		}
		if op.encerrandoFora {
			m.terminarForaSePronto(op)
			continue
		}
		if !op.SuspensaEm.IsZero() || (op.posicao() == 0 && !op.temVivaNaCorretora()) {
			continue
		}
		est, motivo, desde := m.canal(op.conector())
		if est != canalFora && est != canalDesligado {
			continue
		}
		if est == canalFora && agora.Sub(desde) < m.p.CanalForaTolerancia {
			continue
		}
		if est == canalDesligado {
			desde = agora
		}
		m.suspender(op, motivo, desde)
	}
}

// suspender: o canal da operação caiu com posição ou ordem viva.
func (m *Motor) suspender(op *operacao, motivo string, desde time.Time) {
	op.SuspensaEm = m.agora()
	op.SuspensaMotivo = fmt.Sprintf("%s fora do ar desde %s: %s", nomeConector(op.conector()), desde.In(relogio.Fuso).Format("15:04:05"), motivo)
	op.sugestao = ""
	m.salvarOp(op)
	slog.Warn("operação suspensa: o conector caiu com posição", "op", op.OperacaoID, "conector", op.conector(), "motivo", motivo, "posicao", op.posicao())
	m.al.Emitir("CONECTOR_FORA_COM_POSICAO", chaveOp("CONECTOR_FORA", op), m.textoCanalFora(op, motivo, desde), op.OperacaoID)
	d := m.resumoParaMao(op)
	d["motivo"] = op.SuspensaMotivo
	m.publicar("SUSPENSA", op, d)
}

// resumoParaMao: o que o usuário precisa para agir à mão (o evento SUSPENSA e o /saude).
func (m *Motor) resumoParaMao(op *operacao) map[string]any {
	pos := op.posicao()
	d := map[string]any{"conector": op.conector(), "corretora": usuario.Corretora(op.Destino), "corretora_codigo": op.Destino,
		"conta": op.Conta, "simbolo": op.Simbolo, "posicao": pos, "lado": nil, "qtd": abs(pos), "preco_medio": nil, "stop": nil}
	if pos != 0 {
		d["lado"] = map[bool]string{true: "C", false: "V"}[pos > 0]
	}
	if pm, ok := op.medio(true); ok && pos != 0 {
		d["preco_medio"] = pm
	}
	if p := op.viva(dominio.Protecao); p != nil {
		d["stop"] = map[string]any{"clordid": p.ClOrdAtual, "preco_stop": p.Stop, "preco_limite": p.StopLim, "qtd": p.Qtd}
	}
	return d
}

// textoCanalFora: o alerta crítico com tudo para o usuário agir à mão.
func (m *Motor) textoCanalFora(op *operacao, motivo string, desde time.Time) string {
	var b strings.Builder
	pos := op.posicao()
	corretora := usuario.Corretora(op.Destino)
	if corretora == "" {
		corretora = fmt.Sprintf("corretora %d", op.Destino)
	}
	fmt.Fprintf(&b, "operação %d (uid %d): o conector %s está fora do ar desde %s (%s). ", op.OperacaoID, op.UID,
		nomeConector(op.conector()), desde.In(relogio.Fuso).Format("15:04:05"), motivo)
	if pos != 0 {
		lado := "comprado"
		if pos < 0 {
			lado = "vendido"
		}
		pm, _ := op.medio(true)
		fmt.Fprintf(&b, "Posição: %s %d %s, preço médio %s, na %s, conta %s. ", lado, abs(pos), op.Simbolo, pm.String(), corretora, op.Conta)
	} else {
		fmt.Fprintf(&b, "Sem posição executada; na %s, conta %s. ", corretora, op.Conta)
	}
	switch p := op.viva(dominio.Protecao); {
	case p != nil:
		fmt.Fprintf(&b, "Protegendo: a stop %s (gatilho %d, limite %d, %d contrato(s)) segue valendo na corretora. ", p.ClOrdAtual, p.Stop, p.StopLim, p.Qtd)
	case pos != 0:
		b.WriteString("Sem stop na corretora: a posição está desprotegida. ")
	}
	if e := op.viva(dominio.Entrada); e != nil {
		fmt.Fprintf(&b, "Entrada %s viva na corretora (%d de %d executados). ", e.ClOrdAtual, e.QtdExec, e.Qtd)
	}
	b.WriteString("O serviço não manda nada por esse canal enquanto ele estiver fora, nem sai por outro conector. Para sair já: home broker, Profit ou mesa/atendimento da corretora. ")
	fmt.Fprintf(&b, "A operação fica SUSPENSA até o admin confirmar a posição na corretora (kalman-ordens operacao confirmar -op %d -posicao N).", op.OperacaoID)
	return b.String()
}

// marcarAusentesDaVolta: na conciliação, as ordens vivas da operação suspensa que a corretora não
// tem (sem julgar: nada de DESCONHECIDA nem de recriar a stop). julgavel = a conta respondeu e o
// conector tem sessão.
func (m *Motor) marcarAusentesDaVolta(op *operacao, vistas map[*ordem]bool, julgavel bool) {
	for _, o := range op.ordens {
		if !o.vivaNaCorretora() || o.seq > m.massCorte {
			continue
		}
		o.ausenteNaVolta = julgavel && !vistas[o]
	}
}

// avaliarSuspensas: depois da conciliação, a sugestão de cada operação suspensa com o canal de volta.
func (m *Motor) avaliarSuspensas(semResposta map[string]bool) {
	for _, op := range m.ops {
		if op.Estado.Final() || op.SuspensaEm.IsZero() || op.encerrandoFora || m.canalForaDoAr(op.conector()) || m.massSemSessao[op.conector()] {
			continue
		}
		op.sugestao = m.sugestaoDaVolta(op, semResposta[op.Conta])
		m.al.Resolver(chaveOp("CONECTOR_FORA", op))
		zero := ""
		if p := op.viva(dominio.Protecao); p != nil && !p.ausenteNaVolta {
			zero = ", com o cancelamento da stop que ainda está viva lá"
		}
		m.al.Emitir("OPERACAO_SUSPENSA", chaveOp("SUSPENSA", op), fmt.Sprintf(
			"operação %d (uid %d): o conector %s voltou; a operação segue SUSPENSA (nada sai por ela). A conciliação sugere: %s. Confirme a posição na corretora (conta %s): kalman-ordens operacao confirmar -op %d -posicao N -motivo ... — 0 = zerada fora, encerra sem ordem%s; %d (a do sistema) = retoma; outra = divergência",
			op.OperacaoID, op.UID, nomeConector(op.conector()), op.sugestao, op.Conta, op.OperacaoID, zero, op.posicao()), op.OperacaoID)
		d := m.resumoParaMao(op)
		d["sugestao"] = op.sugestao
		m.publicar("SUSPENSA_CONFERIR", op, d)
	}
}

// sugestaoDaVolta: o que a conciliação viu (nunca decide).
func (m *Motor) sugestaoDaVolta(op *operacao, semResposta bool) string {
	if semResposta {
		return "a corretora não respondeu à consulta das ordens da conta: confira à mão"
	}
	p, e := op.viva(dominio.Protecao), op.viva(dominio.Entrada)
	// a última stop, se não está viva: o cancelamento de fora chegou pelo reenvio do PnT ou pela consulta
	var ultima *ordem
	for _, o := range op.ordens {
		if o.Papel == dominio.Protecao {
			ultima = o
		}
	}
	switch {
	case p != nil && !p.ausenteNaVolta:
		return "stop viva na corretora, sem fill: a posição provavelmente continua aberta"
	case p != nil:
		return "stop cancelada fora sem fill: provável zeragem manual"
	case ultima != nil && op.posicao() != 0 && ultima.QtdExec == 0 && (ultima.Estado == dominio.Cancelada || ultima.Estado == dominio.Expirada):
		return "stop cancelada fora sem fill: provável zeragem manual"
	case e != nil && e.ausenteNaVolta:
		return "entrada cancelada fora: provável intervenção manual"
	case e != nil:
		return "entrada viva na corretora"
	case op.posicao() != 0:
		return "sem stop na corretora antes da queda: não dá para saber; confira a posição"
	}
	return "sem posição nem ordem viva no sistema"
}

// limparSuspensao: a operação volta ao fluxo normal (zerada, encerrada fora ou retomada).
func (m *Motor) limparSuspensao(op *operacao) {
	if op.SuspensaEm.IsZero() {
		return
	}
	op.SuspensaEm, op.SuspensaMotivo, op.sugestao = timeZero, "", ""
	m.salvarOp(op)
	for _, k := range []string{"CONECTOR_FORA", "SUSPENSA", "DIVERGENTE_POSICAO"} {
		m.al.Resolver(chaveOp(k, op))
	}
}

// liberadaNaSuspensao: o envio pode sair da operação suspensa? Só o cancelamento que a confirmação
// de posição 0 mandou.
func (m *Motor) bloqueadoNaSuspensao(e envioPendente) bool {
	if e.o == nil {
		return false
	}
	op := m.donoDa[e.o]
	if op == nil || op.SuspensaEm.IsZero() || op.liberadas[e.o] {
		return false
	}
	slog.Warn("operação suspensa: nada sai por ela", "op", op.OperacaoID, "clordid", e.clordid)
	return true
}

// AcaoConfirmarPosicao (v1.28 §3): POST /operacoes/{id}/confirmar-posicao.
const AcaoConfirmarPosicao = "CONFIRMAR_POSICAO"

// confirmarPosicao: o admin informa a posição na corretora da operação suspensa (com sinal: +
// comprado, − vendido).
func (m *Motor) confirmarPosicao(op *operacao, a AcaoAdmin) (Resposta, *recusa) {
	if a.Posicao == nil {
		return nil, simples("CAMPO_INVALIDO", "posicao obrigatória: a posição na corretora, com sinal (+ comprado, - vendido; 0 = zerada)")
	}
	if op.SuspensaEm.IsZero() {
		return nil, simples("OPERACAO_NAO_SUSPENSA", "a operação não está suspensa")
	}
	if est, motivo, _ := m.canal(op.conector()); est != canalNoAr {
		return nil, simples("CONECTOR_FORA", fmt.Sprintf("o conector %s ainda está fora do ar (%s): confirme depois da volta e da conciliação", nomeConector(op.conector()), motivo))
	}
	if m.conciliando {
		return nil, simples("CONCILIANDO", "a conciliação da volta ainda está em curso: tente de novo em instantes")
	}
	sis, cor := op.posicao(), *a.Posicao
	r := Resposta{"aceito": true, "modulo": op.Modulo, "operacao_id": op.OperacaoID, "acao": AcaoConfirmarPosicao,
		"posicao_sistema": sis, "posicao_corretora": cor, "sugestao": nuloSeVazio(op.sugestao)}
	if op.encerrandoFora {
		r["codigo"], r["motivo"] = "JA_ENCERRANDO", "a confirmação de posição 0 já foi aceita: esperando o cancelamento da stop"
		return r, nil
	}
	m.acaoRegistrada(op, AcaoConfirmarPosicao, a)
	switch {
	case cor == 0:
		op.encerrandoFora, op.porFora = true, a.Motivo
		op.liberadas = map[*ordem]bool{}
		for _, o := range op.ordens {
			if !o.viva() {
				continue
			}
			if o.ausenteNaVolta || o.Estado == dominio.Enviando {
				m.canceladaFora(o)
				continue
			}
			op.liberadas[o] = true
			m.cancelar(o)
		}
		if m.terminarForaSePronto(op) {
			r["codigo"], r["motivo"] = "ENCERRADA_FORA", "operacao encerrada fora do sistema, sem ordem"
		} else {
			r["codigo"], r["motivo"] = "CANCELANDO_STOP", "a stop do servico ainda esta viva na corretora: cancelamento enviado (a unica ordem); a operacao fecha ENCERRADA_FORA quando ele confirmar"
		}
	case cor == sis:
		for _, o := range op.ordens {
			if o.viva() && o.ausenteNaVolta {
				m.canceladaFora(o)
			}
		}
		m.limparSuspensao(op)
		m.publicar("RETOMADA", op, map[string]any{"posicao": sis, "motivo": a.Motivo, "por": *a.Por})
		if sis != 0 && op.viva(dominio.Protecao) == nil && !op.Estado.Saindo() {
			m.mudar(op, dominio.PosicionadaSemProtecao)
			op.semProtecaoDesde = m.agora()
			m.ajustarProtecao(op)
		}
		r["codigo"], r["motivo"] = "RETOMADA", "a posicao confere: a operacao volta ao fluxo normal"
	default:
		op.Divergente = true
		m.salvarOp(op)
		m.al.Emitir("DIVERGENTE", chaveOp("DIVERGENTE_POSICAO", op), fmt.Sprintf(
			"operação %d (uid %d, conta %s): posição %d no sistema e %d na corretora (informado por %d: %s); a operação segue SUSPENSA, nada sai por ela",
			op.OperacaoID, op.UID, op.Conta, sis, cor, *a.Por, a.Motivo), op.OperacaoID)
		r["aceito"], r["codigo"], r["motivo"] = false, "POSICAO_DIVERGENTE", fmt.Sprintf("posicao %d no sistema e %d na corretora: a operacao segue suspensa", sis, cor)
	}
	return r, nil
}

// canceladaFora: a ordem que a corretora não tem mais (cancelada fora do sistema).
func (m *Motor) canceladaFora(o *ordem) {
	o.Estado = dominio.Cancelada
	o.ausenteNaVolta = false
	limparPendencia(o)
	m.salvarOrdem(o)
	slog.Info("ordem cancelada fora do sistema", "clordid", o.ClOrdAtual)
}

// terminarForaSePronto: sem ordem viva, a operação confirmada com posição 0 fecha ENCERRADA_FORA.
func (m *Motor) terminarForaSePronto(op *operacao) bool {
	if !op.encerrandoFora {
		return false
	}
	for _, o := range op.ordens {
		if o.viva() {
			return false
		}
	}
	sis := op.posicao()
	op.encerrandoFora, op.liberadas = false, nil
	op.MotivoSaida, op.OrigemSaida = "encerrada fora do sistema: "+op.porFora, "FORA"
	if op.SaidaPedidaEm.IsZero() {
		op.SaidaPedidaEm = m.agora()
	}
	m.limparSuspensao(op)
	op.PnL = op.pnlRealizado()
	op.zeradaEm = m.agora()
	m.mudar(op, dominio.EncerradaFora)
	for _, k := range []string{"SEM_PROTECAO", "STOP_FURADO", "SAIDA_ESGOTOU", "SAIDA_EM_LEILAO", "CANCEL_SEM_RESPOSTA", "SAIDA_SEM_PRECO", "SAIDA_SEM_FILL", "SAIDA_DIVERGENTE", "SAIDA_BLOQUEADA", "PROTECAO_RECRIADA", "PROTECAO_INSTAVEL", "PROTECAO_EM_ERRO", "PROTECAO_EM_ERRO_PRESA", "CONTA_ALTERADA", "SIMBOLO"} {
		m.al.Resolver(chaveOp(k, op))
	}
	m.publicar("ENCERRADA_FORA", op, map[string]any{"posicao_sistema": sis, "motivo": op.porFora, "conta": op.Conta, "conector": op.conector()})
	slog.Warn("operação encerrada fora do sistema", "op", op.OperacaoID, "posicao_sistema", sis)
	return true
}

// canaisSaude: o bloco "canais" do /saude.
func (m *Motor) canaisSaude() map[string]any {
	r := map[string]any{}
	for _, con := range conectores {
		est, motivo, desde := m.canal(con)
		c := map[string]any{"estado": est, "motivo": nuloSeVazio(motivo), "desde": nil}
		if est == canalFora {
			c["desde"] = emBR(desde)
		}
		r[con] = c
	}
	return r
}

// suspensasSaude: as operações suspensas, com o que o usuário precisa para agir à mão.
func (m *Motor) suspensasSaude() []map[string]any {
	r := []map[string]any{}
	for _, op := range m.ops {
		if op.Estado.Final() || op.SuspensaEm.IsZero() {
			continue
		}
		d := m.resumoParaMao(op)
		d["modulo"], d["operacao_id"], d["uid"] = op.Modulo, op.OperacaoID, op.UID
		d["desde"], d["motivo"], d["sugestao"] = emBR(op.SuspensaEm), op.SuspensaMotivo, nuloSeVazio(op.sugestao)
		r = append(r, d)
	}
	return r
}

func nuloSeVazio(s string) any {
	if s == "" {
		return nil
	}
	return s
}
