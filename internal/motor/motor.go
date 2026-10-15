// Package motor é o núcleo do serviço de ordens do Kalman. Todo o estado vive numa única
// goroutine: pedidos HTTP, mensagens FIX e o tick entram na mesma fila, em ordem (Parte 7.1).
//
// Regra de escopo: o motor não decide quando operar. Executa, protege, limita e registra.
package motor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

func init() { decimal.MarshalJSONWithoutQuotes = true }

// Envio é o que o motor precisa da sessão FIX.
type Envio interface {
	Enviar(*quickfix.Message) (int, error)
	Logado() bool
}

// Pedido é o corpo do POST /ordens (Parte 2.1). Modulo: o módulo do GQ que pede (v1.3 M2;
// opcional por enquanto, ausente = KALMAN).
type Pedido struct {
	Modulo     string `json:"modulo,omitempty"`
	OperacaoID *int64 `json:"operacao_id"`
	UID        *int   `json:"uid"`
	Papel      string `json:"papel"`
	Lado       string `json:"lado"`
	Qtd        *int   `json:"qtd"`
	StopPts    *int64 `json:"stop_pts"`
	Motivo     string `json:"motivo"`
	TsDecisao  string `json:"ts_decisao"`
}

// Resposta é um objeto JSON.
type Resposta map[string]any

type pendencia int

const (
	semPendencia pendencia = iota
	pendCancel
	pendReplace
)

// ordem = persistido + volátil.
type ordem struct {
	dominio.Ordem
	pend             pendencia
	clPend           string
	pendDesde        time.Time
	cancelPendenteEm time.Time // cancel pendente confirmado pelo PnT (39=6): o 39=4 vem depois (v1.3 F2)
	qtdPend          int
	precoPend        int64
	stopPend         int64
	stopLimPend      int64
	cancelarApos     bool
	cancelamentos    int
	ultimoCancel     time.Time
	enviada          time.Time
	pendenteDesde    time.Time
	alertouPendente  bool
	recriada         bool // 1014 recriada (P5/P8): rejeição conta no limite de recriações
	// estado de erro do PnT (39=7, v1.1 F14): desde quando, o texto 58, se o motor já agiu e se o
	// cancelamento foi por causa do erro (1014: recriar sob o limite da P8)
	// saída com replace recusado pela ordem já terminal no PnT (35=9 com 39=2/4/8, v1.3 I1): desde
	// quando (repreço suspenso), se o mass status já foi pedido e se ele não foi coerente
	// 1014 disparada no PnT (150=I 39=B com 20807>0, v1.3 J2): quando, e quanto já tinha executado
	disparadaEm      time.Time
	qtdNoDisparo     int
	recusaTerminalEm time.Time
	recusaTerminal39 string
	saidaNoMass      bool
	saidaSemFill     bool
	emErroDesde      time.Time
	erroTexto        string
	erroTratado      bool
	canceladaPorErro bool
	decidirNoMass    bool  // cancel da 1014 em erro rejeitado por outro motivo: o mass status decide (F17)
	seq              int64 // ordem de criação neste processo (0 = carregada do banco)
	// lote (v1.10 B): já gravada uma vez e a cópia da última gravação (para não regravar igual)
	persistida bool
	gravada    dominio.Ordem
	// métricas (v1.9 M), tempo real; zero = carregada do banco
	envioReal, aReal    time.Time
	mediuA, mediuAceite bool
	mediuFill           bool
}

func (o *ordem) viva() bool { return !o.Estado.Final() && o.Estado != dominio.Desconhecida }
func (o *ordem) posicao() int {
	return int(o.Lado) * o.QtdExec
}

// operacao = persistido + ordens + volátil.
type operacao struct {
	dominio.Operacao
	ordens                []*ordem
	semProtecaoDesde      time.Time
	cancelProtEm          time.Time
	aguardandoStopDesde   time.Time
	proximoRepreco        time.Time
	proximaSaidaEm        time.Time
	repreco               int
	continuarRepreco      bool // a próxima saída segue do repreço seguinte (saída cancelada em erro, F14)
	rejeicoesProtecao     int
	retentativaProtEm     time.Time
	furoDesde             time.Time
	furado                bool
	saidaAposEntrada      bool
	encerramentoEm        time.Time
	motivoEncerramento    string
	zerarAposProtecao     bool
	protecaoDesatualizada bool
	saidaDivergente       bool
	congelada             bool
	violacaoDesde         time.Time // invariante de proteção violada desde
	recriacoes            int       // recriações da 1014 na janela de 30 s (P8)
	ultimaRecriacao       time.Time
	// lote (v1.10 B): já gravada uma vez e a cópia da última gravação
	persistida bool
	gravada    dominio.Operacao
}

