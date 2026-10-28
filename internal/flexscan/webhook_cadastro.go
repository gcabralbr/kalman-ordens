package flexscan

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Cadastro do webhook (SPEC-ORDENS v1.21 §2 e v1.21.1, SPEC-FLEXSCAN-v1 §5.4). Na partida e a cada
// ConfereWebhook: GET da lista do usuário; sem a nossa URL com os dois gatilhos (somados entre os
// nossos cadastros), cadastra de novo só os que faltam (sem apagar nem substituir os outros
// cadastros, sem repetir um gatilho já cadastrado) e alerta crítico FLEXSCAN_WEBHOOK_AUSENTE. Este é o
// único arquivo da ponte que fala com o recurso de webhooks, e só com GET e POST (o teste de
// arquitetura confere). A URL tem o segredo: nunca vai para log, tráfego ou alerta.

const caminhoWebhooks = "/webhooks"

// gatilhosWebhook: os do cadastro feito à mão em 27/10. O retrato da estratégia sorder que a ponte lê
// é o que a referência da API descreve para o algo.changed; com os dois cadastrados, chegou em todos
// os passos da 234339 (28/10). A conferência de 03:50 de 28/10 recadastrou só o order.all.
var gatilhosWebhook = []string{"order.all", "algo.changed"}

type cadastroWebhook struct {
	ID       string   `json:"id"`
	URL      string   `json:"url"`
	Triggers []string `json:"triggers"`
}

// conferirWebhook: uma conferência (tarefa da fila).
func (p *Ponte) conferirWebhook(ctx context.Context) {
	defer func() {
		p.mu.Lock()
		p.conferindoWebhook = false
		p.mu.Unlock()
	}()
	faltam, n, ok := p.listarWebhooks(ctx)
	if !ok {
		return
	}
	if len(faltam) == 0 {
		p.marcarWebhook(true, false)
		p.ouv.Resolver("FLEXSCAN_WEBHOOK_AUSENTE")
		return
	}
	p.marcarWebhook(false, false)
	// ausente ou incompleto: cadastra a nossa com os gatilhos que faltam (os outros n cadastros ficam)
	gatilhos := strings.Join(faltam, ",")
	corpo := map[string]any{"target": map[string]any{"type": "user", "id": p.cfg.Usuario}, "url": p.cfg.WebhookURL,
		"triggers": faltam}
	t0 := time.Now()
	r := p.cli.pedir(ctx, http.MethodPost, caminhoWebhooks, corpo)
	if r.Status == http.StatusUnauthorized {
		p.semSessao("401 depois de novo login")
	}
	p.linhaGravada(TrafegoWebhookCadastro, msgCadastro, statusDe(r), duracaoDe(r, time.Since(t0)),
		comResposta(fmt.Sprintf("recadastro (%s; %d outro(s) cadastro(s) do usuario)", gatilhos, n), r))
	if r.Status != http.StatusOK && r.Status != http.StatusCreated {
		texto := fmt.Sprintf("[flexscan] webhook sem %s no cadastro da Flexscan e o recadastro falhou (%s): so a consulta segura as execucoes", gatilhos, r.TextoErro())
		slog.Warn("flexscan: recadastro do webhook falhou", "status", r.Status, "erro", r.TextoErro())
		p.ouv.Alertar("FLEXSCAN_WEBHOOK_AUSENTE", "FLEXSCAN_WEBHOOK_AUSENTE", texto)
		return
	}
	slog.Warn("flexscan: webhook ausente do cadastro: recadastrado")
	p.ouv.Alertar("FLEXSCAN_WEBHOOK_AUSENTE", "FLEXSCAN_WEBHOOK_AUSENTE", "[flexscan] webhook sem "+gatilhos+" no cadastro da Flexscan: recadastrado")
	p.marcarWebhook(false, true)
	// confere de novo: completo, o alerta se resolve (fica o registro de que faltava)
	if faltam, _, ok := p.listarWebhooks(ctx); ok && len(faltam) == 0 {
		p.marcarWebhook(true, false)
		p.ouv.Resolver("FLEXSCAN_WEBHOOK_AUSENTE")
	}
}

// listarWebhooks: o GET; faltam = os gatilhos que nenhum cadastro da nossa URL tem; ok falso = não
// conferido (erro: webhook_cadastrado fica null).
func (p *Ponte) listarWebhooks(ctx context.Context) (faltam []string, outros int, ok bool) {
	t0 := time.Now()
	r := p.cli.pedir(ctx, http.MethodGet, caminhoWebhooks, nil)
	dur := duracaoDe(r, time.Since(t0))
	if r.Status == http.StatusUnauthorized {
		p.semSessao("401 depois de novo login")
	}
	if r.Status != http.StatusOK {
		slog.Warn("flexscan: conferência do webhook sem resposta válida", "status", r.Status, "erro", r.TextoErro())
		p.mu.Lock()
		p.webhookCad = nil
		p.mu.Unlock()
		p.linhaGravada(TrafegoWebhookCadastro, msgCadastro, statusDe(r), dur, comResposta("conferencia", r))
		return nil, 0, false
	}
	var l struct {
		Records []cadastroWebhook `json:"records"`
	}
	if err := decodificar(r.Corpo, &l); err != nil {
		p.linhaGravada(TrafegoWebhookCadastro, msgCadastro, r.Status, dur, "conferencia: lista ilegivel")
		return nil, 0, false
	}
	var nossos []string
	nossa := false
	for _, c := range l.Records {
		if c.URL == p.cfg.WebhookURL {
			nossa = true
			nossos = append(nossos, c.Triggers...)
		} else {
			outros++
		}
	}
	for _, g := range gatilhosWebhook {
		if !slices.Contains(nossos, g) {
			faltam = append(faltam, g)
		}
	}
	situacao := "presente"
	switch {
	case !nossa:
		situacao = "ausente"
	case len(faltam) > 0:
		situacao = "incompleto, sem " + strings.Join(faltam, ",")
	}
	p.linhaGravada(TrafegoWebhookCadastro, msgCadastro, r.Status, dur,
		fmt.Sprintf("conferencia: %s (%d outro(s) cadastro(s) do usuario)", situacao, outros))
	return faltam, outros, true
}

func (p *Ponte) marcarWebhook(cadastrado, recadastrado bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.webhookCad = &cadastrado
	agora := p.rel.Agora()
	if cadastrado || !recadastrado {
		p.webhookConfEm = agora
	}
	if recadastrado {
		p.webhookRecadEm = agora
	}
}
