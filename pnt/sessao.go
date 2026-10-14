package pnt

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/config"
	"github.com/quickfixgo/quickfix/log/file"
	filestore "github.com/quickfixgo/quickfix/store/file"

	"gammaquant.com.br/kalman-ordens/pnt/dicionario"
)

// ConfigSessao configura a sessão initiator com o PnT DSA.
type ConfigSessao struct {
	SenderCompID string
	TargetCompID string // padrão PNT_DSA
	Host         string
	Porta        int
	Usuario      string // 553
	Senha        string // 554
	Dir          string // store/, log/ e dicionario/ ficam aqui
	Inicio       string // StartTime, HH:MM:SS
	Fim          string // EndTime, HH:MM:SS
	FusoHorario  string // padrão America/Sao_Paulo
	HeartBtInt   int    // padrão 30
	// ReconexaoS: espera da biblioteca entre tentativas (padrão 5) e primeira espera crescente.
	ReconexaoS int
	// ReconexaoMaxS: teto da espera crescente depois de logon recusado ou store ilegível (padrão
	// 60; SPEC v1.3 B1, D1).
	ReconexaoMaxS int
	// Esperas substitui a sequência da espera crescente (testes). Vazio: ReconexaoS dobrando até
	// ReconexaoMaxS.
	Esperas []time.Duration
	// SincroniaMs: espera depois do logon antes de liberar o envio (padrão 500; SPEC v1.3 A2).
	SincroniaMs int
	// [PnT-11]: se o PnT exigir reset a cada logon.
	ResetOnLogon bool
	// SemLogArquivo desliga o FileLog (testes).
	SemLogArquivo bool
}

// Ouvinte recebe os eventos da sessão. Os métodos são chamados nas goroutines do QuickFIX/Go:
// quem implementa só enfileira.
type Ouvinte interface {
	Logou()
	Caiu()
	// Recebeu: mensagem de aplicação (35=8, 9, j...), inclusive reenvios (PossDup).
	Recebeu(Mensagem)
	// RejeicaoRecebida: 35=3 enviado pelo PnT (rejeitou algo nosso).
	RejeicaoRecebida(Mensagem)
	// RejeicaoEnviada: 35=3 enviado pela nossa biblioteca (descartou uma mensagem do PnT).
	RejeicaoEnviada(Mensagem)
	// MensagemIlegivel: a biblioteca descartou em silêncio bytes recebidos que não conseguiu ler
	// (ex.: BodyLength errado). Ela só registra no log de eventos; aqui vira aviso. texto = o evento.
	MensagemIlegivel(texto string)
	// LogonRecusado: o PnT recusou o logon porque ainda segura a sessão anterior ("already logged
	// on", depois de uma queda abrupta). A camada tenta de novo com espera crescente. texto = o 58.
	LogonRecusado(texto string)
	// StoreIlegivel: a biblioteca não conseguiu ler do store uma mensagem pedida por
	// ResendRequest. A camada derruba a sessão, repara o store e reconecta com espera crescente.
	StoreIlegivel(texto string)
}

// ErrSemSessao: a sessão não está logada; a mensagem não foi enviada.
var ErrSemSessao = errors.New("pnt: sessão não logada")

// Sessao é a sessão initiator com o PnT.
//
// Um vigia (goroutine própria) reinicia o initiator com espera crescente quando o PnT recusa o
// logon por ainda segurar a sessão anterior, ou quando o reenvio não consegue ler o store (SPEC
// v1.3 B1, D1): sozinha, a biblioteca só sabe tentar de novo a cada ReconnectInterval, para
// sempre. Antes de cada subida o store é reparado (RepararStore).
type Sessao struct {
	cfg      ConfigSessao
	ouvinte  Ouvinte
	st       *quickfix.Settings
	logs     *fabricaLogEspia
	stores   *fabricaStore
	id       quickfix.SessionID
	logado   atomic.Bool
	envio    sync.Mutex
	ultimaSq atomic.Int64
	enviadas atomic.Int64
	recebida atomic.Int64

	mu        sync.Mutex // init
	init      *quickfix.Initiator
	pedido    chan string // reinício com espera, pedido pelo espião (goroutine da sessão)
	fim       chan struct{}
	pararUma  sync.Once
	vigia     sync.WaitGroup
	tentativa int          // só no vigia
	logonEm   atomic.Int64 // UnixNano do último OnLogon
	reinicios atomic.Int64

	transicao sync.Mutex // conectada, geracao, adiadas e a passagem de logado (OnLogon/OnLogout/sincronia)
	conectada bool
	geracao   int
	adiadas   []func() // mensagens recebidas durante a sincronia, entregues depois do Logou
	reenvios  int      // ResendRequests do PnT recebidos durante a sincronia
}

