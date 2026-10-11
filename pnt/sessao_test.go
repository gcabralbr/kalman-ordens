package pnt

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/pnt/falso"
)

type ouvinteTeste struct {
	mu        sync.Mutex
	logons    int
	quedas    int
	recebidas []Mensagem
	enviadas3 []Mensagem
	ilegiveis []string
}

func (o *ouvinteTeste) Logou() { o.mu.Lock(); o.logons++; o.mu.Unlock() }
func (o *ouvinteTeste) Caiu()  { o.mu.Lock(); o.quedas++; o.mu.Unlock() }
func (o *ouvinteTeste) Recebeu(m Mensagem) {
	o.mu.Lock()
	o.recebidas = append(o.recebidas, m)
	o.mu.Unlock()
}
func (o *ouvinteTeste) RejeicaoRecebida(Mensagem) {}
func (o *ouvinteTeste) MensagemIlegivel(s string) {
	o.mu.Lock()
	o.ilegiveis = append(o.ilegiveis, s)
	o.mu.Unlock()
}
func (o *ouvinteTeste) RejeicaoEnviada(m Mensagem) {
	o.mu.Lock()
	o.enviadas3 = append(o.enviadas3, m)
	o.mu.Unlock()
}

func (o *ouvinteTeste) copia() ([]Mensagem, []Mensagem) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Mensagem(nil), o.recebidas...), append([]Mensagem(nil), o.enviadas3...)
}

func esperar(t *testing.T, oque string, cond func() bool) {
	t.Helper()
	fim := time.Now().Add(10 * time.Second)
	for time.Now().Before(fim) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tempo esgotado: %s", oque)
}

