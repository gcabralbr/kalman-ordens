package aprovacao

import (
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

var (
	agora    = time.Date(2026, 11, 17, 21, 0, 0, 0, time.UTC)
	gq1450   = usuario.Cadastro{Conector: "PNT", Corretora: 85, Conta: "4582351"}
	inst     = Instancia{QtdOrdem: 1, PosicaoMax: 1}
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

// SPEC-TECNICA-REAL-PELA-TELA-F1 §5: a matriz das ações do admin — de que estado cada uma sai e para
// onde vai; o resto é TRANSICAO_INVALIDA. Sem linha conta como SEM_APROVACAO.
func TestMatrizDeTransicoes(t *testing.T) {
	estados := []string{dominio.EstadoSemAprovacao, dominio.EstadoAprovado, dominio.EstadoBloqueado, dominio.EstadoContaDivergente, dominio.EstadoRevogado}
	quer := map[string]map[string]string{ // ação → estado de antes → estado de depois ("" = recusa)
		AcaoAprovar:     {dominio.EstadoSemAprovacao: dominio.EstadoAprovado, dominio.EstadoRevogado: dominio.EstadoAprovado, dominio.EstadoContaDivergente: dominio.EstadoAprovado},
		AcaoBloquear:    {dominio.EstadoAprovado: dominio.EstadoBloqueado, dominio.EstadoContaDivergente: dominio.EstadoBloqueado},
		AcaoDesbloquear: {dominio.EstadoBloqueado: dominio.EstadoAprovado},
		AcaoRevogar:     {dominio.EstadoAprovado: dominio.EstadoRevogado, dominio.EstadoBloqueado: dominio.EstadoRevogado, dominio.EstadoContaDivergente: dominio.EstadoRevogado},
		AcaoLimites:     {dominio.EstadoAprovado: dominio.EstadoAprovado, dominio.EstadoBloqueado: dominio.EstadoBloqueado, dominio.EstadoContaDivergente: dominio.EstadoContaDivergente},
	}
	for _, acao := range Acoes {
		for _, e := range estados {
			antes := comEstado(e)
			p := Pedido{Acao: acao, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "teste", Versao: versao(3), Visto: &gq1450, QtdMax: 1, PosMax: 1}
			h, ev, r := Decidir(antes, true, p, gq1450, nil, inst, agora)
			want := quer[acao][e]
			switch {
			case want == "" && (r == nil || r.Codigo != "TRANSICAO_INVALIDA" || r.HTTP != 409 || !strings.Contains(r.Motivo, e)):
				t.Errorf("%s em %s: esperava TRANSICAO_INVALIDA, veio %+v %+v", acao, e, h, r)
			case want != "" && r != nil:
				t.Errorf("%s em %s: recusa %v", acao, e, r)
			case want != "" && (h.Estado != want || h.Habilitado != (want == dominio.EstadoAprovado) || h.Versao != 4 ||
				h.Por != "admin:1450" || h.Motivo != "teste" || !h.Em.Equal(agora) || h.Origem != dominio.OrigemTela || ev != eventos[acao]):
				t.Errorf("%s em %s: %+v evento %s", acao, e, h, ev)
			}
		}
		if got := AcoesPermitidas(dominio.EstadoBloqueado); strings.Join(got, ",") != "desbloquear,revogar,limites" {
			t.Errorf("ações em BLOQUEADO: %v", got)
		}
	}
	// sem linha = SEM_APROVACAO
	h, ev, r := Decidir(dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal}, false,
		Pedido{Acao: AcaoAprovar, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "fase 1", Versao: versao(0), Visto: &gq1450, QtdMax: 1, PosMax: 1},
		gq1450, nil, inst, agora)
	if r != nil || ev != dominio.EventoAprovar || h.Estado != dominio.EstadoAprovado || h.Conta != "4582351" || h.Corretora != 85 ||
		h.ConectorAprovado() != "PNT" || h.Versao != 1 || h.AprovadoPor != "admin:1450" || !h.AprovadoEm.Equal(agora) || h.AprovadoMotivo != "fase 1" {
		t.Fatalf("1ª aprovação: %+v %s %v", h, ev, r)
	}
	if r := (func() *Recusa {
		_, _, r := Decidir(dominio.Habilitacao{}, false, Pedido{Acao: AcaoRevogar, Por: "george", Origem: dominio.OrigemCLI, Motivo: "emergencia"}, gq1450, nil, inst, agora)
		return r
	})(); r == nil || r.Codigo != "TRANSICAO_INVALIDA" {
		t.Fatalf("revogar sem linha: %v", r)
	}
}

// As recusas que não são de estado: campos, versão, cadastro que mudou ou não serve, limites.
func TestRecusasDoAdmin(t *testing.T) {
	sem := dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal}
	base := Pedido{Acao: AcaoAprovar, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "fase 1", Versao: versao(0), Visto: &gq1450, QtdMax: 1, PosMax: 1}
	outro := usuario.Cadastro{Conector: "PNT", Corretora: 85, Conta: "1234567"}
	casos := []struct {
		nome   string
		mexe   func(p *Pedido)
		gq     usuario.Cadastro
		falha  *usuario.Falha
		inst   Instancia
		codigo string
		http   int
	}{
		{"ação desconhecida", func(p *Pedido) { p.Acao = "zerar" }, gq1450, nil, inst, "CAMPO_INVALIDO", 400},
		{"motivo curto", func(p *Pedido) { p.Motivo = " ok " }, gq1450, nil, inst, "CAMPO_INVALIDO", 400},
		{"motivo longo", func(p *Pedido) { p.Motivo = strings.Repeat("x", 201) }, gq1450, nil, inst, "CAMPO_INVALIDO", 400},
		{"sem por", func(p *Pedido) { p.Por = "" }, gq1450, nil, inst, "CAMPO_INVALIDO", 400},
		{"versão velha", func(p *Pedido) { p.Versao = versao(2) }, gq1450, nil, inst, "VERSAO_MUDOU", 409},
		{"sem visto", func(p *Pedido) { p.Visto = nil }, gq1450, nil, inst, "CAMPO_INVALIDO", 400},
		{"o GQ mudou depois de visto", func(p *Pedido) {}, outro, nil, inst, "CADASTRO_MUDOU", 409},
		{"cadastro que não serve", func(p *Pedido) {}, gq1450, &usuario.Falha{Codigo: "CONTA_AMBIENTE", Motivo: "conta de simulador 99999 no REAL"}, inst, "CADASTRO_INVALIDO", 422},
		{"limite zero", func(p *Pedido) { p.QtdMax = 0 }, gq1450, nil, inst, "CAMPO_INVALIDO", 400},
		{"acima da instância", func(p *Pedido) { p.PosMax = 2 }, gq1450, nil, inst, "LIMITE_ACIMA_DA_INSTANCIA", 422},
		{"instância sem limites", func(p *Pedido) {}, gq1450, nil, Instancia{}, "LIMITES_INSTANCIA_AUSENTES", 422},
	}
	for _, c := range casos {
		p := base
		c.mexe(&p)
		if _, _, r := Decidir(sem, false, p, c.gq, c.falha, c.inst, agora); r == nil || r.Codigo != c.codigo || r.HTTP != c.http {
			t.Errorf("%s: esperava %s/%d, veio %v", c.nome, c.codigo, c.http, r)
		}
	}
	// a revogação de emergência da CLI não confere a versão
	h, _, r := Decidir(aprovado, true, Pedido{Acao: AcaoRevogar, Por: "george", Origem: dominio.OrigemCLI, Motivo: "emergencia na tela"}, gq1450, nil, Instancia{}, agora)
	if r != nil || h.Estado != dominio.EstadoRevogado || h.Habilitado || h.Origem != dominio.OrigemCLI || h.Conta != "4582351" || h.AprovadoMotivo != "fase 1" {
		t.Fatalf("revogar pela CLI: %+v %v", h, r)
	}
	// limites acima da instância também na ação limites
	if _, _, r := Decidir(aprovado, true, Pedido{Acao: AcaoLimites, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "dois", QtdMax: 2, PosMax: 1}, gq1450, nil, inst, agora); r == nil || r.Codigo != "LIMITE_ACIMA_DA_INSTANCIA" {
		t.Fatalf("limites: %v", r)
	}
}