type ultimoPreco struct {
	px decimal.Decimal
	em time.Time
}

// idDe: a identificação das ordens da operação — a conta congelada na entrada vai no 1 e no 116
// (como o PT faz), o 142 é da instância (v1.2 U4).
func (m *Motor) idDe(op *operacao) pnt.Identificacao {
	return pnt.Identificacao{Conta: op.Conta, Bolsa: m.p.Bolsa, OnBehalfOfSubID: op.Conta, SenderLocationID: m.p.SenderLocationID}
}

// divergenteOp (v1.2 S2): a posição está num contrato que deixou de ser o vigente.
func (m *Motor) divergenteOp(op *operacao) bool {
	return m.vigente != "" && op.Simbolo != "" && op.Simbolo != m.vigente
}

func (op *operacao) posicao() int {
	s := 0
	for _, o := range op.ordens {
		s += o.posicao()
	}
	return s
}

func (op *operacao) viva(p dominio.Papel) *ordem {
	var r *ordem
	for _, o := range op.ordens {
		if o.Papel == p && o.viva() {
			r = o
		}
	}
	return r
}

func (op *operacao) qtd(f func(*ordem) bool) int {
	n := 0
	for _, o := range op.ordens {
		if f(o) {
			n += o.QtdExec
		}
	}
	return n
}

func (op *operacao) qtdEntrada() int {
	return op.qtd(func(o *ordem) bool { return o.Papel == dominio.Entrada })
}
func (op *operacao) qtdSaida() int {
	return op.qtd(func(o *ordem) bool { return o.Papel != dominio.Entrada })
}

func (op *operacao) medio(entrada bool) (decimal.Decimal, bool) {
	soma, q := decimal.Zero, 0
	for _, o := range op.ordens {
		if (o.Papel == dominio.Entrada) == entrada && o.QtdExec > 0 {
			soma = soma.Add(o.PrecoMedio.Mul(decimal.NewFromInt(int64(o.QtdExec))))
			q += o.QtdExec
		}
	}
	if q == 0 {
		return decimal.Zero, false
	}
	return soma.Div(decimal.NewFromInt(int64(q))).Round(4), true
}

func (op *operacao) pnlRealizado() decimal.Decimal {
	pe, ok1 := op.medio(true)
	ps, ok2 := op.medio(false)
	q := op.qtdSaida()
	if e := op.qtdEntrada(); e < q {
		q = e
	}
	if !ok1 || !ok2 || q == 0 {
		return decimal.Zero
	}
	return ps.Sub(pe).Mul(decimal.NewFromInt(int64(q) * int64(op.Lado)))
}

func (op *operacao) pnlAberto(ultimo decimal.Decimal) decimal.Decimal {
	pos := op.posicao()
	pe, ok := op.medio(true)
	if pos == 0 || !ok {
		return decimal.Zero
	}
	return ultimo.Sub(pe).Mul(decimal.NewFromInt(int64(pos)))
}

func (op *operacao) proximoN(p dominio.Papel) int {
	if op.ContadorN == nil {
		op.ContadorN = map[string]int{}
	}
	op.ContadorN[p.String()]++
	return op.ContadorN[p.String()]
}

// clordid: KM-<papel>-<operacao_id>-<n> no KALMAN (como sempre); nos outros módulos o nome vem
// depois do KM- (KM-PT-E-…), para o ClOrdID não colidir entre operações de mesmo operacao_id
// (v1.3 M3; a v2 troca por um prefixo por módulo).
func (op *operacao) clordid(p dominio.Papel, n int) string {
	if op.Modulo != dominio.ModuloPadrao {
		return fmt.Sprintf("KM-%s-%s-%d-%d", op.Modulo, p, op.OperacaoID, n)
	}
	return fmt.Sprintf("KM-%s-%d-%d", p, op.OperacaoID, n)
}

// chaveOperacao: a operação é única por (módulo, operacao_id) (v1.3 M1).
type chaveOperacao struct {
	modulo string
	id     int64
}

// modulo do pedido: ausente = KALMAN.
func (p Pedido) modulo() string {
	if p.Modulo == "" {
		return dominio.ModuloPadrao
	}
	return p.Modulo
}

// operacaoDo: a operação do pedido (módulo e operacao_id), ou nil.
func (m *Motor) operacaoDo(p Pedido) *operacao {
	if p.OperacaoID == nil {
		return nil
	}
	return m.porOp[chaveOperacao{p.modulo(), *p.OperacaoID}]
}

// Parada (Parte 6).
type Parada struct {
	Modo   string `json:"modo"`
	Origem string `json:"origem"`
	Motivo string `json:"motivo"`
	Por    int    `json:"por,omitempty"` // uid do admin (v1.7 A2)
	Desde  string `json:"desde"`
}

