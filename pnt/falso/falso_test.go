package falso

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/config"

	"gammaquant.com.br/kalman-ordens/pnt/dicionario"
	"gammaquant.com.br/kalman-ordens/pnt/loopback"
)

// cliente é um initiator mínimo no papel do serviço (só para testar o falso).
type cliente struct {
	mu     sync.Mutex
	app    []string // mensagens de aplicação recebidas, com |
	logado bool
	sessao quickfix.SessionID
	init   *quickfix.Initiator
}

func (c *cliente) OnCreate(id quickfix.SessionID)                    { c.sessao = id }
func (c *cliente) OnLogon(quickfix.SessionID)                        { c.mu.Lock(); c.logado = true; c.mu.Unlock() }
func (c *cliente) OnLogout(quickfix.SessionID)                       { c.mu.Lock(); c.logado = false; c.mu.Unlock() }
func (c *cliente) ToAdmin(*quickfix.Message, quickfix.SessionID)     {}
func (c *cliente) ToApp(*quickfix.Message, quickfix.SessionID) error { return nil }
func (c *cliente) FromAdmin(*quickfix.Message, quickfix.SessionID) quickfix.MessageRejectError {
	return nil
}
func (c *cliente) FromApp(m *quickfix.Message, _ quickfix.SessionID) quickfix.MessageRejectError {
	c.mu.Lock()
	c.app = append(c.app, strings.ReplaceAll(m.String(), "\x01", "|"))
	c.mu.Unlock()
	return nil
}

func (c *cliente) recebidas() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.app...)
}

func novoCliente(t *testing.T, porta int) *cliente {
	t.Helper()
	caminho, err := dicionario.Gravar(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := quickfix.NewSettings()
	ss := quickfix.NewSessionSettings()
	ss.Set(config.BeginString, quickfix.BeginStringFIX44)
	ss.Set(config.SenderCompID, "CLIENTE")
	ss.Set(config.TargetCompID, "PNT_DSA")
	ss.Set(config.SocketConnectHost, "127.0.0.1")
	ss.Set(config.SocketConnectPort, strconv.Itoa(porta))
	ss.Set(config.HeartBtInt, "5")
	ss.Set(config.ReconnectInterval, "1")
	ss.Set(config.StartTime, "00:00:00")
	ss.Set(config.EndTime, "00:00:00")
	ss.Set(config.DataDictionary, caminho)
	ss.Set(config.RejectInvalidMessage, "N")
	ss.Set(config.AllowUnknownMessageFields, "Y")
	ss.Set(config.CheckUserDefinedFields, "N")
	ss.Set(config.ValidateFieldsOutOfOrder, "N")
	if _, err := s.AddSession(ss); err != nil {
		t.Fatal(err)
	}
	c := &cliente{}
	c.init, err = quickfix.NewInitiator(c, quickfix.NewMemoryStoreFactory(), s, quickfix.NewNullLogFactory())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.init.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.init.Stop)
	return c
}

func (c *cliente) enviar(t *testing.T, tipo string, campos ...[2]string) {
	t.Helper()
	m := quickfix.NewMessage()
	m.Header.SetString(quickfix.Tag(35), tipo)
	for _, kv := range campos {
		tag, _ := strconv.Atoi(kv[0])
		m.Body.SetString(quickfix.Tag(tag), kv[1])
	}
	if err := quickfix.SendToTarget(m, c.sessao); err != nil {
		t.Fatal(err)
	}
}

