package flexscan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// Ouvinte: o lado do motor (motor.OuvinteFlexscan).
type Ouvinte interface {
	Logou()
	Caiu(motivo string)
	Recebeu(pnt.Mensagem)
	Alertar(codigo, chave, texto string)
	Resolver(chave string)
	Registrar(repo.EventoFIX)
}

// Config da ponte (service.env, SPEC-FLEXSCAN-v1 §6).
type Config struct {
	URL, Usuario, Senha string
	Bolsa               string        // FLEXSCAN_BOLSA (XBMF)
	Timeout             time.Duration // FLEXSCAN_TIMEOUT_MS
	ReqPorS             float64       // FLEXSCAN_REQ_POR_S
	Rajada              int
	Trabalhadores       int
	Confirma            time.Duration // FLEXSCAN_CONFIRMA_S: janela da consulta ativa depois de uma ação
	Consulta            time.Duration // FLEXSCAN_CONSULTA_S: consulta periódica das vivas
	AtrasoWebhook       time.Duration // FLEXSCAN_WEBHOOK_ATRASO_S (sem uso desde a v1.22: EsperaWebhook)
	EsperaWebhook       time.Duration // v1.22: FLEXSCAN_WEBHOOK_ESPERA_S, o webhook de uma ordem depois da ação ou da mudança (0 = não espera)
	ConfirmarFill       bool          // FLEXSCAN_CONFIRMAR_FILL
	Webhook             bool          // o receptor está ligado (sem ele, só consulta e nada de alerta de atraso)
	RenovarAntes        time.Duration // FLEXSCAN_RENOVAR_ANTES_MIN
	VidaToken           time.Duration // FLEXSCAN_TOKEN_VIDA_H (token sem exp)
	EsperaLogin         time.Duration // entre logins que falharam (30 s)
	// limite de logins entre restarts (FLEXSCAN_ESTADO_LOGIN, FLEXSCAN_LOGIN_MAX em
	// FLEXSCAN_LOGIN_JANELA_MIN); arquivo vazio = só em memória
	EstadoLogin string
	LoginMax    int
	LoginJanela time.Duration
	// v1.21: a primeira consulta depois do 202 do POST espera isto (o GET logo depois do 202 dá
	// 404 "Order not found"; 27/10); 0 = na hora, como antes
	PrimeiraConsulta time.Duration
	// v1.21: a URL pública do webhook (com o segredo), para conferir e recadastrar o cadastro na
	// Flexscan a cada ConfereWebhook; vazia = não confere
	WebhookURL     string
	ConfereWebhook time.Duration
}

func (c *Config) padroes() {
	if c.Bolsa == "" {
		c.Bolsa = "XBMF"
	}
	if c.Timeout <= 0 {
		c.Timeout = 3 * time.Second
	}
	if c.Trabalhadores <= 0 {
		c.Trabalhadores = 4
	}
	if c.Rajada <= 0 {
		c.Rajada = 5
	}
	if c.Confirma <= 0 {
		c.Confirma = 5 * time.Second
	}
	if c.Consulta <= 0 {
		c.Consulta = 5 * time.Second
	}
	if c.AtrasoWebhook <= 0 {
		c.AtrasoWebhook = 10 * time.Second
	}
	if c.RenovarAntes <= 0 {
		c.RenovarAntes = 30 * time.Minute
	}
	if c.VidaToken <= 0 {
		c.VidaToken = 9 * time.Hour
	}
	if c.EsperaLogin <= 0 {
		c.EsperaLogin = 30 * time.Second
	}
	if c.ConfereWebhook <= 0 {
		c.ConfereWebhook = 10 * time.Minute
	}
}

// passosPost (v1.21): a consulta ativa do POST começa depois da espera, com os mesmos intervalos.
func passosPost(espera time.Duration) []time.Duration {
	ps := make([]time.Duration, len(passosAtiva))
	for i, d := range passosAtiva {
		ps[i] = espera + d
	}
	return ps
}

// passos da consulta ativa depois de uma ação (SPEC §4.4): +0, +0,3, +0,6, +1,2, +2,4 s, até Confirma.
var passosAtiva = []time.Duration{0, 300 * time.Millisecond, 600 * time.Millisecond, 1200 * time.Millisecond, 2400 * time.Millisecond}

// prioridades das tarefas (proteção > saída/cancel > entrada > consultas).
const (
	prioProtecao = iota
	prioSaida
	prioEntrada
	prioConsulta
)

// pendencia: o PUT (G) ou DELETE (F) em curso de uma ordem — um por vez, como no FIX.
type pendencia struct {
	tipo     byte // 'G' ou 'F'
	cl       string
	qtd      int
	preco    decimal.Decimal
	stop     decimal.Decimal
	desde    time.Time // relógio do motor (janela de confirmação)
	enviada  time.Time // tempo real (webhook depois do envio)
	rejAntes string    // clxRejReason visto antes do PUT (o código pode ficar grudado)
	quando   time.Time // transactTime visto antes do PUT
	saiu     bool      // o pedido HTTP já voltou (202, ou "não sei")
}

// ordemP: o que a ponte sabe de uma ordem e o que já entregou ao motor.
type ordemP struct {
	id, nome, conta, simbolo, lado, tipo string
	qtd                                  int
	clAtual                              string // o ClOrdID KM vigente (o 11 dos ERs)
	fase                                 Fase
	exec                                 int
	medio                                decimal.Decimal
	preco, stop                          decimal.Decimal
	rejVista                             string
	quandoVisto                          time.Time
	aceitaEntregue                       bool
	pend                                 *pendencia
	ativaDesde                           time.Time       // consulta ativa em curso (relógio do motor); zero = nenhuma
	passos                               []time.Duration // os da janela atual (nil = passosAtiva)
	passo                                int
	proxPeriodica                        time.Time
	consultaNaFila                       bool
	incerta                              bool // POST sem resposta: procurada pelo name
	// v1.22 (webhook_espera.go): a espera pelo webhook da ordem (relógio do serviço; zero = nenhuma)
	whEsperaDesde, whUltimo         time.Time
	whEsperaMotivo, whEsperaDetalhe string
	// v1.24 (consulta_webhook.go): o POST em curso, a hora do 202, a consulta pedida pelo webhook à
	// espera de sair (origem; "" = nenhuma) e o estado final já trazido pelo webhook
	postEmCurso   bool
	aceita202Em   time.Time
	confirmar     string
	confirmarApos time.Time
	whFinal       bool
}

func (o *ordemP) chave() string { return o.nome }

type tarefa struct {
	prio  int
	chave string
	seq   int64
	fazer func(ctx context.Context)
}

// Ponte: o conector Flexscan.
type Ponte struct {
	cfg Config
	cli *cliente
	ouv Ouvinte
	rel relogio.Relogio

	mu       sync.Mutex
	ordens   map[string]*ordemP // por id
	porNome  map[string]*ordemP // por name (= ClOrdBase)
	porCl    map[string]*ordemP // por ClOrdID KM (base, de replace, de cancel)
	fila     []*tarefa
	seq      int64
	ocupadas map[string]bool
	emCurso  int
	sinal    chan struct{}

	logado     atomic.Bool
	jaLogou    bool
	definitivo bool      // login com erro definitivo: não tenta sozinho
	proxLogin  time.Time // relógio do serviço (o virtual nos roteiros)
	logando    bool
	renovando  bool
	ultimoErro string
	webhooks   int64
	ultWebhook time.Time
	// v1.22 (webhook_espera.go): faltas seguidas de webhook e os alertas ativos
	whFaltas              int
	whAtrasado, whCritico bool

	// v1.21: tráfego, último login e o cadastro do webhook
	traf              *trafego
	ultLogin          map[string]any
	webhookCad        *bool
	webhookConfEm     time.Time
	webhookRecadEm    time.Time
	proxConfWebhook   time.Time // relógio do serviço; zero = conferir no próximo Tick logado
	conferindoWebhook bool

	entregues atomic.Int64
	ctx       context.Context
	cancelar  context.CancelFunc
	wg        sync.WaitGroup
}

// Nova cria a ponte (não inicia).
func Nova(cfg Config, ouv Ouvinte, rel relogio.Relogio) *Ponte {
	cfg.padroes()
	ctx, cancel := context.WithCancel(context.Background())
	guarda := novaGuarda(cfg.EstadoLogin, cfg.LoginMax, cfg.LoginJanela, cfg.Usuario, cfg.Senha)
	p := &Ponte{cfg: cfg, cli: novoCliente(cfg.URL, cfg.Usuario, cfg.Senha, cfg.Timeout, cfg.ReqPorS, cfg.Rajada, guarda), ouv: ouv, rel: rel,
		ordens: map[string]*ordemP{}, porNome: map[string]*ordemP{}, porCl: map[string]*ordemP{}, ocupadas: map[string]bool{},
		sinal: make(chan struct{}, 1), ctx: ctx, cancelar: cancel, traf: novoTrafego(rel)}
	p.cli.aoLogin = p.registrarLogin
	return p
}

