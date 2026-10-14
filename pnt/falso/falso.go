// Package falso é um PnT falso: acceptor FIX 4.4 local que faz o papel do PnT DSA numa sessão
// real do QuickFIX/Go. Faz parte da camada PnT e não sabe nada de quem está do outro lado: só
// recebe mensagens, valida contra o dicionário e envia o que mandarem.
//
// O que ele oferece a um executor de roteiros:
//   - fila das mensagens de aplicação recebidas (inclui 35=3), cada uma validada em modo estrito;
//   - fila das mensagens de sessão recebidas (0, 1, 2, 4, 5, A);
//   - envio com o corpo exatamente na ordem dada (grupos repetidos "sujos" incluídos);
//   - envio com a sessão caída (grava no store e vai por ResendRequest);
//   - queda de rede simulada (proxy TCP próprio na frente do acceptor).
package falso

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
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
	"github.com/quickfixgo/quickfix/datadictionary"
	"github.com/quickfixgo/quickfix/log/screen"

	"gammaquant.com.br/kalman-ordens/pnt/dicionario"
)

// Campo é um par tag=valor, na ordem em que aparece.
type Campo struct {
	Tag   int
	Valor string
}

// Recebida é uma mensagem que chegou ao PnT falso.
type Recebida struct {
	Tipo   string  // 35
	Campos []Campo // todas as tags, na ordem do fio (cabeçalho, corpo, trailer)
	Bruta  string  // com | no lugar de SOH
	// Validacao é o erro da validação estrita contra o dicionário (nil = passou).
	// Só para mensagens de aplicação e 35=3.
	Validacao error
}

// Valor devolve a primeira ocorrência da tag.
func (r Recebida) Valor(tag int) (string, bool) {
	for _, c := range r.Campos {
		if c.Tag == tag {
			return c.Valor, true
		}
	}
	return "", false
}

// Config do PnT falso.
type Config struct {
	// Porta em que o serviço conecta (a do proxy). 0 = escolhe uma livre.
	Porta int
	// CompID do PnT (SenderCompID do falso). Padrão: PNT_DSA.
	CompID string
	// CompID de quem conecta (TargetCompID do falso).
	Contraparte string
	// Diretório de trabalho (dicionário materializado). Padrão: temporário.
	Dir string
	// Log de tráfego FIX na tela (depuração).
	Verboso bool
}

// Falso é o PnT falso em execução.
type Falso struct {
	cfg       Config
	dd        *datadictionary.DataDictionary
	estrito   quickfix.Validator
	acceptor  *quickfix.Acceptor
	proxy     *proxy
	interna   int // porta do acceptor (o proxy fica na pública)
	sessao    quickfix.SessionID
	logado    atomic.Bool
	logons    atomic.Int64
	autoExec  atomic.Int64
	enviadas  atomic.Int64
	recebidas atomic.Int64
	mu        sync.Mutex
	aplicacao []Recebida
	admin     []Recebida
	novas     chan struct{}
	// recusa de logon (RecusarLogons): texto do 58; vazio = aceita
	recusa    string
	recusasEm []time.Time
	store     *storeFalso
}

// Tipos de mensagem de sessão (não entram na fila de aplicação).
var tiposSessao = map[string]bool{"0": true, "1": true, "2": true, "4": true, "5": true, "A": true}

// Novo prepara o PnT falso (não abre portas).
func Novo(cfg Config) (*Falso, error) {
	if cfg.CompID == "" {
		cfg.CompID = "PNT_DSA"
	}
	if cfg.Contraparte == "" {
		return nil, errors.New("falso: Contraparte obrigatória")
	}
	if cfg.Dir == "" {
		d, err := os.MkdirTemp("", "pnt-falso")
		if err != nil {
			return nil, err
		}
		cfg.Dir = d
	}
	dd, err := dicionario.Carregar()
	if err != nil {
		return nil, err
	}
	f := &Falso{
		cfg: cfg,
		dd:  dd,
		estrito: quickfix.NewValidator(quickfix.ValidatorSettings{
			CheckFieldsOutOfOrder:     true,
			RejectInvalidMessage:      true,
			AllowUnknownMessageFields: false,
			CheckUserDefinedFields:    false,
			CheckFieldsHaveValues:     true,
		}, dd, nil),
		novas: make(chan struct{}, 1),
	}
	return f, nil
}