// NovaSessao prepara a sessão (não conecta).
func NovaSessao(cfg ConfigSessao, o Ouvinte) (*Sessao, error) {
	if cfg.TargetCompID == "" {
		cfg.TargetCompID = "PNT_DSA"
	}
	if cfg.FusoHorario == "" {
		cfg.FusoHorario = "America/Sao_Paulo"
	}
	if cfg.HeartBtInt == 0 {
		cfg.HeartBtInt = 30
	}
	if cfg.ReconexaoS == 0 {
		cfg.ReconexaoS = 5
	}
	if cfg.ReconexaoMaxS == 0 {
		cfg.ReconexaoMaxS = 60
	}
	if cfg.SincroniaMs == 0 {
		cfg.SincroniaMs = 500
	}
	if len(cfg.Esperas) == 0 {
		teto := time.Duration(cfg.ReconexaoMaxS) * time.Second
		for d := time.Duration(cfg.ReconexaoS) * time.Second; ; d *= 2 {
			if d >= teto {
				cfg.Esperas = append(cfg.Esperas, teto)
				break
			}
			cfg.Esperas = append(cfg.Esperas, d)
		}
	}
	if cfg.SenderCompID == "" || cfg.Host == "" || cfg.Porta == 0 || cfg.Dir == "" {
		return nil, errors.New("pnt: SenderCompID, Host, Porta e Dir são obrigatórios")
	}
	return &Sessao{cfg: cfg, ouvinte: o, pedido: make(chan string, 1), fim: make(chan struct{})}, nil
}

// Configuracao devolve as configurações do QuickFIX/Go (SPEC v1.1 F2, F8).
func (s *Sessao) Configuracao() (*quickfix.Settings, error) {
	dd, err := dicionario.Gravar(filepath.Join(s.cfg.Dir, "dicionario"))
	if err != nil {
		return nil, err
	}
	st := quickfix.NewSettings()
	g := st.GlobalSettings()
	g.Set(config.FileStorePath, filepath.Join(s.cfg.Dir, "store"))
	g.Set(config.FileLogPath, filepath.Join(s.cfg.Dir, "log"))
	ss := quickfix.NewSessionSettings()
	ss.Set(config.BeginString, quickfix.BeginStringFIX44)
	ss.Set(config.SenderCompID, s.cfg.SenderCompID)
	ss.Set(config.TargetCompID, s.cfg.TargetCompID)
	ss.Set(config.SocketConnectHost, s.cfg.Host)
	ss.Set(config.SocketConnectPort, strconv.Itoa(s.cfg.Porta))
	ss.Set(config.HeartBtInt, strconv.Itoa(s.cfg.HeartBtInt))
	ss.Set(config.ReconnectInterval, strconv.Itoa(s.cfg.ReconexaoS))
	ss.Set(config.StartTime, s.cfg.Inicio)
	ss.Set(config.EndTime, s.cfg.Fim)
	ss.Set(config.TimeZone, s.cfg.FusoHorario)
	ss.Set(config.DataDictionary, dd)
	ss.Set(config.RejectInvalidMessage, "N")
	ss.Set(config.AllowUnknownMessageFields, "Y")
	ss.Set(config.CheckUserDefinedFields, "N")
	ss.Set(config.ValidateFieldsOutOfOrder, "N")
	ss.Set(config.ValidateFieldsHaveValues, "N")
	ss.Set(config.CheckLatency, "N")
	ss.Set(config.ResetOnLogon, sn(s.cfg.ResetOnLogon))
	ss.Set(config.ResetOnLogout, "N")
	ss.Set(config.ResetOnDisconnect, "N")
	ss.Set(config.PersistMessages, "Y")
	id, err := st.AddSession(ss)
	if err != nil {
		return nil, err
	}
	s.id = id
	return st, nil
}