// Trafego (GET /flexscan/trafego, v1.21 §1): as linhas com seq > desde, o último seq e o bloco
// saude (o /saude.flexscan mais os números do tráfego).
func (p *Ponte) Trafego(desde int64, limite int) ([]LinhaTrafego, int64, map[string]any) {
	ls, ult, extra := p.traf.Consultar(desde, limite)
	s := p.Saude()
	s["logado"] = p.Logado()
	for k, v := range extra {
		s[k] = v
	}
	return ls, ult, s
}

// registrarLogin (v1.21 §2): cada login que foi à rede, ou que a guarda barrou: o último no
// /saude.flexscan e uma linha LOGIN no tráfego (sem usuário nem senha).
func (p *Ponte) registrarLogin(motivo string, err error, dur time.Duration) {
	resultado, status, texto := "OK", http.StatusOK, motivo+" OK"
	var el *ErroLogin
	if err != nil {
		resultado, status = "FALHOU", -1
		if errors.As(err, &el) && el.Status > 0 {
			resultado, status = "RECUSADO", el.Status
		}
		texto = motivo + " " + resultado + ": " + err.Error()
	}
	if err != nil && el == nil {
		dur = -1
	}
	p.mu.Lock()
	p.ultLogin = map[string]any{"em": p.rel.Agora().In(relogio.Fuso).Format(formatoEmTrafego), "motivo": motivo, "resultado": resultado}
	p.mu.Unlock()
	p.linhaGravada(TrafegoLogin, msgLogin, status, dur, texto)
}

// Iniciar sobe os trabalhadores e faz o primeiro login (assíncrono).
func (p *Ponte) Iniciar() {
	for i := 0; i < p.cfg.Trabalhadores; i++ {
		p.wg.Add(1)
		go p.trabalhar()
	}
	p.tentarLogin()
}

// Parar encerra os trabalhadores (nada é cancelado na Flexscan: as ordens ficam na bolsa).
func (p *Ponte) Parar() {
	p.cancelar()
	p.mu.Lock()
	p.fila = nil
	p.mu.Unlock()
	p.acordar()
	p.wg.Wait()
}

// Logado: há token válido.
func (p *Ponte) Logado() bool { return p.logado.Load() }

// Entregues: mensagens entregues ao motor (testes).
func (p *Ponte) Entregues() int64 { return p.entregues.Load() }

// Ocupada: há tarefa na fila ou em curso (testes: o executor espera a ponte assentar).
func (p *Ponte) Ocupada() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.fila) > 0 || p.emCurso > 0 || p.logando
}

// Saude: o bloco "flexscan" do /saude.
func (p *Ponte) Saude() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	vivas := 0
	for _, o := range p.ordens {
		if !o.fase.Terminal() {
			vivas++
		}
	}
	var exp, ult any
	if r := p.cli.renovarEm(0, p.cfg.VidaToken); !r.IsZero() {
		exp = r.Format(time.RFC3339)
	}
	if !p.ultWebhook.IsZero() {
		ult = p.ultWebhook.Format(time.RFC3339)
	}
	var cad, confEm, recadEm, ultLogin any
	if p.webhookCad != nil {
		cad = *p.webhookCad
	}
	if !p.webhookConfEm.IsZero() {
		confEm = p.webhookConfEm.In(relogio.Fuso).Format(time.RFC3339)
	}
	if !p.webhookRecadEm.IsZero() {
		recadEm = p.webhookRecadEm.In(relogio.Fuso).Format(time.RFC3339)
	}
	if p.ultLogin != nil {
		ultLogin = p.ultLogin
	}
	return map[string]any{"token_expira": exp, "webhooks_recebidos": p.webhooks, "ultimo_webhook": ult,
		"ordens_acompanhadas": len(p.ordens), "ordens_vivas": vivas, "ultimo_erro": p.ultimoErro, "receptor": p.cfg.Webhook,
		"webhook_cadastrado": cad, "webhook_conferido_em": confEm, "webhook_recadastrado_em": recadEm, "ultimo_login": ultLogin,
		"webhook_faltas_seguidas": p.whFaltas}
}

// ---------------------------------------------------------------- sessão (login e token)

func (p *Ponte) tentarLogin() {
	p.mu.Lock()
	if p.logando || p.definitivo {
		p.mu.Unlock()
		return
	}
	p.logando = true
	motivo := "partida"
	if p.jaLogou {
		motivo = "reconexao"
	}
	p.jaLogou = true
	p.mu.Unlock()
	go func() {
		err := p.cli.login(p.ctx, motivo)
		p.mu.Lock()
		p.logando = false
		if err != nil {
			p.ultimoErro = err.Error()
			var el *ErroLogin
			if errors.As(err, &el) && el.Definitivo {
				p.definitivo = true
			}
			p.proxLogin = p.rel.Agora().Add(p.cfg.EsperaLogin)
			definitivo := p.definitivo
			p.mu.Unlock()
			slog.Warn("flexscan: login falhou", "erro", err, "definitivo", definitivo)
			if p.logado.Swap(false) {
				p.ouv.Caiu(err.Error())
			}
			p.ouv.Alertar("FLEXSCAN_LOGIN", "FLEXSCAN_LOGIN", "[flexscan] login falhou: "+err.Error()+map[bool]string{true: " (definitivo: não tenta de novo sozinho)", false: ""}[definitivo])
			return
		}
		p.ultimoErro = ""
		p.mu.Unlock()
		p.ouv.Resolver("FLEXSCAN_LOGIN")
		if !p.logado.Swap(true) {
			p.ouv.Logou()
		}
	}()
}

// semSessao: o segundo 401 seguido (ou login impossível) — o conector fica sem sessão.
func (p *Ponte) semSessao(motivo string) {
	if p.logado.Swap(false) {
		p.ouv.Caiu(motivo)
	}
	p.mu.Lock()
	p.ultimoErro = motivo
	if p.proxLogin.IsZero() || p.rel.Agora().After(p.proxLogin) {
		p.proxLogin = p.rel.Agora()
	}
	p.mu.Unlock()
}

// Tick: as ações de tempo — renovação do token, login depois de falha, consultas ativas e periódicas.
// Chamado pelo relógio do serviço (a cada 100 ms) e pelo executor dos roteiros.
func (p *Ponte) Tick() {
	agora := p.rel.Agora()
	// token: renova antes de vencer (sem derrubar a sessão: o token atual vale até o novo chegar)
	if p.logado.Load() {
		if r := p.cli.renovarEm(p.cfg.RenovarAntes, p.cfg.VidaToken); !r.IsZero() && time.Now().After(r) {
			p.mu.Lock()
			renovar := !p.renovando
			p.renovando = true
			p.mu.Unlock()
			if renovar {
				go func() {
					err := p.cli.login(p.ctx, "renovacao")
					p.mu.Lock()
					p.renovando = false
					p.mu.Unlock()
					if err != nil {
						slog.Warn("flexscan: renovação do token falhou", "erro", err)
					}
				}()
			}
		}
		if !p.cli.temToken(p.cfg.VidaToken) {
			p.semSessao("token vencido")
		}
	} else {
		p.mu.Lock()
		vez := !p.proxLogin.IsZero() && agora.After(p.proxLogin) && !p.logando
		p.mu.Unlock()
		if vez {
			p.tentarLogin()
		}
	}
	if !p.logado.Load() {
		return
	}
	p.conferirEsperas(agora)
	p.mu.Lock()
	if p.cfg.WebhookURL != "" && !p.conferindoWebhook && (p.proxConfWebhook.IsZero() || !agora.Before(p.proxConfWebhook)) {
		p.conferindoWebhook = true
		p.proxConfWebhook = agora.Add(p.cfg.ConfereWebhook)
		p.enfileirarLocked(prioConsulta, "", p.conferirWebhook)
	}
	var due []*ordemP
	var expiradas []*ordemP
	for _, o := range p.ordens {
		if o.consultaNaFila {
			continue
		}
		if o.confirmar != "" && p.soltarConsultaWebhookLocked(o, agora) {
			continue
		}
		if !o.ativaDesde.IsZero() {
			if ps := o.passosDaJanela(); o.passo < len(ps) && !agora.Before(o.ativaDesde.Add(ps[o.passo])) {
				o.passo++
				due = append(due, o)
				continue
			}
			if !agora.Before(o.ativaDesde.Add(p.cfg.Confirma)) {
				expiradas = append(expiradas, o)
				continue
			}
		}
		if !o.fase.Terminal() && !agora.Before(o.proxPeriodica) {
			due = append(due, o)
		}
	}
	for _, o := range due {
		o.consultaNaFila = true
		periodica := o.ativaDesde.IsZero()
		o.proxPeriodica = agora.Add(p.cfg.Consulta)
		oo := o
		p.enfileirarLocked(prioConsulta, o.chave(), func(ctx context.Context) {
			p.consultar(ctx, oo, map[bool]string{true: "periodica", false: "ativa"}[periodica])
		})
	}
	var msgs []pnt.Mensagem
	for _, o := range expiradas {
		msgs = append(msgs, p.fimDaJanelaLocked(o)...)
	}
	p.mu.Unlock()
	p.entregar(msgs)
}

