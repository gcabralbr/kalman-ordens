package motor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// pedirEntrada: precedência de docs/ALERTAS.md — habilitação → campos → DUPLICADA → PARADA →
// DIVERGENTE → SEM_SESSAO → SEM_CONFIRMACAO → CONCILIANDO → conta e destino → símbolo → limites → preço.
func (m *Motor) pedirEntrada(p Pedido) (Resposta, *recusa) {
	if r := motivoLongo(p.Motivo, dominio.MaxMotivoEntrada); r != nil {
		return nil, r
	}
	hab, r := m.verificarHabilitacao(p)
	if r != nil {
		return nil, r
	}
	if p.OperacaoID == nil {
		return nil, simples("CAMPO_INVALIDO", "operacao_id ausente")
	}
	lado, ok := dominio.LadoDaLetra(p.Lado)
	if !ok {
		return nil, simples("CAMPO_INVALIDO", "lado deve ser C ou V")
	}
	if p.Qtd == nil || *p.Qtd <= 0 {
		return nil, simples("CAMPO_INVALIDO", "qtd inválida")
	}
	if p.StopPts == nil || *p.StopPts <= 0 {
		return nil, simples("SEM_STOP", "stop_pts ausente na ENTRADA: sem stop, não há ordem")
	}
	if m.operacaoDo(p) != nil {
		// a idempotência (repetido) já tratou o reenvio; aqui só chega operação sem pedido guardado
		return nil, simples("DUPLICADA", "operação já recebida")
	}
	if m.operacaoJaExistiu(p.modulo(), *p.OperacaoID) {
		return nil, simples("DUPLICADA", "operação já recebida (outro dia)")
	}
	if m.parada != nil {
		return nil, simples("PARADA", "serviço em parada "+m.parada.Modo+": "+m.parada.Motivo)
	}
	if m.divergente {
		return nil, simples("DIVERGENTE", "estado divergente; requer retomada manual")
	}
	if !m.algumLogado() {
		return nil, simples("SEM_SESSAO", "sessão FIX não logada")
	}
	if m.haSemConfirmacao() {
		return nil, simples("SEM_CONFIRMACAO", "há ordem sem confirmação do PnT")
	}
	if m.conciliando {
		return nil, simples("CONCILIANDO", "conciliação em andamento")
	}
	// conta e destino: lidos só aqui e congelados na operação (v1.2 U4)
	conta, r := m.contaParaEntrada(*p.UID)
	if r != nil {
		return nil, r
	}
	// SPEC-FLEXSCAN-v1 §14: o conector (da habilitação) tem de ter sessão e, no PnT, o disjuntor de
	// bolsa fechado
	if conta.Conector == dominio.ConectorPnT {
		if r := m.bolsaRecusaEntrada(); r != nil {
			return nil, r
		}
	}
	if !m.logado(conta.Conector) {
		if conta.Conector == dominio.ConectorFlexscan {
			return nil, simples("SEM_SESSAO", "conector Flexscan sem sessão (sem token)")
		}
		return nil, simples("SEM_SESSAO", "sessão FIX não logada")
	}
	// símbolo: o contrato vigente (ticker do /precos/atual da fonte do conector), congelado na
	// operação (v1.2 S1); num sinal (v1.10 §6), uma leitura para o sinal inteiro
	l := m.lerPrecoDe(conta.Conector)
	if l.Situacao == preco.OK && !reWIN.MatchString(l.Ticker) {
		m.al.Emitir("SIMBOLO_INVALIDO", "", fmt.Sprintf("ticker do marketdata %q não é um contrato de WIN: entrada recusada", l.Ticker), 0)
		return nil, simples("SIMBOLO_INVALIDO", fmt.Sprintf("ticker do marketdata %q não é um contrato de WIN", l.Ticker))
	}
	if r := m.verificarLimites(*p.Qtd, l, hab, *p.UID, lado); r != nil {
		return nil, r
	}
	if r := m.verificarPrecoEntrada(l); r != nil {
		return nil, r
	}

	agora := m.agora()
	op := &operacao{Operacao: dominio.Operacao{
		ID: m.novoIDOperacao(), Data: m.dia, Modulo: p.modulo(), OperacaoID: *p.OperacaoID, UID: *p.UID, Conta: conta.Conta, Destino: conta.Destino, Conector: conta.Conector, Simbolo: l.Ticker,
		Lado: lado, QtdPedida: *p.Qtd, StopPts: *p.StopPts,
		Estado: dominio.Entrando, MotivoEntrada: p.Motivo, TsDecisao: p.TsDecisao, CriadaEm: agora,
		ContadorN: map[string]int{},
	}}
	m.salvarOp(op)
	m.aoVivo.Associar(op.Modulo, op.OperacaoID, op.UID)
	m.ops = append(m.ops, op)
	m.porOp[chaveOperacao{op.Modulo, op.OperacaoID}] = op

	var ref decimal.Decimal
	if lado == dominio.Compra {
		ref = l.Ask.Add(decimal.NewFromInt(m.p.ColarEntrada))
	} else {
		ref = l.Bid.Sub(decimal.NewFromInt(m.p.ColarEntrada))
	}
	o := m.novaOrdem(op, dominio.Entrada, lado, *p.Qtd, pnt.EstrategiaAgendada)
	o.Preco = m.agressivo(lado, ref)
	o.MotivoGestor = p.Motivo
	m.salvarOrdem(o)
	m.indexar(op, o)
	var falhou func()

	if c := m.cache; c != nil {
		// a fatia do sinal segue com os agregados atualizados pela entrada aceita
		c.comprometido += *p.Qtd
		if c.porUID != nil {
			c.porUID[*p.UID] += *p.Qtd
		}
	}
	m.ultimaEntrada = agora
	m.entradasRec = append(m.entradasRec, agora)
	m.registrarEntradaUID(op)
	m.entradasHoje++

	rp := m.respostaAtual
	enviar := func() {
		m.enviarNova(op, o, falhou, func() { m.publicarEnviada("ENTRADA_ENVIADA", op, op.SinalEntrada, o.ClOrdAtual) })
	}
	falhou = func() {
		// a entrada não saiu (sessão caiu antes do envio): a operação encerra sem entrada
		o.Estado = dominio.FalhaEnvio
		m.salvarOrdem(o)
		op.Estado = dominio.EncerradaSemEntrada
		op.EncerrouEntrada = true
		if rp != nil && !rp.entregue {
			// a resposta ainda não saiu (mesma volta): vira a recusa de sempre
			nova := m.recusar(p, simples("ENVIO_FALHOU", "a ordem não saiu para o PnT"))
			for k := range rp.r {
				delete(rp.r, k)
			}
			for k, v := range nova {
				rp.r[k] = v
			}
			b, _ := json.Marshal(rp.r)
			op.RespostaEntrada = string(b)
		} else {
			// já respondida (esperava a taxa): o gestor sabe pelo evento
			m.al.Emitir("RECUSA_PNT", "", fmt.Sprintf("entrada %s não saiu para o PnT (sessão caiu antes do envio)", o.ClOrdAtual), op.OperacaoID)
			m.publicar("ENTRADA_ENCERRADA", op, map[string]any{"qtd_final": 0, "preco_medio": 0, "motivo": "REJEITADA"})
		}
		m.salvarOp(op)
	}
	if m.sinalAtual != nil {
		m.adiarEntrada(op, o, enviar) // sai quando o sinal fechar, na ordem da rajada
	} else {
		enviar()
	}
	slog.Info("ENTRADA decidida", "clordid", o.ClOrdAtual, "preco", o.Preco, "conta", op.Conta, "simbolo", op.Simbolo)
	return Resposta{"aceito": true, "operacao_id": op.OperacaoID, "clordid": o.ClOrdAtual, "preco": o.Preco}, nil
}