// Iniciar repara o store, conecta (e reconecta sozinho) e põe o vigia no ar.
func (s *Sessao) Iniciar() error {
	st, err := s.Configuracao()
	if err != nil {
		return err
	}
	var lf quickfix.LogFactory = quickfix.NewNullLogFactory()
	if !s.cfg.SemLogArquivo {
		if lf, err = file.NewLogFactory(st); err != nil {
			return err
		}
	}
	s.st = st
	s.logs = &fabricaLogEspia{base: lf, s: s}
	if err := os.MkdirAll(s.cfg.Dir, 0o700); err != nil {
		return err
	}
	if err := s.reparar(); err != nil {
		return err
	}
	s.stores = &fabricaStore{base: filestore.NewStoreFactory(st)}
	if err := s.subir(); err != nil {
		return err
	}
	s.vigia.Add(1)
	go s.vigiar()
	return ApertarPermissoes(s.cfg.Dir)
}

func (s *Sessao) subir() error {
	init, err := quickfix.NewInitiator(s, s.stores, s.st, s.logs)
	if err != nil {
		return err
	}
	if err := init.Start(); err != nil {
		return err
	}
	s.mu.Lock()
	s.init = init
	s.mu.Unlock()
	return nil
}

// reparar conserta o store antes de a sessão subir (RepararStore).
func (s *Sessao) reparar() error {
	r, err := RepararStore(filepath.Join(s.cfg.Dir, "store"), s.id)
	if err != nil {
		return fmt.Errorf("pnt: reparo do store: %w", err)
	}
	if r.Mudou() {
		slog.Warn("pnt: store reparado", "reparo", r.String())
	}
	return nil
}

// Parar faz logout e desconecta. Não cancela nada no PnT.
// Fecha os stores: o Initiator.Stop do QuickFIX/Go não fecha, e o arquivo ficaria aberto.
func (s *Sessao) Parar() {
	s.pararUma.Do(func() { close(s.fim) })
	s.vigia.Wait()
	s.descer()
}

func (s *Sessao) descer() {
	s.mu.Lock()
	init := s.init
	s.init = nil
	s.mu.Unlock()
	if init != nil {
		init.Stop()
	}
	if s.stores != nil {
		s.stores.fechar()
	}
}

// pedir um reinício com espera (não bloqueia: chamado na goroutine da sessão).
func (s *Sessao) pedir(motivo string) {
	select {
	case s.pedido <- motivo:
	default:
	}
}

// vigiar: a cada pedido, derruba o initiator, repara o store, espera (crescente) e sobe de novo.
// A espera volta ao início depois de uma sessão que ficou de pé por um minuto.
func (s *Sessao) vigiar() {
	defer s.vigia.Done()
	for {
		var motivo string
		select {
		case <-s.fim:
			return
		case motivo = <-s.pedido:
		}
		s.descer()
		select { // o initiator velho pode ter pedido de novo antes de parar
		case <-s.pedido:
		default:
		}
		if t := s.logonEm.Load(); t != 0 && time.Since(time.Unix(0, t)) >= time.Minute {
			s.tentativa = 0
		}
		espera := s.cfg.Esperas[min(s.tentativa, len(s.cfg.Esperas)-1)]
		s.tentativa++
		if err := s.reparar(); err != nil {
			slog.Error("pnt: reparo do store falhou", "erro", err)
		}
		slog.Warn("pnt: sessão reiniciada com espera", "motivo", motivo, "espera", espera, "tentativa", s.tentativa)
		select {
		case <-s.fim:
			return
		case <-time.After(espera):
		}
		if err := s.subir(); err != nil {
			slog.Error("pnt: sessão não subiu", "erro", err)
			s.pedir("falha ao subir: " + err.Error())
			continue
		}
		s.reinicios.Add(1)
		_ = ApertarPermissoes(s.cfg.Dir)
	}
}

// Reinicios conta as subidas feitas pelo vigia (testes).
func (s *Sessao) Reinicios() int64 { return s.reinicios.Load() }

// fabricaStore guarda os stores criados para poder fechá-los. O store grava a mensagem como foi
// ao fio, senha inclusive (SPEC v1.3 A1): o reenvio relê e interpreta cada uma, e uma mensagem
// alterada depois de montada não se lê. A proteção é a do diretório FIX (700/600, só do usuário
// do serviço), a mesma do service.env. Os logs, lidos por quem investiga, saem mascarados.
type fabricaStore struct {
	base    quickfix.MessageStoreFactory
	mu      sync.Mutex
	abertos []quickfix.MessageStore
}