// fimDaJanelaLocked: a consulta ativa acabou sem ver o efeito da ação — o pedido pendente se resolve
// como recusado (35=9), para o motor seguir (SPEC §4.4: "sem efeito em 5 s → não sei").
func (p *Ponte) fimDaJanelaLocked(o *ordemP) []pnt.Mensagem {
	o.ativaDesde, o.passo, o.passos = time.Time{}, 0, nil
	if o.pend == nil {
		return nil
	}
	pd := o.pend
	o.pend = nil
	resp := "2"
	if pd.tipo == 'F' {
		resp = "1"
	}
	texto := fmt.Sprintf("[flexscan] %s sem confirmação em %s", map[byte]string{'F': "cancelamento", 'G': "edição"}[pd.tipo], p.cfg.Confirma)
	slog.Warn("flexscan: pedido sem confirmação", "nome", o.nome, "tipo", string(pd.tipo), "cl", pd.cl)
	return []pnt.Mensagem{p.rejeicaoPedido(o, pd, resp, texto, "")}
}

// iniciarAtivaLocked: a janela da consulta ativa começa; quem chama faz a primeira consulta (o +0)
// na hora, as seguintes saem no Tick.
func (p *Ponte) iniciarAtivaLocked(o *ordemP) {
	o.ativaDesde, o.passo, o.passos = p.rel.Agora(), 1, nil
}

// passosDaJanela: os passos da consulta ativa em curso.
func (o *ordemP) passosDaJanela() []time.Duration {
	if o.passos != nil {
		return o.passos
	}
	return passosAtiva
}

// ---------------------------------------------------------------- fila e trabalhadores

func (p *Ponte) enfileirarLocked(prio int, chave string, f func(ctx context.Context)) {
	p.seq++
	p.fila = append(p.fila, &tarefa{prio: prio, chave: chave, seq: p.seq, fazer: f})
	p.acordar()
}

func (p *Ponte) enfileirar(prio int, chave string, f func(ctx context.Context)) {
	p.mu.Lock()
	p.enfileirarLocked(prio, chave, f)
	p.mu.Unlock()
}

func (p *Ponte) acordar() {
	select {
	case p.sinal <- struct{}{}:
	default:
	}
}

// proximaLocked: a de maior prioridade (e mais antiga) cuja ordem não está ocupada — os pedidos de
// uma mesma ordem saem um de cada vez, na ordem em que o motor decidiu.
func (p *Ponte) proximaLocked() *tarefa {
	sort.SliceStable(p.fila, func(i, j int) bool {
		if p.fila[i].prio != p.fila[j].prio {
			return p.fila[i].prio < p.fila[j].prio
		}
		return p.fila[i].seq < p.fila[j].seq
	})
	bloqueadas := map[string]bool{}
	for i, t := range p.fila {
		if t.chave != "" && (p.ocupadas[t.chave] || bloqueadas[t.chave]) {
			bloqueadas[t.chave] = true
			continue
		}
		// uma tarefa de maior prioridade da mesma ordem não passa à frente de uma mais antiga
		if t.chave != "" {
			antiga := false
			for _, u := range p.fila {
				if u != t && u.chave == t.chave && u.seq < t.seq {
					antiga = true
					break
				}
			}
			if antiga {
				bloqueadas[t.chave] = true
				continue
			}
		}
		p.fila = append(p.fila[:i], p.fila[i+1:]...)
		return t
	}
	return nil
}

func (p *Ponte) trabalhar() {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		t := p.proximaLocked()
		if t != nil {
			p.emCurso++
			if t.chave != "" {
				p.ocupadas[t.chave] = true
			}
		}
		p.mu.Unlock()
		if t == nil {
			select {
			case <-p.ctx.Done():
				return
			case <-p.sinal:
				continue
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("flexscan: pânico numa tarefa", "erro", r)
					p.ouv.Alertar("ERRO_INTERNO", "ERRO_INTERNO", fmt.Sprintf("[flexscan] %v", r))
				}
			}()
			if p.ctx.Err() == nil {
				t.fazer(p.ctx)
			}
		}()
		p.mu.Lock()
		p.emCurso--
		if t.chave != "" {
			delete(p.ocupadas, t.chave)
		}
		p.mu.Unlock()
		p.acordar()
	}
}

// entregar: os ERs ao motor, em ordem.
func (p *Ponte) entregar(msgs []pnt.Mensagem) {
	for _, m := range msgs {
		p.entregues.Add(1)
		p.ouv.Recebeu(m)
	}
}

// ---------------------------------------------------------------- Envio (o que o motor manda)

// Enviar: a mensagem FIX que o motor montou vira uma tarefa REST. Sem sessão, falha na hora (o motor
// trata como "não saiu").
func (p *Ponte) Enviar(m *quickfix.Message) (int, error) {
	if !p.logado.Load() {
		return 0, errors.New("conector Flexscan sem sessão")
	}
	tipo, _ := m.Header.GetString(quickfix.Tag(35))
	g := func(t int) string { v, _ := m.Body.GetString(quickfix.Tag(t)); return v }
	switch tipo {
	case "D":
		p.novaOrdem(g)
	case "G", "F":
		return 0, p.pedidoOrdem(tipo[0], g)
	case "AF":
		reqID, conta := g(584), g(1)
		p.enfileirar(prioSaida, "", func(ctx context.Context) { p.massStatus(ctx, reqID, conta) })
	default:
		return 0, fmt.Errorf("flexscan: mensagem %s não suportada", tipo)
	}
	return 0, nil
}

func prioDoNome(nome string) int {
	switch {
	case strings.Contains(nome, "-P-"):
		return prioProtecao
	case strings.Contains(nome, "-S-"):
		return prioSaida
	}
	return prioEntrada
}

func (p *Ponte) novaOrdem(g func(int) string) {
	nome := g(11)
	o := &ordemP{nome: nome, conta: g(1), simbolo: g(55), lado: g(54), clAtual: nome, fase: FasePendente, postEmCurso: true}
	o.qtd, _ = strconv.Atoi(g(38))
	corpo := map[string]any{"symbol": o.simbolo, "side": o.lado, "qtty": o.qtd, "account": o.conta, "exchange": p.cfg.Bolsa,
		"timeInForce": "0", "name": nome}
	if g(847) == strconv.Itoa(pnt.EstrategiaStop) {
		o.tipo = "4"
		o.stop, _ = decimal.NewFromString(g(20828))
		o.preco, _ = decimal.NewFromString(g(20829))
		corpo["ordType"], corpo["stopPrice"], corpo["price"] = "4", numeroJSON(o.stop), numeroJSON(o.preco)
	} else {
		o.tipo = "2"
		o.preco, _ = decimal.NewFromString(g(44))
		corpo["ordType"], corpo["price"] = "2", numeroJSON(o.preco)
	}
	p.mu.Lock()
	p.porNome[nome] = o
	p.porCl[nome] = o
	p.enfileirarLocked(prioDoNome(nome), nome, func(ctx context.Context) { p.post(ctx, o, corpo) })
	p.mu.Unlock()
}

// numeroJSON: o preço inteiro em pontos como número JSON (212160, não "212160" nem 212160.0).
func numeroJSON(d decimal.Decimal) any {
	if d.Equal(d.Truncate(0)) {
		return d.IntPart()
	}
	f, _ := d.Float64()
	return f
}

