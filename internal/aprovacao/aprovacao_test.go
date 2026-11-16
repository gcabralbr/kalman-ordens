package aprovacao

import (
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

var (
	agora = time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)
	// a linha do 1450 aprovada na v1.35 (com conta, conector e limites gravados)
	aprovado = dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal, Habilitado: true, QtdMaxOrdem: 1, PosicaoMax: 1,
		Conectores: []string{"PNT"}, AprovacaoReal: dominio.AprovacaoReal{Estado: dominio.EstadoAprovado, Corretora: 85, Conta: "4582351",
			Versao: 3, AprovadoPor: "admin:1450", AprovadoMotivo: "fase 1"}}
)

func comEstado(e string) dominio.Habilitacao {
	h := aprovado
	h.Estado, h.Habilitado = e, e == dominio.EstadoAprovado
	return h
}

func versao(n int) *int { return &n }

// SPEC-TECNICA-REAL-PELA-TELA-F1 §5 (v1.36: sem limites e sem CONTA_DIVERGENTE): a matriz das ações
// do admin — de que estado cada uma sai e para onde vai; o resto é TRANSICAO_INVALIDA. Sem linha
// conta como SEM_APROVACAO.
func TestMatrizDeTransicoes(t *testing.T) {
	estados := []string{dominio.EstadoSemAprovacao, dominio.EstadoAprovado, dominio.EstadoBloqueado, dominio.EstadoContaDivergente, dominio.EstadoRevogado}
	quer := map[string]map[string]string{ // ação → estado de antes → estado de depois ("" = recusa)
		AcaoAprovar:     {dominio.EstadoSemAprovacao: dominio.EstadoAprovado, dominio.EstadoRevogado: dominio.EstadoAprovado},
		AcaoBloquear:    {dominio.EstadoAprovado: dominio.EstadoBloqueado, dominio.EstadoContaDivergente: dominio.EstadoBloqueado},
		AcaoDesbloquear: {dominio.EstadoBloqueado: dominio.EstadoAprovado},
		AcaoRevogar: {dominio.EstadoAprovado: dominio.EstadoRevogado, dominio.EstadoBloqueado: dominio.EstadoRevogado,
			dominio.EstadoContaDivergente: dominio.EstadoRevogado},
	}
	// v1.36: o CONTA_DIVERGENTE que sobrar (depois de uma volta para a v1.35) vale como APROVADO
	for _, acao := range Acoes {
		for _, e := range estados {
			antes := comEstado(e)
			p := Pedido{Acao: acao, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "teste", Versao: versao(3)}
			h, ev, r := Decidir(antes, true, p, agora)
			want := quer[acao][e]
			switch {
			case want == "" && (r == nil || r.Codigo != "TRANSICAO_INVALIDA" || r.HTTP != 409 || !strings.Contains(r.Motivo, antes.EstadoVigente())):
				t.Errorf("%s em %s: esperava TRANSICAO_INVALIDA, veio %+v %+v", acao, e, h, r)
			case want != "" && r != nil:
				t.Errorf("%s em %s: recusa %v", acao, e, r)
			case want != "" && (h.Estado != want || h.Habilitado != (want == dominio.EstadoAprovado) || h.Versao != 4 ||
				h.Por != "admin:1450" || h.Motivo != "teste" || !h.Em.Equal(agora) || h.Origem != dominio.OrigemTela || ev != eventos[acao]):
				t.Errorf("%s em %s: %+v evento %s", acao, e, h, ev)
			}
		}
	}
	if got := AcoesPermitidas(dominio.EstadoBloqueado); strings.Join(got, ",") != "desbloquear,revogar" {
		t.Errorf("ações em BLOQUEADO: %v", got)
	}
	// sem linha = SEM_APROVACAO; a aprovação não grava conta, conector nem limites
	h, ev, r := Decidir(dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal}, false,
		Pedido{Acao: AcaoAprovar, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "fase 1", Versao: versao(0)}, agora)
	if r != nil || ev != dominio.EventoAprovar || h.Estado != dominio.EstadoAprovado || !h.Habilitado || h.Conta != "" || h.Corretora != 0 ||
		len(h.Conectores) != 0 || h.QtdMaxOrdem != 0 || h.PosicaoMax != 0 || h.Versao != 1 || h.AprovadoPor != "admin:1450" ||
		!h.AprovadoEm.Equal(agora) || h.AprovadoMotivo != "fase 1" {
		t.Fatalf("1ª aprovação: %+v %s %v", h, ev, r)
	}
	if _, _, r := Decidir(dominio.Habilitacao{}, false, Pedido{Acao: AcaoRevogar, Por: "george", Origem: dominio.OrigemCLI, Motivo: "emergencia"}, agora); r == nil || r.Codigo != "TRANSICAO_INVALIDA" {
		t.Fatalf("revogar sem linha: %v", r)
	}
}