// lerPrecoDe: a leitura da fonte do conector; dentro de um sinal, a primeira do sinal (v1.10 §6),
// uma por fonte.
func (m *Motor) lerPrecoDe(con string) preco.Leitura {
	f := m.precosDo(con)
	if s := m.sinalAtual; s != nil {
		i := 0
		if m.fonteFlxPropria(con) {
			i = 1
		}
		if s.leitura[i] == nil {
			l := f.Ler()
			s.leitura[i] = &l
		}
		return *s.leitura[i]
	}
	return f.Ler()
}

// reWIN: contrato de WIN (mês FGHJKMNQUVXZ, ano com 2 dígitos). O ticker do /precos/atual vem da
// tabela de contratos do GQ; isto só barra um ticker absurdo.
var reWIN = regexp.MustCompile(`^WIN[FGHJKMNQUVXZ][0-9]{2}$`)

// verificarHabilitacao (v1.2 U5): a tabela kalmanordemusuario, lida a cada entrada. Escolher conta
// no GQ não habilita ordem real.
func (m *Motor) verificarHabilitacao(p Pedido) (dominio.Habilitacao, *recusa) {
	if p.UID == nil {
		return dominio.Habilitacao{}, simples("UID_NAO_HABILITADO", "uid ausente")
	}
	h, ok, err := m.habilitacaoDo(*p.UID)
	if err != nil {
		m.al.Emitir("USUARIO_CONFIG", "", fmt.Sprintf("habilitação do uid %d ilegível: %v", *p.UID, err), 0)
		return h, simples("CONFIG_USUARIO_INDISPONIVEL", "habilitação ilegível")
	}
	if !ok || !h.Habilitado {
		return h, simples("UID_NAO_HABILITADO", fmt.Sprintf("uid %d não habilitado para ordens reais", *p.UID))
	}
	return h, nil
}