func (p *Ponte) post(ctx context.Context, o *ordemP, corpo map[string]any) {
	p.registrarEnvio("POST", "/orders", o.nome, "", corpo)
	enviadoEm := p.rel.Agora()
	t0 := time.Now()
	r := p.cli.pedir(ctx, http.MethodPost, "/orders", corpo)
	dur := time.Since(t0)
	p.registrarResposta("POST", o.nome, "", r)
	var criada struct {
		ID string `json:"id"`
	}
	if r.Status == http.StatusAccepted {
		_ = decodificar(r.Corpo, &criada)
	}
	p.traf.linha(TrafegoPOST, "ENV", statusDe(r), duracaoDe(r, dur), o.nome, criada.ID, comResposta(resumoOrdem(corpo), r))
	var msgs []pnt.Mensagem
	p.mu.Lock()
	o.postEmCurso = false
	switch {
	case r.Status == http.StatusAccepted:
		o.aceita202Em = p.rel.Agora()
		var a struct {
			ID string `json:"id"`
		}
		_ = decodificar(r.Corpo, &a)
		if a.ID == "" {
			o.incerta = true
			break
		}
		if o.id == "" { // o webhook pode ter chegado antes (casado pelo name)
			o.id = a.ID
			p.ordens[a.ID] = o
		}
		msgs = append(msgs, p.er(o, "A", "A", nil))
		p.esperarWebhookLocked(o, enviadoEm, esperaAcao, "POST")
		if p.cfg.PrimeiraConsulta > 0 {
			// v1.21: a primeira consulta sai no Tick, depois da espera (o 404 logo depois do 202)
			// a periódica também espera (a ordem nova nasce com ela vencida: era o segundo 404)
			o.ativaDesde, o.passo, o.passos = p.rel.Agora(), 0, passosPost(p.cfg.PrimeiraConsulta)
			o.proxPeriodica = o.ativaDesde.Add(p.cfg.Consulta)
			break
		}
		p.iniciarAtivaLocked(o)
		defer p.consultar(ctx, o, "ativa") // depois de entregar o 150=A (os defers rodam ao contrário)
	case r.Incerta():
		// "não sei": nada ao motor; sem resposta em PRAZO_CONFIRMACAO_S ele pede o mass status, que
		// procura a ordem pelo name (SPEC §4.3)
		o.incerta = true
		p.ultimoErro = "POST sem resposta: " + r.TextoErro()
		slog.Warn("flexscan: POST sem resposta (não sei)", "nome", o.nome, "status", r.Status, "erro", r.Erro)
	default:
		o.fase = FaseRejeitada
		texto := fmt.Sprintf("[flexscan %d] %s", r.Status, r.TextoErro())
		if r.Status == http.StatusUnauthorized {
			texto = "[flexscan] sem sessão (401): " + r.TextoErro()
		}
		msgs = append(msgs, p.erTexto(o, "8", "8", texto, strconv.Itoa(r.ErroAPI().Codigo)))
		if r.Status == http.StatusForbidden {
			defer p.ouv.Alertar("FLEXSCAN_PERMISSAO", "FLEXSCAN_PERMISSAO", "[flexscan] 403 na ordem "+o.nome+": "+r.TextoErro())
		}
	}
	p.mu.Unlock()
	if r.Status == http.StatusUnauthorized {
		p.semSessao("401 depois de novo login")
	}
	p.entregar(msgs)
}

// localizarLocked: a ordem de um G/F — pelo 37 (o id), pelo 41 (ClOrdID KM) ou pelo name.
func (p *Ponte) localizarLocked(id, orig string) *ordemP {
	if o := p.ordens[id]; id != "" && o != nil {
		return o
	}
	if o := p.porCl[orig]; o != nil {
		return o
	}
	return p.porNome[orig]
}

func (p *Ponte) pedidoOrdem(tipo byte, g func(int) string) error {
	cl, orig, id := g(11), g(41), g(37)
	p.mu.Lock()
	defer p.mu.Unlock()
	o := p.localizarLocked(id, orig)
	if o == nil && id != "" {
		// depois de um restart, antes do mass status: a ordem é aprendida pela consulta
		o = &ordemP{id: id, nome: orig, clAtual: orig, fase: FaseDesconhecida}
		p.ordens[id] = o
		p.porNome[orig] = o
	}
	if o == nil {
		return fmt.Errorf("flexscan: ordem %s desconhecida na ponte", orig)
	}
	p.porCl[cl] = o
	pd := &pendencia{tipo: tipo, cl: cl}
	if tipo == 'G' {
		pd.qtd, _ = strconv.Atoi(g(38))
		if g(847) == strconv.Itoa(pnt.EstrategiaStop) {
			pd.stop, _ = decimal.NewFromString(g(20828))
			pd.preco, _ = decimal.NewFromString(g(20829))
		} else {
			pd.preco, _ = decimal.NewFromString(g(44))
		}
	}
	prio := prioSaida
	if strings.Contains(o.nome, "-P-") && tipo == 'G' {
		prio = prioProtecao
	}
	p.enfileirarLocked(prio, o.chave(), func(ctx context.Context) { p.executarPedido(ctx, o, pd) })
	return nil
}

func (p *Ponte) executarPedido(ctx context.Context, o *ordemP, pd *pendencia) {
	p.mu.Lock()
	if o.id == "" || o.fase == FaseDesconhecida {
		p.mu.Unlock()
		// sem id (POST incerto) ou ordem desconhecida: aprende antes
		if o.id != "" {
			p.consultar(ctx, o, "pedido")
		}
		p.mu.Lock()
		if o.id == "" {
			msg := p.rejeicaoPedido(o, pd, map[byte]string{'F': "1", 'G': "2"}[pd.tipo], "[flexscan] ordem sem id (envio sem confirmação)", "")
			p.mu.Unlock()
			p.entregar([]pnt.Mensagem{msg})
			return
		}
	}
	if o.fase.Terminal() {
		msg := p.rejeicaoPedido(o, pd, map[byte]string{'F': "1", 'G': "2"}[pd.tipo], "[flexscan] ordem já "+strings.ToLower(o.fase.String()), "")
		p.mu.Unlock()
		p.entregar([]pnt.Mensagem{msg})
		return
	}
	pd.desde, pd.enviada, pd.rejAntes, pd.quando = p.rel.Agora(), time.Now(), o.rejVista, o.quandoVisto
	o.pend = pd
	id, tipoOrd, qtdAtual := o.id, o.tipo, o.qtd
	p.mu.Unlock()

	caminho := "/orders/" + url.PathEscape(id)
	var r Resposta
	enviadoEm := p.rel.Agora()
	if pd.tipo == 'F' {
		p.registrarEnvio("DELETE", caminho, pd.cl, id, nil)
		t0 := time.Now()
		r = p.cli.pedir(ctx, http.MethodDelete, caminho, nil)
		p.traf.linha(TrafegoDELETE, "ENV", statusDe(r), duracaoDe(r, time.Since(t0)), pd.cl, id, comResposta("cancelamento", r))
		p.registrarResposta("DELETE", pd.cl, id, r)
	} else {
		corpo := map[string]any{"price": numeroJSON(pd.preco)}
		if tipoOrd == "4" {
			corpo["stopPrice"] = numeroJSON(pd.stop)
		}
		if pd.qtd > 0 && pd.qtd != qtdAtual {
			corpo["qtty"] = pd.qtd
		}
		p.registrarEnvio("PUT", caminho, pd.cl, id, corpo)
		t0 := time.Now()
		r = p.cli.pedir(ctx, http.MethodPut, caminho, corpo)
		p.traf.linha(TrafegoPUT, "ENV", statusDe(r), duracaoDe(r, time.Since(t0)), pd.cl, id, comResposta(resumoOrdem(corpo), r))
		p.registrarResposta("PUT", pd.cl, id, r)
	}
	if r.Status == http.StatusUnauthorized {
		p.semSessao("401 depois de novo login")
	}
	p.mu.Lock()
	if o.pend != pd { // resolvido no meio do caminho (webhook)
		p.mu.Unlock()
		return
	}
	pd.saiu = true
	switch {
	case r.Status == http.StatusAccepted || r.Incerta():
		// 202 ou "não sei": a consulta ativa decide (efeito visto → ER; sem efeito em Confirma → 35=9)
		if r.Status == http.StatusAccepted {
			p.esperarWebhookLocked(o, enviadoEm, esperaAcao, map[byte]string{'F': "DELETE", 'G': "PUT"}[pd.tipo])
		}
		p.iniciarAtivaLocked(o)
		p.mu.Unlock()
		p.consultar(ctx, o, "ativa")
		return
	default:
		// 400 (ordem terminal, fora do horário, edição recusada), 401, 403, 404: recusado; o estado
		// real vem da consulta, antes do 35=9 (o "too late" de hoje)
		p.mu.Unlock()
		p.consultar(ctx, o, "pedido")
		p.mu.Lock()
		var msgs []pnt.Mensagem
		if o.pend == pd {
			o.pend = nil
			texto := fmt.Sprintf("[flexscan %d] %s", r.Status, r.TextoErro())
			if foraDoHorario(r) {
				texto = "[flexscan] FORA_DO_HORARIO: " + r.TextoErro()
			}
			m := p.rejeicaoPedido(o, pd, map[byte]string{'F': "1", 'G': "2"}[pd.tipo], texto, strconv.Itoa(r.ErroAPI().Codigo))
			if !o.fase.Terminal() && finalizadaNoTexto(r) {
				// v1.21.1: a recusa diz que a ordem já executou e a consulta ainda não mostra o fill:
				// 39=2, e o motor espera o fill (ESPERA_FILL_POS_RECUSA_S) antes de alertar
				trocar(&m, 39, "2")
			}
			msgs = append(msgs, m)
		}
		p.mu.Unlock()
		p.entregar(msgs)
	}
}