// Iniciar abre o acceptor numa porta interna e o proxy na porta pública.
func (f *Falso) Iniciar() error {
	caminho, err := dicionario.Gravar(filepath.Join(f.cfg.Dir, "dicionario"))
	if err != nil {
		return err
	}
	interna, err := portaLivre()
	if err != nil {
		return err
	}
	s := quickfix.NewSettings()
	// Só em loopback. O QuickFIX/Go lê o SocketAcceptHost apenas nas configurações globais: na
	// sessão ele é ignorado e o acceptor escuta em todas as interfaces (aviso do Firewall do Windows).
	s.GlobalSettings().Set(config.SocketAcceptHost, "127.0.0.1")
	f.interna = interna
	ss := quickfix.NewSessionSettings()
	ss.Set(config.BeginString, quickfix.BeginStringFIX44)
	ss.Set(config.SenderCompID, f.cfg.CompID)
	ss.Set(config.TargetCompID, f.cfg.Contraparte)
	ss.Set(config.SocketAcceptPort, strconv.Itoa(interna))
	ss.Set(config.StartTime, "00:00:00")
	ss.Set(config.EndTime, "00:00:00")
	ss.Set(config.DataDictionary, caminho)
	// A sessão do falso aceita tudo; a validação que conta é a estrita, feita em FromApp.
	ss.Set(config.RejectInvalidMessage, "N")
	ss.Set(config.AllowUnknownMessageFields, "Y")
	ss.Set(config.CheckUserDefinedFields, "N")
	ss.Set(config.ValidateFieldsOutOfOrder, "N")
	ss.Set(config.CheckLatency, "N")
	ss.Set(config.ResetOnLogon, "N")
	ss.Set(config.ResetOnLogout, "N")
	ss.Set(config.ResetOnDisconnect, "N")
	id, err := s.AddSession(ss)
	if err != nil {
		return err
	}
	f.sessao = id

	var lf quickfix.LogFactory = quickfix.NewNullLogFactory()
	if f.cfg.Verboso {
		lf = screen.NewLogFactory()
	}
	f.acceptor, err = quickfix.NewAcceptor(f, fabricaStoreFalso{f}, s, lf)
	if err != nil {
		return err
	}
	if err := f.acceptor.Start(); err != nil {
		return err
	}
	f.proxy, err = novoProxy(f.cfg.Porta, fmt.Sprintf("127.0.0.1:%d", interna))
	if err != nil {
		f.acceptor.Stop()
		return err
	}
	f.cfg.Porta = f.proxy.porta()
	return nil
}

// Parar encerra proxy e acceptor.
func (f *Falso) Parar() {
	if f.proxy != nil {
		f.proxy.fechar()
	}
	if f.acceptor != nil {
		f.acceptor.Stop()
	}
}

// Porta em que o serviço deve conectar.
func (f *Falso) Porta() int { return f.cfg.Porta }

// PortasEscuta: todas as portas em que o falso escuta (proxy e acceptor interno).
func (f *Falso) PortasEscuta() []int { return []int{f.proxy.porta(), f.interna} }

// Logado diz se a sessão está logada do lado do PnT.
func (f *Falso) Logado() bool { return f.logado.Load() }

// Logons conta os logons desde o início (para esperar uma reconexão).
func (f *Falso) Logons() int64 { return f.logons.Load() }

// EsperarLogon espera até haver mais de `antes` logons e a sessão estar logada.
func (f *Falso) EsperarLogon(antes int64, limite time.Duration) error {
	fim := time.Now().Add(limite)
	for time.Now().Before(fim) {
		if f.logons.Load() > antes && f.logado.Load() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("falso: sem logon em %s", limite)
}

// EsperarQueda espera a sessão do lado do PnT registrar a desconexão.
func (f *Falso) EsperarQueda(limite time.Duration) error {
	fim := time.Now().Add(limite)
	for time.Now().Before(fim) {
		if !f.logado.Load() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("falso: a sessão não caiu em %s", limite)
}

// Corromper faz a próxima mensagem de aplicação do falso sair com o CheckSum errado ("checksum")
// ou com o BodyLength somado de delta ("bodylength"; o CheckSum é recalculado sobre os bytes novos).
func (f *Falso) Corromper(tipo string, delta int) error {
	if tipo != "checksum" && tipo != "bodylength" {
		return fmt.Errorf("falso: corrupção desconhecida %q", tipo)
	}
	f.proxy.mu.Lock()
	f.proxy.corrupcao = &corrupcao{tipo: tipo, delta: delta}
	f.proxy.mu.Unlock()
	return nil
}

// RecusarLogons faz o falso recusar todo logon novo com um 35=5 58=texto, sem contar a sequência
// de quem tentou — como o PnT da HML em 14/10, que segurava a sessão depois de uma queda abrupta
// ("already logged on") e depois pediu por ResendRequest os números desses logons. Texto vazio
// volta a aceitar.
func (f *Falso) RecusarLogons(texto string) {
	f.mu.Lock()
	f.recusa = texto
	f.mu.Unlock()
}

// Recusas conta os logons recusados desde o início; RecusasEm dá os instantes.
func (f *Falso) Recusas() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.recusasEm)
}

