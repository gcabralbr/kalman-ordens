package motor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/quickfixgo/quickfix"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// Volta (SPEC v1.10): o motor tira itens das três filas por prioridade estrita, trata vários numa
// volta, grava tudo o que mudou numa transação só (lote) e, depois do commit, envia ao PnT por
// prioridade (1014 > cancel/saída > entrada), com a entrada sujeita ao limitador de taxa. As
// respostas HTTP e os Fazer síncronos saem depois do commit e do envio.

// Classes de entrada (prioridade estrita: P0 sempre antes de P1, P1 antes de P2).
const (
	classeP0 = iota // o que vem do PnT, eventos de sessão, tick, consultas e paradas
	classeP1        // pedidos de saída, ajuste do stop, ações de admin
	classeP2        // pedidos de entrada
)

// Prioridade de envio ao PnT.
const (
	prioProtecao = iota // 1014: nova, recriada, replace
	prioSaida           // cancel, 1031 de saída e o replace dela, mass status
	prioEntrada         // 1031 de entrada (limitador de taxa)
)

const (
	maxItensPorVolta = 256
	maxDuracaoVolta  = 20 * time.Millisecond
	capFilaP0        = 1 << 20 // nunca bloqueia a goroutine da sessão FIX
	capFilaP1        = 100000
	capFilaP2        = 100000
	// maxEnviosPorVolta: cancel, saída e entrada enviados por volta (a 1014 e o mass status não
	// contam); o resto sai na volta seguinte — uma rajada de 4.000 não prende o motor no envio
	// enquanto os fills (e as 1014 deles) esperam.
	maxEnviosPorVolta = 250
	// maxP0ParaEntrada: com mais que isto na P0 (fills e respostas do PnT esperando o motor), a
	// entrada espera — mais entrada agora só traria mais fills para a frente das 1014.
	maxP0ParaEntrada = 200
)

// envioPendente: uma mensagem decidida numa volta, enviada depois do commit.
type envioPendente struct {
	o            *ordem // nil: mass status
	msg          *quickfix.Message
	clordid      string
	prio         int
	pedidoEm     time.Time // métrica pedido_ate_envio (o pedido HTTP que causou)
	pedidoRotulo string
	decididoEm   time.Time // tempo real, métrica de espera pela taxa
	decididoNo   time.Time // relógio do motor: a validade da entrada na fila (ENTRADA_TTL_FILA_S)
	aoEnviar     func()
	aoFalhar     func()
	aoExpirar    func() // entrada que passou de ENTRADA_TTL_FILA_S esperando a taxa: descartada
	nova         bool   // NewOrderSingle da ordem (não cancel/replace): retirarDaFila só casa esta
	daRetencao   bool   // solta pelo disjuntor na volta da bolsa
	con          string // conector (SPEC-FLEXSCAN-v1); "" = o da operação da ordem (mass status: obrigatório)
}

// conectorDo: o conector por onde o envio sai — o dele, o da operação da ordem, ou o PnT.
func (m *Motor) conectorDo(e envioPendente) string {
	if e.con != "" {
		return e.con
	}
	if e.o != nil {
		if op := m.donoDa[e.o]; op != nil {
			return op.conector()
		}
	}
	return dominio.ConectorPnT
}

// estadoVolta: o que a volta acumula até o commit.
type estadoVolta struct {
	opsSujas    map[*operacao]bool
	ordensSujas map[*ordem]bool
	eventos     []repo.EventoFIX
	publicados  []repo.PublicadoNovo
	estados     []repo.EstadoNovo
	sessao      []repo.EventoSessao
	envios      []envioPendente
	enviados    int      // envios limitados desta volta (maxEnviosPorVolta), nas duas rodadas
	respostas   []func() // v1.22: respostas HTTP, antes da gravação
	// respostas que esperam o envio de uma entrada da volta (depois da gravação)
	respostasTarde []func()
	aposVolta      []func()

	muPedidos sync.Mutex
	pedidos   []repo.PedidoHTTP // vêm das goroutines HTTP
}

