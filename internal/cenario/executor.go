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
	motor   *motor.Motor
	sessao  *pnt.Sessao
	http    http.Handler
	baseEnv int64 // falso.EnviadasApp() ao subir
	baseRec int64 // falso.RecebidasApp() ao subir
}

// Executor de um cenário.
type Executor struct {
	c        roteiro.Cenario
	dir      string
	rel      *relogio.Controlado
	preco    *preco.Falsa
	repo     repo.Repositorio
	backend  Backend
	notif    *notificador
	pnt      *falso.Falso
	svc      *servico
	cfg      config.Config
	capturas map[string]string
	resposta map[string]any
	casados  map[int64]bool
	sessaoOK int
	logons   int64
	travado  chan struct{} // motor_lento
	tsPorOp  map[string]string
	Registro []string // o que aconteceu, para depuração
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
			return r, r.Migrar()
		},
		Reabrir: func(velho repo.Repositorio) (repo.Repositorio, error) {
			velho.(*repo.MariaDB).Fechar()
			r, err := repo.AbrirMariaDB(dsn)
			if err != nil {
				return nil, err
			}
			return r, r.Migrar()
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
	return nil
}

func (e *Executor) preparar() error {
	e.rel = relogio.NovoControlado(e.c.Hora)
	e.preco = preco.NovaFalsa(e.rel)
	r, err := e.backend.Novo()
	if err != nil {
		return err
	}
	e.repo = r
	e.notif = &notificador{}
	f, err := falso.Novo(falso.Config{Contraparte: "KALMAN", Dir: filepath.Join(e.dir, "pnt")})
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
	e.cfg = config.Novo(v)
	return e.subir()
}

func (e *Executor) subir() error {
	m := motor.Novo(e.cfg, e.rel, e.repo, e.preco, e.notif)
	s, err := pnt.NovaSessao(pnt.ConfigSessao{
		SenderCompID: "KALMAN", Host: "127.0.0.1", Porta: e.pnt.Porta(), Dir: filepath.Join(e.dir, "fix"),
		Inicio: "00:00:00", Fim: "00:00:00", HeartBtInt: 5, ReconexaoS: 1, SemLogArquivo: true,
	}, m)
	if err != nil {
		return err
	}
	m.Usar(s)
	if err := m.Iniciar(); err != nil {
		return err
	}
	e.svc = &servico{motor: m, sessao: s, http: api.Handler(m, e.cfg.Segundos("HTTP_ESPERA_S", 5)), baseEnv: e.pnt.EnviadasApp() + e.pnt.Reentregas(), baseRec: e.pnt.RecebidasApp()}
	return s.Iniciar()
}

func (e *Executor) descer() {
	if e.svc == nil {
		return
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
	if m, ok := e.repo.(*repo.MariaDB); ok {
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
	for time.Now().Before(fim) {
		proc := e.svc.motor.Processadas()
		dentro := !e.pnt.Logado() || proc >= e.pnt.EnviadasApp()+e.pnt.Reentregas()-e.svc.baseEnv
		fora := e.pnt.RecebidasApp()-e.svc.baseRec >= e.svc.sessao.Enviadas()
		if dentro && fora {
			if e.svc.motor.Processadas() == proc {
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
			e.svc.motor.Tick()
			e.ocioso()
		}
		return nil
	case "aguarda_logon":
		if err := e.pnt.EsperarLogon(e.logons, 10*time.Second); err != nil {
			return err
		}
		e.logons = e.pnt.Logons()
		esperarAte(5*time.Second, e.svc.sessao.Logado)
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
	case "reinicia_servico":
		e.descer()
		_ = e.pnt.EsperarQueda(5 * time.Second)
		r, err := e.backend.Reabrir(e.repo)
		if err != nil {
			return err
		}
		e.repo = r
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
		if p.Verbo == "envia" {
			err = e.pnt.Enviar(cs)
		} else {
			err = e.pnt.EnviarOffline(cs)
		}
		if err != nil {
			return err
		}
		e.ocioso()
		return nil
	case "entrada", "saida":
		return e.pedido(p)
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
	uid := e.cfg.Inteiro("ORDENS_UID_REAL", 0)
	b := map[string]any{"operacao_id": op, "uid": uid, "lado": p.Args[1], "qtd": 1}
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
		}
	}
	j, _ := json.Marshal(b)
	return e.http("POST", "/ordens", string(j))
}

func (e *Executor) http(metodo, caminho, corpo string) error {
	req := httptest.NewRequest(metodo, caminho, bytes.NewBufferString(corpo))
	w := httptest.NewRecorder()
	e.svc.http.ServeHTTP(w, req)
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
	e.Registro = append(e.Registro, fmt.Sprintf("%s %s %s → %d %v", metodo, caminho, corpo, w.Code, e.resposta))
	e.ocioso()
	return nil
}

func (e *Executor) publicados() ([]repo.Publicado, error) {
	var todos []repo.Publicado
	var desde int64
	for {
		ps, err := e.repo.Publicados(desde, 1000)
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

// conferir aplica campo=valor (com caminho a.b) sobre um objeto JSON.
func conferir(obj map[string]any, args []string) error {
	for _, a := range args {
		k, esperado, _ := strings.Cut(a, "=")
		var v any = obj
		for _, parte := range strings.Split(k, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v = m[parte]
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
		return esperado == x || (esperado != "" && iguaisNumero(esperado, x))
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

// Dir cria um diretório de trabalho para o cenário.
func Dir(base, nome string) (string, error) {
	d := filepath.Join(base, strings.TrimSuffix(nome, ".fix"))
	return d, os.MkdirAll(d, 0o755)
}
