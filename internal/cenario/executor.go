// Package cenario executa os roteiros (cenarios/*.fix) contra o serviço completo: motor, sessão
// FIX real (camada PnT) e API HTTP, com o PnT falso do outro lado, o relógio e o preço controlados.
package cenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/api"
	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/flexscan"
	flxfalso "gammaquant.com.br/kalman-ordens/internal/flexscan/falso"
	"gammaquant.com.br/kalman-ordens/internal/motor"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/internal/roteiro"
	"gammaquant.com.br/kalman-ordens/pnt"
	"gammaquant.com.br/kalman-ordens/pnt/falso"
)

const (
	esperaMensagem = 2 * time.Second
	passoRelogio   = 100 * time.Millisecond
	// UIDRoteiro: o uid dos pedidos de entrada e saida (uid=N troca); _comum.fix o habilita.
	UIDRoteiro = 7
)

// notificador guarda o que iria ao Telegram.
type notificador struct {
	mu sync.Mutex
	n  []string
}

func (n *notificador) Notificar(codigo, texto string) {
	n.mu.Lock()
	n.n = append(n.n, codigo+": "+texto)
	n.mu.Unlock()
}

type servico struct {
	motor    *motor.Motor
	sessao   *pnt.Sessao
	http     http.Handler
	baseEnv  int64              // falso.EnviadasApp() ao subir
	baseRec  int64              // falso.RecebidasApp() ao subir
	ponte    *flexscan.Ponte    // SPEC-FLEXSCAN-v1: com config FLEXSCAN_URL=falso
	receptor *flexscan.Receptor // a rota do webhook (chamada direto, sem listener)
}

// SegredoWebhookRoteiro: o FLEXSCAN_WEBHOOK_SEGREDO dos roteiros (o teste de máscara procura por ele
// no que foi gravado).
const SegredoWebhookRoteiro = "segredo-do-roteiro-0123456789abcdefghijkl"

// SenhaFlexscanRoteiro: a FLEXSCAN_SENHA dos roteiros (idem).
const SenhaFlexscanRoteiro = "senha-do-roteiro-flexscan"

// Executor de um cenário.
type Executor struct {
	c           roteiro.Cenario
	dir         string
	rel         *relogio.Controlado
	preco       *preco.Falsa
	repo        repo.Repositorio
	falha       *repoComFalha // v1.13: erro de dado injetado (banco_erro_dado)
	backend     Backend
	notif       *notificador
	pnt         *falso.Falso
	svc         *servico
	flx         *flxfalso.Falso // a Flexscan falsa (só com config FLEXSCAN_URL=falso)
	cfg         config.Config
	capturas    map[string]string
	resposta    map[string]any
	casados     map[int64]bool
	sessaoOK    int
	logons      int64
	travado     chan struct{} // motor_lento
	tsPorOp     map[string]string
	logonsMotor int                             // logons tratados pelo motor em execução (zera a cada subida)
	async       chan *httptest.ResponseRecorder // http_async em curso
	asyncDesc   string
	Registro    []string // o que aconteceu, para depuração
}

// Backend de persistência do cenário. Novo dá um repositório limpo; Reabrir, o mesmo banco numa
// conexão nova (reinicia_servico).
type Backend struct {
	Nome    string
	Novo    func() (repo.Repositorio, error)
	Reabrir func(repo.Repositorio) (repo.Repositorio, error)
}

// Memoria é o backend em memória: reabrir é o mesmo objeto (o que foi salvo continua lá).
var Memoria = Backend{
	Nome:    "memoria",
	Novo:    func() (repo.Repositorio, error) { return repo.NovaMemoria(), nil },
	Reabrir: func(r repo.Repositorio) (repo.Repositorio, error) { return r, nil },
}

// MariaDB devolve o backend sobre o schema de teste (limpo e migrado a cada cenário).
func MariaDB(dsn string) Backend {
	return Backend{
		Nome: "mariadb",
		Novo: func() (repo.Repositorio, error) {
			r, err := repo.AbrirMariaDB(dsn)
			if err != nil {
				return nil, err
			}
			if err := r.LimparTeste(); err != nil {
				return nil, err
			}
			if err := r.Migrar(); err != nil {
				return nil, err
			}
			return r, r.PrepararUsuariosTeste()
		},
		Reabrir: func(velho repo.Repositorio) (repo.Repositorio, error) {
			velho.(*repo.MariaDB).Fechar()
			r, err := repo.AbrirMariaDB(dsn)
			if err != nil {
				return nil, err
			}
			if err := r.Migrar(); err != nil {
				return nil, err
			}
			return r, r.PrepararUsuariosTeste()
		},
	}
}

// Executar roda o cenário; o primeiro passo que falha devolve erro com arquivo:linha.
func Executar(c roteiro.Cenario, dir string, b Backend) (err error) {
	e := &Executor{c: c, dir: dir, backend: b, capturas: map[string]string{}, casados: map[int64]bool{}, tsPorOp: map[string]string{}}
	defer e.encerrar()
	if err := e.preparar(); err != nil {
		return err
	}
	for _, p := range c.Passos {
		if err := e.passo(p); err != nil {
			return fmt.Errorf("%s: %s: %w", p.Origem, p.Verbo, err)
		}
	}
	return e.conferirASCII()
}