// Motor.
type Motor struct {
	p          config.Parametros
	rel        relogio.Relogio
	repo       repo.Repositorio
	precos     preco.Fonte
	fix        Envio
	al         *alerta.Alertas
	p0, p1, p2 chan func() // entrada por prioridade (volta.go)
	fim        chan struct{}

	v             *estadoVolta    // o que a volta acumula até o commit
	esperandoTaxa []envioPendente // entradas esperando o limitador de taxa
	tokens        float64
	tokensEm      time.Time
	chegadas      sync.Map     // ClOrdID → chegada à sessão (relógio do motor)
	chegadaMass   atomic.Int64 // última resposta de mass status chegada à sessão (UnixNano, relógio do motor)
	proxOpID      int64
	proxOrdemID   int64
	execVistos    map[string]bool // fills (37, 17) já aplicados hoje
	usuarios      *cacheUsuarios
	respostaAtual *respostaPendente
	versaoFills   int64 // muda a cada fill aplicado (agregados do resultado do dia)
	agregado      agregadoDia

	dia         string
	ops         []*operacao
	porOp       map[chaveOperacao]*operacao
	porClOrd    map[string]*ordem
	por37       map[string]*ordem
	donoDa      map[*ordem]*operacao
	clordPorSeq map[int]string
	parada      *Parada
	divergente  bool
	vigente     string                 // ticker do /precos/atual: o contrato das entradas (v1.2 S1)
	ultimos     map[string]ultimoPreco // último preco conhecido por ticker (B5.c com a posição em outro)
	proxConfere time.Time              // próxima conferência da conta das posições abertas (U4)

	ultimaEntrada  time.Time
	entradasRec    []time.Time
	enviosRec      []time.Time
	recusasLimite  []time.Time
	entradasHoje   int
	zeragemFeita   bool
	jaLogou        bool
	presaDesde     time.Time // primeiro logon recusado pelo PnT ("already logged on") desde o último logon
	presaTexto     string
	conciliando    bool
	massReqIDs     map[string]bool // os 584 do mass status em curso (um AF por conta)
	massUltima     time.Time
	massTentativas int
	massProxima    time.Time
	massRespostas  []*pnt.Mensagem // nil = ordem de outro (PT)
	massChegadas   []time.Time     // quando cada resposta chegou (o registro usa este horário)
	processadas    int64
	seqOrdens      int64
	proxInvariante time.Time
	massCorte      int64 // ordens com seq até aqui existiam quando o mass status foi pedido

	// métricas (v1.9 M), tempo real: o pedido HTTP e a mensagem FIX em tratamento
	pedidoEm     time.Time
	pedidoRotulo string
	chegadaFIX   time.Time

	// GET /pnt/ordens (v1.7 C): a consulta em curso e a última (intervalo mínimo)
	consulta         *consultaPnT
	ultimaConsulta   Resposta
	ultimaConsultaEm time.Time
}

// Novo cria o motor (não inicia).
func Novo(c config.Config, rel relogio.Relogio, r repo.Repositorio, f preco.Fonte, n alerta.Notificador) *Motor {
	p := c.Parametros()
	m := &Motor{
		p: p, rel: rel, repo: repo.Medir(r), precos: f,
		ultimos: map[string]ultimoPreco{}, massReqIDs: map[string]bool{},
		p0: make(chan func(), capFilaP0), p1: make(chan func(), capFilaP1), p2: make(chan func(), capFilaP2),
		fim:        make(chan struct{}),
		execVistos: map[string]bool{},
		v:          &estadoVolta{opsSujas: map[*operacao]bool{}, ordensSujas: map[*ordem]bool{}},
		usuarios:   novoCacheUsuarios(),
	}
	m.al = alerta.Novo(n, m.publicarAlerta, rel, p.RepeticaoAlerta)
	metricas.DefinirFilas(func() [3]int { return [3]int{len(m.p0), len(m.p1), len(m.p2)} }, capFilaP0+capFilaP1+capFilaP2)
	return m
}

// Usar liga a sessão FIX.
func (m *Motor) Usar(e Envio) { m.fix = e }

// Iniciar carrega o estado do dia (Parte 7.3) e o cache de usuários, e sobe a goroutine do motor.
func (m *Motor) Iniciar() error {
	if err := m.carregarDia(); err != nil {
		return err
	}
	m.carregarUsuarios()
	go m.laco()
	return nil
}

// Parar encerra a goroutine (não mexe em nada no PnT), depois de fechar a volta em curso.
func (m *Motor) Parar() {
	done := make(chan struct{})
	m.p0 <- func() { m.aposVolta(func() { close(done) }) }
	<-done
	close(m.fim)
}