func classeDaOrigem(origem string) int {
	switch origem {
	case "http_entrada":
		return classeP2
	case "http_saida":
		return classeP1
	}
	return classeP0
}

func (m *Motor) filaDa(classe int) chan func() {
	switch classe {
	case classeP1:
		return m.p1
	case classeP2:
		return m.p2
	}
	return m.p0
}

// proxima: o próximo item, por prioridade estrita; bloquear=false devolve ok=false sem item.
func (m *Motor) proxima(bloquear bool) (func(), bool) {
	select {
	case f := <-m.p0:
		return f, true
	default:
	}
	select {
	case f := <-m.p0:
		return f, true
	case f := <-m.p1:
		return f, true
	default:
	}
	select {
	case f := <-m.p0:
		return f, true
	case f := <-m.p1:
		return f, true
	case f := <-m.p2:
		return f, true
	default:
	}
	if !bloquear {
		return nil, false
	}
	select {
	case <-m.fim:
		return nil, false
	case f := <-m.p0:
		return f, true
	case f := <-m.p1:
		return f, true
	case f := <-m.p2:
		return f, true
	}
}

func (m *Motor) laco() {
	for {
		f, ok := m.proxima(true)
		if !ok {
			return
		}
		m.protegido(f)
		inicio := time.Now()
		for n := 1; n < maxItensPorVolta && time.Since(inicio) < maxDuracaoVolta; n++ {
			f, ok := m.proxima(false)
			if !ok {
				break
			}
			m.protegido(f)
		}
		m.protegido(m.fecharVolta)
	}
}

// aposVolta: f roda depois do commit e do envio desta volta (Fazer, Parar, consultas do admin).
func (m *Motor) aposVolta(f func()) { m.v.aposVolta = append(m.v.aposVolta, f) }

// responder (v1.22): a resposta HTTP de um pedido sai no fim da volta, antes da gravação do lote
// (o banco lento não segura o gestor; a resposta vem da memória do motor, a mesma do GET /ordens).
// Com uma entrada desta volta esperando o envio (ela sai depois da gravação), a resposta espera o
// envio, como antes: a entrada que não sai muda a resposta (ENVIO_FALHOU).
func (m *Motor) responder(f func()) {
	if slices.ContainsFunc(m.v.envios, func(e envioPendente) bool { return e.prio == prioEntrada }) {
		m.v.respostasTarde = append(m.v.respostasTarde, f)
		return
	}
	m.v.respostas = append(m.v.respostas, f)
}

// loteLento (v1.22): a gravação do lote acima disso deixa um WARN no journal.
const loteLento = time.Second