func (f *fabricaStore) Create(id quickfix.SessionID) (quickfix.MessageStore, error) {
	st, err := f.base.Create(id)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.abertos = append(f.abertos, st)
	f.mu.Unlock()
	return st, nil
}

// atual: o store da sessão em uso (o último criado).
func (f *fabricaStore) atual() quickfix.MessageStore {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.abertos) == 0 {
		return nil
	}
	return f.abertos[len(f.abertos)-1]
}

func (f *fabricaStore) fechar() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, st := range f.abertos {
		_ = st.Close()
	}
	f.abertos = nil
}

// Logado diz se a sessão está logada (pelos callbacks OnLogon/OnLogout).
func (s *Sessao) Logado() bool { return s.logado.Load() }

// Enviadas e Recebidas contam mensagens de aplicação (para observação e testes).
func (s *Sessao) Enviadas() int64  { return s.enviadas.Load() }
func (s *Sessao) Recebidas() int64 { return s.recebida.Load() }

// Enviar manda uma mensagem de aplicação e devolve o MsgSeqNum usado.
//
// Com a sessão caída, devolve ErrSemSessao: o QuickFIX/Go aceitaria a mensagem sem erro, mas a
// descartaria (SPEC v1.1 F5). Mesmo com sucesso aqui, a garantia de que chegou é a resposta do PnT.
func (s *Sessao) Enviar(m *quickfix.Message) (int, error) {
	if !s.logado.Load() {
		return 0, ErrSemSessao
	}
	s.envio.Lock()
	defer s.envio.Unlock()
	s.ultimaSq.Store(0)
	if err := quickfix.SendToTarget(m, s.id); err != nil {
		return 0, fmt.Errorf("pnt: %w", err)
	}
	s.enviadas.Add(1)
	return int(s.ultimaSq.Load()), nil
}

// ------------------------------------------------------------ quickfix.Application

func (s *Sessao) OnCreate(quickfix.SessionID) {}

// OnLogon: a sessão só fica logada para quem usa (Logado, Logou, Enviar) depois de
// SincroniaMs. Logo depois do logon o PnT pede o reenvio do que perdeu (HML 14/10: ResendRequest
// desde o primeiro logon recusado); uma mensagem de aplicação nossa enviada antes de o gap fill
// sair chega ao PnT com a sequência adiantada, e o PnT tem de guardá-la até o buraco fechar — o
// QuickFIX/Go, por exemplo, perde essa mensagem (resend_state.go: o stash só sobrevive se já
// existia), e ela não volta por reenvio (ToApp suprime). Esperar deixa o ResendRequest chegar
// antes: o reenvio segura os envios (resendMutex) até o gap fill sair. SPEC v1.3 A2.
func (s *Sessao) OnLogon(quickfix.SessionID) {
	s.logonEm.Store(time.Now().UnixNano())
	s.transicao.Lock()
	s.conectada = true
	s.geracao++
	s.reenvios = 0
	g := s.geracao
	s.transicao.Unlock()
	time.AfterFunc(time.Duration(s.cfg.SincroniaMs)*time.Millisecond, func() {
		s.transicao.Lock()
		defer s.transicao.Unlock()
		if !s.conectada || s.geracao != g {
			return
		}
		s.logado.Store(true)
		s.ouvinte.Logou()
		s.registrarSincronia("envio liberado")
		s.entregarAdiadas()
	})
}

func (s *Sessao) OnLogout(quickfix.SessionID) {
	s.transicao.Lock()
	defer s.transicao.Unlock()
	s.conectada = false
	if !s.logado.Load() && s.geracao > 0 {
		s.registrarSincronia("caiu durante a sincronia")
	}
	s.entregarAdiadas() // caiu na sincronia: o que chegou (um fill, talvez) não se perde
	if s.logado.Swap(false) {
		s.ouvinte.Caiu()
	}
}

// entregar: durante a sincronia (logon feito, envio ainda não liberado) o que chega espera e sai
// logo depois do Logou, na ordem — para quem usa, a sessão logou e então as mensagens chegaram,
// como sem a sincronia (ex.: o fill reenviado depois de uma queda leva à 1014 na hora).
func (s *Sessao) entregar(f func()) {
	s.transicao.Lock()
	if s.conectada && !s.logado.Load() {
		s.adiadas = append(s.adiadas, f)
		s.transicao.Unlock()
		return
	}
	s.transicao.Unlock()
	f()
}