func (m *Motor) protegido(f func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("pânico no motor", "erro", r, "pilha", string(debug.Stack()))
			m.al.Emitir("ERRO_INTERNO", "ERRO_INTERNO", fmt.Sprint(r), 0)
		}
	}()
	f()
}

// Fazer roda f na goroutine do motor e espera o fim da volta (commit e envio feitos).
func (m *Motor) Fazer(f func()) { m.fazerComo("outro", f) }

func (m *Motor) fazerComo(origem string, f func()) {
	done := make(chan struct{})
	m.entrar(origem, func() {
		f()
		m.aposVolta(func() { close(done) })
	})
	<-done
}

// entrar põe f na fila da classe da origem (P0: fix, tick, outro; P1: http_saida; P2:
// http_entrada), medindo a espera (fila_espera_segundos{origem}).
func (m *Motor) entrar(origem string, f func()) {
	t0 := time.Now()
	m.filaDa(classeDaOrigem(origem)) <- func() {
		metricas.FilaEspera.Observar(time.Since(t0).Seconds(), origem)
		f()
	}
}

// entrarPedido: um pedido HTTP — ENTRADA em P2, o resto em P1; a primeira mensagem FIX que ele
// causar mede pedido_ate_envio.
func (m *Motor) entrarPedido(pedido string, f func()) {
	t0 := time.Now()
	origem := "http_saida"
	if pedido == "ENTRADA" {
		origem = "http_entrada"
	}
	m.entrar(origem, func() {
		m.pedidoEm, m.pedidoRotulo = t0, pedido
		defer func() { m.pedidoEm = timeZero }()
		f()
	})
}

// entrarFIX: uma mensagem do PnT (P0); a 1014 que um fill de entrada causar mede a espera desde a
// chegada (protecao_fill_ate_envio_segundos).
func (m *Motor) entrarFIX(f func()) {
	t0 := time.Now()
	m.entrar("fix", func() {
		m.chegadaFIX = t0
		defer func() { m.chegadaFIX = timeZero }()
		f()
	})
}

// Tick roda as verificações dependentes de tempo e espera (o executor e o relógio real chamam).
func (m *Motor) Tick() { m.fazerComo("tick", m.tick) }

// TickAssincrono enfileira o tick sem esperar (testes: o relógio anda com o motor travado).
func (m *Motor) TickAssincrono() { m.entrar("tick", m.tick) }

// Processadas conta mensagens FIX de aplicação já tratadas pelo motor (testes).
func (m *Motor) Processadas() int64 {
	var n int64
	m.Fazer(func() { n = m.processadas })
	return n
}

// Pedir trata um POST /ordens e espera o resultado.
func (m *Motor) Pedir(p Pedido) Resposta { return <-m.PedirAssincrono(p) }

// respostaPendente: a resposta HTTP do pedido em tratamento, entregue depois do commit e do envio;
// uma falha de envio na mesma volta ainda a muda (ENVIO_FALHOU).
type respostaPendente struct {
	r        Resposta
	entregue bool
}

// PedirAssincrono enfileira o pedido; o resultado chega no canal depois do commit e do envio.
// Quem desiste de esperar (503) não cancela nada: o motor processa o pedido do mesmo jeito.
func (m *Motor) PedirAssincrono(p Pedido) <-chan Resposta {
	ch := make(chan Resposta, 1)
	m.entrarPedido(p.Papel, func() {
		rp := &respostaPendente{}
		m.respostaAtual = rp
		rp.r = m.pedir(p)
		m.respostaAtual = nil
		m.aposVolta(func() { rp.entregue = true; ch <- rp.r })
	})
	return ch
}

// Enfileirar roda f na goroutine do motor sem esperar (testes: simular motor lento).
func (m *Motor) Enfileirar(f func()) { m.p0 <- f }

func (m *Motor) pedir(p Pedido) Resposta {
	if p.Modulo != "" && !dominio.ModuloValido(p.Modulo) {
		return m.recusar(p, simples("CAMPO_INVALIDO", fmt.Sprintf("modulo %q inválido (2 a 8 letras maiúsculas e dígitos)", p.Modulo)))
	}
	if r, ok := m.repetido(p); ok {
		return r
	}
	var r Resposta
	var rec *recusa
	switch p.Papel {
	case "ENTRADA":
		r, rec = m.pedirEntrada(p)
	case "SAIDA":
		r, rec = m.pedirSaida(p)
	default:
		rec = simples("CAMPO_INVALIDO", "papel deve ser ENTRADA ou SAIDA")
	}
	if rec != nil {
		r = m.recusar(p, rec)
	}
	m.guardarOriginal(p, r)
	return r
}