// fecharVolta (v1.22): 1014, saída, cancel e mass status saem antes da gravação (não esperam o
// banco), as respostas HTTP também; depois grava o lote, envia as entradas (gravar antes de enviar,
// v1.10 B, segue valendo para a entrada) e roda o aposVolta. Um envio que sai e não chega ao banco
// (o processo cai no meio) é achado pela conciliação da partida (conciliacao.go, reconstituir).
func (m *Motor) fecharVolta() {
	m.v.enviados = 0
	m.despachar(true)
	rs := m.v.respostas
	m.v.respostas = nil
	for _, f := range rs {
		f()
	}
	l := m.montarLote()
	if !l.Vazio() {
		t0 := time.Now()
		err := m.repo.GravarLote(l)
		if err != nil {
			// uma nova tentativa na hora (ex.: conexão que caiu)
			slog.Warn("gravação do lote falhou, tentando de novo", "erro", err, "linhas", l.Linhas())
			err = m.repo.GravarLote(l)
		}
		dur := time.Since(t0)
		metricas.Lote.Observar(dur.Seconds())
		metricas.LoteLinhas.Observar(float64(l.Linhas()))
		if dur >= loteLento {
			slog.Warn("gravação do lote lenta", "duracao_ms", dur.Milliseconds(), "linhas", l.Linhas())
		}
		if err != nil && repo.ErroDeDado(err) {
			// v1.13: erro de dado não se resolve repetindo: linha a linha, a ruim para a quarentena
			slog.Error("gravação do lote: erro de dado, gravando linha a linha", "erro", err, "linhas", l.Linhas())
			m.isolarLote(l)
		} else if err != nil {
			// o que não gravou fica para a próxima volta; o envio segue (proteção não espera o banco)
			slog.Error("gravação do lote falhou", "erro", err, "linhas", l.Linhas())
			m.al.Emitir("ERRO_INTERNO", "BANCO_LOTE", "gravação no banco falhou (o envio à corretora seguiu; nova tentativa na próxima volta): "+cortarTexto(err.Error(), 300), 0)
			m.devolverAoLote(l)
		} else {
			m.al.Resolver("BANCO_LOTE")
			m.loteGravado(l)
		}
	}
	m.despachar(false)
	// as que esperavam a entrada, e o que a gravação ou o envio decidiram, respondem nesta volta
	rs = append(m.v.respostasTarde, m.v.respostas...)
	m.v.respostas, m.v.respostasTarde = nil, nil
	for _, f := range rs {
		f()
	}
	fs := m.v.aposVolta
	m.v.aposVolta = nil
	for _, f := range fs {
		f()
	}
}

// montarLote: só o que mudou desde a última gravação (comparado com a cópia gravada).
func (m *Motor) montarLote() *repo.Lote {
	l := &repo.Lote{Eventos: m.v.eventos, Publicados: m.v.publicados, Estados: m.v.estados, Sessao: m.v.sessao}
	m.v.eventos, m.v.publicados, m.v.estados, m.v.sessao = nil, nil, nil, nil
	m.v.muPedidos.Lock()
	l.Pedidos, m.v.pedidos = m.v.pedidos, nil
	m.v.muPedidos.Unlock()
	for op := range m.v.opsSujas {
		c := op.Operacao.Copia()
		switch {
		case !op.persistida:
			l.NovasOperacoes = append(l.NovasOperacoes, c)
		case !reflect.DeepEqual(c, op.gravada):
			l.Operacoes = append(l.Operacoes, c)
		}
	}
	for o := range m.v.ordensSujas {
		c := o.Ordem
		switch {
		case !o.persistida:
			l.NovasOrdens = append(l.NovasOrdens, c)
		case !reflect.DeepEqual(c, o.gravada):
			l.Ordens = append(l.Ordens, c)
		}
	}
	// em ordem de ID (os mapas não têm ordem): o banco e a memória ficam na ordem de criação
	sort.Slice(l.NovasOperacoes, func(i, j int) bool { return l.NovasOperacoes[i].ID < l.NovasOperacoes[j].ID })
	sort.Slice(l.Operacoes, func(i, j int) bool { return l.Operacoes[i].ID < l.Operacoes[j].ID })
	sort.Slice(l.NovasOrdens, func(i, j int) bool { return l.NovasOrdens[i].ID < l.NovasOrdens[j].ID })
	sort.Slice(l.Ordens, func(i, j int) bool { return l.Ordens[i].ID < l.Ordens[j].ID })
	return l
}

func (m *Motor) loteGravado(l *repo.Lote) {
	porIDOp := map[int64]dominio.Operacao{}
	for _, c := range append(append([]dominio.Operacao{}, l.NovasOperacoes...), l.Operacoes...) {
		porIDOp[c.ID] = c
	}
	porIDOrdem := map[int64]dominio.Ordem{}
	for _, c := range append(append([]dominio.Ordem{}, l.NovasOrdens...), l.Ordens...) {
		porIDOrdem[c.ID] = c
	}
	for op := range m.v.opsSujas {
		op.persistida = true
		if c, ok := porIDOp[op.ID]; ok {
			op.gravada = c
		}
	}
	for o := range m.v.ordensSujas {
		o.persistida = true
		if c, ok := porIDOrdem[o.ID]; ok {
			o.gravada = c
		}
	}
	m.v.opsSujas, m.v.ordensSujas = map[*operacao]bool{}, map[*ordem]bool{}
}