// registrarSincronia: no log do serviço e no log de eventos FIX, quantas mensagens chegaram na
// janela de sincronia e quantos ResendRequests do PnT — para conferir na HML se FIX_SINCRONIA_MS
// basta (um ResendRequest depois da janela = o mass status pode ter saído antes do gap fill).
// Com s.transicao travado.
func (s *Sessao) registrarSincronia(oque string) {
	texto := fmt.Sprintf("pnt: %s: %d mensagem(ns) recebida(s) e %d ResendRequest(s) do PnT nos %d ms de sincronia",
		oque, len(s.adiadas), s.reenvios, s.cfg.SincroniaMs)
	slog.Info(texto, "recebidas_na_janela", len(s.adiadas), "reenvios_na_janela", s.reenvios, "sincronia_ms", s.cfg.SincroniaMs)
	s.logs.registrar(s.id, texto)
}

// entregarAdiadas: com s.transicao travado.
func (s *Sessao) entregarAdiadas() {
	for _, f := range s.adiadas {
		f()
	}
	s.adiadas = nil
}

func (s *Sessao) ToAdmin(m *quickfix.Message, _ quickfix.SessionID) {
	tipo, _ := m.Header.GetString(quickfix.Tag(35))
	switch tipo {
	case "A":
		if s.cfg.Usuario != "" {
			m.Body.SetString(quickfix.Tag(553), s.cfg.Usuario)
		}
		if s.cfg.Senha != "" {
			m.Body.SetString(quickfix.Tag(554), s.cfg.Senha)
		}
	case "3":
		msg := Ler(m.String())
		s.entregar(func() { s.ouvinte.RejeicaoEnviada(msg) })
	}
}

// ToApp suprime o reenvio de mensagem de aplicação por ResendRequest: uma ordem velha chegando
// de novo ao PnT seria uma ordem nova. O QuickFIX/Go manda GapFill no lugar.
// Na chamada normal (dentro de Enviar), guarda o MsgSeqNum.
func (s *Sessao) ToApp(m *quickfix.Message, _ quickfix.SessionID) error {
	if pd, err := m.Header.GetString(quickfix.Tag(43)); err == nil && pd == "Y" {
		return quickfix.ErrDoNotSend
	}
	if sq, err := m.Header.GetInt(quickfix.Tag(34)); err == nil {
		s.ultimaSq.Store(int64(sq))
	}
	return nil
}

func (s *Sessao) FromAdmin(m *quickfix.Message, _ quickfix.SessionID) quickfix.MessageRejectError {
	if tipo, _ := m.Header.GetString(quickfix.Tag(35)); tipo == "3" {
		msg := Ler(m.String())
		s.entregar(func() { s.ouvinte.RejeicaoRecebida(msg) })
	}
	return nil
}

func (s *Sessao) FromApp(m *quickfix.Message, _ quickfix.SessionID) quickfix.MessageRejectError {
	s.recebida.Add(1)
	msg := Ler(m.String())
	s.entregar(func() { s.ouvinte.Recebeu(msg) })
	return nil
}

