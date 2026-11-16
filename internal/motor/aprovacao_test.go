package motor

import (
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// v1.35 (SPEC-TECNICA-REAL-PELA-TELA-F1 §2.1): a recusa da entrada REAL por estado, com o motivo que
// a tela mostra; APROVADO passa.
func TestRecusaAprovacao(t *testing.T) {
	h := dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal, Conectores: []string{"PNT"}, Motivo: "conta em revisao",
		AprovacaoReal: dominio.AprovacaoReal{Corretora: 85, Conta: "4582351", GQConector: "PNT", GQCorretora: 85, GQConta: "1234567"}}
	casos := []struct {
		estado string
		existe bool
		codigo string
		motivo string
	}{
		{"", false, "REAL_SEM_APROVACAO", "REAL nao aprovado para este usuario"},
		{dominio.EstadoSemAprovacao, true, "REAL_SEM_APROVACAO", "REAL nao aprovado"},
		{dominio.EstadoAprovado, false, "REAL_SEM_APROVACAO", "REAL nao aprovado"}, // sem linha vale mais que o campo
		{dominio.EstadoBloqueado, true, "REAL_BLOQUEADO", "REAL bloqueado pelo admin: conta em revisao"},
		{dominio.EstadoRevogado, true, "REAL_REVOGADO", "nova aprovacao"},
		{dominio.EstadoContaDivergente, true, "REAL_CONTA_DIVERGENTE", "aprovada PNT / BTG (85) / conta 4582351, atual PNT / BTG (85) / conta 1234567"},
	}
	for _, c := range casos {
		x := h
		x.Estado = c.estado
		r := recusaAprovacao(x, c.existe)
		if r == nil || r.codigo != c.codigo || !strings.Contains(r.motivo, c.motivo) {
			t.Errorf("%q existe=%v: %+v", c.estado, c.existe, r)
		}
	}
	x := h
	x.Estado = dominio.EstadoAprovado
	if r := recusaAprovacao(x, true); r != nil {
		t.Fatalf("APROVADO recusado: %+v", r)
	}
}

// §2.1 item 3: o cadastro do GQ na entrada REAL — ilegível e diferente do aprovado são recusas com
// motivo próprio; sem aprovação (sem conta), só a leitura; igual passa.
func TestCadastroReal(t *testing.T) {
	h := dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal, Conectores: []string{"PNT"},
		AprovacaoReal: dominio.AprovacaoReal{Estado: dominio.EstadoAprovado, Corretora: 85, Conta: "4582351"}}
	if f := cadastroReal(h, true, `{"Robo":{"CORSeqId":85,"CCONroConta":"4582351"}}`); f != nil {
		t.Fatalf("igual: %+v", f)
	}
	if f := cadastroReal(h, true, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":"1234567"}}`); f == nil || f.Codigo != "REAL_CONTA_DIVERGENTE" || f.Alerta != "" {
		t.Fatalf("diferente: %+v", f)
	}
	if f := cadastroReal(h, true, `{"Robo":{"Conector":"FLEXSCAN","CORSeqId":85,"CCONroConta":"4582351"}}`); f == nil || f.Codigo != "REAL_CONTA_DIVERGENTE" {
		t.Fatalf("outro conector: %+v", f)
	}
	if f := cadastroReal(h, true, `{"Robo":`); f == nil || f.Codigo != "REAL_CADASTRO_ILEGIVEL" || !strings.Contains(f.Motivo, "cadastro ilegivel no GQ") {
		t.Fatalf("ilegível: %+v", f)
	}
	if f := cadastroReal(dominio.Habilitacao{}, false, `{"Robo":{"CORSeqId":85,"CCONroConta":"1"}}`); f != nil {
		t.Fatalf("sem aprovação: %+v", f)
	}
}

type notifAval struct{ textos []string }

func (n *notifAval) Notificar(codigo, texto string) { n.textos = append(n.textos, codigo+": "+texto) }