// contaDoUsuario lê a configuração do usuário no GQ e resolve conta e destino no ambiente da
// instância (v1.2 U1–U3). Não emite alerta: quem chama decide.
func (m *Motor) contaDoUsuario(uid int) (usuario.Resolvido, *usuario.Falha) {
	hml, prd, achou, err := m.configUsuarioDo(uid)
	if err != nil {
		return usuario.Resolvido{}, &usuario.Falha{Codigo: "CONFIG_USUARIO_INDISPONIVEL", Motivo: "acusuario ilegível: " + err.Error(), Alerta: "USUARIO_CONFIG"}
	}
	if !achou {
		return usuario.Resolvido{}, &usuario.Falha{Codigo: "CONFIG_USUARIO_INVALIDA", Motivo: fmt.Sprintf("uid %d sem linha em acusuario", uid), Alerta: "USUARIO_CONFIG"}
	}
	texto := hml
	if m.p.Ambiente == usuario.Producao {
		texto = prd
	}
	// SPEC-FLEXSCAN-v1 §14.3: o GQ escolhe o conector (Robo.Conector) entre os da habilitação; a
	// corretora (CORSeqId) não decide
	h, _, err := m.habilitacaoDo(uid)
	if err != nil {
		return usuario.Resolvido{}, &usuario.Falha{Codigo: "CONFIG_USUARIO_INDISPONIVEL", Motivo: "habilitação ilegível: " + err.Error(), Alerta: "USUARIO_CONFIG"}
	}
	return usuario.ResolverCom(usuario.Regras{Ambiente: m.p.Ambiente, ContasHML: m.p.ContasHML, ContasHMLFlx: m.p.ContasHMLFlx, Flexscan: m.p.Flexscan}, texto, h.Conectores)
}