// finalizadaNoTexto (v1.21.1): o 400 de ordem já executada. Simulador, 28/10: "order cannot be
// canceled because it is in the state: Finalizado" (o estado em português).
func finalizadaNoTexto(r Resposta) bool {
	t := strings.ToLower(r.TextoErro())
	return r.Status == http.StatusBadRequest && (strings.Contains(t, "finalizad") || strings.Contains(t, "executad") ||
		strings.Contains(t, "filled") || strings.Contains(t, "finished"))
}

// foraDoHorario: o 400 de horário (o texto exato é [SIM-6]; até lá, pelas palavras).
func foraDoHorario(r Resposta) bool {
	t := strings.ToLower(r.TextoErro())
	return r.Status == http.StatusBadRequest && (strings.Contains(t, "trading hours") || strings.Contains(t, "horario") || strings.Contains(t, "horário") || strings.Contains(t, "market closed"))
}

// consultar: GET /orders/{id} e funde.
func (p *Ponte) consultar(ctx context.Context, o *ordemP, origem string) {
	p.mu.Lock()
	id := o.id
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		o.consultaNaFila = false
		p.mu.Unlock()
	}()
	if id == "" {
		return
	}
	t0 := time.Now()
	r := p.cli.pedir(ctx, http.MethodGet, "/orders/"+url.PathEscape(id), nil)
	dur := time.Since(t0)
	p.traf.consulta()
	if r.Status == http.StatusNotFound && p.aindaNaoRegistrada(o, id, origem, r, dur) {
		return
	}
	if r.Status != http.StatusOK {
		// v1.21: a consulta só vira linha do tráfego quando não é 200 ou quando muda o estado
		p.traf.linha(TrafegoConsulta, "ENV", statusDe(r), duracaoDe(r, dur), o.nome, id, comResposta(origem, r))
	}
	if r.Status == http.StatusUnauthorized {
		p.semSessao("401 depois de novo login")
		return
	}
	if r.Status != http.StatusOK {
		if origem != "periodica" || !r.Incerta() {
			p.registrarResposta("GET", o.nome, id, r)
		}
		return
	}
	var rec map[string]any
	if err := decodificar(r.Corpo, &rec); err != nil {
		return
	}
	e := LerConsulta(rec)
	if e.ID == "" {
		e.ID = id
	}
	p.mu.Lock()
	msgs, avisos := p.fundirLocked(o, e, origem)
	if len(msgs) > 0 {
		p.mu.Unlock()
		p.registrarEstado("GET", o.nome, id, r.Corpo)
		p.traf.linha(TrafegoConsulta, "ENV", r.Status, dur, o.nome, id, origem+": "+resumoEstado(e))
		p.mu.Lock()
	}
	p.mu.Unlock()
	p.entregar(msgs)
	avisos.emitir(p)
}

// ---------------------------------------------------------------- fusão (SPEC §4.3)

type avisos []func(*Ponte)

func (a avisos) emitir(p *Ponte) {
	for _, f := range a {
		f(p)
	}
}

func (p *Ponte) fundirLocked(o *ordemP, e Estado, origem string) ([]pnt.Mensagem, avisos) {
	var msgs []pnt.Mensagem
	var av avisos
	if e.Fonte == "consulta" {
		o.rejVista = e.Rejeicao
		if !e.Quando.IsZero() {
			o.quandoVisto = e.Quando
		}
	}
	if o.id == "" && e.ID != "" {
		o.id = e.ID
		p.ordens[e.ID] = o
	}
	if e.Fonte == "webhook" && (e.Fase == FaseExecutada || e.Fase == FaseCancelada) {
		o.whFinal = true
	}
	if o.conta == "" {
		o.conta, o.simbolo, o.lado, o.tipo, o.qtd = e.Conta, e.Simbolo, e.Lado, e.Tipo, e.Qtd
	}
	if o.fase == FaseDesconhecida && e.Fonte == "consulta" {
		// aprendida agora (restart): o estado entregue é o que o motor já tem; parte daqui
		o.preco, o.stop = e.Preco, e.Stop
	}
	espontanea := false
	// 1. executado só sobe
	if e.Exec > o.exec {
		if e.Fonte == "webhook" && p.cfg.ConfirmarFill && o.tipo == "2" && o.pend == nil {
			// fill espontâneo de limite pelo webhook: o que vale é a consulta (SPEC §5.2 item 6), que
			// respeita a espera desde o 202 (v1.24)
			p.consultaDoWebhookLocked(o, "confirma")
			return nil, nil
		}
		if o.pend == nil && o.ativaDesde.IsZero() {
			espontanea = true
		}
		if !o.aceitaEntregue {
			msgs = append(msgs, p.er(o, "0", "0", nil))
			o.aceitaEntregue = true
		}
		delta := e.Exec - o.exec
		o.exec = e.Exec
		if e.Medio.IsPositive() {
			o.medio = e.Medio
		} else if e.UltPx.IsPositive() && o.medio.IsZero() {
			o.medio = e.UltPx
		}
		st := "1"
		if o.qtd > 0 && o.exec >= o.qtd {
			st = "2"
		}
		px := e.UltPx
		if !px.IsPositive() || e.UltQtd != delta {
			px = o.medio
		}
		msgs = append(msgs, p.er(o, "F", st, map[int]string{14: strconv.Itoa(o.exec), 6: o.medio.String(), 32: strconv.Itoa(delta), 31: px.String(),
			17: fmt.Sprintf("%s:F:%d", o.id, o.exec)}))
	}
	// 2. aceite (viva)
	if !o.aceitaEntregue && e.Fase == FaseViva {
		st := "0"
		if o.exec > 0 {
			st = "1"
		}
		msgs = append(msgs, p.er(o, "0", st, nil))
		o.aceitaEntregue = true
	}
	// 3. edição pendente: decide o estado, não o valor (o preço pode ter sido ajustado ao túnel)
	if pd := o.pend; pd != nil && pd.tipo == 'G' && !e.Fase.Terminal() {
		aceita, recusada := false, false
		switch e.Fonte {
		case "consulta":
			aceita = e.Bruto == "5" && (pd.quando.IsZero() || e.Quando.After(pd.quando)) && pd.saiu
			recusada = !aceita && pd.saiu && e.Rejeicao != "" && e.Rejeicao != "0" && e.Rejeicao != pd.rejAntes
		case "webhook":
			aceita = e.Edicao == EdicaoAceita && (o.tipo != "4" || e.Stop.Equal(pd.stop)) && (o.tipo == "4" || e.Preco.Equal(pd.preco))
			recusada = e.Edicao == EdicaoRecusada && time.Now().After(pd.enviada)
		}
		switch {
		case aceita:
			antes := o.clAtual
			o.clAtual = pd.cl
			o.pend = nil
			o.ativaDesde = time.Time{}
			if pd.qtd > 0 {
				o.qtd = pd.qtd
			}
			if !e.Preco.Equal(pd.preco) && e.Preco.IsPositive() {
				av = append(av, p.avisoPrecoAjustado(o, pd.preco, e.Preco))
			}
			o.preco, o.stop = e.Preco, e.Stop
			if o.tipo == "4" && !e.Stop.IsPositive() {
				o.stop = pd.stop
			}
			st := "0"
			if o.exec > 0 {
				st = "1"
			}
			msgs = append(msgs, p.er(o, "5", st, map[int]string{41: antes, 44: e.Preco.String()}))
		case recusada:
			o.pend = nil
			o.ativaDesde = time.Time{}
			texto := "[flexscan] " + strings.TrimSpace(e.Texto)
			if e.Fonte == "consulta" {
				texto = "[flexscan] edição recusada, razão " + e.Rejeicao
			}
			msgs = append(msgs, p.rejeicaoPedido(o, pd, "2", texto, e.Rejeicao))
		}
	} else if o.pend == nil && !e.Fase.Terminal() && e.Fase == FaseViva && o.aceitaEntregue {
		// 4. sem pedido nosso: preço limite diferente = ajuste ao túnel (não é divergência); gatilho
		// diferente = inesperado
		if e.Preco.IsPositive() && o.preco.IsPositive() && !e.Preco.Equal(o.preco) {
			av = append(av, p.avisoPrecoAjustado(o, o.preco, e.Preco))
			o.preco = e.Preco
		}
		if o.tipo == "4" && e.Stop.IsPositive() && o.stop.IsPositive() && !e.Stop.Equal(o.stop) {
			nome, de, para := o.nome, o.stop, e.Stop
			av = append(av, func(p *Ponte) {
				p.ouv.Alertar("FLEXSCAN_ESTADO_INESPERADO", "FLEXSCAN_ESTADO_INESPERADO:"+nome,
					fmt.Sprintf("[flexscan] gatilho de %s mudou sem pedido nosso: %s -> %s", nome, de, para))
			})
			o.stop = e.Stop
		}
	}
	// 5. terminal (definitivo)
	if e.Fase.Terminal() && !o.fase.Terminal() {
		pd := o.pend
		switch e.Fase {
		case FaseCancelada:
			cl := o.clAtual
			extra := map[int]string{}
			if pd != nil && pd.tipo == 'F' {
				extra[41] = o.clAtual
				cl = pd.cl
				o.pend = nil
			} else {
				espontanea = espontanea || (pd == nil && o.ativaDesde.IsZero())
			}
			o.clAtual = cl
			msgs = append(msgs, p.er(o, "4", "4", extra))
		case FaseRejeitada:
			msgs = append(msgs, p.erTexto(o, "8", "8", "[flexscan] "+strings.TrimSpace(e.Texto), e.Rejeicao))
		case FaseExpirada:
			msgs = append(msgs, p.er(o, "C", "C", nil))
		case FaseExecutada:
			if o.exec < o.qtd && e.Fonte == "webhook" {
				// FINISHED sem a quantidade toda: a consulta decide
				p.consultaDoWebhookLocked(o, "confirma")
				return msgs, av
			}
		}
		o.fase = e.Fase
		if o.pend != nil {
			// pedido pendente de uma ordem que terminou: 35=9 com o estado terminal ("too late")
			pd := o.pend
			o.pend = nil
			msgs = append(msgs, p.rejeicaoPedido(o, pd, map[byte]string{'F': "1", 'G': "2"}[pd.tipo], "[flexscan] ordem já "+strings.ToLower(o.fase.String()), ""))
		}
		o.ativaDesde = time.Time{}
	} else if !o.fase.Terminal() {
		switch e.Fase {
		case FaseViva, FaseCancelando:
			o.fase = FaseViva
		case FaseInesperada:
			nome, bruto := o.nome, e.Bruto
			av = append(av, func(p *Ponte) {
				p.ouv.Alertar("FLEXSCAN_ESTADO_INESPERADO", "FLEXSCAN_ESTADO_INESPERADO:"+nome, fmt.Sprintf("[flexscan] %s em estado %s (%s)", nome, bruto, e.Texto))
			})
		}
	}
	// pedido sem efeito resolvido? a janela da consulta ativa termina
	if o.pend == nil && !o.ativaDesde.IsZero() && (o.fase != FasePendente) && o.aceitaEntregue {
		o.ativaDesde = time.Time{}
	}
	// 6. atraso do webhook (v1.22): a consulta periódica viu uma mudança espontânea que ele não
	// trouxe; o webhook dela tem FLEXSCAN_WEBHOOK_ESPERA_S para chegar (webhook_espera.go)
	if espontanea && len(msgs) > 0 && origem == "periodica" {
		p.esperarWebhookLocked(o, p.rel.Agora(), esperaMudanca, resumoEstado(e))
	}
	return msgs, av
}

