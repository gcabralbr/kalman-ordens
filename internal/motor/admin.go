package motor

import (
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// Funções da tela de admin do Kalman (SPEC v1.7). Contrato oficial do responsável.

// AcaoAdmin é o corpo das ações de admin: motivo e por (uid do admin) obrigatórios.
type AcaoAdmin struct {
	Motivo string `json:"motivo"`
	Por    *int   `json:"por"`
}

func (a AcaoAdmin) validar() *recusa {
	if strings.TrimSpace(a.Motivo) == "" {
		return simples("CAMPO_INVALIDO", "motivo obrigatório")
	}
	if a.Por == nil || *a.Por <= 0 {
		return simples("CAMPO_INVALIDO", "por (uid do admin) obrigatório")
	}
	return nil
}

// Ações sobre uma operação (v1.7 A1).
const (
	AcaoEncerrar        = "ENCERRAR"
	AcaoCancelarEntrada = "CANCELAR_ENTRADA"
)

// AcaoOperacaoAssincrona enfileira encerrar / cancelar-entrada; o resultado chega no canal.
func (m *Motor) AcaoOperacaoAssincrona(acao, modulo string, id int64, a AcaoAdmin) <-chan Resposta {
	ch := make(chan Resposta, 1)
	m.entrarPedido(acao, func() {
		r, rec := m.acaoOperacao(acao, modulo, id, a)
		if rec != nil {
			r = m.recusar(Pedido{Modulo: modulo, OperacaoID: &id, Papel: acao}, rec)
		}
		m.aposVolta(func() { ch <- r })
	})
	return ch
}

func (m *Motor) acaoOperacao(acao, modulo string, id int64, a AcaoAdmin) (Resposta, *recusa) {
	if rec := a.validar(); rec != nil {
		return nil, rec
	}
	if modulo != "" && !dominio.ModuloValido(modulo) {
		return nil, simples("CAMPO_INVALIDO", fmt.Sprintf("modulo %q inválido", modulo))
	}
	op := m.operacaoDo(Pedido{Modulo: modulo, OperacaoID: &id})
	if op == nil {
		return nil, simples("OPERACAO_INEXISTENTE", "operação inexistente hoje neste módulo")
	}
	if op.Estado.Final() {
		return nil, simples("OPERACAO_ENCERRADA", fmt.Sprintf("operação já encerrada (%s)", op.Estado))
	}
	if op.Estado.Saindo() || !op.SaidaPedidaEm.IsZero() || op.saidaAposEntrada {
		return nil, simples("OPERACAO_EM_SAIDA", "a operação já está saindo (origem "+op.OrigemSaida+")")
	}
	pos := op.posicao()
	e := op.viva(dominio.Entrada)
	switch acao {
	case AcaoEncerrar:
		if pos == 0 && e == nil {
			return nil, simples("SEM_POSICAO", "sem posição e sem entrada pendente")
		}
		// B5.d: posição num contrato que não é mais o vigente e sem preço recente dele
		if m.divergenteOp(op) && !m.ultimoCertoValido(op) {
			return nil, simples("SIMBOLO_DIVERGENTE", "ticker divergente e sem preço recente do símbolo da posição")
		}
		m.acaoRegistrada(op, acao, a)
		m.iniciarSaida(op, a.Motivo, "ADMIN")
		return Resposta{"aceito": true, "codigo": "ENCERRANDO", "motivo": "saida iniciada (origem ADMIN); a operacao fecha com ZERADA",
			"modulo": op.Modulo, "operacao_id": op.OperacaoID, "acao": acao, "posicao": op.posicao()}, nil
	case AcaoCancelarEntrada:
		if e == nil {
			return nil, simples("SEM_ENTRADA_PENDENTE", "sem entrada pendente")
		}
		if pos != 0 {
			return nil, simples("COM_POSICAO", fmt.Sprintf("a entrada já executou %d; usar encerrar", abs(pos)))
		}
		if e.pend == pendCancel || !e.cancelPendenteEm.IsZero() || e.cancelamentos > 0 {
			// o cancel já foi pedido (timeout da entrada ou outro pedido): nada de novo
			return Resposta{"aceito": true, "codigo": "JA_CANCELANDO", "motivo": "o cancelamento da entrada ja estava em curso",
				"modulo": op.Modulo, "operacao_id": op.OperacaoID, "acao": acao, "situacao": "JA_CANCELANDO"}, nil
		}
		m.acaoRegistrada(op, acao, a)
		m.cancelar(e)
		return Resposta{"aceito": true, "codigo": "CANCELANDO", "motivo": "cancelamento da entrada enviado ao PnT",
			"modulo": op.Modulo, "operacao_id": op.OperacaoID, "acao": acao, "situacao": "CANCELANDO"}, nil
	}
	return nil, simples("CAMPO_INVALIDO", "ação desconhecida")
}

// acaoRegistrada: evento ADMIN_ACAO na linha do tempo da operação (e ao gestor), antes do efeito.
func (m *Motor) acaoRegistrada(op *operacao, acao string, a AcaoAdmin) {
	slog.Warn("ação de admin", "acao", acao, "op", op.OperacaoID, "modulo", op.Modulo, "por", *a.Por, "motivo", a.Motivo)
	m.publicar("ADMIN_ACAO", op, map[string]any{"acao": acao, "motivo": a.Motivo, "por": *a.Por})
}

// ---------------------------------------------------------------- GET /pnt/ordens (v1.7 C)

// consultaPnT: um 35=AF (585=9) por conta, só leitura — as respostas não mexem no estado (a
// conciliação automática continua a do logon). Fecha quando todas as contas responderam (912=Y,
// o 911 atingido, ou 35=j) ou em CONSULTA_PNT_ESPERA_S.
type consultaPnT struct {
	inicio    time.Time
	corte     int64             // ordens criadas até aqui existiam quando a consulta saiu
	contaDoID map[string]string // 584 → conta
	contas    []string
	feitas    map[string]bool
	recebidas map[string]int // por conta
	respostas []pnt.Mensagem // ordens KM
	outros    map[string]int // por conta
	esperando []chan Resposta
}

// ConsultarPnTAssincrono: GET /pnt/ordens.
func (m *Motor) ConsultarPnTAssincrono() <-chan Resposta {
	ch := make(chan Resposta, 1)
	m.entrar("http_saida", func() { m.consultarPnT(ch) })
	return ch
}

func (m *Motor) consultarPnT(ch chan Resposta) {
	agora := m.agora()
	if m.consulta != nil {
		m.consulta.esperando = append(m.consulta.esperando, ch)
		return
	}
	if m.ultimaConsulta != nil && agora.Sub(m.ultimaConsultaEm) < m.p.ConsultaPnTIntervalo {
		r := Resposta{}
		for k, v := range m.ultimaConsulta {
			r[k] = v
		}
		r["cache"] = true
		ch <- r
		return
	}
	if m.fix == nil || !m.fix.Logado() {
		ch <- Resposta{"erro": "SEM_SESSAO", "motivo": "sessao FIX nao logada"}
		return
	}
	c := &consultaPnT{inicio: agora, corte: m.seqOrdens, contaDoID: map[string]string{}, feitas: map[string]bool{},
		recebidas: map[string]int{}, outros: map[string]int{}, esperando: []chan Resposta{ch}}
	c.contas = m.contasAdmin()
	m.consulta = c
	for _, conta := range c.contas {
		contadorMass++
		id := fmt.Sprintf("KM-AD-%d-%d", agora.UnixMilli(), contadorMass)
		c.contaDoID[id] = conta
		conta := conta
		m.enfileirarEnvio(envioPendente{msg: pnt.MassStatus(conta, id), clordid: id, prio: prioSaida, aoFalhar: func() {
			c.feitas[conta] = true // não saiu: nada a esperar desta conta
			m.fecharConsultaSePronta(false)
		}})
	}
	slog.Info("consulta de ordens no PnT (admin)", "contas", c.contas)
	m.fecharConsultaSePronta(false)
}

// respostaDaConsulta: 150=I de um AF da consulta do admin. Grava (como toda mensagem recebida),
// sem aplicar ao estado.
func (m *Motor) respostaDaConsulta(msg pnt.Mensagem) {
	c := m.consulta
	conta := c.contaDoID[msg.Texto(584)]
	o := m.localizar(msg.Texto(37), msg.Texto(11), msg.Texto(41))
	if novo := m.registrarRecebida(o, msg); novo && o != nil {
		// o estado da ordem vem do 39 em qualquer ExecutionReport, inclusive no 150=I da consulta
		// (16/10: o 39=7 de uma saída só veio aqui, e a regra de erro F14 não rodou). A consulta
		// continua sem conciliar: ordem ausente não é julgada, desconhecida não é criada.
		m.aplicarExecReport(m.donoDa[o], o, msg)
	}
	c.recebidas[conta]++
	if ehNosso(msg.Texto(11)) || ehNosso(msg.Texto(41)) {
		c.respostas = append(c.respostas, msg)
	} else if msg.Texto(11) != "" || msg.Texto(37) != "" {
		c.outros[conta]++
	}
	total, temTotal := msg.Inteiro(911)
	if msg.Texto(912) == "Y" || (temTotal && c.recebidas[conta] >= total) {
		c.feitas[conta] = true
	}
	m.fecharConsultaSePronta(false)
}

// fecharConsultaSePronta: todas as contas responderam, ou passou CONSULTA_PNT_ESPERA_S (tick).
func (m *Motor) fecharConsultaSePronta(tempo bool) {
	c := m.consulta
	if c == nil {
		return
	}
	completa := true
	for _, conta := range c.contas {
		if !c.feitas[conta] {
			completa = false
		}
	}
	if !completa && !(tempo && m.agora().Sub(c.inicio) >= m.p.ConsultaPnTEspera) {
		return
	}
	r := m.resultadoConsulta(c, completa)
	m.consulta = nil
	m.ultimaConsulta, m.ultimaConsultaEm = r, c.inicio
	slog.Info("consulta de ordens no PnT (admin) concluída", "completa", completa, "ordens_km", len(c.respostas), "outros", c.outros)
	m.aposVolta(func() {
		for _, ch := range c.esperando {
			ch <- r
		}
	})
}

var textoOrdStatus = map[string]string{
	"0": "nova", "1": "parcialmente executada", "2": "executada", "4": "cancelada", "5": "substituida",
	"6": "cancelamento pendente", "7": "em erro", "8": "rejeitada", "A": "nova pendente", "B": "disparada",
	"C": "expirada", "E": "substituicao pendente",
}

// vivaNoPnT: o 39 de uma ordem que ainda trabalha no PnT.
func vivaNoPnT(st string) bool {
	switch st {
	case "0", "1", "5", "6", "7", "A", "B", "E":
		return true
	}
	return false
}

// reClOrd: KM-<papel>-<op>-<n> ou KM-<MODULO>-<papel>-<op>-<n> (v1.3 M3).
var reClOrd = regexp.MustCompile(`^KM-(?:([A-Z][A-Z0-9]{1,7})-)?([EPS])-(\d+)-\d+$`)

func doClOrdID(cls ...string) (modulo string, papel string, op int64, ok bool) {
	for _, cl := range cls {
		if g := reClOrd.FindStringSubmatch(cl); g != nil {
			modulo = g[1]
			if modulo == "" {
				modulo = dominio.ModuloPadrao
			}
			op, _ = strconv.ParseInt(g[3], 10, 64)
			return modulo, g[2], op, true
		}
	}
	return "", "", 0, false
}

func ladoLetra(fix string) any {
	switch fix {
	case "1":
		return "C"
	case "2":
		return "V"
	}
	return nil
}

func pontos(msg pnt.Mensagem, tag int) any {
	if d, ok := msg.Decimal(tag); ok {
		return d.Round(0).IntPart()
	}
	return nil
}

// resultadoConsulta monta a resposta do contrato: ordens KM do PnT, cada uma comparada com o
// serviço; as vivas no serviço e ausentes do PnT (só com todas as contas respondidas); os outros
// sistemas contados por conta; a posição pelo que o PnT mostra.
func (m *Motor) resultadoConsulta(c *consultaPnT, completa bool) Resposta {
	ordens := []map[string]any{}
	vistas := map[*ordem]bool{}
	type chaveOp struct {
		mod string
		id  int64
	}
	type acum struct {
		simbolo  string
		liq      int             // posição líquida (+ comprado)
		entQtd   int             // executado nas entradas
		entValor decimal.Decimal // Σ avgpx × cum das entradas
	}
	porOp := map[chaveOp]*acum{}
	var chaves []chaveOp
	for _, msg := range c.respostas {
		mod, papel, opID, _ := doClOrdID(msg.Texto(11), msg.Texto(41))
		cum, _ := msg.Inteiro(14)
		st := msg.Texto(39)
		item := map[string]any{"origem": "KM", "operacao_id": nil, "modulo": nil, "papel": nil, "simbolo": textoOuNulo(msg.Texto(55)),
			"lado": ladoLetra(msg.Texto(54)), "qtd": nil, "preco": pontos(msg, 44), "gatilho": pontos(msg, 20828), "cumqty": cum,
			"ordstatus": textoOuNulo(st), "ordstatus_texto": textoOuNulo(textoOrdStatus[st]), "orderid": textoOuNulo(msg.Texto(37)),
			"clordid": textoOuNulo(msg.Texto(11)), "estado_servico": nil, "confere": false, "divergencia": nil}
		if q, ok := msg.Inteiro(38); ok {
			item["qtd"] = q
		}
		if opID != 0 {
			item["operacao_id"], item["modulo"], item["papel"] = opID, mod, papel
		}
		o := m.localizar(msg.Texto(37), msg.Texto(11), msg.Texto(41))
		if o == nil {
			item["divergencia"] = "ordem KM sem registro no servico"
		} else {
			vistas[o] = true
			if op := m.donoDa[o]; op != nil {
				item["operacao_id"], item["modulo"], item["papel"] = op.OperacaoID, op.Modulo, o.Papel.String()
			}
			item["estado_servico"] = o.Estado
			if d := divergencias(o, msg); len(d) > 0 {
				item["divergencia"] = strings.Join(d, "; ")
			} else {
				item["confere"] = true
			}
		}
		ordens = append(ordens, item)
		// posição pelo PnT, por operação (o ClOrdID diz qual)
		if opID != 0 && cum > 0 {
			k := chaveOp{mod, opID}
			a := porOp[k]
			if a == nil {
				a = &acum{simbolo: msg.Texto(55)}
				porOp[k] = a
				chaves = append(chaves, k)
			}
			sinal := 1
			if msg.Texto(54) == "2" {
				sinal = -1
			}
			a.liq += sinal * cum
			if papel == "E" {
				avg, _ := msg.Decimal(6)
				a.entQtd += cum
				a.entValor = a.entValor.Add(avg.Mul(decimal.NewFromInt(int64(cum))))
			}
		}
	}
	if completa {
		consultadas := map[string]bool{}
		for _, conta := range c.contas {
			consultadas[conta] = true
		}
		for _, op := range m.ops {
			for _, o := range op.ordens {
				if !o.viva() || vistas[o] || o.seq > c.corte || !consultadas[o.Conta] {
					continue
				}
				var gat any
				if o.Stop != 0 {
					gat = o.Stop
				}
				ordens = append(ordens, map[string]any{"origem": "KM", "operacao_id": op.OperacaoID, "modulo": op.Modulo,
					"papel": o.Papel.String(), "simbolo": o.Simbolo, "lado": o.Lado.Letra(), "qtd": o.Qtd, "preco": o.Preco, "gatilho": gat,
					"cumqty": o.QtdExec, "ordstatus": nil, "ordstatus_texto": nil, "orderid": textoOuNulo(o.OrderID), "clordid": o.ClOrdAtual,
					"estado_servico": o.Estado, "confere": false, "divergencia": "viva no servico e ausente no PnT"})
			}
		}
	}
	// posição: por símbolo, das operações com posição líquida no PnT; preço médio = o da entrada
	type pos struct {
		qtd   int
		valor decimal.Decimal
		n     int
	}
	porSimbolo := map[string]*pos{}
	var simbolos []string
	for _, k := range chaves {
		a := porOp[k]
		if a.liq == 0 {
			continue
		}
		p := porSimbolo[a.simbolo]
		if p == nil {
			p = &pos{}
			porSimbolo[a.simbolo] = p
			simbolos = append(simbolos, a.simbolo)
		}
		p.qtd += a.liq
		if a.entQtd > 0 {
			medio := a.entValor.Div(decimal.NewFromInt(int64(a.entQtd)))
			p.valor = p.valor.Add(medio.Mul(decimal.NewFromInt(int64(abs(a.liq)))))
			p.n += abs(a.liq)
		}
	}
	sort.Strings(simbolos)
	posicao := []map[string]any{}
	for _, s := range simbolos {
		p := porSimbolo[s]
		var medio any
		if p.n > 0 {
			medio = p.valor.Div(decimal.NewFromInt(int64(p.n))).Round(2)
		}
		posicao = append(posicao, map[string]any{"simbolo": s, "qtd": p.qtd, "preco_medio": medio})
	}
	outros := map[string]int{}
	for _, conta := range c.contas {
		outros[conta] = c.outros[conta]
	}
	contas := append([]string{}, c.contas...)
	// v1.7 C8: a consulta terminou com todas as contas respondidas? senão, quais faltaram
	semResposta := []string{}
	for _, conta := range c.contas {
		if !c.feitas[conta] {
			semResposta = append(semResposta, conta)
		}
	}
	return Resposta{"consultado_em": emBR(c.inicio), "cache": false, "completo": completa, "contas_sem_resposta": semResposta,
		"contas": contas, "ordens": ordens, "outros": outros, "posicao": posicao}
}

// divergencias: o PnT e o serviço discordam sobre a ordem?
func divergencias(o *ordem, msg pnt.Mensagem) []string {
	var d []string
	st := msg.Texto(39)
	switch {
	case vivaNoPnT(st) && !o.viva():
		d = append(d, fmt.Sprintf("viva no PnT (39=%s), %s no servico", st, o.Estado))
	case !vivaNoPnT(st) && o.viva():
		d = append(d, fmt.Sprintf("encerrada no PnT (39=%s), viva no servico (%s)", st, o.Estado))
	}
	if cum, ok := msg.Inteiro(14); ok && cum != o.QtdExec {
		d = append(d, fmt.Sprintf("executado: PnT %d, servico %d", cum, o.QtdExec))
	}
	if vivaNoPnT(st) && o.viva() && o.pend == semPendencia {
		if q, ok := msg.Inteiro(38); ok && q != o.Qtd {
			d = append(d, fmt.Sprintf("quantidade: PnT %d, servico %d", q, o.Qtd))
		}
		if v, ok := msg.Decimal(20828); ok && o.Stop != 0 && v.Round(0).IntPart() != o.Stop {
			d = append(d, fmt.Sprintf("gatilho: PnT %d, servico %d", v.Round(0).IntPart(), o.Stop))
		}
		if v, ok := msg.Decimal(44); ok && o.Preco != 0 && v.Round(0).IntPart() != o.Preco {
			d = append(d, fmt.Sprintf("preco: PnT %d, servico %d", v.Round(0).IntPart(), o.Preco))
		}
	}
	return d
}

// ---------------------------------------------------------------- habilitação pelo admin (v1.8 H)

// HabilitacaoAdmin é o corpo do POST /usuarios/{uid}/habilitacao (estrito): os três obrigatórios.
type HabilitacaoAdmin struct {
	Habilitado *bool  `json:"habilitado"`
	Motivo     string `json:"motivo"`
	Por        *int   `json:"por"`
}

// HabilitacaoDoUsuario: GET /usuarios/{uid}/habilitacao (direto do banco). Sem linha: existe e
// habilitado false, o resto nulo.
func HabilitacaoDoUsuario(r interface {
	Habilitacao(int) (dominio.Habilitacao, bool, error)
}, uid int) (Resposta, error) {
	h, ok, err := r.Habilitacao(uid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Resposta{"uid": uid, "existe": false, "habilitado": false, "posicao_max": nil, "qtd_max_ordem": nil,
			"por": nil, "motivo": nil, "em": nil}, nil
	}
	return Resposta{"uid": uid, "existe": true, "habilitado": h.Habilitado, "posicao_max": h.PosicaoMax, "qtd_max_ordem": h.QtdMaxOrdem,
		"por": h.Por, "motivo": h.Motivo, "em": emBROuNulo(h.Em)}, nil
}

// AlterarHabilitacaoAssincrono: POST /usuarios/{uid}/habilitacao, na fila do motor.
func (m *Motor) AlterarHabilitacaoAssincrono(uid int, a HabilitacaoAdmin) <-chan Resposta {
	ch := make(chan Resposta, 1)
	m.entrarPedido("HABILITACAO", func() {
		r, rec := m.alterarHabilitacao(uid, a)
		if rec != nil {
			r = m.recusar(Pedido{UID: &uid, Papel: "HABILITACAO"}, rec)
		}
		m.aposVolta(func() { ch <- r })
	})
	return ch
}

// alterarHabilitacao (v1.8 H2): bloqueia ou desbloqueia o uid. Grava só habilitado, por
// ("admin:<por>"), motivo e em, preservando os limites, com o histórico; a 1ª habilitação continua
// só pela CLI. Efeito: só a ENTRADA confere a habilitação (verificarHabilitacao); posição, 1014,
// saída e entrada pendente seguem.
func (m *Motor) alterarHabilitacao(uid int, a HabilitacaoAdmin) (Resposta, *recusa) {
	if uid <= 0 {
		return nil, simples("CAMPO_INVALIDO", "uid invalido")
	}
	if a.Habilitado == nil {
		return nil, simples("CAMPO_INVALIDO", "habilitado obrigatório")
	}
	if rec := (AcaoAdmin{Motivo: a.Motivo, Por: a.Por}).validar(); rec != nil {
		return nil, rec
	}
	hab := *a.Habilitado
	antes, existia, err := m.repo.AlterarHabilitado(uid, hab, fmt.Sprintf("admin:%d", *a.Por), a.Motivo, m.agora())
	if err != nil {
		slog.Error("alterar habilitação", "uid", uid, "erro", err)
		return nil, simples("CONFIG_USUARIO_INDISPONIVEL", "habilitação ilegível no banco")
	}
	m.invalidarUsuario(uid) // o cache relê do banco na próxima entrada do uid (v1.10 B)
	switch {
	case !existia && hab:
		return nil, simples("SEM_HABILITACAO_PREVIA", fmt.Sprintf("uid %d nunca foi habilitado: a 1ª habilitação é pela CLI (kalman-ordens usuario habilitar)", uid))
	case !existia || (antes.Habilitado == hab && !hab):
		return Resposta{"aceito": true, "codigo": "JA_BLOQUEADO", "motivo": fmt.Sprintf("uid %d ja nao estava habilitado", uid)}, nil
	case antes.Habilitado == hab:
		return Resposta{"aceito": true, "codigo": "JA_DESBLOQUEADO", "motivo": fmt.Sprintf("uid %d ja estava habilitado", uid)}, nil
	}
	acao, codigo, texto := "DESBLOQUEAR_USUARIO", "DESBLOQUEADO", "habilitado: entradas novas voltam a ser aceitas"
	if !hab {
		acao, codigo, texto = "BLOQUEAR_USUARIO", "BLOQUEADO", "bloqueado: entradas novas recusadas; posição, 1014 e saídas seguem"
	}
	slog.Warn("habilitação alterada pelo admin", "uid", uid, "habilitado", hab, "por", *a.Por, "motivo", a.Motivo)
	m.publicar("ADMIN_ACAO", nil, map[string]any{"acao": acao, "uid": uid, "motivo": a.Motivo, "por": *a.Por})
	return Resposta{"aceito": true, "codigo": codigo, "motivo": alerta.ASCII(fmt.Sprintf("uid %d %s", uid, texto))}, nil
}