// contaParaEntrada: contaDoUsuario com os alertas da entrada. A trava cruzada (CONTA_AMBIENTE) é
// crítica por uid e se resolve na próxima entrada válida do mesmo uid.
func (m *Motor) contaParaEntrada(uid int) (usuario.Resolvido, *recusa) {
	r, f := m.contaDoUsuario(uid)
	chave := "CONTA_AMBIENTE:" + strconv.Itoa(uid)
	if f == nil {
		m.al.Resolver(chave)
		return r, nil
	}
	texto := fmt.Sprintf("entrada do uid %d recusada (%s): %s", uid, f.Codigo, f.Motivo)
	switch f.Alerta {
	case "":
		slog.Info("entrada recusada pela configuração do usuário", "uid", uid, "codigo", f.Codigo, "motivo", f.Motivo)
	case "CONTA_AMBIENTE":
		m.al.Emitir("CONTA_AMBIENTE", chave, texto, 0)
	default:
		m.al.Emitir(f.Alerta, "", texto, 0)
	}
	return r, simples(f.Codigo, f.Motivo)
}

func (m *Motor) verificarLimites(qtd int, l preco.Leitura, hab dominio.Habilitacao, uid int, lado dominio.Lado) *recusa {
	agora := m.agora()
	h := m.horaDoDia()
	if h < m.p.JanelaInicio || h >= m.p.JanelaFim {
		return limite("FORA_JANELA", "fora da janela de entradas")
	}
	if qtd > m.p.QtdMaxOrdem {
		return limite("QTD_MAXIMA", fmt.Sprintf("qtd %d > %d", qtd, m.p.QtdMaxOrdem))
	}
	comprometido := m.comprometidoTotal()
	if comprometido+qtd > m.p.PosicaoMax {
		return limite("POSICAO_MAXIMA", fmt.Sprintf("posição + entradas pendentes = %d, máximo %d", comprometido, m.p.PosicaoMax))
	}
	// limites do uid (v1.2 U5): valem junto com os globais
	if hab.QtdMaxOrdem > 0 && qtd > hab.QtdMaxOrdem {
		return limite("QTD_MAXIMA", fmt.Sprintf("qtd %d > %d (limite do uid %d)", qtd, hab.QtdMaxOrdem, uid))
	}
	if hab.PosicaoMax > 0 {
		doUID := m.comprometidoDoUID(uid)
		if doUID+qtd > hab.PosicaoMax {
			return limite("POSICAO_MAXIMA", fmt.Sprintf("posição + entradas pendentes do uid %d = %d, máximo %d", uid, doUID, hab.PosicaoMax))
		}
	}
	if r := m.verificarLimitesUID(uid, lado, agora); r != nil {
		return r
	}
	if !m.ultimaEntrada.IsZero() && agora.Sub(m.ultimaEntrada) < m.p.IntervaloEntradas {
		return limite("INTERVALO_MINIMO", fmt.Sprintf("última entrada há %s", agora.Sub(m.ultimaEntrada)))
	}
	m.entradasRec = aparar(m.entradasRec, agora.Add(-time.Minute))
	if len(m.entradasRec) >= m.p.EntradasPorMinuto {
		return limite("ENTRADAS_POR_MINUTO", "limite de entradas por minuto")
	}
	m.enviosRec = aparar(m.enviosRec, agora.Add(-time.Minute))
	if len(m.enviosRec) >= m.p.MensagensPorMinuto {
		m.al.Emitir("LIMITE", "", fmt.Sprintf("limite de mensagens por minuto (%d)", len(m.enviosRec)), 0)
		return limite("ORDENS_POR_MINUTO", "limite de mensagens por minuto")
	}
	if m.entradasHoje >= m.p.EntradasPorDia {
		m.al.Emitir("LIMITE", "", fmt.Sprintf("limite de entradas por dia (%d)", m.entradasHoje), 0)
		return limite("ENTRADAS_POR_DIA", "limite de entradas por dia")
	}
	// o resultado do dia usa a fonte de cada conector; l (a da entrada) pode ser a da Flexscan
	if ultimo, ok := m.precoDoDiaFatia(m.lerPrecoDe(dominio.ConectorPnT)); ok {
		r := m.resultadoDoDia(ultimo)
		if r.LessThanOrEqual(decimal.NewFromInt(-m.p.PerdaDiaPts)) {
			m.ativarParada("BLOQUEAR", "auto", "perda do dia "+r.String()+" pts")
			return limite("PERDA_DIARIA", "perda do dia "+r.String()+" pts")
		}
	}
	return nil
}