func (f *Falso) RecusasEm() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.recusasEm...)
}

// AjustarSequenciaEsperada muda o próximo MsgSeqNum que o falso espera do cliente, com a sessão
// caída: delta > 0 = o falso espera mais à frente (o logon seguinte é "MsgSeqNum too low");
// delta < 0 = o falso "esquece" as últimas mensagens recebidas e pede o reenvio delas no logon.
func (f *Falso) AjustarSequenciaEsperada(delta int) error {
	if f.Logado() {
		return errors.New("falso: AjustarSequenciaEsperada com a sessão logada")
	}
	f.mu.Lock()
	st := f.store
	f.mu.Unlock()
	if st == nil {
		return errors.New("falso: sessão sem store")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	n := st.base.NextTargetMsgSeqNum() + delta
	if n < 1 {
		return fmt.Errorf("falso: sequência esperada ficaria %d", n)
	}
	return st.base.SetNextTargetMsgSeqNum(n)
}

// Derrubar corta a conexão TCP (queda de rede). O serviço reconecta sozinho.
func (f *Falso) Derrubar() error {
	f.proxy.derrubar()
	return f.EsperarQueda(5 * time.Second)
}

// Enviar manda uma mensagem com a sessão logada. A tag 35 vai no cabeçalho; o resto vai no corpo
// exatamente na ordem dada. 60 e, em 35=8, 17 são preenchidos se ausentes.
func (f *Falso) Enviar(campos []Campo) error {
	if !f.Logado() {
		return errors.New("falso: Enviar com a sessão caída (use EnviarOffline)")
	}
	return f.enviar(campos)
}

// EnviarOffline grava a mensagem no store sem transmitir (sessão caída): ela chega ao serviço
// por ResendRequest, com PossDupFlag=Y.
func (f *Falso) EnviarOffline(campos []Campo) error {
	if f.Logado() {
		return errors.New("falso: EnviarOffline com a sessão logada")
	}
	return f.enviar(campos)
}

func (f *Falso) enviar(campos []Campo) error {
	m, err := f.montar(campos)
	if err != nil {
		return err
	}
	if err := quickfix.SendToTarget(m, f.sessao); err != nil {
		return err
	}
	f.enviadas.Add(1)
	return nil
}

// Reentregas conta as mensagens que o serviço descartou por BodyLength corrompido e recebeu de
// novo por ResendRequest: o serviço processa cada uma duas vezes (o aviso e o reenvio).
func (f *Falso) Reentregas() int64 { return f.proxy.reentregas.Load() }

// EnviadasApp conta as mensagens de aplicação enviadas pelo falso (inclusive offline).
func (f *Falso) EnviadasApp() int64 { return f.enviadas.Load() }

// RecebidasApp conta as mensagens de aplicação (e 35=3) recebidas pelo falso desde o início.
func (f *Falso) RecebidasApp() int64 { return f.recebidas.Load() }

// Proxima tira a próxima mensagem de aplicação da fila, esperando até `limite`.
func (f *Falso) Proxima(limite time.Duration) (Recebida, error) {
	fim := time.Now().Add(limite)
	for {
		f.mu.Lock()
		if len(f.aplicacao) > 0 {
			r := f.aplicacao[0]
			f.aplicacao = f.aplicacao[1:]
			f.mu.Unlock()
			return r, nil
		}
		f.mu.Unlock()
		resta := time.Until(fim)
		if resta <= 0 {
			return Recebida{}, ErrNada
		}
		select {
		case <-f.novas:
		case <-time.After(resta):
		}
	}
}

// ErrNada: nenhuma mensagem chegou no prazo.
var ErrNada = errors.New("falso: nenhuma mensagem")

// Pendentes devolve (sem consumir) as mensagens de aplicação ainda na fila.
func (f *Falso) Pendentes() []Recebida {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Recebida(nil), f.aplicacao...)
}