// Aprovar do CONTA_DIVERGENTE grava o cadastro novo; desbloquear com o GQ diferente volta divergente.
func TestAprovarNovoCadastroEDesbloquearDivergente(t *testing.T) {
	novo := usuario.Cadastro{Corretora: 85, Conta: "1234567"} // sem Robo.Conector: o PnT
	div := comEstado(dominio.EstadoContaDivergente)
	div.GQConector, div.GQCorretora, div.GQConta, div.DivergenteDesde = "PNT", 85, "1234567", agora.Add(-time.Hour)
	h, _, r := Decidir(div, true, Pedido{Acao: AcaoAprovar, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "conta nova conferida",
		Versao: versao(3), Visto: &usuario.Cadastro{Conector: "PNT", Corretora: 85, Conta: "1234567"}, QtdMax: 1, PosMax: 1}, novo, nil, inst, agora)
	if r != nil || h.Estado != dominio.EstadoAprovado || h.Conta != "1234567" || h.GQConta != "" || !h.DivergenteDesde.IsZero() || !h.Habilitado {
		t.Fatalf("aprovar o novo: %+v %v", h, r)
	}
	bloq := comEstado(dominio.EstadoBloqueado)
	h, ev, r := Decidir(bloq, true, Pedido{Acao: AcaoDesbloquear, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "volta"}, novo, nil, inst, agora)
	if r != nil || ev != dominio.EventoDesbloquear || h.Estado != dominio.EstadoContaDivergente || h.Habilitado || h.GQConta != "1234567" || !h.DivergenteDesde.Equal(agora) {
		t.Fatalf("desbloquear divergente: %+v %v", h, r)
	}
	// com o GQ ilegível, desbloqueia (a entrada recusa por REAL_CADASTRO_ILEGIVEL)
	if h, _, r := Decidir(bloq, true, Pedido{Acao: AcaoDesbloquear, Por: "admin:1450", Origem: dominio.OrigemTela, Motivo: "volta"}, usuario.Cadastro{}, &usuario.Falha{Codigo: "CADASTRO_ILEGIVEL"}, inst, agora); r != nil || h.Estado != dominio.EstadoAprovado {
		t.Fatalf("desbloquear com o GQ ilegível: %+v %v", h, r)
	}
}