// itemLote: uma linha do lote, sozinha num lote (isolarLote).
type itemLote struct {
	tabela string
	lote   *repo.Lote
	linha  any
}

// quebrarLote: cada linha num lote próprio, na ordem de gravação do GravarLote.
func quebrarLote(l *repo.Lote) []itemLote {
	var r []itemLote
	for _, o := range l.NovasOperacoes {
		r = append(r, itemLote{"kalmanordemoperacao", &repo.Lote{NovasOperacoes: []dominio.Operacao{o}}, o})
	}
	for _, o := range l.Operacoes {
		r = append(r, itemLote{"kalmanordemoperacao", &repo.Lote{Operacoes: []dominio.Operacao{o}}, o})
	}
	for _, o := range l.NovasOrdens {
		r = append(r, itemLote{"kalmanordem", &repo.Lote{NovasOrdens: []dominio.Ordem{o}}, o})
	}
	for _, o := range l.Ordens {
		r = append(r, itemLote{"kalmanordem", &repo.Lote{Ordens: []dominio.Ordem{o}}, o})
	}
	for _, e := range l.Eventos {
		r = append(r, itemLote{"kalmanordemevento", &repo.Lote{Eventos: []repo.EventoFIX{e}}, e})
	}
	for _, p := range l.Publicados {
		r = append(r, itemLote{"kalmanordempub", &repo.Lote{Publicados: []repo.PublicadoNovo{p}}, p})
	}
	for _, e := range l.Estados {
		r = append(r, itemLote{"kalmanordemestado", &repo.Lote{Estados: []repo.EstadoNovo{e}}, e})
	}
	for _, s := range l.Sessao {
		r = append(r, itemLote{"kalmanordemsessao", &repo.Lote{Sessao: []repo.EventoSessao{s}}, s})
	}
	for _, p := range l.Pedidos {
		r = append(r, itemLote{"kalmanordempedido", &repo.Lote{Pedidos: []repo.PedidoHTTP{p}}, p})
	}
	return r
}

// isolarLote (v1.13): o lote falhou com erro de dado. Cada linha na sua transação, em ordem; a que
// falha com erro de dado vai para a kalmanordemquarentena com alerta crítico BANCO_QUARENTENA (o
// estado em memória segue; um restart com quarentena pendente sobe em PARADA). Erro que não é de
// dado: o resto volta ao lote seguinte, como antes.
func (m *Motor) isolarLote(l *repo.Lote) {
	itens := quebrarLote(l)
	for i, it := range itens {
		err := m.repo.GravarLote(it.lote)
		if err == nil {
			m.linhaGravada(it.lote)
			continue
		}
		if repo.ErroDeDado(err) {
			b, _ := json.Marshal(it.linha)
			id, qerr := m.repo.Quarentenar(repo.Quarentena{Tabela: it.tabela, Linha: string(b), Erro: err.Error(), Em: m.agora()})
			if qerr == nil {
				m.linhaGravada(it.lote) // não repete: a linha ficou na quarentena
				slog.Error("linha na quarentena", "id", id, "tabela", it.tabela, "erro", err)
				m.al.Emitir("BANCO_QUARENTENA", "BANCO_QUARENTENA",
					fmt.Sprintf("linha de %s recusada pelo banco (%s): na quarentena (id %d); o serviço segue, mas um restart sobe em PARADA até a resolução (kalman-ordens quarentena)",
						it.tabela, cortarTexto(err.Error(), 200), id), 0)
				continue
			}
			err = qerr
		}
		// não é de dado (ou a quarentena falhou): o resto volta ao lote seguinte
		resto := &repo.Lote{}
		for _, x := range itens[i:] {
			resto.Eventos = append(resto.Eventos, x.lote.Eventos...)
			resto.Publicados = append(resto.Publicados, x.lote.Publicados...)
			resto.Estados = append(resto.Estados, x.lote.Estados...)
			resto.Sessao = append(resto.Sessao, x.lote.Sessao...)
			resto.Pedidos = append(resto.Pedidos, x.lote.Pedidos...)
		}
		slog.Error("gravação do lote falhou", "erro", err, "linhas", resto.Linhas())
		m.al.Emitir("ERRO_INTERNO", "BANCO_LOTE", "gravação no banco falhou (o envio à corretora seguiu; nova tentativa na próxima volta): "+cortarTexto(err.Error(), 300), 0)
		m.devolverAoLote(resto)
		return
	}
	for op := range m.v.opsSujas {
		if op.persistida && reflect.DeepEqual(op.Operacao.Copia(), op.gravada) {
			delete(m.v.opsSujas, op)
		}
	}
	for o := range m.v.ordensSujas {
		if o.persistida && reflect.DeepEqual(o.Ordem, o.gravada) {
			delete(m.v.ordensSujas, o)
		}
	}
	m.al.Resolver("BANCO_LOTE")
}

