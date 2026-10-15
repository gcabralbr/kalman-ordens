package pnt

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
	recusados []string
	stores    []string
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
func (o *ouvinteTeste) EventoSessao(string, string) {}
func (o *ouvinteTeste) LogonRecusado(s string) {
	o.mu.Lock()
	o.recusados = append(o.recusados, s)
	o.mu.Unlock()
}
func (o *ouvinteTeste) StoreIlegivel(s string) {
	o.mu.Lock()
	o.stores = append(o.stores, s)
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

// A senha vai no logon (no fio, inteira) e não aparece nos logs do diretório FIX (log de
// mensagens e de eventos; o log antigo do PT gravava 553/554 em texto puro). O store guarda o
// logon como foi ao fio, senha inclusive (SPEC v1.3 A1): é o que o reenvio relê. Diretório 700/600.
func TestSenhaSoNoStore(t *testing.T) {
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
	logs, mascarados, noStore := 0, 0, 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch strings.Split(filepath.ToSlash(rel), "/")[0] {
		case "log":
			logs++
			if strings.Contains(string(b), senha) {
				t.Errorf("senha em texto puro no log %s", p)
			}
			if strings.Contains(string(b), "554=***") {
				mascarados++
			}
		case "store":
			if strings.Contains(string(b), "\x01554="+senha+"\x01") {
				noStore++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// diretório FIX só do usuário do serviço: 700/600 (no Windows o modo não se aplica)
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, _ := d.Info()
			esperado := fs.FileMode(0o600)
			if d.IsDir() {
				esperado = 0o700
			}
			if info.Mode().Perm() != esperado {
				t.Errorf("%s com modo %o, esperado %o", p, info.Mode().Perm(), esperado)
			}
			return nil
		})
	}
	if logs < 2 || mascarados < 1 || noStore != 1 {
		t.Fatalf("conferência fraca: %d log(s), %d com o logon mascarado, %d arquivo(s) do store com o logon fiel (esperado 1, o .body)", logs, mascarados, noStore)
	}
}

// subirComSenha: sessão com senha (sem log em arquivo) no diretório dado, espera crescente curta.
func subirComSenha(t *testing.T, f *falso.Falso, dir string, esperas ...time.Duration) (*Sessao, *ouvinteTeste) {
	t.Helper()
	o := &ouvinteTeste{}
	s, err := NovaSessao(ConfigSessao{SenderCompID: "CLIENTE", Host: "127.0.0.1", Porta: f.Porta(), Dir: dir,
		Inicio: "00:00:00", Fim: "00:00:00", HeartBtInt: 5, ReconexaoS: 1, Usuario: "U", Senha: "Senha-de-teste-123",
		Esperas: esperas, SemLogArquivo: true}, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Iniciar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Parar)
	return s, o
}

func novoFalso(t *testing.T) *falso.Falso {
	t.Helper()
	f, err := falso.Novo(falso.Config{Contraparte: "CLIENTE", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Iniciar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Parar)
	return f
}

func gapFills(f *falso.Falso) int {
	n := 0
	for _, m := range f.Sessao() {
		if v, _ := m.Valor(123); m.Tipo == "4" && v == "Y" {
			n++
		}
	}
	return n
}

// HML 14/10 (SPEC v1.3 A1, D1): depois de uma queda abrupta o PnT recusa os logons ("already
// logged on") sem contar a sequência deles; aceito o logon, pede o reenvio desde o primeiro
// recusado. Os logons gravados (com a senha) se leem: viram gap fill e a sessão fica de pé. Cada
// recusa chega ao ouvinte, e as tentativas se espaçam com a espera crescente.
func TestLogonsRecusadosReenvioViraGapFill(t *testing.T) {
	f := novoFalso(t)
	s, o := subirComSenha(t, f, t.TempDir(), 50*time.Millisecond, 100*time.Millisecond, 200*time.Millisecond, 400*time.Millisecond)
	esperar(t, "logon", s.Logado)
	f.RecusarLogons("Session already logged on")
	if err := f.Derrubar(); err != nil {
		t.Fatal(err)
	}
	esperar(t, "4 logons recusados", func() bool { return f.Recusas() >= 4 })
	f.RecusarLogons("")
	esperar(t, "logon aceito", func() bool { return s.Logado() && f.Logado() })
	esperar(t, "gap fill dos logons recusados", func() bool { return gapFills(f) >= 1 })
	if _, err := s.Enviar(MassStatus("12345", "MS-9")); err != nil {
		t.Fatal(err)
	}
	if r, err := f.Proxima(5 * time.Second); err != nil || r.Tipo != "AF" {
		t.Fatalf("a sessão não ficou de pé depois do gap fill: %v %v", r.Bruta, err)
	}
	o.mu.Lock()
	recusados, stores := len(o.recusados), len(o.stores)
	o.mu.Unlock()
	if recusados < 4 || stores != 0 {
		t.Fatalf("recusas avisadas=%d (esperado ≥ 4), store ilegível=%d (esperado 0)", recusados, stores)
	}
	// espera crescente: cada intervalo entre recusas ≥ a espera da tentativa (50, 100, 200 ms)
	em := f.RecusasEm()
	for i, minimo := range []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond} {
		if d := em[i+1].Sub(em[i]); d < minimo {
			t.Errorf("intervalo %d entre recusas %s, esperado ≥ %s", i+1, d, minimo)
		}
	}
}