func subir(t *testing.T) (*falso.Falso, *Sessao, *ouvinteTeste) {
	t.Helper()
	f, err := falso.Novo(falso.Config{Contraparte: "CLIENTE", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Iniciar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Parar)
	o := &ouvinteTeste{}
	s, err := NovaSessao(ConfigSessao{SenderCompID: "CLIENTE", Host: "127.0.0.1", Porta: f.Porta(), Dir: t.TempDir(),
		Inicio: "00:00:00", Fim: "00:00:00", HeartBtInt: 5, ReconexaoS: 1, SemLogArquivo: true}, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Iniciar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Parar)
	esperar(t, "logon", s.Logado)
	return f, s, o
}

func c(tag int, v string) falso.Campo { return falso.Campo{Tag: tag, Valor: v} }

var id = Identificacao{Conta: "12345", Bolsa: "BVMF"}

func TestOrdensPassamNoDicionario(t *testing.T) {
	f, s, _ := subir(t)
	agora := time.Now()
	msgs := map[string]func() (int, error){
		"1031": func() (int, error) {
			return s.Enviar(NovaOrdem(id, Ordem{ClOrdID: "X-E-1-1", Simbolo: "WINZ26", Lado: Compra, Qtd: 1, Preco: 130020, Estrategia: EstrategiaAgendada}, agora))
		},
		"1014": func() (int, error) {
			return s.Enviar(NovaOrdem(id, Ordem{ClOrdID: "X-P-1-1", Simbolo: "WINZ26", Lado: Venda, Qtd: 1, Estrategia: EstrategiaStop, Stop: 129510, StopLim: 129410}, agora))
		},
		"replace": func() (int, error) {
			return s.Enviar(Replace(id, Ordem{ClOrdID: "X-P-1-2", Simbolo: "WINZ26", Lado: Venda, Qtd: 2, Estrategia: EstrategiaStop, Stop: 129515, StopLim: 129415}, "X-P-1-1", "dsa.2", agora))
		},
		"cancel": func() (int, error) {
			return s.Enviar(Cancelamento(id, "X-P-1-3", "X-P-1-2", "dsa.2", "WINZ26", Venda, 2, agora))
		},
		"mass status": func() (int, error) { return s.Enviar(MassStatus("12345", "MS-1")) },
	}
	for _, nome := range []string{"1031", "1014", "replace", "cancel", "mass status"} {
		sq, err := msgs[nome]()
		if err != nil || sq == 0 {
			t.Fatalf("%s: seq=%d err=%v", nome, sq, err)
		}
		r, err := f.Proxima(5 * time.Second)
		if err != nil {
			t.Fatalf("%s: %v", nome, err)
		}
		if r.Validacao != nil {
			t.Errorf("%s não passou no dicionário: %v\n%s", nome, r.Validacao, r.Bruta)
		}
	}
}

func TestRecebeFillE35_3(t *testing.T) {
	f, _, o := subir(t)
	if err := f.Enviar([]falso.Campo{c(35, "8"), c(11, "X-E-1-1"), c(37, "dsa.1"), c(150, "F"), c(39, "2"), c(14, "1"), c(32, "1"), c(31, "130010")}); err != nil {
		t.Fatal(err)
	}
	esperar(t, "fill incompleto entregue", func() bool { r, _ := o.copia(); return len(r) == 1 })
	r, _ := o.copia()
	if q, ok := r[0].Inteiro(14); !ok || q != 1 || r[0].Texto(11) != "X-E-1-1" {
		t.Fatalf("fill: %s", r[0].Bruta)
	}
	if err := f.Enviar([]falso.Campo{c(35, "U9"), c(58, "x")}); err != nil {
		t.Fatal(err)
	}
	esperar(t, "35=3 enviado registrado", func() bool { _, e := o.copia(); return len(e) == 1 })
	_, e := o.copia()
	if e[0].Texto(373) != "11" {
		t.Fatalf("35=3: %s", e[0].Bruta)
	}
}

func TestEnviarComSessaoCaida(t *testing.T) {
	f, s, o := subir(t)
	if err := f.Derrubar(); err != nil {
		t.Fatal(err)
	}
	esperar(t, "sessão cair", func() bool { return !s.Logado() })
	if _, err := s.Enviar(MassStatus("12345", "MS-2")); err != ErrSemSessao {
		t.Fatalf("esperava ErrSemSessao, veio %v", err)
	}
	esperar(t, "religar", s.Logado)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.quedas != 1 || o.logons != 2 {
		t.Fatalf("quedas=%d logons=%d", o.quedas, o.logons)
	}
}

// BodyLength errado: a biblioteca descarta em silêncio; o espião avisa na hora, e o reenvio
// (ResendRequest, quando a sequência revela o buraco) entrega a mensagem uma vez.
func TestBodyLengthErradoAvisado(t *testing.T) {
	f, _, o := subir(t)
	if err := f.Corromper("bodylength", -3); err != nil {
		t.Fatal(err)
	}
	if err := f.Enviar([]falso.Campo{c(35, "8"), c(11, "X-1"), c(37, "d.1"), c(150, "F"), c(39, "2"), c(14, "1"), c(32, "1"), c(31, "1"), c(17, "E1")}); err != nil {
		t.Fatal(err)
	}
	esperar(t, "aviso de mensagem ilegível", func() bool { o.mu.Lock(); defer o.mu.Unlock(); return len(o.ilegiveis) == 1 })
	if r, _ := o.copia(); len(r) != 0 {
		t.Fatalf("a mensagem corrompida não devia chegar à aplicação: %v", r)
	}
	// a mensagem seguinte revela o buraco: ResendRequest e reenvio com 43=Y
	_ = f.Enviar([]falso.Campo{c(35, "8"), c(11, "X-2"), c(37, "d.2"), c(150, "0"), c(39, "0"), c(14, "0"), c(17, "E2")})
	esperar(t, "reenvio", func() bool { r, _ := o.copia(); return len(r) == 2 })
	r, _ := o.copia()
	if r[0].Texto(11) != "X-1" || !r[0].PossDup() {
		t.Fatalf("reenvio: %s", r[0].Bruta)
	}
}

// A senha vai no logon (no fio, inteira) e não aparece em nenhum arquivo do diretório FIX do
// serviço: log de mensagens, log de eventos e store. O log antigo do PT gravava 553/554 em texto puro.
func TestSenhaForaDosArquivos(t *testing.T) {
	const senha = "Segredo-HML-7f3a"
	f, err := falso.Novo(falso.Config{Contraparte: "CLIENTE", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Iniciar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Parar)
	dir, _ := os.MkdirTemp("", "pnt-senha") // o FileLog do QuickFIX/Go não fecha: não apagar no Windows
	o := &ouvinteTeste{}
	s, err := NovaSessao(ConfigSessao{SenderCompID: "CLIENTE", Host: "127.0.0.1", Porta: f.Porta(), Dir: dir,
		Inicio: "00:00:00", Fim: "00:00:00", HeartBtInt: 5, ReconexaoS: 1, Usuario: "USUARIO_HML", Senha: senha}, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Iniciar(); err != nil {
		t.Fatal(err)
	}
	esperar(t, "logon", s.Logado)
	// uma ordem e um evento de sessão a mais, para o log e o store terem conteúdo além do logon
	if _, err := s.Enviar(NovaOrdem(id, Ordem{ClOrdID: "KM-E-1-1", Simbolo: "WINZ26", Lado: Compra, Qtd: 1, Preco: 130000, Estrategia: EstrategiaAgendada}, time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Proxima(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	s.Parar()

	noFio := false
	for _, m := range f.Sessao() {
		if m.Tipo == "A" && strings.Contains(m.Bruta, "|554="+senha+"|") {
			noFio = true
		}
	}
	if !noFio {
		t.Fatal("o logon não levou a senha no fio")
	}
	arquivos, comLogon := 0, 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		arquivos++
		if strings.Contains(string(b), senha) {
			t.Errorf("senha em texto puro em %s", p)
		}
		if strings.Contains(string(b), "554=***") {
			comLogon++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if arquivos < 3 || comLogon < 2 {
		t.Fatalf("conferência fraca: %d arquivos, %d com o logon mascarado (esperado log e store)", arquivos, comLogon)
	}
}

func TestMascarar(t *testing.T) {
	for _, c := range []struct{ de, para string }{
		{"8=FIX.4.435=A553=U554=abc10=000", "8=FIX.4.435=A553=U554=***10=000"},
		{"35=A|554=abc|925=nova|", "35=A|554=***|925=***|"},
		{"554=x|", "554=***|"},
		{"35=8|5540=1|1554=2|", "35=8|5540=1|1554=2|"},
	} {
		if r := MascararTexto(c.de); r != c.para {
			t.Errorf("%q → %q, esperado %q", c.de, r, c.para)
		}
	}
}
