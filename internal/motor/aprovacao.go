package motor

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/aprovacao"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// A aprovação do REAL (SPEC-TECNICA-REAL-PELA-TELA-F1, v1.35). A linha REAL da kalmanordemusuario é
// a aprovação do admin; a entrada REAL só passa com APROVADO e com o cadastro do GQ igual ao
// aprovado (§2). O avaliador (§3) compara o cadastro com o aprovado a cada CACHE_USUARIOS_S e a cada
// entrada que achou diferença, grava as transições automáticas (CONTA_DIVERGENTE e a volta) e emite
// os alertas REAL_CONTA_DIVERGENTE e REAL_CADASTRO_ILEGIVEL. Ele roda numa instância só (a
// principal), com ou sem a instância REAL: a aceitação "pela tela, fora do pregão" funciona com o
// REAL fora de AMBIENTES_PERMITIDOS.

// avaliador: o estado do avaliador na instância que o hospeda (confinado à goroutine dela, salvo mu).
type avaliador struct {
	mu       sync.Mutex // um passe por vez (os passes rodam fora da goroutine do motor)
	correndo bool
	prox     time.Time
}

// Os códigos de recusa por estado (§2.1).
const (
	recusaSemAprovacao     = "REAL_SEM_APROVACAO"
	recusaBloqueado        = "REAL_BLOQUEADO"
	recusaRevogado         = "REAL_REVOGADO"
	recusaContaDivergente  = "REAL_CONTA_DIVERGENTE"
	recusaCadastroIlegivel = "REAL_CADASTRO_ILEGIVEL"
)

// recusaAprovacao: a recusa da entrada REAL pelo estado da linha (nil = APROVADO).
func recusaAprovacao(h dominio.Habilitacao, existia bool) *recusa {
	estado := h.EstadoReal()
	if !existia {
		estado = dominio.EstadoSemAprovacao
	}
	switch estado {
	case dominio.EstadoAprovado:
		return nil
	case dominio.EstadoBloqueado:
		return simples(recusaBloqueado, "REAL bloqueado pelo admin: "+h.Motivo)
	case dominio.EstadoRevogado:
		return simples(recusaRevogado, "REAL revogado pelo admin: precisa de nova aprovacao")
	case dominio.EstadoContaDivergente:
		gq := usuario.Cadastro{Conector: h.GQConector, Corretora: h.GQCorretora, Conta: h.GQConta}
		return simples(recusaContaDivergente, aprovacao.MotivoDivergente(h, gq))
	}
	return simples(recusaSemAprovacao, "REAL nao aprovado para este usuario")
}

// avisarRecusaEstado: a entrada REAL recusada pelo estado. A tela desabilita o "Enviar ordens" nesses
// estados: a entrada aqui é um defeito do outro lado. Um aviso por uid, estado e versão.
func (m *Motor) avisarRecusaEstado(uid int, h dominio.Habilitacao, r *recusa) {
	if m.avisosEstado == nil {
		m.avisosEstado = map[string]bool{}
	}
	k := fmt.Sprintf("%d:%s:%d", uid, r.codigo, h.Versao)
	if m.avisosEstado[k] {
		return
	}
	m.avisosEstado[k] = true
	m.al.Emitir("REAL_RECUSA_ESTADO", "", fmt.Sprintf("entrada REAL do uid %d recusada (%s): %s", uid, r.codigo, r.motivo), 0)
}

// cadastroReal (§2.1 item 3): na entrada e no /destino do REAL, o cadastro do GQ conferido com o
// aprovado antes da resolução de sempre — ilegível ou diferente é recusa (sem alerta aqui: o
// avaliador alerta).
func cadastroReal(h dominio.Habilitacao, existia bool, texto string) *usuario.Falha {
	cad, f := usuario.LerCadastro(texto)
	if f != nil {
		return &usuario.Falha{Codigo: recusaCadastroIlegivel, Motivo: "cadastro ilegivel no GQ (" + f.Motivo + "): entradas recusadas ate o cadastro ser corrigido"}
	}
	if existia && h.Conta != "" && aprovacao.Diverge(h, cad) {
		return &usuario.Falha{Codigo: recusaContaDivergente, Motivo: aprovacao.MotivoDivergente(h, cad)}
	}
	return nil
}

