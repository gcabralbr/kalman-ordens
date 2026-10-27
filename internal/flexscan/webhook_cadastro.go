package flexscan

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"
)

// Cadastro do webhook (SPEC-ORDENS v1.21 §2, SPEC-FLEXSCAN-v1 §5.4). Na partida e a cada
// ConfereWebhook: GET da lista do usuário; sem a nossa URL com order.all, cadastra de novo (só ela:
// sem apagar nem substituir os outros cadastros) e alerta crítico FLEXSCAN_WEBHOOK_AUSENTE. Este é o
// único arquivo da ponte que fala com o recurso de webhooks, e só com GET e POST (o teste de
// arquitetura confere). A URL tem o segredo: nunca vai para log, tráfego ou alerta.

const caminhoWebhooks = "/webhooks"

const gatilhoWebhook = "order.all"

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
	achou, n, ok := p.listarWebhooks(ctx)
	if !ok {
		return
	}
	if achou {
		p.marcarWebhook(true, false)
		p.ouv.Resolver("FLEXSCAN_WEBHOOK_AUSENTE")
		return
	}
	p.marcarWebhook(false, false)
	// ausente: cadastra a nossa (os outros n cadastros ficam como estão)
	corpo := map[string]any{"target": map[string]any{"type": "user", "id": p.cfg.Usuario}, "url": p.cfg.WebhookURL,
		"triggers": []string{gatilhoWebhook}}
	t0 := time.Now()
	r := p.cli.pedir(ctx, http.MethodPost, caminhoWebhooks, corpo)
	if r.Status == http.StatusUnauthorized {
		p.semSessao("401 depois de novo login")
	}
	p.traf.linha(TrafegoWebhookCadastro, "ENV", statusDe(r), duracaoDe(r, time.Since(t0)), "", "",
		comResposta(fmt.Sprintf("recadastro (%s; %d outro(s) cadastro(s) do usuario)", gatilhoWebhook, n), r))
	if r.Status != http.StatusOK && r.Status != http.StatusCreated {
		texto := fmt.Sprintf("[flexscan] webhook ausente do cadastro da Flexscan e o recadastro falhou (%s): so a consulta segura as execucoes", r.TextoErro())
		slog.Warn("flexscan: recadastro do webhook falhou", "status", r.Status, "erro", r.TextoErro())
		p.ouv.Alertar("FLEXSCAN_WEBHOOK_AUSENTE", "FLEXSCAN_WEBHOOK_AUSENTE", texto)
		return
	}
	slog.Warn("flexscan: webhook ausente do cadastro: recadastrado")
	p.ouv.Alertar("FLEXSCAN_WEBHOOK_AUSENTE", "FLEXSCAN_WEBHOOK_AUSENTE", "[flexscan] webhook ausente do cadastro da Flexscan: recadastrado")
	p.marcarWebhook(false, true)
	// confere de novo: achou, o alerta se resolve (fica o registro de que faltava)
	if achou, _, ok := p.listarWebhooks(ctx); ok && achou {
		p.marcarWebhook(true, false)
		p.ouv.Resolver("FLEXSCAN_WEBHOOK_AUSENTE")
	}
}

// listarWebhooks: o GET; ok falso = não conferido (erro: webhook_cadastrado fica null).
func (p *Ponte) listarWebhooks(ctx context.Context) (achou bool, outros int, ok bool) {
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
		p.traf.linha(TrafegoWebhookCadastro, "ENV", statusDe(r), dur, "", "", comResposta("conferencia", r))
		return false, 0, false
	}
	var l struct {
		Records []cadastroWebhook `json:"records"`
	}
	if err := decodificar(r.Corpo, &l); err != nil {
		p.traf.linha(TrafegoWebhookCadastro, "ENV", r.Status, dur, "", "", "conferencia: lista ilegivel")
		return false, 0, false
	}
	for _, c := range l.Records {
		if c.URL == p.cfg.WebhookURL && slices.Contains(c.Triggers, gatilhoWebhook) {
			achou = true
		} else {
			outros++
		}
	}
	p.traf.linha(TrafegoWebhookCadastro, "ENV", r.Status, dur, "", "",
		fmt.Sprintf("conferencia: %s (%d outro(s) cadastro(s) do usuario)", map[bool]string{true: "presente", false: "ausente"}[achou], outros))
	return achou, outros, true
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