func (p *Ponte) avisoPrecoAjustado(o *ordemP, de, para decimal.Decimal) func(*Ponte) {
	nome, id := o.nome, o.id
	return func(p *Ponte) {
		slog.Info("flexscan: preço ajustado pela Flexscan (túnel)", "nome", nome, "enviado", de, "efetivo", para)
		p.traf.linha(TrafegoPreco, "REC", -1, -1, nome, id, fmt.Sprintf("enviado %s efetivo %s", de, para))
		p.ouv.Registrar(repo.EventoFIX{Direcao: "REC", MsgType: "FLX-PRECO", ClOrdID: nome, OrderID: id,
			Texto: fmt.Sprintf("preço ajustado pela Flexscan: %s -> %s", de, para)})
	}
}

// ---------------------------------------------------------------- ERs sintéticos

var seqExec atomic.Int64

func (p *Ponte) base(o *ordemP, tipo string) pnt.Mensagem {
	m := pnt.Mensagem{Tipo: tipo}
	m.Campos = append(m.Campos, pnt.Campo{Tag: 35, Valor: tipo})
	return m
}

func add(m *pnt.Mensagem, tag int, v string) {
	if v != "" {
		m.Campos = append(m.Campos, pnt.Campo{Tag: tag, Valor: v})
	}
}

// trocar: o valor de uma tag já posta (e a bruta de novo).
func trocar(m *pnt.Mensagem, tag int, v string) {
	for i := range m.Campos {
		if m.Campos[i].Tag == tag {
			m.Campos[i].Valor = v
		}
	}
	bruta(m)
}

func bruta(m *pnt.Mensagem) {
	var b strings.Builder
	for i, c := range m.Campos {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(strconv.Itoa(c.Tag))
		b.WriteByte('=')
		b.WriteString(c.Valor)
	}
	m.Bruta = "FLX|" + b.String()
}

// er: um ExecutionReport (35=8) da ordem; extra sobrescreve/acrescenta tags.
func (p *Ponte) er(o *ordemP, et, st string, extra map[int]string) pnt.Mensagem {
	m := p.base(o, "8")
	cl := o.clAtual
	if v, ok := extra[11]; ok {
		cl = v
	}
	add(&m, 11, cl)
	add(&m, 41, extra[41])
	id := o.id
	if id == "" {
		id = "NONE"
	}
	add(&m, 37, id)
	ex := extra[17]
	if ex == "" {
		ex = fmt.Sprintf("%s:%s:%s:%d:%d", id, et, st, o.exec, seqExec.Add(1))
	}
	add(&m, 17, ex)
	add(&m, 150, et)
	add(&m, 39, st)
	add(&m, 1, o.conta)
	add(&m, 55, o.simbolo)
	add(&m, 54, o.lado)
	if o.qtd > 0 {
		add(&m, 38, strconv.Itoa(o.qtd))
	}
	if _, ok := extra[14]; !ok {
		add(&m, 14, strconv.Itoa(o.exec))
	}
	if _, ok := extra[6]; !ok && o.exec > 0 {
		add(&m, 6, o.medio.String())
	}
	if _, ok := extra[44]; !ok && o.preco.IsPositive() {
		add(&m, 44, o.preco.String())
	}
	ks := make([]int, 0, len(extra))
	for k := range extra {
		if k != 11 && k != 41 && k != 17 {
			ks = append(ks, k)
		}
	}
	sort.Ints(ks)
	for _, k := range ks {
		add(&m, k, extra[k])
	}
	bruta(&m)
	return m
}

func (p *Ponte) erTexto(o *ordemP, et, st, texto, codigo string) pnt.Mensagem {
	return p.er(o, et, st, map[int]string{58: texto58(texto), 103: codigoFLX(codigo)})
}

// texto58: o motivo em ASCII, sem o prefixo [flexscan] (o código FLX já diz de onde vem).
func texto58(t string) string { return alerta.ASCII(strings.TrimPrefix(t, "[flexscan] ")) }

// codigoFLX: o código da Flexscan, ou FLX quando ela não deu um (o motor mostra "[código] texto").
func codigoFLX(c string) string {
	if c == "" || c == "0" {
		return "FLX"
	}
	return c
}

// rejeicaoPedido: 35=9 do pedido pendente (434: 1 cancel, 2 replace), com o estado atual no 39.
func (p *Ponte) rejeicaoPedido(o *ordemP, pd *pendencia, resp, texto, codigo string) pnt.Mensagem {
	m := p.base(o, "9")
	add(&m, 11, pd.cl)
	add(&m, 41, o.clAtual)
	id := o.id
	if id == "" {
		id = "NONE"
	}
	add(&m, 37, id)
	add(&m, 39, statusFIX(o))
	add(&m, 434, resp)
	add(&m, 58, texto58(texto))
	add(&m, 102, codigoFLX(codigo))
	bruta(&m)
	return m
}