// §3.2: o avaliador com o repositório em memória — o GQ diferente grava CONTA_DIVERGENTE (uma vez, com
// o histórico) e abre o crítico com aprovado × atual; a recarga sem mudança não grava; o cadastro de
// volta grava APROVADO e resolve; o ilegível não transita e abre o próprio crítico; BLOQUEADO não
// alerta. A instância que hospeda não é a REAL: o texto leva [REAL].
func TestAvaliadorDoReal(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC))
	r := repo.NovaMemoria()
	n := &notifAval{}
	m := &Motor{amb: dominio.AmbienteSimulador, rel: rel, repo: r, aval: &avaliador{}, usuarios: novoCacheUsuarios()}
	m.al = alerta.Novo(n, nil, rel, 30*time.Second)
	aprovar := func(uid int, conta string, estado string) {
		t.Helper()
		if err := r.GravarHabilitacao(dominio.Habilitacao{UID: uid, Ambiente: dominio.AmbienteReal, Habilitado: estado == dominio.EstadoAprovado,
			QtdMaxOrdem: 1, PosicaoMax: 1, Conectores: []string{"PNT"}, Por: "admin:1450", Motivo: "fase 1", Em: rel.Agora(),
			AprovacaoReal: dominio.AprovacaoReal{Estado: estado, Corretora: 85, Conta: conta, Versao: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	aprovar(1450, "4582351", dominio.EstadoAprovado)
	aprovar(35, "7777777", dominio.EstadoBloqueado)
	gq := func(uid int, prd string) {
		t.Helper()
		if err := r.DefinirUsuario(uid, "prd", prd); err != nil {
			t.Fatal(err)
		}
	}
	gq(1450, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":"4582351"}}`)
	gq(35, `{"Robo":{"CORSeqId":85,"CCONroConta":"8888888"}}`)
	passe := func() { m.aplicarAvaliacao(m.passeAprovacoes(0, rel.Agora())) }
	estado := func(uid int) dominio.Habilitacao {
		h, _, _ := r.Habilitacao(uid, dominio.AmbienteReal)
		return h
	}

	passe()
	if h := estado(1450); h.Estado != dominio.EstadoAprovado || h.Versao != 1 {
		t.Fatalf("igual mudou: %+v", h)
	}
	if h := estado(35); h.Estado != dominio.EstadoBloqueado || h.GQConta != "8888888" || m.al.Ativo(chaveDivergente(35)) {
		t.Fatalf("BLOQUEADO: %+v %v", h, m.al.Ativos())
	}

	gq(1450, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":"1234567"}}`)
	rel.Avancar(30 * time.Second)
	passe()
	h := estado(1450)
	if h.Estado != dominio.EstadoContaDivergente || h.Habilitado || h.Versao != 2 || h.GQConta != "1234567" || h.Origem != dominio.OrigemServico {
		t.Fatalf("divergente: %+v", h)
	}
	if !m.al.Ativo(chaveDivergente(1450)) || len(n.textos) != 1 ||
		!strings.Contains(n.textos[0], "[REAL] uid 1450: cadastro do GQ diferente do aprovado. Aprovado PNT / BTG (85) / conta 4582351; atual PNT / BTG (85) / conta 1234567.") ||
		!strings.Contains(n.textos[0], "Sem posicao aberta.") {
		t.Fatalf("alerta: %v", n.textos)
	}
	passe()
	if h := estado(1450); h.Versao != 2 || len(n.textos) != 1 {
		t.Fatalf("a recarga sem mudança gravou ou alertou: %+v %v", h, n.textos)
	}
	if hist, _ := r.HistoricoHabilitacao(1450, dominio.AmbienteReal, 10); len(hist) != 2 || hist[0].Evento != dominio.EventoDivergencia {
		t.Fatalf("histórico: %+v", hist)
	}

	gq(1450, `{"Robo":`)
	passe()
	if h := estado(1450); h.Estado != dominio.EstadoContaDivergente || h.Versao != 2 || !m.al.Ativo(chaveIlegivel(1450)) {
		t.Fatalf("ilegível transitou ou não alertou: %+v %v", h, m.al.Ativos())
	}
	gq(1450, `{"Robo":{"CORSeqId":85,"CCONroConta":"4582351"}}`)
	passe()
	if h := estado(1450); h.Estado != dominio.EstadoAprovado || !h.Habilitado || h.Versao != 3 || h.GQConta != "" ||
		m.al.Ativo(chaveDivergente(1450)) || m.al.Ativo(chaveIlegivel(1450)) {
		t.Fatalf("voltou: %+v %v", h, m.al.Ativos())
	}
	if !strings.Contains(strings.Join(n.textos, "\n"), "RESOLVIDO: REAL_CONTA_DIVERGENTE: REAL_CONTA_DIVERGENTE:1450") {
		t.Fatalf("sem RESOLVIDO: %v", n.textos)
	}
}
