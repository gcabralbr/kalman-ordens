package pnt

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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
	ReconexaoS   int    // padrão 5
	// [PnT-11]: se o PnT exigir reset a cada logon.
	ResetOnLogon bool
	// SemLogArquivo desliga o FileLog (testes). O FileLog do QuickFIX/Go não tem Close: o arquivo
	// fica aberto até o processo terminar, o que só importa quando a sessão é recriada no mesmo processo.
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
}

// ErrSemSessao: a sessão não está logada; a mensagem não foi enviada.
var ErrSemSessao = errors.New("pnt: sessão não logada")

// Sessao é a sessão initiator com o PnT.
type Sessao struct {
	cfg      ConfigSessao
	ouvinte  Ouvinte
	init     *quickfix.Initiator
	stores   *fabricaStore
	id       quickfix.SessionID
	logado   atomic.Bool
	envio    sync.Mutex
	ultimaSq atomic.Int64
	enviadas atomic.Int64
	recebida atomic.Int64
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
	if cfg.SenderCompID == "" || cfg.Host == "" || cfg.Porta == 0 || cfg.Dir == "" {
		return nil, errors.New("pnt: SenderCompID, Host, Porta e Dir são obrigatórios")
	}
	return &Sessao{cfg: cfg, ouvinte: o}, nil
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

// Iniciar conecta (e reconecta sozinho).
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
	lf = fabricaLogEspia{base: lf, aviso: s.ouvinte.MensagemIlegivel}
	s.stores = &fabricaStore{base: filestore.NewStoreFactory(st)}
	s.init, err = quickfix.NewInitiator(s, s.stores, st, lf)
	if err != nil {
		return err
	}
	return s.init.Start()
}

// Parar faz logout e desconecta. Não cancela nada no PnT.
// Fecha os stores: o Initiator.Stop do QuickFIX/Go não fecha, e o arquivo ficaria aberto.
func (s *Sessao) Parar() {
	if s.init != nil {
		s.init.Stop()
	}
	if s.stores != nil {
		s.stores.fechar()
	}
}

// fabricaStore guarda os stores criados para poder fechá-los.
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
	return storeMascarado{st}, nil
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

func (s *Sessao) OnLogon(quickfix.SessionID) {
	s.logado.Store(true)
	s.ouvinte.Logou()
}

func (s *Sessao) OnLogout(quickfix.SessionID) {
	if s.logado.Swap(false) {
		s.ouvinte.Caiu()
	}
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
		s.ouvinte.RejeicaoEnviada(Ler(m.String()))
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
		s.ouvinte.RejeicaoRecebida(Ler(m.String()))
	}
	return nil
}

func (s *Sessao) FromApp(m *quickfix.Message, _ quickfix.SessionID) quickfix.MessageRejectError {
	s.recebida.Add(1)
	s.ouvinte.Recebeu(Ler(m.String()))
	return nil
}

func sn(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

// O QuickFIX/Go descarta em silêncio a mensagem que não consegue ler (BodyLength errado, por
// exemplo): só escreve "Msg Parse Error" no log de eventos da sessão. O espião repassa esse evento
// ao ouvinte, para nunca haver perda silenciosa (SPEC v1.1 F12). Também mascara a senha (554/925)
// em tudo o que vai para o log (mascara.go).
type fabricaLogEspia struct {
	base  quickfix.LogFactory
	aviso func(string)
}

func (f fabricaLogEspia) Create() (quickfix.Log, error) {
	l, err := f.base.Create()
	return logEspia{base: l, aviso: f.aviso}, err
}

func (f fabricaLogEspia) CreateSessionLog(id quickfix.SessionID) (quickfix.Log, error) {
	l, err := f.base.CreateSessionLog(id)
	return logEspia{base: l, aviso: f.aviso}, err
}

type logEspia struct {
	base  quickfix.Log
	aviso func(string)
}

func (l logEspia) OnIncoming(b []byte) { l.base.OnIncoming(Mascarar(b)) }
func (l logEspia) OnOutgoing(b []byte) { l.base.OnOutgoing(Mascarar(b)) }
func (l logEspia) OnEvent(s string) {
	s = MascararTexto(s)
	l.base.OnEvent(s)
	l.verificar(s)
}
func (l logEspia) OnEventf(formato string, v ...interface{}) {
	s := MascararTexto(fmt.Sprintf(formato, v...))
	l.base.OnEvent(s)
	l.verificar(s)
}

func (l logEspia) verificar(s string) {
	if strings.HasPrefix(s, "Msg Parse Error") {
		l.aviso(s)
	}
}