// verificarLimitesUID (v1.10 L): o laço de reentrada de um uid — no máximo LIMITE_ENTRADAS_MIN_UID
// entradas por minuto e LIMITE_INTERVALO_UID_S entre duas no mesmo sentido. Barra só o uid: não
// conta para a parada automática (LIMITE_RECUSAS_PARADA) nem emite alerta. A virada de mão (entrada
// oposta até LIMITE_VIRADA_S depois do ZERADA por SINAL_OPOSTO da última operação do uid) não passa
// por aqui.
func (m *Motor) verificarLimitesUID(uid int, lado dominio.Lado, agora time.Time) *recusa {
	if m.viradaDeMao(uid, lado, agora) {
		return nil
	}
	recentes := aparar(m.entradasUID[uid], agora.Add(-time.Minute))
	m.entradasUID[uid] = recentes
	if m.p.EntradasPorMinutoUID > 0 && len(recentes) >= m.p.EntradasPorMinutoUID {
		return simples("ENTRADAS_POR_MINUTO_UID", fmt.Sprintf("uid %d: %d entradas no último minuto, máximo %d", uid, len(recentes), m.p.EntradasPorMinutoUID))
	}
	if t, ok := m.ultimaEntradaUID[ladoUID{uid, lado}]; ok && m.p.IntervaloUID > 0 && agora.Sub(t) < m.p.IntervaloUID {
		return simples("INTERVALO_MINIMO_UID", fmt.Sprintf("uid %d: entrada %s há %s, mínimo %s no mesmo sentido", uid, lado.Letra(), agora.Sub(t).Truncate(time.Millisecond), m.p.IntervaloUID))
	}
	return nil
}

// viradaDeMao: a última operação do uid zerou por SINAL_OPOSTO há no máximo LIMITE_VIRADA_S e a
// entrada nova é do lado oposto.
func (m *Motor) viradaDeMao(uid int, lado dominio.Lado, agora time.Time) bool {
	op := m.ultimaOpUID[uid]
	return op != nil && op.Estado == dominio.Zerada && op.MotivoSaida == "SINAL_OPOSTO" && lado == op.Lado.Oposto() &&
		!op.zeradaEm.IsZero() && agora.Sub(op.zeradaEm) <= m.p.JanelaVirada
}

// registrarEntradaUID: a entrada aceita conta nos limites do uid.
func (m *Motor) registrarEntradaUID(op *operacao) {
	m.entradasUID[op.UID] = append(m.entradasUID[op.UID], op.CriadaEm)
	m.ultimaEntradaUID[ladoUID{op.UID, op.Lado}] = op.CriadaEm
	if u := m.ultimaOpUID[op.UID]; u == nil || !op.CriadaEm.Before(u.CriadaEm) {
		m.ultimaOpUID[op.UID] = op
	}
}

// comprometidoTotal: |posição| + o que falta executar das entradas vivas.
func (m *Motor) comprometidoTotal() int {
	if m.cache != nil {
		return m.cache.comprometido
	}
	c := abs(m.posicaoTotal())
	for _, op := range m.ops {
		if e := op.viva(dominio.Entrada); e != nil {
			c += e.Qtd - e.QtdExec
		}
	}
	return c
}

// comprometidoDoUID: o mesmo, só das operações do uid (limite do uid, v1.2 U5).
func (m *Motor) comprometidoDoUID(uid int) int {
	if c := m.cache; c != nil {
		if c.porUID == nil {
			c.porUID = map[int]int{}
			for _, op := range m.ops {
				c.porUID[op.UID] += comprometidoDa(op)
			}
		}
		return c.porUID[uid]
	}
	n := 0
	for _, op := range m.ops {
		if op.UID == uid {
			n += comprometidoDa(op)
		}
	}
	return n
}

func comprometidoDa(op *operacao) int {
	n := abs(op.posicao())
	if e := op.viva(dominio.Entrada); e != nil {
		n += e.Qtd - e.QtdExec
	}
	return n
}

