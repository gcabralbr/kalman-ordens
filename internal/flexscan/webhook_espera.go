package flexscan

import (
	"fmt"
	"log/slog"
	"time"
)

// Webhook que não chega (SPEC-ORDENS v1.22 §2). Duas coisas abrem uma espera pelo webhook de uma
// ordem: um POST/PUT/DELETE nosso respondido com 202 (contada do envio: o webhook pode chegar
// antes do 202) e uma mudança espontânea vista pela consulta periódica (fill, stop executado,
// cancelamento). Chegou um webhook daquela ordem: a espera fecha. Passou FLEXSCAN_WEBHOOK_ESPERA_S
// (5) sem ele: FLEXSCAN_WEBHOOK_ATRASADO (não crítico) e uma falta. faltasCriticas faltas seguidas:
// FLEXSCAN_WEBHOOK_SEM_CHEGAR (crítico) e a conferência do cadastro roda na hora. Um webhook de
// qualquer ordem zera as faltas e resolve os dois alertas. Sem o receptor ligado, nada disso.
// v1.29: as faltas seguidas são FLEXSCAN_WEBHOOK_FALTAS_N (3); e uma mudança espontânea sem webhook
// já pede a conferência na hora (no máximo uma por intervaloConfEvento; a das faltas seguidas, sempre).

// intervaloConfEvento: entre duas conferências pedidas por indício de webhook faltando.
const intervaloConfEvento = time.Minute

// Motivos da espera (vão no texto do alerta).
const (
	esperaAcao      = "acao"
	esperaMudanca   = "mudanca"
	textoEsperaAcao = "%s %s respondido 202 e nenhum webhook da ordem em %s"
	textoMudanca    = "a consulta periodica viu uma mudanca de %s (%s) e nenhum webhook dela em %s"
)

// esperarWebhookLocked: abre (ou renova) a espera pelo webhook de o. desde = o envio do pedido
// (ação) ou a hora da consulta (mudança). Se um webhook da ordem já chegou depois de desde, não abre.
func (p *Ponte) esperarWebhookLocked(o *ordemP, desde time.Time, motivo, detalhe string) {
	if !p.cfg.Webhook || p.cfg.EsperaWebhook <= 0 {
		return
	}
	if !o.whUltimo.IsZero() && !o.whUltimo.Before(desde) {
		return
	}
	o.whEsperaDesde, o.whEsperaMotivo, o.whEsperaDetalhe = desde, motivo, detalhe
}

// webhookDaOrdemLocked: um retrato de o chegou num webhook.
func (p *Ponte) webhookDaOrdemLocked(o *ordemP, agora time.Time) {
	o.whUltimo = agora
	o.whEsperaDesde = time.Time{}
}

// webhookChegou: um webhook válido (qualquer ordem) zera as faltas e resolve os alertas.
func (p *Ponte) webhookChegou() {
	p.mu.Lock()
	tinha := p.whFaltas > 0 || p.whAtrasado || p.whCritico
	p.whFaltas, p.whAtrasado, p.whCritico = 0, false, false
	p.mu.Unlock()
	if tinha {
		p.ouv.Resolver("FLEXSCAN_WEBHOOK_ATRASADO")
		p.ouv.Resolver("FLEXSCAN_WEBHOOK_SEM_CHEGAR")
	}
}

// conferirEsperas: no Tick, as esperas vencidas viram falta.
func (p *Ponte) conferirEsperas(agora time.Time) {
	var av avisos
	p.mu.Lock()
	for _, o := range p.ordens {
		if o.whEsperaDesde.IsZero() || agora.Sub(o.whEsperaDesde) < p.cfg.EsperaWebhook {
			continue
		}
		texto := fmt.Sprintf(textoEsperaAcao, o.whEsperaDetalhe, o.nome, p.cfg.EsperaWebhook)
		if o.whEsperaMotivo == esperaMudanca {
			texto = fmt.Sprintf(textoMudanca, o.nome, o.whEsperaDetalhe, p.cfg.EsperaWebhook)
		}
		o.whEsperaDesde = time.Time{}
		p.whFaltas++
		p.whAtrasado = true
		faltas := p.whFaltas
		slog.Warn("flexscan: webhook não chegou", "nome", o.nome, "motivo", o.whEsperaMotivo, "faltas_seguidas", faltas)
		av = append(av, func(p *Ponte) {
			p.ouv.Alertar("FLEXSCAN_WEBHOOK_ATRASADO", "FLEXSCAN_WEBHOOK_ATRASADO", "[flexscan] "+texto)
		})
		if o.whEsperaMotivo == esperaMudanca {
			p.pedirConferenciaLocked(agora, "mudanca sem webhook", true)
		}
		if faltas >= p.cfg.FaltasWebhook && !p.whCritico {
			p.whCritico = true
			p.pedirConferenciaLocked(agora, fmt.Sprintf("%d esperas sem webhook", faltas), false)
			av = append(av, func(p *Ponte) {
				p.ouv.Alertar("FLEXSCAN_WEBHOOK_SEM_CHEGAR", "FLEXSCAN_WEBHOOK_SEM_CHEGAR",
					fmt.Sprintf("[flexscan] %d esperas seguidas sem webhook (a ultima: %s): so a consulta segura as execucoes; conferindo o cadastro do webhook agora", faltas, texto))
			})
		}
	}
	p.mu.Unlock()
	for _, f := range av {
		f(p)
	}
}

// pedirConferenciaLocked (v1.29): a conferência do cadastro no próximo Tick, por indício de webhook
// faltando; a da mudança sem webhook (espacar) no máximo uma por intervaloConfEvento; a das faltas
// seguidas, sempre.
func (p *Ponte) pedirConferenciaLocked(agora time.Time, por string, espacar bool) {
	if p.cfg.WebhookURL == "" || (espacar && !p.ultConfWebhook.IsZero() && agora.Sub(p.ultConfWebhook) < intervaloConfEvento) {
		return
	}
	if p.conferirPor == "" {
		p.conferirPor = por
	}
}