func (e *Executor) preparar() error {
	e.rel = relogio.NovoControlado(e.c.Hora)
	e.preco = preco.NovaFalsa(e.rel)
	r, err := e.backend.Novo()
	if err != nil {
		return err
	}
	e.falha = &repoComFalha{Repositorio: r}
	e.repo = e.falha
	e.notif = &notificador{}
	f, err := falso.Novo(falso.Config{Contraparte: "KALMAN", Dir: filepath.Join(e.dir, "pnt"), Verboso: os.Getenv("FALSO_VERBOSO") != ""})
	if err != nil {
		return err
	}
	if err := f.Iniciar(); err != nil {
		return err
	}
	e.pnt = f
	v := map[string]string{}
	for k, x := range e.c.Config {
		v[k] = x
	}
	if v["FERIADOS_ARQ"] == "" {
		// os feriados de teste (o arquivo real da máquina não entra nos roteiros)
		v["FERIADOS_ARQ"] = filepath.Join("..", "..", "cenarios", "referencia", "feriados-teste.json")
	}
	if v["FLEXSCAN_URL"] == "falso" {
		// SPEC-FLEXSCAN-v1: a Flexscan falsa, com o relógio do roteiro nos horários das ordens
		e.flx = flxfalso.Novo("flx_roteiro", SenhaFlexscanRoteiro, e.rel.Agora)
		v["FLEXSCAN_URL"] = e.flx.URL()
		v["FLEXSCAN_USUARIO"], v["FLEXSCAN_SENHA"] = "flx_roteiro", SenhaFlexscanRoteiro
		for k, x := range map[string]string{"FLEXSCAN_WEBHOOK_ENDERECO": "roteiro", "FLEXSCAN_WEBHOOK_SEGREDO": SegredoWebhookRoteiro,
			"FLEXSCAN_TIMEOUT_MS": "300", "FLEXSCAN_REQ_POR_S": "0", "FLEXSCAN_CONTAS_HML": "S-1117150",
			"FLEXSCAN_ESTADO_LOGIN": filepath.Join(e.dir, "flexscan-login.json"), "FLEXSCAN_LOGIN_MAX": "50"} {
			if v[k] == "" {
				v[k] = x
			}
		}
		e.flx.EsperaTimeout(600 * time.Millisecond)
	}
	e.cfg = config.Novo(v)
	for _, p := range e.c.Preparo {
		if err := e.usuario(p); err != nil {
			return fmt.Errorf("%s: %s: %w", p.Origem, p.Verbo, err)
		}
	}
	return e.subir()
}

// usuario aplica usuario/habilita/desabilita: a configuração do GQ (acusuario de teste) e a
// habilitação (o que a CLI faria).
func (e *Executor) usuario(p roteiro.Passo) error {
	uid, err := strconv.Atoi(p.Args[0])
	if err != nil {
		return fmt.Errorf("uid %q", p.Args[0])
	}
	switch p.Verbo {
	case "usuario":
		u, ok := e.falha.Repositorio.(repo.UsuariosDeTeste)
		if !ok {
			return fmt.Errorf("o backend não aceita usuários de teste")
		}
		valor := ""
		if len(p.Args) > 2 {
			valor = p.Args[2]
		}
		return u.DefinirUsuario(uid, p.Args[1], valor)
	case "habilita", "desabilita":
		h := dominio.Habilitacao{UID: uid, Habilitado: p.Verbo == "habilita", Por: "roteiro", Motivo: p.Origem, Em: e.rel.Agora()}
		for _, a := range p.Args[1:] {
			k, v, _ := strings.Cut(a, "=")
			if k == "conectores" {
				cs, err := dominio.LerConectores(v)
				if err != nil {
					return err
				}
				h.Conectores = cs
				continue
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s", a)
			}
			switch k {
			case "posicao_max":
				h.PosicaoMax = n
			case "qtd_max":
				h.QtdMaxOrdem = n
			default:
				return fmt.Errorf("habilita UID [posicao_max=N] [qtd_max=N] [conectores=pnt,flexscan]: %s", a)
			}
		}
		return e.repo.GravarHabilitacao(h)
	}
	return fmt.Errorf("verbo %s", p.Verbo)
}

func (e *Executor) subir() error {
	m := motor.Novo(e.cfg, e.rel, e.repo, e.preco, e.notif)
	s, err := pnt.NovaSessao(pnt.ConfigSessao{
		SenderCompID: "KALMAN", Host: "127.0.0.1", Porta: e.pnt.Porta(), Dir: filepath.Join(e.dir, "fix"),
		Inicio: "00:00:00", Fim: "00:00:00", HeartBtInt: 5, SemLogArquivo: true,
		Usuario: e.cfg.Texto("FIX_USUARIO", ""), Senha: e.cfg.Texto("FIX_SENHA", ""),
		ReconexaoS: e.cfg.Inteiro("FIX_RECONEXAO_S", 1), ReconexaoMaxS: e.cfg.Inteiro("FIX_RECONEXAO_MAX_S", 60),
		SincroniaMs: e.cfg.Inteiro("FIX_SINCRONIA_MS", 500),
		Espelho:     m.AoVivo().Registrar,
	}, m)
	if err != nil {
		return err
	}
	m.Usar(s)
	var ponte *flexscan.Ponte
	var rec *flexscan.Receptor
	if e.flx != nil {
		ponte = flexscan.Nova(flexscan.ConfigDe(e.cfg), m.OuvinteFlexscan(), e.rel)
		m.UsarFlexscan(ponte)
		if rec, err = flexscan.NovoReceptor(ponte, e.cfg.Texto("FLEXSCAN_WEBHOOK_PREFIXO", "/flexscan/webhook/"), e.cfg.Texto("FLEXSCAN_WEBHOOK_SEGREDO", "")); err != nil {
			return err
		}
	}
	if err := m.Iniciar(); err != nil {
		return err
	}
	e.logonsMotor = 0
	e.svc = &servico{motor: m, sessao: s, http: api.Handler(m, e.cfg.Segundos("HTTP_ESPERA_S", 5)), baseEnv: e.pnt.EnviadasApp() + e.pnt.Reentregas(), baseRec: e.pnt.RecebidasApp(),
		ponte: ponte, receptor: rec}
	if err := s.Iniciar(); err != nil {
		return err
	}
	if ponte != nil {
		ponte.Iniciar()
		if !esperarAte(5*time.Second, ponte.Logado) {
			return fmt.Errorf("a ponte Flexscan não logou na Flexscan falsa")
		}
	}
	return nil
}