// operacaoJaExistiu: (modulo, operacao_id) já recebido em outro dia (DUPLICADA). Na fatia de um
// sinal, pela consulta única da fatia.
func (m *Motor) operacaoJaExistiu(modulo string, id int64) bool {
	if c := m.cache; c != nil && c.existentes != nil && modulo == c.modulo {
		return c.existentes[id]
	}
	existe, _ := m.repo.OperacaoExiste(modulo, id)
	return existe
}

// precoDoDiaFatia: precoDoDia, uma vez por fatia de sinal (a posição não muda dentro dela).
func (m *Motor) precoDoDiaFatia(l preco.Leitura) (precoDia, bool) {
	c := m.cache
	if c == nil {
		return m.precoDoDia(l)
	}
	if !c.precoLido {
		c.preco, c.precoOK = m.precoDoDia(l)
		c.precoLido = true
	}
	return c.preco, c.precoOK
}

// verificarPrecoEntrada (SPEC v1.1 B1), na ordem de docs/referencia/precos-atual.md.
func (m *Motor) verificarPrecoEntrada(l preco.Leitura) *recusa {
	if l.Situacao != preco.OK {
		return limite("SEM_PRECO", "sem preço do marketdata")
	}
	if l.Status84 != 0 {
		return limite("LEILAO", fmt.Sprintf("status84 = %d", l.Status84))
	}
	if t := mercadoTravado(l); t != nil {
		// v1.17 (26/10): antes BOOK_INVALIDO (bid = ask) ou SEM_PRECO (um lado só)
		return limite("MERCADO_TRAVADO", t.motivo(l, m.agora()))
	}
	if l.Preco == nil || l.Bid == nil || l.Ask == nil {
		return limite("SEM_PRECO", "falta preco, bid ou ask")
	}
	if l.Idade(m.agora()) > m.p.IdadeMaxPreco {
		return limite("PRECO_VELHO", fmt.Sprintf("idade %s", l.Idade(m.agora())))
	}
	if !l.Bid.LessThan(*l.Ask) {
		return limite("BOOK_INVALIDO", "bid >= ask")
	}
	meio := l.Bid.Add(*l.Ask).Div(decimal.NewFromInt(2))
	if meio.Sub(*l.Preco).Abs().GreaterThan(decimal.NewFromInt(m.p.BookDescoladoPts)) {
		return limite("BOOK_DESCOLADO", "|meio − preco| = "+meio.Sub(*l.Preco).Abs().String())
	}
	return nil
}

// precoConfiavel: o preco do símbolo pedido, fresco; nil se não houver (ou se o marketdata está
// em outro contrato).
func (m *Motor) precoConfiavel(l preco.Leitura, simbolo string) *decimal.Decimal {
	if l.Situacao != preco.OK || simbolo == "" || l.Ticker != simbolo || l.Preco == nil || l.Idade(m.agora()) > m.p.IdadeMaxPreco {
		return nil
	}
	return l.Preco
}

func (m *Motor) recusar(p Pedido, r *recusa) Resposta {
	op := "?"
	if p.OperacaoID != nil {
		op = fmt.Sprint(*p.OperacaoID)
	}
	slog.Info("pedido recusado", "papel", p.Papel, "modulo", p.modulo(), "operacao", op, "codigo", r.codigo, "motivo", r.motivo)
	if r.porLimite {
		agora := m.agora()
		m.recusasLimite = append(aparar(m.recusasLimite, agora.Add(-time.Minute)), agora)
		if len(m.recusasLimite) >= m.p.RecusasParaParada && m.parada == nil {
			m.ativarParada("BLOQUEAR", "auto", fmt.Sprintf("%d recusas por limite em 1 min (última: %s)", len(m.recusasLimite), r.codigo))
		}
	}
	metricas.Recusas.Inc(p.Papel, r.codigo)
	return Resposta{"aceito": false, "codigo": r.codigo, "motivo": alerta.ASCII(r.motivo)}
}