// A reaprovação de uma linha da v1.35 limpa o que a v1.36 não usa (conta, conector, limites, o GQ
// visto); bloquear e revogar copiam.
func TestAprovarLimpaAsColunasDaV135(t *testing.T) {
	rev := comEstado(dominio.EstadoRevogado)
	rev.GQConector, rev.GQCorretora, rev.GQConta, rev.DivergenteDesde = "PNT", 85, "1234567", agora.Add(-time.Hour)
	h, _, r := Decidir(rev, true, Pedido{Acao: AcaoAprovar, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "de novo", Versao: versao(3)}, agora)
	if r != nil || h.Conta != "" || h.Corretora != 0 || len(h.Conectores) != 0 || h.QtdMaxOrdem != 0 || h.GQConta != "" || !h.DivergenteDesde.IsZero() {
		t.Fatalf("aprovar: %+v %v", h, r)
	}
	b, _, r := Decidir(aprovado, true, Pedido{Acao: AcaoBloquear, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "pausa", Versao: versao(3)}, agora)
	if r != nil || b.Conta != "4582351" || b.QtdMaxOrdem != 1 || b.ConectoresTexto() != aprovado.ConectoresTexto() {
		t.Fatalf("bloquear copia: %+v %v", b, r)
	}
}

// As recusas que não são de estado: campos e versão.
func TestRecusasDoAdmin(t *testing.T) {
	sem := dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal}
	base := Pedido{Acao: AcaoAprovar, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "fase 1", Versao: versao(0)}
	casos := []struct {
		nome   string
		mexe   func(p *Pedido)
		codigo string
		http   int
	}{
		{"ação desconhecida", func(p *Pedido) { p.Acao = "zerar" }, "CAMPO_INVALIDO", 400},
		{"a ação limites saiu (D7)", func(p *Pedido) { p.Acao = "limites" }, "CAMPO_INVALIDO", 400},
		{"motivo curto", func(p *Pedido) { p.Motivo = " ok " }, "CAMPO_INVALIDO", 400},
		{"motivo longo", func(p *Pedido) { p.Motivo = strings.Repeat("x", 201) }, "CAMPO_INVALIDO", 400},
		{"sem por", func(p *Pedido) { p.Por = "" }, "CAMPO_INVALIDO", 400},
		{"versão velha", func(p *Pedido) { p.Versao = versao(2) }, "VERSAO_MUDOU", 409},
	}
	for _, c := range casos {
		p := base
		c.mexe(&p)
		if _, _, r := Decidir(sem, false, p, agora); r == nil || r.Codigo != c.codigo || r.HTTP != c.http {
			t.Errorf("%s: esperava %s/%d, veio %v", c.nome, c.codigo, c.http, r)
		}
	}
	// a revogação de emergência da CLI não confere a versão
	h, _, r := Decidir(aprovado, true, Pedido{Acao: AcaoRevogar, Por: "george", Origem: dominio.OrigemCLI, Motivo: "emergencia na tela"}, agora)
	if r != nil || h.Estado != dominio.EstadoRevogado || h.Habilitado || h.Origem != dominio.OrigemCLI || h.AprovadoMotivo != "fase 1" {
		t.Fatalf("revogar pela CLI: %+v %v", h, r)
	}
}
