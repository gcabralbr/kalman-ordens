package flexscan

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/flexscan/falso"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

func TestLerAgenda(t *testing.T) {
	a, err := LerAgenda("08:00-18:30", "08:15", "08:30")
	if err != nil || a.Inicio != 480 || a.Fim != 1110 || a.RenovarAbertura != 495 || a.ConferirAbertura != 510 || a.Texto() != "08:00-18:30" {
		t.Fatalf("%+v %v", a, err)
	}
	for _, j := range []string{"", "sempre", "SEMPRE"} {
		if a, err := LerAgenda(j, "", ""); err != nil || !a.Sempre() || a.RenovarAbertura != -1 || a.Texto() != "sempre" {
			t.Fatalf("%q: %+v %v", j, a, err)
		}
	}
	for _, c := range [][3]string{{"08:00", "", ""}, {"18:30-08:00", "", ""}, {"8h-18h", "", ""}, {"08:00-18:30", "25:00", ""}, {"08:00-18:30", "", "x"}} {
		if _, err := LerAgenda(c[0], c[1], c[2]); err == nil {
			t.Errorf("%v: esperava erro", c)
		}
	}
	quinta := func(h, m int) time.Time { return time.Date(2026, 10, 29, h, m, 0, 0, relogio.Fuso) }
	a, _ = LerAgenda("08:00-18:30", "08:15", "08:30")
	if !a.dentro(quinta(8, 0), true) || a.dentro(quinta(18, 30), true) || a.dentro(quinta(10, 0), false) || a.dentro(quinta(7, 59), true) {
		t.Fatal("dentro")
	}
	if a.devida(a.ConferirAbertura, quinta(8, 29), true) || !a.devida(a.ConferirAbertura, quinta(8, 30), true) ||
		a.devida(a.ConferirAbertura, quinta(19, 0), true) || a.devida(a.ConferirAbertura, quinta(9, 0), false) {
		t.Fatal("devida")
	}
	if a.inicioDoDia() != 480 || !a.fimDoDia(quinta(3, 0)).Equal(quinta(18, 30)) {
		t.Fatal("inicioDoDia/fimDoDia")
	}
}

