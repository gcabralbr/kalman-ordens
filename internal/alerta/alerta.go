// Package alerta emite os alertas do catálogo docs/ALERTAS.md (Telegram e evento ALERTA).
// Os críticos repetem a cada ALERTA_REPETICAO_S até serem resolvidos — nos primeiros
// ALERTA_REPETICAO_CURTA_S; depois, a cada ALERTA_REPETICAO_LONGA_S (22/10: sem enxurrada numa falha
// longa) —, com "(continua desde HH:MM:SS)" (v1.32 §4); a resolução gera RESOLVIDO.
// v1.36: os "uma vez" (UmaVez) são guardados pela chave como um crítico (sem duplicar, com o
// RESOLVIDO), mas não repetem.
// Confinado à goroutine do motor.
package alerta

import (
	"fmt"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"log/slog"
	"sort"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// Catalogo: código → crítico. Fonte: docs/ALERTAS.md (alerta_test.go confere).
var Catalogo = map[string]bool{
	"SESSAO_CAIU": false, "SESSAO_VOLTOU": false, "SESSAO_REJEITOU_MENSAGEM": true, "MENSAGEM_ILEGIVEL": true,
	"SESSAO_PRESA_NO_PNT": true, "FIX_STORE_ILEGIVEL": true,
	"PROTECAO_EM_ERRO": true, "PROTECAO_EM_ERRO_PRESA": true, "SAIDA_EM_ERRO": true, "ENTRADA_EM_ERRO": false,
	"CONCILIACAO": false, "MASS_STATUS_REJEITADO": true, "DESCONHECIDA": false, "DIVERGENTE": true, "ORDEM_RECONSTITUIDA": true,
	"SEM_CONFIRMACAO": true, "PENDENTE_39A": false, "RECUSA_PNT": false, "RECUSA_FLEXSCAN": false, "CANCEL_STOP_REJEITADO": false,
	"CANCEL_SEM_RESPOSTA": true, "SEM_PROTECAO": true, "PROTECAO_REJEITADA": false, "PROTECAO_RECRIADA": true, "PROTECAO_INSTAVEL": true, "STOP_FURADO": true,
	"SAIDA_ESGOTOU": true, "SAIDA_SEM_PRECO": true, "SAIDA_SEM_FILL": true, "SAIDA_PRECO_DEGRADADO": false,
	"FILL_ORDEM_ENCERRADA": false, "ENTRADA_TARDIA": false, "SEM_POSICAO": false, "LADO_INCOMPATIVEL": false,
	"DUPLICADA_DIFERENTE": false, "LIMITE": false, "PARADA": false, "RETOMADA": false, "ZERAGEM": false,
	"SIMBOLO_DIVERGENTE_COM_POSICAO": true, "CONTRATO_VIGENTE": false, "SIMBOLO_INVALIDO": false,
	"USUARIO_CONFIG": false, "CONTA_AMBIENTE": true,
	"AMBIENTE_NAO_PERMITIDO": true, "AMBIENTE_PEDIDO_DIVERGENTE": true, "CONTA_ALTERADA_COM_POSICAO": true, "SAIDA_DIVERGENTE_NAO_EXECUTOU": true,
	"SAIDA_BLOQUEADA_PRECO_ANTIGO": true, "FIM_DIA_POSICAO": true, "RESUMO_DIA": false,
	"VIRADA_COM_OPERACAO": true, "ERRO_INTERNO": true, "RESOLVIDO": false,
	"PNT_BOLSA_INDISPONIVEL": true,
	"BANCO_QUARENTENA":       true, "PROTECAO_RESERVA_REPLACES": false, "TUNNEL_RECUSA_ENTRADA": false,
	"FONTE_DEGRADADA": true, "MERCADO_TRAVADO": true, "SAIDA_EM_LEILAO": true,
	// SPEC-FLEXSCAN-v1
	"FLEXSCAN_LOGIN": true, "FLEXSCAN_PERMISSAO": true, "FLEXSCAN_WEBHOOK_ATRASADO": false, "FLEXSCAN_WEBHOOK_INVALIDO": false,
	"FLEXSCAN_WEBHOOK_INCOERENTE": true, "FLEXSCAN_WEBHOOK_AUSENTE": true, "FLEXSCAN_WEBHOOK_SEM_CHEGAR": true, "FLEXSCAN_ESTADO_INESPERADO": true,
	// v1.28 §3: o conector da operação fora do ar
	"CONECTOR_FORA_COM_POSICAO": true, "OPERACAO_SUSPENSA": true,
	// v1.31: a boleta do admin no REAL
	"BOLETA_REAL": false,
	// v1.32: a consulta de uma operação de outro dia, não final no banco
	"OPERACAO_DE_OUTRO_DIA": false,
	// v1.32 §4: a linha única da abertura da Flexscan (login e recadastro que se resolveram sozinhos)
	"FLEXSCAN_ABERTURA": false,
	// v1.35 (SPEC-TECNICA-REAL-PELA-TELA-F1): a aprovação do REAL pela tela
	"REAL_RECUSA_ESTADO": false,
	// v1.36 (SPEC-TECNICA-v1.36-CONTA-LIVRE): o cadastro do GQ que o sistema não atende (uma vez, §3.3)
	// e a segunda entrada de um uid com uma operação aberta no REAL (defeito, uma vez por operação, §2)
	"REAL_CADASTRO_INUTILIZAVEL": false, "UID_COM_OPERACAO_ABERTA": false,
}

// UmaVez (v1.36 §3.3): os alertas que se guardam pela chave até o Resolver, como um crítico, mas
// saem uma vez só (o Tick não repete). Ficam em Ativos enquanto abertos.
var UmaVez = map[string]bool{"REAL_CADASTRO_INUTILIZAVEL": true}

// Notificador entrega o alerta para fora (Telegram). Não pode bloquear.
type Notificador interface {
	Notificar(codigo, texto string)
}

// Origem (v1.34, SPEC-ADMIN-CONECTORES-v1 §3.2): o conector (PNT, FLEXSCAN) e o canal (a corretora
// da ponte Flexscan no REAL) de quem emitiu. Vazia: o motor decide pelo código.
type Origem struct{ Conector, Canal string }

// Publicador grava o evento ALERTA para o gestor.
type Publicador func(codigo, texto string, operacaoID int64, o Origem)

type critico struct {
	codigo, texto string
	operacao      int64
	origem        Origem    // a de quem emitiu: o RESOLVIDO sai com ela
	desde         time.Time // a emissão: a repetição espaça depois de curtaAte
	proximo       time.Time
	umaVez        bool // v1.36: não repete
}

// Alertas.
type Alertas struct {
	notif     Notificador
	publicar  Publicador
	rel       relogio.Relogio
	repeticao time.Duration
	curtaAte  time.Duration // 0 = sempre a repeticao
	longa     time.Duration
	ativos    map[string]*critico
	origem    Origem // a de quem emite agora (ComOrigem)
}

// ComOrigem: as emissões e resoluções dentro de f saem com a origem o.
func (a *Alertas) ComOrigem(o Origem, f func()) {
	ant := a.origem
	a.origem = o
	defer func() { a.origem = ant }()
	f()
}

func Novo(n Notificador, p Publicador, rel relogio.Relogio, repeticao time.Duration) *Alertas {
	return &Alertas{notif: n, publicar: p, rel: rel, repeticao: repeticao, ativos: map[string]*critico{}}
}

// Espacar: repetição a cada `repeticao` só até curtaAte desde a emissão; depois, a cada longa.
func (a *Alertas) Espacar(curtaAte, longa time.Duration) { a.curtaAte, a.longa = curtaAte, longa }

// Emitir: alerta com código do catálogo. Se o código é crítico, chave identifica a condição
// (uma segunda emissão com a mesma chave não duplica) e ele repete até Resolver(chave).
func (a *Alertas) Emitir(codigo, chave, texto string, operacaoID int64) {
	texto = ASCII(texto)
	cr, ok := Catalogo[codigo]
	if !ok {
		slog.Error("alerta fora do catálogo", "codigo", codigo)
		cr = true
	}
	if cr || UmaVez[codigo] {
		if chave == "" {
			chave = codigo
		}
		if _, ja := a.ativos[chave]; ja {
			return
		}
		agora := a.rel.Agora()
		a.ativos[chave] = &critico{codigo: codigo, texto: texto, operacao: operacaoID, origem: a.origem, desde: agora, proximo: agora.Add(a.repeticao),
			umaVez: UmaVez[codigo]}
	}
	a.enviar(codigo, texto, operacaoID, a.origem)
}

// Atualizar (v1.32 §4): troca o texto de um crítico aberto (as repetições seguintes saem com ele,
// ex.: o login com o número de tentativas); sem efeito se a chave não está aberta.
func (a *Alertas) Atualizar(chave, texto string) {
	if c, ok := a.ativos[chave]; ok {
		c.texto = ASCII(texto)
	}
}

// Resolver encerra um crítico (e avisa com RESOLVIDO).
func (a *Alertas) Resolver(chave string) { a.ResolverPor(chave, "") }

// ResolverPor (v1.36): o Resolver com o porquê no fim do RESOLVIDO ("uid fora do APROVADO").
func (a *Alertas) ResolverPor(chave, porque string) {
	c, ok := a.ativos[chave]
	if !ok {
		return
	}
	delete(a.ativos, chave)
	texto := c.codigo // sem chave própria a chave é o código: "RESOLVIDO: FONTE_DEGRADADA", não repetido
	if chave != c.codigo {
		texto = fmt.Sprintf("%s: %s", c.codigo, chave)
	}
	if porque != "" {
		texto += " (" + ASCII(porque) + ")"
	}
	o := c.origem
	if o == (Origem{}) {
		o = a.origem
	}
	a.enviar("RESOLVIDO", texto, c.operacao, o)
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
		if c.umaVez || agora.Before(c.proximo) {
			continue
		}
		c.proximo = agora.Add(a.repeticao)
		if a.longa > 0 && agora.Sub(c.desde) >= a.curtaAte {
			c.proximo = agora.Add(a.longa)
		}
		a.notif.Notificar(c.codigo, c.texto+" (continua desde "+c.desde.In(relogio.Fuso).Format("15:04:05")+")")
	}
}

func (a *Alertas) enviar(codigo, texto string, op int64, o Origem) {
	metricas.Alertas.Inc(codigo) // só o que sai (um crítico já ativo não conta de novo)
	slog.Warn("ALERTA", "codigo", codigo, "texto", texto, "operacao", op)
	a.notif.Notificar(codigo, texto)
	if a.publicar != nil {
		a.publicar(codigo, texto, op, o)
	}
}