func subir(t *testing.T) (*Falso, *cliente) {
	t.Helper()
	f, err := Novo(Config{Contraparte: "CLIENTE", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Iniciar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Parar)
	c := novoCliente(t, f.Porta())
	if err := f.EsperarLogon(0, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	// como em produção (pnt.Sessao.Enviar): só envia depois do próprio OnLogon. Antes dele, o
	// handleLogon do QuickFIX/Go ainda lê a sequência do store sem trava (o -race acusa).
	esperar(t, "logon do cliente", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.logado })
	return f, c
}

func esperar(t *testing.T, oque string, cond func() bool) {
	t.Helper()
	fim := time.Now().Add(5 * time.Second)
	for time.Now().Before(fim) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tempo esgotado: %s", oque)
}

// NewOrderSingle 1031 no formato do serviço passa na validação estrita; um cancel com tags que a
// OrderCancelRequest do dicionário não declara (como nos exemplos do RoE 2025) não passa.
func TestValidacaoEstrita(t *testing.T) {
	f, c := subir(t)
	agora := time.Now().UTC().Format("20060102-15:04:05.000")
	c.enviar(t, "D", [2]string{"1", "12345"}, [2]string{"11", "KM-E-1-1"}, [2]string{"21", "4"},
		[2]string{"38", "1"}, [2]string{"40", "2"}, [2]string{"44", "130020"}, [2]string{"54", "1"},
		[2]string{"55", "WINZ26"}, [2]string{"59", "0"}, [2]string{"60", agora}, [2]string{"207", "BVMF"},
		[2]string{"847", "1031"})
	r, err := f.Proxima(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tipo != "D" || r.Validacao != nil {
		t.Fatalf("D deveria passar: tipo=%s validação=%v", r.Tipo, r.Validacao)
	}
	if v, _ := r.Valor(11); v != "KM-E-1-1" {
		t.Fatalf("11=%s", v)
	}

	c.enviar(t, "F", [2]string{"1", "12345"}, [2]string{"11", "KM-E-1-2"}, [2]string{"41", "KM-E-1-1"},
		[2]string{"37", "dsa.1"}, [2]string{"54", "1"}, [2]string{"55", "WINZ26"}, [2]string{"60", agora},
		[2]string{"847", "1031"})
	r, err = f.Proxima(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tipo != "F" || r.Validacao == nil {
		t.Fatalf("F com 847 deveria falhar na validação estrita: %v", r.Validacao)
	}
}

// O corpo sai na ordem do roteiro: o grupo 555 "sujo" do mass status 2028 (RoE 2025) chega intacto,
// e 17/60 são preenchidos quando ausentes.
func TestEnvioPreservaOrdem(t *testing.T) {
	f, c := subir(t)
	bruta := "35=8|1=12345|6=0|11=GQ-PT-P-WINFUT:E:9045770_5afaaf2c|14=0|37=dsa.23512|39=1|40=1|54=1|55=WINQ25|150=I|151=180|584=MS1|847=2028|911=4|5600=2184012308|555=2|600=WINQ25|616=XBMF|624=2|20787=0|20800=0|687=180|654=0|670=1|671=10101012|600=WINQ25|616=XBMF|624=1|20787=0|20800=180|687=180|654=1|670=1|671=10101012"
	var campos []Campo
	for _, p := range strings.Split(bruta, "|") {
		i := strings.IndexByte(p, '=')
		tag, _ := strconv.Atoi(p[:i])
		campos = append(campos, Campo{tag, p[i+1:]})
	}
	if err := f.Enviar(campos); err != nil {
		t.Fatal(err)
	}
	esperar(t, "cliente receber", func() bool { return len(c.recebidas()) == 1 })
	got := c.recebidas()[0]
	// o corpo esperado aparece inteiro e em ordem (depois do cabeçalho)
	semTipo := strings.TrimPrefix(bruta, "35=8|")
	if !strings.Contains(got, semTipo) {
		t.Fatalf("ordem do corpo não preservada:\nenviado: %s\nrecebido: %s", semTipo, got)
	}
	if !strings.Contains(got, "|17=AUTO-") || !strings.Contains(got, "|60=") {
		t.Fatalf("17/60 automáticos ausentes: %s", got)
	}
}

// Queda pelo proxy → mensagem enviada com a sessão caída → o cliente reconecta, pede ResendRequest
// e recebe a mensagem com PossDupFlag=Y.
func TestQuedaEResend(t *testing.T) {
	f, c := subir(t)
	antes := f.Logons()
	if err := f.Derrubar(); err != nil {
		t.Fatal(err)
	}
	if err := f.Enviar([]Campo{{35, "8"}}); err == nil {
		t.Fatal("Enviar com a sessão caída deveria falhar")
	}
	fill := []Campo{{35, "8"}, {1, "12345"}, {6, "130010"}, {11, "KM-E-9-1"}, {14, "1"}, {17, "T-9-1"},
		{31, "130010"}, {32, "1"}, {37, "dsa.9"}, {39, "2"}, {54, "1"}, {55, "WINZ26"}, {150, "F"}, {151, "0"}}
	if err := f.EnviarOffline(fill); err != nil {
		t.Fatal(err)
	}
	if err := f.EsperarLogon(antes, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	esperar(t, "fill reenviado", func() bool {
		for _, m := range c.recebidas() {
			if strings.Contains(m, "|17=T-9-1|") {
				return true
			}
		}
		return false
	})
	for _, m := range c.recebidas() {
		if strings.Contains(m, "|17=T-9-1|") && !strings.Contains(m, "|43=Y|") {
			t.Fatalf("reenvio sem PossDupFlag=Y: %s", m)
		}
	}
	esperar(t, "ResendRequest registrado", func() bool {
		for _, s := range f.Sessao() {
			if s.Tipo == "2" {
				return true
			}
		}
		return false
	})
}

// Acceptor interno e proxy só em 127.0.0.1 (sem aviso do Firewall do Windows).
func TestSoLoopback(t *testing.T) {
	f, err := Novo(Config{CompID: "PNT_DSA", Contraparte: "KALMAN"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Iniciar(); err != nil {
		t.Fatal(err)
	}
	defer f.Parar()
	loopback.Exigir(t, f.PortasEscuta()...)
}