// O store como a v1.2 gravava (logon com a senha mascarada, 9 errado): sem o reparo na subida, o
// reenvio não lê o logon ("Resend Msg Parse Error") e a sessão cai em laço (HML 14/10).
func TestStoreDaV12ReparadoNaSubida(t *testing.T) {
	f := novoFalso(t)
	dir := t.TempDir()
	s, _ := subirComSenha(t, f, dir, 50*time.Millisecond)
	esperar(t, "logon", s.Logado)
	s.Parar()
	esperar(t, "queda no falso", func() bool { return !f.Logado() })
	pre := strings.TrimSuffix(mustGlob(t, filepath.Join(dir, "store", "*.body")), ".body")
	b, err := os.ReadFile(pre + ".body")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := os.ReadFile(pre + ".header")
	if err != nil {
		t.Fatal(err)
	}
	// o store da v1.2: cada mensagem mascarada depois de montada (o 9 continua o de antes)
	var corpo, indice []byte
	linhas := strings.Split(strings.TrimSpace(string(idx)), "\n")
	for _, l := range linhas {
		var seq, pos, tam int
		if _, err := fmt.Sscanf(l, "%d,%d,%d", &seq, &pos, &tam); err != nil {
			t.Fatal(err)
		}
		m := Mascarar(b[pos : pos+tam])
		indice = fmt.Appendf(indice, "%d,%d,%d\n", seq, len(corpo), len(m))
		corpo = append(corpo, m...)
	}
	if len(corpo) == len(b) {
		t.Fatal("o store não tinha a senha")
	}
	if err := os.WriteFile(pre+".body", corpo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pre+".header", indice, 0o600); err != nil {
		t.Fatal(err)
	}
	// o falso perde o que recebeu (logon e logout): pede o reenvio no próximo logon
	if err := f.AjustarSequenciaEsperada(-len(linhas)); err != nil {
		t.Fatal(err)
	}
	s2, o2 := subirComSenha(t, f, dir, 50*time.Millisecond)
	esperar(t, "logon depois do reparo", func() bool { return s2.Logado() && f.Logado() })
	esperar(t, "gap fill do logon mascarado", func() bool { return gapFills(f) >= 1 })
	o2.mu.Lock()
	defer o2.mu.Unlock()
	if len(o2.stores) != 0 || s2.Reinicios() != 0 {
		t.Fatalf("store ilegível=%v reinícios=%d: o reparo na subida não bastou", o2.stores, s2.Reinicios())
	}
}

func mustGlob(t *testing.T, padrao string) string {
	t.Helper()
	m, err := filepath.Glob(padrao)
	if err != nil || len(m) != 1 {
		t.Fatalf("%s: %v %v", padrao, m, err)
	}
	return m[0]
}

// SPEC v1.3 B1: uma mensagem do store que o reenvio não lê → aviso, sessão derrubada, store
// reparado, reconexão com espera; nunca o laço silencioso.
func TestStoreIlegivelNoReenvio(t *testing.T) {
	f := novoFalso(t)
	dir := t.TempDir()
	s, o := subirComSenha(t, f, dir, 50*time.Millisecond)
	esperar(t, "logon", s.Logado)
	if _, err := s.Enviar(MassStatus("12345", "MS-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Proxima(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := f.Derrubar(); err != nil {
		t.Fatal(err)
	}
	esperar(t, "queda", func() bool { return !s.Logado() })
	// o AF gravado (última mensagem) fica com o 9 errado, e o falso pede o reenvio dele
	pre := strings.TrimSuffix(mustGlob(t, filepath.Join(dir, "store", "*.body")), ".body")
	b, _ := os.ReadFile(pre + ".body")
	cab := "8=FIX.4.4\x019="
	i := strings.LastIndex(string(b), cab) + len(cab)
	b[i]++
	if err := os.WriteFile(pre+".body", b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.AjustarSequenciaEsperada(-1); err != nil {
		t.Fatal(err)
	}
	esperar(t, "aviso de store ilegível", func() bool { o.mu.Lock(); defer o.mu.Unlock(); return len(o.stores) >= 1 })
	esperar(t, "reinício pelo vigia", func() bool { return s.Reinicios() >= 1 })
	esperar(t, "logon depois do reparo", func() bool { return s.Logado() && f.Logado() })
	esperar(t, "gap fill do AF", func() bool { return gapFills(f) >= 1 })
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.stores) != 1 || !strings.Contains(o.stores[0], "Incorrect Message Length") {
		t.Fatalf("avisos de store ilegível: %v", o.stores)
	}
}

// SPEC v1.3 C1: logout "MsgSeqNum too low, expecting N" → a próxima tentativa já sai com N (a
// biblioteca, sozinha, subiria de 1 em 1: 50 tentativas, uma por segundo).
func TestLogoutTooLowAjustaSequencia(t *testing.T) {
	f := novoFalso(t)
	s, _ := subirComSenha(t, f, t.TempDir(), 50*time.Millisecond)
	esperar(t, "logon", s.Logado)
	if err := f.Derrubar(); err != nil {
		t.Fatal(err)
	}
	esperar(t, "queda", func() bool { return !s.Logado() })
	if err := f.AjustarSequenciaEsperada(50); err != nil {
		t.Fatal(err)
	}
	inicio := time.Now()
	esperar(t, "logon com a sequência ajustada", func() bool { return s.Logado() && f.Logado() })
	if d := time.Since(inicio); d > 5*time.Second {
		t.Fatalf("logon só depois de %s", d)
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