// impressao: o pedido canônico (todos os campos), para comparar reenvios. "modulo":"KALMAN" e
// sem modulo são o mesmo pedido (e o mesmo de antes da v1.3 M2).
func impressao(p Pedido) string {
	if p.Modulo == dominio.ModuloPadrao {
		p.Modulo = ""
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// repetido: mesmo (modulo, operacao_id, papel) já existente. Idêntico → resultado original + repetido=true +
// estado atual, sem alerta. Diferente → DUPLICADA + alerta (bug no gestor).
func (m *Motor) repetido(p Pedido) (Resposta, bool) {
	op := m.operacaoDo(p)
	if op == nil {
		return nil, false
	}
	var pedido, resposta string
	switch p.Papel {
	case "ENTRADA":
		pedido, resposta = op.PedidoEntrada, op.RespostaEntrada
	case "SAIDA":
		pedido, resposta = op.PedidoSaida, op.RespostaSaida
	default:
		return nil, false
	}
	if pedido == "" {
		return nil, false
	}
	if pedido != impressao(p) {
		m.al.Emitir("DUPLICADA_DIFERENTE", "", fmt.Sprintf("%s repetida com conteúdo diferente: operação %d (%s)", p.Papel, op.OperacaoID, op.Modulo), op.OperacaoID)
		return m.recusar(p, simples("DUPLICADA", "operação já recebida com outro conteúdo")), true
	}
	r := Resposta{}
	_ = json.Unmarshal([]byte(resposta), &r)
	r["repetido"] = true
	r["estado_atual"] = m.descrever(op)
	return r, true
}

// guardarOriginal grava pedido e resposta quando o (modulo, operacao_id, papel) passa a existir.
// Entrada: sempre que a operação foi criada (aceita ou ENVIO_FALHOU). Saída: só aceita.
func (m *Motor) guardarOriginal(p Pedido, r Resposta) {
	op := m.operacaoDo(p)
	if op == nil {
		return
	}
	b, _ := json.Marshal(r)
	switch {
	case p.Papel == "ENTRADA" && op.PedidoEntrada == "":
		op.PedidoEntrada, op.RespostaEntrada = impressao(p), string(b)
	case p.Papel == "SAIDA" && op.PedidoSaida == "" && r["aceito"] == true:
		op.PedidoSaida, op.RespostaSaida = impressao(p), string(b)
	default:
		return
	}
	m.salvarOp(op)
}

// AtivarParada (POST /parada). por: uid do admin (v1.7 A2; 0 = não informado, ex.: a CLI).
func (m *Motor) AtivarParada(modo, origem, motivo string, por int) Resposta {
	var r Resposta
	m.Fazer(func() {
		if modo != "BLOQUEAR" && modo != "ZERAR" {
			r = Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": "modo deve ser BLOQUEAR ou ZERAR"}
			return
		}
		m.publicar("ADMIN_ACAO", nil, map[string]any{"acao": "PARADA", "modo": modo, "origem": origem, "motivo": motivo, "por": porOuNulo(por)})
		m.ativarParada(modo, origem, motivoComPor(motivo, por))
		m.parada.Motivo, m.parada.Por = motivo, por
		if b, err := json.Marshal(m.parada); err == nil {
			m.gravarEstado("parada", string(b))
		}
		r = Resposta{"aceito": true, "codigo": "PARADA_ATIVA", "motivo": "parada " + m.parada.Modo + " ativa", "parada": m.parada}
	})
	return r
}

func porOuNulo(por int) any {
	if por <= 0 {
		return nil
	}
	return por
}

// motivoComPor: o texto do alerta PARADA/RETOMADA leva quem pediu.
func motivoComPor(motivo string, por int) string {
	if por <= 0 {
		return motivo
	}
	return fmt.Sprintf("%s (por %d)", motivo, por)
}

// Retomar (POST /retomar). Sempre manual. por: uid do admin (v1.7 A2).
func (m *Motor) Retomar(origem, motivo string, por int) Resposta {
	m.Fazer(func() {
		m.publicar("ADMIN_ACAO", nil, map[string]any{"acao": "RETOMAR", "origem": origem, "motivo": motivo, "por": porOuNulo(por)})
		motivo = motivoComPor(motivo, por)
		antes := m.parada
		m.parada = nil
		m.divergente = false
		for _, op := range m.ops {
			op.Divergente = false
			m.salvarOp(op)
		}
		m.gravarEstado("parada", "")
		m.al.Resolver("DIVERGENTE")
		m.al.Emitir("RETOMADA", "", fmt.Sprintf("retomado por %s: %s (estava: %+v)", origem, motivo, antes), 0)
	})
	return Resposta{"aceito": true, "codigo": "RETOMADO", "motivo": "parada retirada"}
}

// Consultar (GET /ordens/{id}?modulo=; sem modulo = KALMAN).
func (m *Motor) Consultar(modulo string, id int64) Resposta {
	var r Resposta
	m.Fazer(func() {
		op := m.operacaoDo(Pedido{Modulo: modulo, OperacaoID: &id})
		if op == nil {
			r = Resposta{"existe": false}
			return
		}
		r = m.descrever(op)
	})
	return r
}

// Eventos (GET /eventos?modulo=; sem modulo = todos).
func (m *Motor) Eventos(desde int64, limite int, modulo string) ([]repo.Publicado, error) {
	return m.repo.Publicados(desde, limite, modulo)
}

// ---------------------------------------------------------------- pnt.Ouvinte (só enfileira)

func (m *Motor) Logou() { m.entrarFIX(m.aoLogar) }
func (m *Motor) Caiu() {
	m.entrarFIX(func() {
		m.conciliando = false
		m.al.Emitir("SESSAO_CAIU", "", "sessão FIX caiu", 0)
	})
}
func (m *Motor) Recebeu(msg pnt.Mensagem) {
	metricas.FixMensagens.Inc("recebida", msg.Tipo)
	m.marcarChegada(msg.Texto(11), msg.Texto(41))
	if (msg.Tipo == "8" && msg.Texto(150) == "I") || msg.Tipo == "j" {
		// resposta de mass status chegou (ainda que na fila): o silêncio da conciliação conta daqui
		m.chegadaMass.Store(m.rel.Agora().UnixNano())
	}
	m.entrarFIX(func() { m.processadas++; m.processar(msg) })
}
func (m *Motor) RejeicaoRecebida(msg pnt.Mensagem) {
	metricas.FixMensagens.Inc("recebida", "3")
	m.entrarFIX(func() { m.rejeicaoSessao(msg) })
}
func (m *Motor) MensagemIlegivel(texto string) {
	m.entrarFIX(func() { m.processadas++; m.mensagemIlegivel(texto) })
}
func (m *Motor) RejeicaoEnviada(msg pnt.Mensagem) {
	m.entrarFIX(func() { m.processadas++; m.rejeicaoEnviada(msg) })
}
func (m *Motor) LogonRecusado(texto string) { m.entrarFIX(func() { m.logonRecusado(texto) }) }
func (m *Motor) EventoSessao(tipo, texto string) {
	m.entrarFIX(func() { m.registrarSessao(tipo, texto) })
}
func (m *Motor) StoreIlegivel(texto string) { m.entrarFIX(func() { m.storeIlegivel(texto) }) }

// ---------------------------------------------------------------- carga do dia

func (m *Motor) carregarDia() error {
	m.dia = m.hoje()
	m.ops, m.porOp = nil, map[chaveOperacao]*operacao{}
	m.porClOrd, m.por37, m.donoDa, m.clordPorSeq = map[string]*ordem{}, map[string]*ordem{}, map[*ordem]*operacao{}, map[int]string{}
	ops, ords, err := m.repo.CarregarDia(m.dia)
	if err != nil {
		return err
	}
	for i := range ops {
		op := &operacao{Operacao: ops[i], persistida: true, gravada: ops[i].Copia()}
		m.ops = append(m.ops, op)
		m.porOp[chaveOperacao{op.Modulo, op.OperacaoID}] = op
		if op.Estado == dominio.PosicionadaSemProtecao {
			op.semProtecaoDesde = m.agora()
		}
	}
	for i := range ords {
		op := m.porOp[chaveOperacao{ords[i].Modulo, ords[i].OperacaoID}]
		if op == nil {
			continue
		}
		o := &ordem{Ordem: ords[i], enviada: ords[i].CriadaEm, persistida: true, gravada: ords[i]}
		m.indexar(op, o)
	}
	if v, ok, _ := m.repo.LerEstado("parada"); ok && v != "" {
		var p Parada
		if json.Unmarshal([]byte(v), &p) == nil {
			m.parada = &p
		}
	}
	if v, ok, _ := m.repo.LerEstado("zeragem_feita"); ok && v == m.dia {
		m.zeragemFeita = true
	}
	m.entradasHoje = len(m.ops)
	umMinuto := m.agora().Add(-time.Minute)
	for _, op := range m.ops {
		if op.CriadaEm.After(m.ultimaEntrada) {
			m.ultimaEntrada = op.CriadaEm
		}
		if op.CriadaEm.After(umMinuto) {
			m.entradasRec = append(m.entradasRec, op.CriadaEm)
		}
		if op.Divergente && !op.Estado.Final() {
			m.divergente = true
		}
	}
	sort.Slice(m.entradasRec, func(i, j int) bool { return m.entradasRec[i].Before(m.entradasRec[j]) })
	if n, err := m.repo.EnviosDesde(umMinuto); err == nil {
		for i := 0; i < n; i++ {
			m.enviosRec = append(m.enviosRec, m.agora())
		}
	}
	m.versaoFills++
	// v1.10 B: o motor atribui os IDs e deduplica os fills em memória
	maxOp, maxOrdem, err := m.repo.MaioresIDs()
	if err != nil {
		return err
	}
	m.proxOpID, m.proxOrdemID = maxOp, maxOrdem
	de, err := time.ParseInLocation("2006-01-02", m.dia, relogio.Fuso)
	if err != nil {
		return err
	}
	if m.execVistos, err = m.repo.ChavesFillDoDia(de, de.AddDate(0, 0, 1)); err != nil {
		return err
	}
	slog.Info("dia carregado", "dia", m.dia, "operacoes", len(m.ops), "posicao", m.posicaoTotal(), "parada", m.parada, "fills_vistos", len(m.execVistos))
	return nil
}

func (m *Motor) indexar(op *operacao, o *ordem) {
	achou := false
	for _, x := range op.ordens {
		if x == o {
			achou = true
		}
	}
	if !achou {
		op.ordens = append(op.ordens, o)
	}
	m.donoDa[o] = op
	m.porClOrd[o.ClOrdBase] = o
	m.porClOrd[o.ClOrdAtual] = o
	if o.OrderID != "" {
		m.por37[o.OrderID] = o
	}
}

func (m *Motor) localizar(id37, cl, orig string) *ordem {
	if id37 != "" && id37 != "NONE" {
		if o := m.por37[id37]; o != nil {
			return o
		}
	}
	if o := m.porClOrd[cl]; cl != "" && o != nil {
		return o
	}
	if o := m.porClOrd[orig]; orig != "" && o != nil {
		return o
	}
	return nil
}

// ---------------------------------------------------------------- persistência e publicação

// salvarOp / salvarOrdem: entram no lote da volta (gravados no commit, só se mudaram).
func (m *Motor) salvarOp(op *operacao) { m.v.opsSujas[op] = true }

func (m *Motor) salvarOrdem(o *ordem) { m.v.ordensSujas[o] = true }

// gravarEstado: kalmanordemestado, no lote da volta.
func (m *Motor) gravarEstado(k, v string) {
	m.v.estados = append(m.v.estados, repo.EstadoNovo{Chave: k, Valor: v, Em: m.agora()})
}

func (m *Motor) mudar(op *operacao, e dominio.EstadoOperacao) {
	if op.Estado == e {
		return
	}
	slog.Info("operação", "op", op.OperacaoID, "de", op.Estado, "para", e)
	op.Estado = e
	m.salvarOp(op)
}

func (m *Motor) publicar(tipo string, op *operacao, dados map[string]any) {
	d := map[string]any{}
	var id int64
	mod := ""
	if op != nil {
		id, mod = op.OperacaoID, op.Modulo
		d["modulo"] = op.Modulo
		d["operacao_id"] = op.OperacaoID
		d["uid"] = op.UID
	}
	for k, v := range dados {
		if t, ok := v.(string); ok {
			v = alerta.ASCII(t) // v1.4 L1
		}
		d[k] = v
	}
	b, err := json.Marshal(d)
	if err != nil {
		slog.Error("publicar", "tipo", tipo, "erro", err)
		return
	}
	m.v.publicados = append(m.v.publicados, repo.PublicadoNovo{Tipo: tipo, Modulo: mod, OperacaoID: id, Dados: b, Em: m.agora()})
	slog.Info("evento", "tipo", tipo, "dados", string(b))
}

// publicarAlerta: o alerta só traz o operacao_id; o módulo é o da operação do dia com esse id
// (com o mesmo id em dois módulos, o primeiro — alerta com módulo próprio é da v2).
func (m *Motor) publicarAlerta(codigo, texto string, op int64) {
	d := map[string]any{"codigo": codigo, "texto": texto}
	mod := ""
	if op != 0 {
		d["operacao_id"] = op
		for _, x := range m.ops {
			if x.OperacaoID == op {
				mod = x.Modulo
				d["modulo"] = mod
				break
			}
		}
	}
	b, _ := json.Marshal(d)
	m.v.publicados = append(m.v.publicados, repo.PublicadoNovo{Tipo: "ALERTA", Modulo: mod, OperacaoID: op, Dados: b, Em: m.agora()})
	if op == 0 {
		// alerta sem operação: também no registro da sessão (v1.5 R3)
		tipo := "ALERTA"
		if codigo == "CONCILIACAO" {
			tipo = "CONCILIACAO"
		}
		m.registrarSessao(tipo, codigo+": "+texto)
	}
}

// registrarSessao (v1.5 R3): GET /sessao/eventos. Texto em ASCII (v1.4 L1).
func (m *Motor) registrarSessao(tipo, texto string) {
	m.v.sessao = append(m.v.sessao, repo.EventoSessao{Tipo: tipo, Texto: alerta.ASCII(texto), Em: m.agora()})
}

// ---------------------------------------------------------------- utilitários

func (m *Motor) agora() time.Time { return m.rel.Agora() }
func (m *Motor) hoje() string     { return m.agora().In(relogio.Fuso).Format("2006-01-02") }

func (m *Motor) horaDoDia() time.Duration {
	t := m.agora().In(relogio.Fuso)
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second
}

func (m *Motor) posicaoTotal() int {
	s := 0
	for _, op := range m.ops {
		s += op.posicao()
	}
	return s
}

// precoDoDia: o preco para o resultado do dia (Parte 5) — o do vigente, fresco, e só se nenhuma
// posição aberta estiver em outro contrato (pendência 2 da v1.1: suspenso durante a divergência).
func (m *Motor) precoDoDia(l preco.Leitura) (*decimal.Decimal, bool) {
	for _, op := range m.ops {
		if !op.Estado.Final() && op.posicao() != 0 && m.divergenteOp(op) {
			return nil, false
		}
	}
	px := m.precoConfiavel(l, m.vigente)
	return px, px != nil
}

// resultadoDoDia: realizado + aberto de todas as operações. O aberto é Σ (último − médio) × posição
// = último × Σposição − Σ(médio × posição): os agregados só são recalculados quando um fill muda
// algo (versaoFills), e a conta por entrada fica O(1) — antes, O(operações do dia) em decimal a cada
// entrada (v1.10: 40% da CPU do motor numa rajada de 1.000).
func (m *Motor) resultadoDoDia(ultimo *decimal.Decimal) decimal.Decimal {
	a := &m.agregado
	if !a.valido || a.versao != m.versaoFills {
		a.realizado, a.somaPos, a.somaMedioPos = decimal.Zero, 0, decimal.Zero
		for _, op := range m.ops {
			m.somarContribuicao(op, 1)
		}
		a.versao, a.valido = m.versaoFills, true
	}
	r := a.realizado
	if ultimo != nil {
		r = r.Add(ultimo.Mul(decimal.NewFromInt(a.somaPos)).Sub(a.somaMedioPos))
	}
	return r
}

// somarContribuicao: soma (sinal 1) ou tira (sinal -1) a parte da operação nos agregados; o fill
// tira antes de mudar a ordem e soma depois (aplicarFill), sem recalcular o dia.
func (m *Motor) somarContribuicao(op *operacao, sinal int64) {
	a := &m.agregado
	s := decimal.NewFromInt(sinal)
	a.realizado = a.realizado.Add(op.pnlRealizado().Mul(s))
	pos := op.posicao()
	if pe, ok := op.medio(true); pos != 0 && ok {
		a.somaPos += sinal * int64(pos)
		a.somaMedioPos = a.somaMedioPos.Add(pe.Mul(decimal.NewFromInt(sinal * int64(pos))))
	}
}

// agregadoDia: os agregados de resultadoDoDia.
type agregadoDia struct {
	valido       bool
	versao       int64
	realizado    decimal.Decimal
	somaPos      int64
	somaMedioPos decimal.Decimal
}

// arredondamento ao tick
func (m *Motor) acima(p decimal.Decimal) int64 {
	t := decimal.NewFromInt(m.p.Tick)
	return p.Div(t).Ceil().Mul(t).IntPart()
}

func (m *Motor) abaixo(p decimal.Decimal) int64 {
	t := decimal.NewFromInt(m.p.Tick)
	return p.Div(t).Floor().Mul(t).IntPart()
}

func (m *Motor) agressivo(l dominio.Lado, p decimal.Decimal) int64 {
	if l == dominio.Compra {
		return m.acima(p)
	}
	return m.abaixo(p)
}

func aparar(ts []time.Time, limite time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(limite) {
		i++
	}
	return ts[i:]
}

func chaveOp(prefixo string, op *operacao) string {
	if op.Modulo != dominio.ModuloPadrao {
		return prefixo + ":" + op.Modulo + ":" + strconv.FormatInt(op.OperacaoID, 10)
	}
	return prefixo + ":" + strconv.FormatInt(op.OperacaoID, 10)
}

type recusa struct {
	codigo, motivo string
	porLimite      bool
}

func simples(c, m string) *recusa { return &recusa{codigo: c, motivo: m} }
func limite(c, m string) *recusa  { return &recusa{codigo: c, motivo: m, porLimite: true} }
