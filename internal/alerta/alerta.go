// Package alerta emite os alertas do catálogo docs/ALERTAS.md (Telegram e evento ALERTA).
// Os críticos repetem a cada ALERTA_REPETICAO_S até serem resolvidos; a resolução gera RESOLVIDO.
// Confinado à goroutine do motor.
package alerta

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// Catalogo: código → crítico. Fonte: docs/ALERTAS.md (alerta_test.go confere).
var Catalogo = map[string]bool{
	"SESSAO_CAIU": false, "SESSAO_VOLTOU": false, "SESSAO_REJEITOU_MENSAGEM": true, "MENSAGEM_ILEGIVEL": true,
	"PROTECAO_EM_ERRO": true, "PROTECAO_EM_ERRO_PRESA": true, "SAIDA_EM_ERRO": true, "ENTRADA_EM_ERRO": false,
	"CONCILIACAO": false, "MASS_STATUS_REJEITADO": true, "DESCONHECIDA": false, "DIVERGENTE": true,
	"SEM_CONFIRMACAO": true, "PENDENTE_39A": false, "RECUSA_PNT": false, "CANCEL_STOP_REJEITADO": false,
	"CANCEL_SEM_RESPOSTA": true, "SEM_PROTECAO": true, "PROTECAO_REJEITADA": false, "PROTECAO_RECRIADA": true, "PROTECAO_INSTAVEL": true, "STOP_FURADO": true,
	"SAIDA_ESGOTOU": true, "SAIDA_SEM_PRECO": true, "SAIDA_PRECO_DEGRADADO": false,
	"FILL_ORDEM_ENCERRADA": false, "ENTRADA_TARDIA": false, "SEM_POSICAO": false, "LADO_INCOMPATIVEL": false,
	"DUPLICADA_DIFERENTE": false, "LIMITE": false, "PARADA": false, "RETOMADA": false, "ZERAGEM": false,
	"SIMBOLO_DIVERGENTE_COM_POSICAO": true, "CONTRATO_VIGENTE": false, "SIMBOLO_INVALIDO": false,
	"USUARIO_CONFIG": false, "CONTA_AMBIENTE": true, "CONTA_ALTERADA_COM_POSICAO": true, "SAIDA_DIVERGENTE_NAO_EXECUTOU": true,
	"SAIDA_BLOQUEADA_PRECO_ANTIGO": true, "FIM_DIA_POSICAO": true, "RESUMO_DIA": false,
	"VIRADA_COM_OPERACAO": true, "ERRO_INTERNO": true, "RESOLVIDO": false,
}

// Notificador entrega o alerta para fora (Telegram). Não pode bloquear.
type Notificador interface {
	Notificar(codigo, texto string)
}

// Publicador grava o evento ALERTA para o gestor.
type Publicador func(codigo, texto string, operacaoID int64)

type critico struct {
	codigo, texto string
	operacao      int64
	proximo       time.Time
}

// Alertas.
type Alertas struct {
	notif     Notificador
	publicar  Publicador
	rel       relogio.Relogio
	repeticao time.Duration
	ativos    map[string]*critico
}

func Novo(n Notificador, p Publicador, rel relogio.Relogio, repeticao time.Duration) *Alertas {
	return &Alertas{notif: n, publicar: p, rel: rel, repeticao: repeticao, ativos: map[string]*critico{}}
}

// Emitir: alerta com código do catálogo. Se o código é crítico, chave identifica a condição
// (uma segunda emissão com a mesma chave não duplica) e ele repete até Resolver(chave).
func (a *Alertas) Emitir(codigo, chave, texto string, operacaoID int64) {
	cr, ok := Catalogo[codigo]
	if !ok {
		slog.Error("alerta fora do catálogo", "codigo", codigo)
		cr = true
	}
	if cr {
		if chave == "" {
			chave = codigo
		}
		if _, ja := a.ativos[chave]; ja {
			return
		}
		a.ativos[chave] = &critico{codigo: codigo, texto: texto, operacao: operacaoID, proximo: a.rel.Agora().Add(a.repeticao)}
	}
	a.enviar(codigo, texto, operacaoID)
}

// Resolver encerra um crítico (e avisa com RESOLVIDO).
func (a *Alertas) Resolver(chave string) {
	c, ok := a.ativos[chave]
	if !ok {
		return
	}
	delete(a.ativos, chave)
	a.enviar("RESOLVIDO", fmt.Sprintf("%s: %s", c.codigo, chave), c.operacao)
}

// Ativo diz se a condição está aberta.
func (a *Alertas) Ativo(chave string) bool { _, ok := a.ativos[chave]; return ok }

// Ativos lista os críticos abertos (chave → código).
func (a *Alertas) Ativos() map[string]string {
	r := map[string]string{}
	for k, c := range a.ativos {
		r[k] = c.codigo
	}
	return r
}

// Tick repete os críticos vencidos (só Telegram; o evento ALERTA não se repete).
func (a *Alertas) Tick() {
	agora := a.rel.Agora()
	chaves := make([]string, 0, len(a.ativos))
	for k := range a.ativos {
		chaves = append(chaves, k)
	}
	sort.Strings(chaves)
	for _, k := range chaves {
		c := a.ativos[k]
		if agora.Before(c.proximo) {
			continue
		}
		c.proximo = agora.Add(a.repeticao)
		a.notif.Notificar(c.codigo, c.texto+" (continua)")
	}
}

func (a *Alertas) enviar(codigo, texto string, op int64) {
	slog.Warn("ALERTA", "codigo", codigo, "texto", texto, "operacao", op)
	a.notif.Notificar(codigo, texto)
	if a.publicar != nil {
		a.publicar(codigo, texto, op)
	}
}