// linhaGravada: como loteGravado, só para as operações e ordens deste lote (as outras continuam sujas).
func (m *Motor) linhaGravada(l *repo.Lote) {
	for _, c := range append(append([]dominio.Operacao{}, l.NovasOperacoes...), l.Operacoes...) {
		for op := range m.v.opsSujas {
			if op.ID == c.ID {
				op.persistida, op.gravada = true, c
				delete(m.v.opsSujas, op)
			}
		}
	}
	for _, c := range append(append([]dominio.Ordem{}, l.NovasOrdens...), l.Ordens...) {
		for o := range m.v.ordensSujas {
			if o.ID == c.ID {
				o.persistida, o.gravada = true, c
				delete(m.v.ordensSujas, o)
			}
		}
	}
}

// devolverAoLote: a gravação falhou; o que era do lote volta para a próxima (as operações e ordens
// continuam sujas).
func (m *Motor) devolverAoLote(l *repo.Lote) {
	m.v.eventos = append(l.Eventos, m.v.eventos...)
	m.v.publicados = append(l.Publicados, m.v.publicados...)
	m.v.estados = append(l.Estados, m.v.estados...)
	m.v.sessao = append(l.Sessao, m.v.sessao...)
	m.v.muPedidos.Lock()
	m.v.pedidos = append(l.Pedidos, m.v.pedidos...)
	m.v.muPedidos.Unlock()
}

// ---------------------------------------------------------------- envio e limitador de taxa

// enfileirarEnvio: a mensagem sai depois do commit desta volta (despachar).
func (m *Motor) enfileirarEnvio(e envioPendente) {
	e.pedidoEm, e.pedidoRotulo, e.decididoEm, e.decididoNo = m.pedidoEm, m.pedidoRotulo, time.Now(), m.agora()
	m.pedidoEm = timeZero // a métrica do pedido mede só a primeira mensagem que ele causou
	m.v.envios = append(m.v.envios, e)
}

// taxaEntrada: o balde da entrada — a taxa da sessão menos a reserva da proteção e da saída.
func (m *Motor) taxaEntrada() float64 {
	return max(m.p.TaxaMsgS-m.p.ReservaSaidaMsgS, 0.01)
}

// recarregarTokens: o balde da entrada (token bucket no relógio do motor), rajada FIX_RAJADA_MSG.
func (m *Motor) recarregarTokens(agora time.Time) {
	if m.tokensEm.IsZero() {
		m.tokens, m.tokensEm = float64(m.p.RajadaMsg), agora
		return
	}
	if dt := agora.Sub(m.tokensEm).Seconds(); dt > 0 {
		m.tokens = min(float64(m.p.RajadaMsg), m.tokens+dt*m.taxaEntrada())
		m.tokensEm = agora
	}
}