func (e *Executor) descer() {
	if e.svc == nil {
		return
	}
	if e.svc.ponte != nil {
		e.svc.ponte.Parar()
	}
	e.svc.sessao.Parar()
	e.svc.motor.Parar()
	e.svc = nil
}

func (e *Executor) encerrar() {
	// um passo que falhou com o motor travado (motor_lento) não pode deixar o Parar esperando
	if e.travado != nil {
		close(e.travado)
		e.travado = nil
	}
	e.descer()
	if e.pnt != nil {
		e.pnt.Parar()
	}
	if e.flx != nil {
		e.flx.Parar()
	}
	if m, ok := e.falha.Repositorio.(*repo.MariaDB); ok {
		m.Fechar()
	}
}

// ocioso espera o serviço tratar tudo o que o falso mandou e o falso receber tudo o que o
// serviço mandou.
func (e *Executor) ocioso() {
	if e.travado != nil {
		return // motor_lento: nada anda até motor_normal
	}
	fim := time.Now().Add(3 * time.Second)
	pnte := e.svc.ponte
	for time.Now().Before(fim) {
		proc := e.svc.motor.Processadas()
		var pntParte, flxParte int64
		if e.pnt.Logado() {
			pntParte = e.pnt.EnviadasApp() + e.pnt.Reentregas() - e.svc.baseEnv
		}
		if pnte != nil {
			flxParte = pnte.Entregues() // os ERs sintéticos da ponte também contam no Processadas
		}
		dentro := proc >= pntParte+flxParte || (pnte == nil && !e.pnt.Logado())
		fora := e.pnt.RecebidasApp()-e.svc.baseRec >= e.svc.sessao.Enviadas()
		quieta := pnte == nil || !pnte.Ocupada()
		if dentro && fora && quieta {
			if e.svc.motor.Processadas() == proc && (pnte == nil || (!pnte.Ocupada() && pnte.Entregues() == flxParte)) {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (e *Executor) passo(p roteiro.Passo) error {
	switch p.Verbo {
	case "preco":
		return e.definirPreco(p.Args)
	case "avanca":
		d, _ := roteiro.Duracao(p.Args[0])
		for d > 0 {
			s := passoRelogio
			if d < s {
				s = d
			}
			e.rel.Avancar(s)
			d -= s
			if e.travado != nil {
				// motor_lento: o tick entra na fila atrás do motor parado
				e.svc.motor.TickAssincrono()
				continue
			}
			e.svc.motor.Tick()
			if e.svc.ponte != nil {
				e.svc.ponte.Tick()
			}
			e.ocioso()
		}
		return nil
	case "aguarda_logon":
		n := 1
		if len(p.Args) == 1 {
			n, _ = strconv.Atoi(p.Args[0])
		}
		antes := e.logonsMotor
		if err := e.pnt.EsperarLogon(e.logons+int64(n-1), 10*time.Second); err != nil {
			return err
		}
		e.logons = e.pnt.Logons()
		esperarAte(5*time.Second, e.svc.sessao.Logado)
		// o logon tem de ter chegado ao motor (a sessão marca logada um instante antes)
		esperarAte(5*time.Second, func() bool { return e.svc.motor.LogonsTratados() > antes })
		e.logonsMotor = e.svc.motor.LogonsTratados()
		e.ocioso()
		return nil
	case "derruba_sessao":
		if err := e.pnt.Derrubar(); err != nil {
			return err
		}
		if !esperarAte(5*time.Second, func() bool { return !e.svc.sessao.Logado() }) {
			return fmt.Errorf("o serviço não percebeu a queda")
		}
		e.ocioso()
		return nil
	case "pnt_recusa_logon":
		e.pnt.RecusarLogons(p.Args[0])
		return nil
	case "pnt_aceita_logon":
		e.pnt.RecusarLogons("")
		return nil
	case "aguarda_recusas":
		n, _ := strconv.Atoi(p.Args[0])
		if !esperarAte(15*time.Second, func() bool { return e.pnt.Recusas() >= n }) {
			return fmt.Errorf("%d logon(s) recusado(s) em 15 s, esperava %d", e.pnt.Recusas(), n)
		}
		e.ocioso()
		return nil
	case "pnt_espera_seq", "pnt_esquece":
		n, _ := strconv.Atoi(strings.TrimPrefix(p.Args[0], "+"))
		if p.Verbo == "pnt_esquece" {
			n = -n
		}
		return e.pnt.AjustarSequenciaEsperada(n)
	case "corrompe_store":
		return corromperUltimaDoStore(filepath.Join(e.dir, "fix", "store"))
	case "descarta":
		e.ocioso()
		for i := len(e.pnt.Pendentes()) - 1; i >= 0; i-- {
			if r := e.pnt.Pendentes()[i]; e.casa(p.Campos, r, false) == nil {
				e.pnt.Consumir(i)
			}
		}
		// o que o serviço enviou numa sessão que caiu sem o PnT falso processar não chega mais
		e.svc.baseRec = e.pnt.RecebidasApp() - e.svc.sessao.Enviadas()
		return nil
	case "banco_erro_dado":
		// banco_erro_dado operacao=N : a gravação da linha da operação N falha com erro de dado
		for _, a := range p.Args {
			if v, ok := strings.CutPrefix(a, "operacao="); ok {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return err
				}
				e.falha.definir(n)
				return nil
			}
		}
		return fmt.Errorf("banco_erro_dado operacao=N")
	case "quarentena_resolver":
		// quarentena_resolver id=N : como a CLI (kalman-ordens quarentena resolver)
		for _, a := range p.Args {
			if v, ok := strings.CutPrefix(a, "id="); ok {
				if v == "todas" {
					qs, err := e.repo.QuarentenaPendente()
					if err != nil {
						return err
					}
					if len(qs) == 0 {
						return fmt.Errorf("nenhuma linha pendente na quarentena")
					}
					for _, q := range qs {
						if _, err := e.repo.ResolverQuarentena(q.ID, "cli:1450", "roteiro", e.rel.Agora()); err != nil {
							return err
						}
					}
					return nil
				}
				n, _ := strconv.ParseInt(v, 10, 64)
				ok, err := e.repo.ResolverQuarentena(n, "cli:1450", "roteiro", e.rel.Agora())
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("quarentena %d não existe ou já resolvida", n)
				}
				return nil
			}
		}
		return fmt.Errorf("quarentena_resolver id=N")
	case "reinicia_servico":
		e.descer()
		_ = e.pnt.EsperarQueda(5 * time.Second)
		r, err := e.backend.Reabrir(e.falha.Repositorio)
		if err != nil {
			return err
		}
		e.falha = &repoComFalha{Repositorio: r} // a falha injetada não sobrevive ao restart
		e.repo = e.falha
		return e.subir()
	case "recebe":
		return e.recebe(p.Campos)
	case "em_qualquer_ordem":
		return e.emQualquerOrdem(p.Filhos)
	case "nada":
		e.ocioso()
		for _, r := range e.pnt.Pendentes() {
			if len(p.Campos) == 0 || e.casa(p.Campos, r, false) == nil {
				return fmt.Errorf("chegou mensagem inesperada: %s", r.Bruta)
			}
		}
		return nil
	case "recebe_sessao":
		fim := time.Now().Add(esperaMensagem)
		for time.Now().Before(fim) {
			ss := e.pnt.Sessao()
			for i := e.sessaoOK; i < len(ss); i++ {
				if e.casa(p.Campos, ss[i], true) == nil {
					e.sessaoOK = i + 1
					return nil
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		return fmt.Errorf("nenhuma mensagem de sessão casou com %v", campos(p.Campos))
	case "envia", "envia_offline":
		cs, err := e.resolver(p.Campos)
		if err != nil {
			return err
		}
		antes := e.svc.sessao.Entregues()
		if p.Verbo == "envia" {
			err = e.pnt.Enviar(cs)
		} else {
			err = e.pnt.EnviarOffline(cs)
		}
		if err != nil {
			return err
		}
		if e.travado != nil && p.Verbo == "envia" {
			// motor_lento: espera a mensagem chegar à sessão (o motor não a trata até motor_normal)
			fim := time.Now().Add(esperaMensagem)
			for e.svc.sessao.Entregues() <= antes && time.Now().Before(fim) {
				time.Sleep(time.Millisecond)
			}
		}
		e.ocioso()
		return nil
	case "entrada", "saida":
		return e.pedido(p)
	case "stop":
		return e.ajusteStop(p)
	case "sinal":
		return e.sinal(p)
	case "usuario", "habilita", "desabilita":
		if err := e.usuario(p); err != nil {
			return err
		}
		// o roteiro mudou o banco por fora (como a CLI): o cache do motor relê (v1.10 B)
		if e.svc != nil && e.travado == nil {
			e.svc.motor.InvalidarUsuarios()
		}
		e.ocioso()
		return nil
	case "corrompe":
		delta := 0
		if len(p.Args) > 1 {
			delta, _ = strconv.Atoi(p.Args[1])
		}
		return e.pnt.Corromper(p.Args[0], delta)
	case "motor_lento":
		e.travado = make(chan struct{})
		trava := e.travado
		e.svc.motor.Enfileirar(func() { <-trava })
		return nil
	case "motor_normal":
		if e.travado != nil {
			close(e.travado)
			e.travado = nil
		}
		e.ocioso()
		return nil
	case "http":
		corpo := ""
		if len(p.Args) > 2 {
			corpo = p.Args[2]
		}
		return e.http(p.Args[0], p.Args[1], corpo)
	case "http_async":
		corpo := ""
		if len(p.Args) > 2 {
			corpo = p.Args[2]
		}
		return e.httpAsync(p.Args[0], p.Args[1], corpo)
	case "http_aguarda":
		return e.httpAguarda()
	case "metrica":
		return e.metrica(p.Args)
	case "resposta":
		return conferir(e.resposta, p.Args)
	case "saude":
		if err := e.http("GET", "/saude", ""); err != nil {
			return err
		}
		return conferir(e.resposta, p.Args)
	case "evento", "sem_evento", "conta_eventos":
		return e.eventos(p)
	case "alerta", "sem_alerta":
		return e.alerta(p)
	}
	if strings.HasPrefix(p.Verbo, "flx_") {
		return e.flexscan(p)
	}
	return fmt.Errorf("verbo sem execução: %s", p.Verbo)
}

// ---------------------------------------------------------------- preço

func (e *Executor) definirPreco(args []string) error {
	if len(args) == 1 && args[0] == "vazio" {
		e.preco.Vazio()
		return nil
	}
	if len(args) == 1 && args[0] == "normal" {
		e.preco.Normal()
		return nil
	}
	for _, a := range args {
		k, v, _ := strings.Cut(a, "=")
		if err := e.preco.Definir(k, v); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- mensagens

func (e *Executor) resolver(cs []roteiro.Campo) ([]falso.Campo, error) {
	var r []falso.Campo
	for _, c := range cs {
		v, err := e.substituir(c.Valor)
		if err != nil {
			return nil, err
		}
		r = append(r, falso.Campo{Tag: c.Tag, Valor: v})
	}
	return r, nil
}

func (e *Executor) substituir(v string) (string, error) {
	var err error
	r := roteiro.RefRe.ReplaceAllStringFunc(v, func(m string) string {
		x, ok := e.capturas[m[1:]]
		if !ok {
			err = fmt.Errorf("captura %s não definida", m)
		}
		return x
	})
	return r, err
}

func (e *Executor) recebe(cs []roteiro.Campo) error {
	r, err := e.pnt.Proxima(esperaMensagem)
	if err != nil {
		return fmt.Errorf("nenhuma mensagem chegou em %s (esperava %v)", esperaMensagem, campos(cs))
	}
	if r.Validacao != nil {
		return fmt.Errorf("mensagem do serviço reprovada no dicionário: %v\n  %s", r.Validacao, r.Bruta)
	}
	if err := e.casa(cs, r, true); err != nil {
		return fmt.Errorf("%w\n  recebida: %s", err, r.Bruta)
	}
	return nil
}

func (e *Executor) emQualquerOrdem(filhos []roteiro.Passo) error {
	faltam := append([]roteiro.Passo(nil), filhos...)
	fim := time.Now().Add(esperaMensagem)
	for len(faltam) > 0 && time.Now().Before(fim) {
		pend := e.pnt.Pendentes()
		achou := false
		for i, r := range pend {
			for j, f := range faltam {
				if e.casa(f.Campos, r, false) == nil {
					if r.Validacao != nil {
						return fmt.Errorf("mensagem do serviço reprovada no dicionário: %v\n  %s", r.Validacao, r.Bruta)
					}
					_ = e.casa(f.Campos, r, true)
					e.pnt.Consumir(i)
					faltam = append(faltam[:j], faltam[j+1:]...)
					achou = true
					break
				}
			}
			if achou {
				break
			}
		}
		if !achou {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(faltam) > 0 {
		var pend []string
		for _, r := range e.pnt.Pendentes() {
			pend = append(pend, r.Bruta)
		}
		return fmt.Errorf("não chegaram: %v; pendentes: %v", campos(faltam[0].Campos), pend)
	}
	return nil
}

// casa confere as tags listadas; com capturar, guarda as capturas.
func (e *Executor) casa(cs []roteiro.Campo, r falso.Recebida, capturar bool) error {
	novas := map[string]string{}
	for _, c := range cs {
		v, ok := r.Valor(c.Tag)
		if c.Captura != "" {
			if !ok {
				return fmt.Errorf("tag %d ausente (captura %s)", c.Tag, c.Captura)
			}
			novas[c.Captura] = v
			continue
		}
		if c.Valor == "-" {
			// tag=- : a tag não pode estar na mensagem (v1.13: 20864 só fora da entrada)
			if ok {
				return fmt.Errorf("tag %d = %s, esperava ausente", c.Tag, v)
			}
			continue
		}
		esperado, err := e.substituir(c.Valor)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("tag %d ausente (esperava %s)", c.Tag, esperado)
		}
		if !iguaisTexto(esperado, v) {
			return fmt.Errorf("tag %d = %s, esperava %s", c.Tag, v, esperado)
		}
	}
	if capturar {
		for k, v := range novas {
			e.capturas[k] = v
		}
	}
	return nil
}

// ---------------------------------------------------------------- HTTP

func (e *Executor) pedido(p roteiro.Passo) error {
	op, _ := strconv.ParseInt(p.Args[0], 10, 64)
	b := map[string]any{"operacao_id": op, "uid": UIDRoteiro, "lado": p.Args[1], "qtd": 1}
	extras := p.Args[2:]
	if p.Verbo == "entrada" {
		b["papel"], b["stop_pts"], b["motivo"] = "ENTRADA", 500, "SINAL"
		// reenvio da mesma operação repete o ts_decisao (o gestor reenvia o mesmo corpo)
		ts, ok := e.tsPorOp[p.Args[0]]
		if !ok {
			ts = e.rel.Agora().In(relogio.Fuso).Format("2006-01-02T15:04:05.000-07:00")
			e.tsPorOp[p.Args[0]] = ts
		}
		b["ts_decisao"] = ts
	} else {
		b["papel"], b["motivo"] = "SAIDA", p.Args[2]
		extras = p.Args[3:]
	}
	for _, a := range extras {
		k, v, _ := strings.Cut(a, "=")
		switch k {
		case "qtd":
			b["qtd"], _ = strconv.Atoi(v)
		case "stop":
			b["stop_pts"], _ = strconv.Atoi(v)
		case "motivo":
			b["motivo"] = v
		case "ts":
			b["ts_decisao"] = v
		case "uid":
			b["uid"], _ = strconv.Atoi(v)
		case "modulo":
			b["modulo"] = v
		}
	}
	j, _ := json.Marshal(b)
	return e.http("POST", "/ordens", string(j))
}

// sinal: sinal ID [parte=N] [partes=N] [modulo=M] [ts=T] e=OP:C|V[:UID] ... s=OP:C|V:MOTIVO[:UID]
// ... → POST /sinais (v1.10 §6). Entrada: qtd 1, stop_pts 500, motivo SINAL; uid 7 se ausente.
// ts_decisao = hora do relógio no primeiro envio do sinal e o mesmo nos reenvios.
func (e *Executor) sinal(p roteiro.Passo) error {
	id := p.Args[0]
	ts, ok := e.tsPorOp["sinal:"+id]
	if !ok {
		ts = e.rel.Agora().In(relogio.Fuso).Format("2006-01-02T15:04:05.000-07:00")
		e.tsPorOp["sinal:"+id] = ts
	}
	b := map[string]any{"sinal_id": id, "ts_decisao": ts, "parte": 1, "partes": 1}
	entradas, saidas := []any{}, []any{}
	for _, a := range p.Args[1:] {
		k, v, _ := strings.Cut(a, "=")
		switch k {
		case "parte", "partes":
			b[k], _ = strconv.Atoi(v)
		case "modulo":
			b["modulo"] = v
		case "ts":
			b["ts_decisao"] = v
		case "e", "s":
			c := strings.Split(v, ":")
			op, _ := strconv.ParseInt(c[0], 10, 64)
			it := map[string]any{"operacao_id": op, "uid": UIDRoteiro, "lado": c[1]}
			if k == "e" {
				it["qtd"], it["stop_pts"], it["motivo"] = 1, 500, "SINAL"
				if len(c) > 2 {
					it["uid"], _ = strconv.Atoi(c[2])
				}
				entradas = append(entradas, it)
			} else {
				it["motivo"] = c[2]
				if len(c) > 3 {
					it["uid"], _ = strconv.Atoi(c[3])
				}
				saidas = append(saidas, it)
			}
		}
	}
	b["entradas"], b["saidas"] = entradas, saidas
	j, _ := json.Marshal(b)
	return e.http("POST", "/sinais", string(j))
}

// ajusteStop: stop OP preco=N | pts=N [modulo=M] [uid=N] → POST /operacoes/OP/stop (v1.4 K1).
func (e *Executor) ajusteStop(p roteiro.Passo) error {
	b := map[string]any{}
	for _, a := range p.Args[1:] {
		k, v, _ := strings.Cut(a, "=")
		switch k {
		case "preco":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return err
			}
			b["preco_stop"] = f
		case "pts":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return err
			}
			b["stop_pts"] = n
		case "uid":
			b["uid"], _ = strconv.Atoi(v)
		case "modulo":
			b["modulo"] = v
		default:
			return fmt.Errorf("stop: argumento %q", a)
		}
	}
	j, _ := json.Marshal(b)
	return e.http("POST", "/operacoes/"+p.Args[0]+"/stop", string(j))
}

// soASCII (v1.4 L1): todo texto de recusa, alerta e evento que o serviço publica é ASCII.
func soASCII(onde, s string) error {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return fmt.Errorf("%s fora do ASCII: %q", onde, s)
		}
	}
	return nil
}

// conferirASCII: os eventos publicados (inclusive os ALERTA) e o que foi ao Telegram.
func (e *Executor) conferirASCII() error {
	ps, err := e.publicados()
	if err != nil {
		return err
	}
	for _, x := range ps {
		if err := soASCII("evento "+x.Tipo, string(x.Dados)); err != nil {
			return err
		}
	}
	e.notif.mu.Lock()
	defer e.notif.mu.Unlock()
	for _, t := range e.notif.n {
		if err := soASCII("Telegram", t); err != nil {
			return err
		}
	}
	return nil
}

// metrica: GET /metrics (texto do Prometheus); NOME [rotulo=valor ...] OP N, OP em = >= > <=.
// A série é a que tem exatamente esses rótulos (fora "le"). Os contadores somam em todos os
// roteiros do processo: os roteiros afirmam ">=".
func (e *Executor) metrica(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("metrica NOME [rotulo=valor...] OP N")
	}
	nome, op, alvoTxt := args[0], args[len(args)-2], args[len(args)-1]
	alvo, err := strconv.ParseFloat(alvoTxt, 64)
	if err != nil {
		return err
	}
	quer := map[string]string{}
	for _, a := range args[1 : len(args)-2] {
		k, v, _ := strings.Cut(a, "=")
		quer[k] = v
	}
	w := httptest.NewRecorder()
	e.svc.http.ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	texto := w.Body.String()
	for _, linha := range strings.Split(texto, "\n") {
		if strings.HasPrefix(linha, "#") || linha == "" {
			continue
		}
		i := strings.LastIndex(linha, " ") // o valor vem depois do último espaço (rótulo pode ter espaço)
		serie, valorTxt := linha[:i], linha[i+1:]
		n, rot, _ := strings.Cut(serie, "{")
		if n != nome {
			continue
		}
		tem := map[string]string{}
		for _, par := range strings.Split(strings.TrimSuffix(rot, "}"), "\",") {
			if k, v, ok := strings.Cut(par, "="); ok {
				tem[k] = strings.Trim(v, "\"")
			}
		}
		if len(tem) != len(quer) {
			continue
		}
		casa := true
		for k, v := range quer {
			if tem[k] != v {
				casa = false
			}
		}
		if !casa {
			continue
		}
		v, _ := strconv.ParseFloat(valorTxt, 64)
		ok := map[string]bool{"=": v == alvo, ">=": v >= alvo, ">": v > alvo, "<=": v <= alvo}[op]
		if !ok {
			return fmt.Errorf("%s%v = %v, esperava %s %v", nome, quer, v, op, alvo)
		}
		return nil
	}
	return fmt.Errorf("métrica %s%v ausente do /metrics", nome, quer)
}

// httpAsync: a requisição fica em curso (ex.: GET /pnt/ordens, que espera as respostas do PnT
// falso); http_aguarda pega a resposta.
func (e *Executor) httpAsync(metodo, caminho, corpo string) error {
	if e.async != nil {
		return fmt.Errorf("já há uma http_async em curso")
	}
	ch := make(chan *httptest.ResponseRecorder, 1)
	h := e.svc.http
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(metodo, caminho, bytes.NewBufferString(corpo)))
		ch <- w
	}()
	e.async, e.asyncDesc = ch, metodo+" "+caminho+" "+corpo
	return nil
}

func (e *Executor) httpAguarda() error {
	if e.async == nil {
		return fmt.Errorf("nenhuma http_async em curso")
	}
	defer func() { e.async = nil }()
	select {
	case w := <-e.async:
		return e.lerResposta(e.asyncDesc, w)
	case <-time.After(15 * time.Second):
		return fmt.Errorf("http_async sem resposta em 15 s: %s", e.asyncDesc)
	}
}

func (e *Executor) http(metodo, caminho, corpo string) error {
	req := httptest.NewRequest(metodo, caminho, bytes.NewBufferString(corpo))
	w := httptest.NewRecorder()
	e.svc.http.ServeHTTP(w, req)
	return e.lerResposta(metodo+" "+caminho+" "+corpo, w)
}

func (e *Executor) lerResposta(desc string, w *httptest.ResponseRecorder) error {
	dec := json.NewDecoder(w.Body)
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("resposta HTTP ilegível (%d): %v", w.Code, err)
	}
	if m, ok := v.(map[string]any); ok {
		e.resposta = m
	} else {
		e.resposta = map[string]any{"_lista": v}
	}
	e.resposta["_status"] = json.Number(strconv.Itoa(w.Code))
	if mot, ok := e.resposta["motivo"].(string); ok {
		if err := soASCII("motivo da resposta", mot); err != nil {
			return err
		}
	}
	e.Registro = append(e.Registro, fmt.Sprintf("%s → %d %v", desc, w.Code, e.resposta))
	e.ocioso()
	return nil
}