// statusFIX: o 39 do estado que a ponte conhece.
func statusFIX(o *ordemP) string {
	switch o.fase {
	case FaseExecutada:
		return "2"
	case FaseCancelada:
		return "4"
	case FaseRejeitada:
		return "8"
	case FaseExpirada:
		return "C"
	case FasePendente:
		return "A"
	}
	if o.exec > 0 {
		return "1"
	}
	return "0"
}

// ---------------------------------------------------------------- mass status (conciliação)

// nomeDoMotor: o ClOrdID que o motor gera (KM-[MÓDULO-]E|P|S-<op>-<n>). Ordens com KM- fora desse
// formato (as da sondagem manual) não são do motor.
var nomeDoMotor = regexp.MustCompile(`^KM-(?:[A-Z][A-Z0-9]{1,7}-)?[EPS]-[0-9]+-[0-9]+$`)

// massStatus: o AF de uma conta — as ordens KM da conta na Flexscan (GET /orders/filter), como
// 150=I com 911 (total) e 912=Y na última. Vivas, todas as KM; terminais, só as do motor de hoje.
// Ordem que ficou "não sei" no POST e ainda não aparece: até 3 consultas (SIM-3) antes de responder.
func (p *Ponte) massStatus(ctx context.Context, reqID, conta string) {
	var recs []Estado
	for tentativa := 0; tentativa < 3; tentativa++ {
		var ok bool
		recs, ok = p.listarConta(ctx, conta)
		if !ok {
			slog.Warn("flexscan: mass status sem resposta", "conta", conta)
			return // o motor fecha a conciliação pela espera máxima, sem julgar a conta
		}
		if !p.faltamIncertas(conta, recs) {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	// "hoje" pelo relógio do serviço, no fuso de Brasília (o sendingTime vem em UTC: 21:00 BRT já é o
	// dia seguinte em UTC)
	hoje := p.rel.Agora().In(fuso).Format("20060102")
	var msgs []pnt.Mensagem
	// POST "não sei" que não aparece depois das consultas: não foi criada — a entrada encerra sem
	// reenvio, a proteção é recriada pelo motor com outro name (SPEC §2 item 3); vai antes das
	// respostas do mass status, para a ordem já estar encerrada quando ele julgar
	msgs = append(msgs, p.naoEncontradas(conta, recs)...)
	var sel []Estado
	for _, e := range recs {
		if !strings.HasPrefix(e.Nome, "KM-") {
			continue
		}
		if e.Fase.Terminal() && (!nomeDoMotor.MatchString(e.Nome) || e.Quando.In(fuso).Format("20060102") != hoje) {
			continue
		}
		sel = append(sel, e)
	}
	p.mu.Lock()
	for i, e := range sel {
		o := p.ordens[e.ID]
		if o == nil {
			o = p.porNome[e.Nome]
		}
		if o == nil {
			o = &ordemP{nome: e.Nome, clAtual: e.Nome}
			p.porNome[e.Nome] = o
			p.porCl[e.Nome] = o
		}
		o.id, o.conta, o.simbolo, o.lado, o.tipo, o.qtd = e.ID, e.Conta, e.Simbolo, e.Lado, e.Tipo, e.Qtd
		p.ordens[e.ID] = o
		o.incerta = false
		if e.Exec > o.exec {
			o.exec, o.medio = e.Exec, e.Medio
		}
		if !o.fase.Terminal() {
			o.fase = e.Fase
		}
		o.preco, o.stop, o.rejVista, o.quandoVisto = e.Preco, e.Stop, e.Rejeicao, e.Quando
		o.aceitaEntregue = o.aceitaEntregue || e.Fase == FaseViva || e.Fase.Terminal()
		extra := map[int]string{584: reqID, 911: strconv.Itoa(len(sel)), 17: fmt.Sprintf("MS:%s:%s", reqID, e.ID), 151: strconv.Itoa(max(o.qtd-o.exec, 0))}
		if o.tipo == "4" && o.stop.IsPositive() {
			// v1.22: o gatilho e o limite da stop, como no PnT, para o motor reconstituir a 1014 que
			// saiu e não chegou ao banco
			extra[20828], extra[20829] = o.stop.String(), o.preco.String()
		}
		if i == len(sel)-1 {
			extra[912] = "Y"
		}
		msgs = append(msgs, p.er(o, "I", statusFIX(o), extra))
	}
	if len(sel) == 0 {
		// conta sem ordens KM: como o PnT, 35=j 380=101 "No orders found." (v1.3 E2)
		m := pnt.Mensagem{Tipo: "j"}
		for _, c := range []pnt.Campo{{Tag: 35, Valor: "j"}, {Tag: 372, Valor: "AF"}, {Tag: 379, Valor: reqID}, {Tag: 380, Valor: "101"},
			{Tag: 58, Valor: "No orders found."}, {Tag: 1, Valor: conta}} {
			m.Campos = append(m.Campos, c)
		}
		bruta(&m)
		msgs = append(msgs, m)
	}
	p.mu.Unlock()
	p.entregar(msgs)
}

// naoEncontradas: as ordens desta conta com POST "não sei" fora da lista — 150=8 para cada uma.
func (p *Ponte) naoEncontradas(conta string, recs []Estado) []pnt.Mensagem {
	nomes := map[string]bool{}
	for _, e := range recs {
		nomes[e.Nome] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var msgs []pnt.Mensagem
	for _, o := range p.porNome {
		if o.incerta && o.conta == conta && !nomes[o.nome] {
			o.incerta = false
			o.fase = FaseRejeitada
			slog.Warn("flexscan: ordem com POST sem resposta não encontrada pelo name", "nome", o.nome, "conta", conta)
			msgs = append(msgs, p.erTexto(o, "8", "8", "[flexscan] envio sem resposta e ordem nao encontrada pelo name: nao foi criada", ""))
		}
	}
	return msgs
}

// faltamIncertas: alguma ordem desta conta com POST "não sei" ainda não aparece na lista.
func (p *Ponte) faltamIncertas(conta string, recs []Estado) bool {
	nomes := map[string]bool{}
	for _, e := range recs {
		nomes[e.Nome] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, o := range p.porNome {
		if o.incerta && o.conta == conta && !nomes[o.nome] {
			return true
		}
	}
	return false
}

// listarConta: GET /orders/filter?account=, todas as páginas.
func (p *Ponte) listarConta(ctx context.Context, conta string) ([]Estado, bool) {
	var r []Estado
	offset := 0
	for pagina := 0; pagina < 20; pagina++ {
		q := url.Values{"account": {conta}, "limit": {"1000"}, "offset": {strconv.Itoa(offset)}, "detailed": {"1"}}
		resp := p.cli.pedir(ctx, http.MethodGet, "/orders/filter?"+q.Encode(), nil)
		if resp.Status == http.StatusUnauthorized {
			p.semSessao("401 depois de novo login")
			return nil, false
		}
		if resp.Status != http.StatusOK {
			p.registrarResposta("GET", "", "", resp)
			return nil, false
		}
		var col struct {
			Records []map[string]any `json:"records"`
			HasNext bool             `json:"has_next"`
		}
		if err := decodificar(resp.Corpo, &col); err != nil {
			return nil, false
		}
		for _, rec := range col.Records {
			r = append(r, LerConsulta(rec))
		}
		if !col.HasNext || len(col.Records) == 0 {
			return r, true
		}
		offset += len(col.Records)
	}
	return r, true
}

// ---------------------------------------------------------------- webhook

// ReceberWebhook: um corpo já validado pelo receptor (segredo certo). Volta erro se a forma é
// desconhecida (o receptor responde 200 assim mesmo, SPEC §5.2 item 1).
func (p *Ponte) ReceberWebhook(corpo []byte, origem string) error {
	p.mu.Lock()
	p.webhooks++
	p.ultWebhook = time.Now()
	p.mu.Unlock()
	p.ouv.Registrar(repo.EventoFIX{Direcao: "REC", MsgType: "FLX-WH", Texto: "origem " + origem, Bruta: mascararCorpo(string(corpo))})
	es, err := LerWebhook(corpo)
	p.registrarWebhook(es, err)
	if err != nil {
		p.ouv.Alertar("FLEXSCAN_WEBHOOK_INVALIDO", "FLEXSCAN_WEBHOOK_INVALIDO", "[flexscan] webhook com forma desconhecida: "+err.Error())
		p.mu.Lock()
		for _, o := range p.ordens {
			if !o.fase.Terminal() && !o.consultaNaFila {
				p.consultaDoWebhookLocked(o, "webhook_invalido")
			}
		}
		p.mu.Unlock()
		return err
	}
	p.webhookChegou()
	p.enfileirar(prioProtecao, "", func(ctx context.Context) { p.aplicarWebhook(es) })
	return nil
}

func (p *Ponte) aplicarWebhook(es []Estado) {
	var msgs []pnt.Mensagem
	var av avisos
	p.mu.Lock()
	for _, e := range es {
		if !strings.HasPrefix(e.Nome, "KM-") {
			continue
		}
		o := p.ordens[e.ID]
		if o == nil {
			o = p.porNome[e.Nome]
		}
		if o == nil {
			// KM sem registro na ponte: aprende pela consulta (o motor decide se conhece)
			o = &ordemP{id: e.ID, nome: e.Nome, clAtual: e.Nome, fase: FaseDesconhecida}
			p.ordens[e.ID], p.porNome[e.Nome], p.porCl[e.Nome] = o, o, o
			oo := o
			o.consultaNaFila = true
			p.enfileirarLocked(prioConsulta, o.chave(), func(ctx context.Context) { p.consultarDesconhecida(ctx, oo) })
			continue
		}
		if motivo := p.incoerente(o, e); motivo != "" {
			nome := e.Nome
			av = append(av, func(p *Ponte) {
				p.ouv.Alertar("FLEXSCAN_WEBHOOK_INCOERENTE", "FLEXSCAN_WEBHOOK_INCOERENTE",
					fmt.Sprintf("[flexscan] webhook de %s descartado: %s", nome, motivo))
			})
			continue
		}
		if o.fase == FaseDesconhecida {
			continue // a consulta em curso decide
		}
		p.webhookDaOrdemLocked(o, p.rel.Agora())
		m, a := p.fundirLocked(o, e, "webhook")
		msgs = append(msgs, m...)
		av = append(av, a...)
	}
	p.mu.Unlock()
	p.entregar(msgs)
	av.emitir(p)
}

// incoerente: o retrato não bate com a ordem (pode ser forjado ou bug) — SPEC §5.2 item 4 e 5.
func (p *Ponte) incoerente(o *ordemP, e Estado) string {
	switch {
	case o.id != "" && e.ID != o.id:
		return fmt.Sprintf("ClOrdID %s, esperado %s", e.ID, o.id)
	case o.conta != "" && e.Conta != "" && e.Conta != o.conta:
		return fmt.Sprintf("conta %s, esperada %s", e.Conta, o.conta)
	case o.simbolo != "" && e.Simbolo != "" && e.Simbolo != o.simbolo:
		return fmt.Sprintf("símbolo %s, esperado %s", e.Simbolo, o.simbolo)
	case o.lado != "" && e.Lado != "" && e.Lado != o.lado:
		return fmt.Sprintf("lado %s, esperado %s", e.Lado, o.lado)
	case e.Usuario != "" && e.Usuario != p.cfg.Usuario:
		return fmt.Sprintf("usuário %s", e.Usuario)
	case e.Exec < 0 || (e.Qtd > 0 && e.Exec > e.Qtd):
		return fmt.Sprintf("executado %d de %d", e.Exec, e.Qtd)
	}
	return ""
}

// consultarDesconhecida: KM que a ponte não conhecia (webhook): a consulta traz o estado e o motor
// recebe como qualquer ER (se ele também não conhece, é o DESCONHECIDA de sempre).
func (p *Ponte) consultarDesconhecida(ctx context.Context, o *ordemP) {
	defer func() { p.mu.Lock(); o.consultaNaFila = false; p.mu.Unlock() }()
	r := p.cli.pedir(ctx, http.MethodGet, "/orders/"+url.PathEscape(o.id), nil)
	if r.Status != http.StatusOK {
		return
	}
	var rec map[string]any
	if decodificar(r.Corpo, &rec) != nil {
		return
	}
	e := LerConsulta(rec)
	p.mu.Lock()
	o.conta, o.simbolo, o.lado, o.tipo, o.qtd, o.preco, o.stop = e.Conta, e.Simbolo, e.Lado, e.Tipo, e.Qtd, e.Preco, e.Stop
	o.fase = FasePendente
	msgs, av := p.fundirLocked(o, e, "consulta")
	p.mu.Unlock()
	p.entregar(msgs)
	av.emitir(p)
}

// ---------------------------------------------------------------- registro (sem segredo)

var reDoc = regexp.MustCompile(`("DocNumber"\s*:\s*")[^"]*(")`)

func mascararCorpo(s string) string {
	s = reDoc.ReplaceAllString(s, "${1}***${2}")
	return cortar(s, 16000)
}

func (p *Ponte) registrarEnvio(metodo, caminho, cl, id string, corpo map[string]any) {
	b := metodo + " " + caminho
	if corpo != nil {
		ks := make([]string, 0, len(corpo))
		for k := range corpo {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var partes []string
		for _, k := range ks {
			partes = append(partes, fmt.Sprintf("%s=%v", k, corpo[k]))
		}
		b += " " + strings.Join(partes, " ")
	}
	p.ouv.Registrar(repo.EventoFIX{Direcao: "ENV", MsgType: "FLX-" + metodo, ClOrdID: cl, OrderID: id, Bruta: b})
}

func (p *Ponte) registrarResposta(metodo, cl, id string, r Resposta) {
	tipo := "FLX-" + strconv.Itoa(r.Status)
	if r.Status == 0 {
		tipo = "FLX-SEMRESPOSTA"
	}
	texto := ""
	if r.Status != http.StatusAccepted && r.Status != http.StatusOK {
		texto = r.TextoErro()
	}
	p.ouv.Registrar(repo.EventoFIX{Direcao: "REC", MsgType: tipo, ClOrdID: cl, OrderID: id, Texto: cortar(metodo+" "+texto, 1000),
		Codigo: codigoTexto(r.ErroAPI().Codigo), Bruta: mascararCorpo(string(r.Corpo))})
}

func (p *Ponte) registrarEstado(metodo, cl, id string, corpo []byte) {
	p.ouv.Registrar(repo.EventoFIX{Direcao: "REC", MsgType: "FLX-" + metodo, ClOrdID: cl, OrderID: id, Bruta: mascararCorpo(string(corpo))})
}

func codigoTexto(c int) string {
	if c == 0 {
		return ""
	}
	return strconv.Itoa(c)
}

// registrarWebhook (v1.21): a linha do tráfego de um webhook recebido (o receptor respondeu 200).
func (p *Ponte) registrarWebhook(es []Estado, err error) {
	if err != nil {
		p.traf.linha(TrafegoWebhook, "REC", http.StatusOK, -1, "", "", "forma desconhecida: "+err.Error())
		return
	}
	cl, id, resumo, nomes := resumoWebhook(es)
	p.traf.linha(TrafegoWebhook, "REC", http.StatusOK, -1, cl, id, resumo, nomes...)
}

// resumoWebhook: os retratos de um webhook (o clordid e o id quando é uma ordem só; com vários, os
// clordid de todos em nomes, para quem consulta achar a operação).
func resumoWebhook(es []Estado) (cl, id, resumo string, nomes []string) {
	if len(es) == 1 {
		cl, id = es[0].Nome, es[0].ID
	}
	partes := make([]string, 0, len(es))
	for _, e := range es {
		partes = append(partes, e.Nome+" "+resumoEstado(e))
		if len(es) > 1 {
			nomes = append(nomes, e.Nome)
		}
	}
	return cl, id, fmt.Sprintf("%d retrato(s): %s", len(es), strings.Join(partes, "; ")), nomes
}

// linhaGravada: as linhas que só o tráfego conhece (LOGIN, WEBHOOK_CADASTRO) também vão ao
// kalmanordemevento, para o recarregamento da partida (sem segredo: só o resumo e o status).
func (p *Ponte) linhaGravada(tipo, msgtype string, status int, dur time.Duration, resumo string) {
	p.traf.linha(tipo, "ENV", status, dur, "", "", resumo)
	cod := ""
	if status >= 0 {
		cod = strconv.Itoa(status)
	}
	p.ouv.Registrar(repo.EventoFIX{Direcao: "ENV", MsgType: msgtype, Texto: resumoTrafego(resumo), Codigo: cod})
}

// WebhookRecusado (v1.21): o receptor recusou (404) um pedido; o motivo, nunca o caminho.
func (p *Ponte) WebhookRecusado(motivo string) {
	p.traf.linha(TrafegoWebhook, "REC", http.StatusNotFound, -1, "", "", "recusado: "+motivo)
}

// duracaoDe: sem resposta, sem duração.
func duracaoDe(r Resposta, d time.Duration) time.Duration {
	if r.Status == 0 {
		return -1
	}
	return d
}