// Consumir tira da fila a mensagem de aplicação na posição i (para casamentos fora de ordem).
func (f *Falso) Consumir(i int) (Recebida, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.aplicacao) {
		return Recebida{}, false
	}
	r := f.aplicacao[i]
	f.aplicacao = append(f.aplicacao[:i], f.aplicacao[i+1:]...)
	return r, true
}

// Sessao devolve (sem consumir) as mensagens de sessão recebidas desde o início.
func (f *Falso) Sessao() []Recebida {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Recebida(nil), f.admin...)
}

// ---------------------------------------------------------------- montagem

func (f *Falso) montar(campos []Campo) (*quickfix.Message, error) {
	m := quickfix.NewMessage()
	var corpo []Campo
	tipo := ""
	tem17, tem60 := false, false
	for _, c := range campos {
		switch c.Tag {
		case 35:
			tipo = c.Valor
		case 8, 9, 10, 34, 49, 52, 56:
			return nil, fmt.Errorf("falso: tag %d é da sessão, não do roteiro", c.Tag)
		default:
			if c.Tag == 17 {
				tem17 = true
			}
			if c.Tag == 60 {
				tem60 = true
			}
			corpo = append(corpo, c)
		}
	}
	if tipo == "" {
		return nil, errors.New("falso: mensagem sem 35")
	}
	m.Header.SetString(quickfix.Tag(35), tipo)
	if tipo == "8" && !tem17 {
		corpo = append(corpo, Campo{17, fmt.Sprintf("AUTO-%d", f.autoExec.Add(1))})
	}
	if !tem60 && tipo != "j" && tipo != "3" {
		corpo = append(corpo, Campo{60, time.Now().UTC().Format("20060102-15:04:05.000")})
	}
	if len(corpo) > 0 {
		m.Body.SetGroup(corpoOrdenado(corpo))
	}
	return m, nil
}

// blocoOrdenado é um "grupo" falso que carrega o corpo inteiro: o QuickFIX/Go escreve os
// TagValue dele na ordem dada, em vez de ordenar por número de tag.
type blocoOrdenado struct {
	tag quickfix.Tag
	tvs []quickfix.TagValue
}

func (b blocoOrdenado) Tag() quickfix.Tag          { return b.tag }
func (b blocoOrdenado) Write() []quickfix.TagValue { return b.tvs }

func corpoOrdenado(campos []Campo) blocoOrdenado {
	b := blocoOrdenado{tag: quickfix.Tag(campos[0].Tag)}
	for _, c := range campos {
		b.tvs = append(b.tvs, tagValue(quickfix.Tag(c.Tag), c.Valor))
	}
	return b
}

// tagValue fabrica um TagValue (o construtor do QuickFIX/Go não é exportado) passando por um
// RepeatingGroup de um elemento: Write() devolve [contador, tag=valor].
func tagValue(tag quickfix.Tag, valor string) quickfix.TagValue {
	g := quickfix.NewRepeatingGroup(quickfix.Tag(1), quickfix.GroupTemplate{quickfix.GroupElement(tag)})
	g.Add().SetString(tag, valor)
	return g.Write()[1]
}

// ---------------------------------------------------------------- Application

func (f *Falso) OnCreate(quickfix.SessionID) {}

func (f *Falso) OnLogon(quickfix.SessionID) {
	f.logado.Store(true)
	f.logons.Add(1)
}

func (f *Falso) OnLogout(quickfix.SessionID) { f.logado.Store(false) }

func (f *Falso) ToAdmin(*quickfix.Message, quickfix.SessionID) {}

func (f *Falso) ToApp(*quickfix.Message, quickfix.SessionID) error { return nil }

func (f *Falso) FromAdmin(m *quickfix.Message, _ quickfix.SessionID) quickfix.MessageRejectError {
	r := recebida(m)
	f.mu.Lock()
	if r.Tipo == "A" && f.recusa != "" {
		texto := f.recusa
		f.admin = append(f.admin, r)
		f.recusasEm = append(f.recusasEm, time.Now())
		f.store.congelarAlvo.Store(true) // a biblioteca contaria o logon recusado
		f.mu.Unlock()
		return quickfix.RejectLogon{Text: texto}
	}
	if tiposSessao[r.Tipo] {
		f.admin = append(f.admin, r)
	} else {
		// 35=3 enviado pelo serviço: entra na fila de aplicação, como no roteiro
		f.aplicacao = append(f.aplicacao, r)
		f.recebidas.Add(1)
	}
	f.mu.Unlock()
	f.avisar()
	return nil
}