func (e *Executor) publicados() ([]repo.Publicado, error) {
	var todos []repo.Publicado
	var desde int64
	for {
		ps, err := e.repo.Publicados(desde, 1000, "")
		if err != nil {
			return nil, err
		}
		todos = append(todos, ps...)
		if len(ps) < 1000 {
			return todos, nil
		}
		desde = ps[len(ps)-1].Seq
	}
}

func dados(p repo.Publicado) map[string]any {
	dec := json.NewDecoder(bytes.NewReader(p.Dados))
	dec.UseNumber()
	var m map[string]any
	_ = dec.Decode(&m)
	return m
}

func (e *Executor) eventos(p roteiro.Passo) error {
	e.ocioso()
	tipo := p.Args[0]
	filtros := p.Args[1:]
	if p.Verbo == "conta_eventos" {
		n, _ := strconv.Atoi(filtros[len(filtros)-1])
		filtros = filtros[:len(filtros)-2]
		ps, err := e.publicados()
		if err != nil {
			return err
		}
		total := 0
		for _, x := range ps {
			if x.Tipo == tipo && conferir(dados(x), filtros) == nil {
				total++
			}
		}
		if total != n {
			return fmt.Errorf("%d evento(s) %s %v, esperava %d", total, tipo, filtros, n)
		}
		return nil
	}
	ps, err := e.publicados()
	if err != nil {
		return err
	}
	for _, x := range ps {
		if e.casados[x.Seq] || x.Tipo != tipo || conferir(dados(x), filtros) != nil {
			continue
		}
		if p.Verbo == "sem_evento" {
			return fmt.Errorf("evento inesperado: %s %s", x.Tipo, string(x.Dados))
		}
		e.casados[x.Seq] = true
		return nil
	}
	if p.Verbo == "sem_evento" {
		return nil
	}
	var mesmos []string
	for _, x := range ps {
		if x.Tipo == tipo {
			mesmos = append(mesmos, fmt.Sprintf("seq %d %s (casado=%v)", x.Seq, string(x.Dados), e.casados[x.Seq]))
		}
	}
	return fmt.Errorf("nenhum evento %s %v; eventos desse tipo: %v", tipo, filtros, mesmos)
}