// despachar: 1014 > cancel/saída > entrada; a ordem de decisão é mantida dentro da classe. A
// entrada tem balde próprio (FIX_TAXA_MSG_S − FIX_RESERVA_SAIDA_MSG_S) e só sai com saldo; 1014,
// cancel, saída e mass status não passam pelo balde da entrada — a reserva deles nunca é esgotada
// por entradas. A entrada que espera mais que ENTRADA_TTL_FILA_S é descartada (não entra
// atrasada). urgentes (v1.22): só o que não é entrada (a rodada antes da gravação); as entradas
// ficam para a rodada depois dela.
func (m *Motor) despachar(urgentes bool) {
	todos := append(m.esperandoTaxa, m.v.envios...)
	m.esperandoTaxa, m.v.envios = nil, nil
	pend := todos
	if urgentes {
		pend = nil
		for _, e := range todos {
			if e.prio == prioEntrada {
				m.v.envios = append(m.v.envios, e)
			} else {
				pend = append(pend, e)
			}
		}
	}
	defer func() { metricas.DefinirAguardandoTaxa(len(m.esperandoTaxa)) }()
	if len(pend) == 0 {
		return
	}
	sort.SliceStable(pend, func(i, j int) bool { return pend[i].prio < pend[j].prio })
	agora := m.agora()
	m.recarregarTokens(agora)
	enviados := &m.v.enviados
	for _, e := range pend {
		if m.reter(e) {
			continue // disjuntor de bolsa indisponível: só a sonda sai
		}
		if e.daRetencao && !e.nova && e.o != nil && e.o.Estado.Final() {
			// cancel/replace retido de uma ordem que terminou enquanto esperava (a saída executou na
			// volta da bolsa): não sai — seria só uma rejeição
			if e.o.clPend == e.clordid {
				e.o.pend, e.o.clPend = semPendencia, ""
			}
			continue
		}
		limitado := e.o != nil && e.prio != prioProtecao
		if limitado && *enviados >= maxEnviosPorVolta {
			m.esperandoTaxa = append(m.esperandoTaxa, e)
			continue
		}
		if e.prio != prioEntrada {
			if limitado {
				*enviados++
			}
			m.despacharUm(e)
			continue
		}
		if m.p.TTLFilaEntrada > 0 && agora.Sub(e.decididoNo) >= m.p.TTLFilaEntrada {
			metricas.EntradasExpiradasFila.Inc()
			if e.aoExpirar != nil {
				e.aoExpirar()
			}
			continue
		}
		if m.tokens < 1 || len(m.p0) > maxP0ParaEntrada {
			m.esperandoTaxa = append(m.esperandoTaxa, e)
			continue
		}
		m.tokens--
		*enviados++
		m.despacharUm(e)
	}
	if *enviados >= maxEnviosPorVolta && len(m.esperandoTaxa) > 0 {
		// sobrou envio pelo limite da volta: uma volta logo (depois do que estiver na P0 e na P1)
		m.agendarVolta()
	}
}

// agendarVolta: uma volta logo, para o que ficou esperando sair (uma por vez).
func (m *Motor) agendarVolta() {
	if m.voltaAgendada {
		return
	}
	m.voltaAgendada = true
	select {
	case m.p2 <- func() { m.voltaAgendada = false }:
	default:
		m.voltaAgendada = false
	}
}

