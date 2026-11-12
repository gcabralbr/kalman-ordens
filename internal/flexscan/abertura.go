package flexscan

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// A abertura da Flexscan (SPEC-ORDENS v1.32 §4, 12/11). Todo dia de pregão, às 08:00, o login da
// ponte que acordou ficava sem resposta em 3 s (a Flexscan lenta no primeiro instante; o seguinte,
// 30 s depois, em 180 ms) e a conferência depois dele achava o webhook ausente (a limpeza noturna da
// Flexscan, ~03:45): dois críticos e dois RESOLVIDO no Telegram, todo dia, sem nada errado.
//
// Agora, num dia de pregão, do início do dia da agenda até FLEXSCAN_TOLERANCIA_ABERTURA (08:45), sem
// ordem viva da Flexscan:
//   - o login que falha (não definitivo) não alerta; a ponte tenta a cada FLEXSCAN_ESPERA_LOGIN_S;
//   - a primeira conferência do dia que acha o cadastro ausente, recadastra e confere completo não
//     alerta;
//   - quando há o que contar, uma linha só (FLEXSCAN_ABERTURA, não crítico) depois da conferência
//     que segue o login.
// Fora disso (no pregão, com ordem viva, erro definitivo, ainda falhando no fim da tolerância, o
// recadastro que falha, o webhook que some depois de conferido no dia): o crítico de sempre, com o
// número de tentativas e desde quando.

// resumoAbertura: o que a linha da abertura conta.
type resumoAbertura struct {
	dia       string
	falhas    int       // os logins que falharam, tolerados
	primeira  time.Time // a primeira falha
	erro      string    // o erro da primeira falha
	logou     time.Time // o login que resolveu
	tentativa int       // a tentativa que logou
	webhook   string    // o recadastro tolerado ("" = nenhum)
	pendente  bool      // há o que contar e a linha ainda não saiu
}

func hms(t time.Time) string { return t.In(relogio.Fuso).Format("15:04:05") }

// toleraLocked: agora está na tolerância da abertura, sem ordem viva (pregao vem de fora: o
// diaDePregao trava p.mu).
func (p *Ponte) toleraLocked(agora time.Time, pregao bool) bool {
	return p.cfg.Agenda.naTolerancia(agora, pregao) && !p.vivasMotor.Load() && !p.vivasLocked()
}

// resumoLocked: o resumo do dia de agora (um dia novo começa vazio).
func (p *Ponte) resumoLocked(agora time.Time) *resumoAbertura {
	if d := diaDe(agora); p.abert.dia != d {
		p.abert = resumoAbertura{dia: d}
	}
	return &p.abert
}

// loginFalhou: um login que falhou (tentarLogin). Na tolerância, só no log e no resumo; senão, o
// FLEXSCAN_LOGIN, cujo texto acompanha as tentativas (as repetições do crítico saem com ele).
func (p *Ponte) loginFalhou(err error, definitivo bool) {
	agora := p.rel.Agora()
	pregao := p.diaDePregao(agora)
	p.mu.Lock()
	if p.loginFalhas == 0 {
		p.loginDesde = agora
	}
	p.loginFalhas++
	n, desde := p.loginFalhas, p.loginDesde
	tolera := !definitivo && p.toleraLocked(agora, pregao)
	if tolera {
		r := p.resumoLocked(agora)
		if r.falhas == 0 {
			r.primeira, r.erro = agora, err.Error()
		}
		r.falhas++
	} else {
		p.loginCritico = true
	}
	p.mu.Unlock()
	if tolera {
		slog.Warn("flexscan: login da abertura falhou (sem alerta até a tolerância)", "tentativa", n,
			"tolerancia", hhmm(p.cfg.Agenda.ToleranciaAbertura), "erro", err)
		return
	}
	texto := "[flexscan] login falhou: " + err.Error()
	if definitivo {
		texto += " (definitivo: não tenta de novo sozinho)"
	}
	if n > 1 {
		texto += fmt.Sprintf(" (%d tentativas desde %s)", n, hms(desde))
	}
	p.ouv.Alertar("FLEXSCAN_LOGIN", "FLEXSCAN_LOGIN", texto)
	p.atualizarAlerta("FLEXSCAN_LOGIN", texto) // já aberto: as repetições saem com o texto novo
}

// loginDeuCerto: o login que resolveu a série (tentarLogin). As falhas toleradas vão para a linha
// da abertura; sem conferência do webhook depois dele, a linha sai já.
func (p *Ponte) loginDeuCerto() {
	agora := p.rel.Agora()
	p.mu.Lock()
	n, critico := p.loginFalhas, p.loginCritico
	p.loginFalhas, p.loginDesde, p.loginCritico = 0, time.Time{}, false
	agoraLinha := false
	if n > 0 && !critico {
		r := p.resumoLocked(agora)
		r.logou, r.tentativa, r.pendente = agora, n+1, true
		agoraLinha = p.cfg.WebhookURL == ""
	}
	p.mu.Unlock()
	if agoraLinha {
		p.linhaAbertura("")
	}
}

// atualizarAlerta: o texto novo de um crítico aberto (o motor; os ouvintes de teste podem não ter).
func (p *Ponte) atualizarAlerta(chave, texto string) {
	if a, ok := p.ouv.(interface{ AtualizarAlerta(chave, texto string) }); ok {
		a.AtualizarAlerta(chave, texto)
	}
}

// webhookToleradoLocked: o cadastro ausente (todos os gatilhos) na primeira conferência do dia que
// achou algo, na tolerância, sem ordem viva: recadastra sem crítico.
func (p *Ponte) webhookToleradoLocked(agora time.Time, pregao bool, faltam []string) bool {
	return len(faltam) == len(gatilhosWebhook) && p.diaWebhookOK != diaDe(agora) && p.toleraLocked(agora, pregao)
}

// linhaAbertura: a linha única da abertura (FLEXSCAN_ABERTURA, não crítico), se há o que contar;
// webhook = o desfecho da conferência quando não foi o recadastro tolerado ("" = nenhuma).
func (p *Ponte) linhaAbertura(webhook string) {
	agora := p.rel.Agora()
	p.mu.Lock()
	r := p.resumoLocked(agora)
	if !r.pendente {
		p.mu.Unlock()
		return
	}
	var partes []string
	if r.tentativa > 0 {
		partes = append(partes, fmt.Sprintf("logou %s na %da tentativa (a 1a, %s: %s)", hms(r.logou), r.tentativa, hms(r.primeira), r.erro))
	}
	switch {
	case r.webhook != "":
		partes = append(partes, r.webhook)
	case webhook != "":
		partes = append(partes, "webhook "+webhook)
	}
	*r = resumoAbertura{dia: r.dia}
	p.mu.Unlock()
	if len(partes) == 0 {
		return
	}
	p.ouv.Alertar("FLEXSCAN_ABERTURA", "", "[flexscan] abertura: "+strings.Join(partes, "; "))
}

// motivoConferencia: o motivo da conferência para o log e o texto do alerta ("depois do login das
// 08:00:33 (reconexao)", "da abertura", "periodica", "por mudanca sem webhook"...).
func motivoConferencia(abertura bool, pedida string) string {
	if strings.HasPrefix(pedida, "login ") {
		pedida = "depois do " + pedida
	} else if pedida != "" {
		pedida = "por " + pedida
	}
	switch {
	case abertura && pedida != "":
		return "da abertura, " + pedida
	case abertura:
		return "da abertura"
	case pedida == "":
		return "periodica"
	}
	return pedida
}

// porTexto: a conferência no texto do alerta.
func porTexto(por string) string { return "conferencia " + por }