// v1.28: a agenda de ponta a ponta, no relógio controlado (o token da falsa vale 10 h nele): fora
// da janela, sem ordem viva, nenhuma chamada; o token que vence de madrugada não é renovado (a ponte
// dorme, sem alerta) e o login volta no início da janela; a conferência obrigatória da abertura; com
// ordem viva, a conferência e a renovação continuam fora da janela; um envio acorda a ponte.
func TestAgendaDaPonte(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 29, 18, 0, 0, 0, relogio.Fuso)) // quinta
	f := falso.Novo("flx_agenda", "senha-agenda", rel.Agora)
	defer f.Parar()
	f.CadastrarWebhook("https://kalman.exemplo/flexscan/webhook/segredo", "order.all", "algo.changed")
	ouv := &ouvinteTeste{}
	ag, _ := LerAgenda("08:00-18:30", "08:15", "08:30")
	p := Nova(Config{URL: f.URL(), Usuario: "flx_agenda", Senha: "senha-agenda", Timeout: time.Second, Agenda: &ag,
		WebhookURL: "https://kalman.exemplo/flexscan/webhook/segredo", ConfereWebhook: 10 * time.Minute, RenovarAntes: 30 * time.Minute}, ouv, rel)
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
	// conta as linhas do tráfego por tipo (LOGIN com o motivo) desde a última contagem
	var visto int64
	contar := func() map[string]int {
		assentar()
		ls, ult, _ := p.Trafego(visto, 10000)
		visto = ult
		n := map[string]int{}
		for _, l := range ls {
			k := l.Tipo
			if l.Tipo == TrafegoLogin {
				k += ":" + strings.Fields(l.Resumo)[0]
			}
			n[k]++
		}
		return n
	}
	// avança de minuto em minuto até hh:mm (do dia seguinte se já passou), com um Tick por minuto
	ate := func(dias, h, m int) {
		alvo := time.Date(2026, 10, 29+dias, h, m, 0, 0, relogio.Fuso)
		for rel.Agora().Before(alvo) {
			rel.Avancar(time.Minute)
			p.Tick()
			assentar()
		}
	}
	quer := func(onde string, n map[string]int, w map[string]int) {
		t.Helper()
		if fmt.Sprint(n) != fmt.Sprint(w) {
			t.Fatalf("%s: tráfego %v, esperava %v", onde, n, w)
		}
	}
	quer("partida 18:00", contar(), map[string]int{"LOGIN:partida": 1})

	// na janela: a conferência a cada 10 min (18:01, 18:11, 18:21); depois das 18:30, nada a noite
	// inteira, nem a renovação do token (vence às 04:00 no relógio da falsa)
	ate(0, 18, 25)
	quer("18:00–18:25", contar(), map[string]int{TrafegoWebhookCadastro: 3})
	ate(0, 23, 59)
	quer("18:25–23:59", contar(), map[string]int{})
	ate(1, 7, 59)
	quer("00:00–07:59", contar(), map[string]int{})
	if p.Logado() || !p.Dormindo() {
		t.Fatalf("o token vencido fora da janela devia deixar a ponte dormindo: logado=%v", p.Logado())
	}
	ouv.mu.Lock()
	alertas := strings.Join(ouv.alertas, ",")
	ouv.mu.Unlock()
	if strings.Contains(alertas, "SESSAO") {
		t.Fatalf("alerta de sessão de madrugada: %s", alertas)
	}

	// 08:00 (sexta, pregão): o login da abertura e a conferência; às 08:15 o login das 08:00 já vale
	// pela renovação da abertura; às 08:30 a conferência obrigatória (a periódica também cai nela)
	ate(1, 8, 1)
	if !p.Logado() || p.Dormindo() {
		t.Fatal("08:00: devia ter logado")
	}
	ate(1, 8, 29)
	quer("08:00–08:29", contar(), map[string]int{"LOGIN:reconexao": 1, TrafegoWebhookCadastro: 3})
	ate(1, 8, 30)
	quer("08:30", contar(), map[string]int{TrafegoWebhookCadastro: 1})

	// sexta 18:30 em diante, com ordem viva (MarcarVivas, como o motor): a conferência continua a
	// cada 10 min e o token é renovado antes de vencer (o das 08:00 vence às 18:00: renovado às 17:30
	// na janela; o de 17:30 vence às 03:30: renovado às 03:00 fora dela)
	ate(1, 18, 29)
	contar()
	p.MarcarVivas(true)
	ate(1, 23, 59)
	n := contar() // o tráfego é do dia: conta antes da meia-noite
	ate(2, 3, 5)
	for k, v := range contar() {
		n[k] += v
	}
	if n["LOGIN:renovacao"] != 1 || n[TrafegoWebhookCadastro] < 50 {
		t.Fatalf("com ordem viva fora da janela: %v", n)
	}
	p.MarcarVivas(false)

	// sábado: nada (nem a abertura); o token de 03:00 vence às 13:00 e a ponte dorme
	ate(2, 23, 0)
	quer("sábado", contar(), map[string]int{})
	if !p.Dormindo() {
		t.Fatal("sábado: devia dormir")
	}
	// um envio do motor com a ponte dormindo: loga na hora (e o envio volta "sem sessão")
	if _, err := p.Enviar(nil); err == nil {
		t.Fatal("o envio com a ponte dormindo devia falhar")
	}
	esperar(t, p.Logado)
	quer("envio", contar(), map[string]int{"LOGIN:reconexao": 1})
}

// v1.28 (30/10, ajuste aprovado): a partida fora da janela loga uma vez (a instalação valida as
// credenciais) e mais nada: nem conferência, nem renovação; o token vencido deixa a ponte dormindo.
func TestAgendaPartidaForaDaJanela(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 10, 29, 19, 3, 0, 0, relogio.Fuso)) // quinta, depois da janela
	f := falso.Novo("flx_partida", "senha-partida", rel.Agora)
	defer f.Parar()
	ag, _ := LerAgenda("08:00-18:30", "08:15", "08:30")
	p := Nova(Config{URL: f.URL(), Usuario: "flx_partida", Senha: "senha-partida", Timeout: time.Second, Agenda: &ag,
		WebhookURL: "https://kalman.exemplo/flexscan/webhook/segredo", ConfereWebhook: 10 * time.Minute, RenovarAntes: 30 * time.Minute}, &ouvinteTeste{}, rel)
	p.Iniciar()
	defer p.Parar()
	esperar(t, p.Logado)
	for i := 0; i < 4*60; i++ { // até 23:03
		rel.Avancar(time.Minute)
		p.Tick()
		esperar(t, func() bool { return !p.Ocupada() })
	}
	ls, _, _ := p.Trafego(0, 1000)
	if len(ls) != 1 || ls[0].Tipo != TrafegoLogin || !strings.HasPrefix(ls[0].Resumo, "partida") {
		t.Fatalf("fora da janela, só o login da partida: %+v", ls)
	}
}