// §3.2: a transição automática — divergir, mudar de novo, voltar; BLOQUEADO e REVOGADO só guardam o
// GQ visto; sem mudança, não grava; o conector ausente no GQ vale o aprovado.
func TestDecidirCadastro(t *testing.T) {
	outro := usuario.Cadastro{Conector: "PNT", Corretora: 85, Conta: "1234567"}
	if _, _, gravar := DecidirCadastro(aprovado, true, usuario.Cadastro{Corretora: 85, Conta: "4582351"}, agora); gravar {
		t.Fatal("conector ausente no GQ divergiu")
	}
	if _, _, gravar := DecidirCadastro(dominio.Habilitacao{}, false, outro, agora); gravar {
		t.Fatal("sem linha gravou")
	}
	if _, _, gravar := DecidirCadastro(comEstado(dominio.EstadoSemAprovacao), true, outro, agora); gravar {
		t.Fatal("SEM_APROVACAO gravou")
	}
	div, ev, gravar := DecidirCadastro(aprovado, true, outro, agora)
	if !gravar || ev != dominio.EventoDivergencia || div.Estado != dominio.EstadoContaDivergente || div.Habilitado || div.Conta != "4582351" ||
		div.GQConta != "1234567" || div.GQCorretora != 85 || !div.DivergenteDesde.Equal(agora) || div.Versao != 4 || div.Origem != dominio.OrigemServico ||
		div.Por != "servico" || !strings.Contains(div.Motivo, "1234567") {
		t.Fatalf("divergir: %+v %s", div, ev)
	}
	if _, _, gravar := DecidirCadastro(div, true, outro, agora.Add(30*time.Second)); gravar {
		t.Fatal("a recarga sem mudança gravou")
	}
	mais := usuario.Cadastro{Conector: "FLEXSCAN", Corretora: 85, Conta: "1234567"}
	div2, ev, gravar := DecidirCadastro(div, true, mais, agora.Add(time.Minute))
	if !gravar || ev != dominio.EventoDivergencia || div2.GQConector != "FLEXSCAN" || !div2.DivergenteDesde.Equal(agora) || div2.Versao != 5 {
		t.Fatalf("mudou de novo: %+v", div2)
	}
	if Campos(div2, mais)[0] != "conector" || len(Campos(div2, mais)) != 2 {
		t.Fatalf("campos: %v", Campos(div2, mais))
	}
	volta, ev, gravar := DecidirCadastro(div2, true, gq1450, agora.Add(2*time.Minute))
	if !gravar || ev != dominio.EventoVoltou || volta.Estado != dominio.EstadoAprovado || !volta.Habilitado || volta.GQConta != "" || !volta.DivergenteDesde.IsZero() {
		t.Fatalf("voltou: %+v %s", volta, ev)
	}
	for _, e := range []string{dominio.EstadoBloqueado, dominio.EstadoRevogado} {
		b, ev, gravar := DecidirCadastro(comEstado(e), true, outro, agora)
		if !gravar || ev != dominio.EventoDivergencia || b.Estado != e || b.Habilitado || b.GQConta != "1234567" {
			t.Fatalf("%s divergente: %+v", e, b)
		}
		if _, _, gravar := DecidirCadastro(b, true, outro, agora); gravar {
			t.Fatalf("%s: a recarga sem mudança gravou", e)
		}
		v, ev, gravar := DecidirCadastro(b, true, gq1450, agora)
		if !gravar || ev != dominio.EventoVoltou || v.Estado != e || v.GQConta != "" {
			t.Fatalf("%s de volta: %+v", e, v)
		}
	}
	if m := MotivoDivergente(aprovado, outro); m != "conta alterada no GQ (aprovada PNT / BTG (85) / conta 4582351, atual PNT / BTG (85) / conta 1234567): aguardando aprovacao" {
		t.Fatalf("motivo: %q", m)
	}
}