func (f *Falso) FromApp(m *quickfix.Message, _ quickfix.SessionID) quickfix.MessageRejectError {
	r := recebida(m)
	if err := f.estrito.Validate(m); err != nil {
		r.Validacao = err
	}
	f.mu.Lock()
	f.aplicacao = append(f.aplicacao, r)
	f.recebidas.Add(1)
	f.mu.Unlock()
	f.avisar()
	return nil
}

func (f *Falso) avisar() {
	select {
	case f.novas <- struct{}{}:
	default:
	}
}

func recebida(m *quickfix.Message) Recebida {
	bruta := m.String()
	r := Recebida{Bruta: strings.ReplaceAll(bruta, "\x01", "|")}
	for _, p := range strings.Split(strings.TrimSuffix(bruta, "\x01"), "\x01") {
		i := strings.IndexByte(p, '=')
		if i <= 0 {
			continue
		}
		tag, err := strconv.Atoi(p[:i])
		if err != nil {
			continue
		}
		r.Campos = append(r.Campos, Campo{tag, p[i+1:]})
		if tag == 35 {
			r.Tipo = p[i+1:]
		}
	}
	return r
}

// ---------------------------------------------------------------- store

// fabricaStoreFalso: store em memória com trava (o executor ajusta a sequência esperada de fora
// da goroutine da sessão) e sem contar o logon recusado.
type fabricaStoreFalso struct{ f *Falso }

func (x fabricaStoreFalso) Create(id quickfix.SessionID) (quickfix.MessageStore, error) {
	base, err := quickfix.NewMemoryStoreFactory().Create(id)
	if err != nil {
		return nil, err
	}
	st := &storeFalso{base: base}
	x.f.mu.Lock()
	x.f.store = st
	x.f.mu.Unlock()
	return st, nil
}

type storeFalso struct {
	mu           sync.Mutex
	base         quickfix.MessageStore
	congelarAlvo atomic.Bool // o próximo IncrNextTargetMsgSeqNum não conta (logon recusado)
}

func (s *storeFalso) NextSenderMsgSeqNum() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.NextSenderMsgSeqNum()
}

func (s *storeFalso) NextTargetMsgSeqNum() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.NextTargetMsgSeqNum()
}

func (s *storeFalso) IncrNextSenderMsgSeqNum() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.IncrNextSenderMsgSeqNum()
}

func (s *storeFalso) IncrNextTargetMsgSeqNum() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.congelarAlvo.Swap(false) {
		return nil
	}
	return s.base.IncrNextTargetMsgSeqNum()
}

func (s *storeFalso) SetNextSenderMsgSeqNum(n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.SetNextSenderMsgSeqNum(n)
}

func (s *storeFalso) SetNextTargetMsgSeqNum(n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.SetNextTargetMsgSeqNum(n)
}

func (s *storeFalso) CreationTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.CreationTime()
}

func (s *storeFalso) SetCreationTime(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.base.SetCreationTime(t)
}

func (s *storeFalso) SaveMessage(seq int, msg []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.SaveMessage(seq, msg)
}

func (s *storeFalso) SaveMessageAndIncrNextSenderMsgSeqNum(seq int, msg []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.SaveMessageAndIncrNextSenderMsgSeqNum(seq, msg)
}

func (s *storeFalso) GetMessages(de, ate int) ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.GetMessages(de, ate)
}

func (s *storeFalso) IterateMessages(de, ate int, cb func([]byte) error) error {
	s.mu.Lock()
	msgs, err := s.base.GetMessages(de, ate)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if err := cb(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *storeFalso) Refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.Refresh()
}

func (s *storeFalso) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.Reset()
}

func (s *storeFalso) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base.Close()
}

// ---------------------------------------------------------------- proxy de queda

type proxy struct {
	ln      net.Listener
	destino string
	mu      sync.Mutex
	pares   [][2]net.Conn
	// corrupção pendente para a próxima mensagem de aplicação PnT → contraparte (testes de
	// BodyLength/CheckSum errados no fio)
	corrupcao *corrupcao
	// descartadas: MsgSeqNum das mensagens com BodyLength corrompido (o serviço as descarta e as
	// recebe de novo por ResendRequest); reentregas conta esses reenvios ao passarem pelo proxy.
	descartadas map[string]bool
	reentregas  atomic.Int64
}

type corrupcao struct {
	tipo  string // "checksum" | "bodylength"
	delta int
}