// cutucarAvaliador: a entrada achou o cadastro diferente ou ilegível; o avaliador confere o uid já
// (grava a transição e alerta), sem esperar a próxima volta.
func (m *Motor) cutucarAvaliador(uid int) {
	host := m.avalHost
	if host == nil {
		return
	}
	agora := m.agora()
	host.emVoo()
	go func() {
		defer host.pousou()
		r := host.passeAprovacoes(uid, agora)
		host.entrar("outro", func() { host.aplicarAvaliacao(r) })
	}()
}

// emVoo e pousou: o passe que roda fora da goroutine conta como trabalho do motor até o resultado
// entrar na fila (Parado: os roteiros esperam o efeito, não um tempo).
func (m *Motor) emVoo()  { m.naFila.Add(1) }
func (m *Motor) pousou() { m.feitos.Add(1) }

// tickAvaliador: na instância que hospeda o avaliador, um passe a cada CACHE_USUARIOS_S, fora da
// goroutine do motor (lê o banco); o resultado volta pela fila.
func (m *Motor) tickAvaliador(agora time.Time) {
	a := m.aval
	if a == nil || a.correndo || agora.Before(a.prox) {
		return
	}
	a.correndo, a.prox = true, agora.Add(m.p.CacheUsuarios)
	m.emVoo()
	go func() {
		defer m.pousou()
		r := m.passeAprovacoes(0, agora)
		m.entrar("outro", func() {
			a.correndo = false
			m.aplicarAvaliacao(r)
		})
	}()
}

// avaliado: o que o passe viu de um uid (aplicado na goroutine do motor).
type avaliado struct {
	uid      int
	h        dominio.Habilitacao // a linha depois do passe
	cad      usuario.Cadastro
	ilegivel string // o motivo, se a prd não se lê
	evento   string // a transição gravada ("" = nenhuma)
	posicao  string // a posição aberta no REAL, para o texto do alerta
}

// resultadoPasse: os uids avaliados; completo = o passe de todos (só ele resolve o ilegível de quem
// saiu da lista).
type resultadoPasse struct {
	itens    []avaliado
	completo bool
	falhou   bool
}

