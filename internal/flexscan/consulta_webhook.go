package flexscan

import (
	"context"
	"net/http"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// Consulta pedida pelo webhook (v1.24, 29/10 11:02:00, op 234413): o webhook da execução chegou
// 94 ms depois do POST, e a consulta de confirmação, disparada por ele 11 ms depois, deu 404: a
// API de consulta da Flexscan ainda não conhecia a ordem (a de 313 ms já achou).
//   - A consulta disparada pelo webhook ("confirma", "webhook_invalido") respeita a mesma espera
//     da primeira consulta depois do POST: não sai com o POST em curso nem antes de
//     FLEXSCAN_PRIMEIRA_CONSULTA_MS desde o 202. Quem solta é o Tick.
//   - Com o estado final (executada/cancelada) já trazido pelo webhook, o 404 até JanelaRegistro
//     depois do 202 é "ainda não registrada": vai ao registro e ao tráfego marcado
//     (nao_registrada, não conta como erro) e a consulta se repete: no próximo passo da consulta
//     ativa ou, sem ele, em RepeteNaoRegistrada.

// JanelaRegistro: até quanto depois do 202 o 404 de uma ordem já final pelo webhook é "ainda não
// registrada". RepeteNaoRegistrada: quando a consulta se repete.
const (
	JanelaRegistro      = 2 * time.Second
	RepeteNaoRegistrada = 300 * time.Millisecond
)

// PrefixoNaoRegistrada: o começo do texto gravado (kalmanordemevento) e do resumo do tráfego.
const PrefixoNaoRegistrada = "ainda nao registrada"

// consultaDoWebhookLocked: a consulta pedida pelo webhook sai já, se a ordem está liberada, ou
// fica para o Tick.
func (p *Ponte) consultaDoWebhookLocked(o *ordemP, origem string) {
	if o.confirmar == "" || origem == "confirma" {
		o.confirmar = origem
	}
	if !o.consultaNaFila {
		p.soltarConsultaWebhookLocked(o, p.rel.Agora())
	}
}

// soltarConsultaWebhookLocked: enfileira a consulta pendente do webhook se a espera passou.
func (p *Ponte) soltarConsultaWebhookLocked(o *ordemP, agora time.Time) bool {
	if o.fase.Terminal() {
		// outra consulta já trouxe o estado final (a do webhook volta antes de mudar a fase)
		o.confirmar, o.confirmarApos = "", time.Time{}
		return false
	}
	if o.postEmCurso || agora.Before(o.confirmarApos) ||
		(!o.aceita202Em.IsZero() && agora.Before(o.aceita202Em.Add(p.cfg.PrimeiraConsulta))) {
		return false
	}
	origem, prio := o.confirmar, prioProtecao
	if origem != "confirma" {
		prio = prioConsulta
	}
	o.confirmar, o.confirmarApos = "", time.Time{}
	o.consultaNaFila = true
	// o passo da consulta ativa que já venceu é esta mesma consulta (sem duas seguidas)
	for ps := o.passosDaJanela(); !o.ativaDesde.IsZero() && o.passo < len(ps) && !agora.Before(o.ativaDesde.Add(ps[o.passo])); {
		o.passo++
	}
	oo := o
	p.enfileirarLocked(prio, "", func(ctx context.Context) { p.consultar(ctx, oo, origem) })
	return true
}

// aindaNaoRegistrada: o 404 de uma ordem que o webhook já deu como final, até JanelaRegistro
// depois do 202 — registra (sem erro) e agenda a repetição. Falso = o 404 segue o caminho de sempre.
func (p *Ponte) aindaNaoRegistrada(o *ordemP, id, origem string, r Resposta, dur time.Duration) bool {
	agora := p.rel.Agora()
	p.mu.Lock()
	sim := o.whFinal && !o.aceita202Em.IsZero() && agora.Before(o.aceita202Em.Add(JanelaRegistro))
	if sim && (o.ativaDesde.IsZero() || o.passo >= len(o.passosDaJanela())) {
		// sem passo da consulta ativa pela frente: a repetição é a do webhook
		repete := "confirma" // a ativa e a periódica se repetem como a do webhook
		if origem == "webhook_invalido" {
			repete = origem
		}
		if o.confirmar == "" || repete == "confirma" {
			o.confirmar = repete
		}
		o.confirmarApos = agora.Add(RepeteNaoRegistrada)
	}
	p.mu.Unlock()
	if !sim {
		return false
	}
	texto := PrefixoNaoRegistrada + ": " + r.TextoErro()
	p.ouv.Registrar(repo.EventoFIX{Direcao: "REC", MsgType: "FLX-404", ClOrdID: o.nome, OrderID: id, Texto: cortar("GET "+texto, 1000),
		Codigo: codigoTexto(r.ErroAPI().Codigo), Bruta: mascararCorpo(string(r.Corpo))})
	p.traf.linhaNaoRegistrada(http.StatusNotFound, duracaoDe(r, dur), o.nome, id, origem+" -> "+texto)
	return true
}