func (e *Executor) alerta(p roteiro.Passo) error {
	e.ocioso()
	ps, err := e.publicados()
	if err != nil {
		return err
	}
	for _, x := range ps {
		if x.Tipo != "ALERTA" || e.casados[x.Seq] {
			continue
		}
		if fmt.Sprint(dados(x)["codigo"]) != p.Args[0] {
			continue
		}
		if p.Verbo == "sem_alerta" {
			return fmt.Errorf("alerta inesperado: %s", string(x.Dados))
		}
		e.casados[x.Seq] = true
		return nil
	}
	if p.Verbo == "sem_alerta" {
		return nil
	}
	var todos []string
	for _, x := range ps {
		if x.Tipo == "ALERTA" {
			todos = append(todos, fmt.Sprintf("%v (casado=%v)", dados(x)["codigo"], e.casados[x.Seq]))
		}
	}
	return fmt.Errorf("nenhum alerta %s; alertas: %v", p.Args[0], todos)
}

// ---------------------------------------------------------------- comparação

// conferir aplica campo=valor (com caminho a.b; numa lista, a.0.b e a.# = tamanho) sobre um objeto JSON.
func conferir(obj map[string]any, args []string) error {
	for _, a := range args {
		k, esperado, _ := strings.Cut(a, "=")
		var v any = obj
		for _, parte := range strings.Split(k, ".") {
			switch x := v.(type) {
			case map[string]any:
				v = x[parte]
			case []any:
				// lista: índice (lista.0.campo) ou tamanho (lista.#)
				if parte == "#" {
					v = json.Number(strconv.Itoa(len(x)))
					continue
				}
				i, err := strconv.Atoi(parte)
				if err != nil || i < 0 || i >= len(x) {
					v = nil
					continue
				}
				v = x[i]
			default:
				v = nil
			}
		}
		if !iguaisJSON(esperado, v) {
			return fmt.Errorf("%s = %v, esperava %s (objeto: %v)", k, v, esperado, obj)
		}
	}
	return nil
}

