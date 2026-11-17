package motor

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// A aprovação do REAL (SPEC-TECNICA-REAL-PELA-TELA-F1, v1.35; SPEC-TECNICA-v1.36-CONTA-LIVRE). A
// linha REAL da kalmanordemusuario é a aprovação do admin: desde a v1.36, só "este uid pode usar o
// REAL", sem conta, sem conector e sem limites. Conta, corretora e conector vêm do cadastro do GQ a
// cada entrada (usuario.CadastroReal, a regra única do §3.1). O verificador (§3.3) lê o cadastro dos
// uids APROVADO a cada CACHE_USUARIOS_S e emite o REAL_CADASTRO_INUTILIZAVEL (uma vez) e o RESOLVIDO;
// não grava nada. Ele roda na instância principal e só com a instância REAL na máquina.

// avaliador: o estado do verificador na instância que o hospeda (confinado à goroutine dela, salvo mu).
type avaliador struct {
	mu       sync.Mutex // um passe por vez (os passes rodam fora da goroutine do motor)
	correndo bool
	prox     time.Time
	partida  bool // o primeiro passe completo já aplicado (os alertas dele são "vistos na partida")
	// os testes de unidade (sem a instância REAL rodando): as regras e a posição; nil = as dela
	regras  func() usuario.RegrasReal
	posicao func(uid int) string
}

// Os códigos de recusa do REAL (§3.2 e §2).
const (
	recusaSemAprovacao     = "REAL_SEM_APROVACAO"
	recusaBloqueado        = "REAL_BLOQUEADO"
	recusaRevogado         = "REAL_REVOGADO"
	recusaInutilizavel     = "REAL_CADASTRO_INUTILIZAVEL"
	recusaOperacaoAberta   = "UID_COM_OPERACAO_ABERTA"
	alertaInutilizavel     = recusaInutilizavel
	prefixoMotivoCadastro  = "cadastro do GQ nao utilizavel: "
	motivoForaDoAprovado   = "uid fora do APROVADO"
	textoPosicaoDaOperacao = "Posicao aberta segue na conta da operacao."
)