// passeAprovacoes (fora da goroutine do motor): as linhas REAL com aprovação (uid 0 = todas), o
// cadastro do GQ de cada uma e a transição automática gravada (aprovacao.DecidirCadastro, com a
// linha travada). O GQ ilegível não transita (§3.1); o banco indisponível não transita nada.
func (m *Motor) passeAprovacoes(uid int, agora time.Time) resultadoPasse {
	a := m.aval
	if a == nil {
		return resultadoPasse{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	hs, err := m.repo.Habilitacoes()
	if err != nil {
		slog.Warn("avaliador do REAL: habilitações ilegíveis", "erro", err)
		return resultadoPasse{falhou: true}
	}
	var linhas []dominio.Habilitacao
	var uids []int
	for _, h := range hs {
		if dominio.AmbienteDe(h.Ambiente) != dominio.AmbienteReal || h.EstadoReal() == dominio.EstadoSemAprovacao || (uid != 0 && h.UID != uid) {
			continue
		}
		linhas = append(linhas, h)
		uids = append(uids, h.UID)
	}
	res := resultadoPasse{completo: uid == 0}
	if len(linhas) == 0 {
		return res
	}
	cfg, err := m.repo.ConfigUsuarios(uids)
	if err != nil {
		slog.Warn("avaliador do REAL: acusuario ilegível", "erro", err)
		return resultadoPasse{falhou: true}
	}
	for _, h := range linhas {
		it := avaliado{uid: h.UID, h: h}
		c, ok := cfg[h.UID]
		if !ok {
			it.ilegivel = "uid sem linha em acusuario"
			res.itens = append(res.itens, it)
			continue
		}
		cad, f := usuario.LerCadastro(c[1])
		if f != nil {
			it.ilegivel = f.Motivo
			res.itens = append(res.itens, it)
			continue
		}
		it.cad = cad
		t, err := m.repo.TransicaoReal(h.UID, func(antes dominio.Habilitacao, existia bool) (dominio.Habilitacao, string, bool, error) {
			depois, ev, gravar := aprovacao.DecidirCadastro(antes, existia, cad, agora)
			return depois, ev, gravar, nil
		})
		switch {
		case err != nil:
			slog.Warn("avaliador do REAL: transição não gravada", "uid", h.UID, "erro", err)
		case t.Gravou:
			it.h, it.evento = t.Depois, t.Depois.Estado
			slog.Warn("aprovação do REAL: transição pelo cadastro do GQ", "uid", h.UID, "de", t.Antes.EstadoReal(), "para", t.Depois.Estado, "versao", t.Depois.Versao)
		default:
			it.h = t.Antes
		}
		if it.h.EstadoReal() == dominio.EstadoContaDivergente {
			it.posicao = m.posicaoRealDo(h.UID)
		}
		res.itens = append(res.itens, it)
	}
	return res
}

// posicaoRealDo (fora da goroutine do motor): a posição aberta do uid na instância REAL, para o
// texto do alerta ("Sem posicao aberta." sem instância REAL ou sem operação viva).
func (m *Motor) posicaoRealDo(uid int) string {
	r := m.realInst
	if r == nil {
		return "Sem posicao aberta."
	}
	var partes []string
	r.Fazer(func() {
		for _, op := range r.ops {
			if op.UID != uid || op.Estado.Final() || op.posicao() == 0 {
				continue
			}
			stop := "sem stop viva"
			if p := op.viva(dominio.Protecao); p != nil {
				stop = fmt.Sprintf("stop %d viva", p.Stop)
			}
			partes = append(partes, fmt.Sprintf("%d %s na operacao %d, segue na conta %s (%s)", op.posicao(), op.Simbolo, op.OperacaoID, op.Conta, stop))
		}
	})
	if len(partes) == 0 {
		return "Sem posicao aberta."
	}
	sort.Strings(partes)
	return "Posicao aberta: " + strings.Join(partes, "; ") + "."
}

// prefixoReal: o alerta da aprovação é do REAL; na instância que já prefixa (a REAL), não repete.
func (m *Motor) prefixoReal() string {
	if m.amb == dominio.AmbienteReal {
		return ""
	}
	return "[REAL] "
}

func chaveDivergente(uid int) string { return "REAL_CONTA_DIVERGENTE:" + strconv.Itoa(uid) }
func chaveIlegivel(uid int) string   { return "REAL_CADASTRO_ILEGIVEL:" + strconv.Itoa(uid) }

// aplicarAvaliacao (goroutine do motor que hospeda o avaliador): os alertas pelo estado visto —
// idempotentes, valem também na partida para uma divergência gravada antes — e o cache das
// instâncias invalidado para os uids que mudaram.
func (m *Motor) aplicarAvaliacao(r resultadoPasse) {
	if r.falhou {
		return
	}
	vistos := map[int]bool{}
	for _, it := range r.itens {
		vistos[it.uid] = true
		if it.ilegivel != "" {
			m.al.Emitir("REAL_CADASTRO_ILEGIVEL", chaveIlegivel(it.uid), fmt.Sprintf("%suid %d: cadastro do GQ ilegivel (%s). Entradas REAL recusadas ate o cadastro ser corrigido; posicao aberta segue na conta da operacao.",
				m.prefixoReal(), it.uid, it.ilegivel), 0)
			continue
		}
		m.al.Resolver(chaveIlegivel(it.uid))
		if it.h.EstadoReal() == dominio.EstadoContaDivergente {
			texto := fmt.Sprintf("%suid %d: cadastro do GQ diferente do aprovado. Aprovado %s; atual %s. Entradas novas recusadas ate nova aprovacao (ou o cadastro voltar). %s",
				m.prefixoReal(), it.uid, aprovacao.TextoAprovado(it.h), aprovacao.TextoGQ(it.h, it.cad), it.posicao)
			if m.al.Ativo(chaveDivergente(it.uid)) {
				m.al.Atualizar(chaveDivergente(it.uid), texto)
			} else {
				m.al.Emitir("REAL_CONTA_DIVERGENTE", chaveDivergente(it.uid), texto, 0)
			}
		} else {
			m.al.Resolver(chaveDivergente(it.uid))
		}
		if it.evento != "" {
			m.invalidarAprovacao(it.uid)
		}
	}
	if r.completo {
		// quem saiu da lista (revogado sem divergência, sem aprovação): nada aberto
		for k, cod := range m.al.Ativos() {
			if cod != "REAL_CONTA_DIVERGENTE" && cod != "REAL_CADASTRO_ILEGIVEL" {
				continue
			}
			if uid, err := strconv.Atoi(k[strings.LastIndex(k, ":")+1:]); err == nil && !vistos[uid] {
				m.al.Resolver(k)
			}
		}
	}
}

// invalidarAprovacao: a linha REAL do uid mudou; o cache desta instância e o da REAL releem.
func (m *Motor) invalidarAprovacao(uid int) {
	m.invalidarUsuario(uid)
	if r := m.realInst; r != nil && r != m {
		r.entrar("outro", func() { r.invalidarUsuario(uid) })
	}
}