func novoProxy(porta int, destino string) (*proxy, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", porta))
	if err != nil {
		return nil, err
	}
	p := &proxy{ln: ln, destino: destino}
	go p.aceitar()
	return p, nil
}

func (p *proxy) porta() int { return p.ln.Addr().(*net.TCPAddr).Port }

func (p *proxy) aceitar() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		d, err := net.Dial("tcp", p.destino)
		if err != nil {
			c.Close()
			continue
		}
		p.mu.Lock()
		p.pares = append(p.pares, [2]net.Conn{c, d})
		p.mu.Unlock()
		go copiar(c, d)
		go p.copiarEnquadrando(d, c)
	}
}

func copiar(de, para net.Conn) {
	_, _ = io.Copy(para, de)
	de.Close()
	para.Close()
}

// copiarEnquadrando repassa mensagem a mensagem (de 8=FIX até 10=xxx<SOH>), para poder corromper a
// próxima mensagem de aplicação quando pedido.
func (p *proxy) copiarEnquadrando(de, para net.Conn) {
	defer de.Close()
	defer para.Close()
	var buf []byte
	tmp := make([]byte, 64*1024)
	for {
		n, err := de.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				msg, resto, ok := proximaMensagem(buf)
				if !ok {
					break
				}
				buf = resto
				if _, err := para.Write(p.talvezCorromper(msg)); err != nil {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

var fimMensagem = []byte("10=")

func proximaMensagem(buf []byte) (msg, resto []byte, ok bool) {
	i := bytes.Index(buf, fimMensagem)
	if i < 0 {
		return nil, buf, false
	}
	j := bytes.IndexByte(buf[i+1:], 0x01)
	if j < 0 {
		return nil, buf, false
	}
	fim := i + 1 + j + 1
	return append([]byte(nil), buf[:fim]...), buf[fim:], true
}

func (p *proxy) talvezCorromper(msg []byte) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.corrupcao
	tipo := ""
	if m := regexp35.FindSubmatch(msg); m != nil {
		tipo = string(m[1])
	}
	seq := ""
	if m := regexp34.FindSubmatch(msg); m != nil {
		seq = string(m[1])
	}
	if p.descartadas[seq] && regexp43.Match(msg) {
		delete(p.descartadas, seq)
		p.reentregas.Add(1)
	}
	if c == nil || tiposSessao[tipo] {
		return msg
	}
	p.corrupcao = nil
	if c.tipo == "bodylength" {
		if p.descartadas == nil {
			p.descartadas = map[string]bool{}
		}
		p.descartadas[seq] = true
	}
	return corromper(msg, *c)
}

var (
	regexp35 = regexp.MustCompile("35=([^]*)")
	regexp9  = regexp.MustCompile("^8=[^]*9=([0-9]+)")
	regexp10 = regexp.MustCompile("10=([0-9]{3})$")
	regexp34 = regexp.MustCompile("34=([0-9]+)")
	regexp43 = regexp.MustCompile("43=Y")
)

// corromper altera o 9 (e recalcula o 10 sobre os bytes novos) ou só o 10.
func corromper(msg []byte, c corrupcao) []byte {
	switch c.tipo {
	case "checksum":
		m := regexp10.FindSubmatchIndex(msg)
		v, _ := strconv.Atoi(string(msg[m[2]:m[3]]))
		return append(append(append([]byte(nil), msg[:m[2]]...), []byte(fmt.Sprintf("%03d", (v+1)%256))...), msg[m[3]:]...)
	case "bodylength":
		m := regexp9.FindSubmatchIndex(msg)
		v, _ := strconv.Atoi(string(msg[m[2]:m[3]]))
		out := append(append(append([]byte(nil), msg[:m[2]]...), []byte(strconv.Itoa(v+c.delta))...), msg[m[3]:]...)
		k := regexp10.FindSubmatchIndex(out)
		soma := 0
		for _, b := range out[:k[0]+1] {
			soma += int(b)
		}
		return append(append([]byte(nil), out[:k[2]]...), []byte(fmt.Sprintf("%03d", soma%256))...)
	}
	return msg
}

func (p *proxy) derrubar() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, par := range p.pares {
		par[0].Close()
		par[1].Close()
	}
	p.pares = nil
}

func (p *proxy) fechar() {
	p.ln.Close()
	p.derrubar()
}

func portaLivre() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// Juntar formata campos como tag=valor|... (para mensagens de erro).
func Juntar(cs []Campo) string {
	var b bytes.Buffer
	for i, c := range cs {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%d=%s", c.Tag, c.Valor)
	}
	return b.String()
}