// recusaAprovacao: a recusa da entrada REAL pelo estado da linha (nil = APROVADO).
func recusaAprovacao(h dominio.Habilitacao, existia bool) *recusa {
	estado := h.EstadoVigente()
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

// regrasReal (§3.1): o que esta instância atende no REAL — as sessões, as corretoras declaradas e
// as contas de cada guarda-chuva. Só leitura de campos fixos depois da montagem (vale fora da
// goroutine do motor).
func (m *Motor) regrasReal() usuario.RegrasReal {
	if m.amb != dominio.AmbienteReal && m.rgRealEnv != nil {
		// a instância que não é a REAL (o /destino REAL na principal, sem a instância REAL): pelo env
		return *m.rgRealEnv
	}
	rg := usuario.RegrasReal{PnT: !m.p.PnTDesligado, Flexscan: m.p.Flexscan && m.temFlexscan(), Corretoras: m.corretorasDeclaradas(),
		ContasHML: m.p.ContasHML, ContasHMLFlx: m.p.ContasHMLFlx}
	if m.porCorretora() {
		rg.Contas = map[int][]string{}
		for _, c := range m.flxs {
			rg.Contas[c.codigo] = c.contas
		}
	}
	return rg
}

// falhaCadastro: a falha do cadastro (a causa no Codigo) como a recusa da entrada e do /destino.
func falhaCadastro(f *usuario.Falha) *usuario.Falha {
	return &usuario.Falha{Codigo: recusaInutilizavel, Motivo: prefixoMotivoCadastro + f.Motivo, Causa: f.Codigo}
}

// cutucarAvaliador: a entrada achou o cadastro inutilizável, ou o admin mudou a aprovação; o
// verificador confere o uid já (o alerta ou o RESOLVIDO), sem esperar a próxima volta.
func (m *Motor) cutucarAvaliador(uid int) {
	host := m.avalHost
	if host == nil || host.aval == nil || (host.realInst == nil && host.aval.regras == nil) {
		return
	}
	host.emVoo()
	go func() {
		defer host.pousou()
		r := host.passeAprovacoes(uid)
		host.entrar("outro", func() { host.aplicarAvaliacao(r) })
	}()
}

// emVoo e pousou: o passe que roda fora da goroutine conta como trabalho do motor até o resultado
// entrar na fila (Parado: os roteiros esperam o efeito, não um tempo).
func (m *Motor) emVoo()  { m.naFila.Add(1) }
func (m *Motor) pousou() { m.feitos.Add(1) }

// tickAvaliador: na instância que hospeda o verificador, com a instância REAL na máquina, um passe a
// cada CACHE_USUARIOS_S, fora da goroutine do motor (lê o banco); o resultado volta pela fila.
func (m *Motor) tickAvaliador(agora time.Time) {
	a := m.aval
	if a == nil || (m.realInst == nil && a.regras == nil) || a.correndo || agora.Before(a.prox) {
		return
	}
	a.correndo, a.prox = true, agora.Add(m.p.CacheUsuarios)
	m.emVoo()
	go func() {
		defer m.pousou()
		r := m.passeAprovacoes(0)
		m.entrar("outro", func() {
			a.correndo = false
			m.aplicarAvaliacao(r)
		})
	}()
}

// avaliado: o que o passe viu de um uid APROVADO (aplicado na goroutine do motor).
type avaliado struct {
	uid     int
	falha   *usuario.Falha // a causa no Codigo; nil = utilizável
	posicao string         // a posição aberta no REAL, para o texto do alerta
}

// resultadoPasse: os uids APROVADO avaliados; uid = o do passe de um uid só (0 = todos).
type resultadoPasse struct {
	itens  []avaliado
	uid    int
	falhou bool
}

// passeAprovacoes (fora da goroutine do motor): as linhas REAL em APROVADO (uid 0 = todas) e o
// cadastro do GQ de cada uma, pela regra da entrada (usuario.CadastroReal com as regras da
// instância REAL). Só lê. O banco indisponível não muda nenhum alerta.
func (m *Motor) passeAprovacoes(uid int) resultadoPasse {
	a := m.aval
	if a == nil || (m.realInst == nil && a.regras == nil) {
		return resultadoPasse{falhou: true}
	}
	regras, posicao := a.regras, a.posicao
	if regras == nil {
		regras = m.realInst.regrasReal
	}
	if posicao == nil {
		posicao = m.posicaoRealDo
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	hs, err := m.repo.Habilitacoes()
	if err != nil {
		slog.Warn("verificador do REAL: habilitações ilegíveis", "erro", err)
		return resultadoPasse{falhou: true}
	}
	var uids []int
	for _, h := range hs {
		if dominio.AmbienteDe(h.Ambiente) != dominio.AmbienteReal || h.EstadoVigente() != dominio.EstadoAprovado || (uid != 0 && h.UID != uid) {
			continue
		}
		uids = append(uids, h.UID)
	}
	res := resultadoPasse{uid: uid}
	if len(uids) == 0 {
		return res
	}
	cfg, err := m.repo.ConfigUsuarios(uids)
	if err != nil {
		slog.Warn("verificador do REAL: acusuario ilegível", "erro", err)
		return resultadoPasse{falhou: true}
	}
	rg := regras()
	for _, u := range uids {
		it := avaliado{uid: u}
		if c, ok := cfg[u]; !ok {
			it.falha = &usuario.Falha{Codigo: usuario.CausaSemCadastro, Motivo: "uid sem linha em acusuario"}
		} else if _, f := usuario.CadastroReal(c[1], rg); f != nil {
			it.falha = f
		}
		if it.falha != nil {
			it.posicao = posicao(u)
		}
		res.itens = append(res.itens, it)
	}
	return res
}

// posicaoRealDo (fora da goroutine do motor): a posição aberta do uid na instância REAL, para o
// texto do alerta ("" sem operação viva com posição).
func (m *Motor) posicaoRealDo(uid int) string {
	r := m.realInst
	if r == nil {
		return ""
	}
	var partes []string
	r.Fazer(func() {
		for _, op := range r.ops {
			if op.UID != uid || op.Estado.Final() || op.posicao() == 0 {
				continue
			}
			partes = append(partes, fmt.Sprintf("%d %s na operacao %d, conta %s", op.posicao(), op.Simbolo, op.OperacaoID, op.Conta))
		}
	})
	if len(partes) == 0 {
		return ""
	}
	sort.Strings(partes)
	return " " + textoPosicaoDaOperacao + " (" + strings.Join(partes, "; ") + ")"
}

// prefixoReal: o alerta da aprovação é do REAL; na instância que já prefixa (a REAL), não repete.
func (m *Motor) prefixoReal() string {
	if m.amb == dominio.AmbienteReal {
		return ""
	}
	return "[REAL] "
}

func chaveInutilizavel(uid int) string { return alertaInutilizavel + ":" + strconv.Itoa(uid) }

// aplicarAvaliacao (goroutine do motor que hospeda o verificador): o REAL_CADASTRO_INUTILIZAVEL é
// "uma vez" (alerta.UmaVez): a 1ª emissão sai, as seguintes só atualizam o texto guardado; o
// cadastro de volta, ou o uid fora do APROVADO, gera o RESOLVIDO. Um alerta aberto depois de um
// restart sai uma vez de novo, com "(visto na partida)".
func (m *Motor) aplicarAvaliacao(r resultadoPasse) {
	if r.falhou {
		return
	}
	a := m.aval
	partida := a != nil && !a.partida && r.uid == 0
	vistos := map[int]bool{}
	for _, it := range r.itens {
		vistos[it.uid] = true
		k := chaveInutilizavel(it.uid)
		if it.falha == nil {
			m.al.Resolver(k)
			continue
		}
		texto := fmt.Sprintf("%suid %d: %s%s (%s). Entradas REAL recusadas ate o cadastro ser corrigido.%s",
			m.prefixoReal(), it.uid, prefixoMotivoCadastro, it.falha.Motivo, it.falha.Codigo, it.posicao)
		if m.al.Ativo(k) {
			m.al.Atualizar(k, texto)
			continue
		}
		if partida {
			texto += " (visto na partida)"
		}
		m.al.Emitir(alertaInutilizavel, k, texto, 0)
	}
	if partida {
		a.partida = true
	}
	// quem saiu do APROVADO (bloqueado, revogado) com o alerta aberto: RESOLVIDO com o porquê
	for k, cod := range m.al.Ativos() {
		if cod != alertaInutilizavel {
			continue
		}
		u, err := strconv.Atoi(k[strings.LastIndex(k, ":")+1:])
		if err != nil || vistos[u] || (r.uid != 0 && u != r.uid) {
			continue
		}
		m.al.ResolverPor(k, motivoForaDoAprovado)
	}
}
