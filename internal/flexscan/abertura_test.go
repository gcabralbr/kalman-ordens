package flexscan

import (
	"strings"
	"sync"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/flexscan/falso"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// ouvinteAbertura: o ouvinte de teste com o AtualizarAlerta do motor (o texto vivo de cada crítico).
type ouvinteAbertura struct {
	ouvinteTeste
	mu     sync.Mutex
	textos map[string]string
}

func (o *ouvinteAbertura) Alertar(c, k, t string) {
	o.ouvinteTeste.Alertar(c, k, t)
	o.AtualizarAlerta(k, t)
}

func (o *ouvinteAbertura) AtualizarAlerta(k, t string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.textos == nil {
		o.textos = map[string]string{}
	}
	o.textos[k] = t
}

// desde: os alertas a partir do índice i, e o novo índice.
func (o *ouvinteAbertura) desde(i int) ([]string, int) {
	o.ouvinteTeste.mu.Lock()
	defer o.ouvinteTeste.mu.Unlock()
	return append([]string(nil), o.alertas[i:]...), len(o.alertas)
}

func (o *ouvinteAbertura) texto(k string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.textos[k]
}

func contarCodigo(as []string, cod string) (n int, ult string) {
	for _, a := range as {
		if strings.HasPrefix(a, cod+" ") {
			n++
			ult = a
		}
	}
	return
}

// v1.32 §4 (12/11): o login que falha às 08:00 e o webhook limpo de madrugada, de ponta a ponta no
// relógio controlado. Na tolerância (até 08:45, sem ordem viva): nenhum crítico, uma linha
// FLEXSCAN_ABERTURA. O webhook que some depois de conferido no dia é crítico. Ainda falhando depois das
// 08:45: FLEXSCAN_LOGIN com as tentativas e desde quando, e o recadastro fora da tolerância é crítico,
// dizendo que foi a conferência depois do login. Com ordem viva: crítico na hora.
func TestAberturaTolerada(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 11, 11, 18, 0, 0, 0, relogio.Fuso)) // quarta
	f := falso.Novo("flx_abertura", "senha-abertura", rel.Agora)
	defer f.Parar()
	const url = "https://kalman.exemplo/flexscan/webhook/segredo"
	f.CadastrarWebhook(url, "order.all", "algo.changed")
	ouv := &ouvinteAbertura{}
	ag, _ := LerAgenda("08:00-18:30", "08:15", "08:30")
	ag.ToleranciaAbertura = 8*60 + 45
	p := Nova(Config{URL: f.URL(), Usuario: "flx_abertura", Senha: "senha-abertura", Timeout: time.Second, Agenda: &ag,
		WebhookURL: url, RenovarAntes: 30 * time.Minute, EsperaLogin: 30 * time.Second, LoginMax: 1000}, ouv, rel)
	p.Iniciar()
	defer p.Parar()
	esperar(t, p.Logado)
	assentar := func() {
		esperar(t, func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			return len(p.fila) == 0 && p.emCurso == 0 && !p.logando && !p.renovando && !p.conferindoWebhook
		})
	}
	// avança de passo em passo até dia/hh:mm:ss, com um Tick por passo
	ate := func(dia, h, m, s int, passo time.Duration) {
		alvo := time.Date(2026, 11, dia, h, m, s, 0, relogio.Fuso)
		for rel.Agora().Before(alvo) {
			rel.Avancar(passo)
			p.Tick()
			assentar()
		}
	}
	ate(11, 18, 1, 0, time.Minute)
	_, visto := ouv.desde(0)

	// quinta: a limpeza noturna e a Flexscan sem login às 08:00; volta às 08:00:10
	ate(12, 3, 50, 0, time.Minute)
	f.LimparWebhooks()
	ate(12, 7, 59, 0, time.Minute)
	if !p.Dormindo() {
		t.Fatal("de madrugada a ponte devia dormir")
	}
	f.ForaDoAr(true)
	ate(12, 8, 0, 10, time.Second)
	f.ForaDoAr(false)
	ate(12, 8, 1, 0, time.Second)
	as, visto := ouv.desde(visto)
	if n, _ := contarCodigo(as, "FLEXSCAN_LOGIN"); n != 0 {
		t.Fatalf("na tolerância, sem FLEXSCAN_LOGIN: %v", as)
	}
	if n, _ := contarCodigo(as, "FLEXSCAN_WEBHOOK_AUSENTE"); n != 0 {
		t.Fatalf("na tolerância, sem FLEXSCAN_WEBHOOK_AUSENTE: %v", as)
	}
	n, linha := contarCodigo(as, "FLEXSCAN_ABERTURA")
	if n != 1 || !strings.Contains(linha, "logou 08:00:30 na 2a tentativa (a 1a, 08:00:00: login 503") ||
		!strings.Contains(linha, "webhook recadastrado (ausente na conferencia depois do login das 08:00:30 (reconexao)") {
		t.Fatalf("a linha da abertura: %v", as)
	}
	if !p.Logado() || len(f.Gatilhos(url)) != 2 {
		t.Fatalf("logado=%v gatilhos=%v", p.Logado(), f.Gatilhos(url))
	}

	// o cadastro some depois de conferido hoje: a conferência da abertura (08:30) é crítica
	ate(12, 8, 20, 0, time.Minute)
	f.LimparWebhooks()
	ate(12, 8, 31, 0, time.Minute)
	as, visto = ouv.desde(visto)
	if n, ult := contarCodigo(as, "FLEXSCAN_WEBHOOK_AUSENTE"); n != 1 || !strings.Contains(ult, "(conferencia da abertura): recadastrado") {
		t.Fatalf("o sumiço depois de conferido: %v", as)
	}
	if n, _ := contarCodigo(as, "FLEXSCAN_ABERTURA"); n != 0 {
		t.Fatalf("sem linha da abertura: %v", as)
	}

	// sexta: a Flexscan sem login das 08:00 às 08:46; o crítico só a partir das 08:45, com as
	// tentativas; o recadastro depois das 08:45 é crítico
	ate(13, 3, 50, 0, time.Minute)
	f.LimparWebhooks()
	ate(13, 7, 59, 0, time.Minute)
	f.ForaDoAr(true)
	ate(13, 8, 44, 50, 10*time.Second)
	as, visto = ouv.desde(visto)
	if n, _ := contarCodigo(as, "FLEXSCAN_LOGIN"); n != 0 {
		t.Fatalf("antes das 08:45, sem crítico: %v", as)
	}
	ate(13, 8, 46, 0, 10*time.Second)
	as, visto = ouv.desde(visto)
	// uma tentativa a cada 30 s desde 08:00:00: a de 08:45:00 é a 91a e a primeira fora da tolerância
	// (o motor abre o crítico nela e ignora as seguintes); o texto vivo acompanha (08:46:00, a 93a)
	if n, _ := contarCodigo(as, "FLEXSCAN_LOGIN"); n == 0 || !strings.Contains(as[0], "login 503") || !strings.Contains(as[0], "(91 tentativas desde 08:00:00)") {
		t.Fatalf("depois das 08:45, o crítico: %v", as)
	}
	if tx := ouv.texto("FLEXSCAN_LOGIN"); !strings.Contains(tx, "(93 tentativas desde 08:00:00)") {
		t.Fatalf("texto vivo do crítico: %q", tx)
	}
	f.ForaDoAr(false)
	ate(13, 8, 47, 0, 10*time.Second)
	as, visto = ouv.desde(visto)
	if n, ult := contarCodigo(as, "FLEXSCAN_WEBHOOK_AUSENTE"); n != 1 || !strings.Contains(ult, "(conferencia da abertura, depois do login das 08:46:30 (reconexao)): recadastrado") {
		t.Fatalf("o recadastro fora da tolerância: %v", as)
	}
	if n, _ := contarCodigo(as, "FLEXSCAN_ABERTURA"); n != 0 {
		t.Fatalf("o login que deu crítico não vai para a linha da abertura: %v", as)
	}

	// com ordem viva, o login que falha às 08:10 é crítico na hora (segunda: o token de sexta venceu)
	ate(16, 8, 9, 0, time.Minute)
	p.MarcarVivas(true)
	f.ForaDoAr(true)
	p.semSessao("teste")
	ate(16, 8, 9, 20, time.Second)
	as, _ = ouv.desde(visto)
	if n, _ := contarCodigo(as, "FLEXSCAN_LOGIN"); n == 0 {
		t.Fatalf("com ordem viva, o crítico na hora: %v", as)
	}
	f.ForaDoAr(false)
	p.MarcarVivas(false)
}