func (m *Motor) novaOrdem(op *operacao, papel dominio.Papel, lado dominio.Lado, qtd, estrategia int) *ordem {
	cl := op.clordid(papel, op.proximoN(papel))
	m.seqOrdens++
	return &ordem{seq: m.seqOrdens, Ordem: dominio.Ordem{
		ID: m.novoIDOrdem(), Data: op.Data, Modulo: op.Modulo, UID: op.UID, OperacaoID: op.OperacaoID, Papel: papel, Conta: op.Conta, Simbolo: op.Simbolo, ClOrdBase: cl, ClOrdAtual: cl,
		Estrategia: estrategia, Lado: lado, Qtd: qtd, Estado: dominio.Enviando, CriadaEm: m.agora(),
	}}
}

// enviarNova: a NewOrderSingle sai depois do commit da volta (gravar antes de enviar, v1.10 B), na
// prioridade do papel (1014 > saída > entrada; a entrada sujeita à taxa). aoFalhar roda se não
// sair; aoEnviar, depois de sair. A entrada que passa da validade na fila da taxa é descartada
// (expirarNaFila).
// ignorarTunel (v1.13): 20864=Y na 1014 e na saída; na entrada, só com IGNORE_TUNNEL_ENTRADA.
func (m *Motor) ignorarTunel(o *ordem) bool {
	return o.Papel != dominio.Entrada || m.p.IgnoreTunnelEntrada
}

func (m *Motor) enviarNova(op *operacao, o *ordem, aoFalhar func(), aoEnviar ...func()) {
	msg := pnt.NovaOrdem(m.idDe(op), pnt.Ordem{
		ClOrdID: o.ClOrdAtual, Simbolo: op.Simbolo, Lado: o.Lado.FIX(), Qtd: o.Qtd, Preco: o.Preco,
		Estrategia: o.Estrategia, Stop: o.Stop, StopLim: o.StopLim, IgnorarTunel: m.ignorarTunel(o),
	}, m.agora())
	m.salvarOp(op)
	prio := prioEntrada
	switch o.Papel {
	case dominio.Protecao:
		prio = prioProtecao
	case dominio.Saida:
		prio = prioSaida
	}
	var aoExpirar func()
	if prio == prioEntrada {
		aoExpirar = func() { m.expirarNaFila(op, o) }
	}
	m.enfileirarEnvio(envioPendente{o: o, msg: msg, clordid: o.ClOrdAtual, prio: prio, nova: true, aoFalhar: aoFalhar, aoExpirar: aoExpirar, aoEnviar: func() {
		o.enviada = m.agora()
		o.envioReal = time.Now()
		for _, f := range aoEnviar {
			f()
		}
	}})
}

// expirarNaFila (v1.10 P5): a entrada esperou a taxa mais que ENTRADA_TTL_FILA_S — o sinal
// envelheceu; descartada sem ir ao PnT: ENTRADA_ENCERRADA motivo=EXPIRADA_NA_FILA.
func (m *Motor) expirarNaFila(op *operacao, o *ordem) {
	slog.Warn("entrada expirada na fila da taxa (não foi ao PnT)", "clordid", o.ClOrdAtual, "op", op.OperacaoID, "ttl", m.p.TTLFilaEntrada)
	o.Estado = dominio.FalhaEnvio
	o.MotivoRejeicao = "EXPIRADA_NA_FILA"
	m.salvarOrdem(o)
	op.Estado = dominio.EncerradaSemEntrada
	op.EncerrouEntrada = true
	op.saidaAposEntrada = false
	m.salvarOp(op)
	m.publicar("ENTRADA_ENCERRADA", op, map[string]any{"qtd_final": 0, "preco_medio": 0, "motivo": "EXPIRADA_NA_FILA"})
}

func (m *Motor) haSemConfirmacao() bool {
	if m.cache != nil {
		return m.cache.semConfirmacao
	}
	for _, op := range m.ops {
		for _, o := range op.ordens {
			if o.Estado == dominio.SemConfirmacao {
				return true
			}
		}
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