func iguaisJSON(esperado string, v any) bool {
	switch x := v.(type) {
	case nil:
		return esperado == "null"
	case json.Number:
		return iguaisTexto(esperado, x.String())
	case bool:
		return esperado == strconv.FormatBool(x)
	case string:
		return esperado == "*" || esperado == x || (esperado != "" && iguaisNumero(esperado, x))
	default:
		return esperado == "*"
	}
}

func iguaisTexto(esperado, obtido string) bool {
	if esperado == "*" {
		return true
	}
	return esperado == obtido || iguaisNumero(esperado, obtido)
}

func iguaisNumero(a, b string) bool {
	x, e1 := decimal.NewFromString(a)
	y, e2 := decimal.NewFromString(b)
	return e1 == nil && e2 == nil && x.Equal(y)
}

func campos(cs []roteiro.Campo) string {
	var b []string
	for _, c := range cs {
		if c.Captura != "" {
			b = append(b, fmt.Sprintf("%d=>%s", c.Tag, c.Captura))
		} else {
			b = append(b, fmt.Sprintf("%d=%s", c.Tag, c.Valor))
		}
	}
	return strings.Join(b, " ")
}

func esperarAte(limite time.Duration, cond func() bool) bool {
	fim := time.Now().Add(limite)
	for time.Now().Before(fim) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// corromperUltimaDoStore muda, no lugar, o 9 da última mensagem gravada no store FIX do serviço
// (o reenvio não consegue mais lê-la). Só com a sessão caída.
func corromperUltimaDoStore(dir string) error {
	idx, err := filepath.Glob(filepath.Join(dir, "*.header"))
	if err != nil || len(idx) != 1 {
		return fmt.Errorf("store do serviço: índice %v (%v)", idx, err)
	}
	b, err := os.ReadFile(idx[0])
	if err != nil {
		return err
	}
	linhas := strings.Split(strings.TrimSpace(string(b)), "\n")
	var seq, tam int
	var pos int64
	if _, err := fmt.Sscanf(linhas[len(linhas)-1], "%d,%d,%d", &seq, &pos, &tam); err != nil {
		return err
	}
	f, err := os.OpenFile(strings.TrimSuffix(idx[0], ".header")+".body", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	msg := make([]byte, tam)
	if _, err := f.ReadAt(msg, pos); err != nil {
		return err
	}
	i := bytes.Index(msg, []byte("\x019=")) + 3
	j := i + bytes.IndexByte(msg[i:], 1)
	v, err := strconv.Atoi(string(msg[i:j]))
	if err != nil {
		return err
	}
	novo := strconv.Itoa(v + 1)
	if len(novo) != j-i {
		novo = strconv.Itoa(v - 1)
	}
	_, err = f.WriteAt([]byte(novo), pos+int64(i))
	return err
}

// Dir cria um diretório de trabalho para o cenário.
func Dir(base, nome string) (string, error) {
	d := filepath.Join(base, strings.TrimSuffix(nome, ".fix"))
	return d, os.MkdirAll(d, 0o755)
}

// repoComFalha (v1.13): a gravação de uma linha de operação falha com erro de dado (como o 1406 de
// 19/10), nos dois backends, para os roteiros da quarentena do lote.
type repoComFalha struct {
	repo.Repositorio
	mu sync.Mutex
	op int64
}

func (r *repoComFalha) definir(op int64) { r.mu.Lock(); r.op = op; r.mu.Unlock() }

func (r *repoComFalha) GravarLote(l *repo.Lote) error {
	r.mu.Lock()
	op := r.op
	r.mu.Unlock()
	if op != 0 {
		for _, o := range append(append([]dominio.Operacao{}, l.NovasOperacoes...), l.Operacoes...) {
			if o.OperacaoID == op {
				return fmt.Errorf("kalmanordemoperacao: %w (injetado)", repo.ErrDadoInvalido)
			}
		}
	}
	return r.Repositorio.GravarLote(l)
}