func (m *Motor) despacharUm(e envioPendente) {
	tipo, _ := e.msg.Header.GetString(quickfix.Tag(35))
	ok := false
	con := m.conectorDo(e)
	if env := m.envioDo(con); env != nil {
		seq, err := env.Enviar(e.msg)
		if err == nil {
			ok = true
			if con == dominio.ConectorPnT {
				m.clordPorSeq[seq] = e.clordid // o 35=3 do PnT aponta a mensagem pelo 45
			}
			m.enviosRec = append(m.enviosRec, m.agora())
			metricas.FixMensagens.Inc("enviada", tipo)
			metricas.EsperaEnvio.Observar(time.Since(e.decididoEm).Seconds(), nomePrio(e.prio))
			if !e.pedidoEm.IsZero() {
				metricas.PedidoAteEnvio.Observar(time.Since(e.pedidoEm).Seconds(), e.pedidoRotulo)
			}
		} else {
			slog.Warn("envio falhou", "clordid", e.clordid, "erro", err)
		}
	}
	ev := repo.EventoFIX{Direcao: "ENV", MsgType: tipo, ClOrdID: e.clordid, Bruta: e.msg.String(), Em: m.agora()}
	if e.o != nil {
		ev.OrdemID, ev.OrderID, ev.Modulo = e.o.ID, e.o.OrderID, e.o.Modulo
	}
	if !ok {
		ev.Texto = "NAO_ENVIADA"
	}
	m.v.eventos = append(m.v.eventos, ev)
	if ok {
		if e.aoEnviar != nil {
			e.aoEnviar()
		}
	} else if e.aoFalhar != nil {
		e.aoFalhar()
	}
}

func nomePrio(p int) string {
	switch p {
	case prioProtecao:
		return "PROTECAO"
	case prioSaida:
		return "SAIDA"
	}
	return "ENTRADA"
}

// retirarDaFila: a NewOrderSingle da ordem ainda não saiu (sinal aberto, esperando a taxa ou nesta
// volta)?
// Então sai da fila e nunca vai ao PnT.
func (m *Motor) retirarDaFila(o *ordem) bool {
	if o.adiada {
		// entrada de um sinal ainda aberto (v1.10 §6): não sai quando ele fechar
		o.adiada = false
		return true
	}
	// só a NewOrderSingle conta: um cancel ou replace da ordem esperando a vez (maxEnviosPorVolta,
	// disjuntor) não quer dizer que ela nunca saiu
	for _, lista := range []*[]envioPendente{&m.esperandoTaxa, &m.v.envios, &m.bolsa.retidos} {
		for i, e := range *lista {
			if e.o == o && e.nova {
				*lista = append((*lista)[:i], (*lista)[i+1:]...)
				o.retida = false
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- chegada à sessão

// marcarChegada (goroutine da sessão): a resposta do PnT chegou, mesmo que ainda na fila do motor —
// os prazos de confirmação contam até a chegada, não até o motor tratá-la (v1.10 P3).
func (m *Motor) marcarChegada(cls ...string) {
	agora := m.rel.Agora()
	for _, c := range cls {
		if c != "" {
			m.chegadas.Store(c, agora)
		}
	}
}

// chegouDesde: alguma mensagem do PnT com um destes ClOrdID chegou à sessão em ou depois de t.
func (m *Motor) chegouDesde(t time.Time, cls ...string) bool {
	for _, c := range cls {
		if c == "" {
			continue
		}
		if v, ok := m.chegadas.Load(c); ok && !v.(time.Time).Before(t) {
			return true
		}
	}
	return false
}

// chegouDepois: alguma mensagem com um destes ClOrdID chegou à sessão estritamente depois de t.
func (m *Motor) chegouDepois(t time.Time, cls ...string) bool {
	for _, c := range cls {
		if v, ok := m.chegadas.Load(c); c != "" && ok && v.(time.Time).After(t) {
			return true
		}
	}
	return false
}

// ultimoSinalMass: o mais recente entre o último AF enviado (ou a última resposta tratada) e a última
// resposta de mass status chegada à sessão — o silêncio da conciliação conta daqui (v1.10 P3).
func (m *Motor) ultimoSinalMass() time.Time {
	u := m.massUltima
	if n := m.chegadaMass.Load(); n != 0 {
		if c := time.Unix(0, n); c.After(u) {
			u = c
		}
	}
	return u
}

// ---------------------------------------------------------------- IDs

func (m *Motor) novoIDOperacao() int64 { m.proxOpID++; return m.proxOpID }
func (m *Motor) novoIDOrdem() int64    { m.proxOrdemID++; return m.proxOrdemID }

var _ = fmt.Sprint