func sn(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

// O espião fica entre a biblioteca e o log da sessão (o único lugar em que ela conta o que fez
// com o que não entendeu) e mascara a senha (554/925) em tudo o que vai para o log:
//   - "Msg Parse Error": a biblioteca descartou em silêncio uma mensagem que não conseguiu ler
//     (BodyLength errado, por exemplo) → aviso ao ouvinte, nunca perda silenciosa (SPEC v1.1 F12);
//   - "error retrieving messages from store": o reenvio não conseguiu ler o store → aviso,
//     reinício com espera e reparo do store (v1.3 B1);
//   - logout do PnT "MsgSeqNum too low, expecting N" → a próxima sequência de envio passa a N
//     antes da próxima tentativa (v1.3 C1);
//   - logout do PnT "already logged on" → aviso e reinício com espera crescente (v1.3 D1).
//
// Os logs criados são guardados: o FileLog do QuickFIX/Go não tem Close, e o vigia recria o
// initiator a cada reinício.
type fabricaLogEspia struct {
	base    quickfix.LogFactory
	s       *Sessao
	mu      sync.Mutex
	global  quickfix.Log
	sessoes map[quickfix.SessionID]quickfix.Log
}

func (f *fabricaLogEspia) Create() (quickfix.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.global == nil {
		l, err := f.base.Create()
		if err != nil {
			return nil, err
		}
		f.global = l
	}
	return logEspia{base: f.global, s: f.s}, nil
}

func (f *fabricaLogEspia) CreateSessionLog(id quickfix.SessionID) (quickfix.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.sessoes[id]
	if !ok {
		var err error
		if l, err = f.base.CreateSessionLog(id); err != nil {
			return nil, err
		}
		if f.sessoes == nil {
			f.sessoes = map[quickfix.SessionID]quickfix.Log{}
		}
		f.sessoes[id] = l
	}
	return logEspia{base: l, s: f.s}, nil
}

type logEspia struct {
	base quickfix.Log
	s    *Sessao
}

// registrar escreve um evento da camada no log de eventos da sessão.
func (f *fabricaLogEspia) registrar(id quickfix.SessionID, texto string) {
	f.mu.Lock()
	l := f.sessoes[id]
	f.mu.Unlock()
	if l != nil {
		l.OnEvent(texto)
	}
}

func (l logEspia) OnIncoming(b []byte) {
	l.base.OnIncoming(Mascarar(b))
	if bytes.Contains(b, []byte("\x0135=2\x01")) {
		l.s.transicao.Lock()
		if l.s.conectada && !l.s.logado.Load() {
			l.s.reenvios++
		}
		l.s.transicao.Unlock()
	}
	if ev := l.s.logout(b); ev != "" {
		l.base.OnEvent(ev)
	}
}
func (l logEspia) OnOutgoing(b []byte) { l.base.OnOutgoing(Mascarar(b)) }
func (l logEspia) OnEvent(s string) {
	s = MascararTexto(s)
	l.base.OnEvent(s)
	l.s.evento(s)
}
func (l logEspia) OnEventf(formato string, v ...interface{}) {
	l.OnEvent(fmt.Sprintf(formato, v...))
}

func (s *Sessao) evento(e string) {
	switch {
	case strings.HasPrefix(e, "Msg Parse Error"):
		s.entregar(func() { s.ouvinte.MensagemIlegivel(e) })
	case strings.HasPrefix(e, "error retrieving messages from store"):
		s.ouvinte.StoreIlegivel(e)
		s.pedir("store ilegível no reenvio")
	}
}

var (
	reEsperado    = regexp.MustCompile(`(?i)MsgSeqNum too low, expecting (\d+)`)
	reSessaoPresa = regexp.MustCompile(`(?i)already logged`)
)

// logout trata o 35=5 recebido (na goroutine da sessão, antes de a biblioteca processá-lo) e
// devolve um evento para o log da sessão (vazio = nada a registrar).
func (s *Sessao) logout(b []byte) string {
	if !bytes.Contains(b, []byte("\x0135=5\x01")) {
		return ""
	}
	texto := Ler(string(b)).Texto(58)
	if g := reEsperado.FindStringSubmatch(texto); g != nil {
		n, _ := strconv.Atoi(g[1])
		if st := s.stores.atual(); st != nil && n > st.NextSenderMsgSeqNum() {
			de := st.NextSenderMsgSeqNum()
			if err := st.SetNextSenderMsgSeqNum(n); err != nil {
				return fmt.Sprintf("pnt: o PnT espera a sequência %d; o ajuste falhou: %v", n, err)
			}
			slog.Warn("pnt: o PnT espera outra sequência; ajustada", "de", de, "para", n, "texto", texto)
			return fmt.Sprintf("pnt: o PnT espera a sequência %d; próxima sequência de envio ajustada de %d para %d", n, de, n)
		}
	}
	if reSessaoPresa.MatchString(texto) {
		s.ouvinte.LogonRecusado(texto)
		s.pedir("logon recusado: " + texto)
		return "pnt: logon recusado pelo PnT (" + texto + "); nova tentativa com espera crescente"
	}
	return ""
}

// ApertarPermissoes deixa o diretório FIX (log e store) só para o usuário do serviço: diretórios
// 700, arquivos 600. O QuickFIX/Go cria o log com os.ModePerm e o store com 0660 (com o umask
// padrão, log legível por todos); a unidade systemd usa UMask=0077 e isto aperta o que já
// existia. O store guarda a senha (v1.3 A1) e o log revela o tamanho dela pelo 9 do logon.
func ApertarPermissoes(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		modo := fs.FileMode(0o600)
		if d.IsDir() {
			modo = 0o700
		}
		return os.Chmod(p, modo)
	})
}
