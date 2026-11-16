package motor

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// v1.35 (SPEC-TECNICA-REAL-PELA-TELA-F1 §2.1): a recusa da entrada REAL por estado, com o motivo que
// a tela mostra; APROVADO passa. v1.36: o CONTA_DIVERGENTE que sobrar vale como APROVADO.
func TestRecusaAprovacao(t *testing.T) {
	h := dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteReal, Motivo: "conta em revisao"}
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
	}
	for _, c := range casos {
		x := h
		x.Estado = c.estado
		r := recusaAprovacao(x, c.existe)
		if r == nil || r.codigo != c.codigo || !strings.Contains(r.motivo, c.motivo) {
			t.Errorf("%q existe=%v: %+v", c.estado, c.existe, r)
		}
	}
	for _, e := range []string{dominio.EstadoAprovado, dominio.EstadoContaDivergente} {
		x := h
		x.Estado = e
		if r := recusaAprovacao(x, true); r != nil {
			t.Fatalf("%s recusado: %+v", e, r)
		}
	}
}

type notifAval struct{ textos []string }

func (n *notifAval) Notificar(codigo, texto string) { n.textos = append(n.textos, codigo+": "+texto) }

// v1.36 §3.3: o verificador com o repositório em memória. Só lê (nenhuma linha muda); só olha os
// APROVADO (o CONTA_DIVERGENTE que sobrar conta como APROVADO); trocar a conta não alerta; o cadastro
// inutilizável alerta uma vez (o 1º passe, "visto na partida"), não repete, a causa nova só atualiza
// o texto; a volta e o uid fora do APROVADO dão o RESOLVIDO. A instância que hospeda não é a REAL: o
// texto leva [REAL].
func TestVerificadorDoReal(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC))
	r := repo.NovaMemoria()
	n := &notifAval{}
	pos := map[int]string{}
	m := &Motor{amb: dominio.AmbienteSimulador, rel: rel, repo: r, usuarios: novoCacheUsuarios(),
		aval: &avaliador{regras: func() usuario.RegrasReal { return usuario.RegrasReal{PnT: true} }, posicao: func(uid int) string { return pos[uid] }}}
	m.al = alerta.Novo(n, nil, rel, 30*time.Second)
	m.al.Espacar(2*time.Minute, 5*time.Minute)
	gravar := func(uid int, estado string) {
		t.Helper()
		if err := r.GravarHabilitacao(dominio.Habilitacao{UID: uid, Ambiente: dominio.AmbienteReal, Habilitado: estado == dominio.EstadoAprovado,
			Por: "admin:1450", Motivo: "fase 1", Em: rel.Agora(), AprovacaoReal: dominio.AprovacaoReal{Estado: estado, Versao: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	gq := func(uid int, prd string) {
		t.Helper()
		if err := r.DefinirUsuario(uid, "prd", prd); err != nil {
			t.Fatal(err)
		}
	}
	gravar(1450, dominio.EstadoAprovado)
	gravar(35, dominio.EstadoBloqueado)
	gravar(36, dominio.EstadoContaDivergente)
	gq(1450, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":"4582351"}}`)
	gq(35, `{"Robo":{"Conector":"PNT","CORSeqId":107,"CCONroConta":"1"}}`) // bloqueado: não alerta
	gq(36, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":"36"}}`)
	passe := func() {
		for i := 0; i < 10; i++ {
			rel.Avancar(30 * time.Second)
			m.al.Tick()
		}
		m.aplicarAvaliacao(m.passeAprovacoes(0))
	}
	versoes := func() string {
		var v []string
		for _, u := range []int{1450, 35, 36} {
			h, _, _ := r.Habilitacao(u, dominio.AmbienteReal)
			v = append(v, h.Estado+"/"+strconv.Itoa(h.Versao))
		}
		return strings.Join(v, " ")
	}
	antes := versoes()

	passe()
	if len(n.textos) != 0 {
		t.Fatalf("utilizáveis alertaram: %v", n.textos)
	}
	// a troca de conta não alerta (D6)
	gq(1450, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":"1234567"}}`)
	passe()
	if len(n.textos) != 0 {
		t.Fatalf("troca de conta alertou: %v", n.textos)
	}
	// corretora 107: uma vez, com a posição; não repete
	gq(1450, `{"Robo":{"Conector":"PNT","CORSeqId":107,"CCONroConta":"1234567"}}`)
	pos[1450] = " Posicao aberta segue na conta da operacao. (1 WINZ26 na operacao 9, conta 4582351)"
	passe()
	passe()
	if len(n.textos) != 1 || !strings.Contains(n.textos[0], "REAL_CADASTRO_INUTILIZAVEL: [REAL] uid 1450: cadastro do GQ nao utilizavel: corretora 107 nao atendida pelo PnT (so BTG, 85) (CORRETORA_NAO_ATENDIDA).") ||
		!strings.Contains(n.textos[0], "conta 4582351)") || strings.Contains(n.textos[0], "partida") {
		t.Fatalf("alerta: %v", n.textos)
	}
	// a causa muda: nada sai, o texto guardado muda
	gq(1450, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":""}}`)
	passe()
	if len(n.textos) != 1 || !m.al.Ativo(chaveInutilizavel(1450)) {
		t.Fatalf("a causa nova alertou: %v", n.textos)
	}
	// volta: RESOLVIDO
	gq(1450, `{"Robo":{"Conector":"PNT","CORSeqId":85,"CCONroConta":"4582351"}}`)
	passe()
	if len(n.textos) != 2 || n.textos[1] != "RESOLVIDO: REAL_CADASTRO_INUTILIZAVEL: REAL_CADASTRO_INUTILIZAVEL:1450" {
		t.Fatalf("volta: %v", n.textos)
	}
	// o 36 (CONTA_DIVERGENTE que sobrou, vale como APROVADO) fica inutilizável; depois é bloqueado
	gq(36, `{"Robo":{"CORSeqId":85,"CCONroConta":"36"}}`)
	passe()
	if len(n.textos) != 3 || !strings.Contains(n.textos[2], "uid 36: cadastro do GQ nao utilizavel: sem Robo.Conector (CONECTOR_NAO_ATENDIDO)") {
		t.Fatalf("36: %v", n.textos)
	}
	gravar(36, dominio.EstadoBloqueado)
	passe()
	if len(n.textos) != 4 || n.textos[3] != "RESOLVIDO: REAL_CADASTRO_INUTILIZAVEL: REAL_CADASTRO_INUTILIZAVEL:36 (uid fora do APROVADO)" {
		t.Fatalf("fora do APROVADO: %v", n.textos)
	}
	if v := versoes(); v != strings.Replace(antes, "CONTA_DIVERGENTE/1", "BLOQUEADO/1", 1) {
		t.Fatalf("o verificador gravou: antes %s, depois %s", antes, v)
	}
	// um restart com o cadastro ainda ruim: uma vez de novo, "visto na partida"
	m2 := &Motor{amb: dominio.AmbienteSimulador, rel: rel, repo: r, usuarios: novoCacheUsuarios(), aval: &avaliador{regras: m.aval.regras, posicao: m.aval.posicao}}
	m2.al = alerta.Novo(n, nil, rel, 30*time.Second)
	gq(1450, `{"Robo":`)
	m2.aplicarAvaliacao(m2.passeAprovacoes(0))
	m2.aplicarAvaliacao(m2.passeAprovacoes(0))
	if len(n.textos) != 5 || !strings.Contains(n.textos[4], "cadastro ilegivel") || !strings.HasSuffix(n.textos[4], "(visto na partida)") {
		t.Fatalf("partida: %v", n.textos)
	}
}

// §5.5: os estados do cartão que o roteiro 134 não alcança — DESPROTEGIDA (posição sem 1014 e sem
// saída: a recriação é na mesma volta, então só aqui), SAINDO pela operação em CANCELANDO_PROTECAO,
// e a 1014 disparada (a filha no mercado) ainda VIVA.
func TestStopCorretoraSemStop(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC))
	m := &Motor{rel: rel}
	entrada := &ordem{Ordem: dominio.Ordem{Papel: dominio.Entrada, Lado: dominio.Compra, Qtd: 1, QtdExec: 1, Estado: dominio.Executada}}
	op := &operacao{ordens: []*ordem{entrada}}
	op.Estado = dominio.PosicionadaSemProtecao
	op.semProtecaoDesde = rel.Agora()
	r := m.stopCorretora(op)
	if r["estado"] != "DESPROTEGIDA" || r["desde"] != "2026-11-17T10:00:00.000-03:00" || r["clordid"] != nil || r["preco_stop"] != nil {
		t.Fatalf("DESPROTEGIDA: %v", r)
	}
	op.Estado = dominio.CancelandoProtecao
	if r := m.stopCorretora(op); r["estado"] != "SAINDO" || r["desde"] != nil {
		t.Fatalf("SAINDO: %v", r)
	}
	op.Estado = dominio.Protegida
	p := &ordem{Ordem: dominio.Ordem{Papel: dominio.Protecao, Lado: dominio.Venda, Qtd: 1, Estado: dominio.Ativa, ClOrdAtual: "KM-P-1-1", Stop: 129510, StopLim: 129410}}
	p.disparadaEm = rel.Agora()
	op.ordens = append(op.ordens, p)
	if r := m.stopCorretora(op); r["estado"] != "VIVA" || r["disparada"] != true || r["preco_stop"] != int64(129510) || r["orderid37"] != nil {
		t.Fatalf("disparada: %v", r)
	}
}

// v1.36 §2: no REAL, uma operação aberta por uid (qualquer módulo); a segunda entrada recusa com
// UID_COM_OPERACAO_ABERTA, alerta uma vez por operação aberta e não conta para a Parada das recusas
// (simples, não limite). Operação final não conta; outro uid passa; o SIMULADOR não tem a trava.
func TestOperacaoAbertaPorUID(t *testing.T) {
	rel := relogio.NovoControlado(time.Date(2026, 11, 17, 13, 0, 0, 0, time.UTC))
	n := &notifAval{}
	m := &Motor{amb: dominio.AmbienteReal, rel: rel}
	m.al = alerta.Novo(n, nil, rel, 30*time.Second)
	op := func(id int64, uid int, opid int64, e dominio.EstadoOperacao) *operacao {
		o := &operacao{}
		o.ID, o.UID, o.OperacaoID, o.Modulo, o.Estado, o.CriadaEm = id, uid, opid, "KALMAN", e, rel.Agora()
		return o
	}
	m.ops = []*operacao{op(1, 1450, 8, dominio.Zerada), op(2, 1450, 9, dominio.Protegida)}
	r := m.recusaOperacaoAberta(1450)
	if r == nil || r.codigo != "UID_COM_OPERACAO_ABERTA" || r.porLimite || !strings.Contains(r.motivo, "operacao 9 aberta") {
		t.Fatalf("recusa: %+v", r)
	}
	m.recusaOperacaoAberta(1450)
	if len(n.textos) != 1 || !strings.HasPrefix(n.textos[0], "UID_COM_OPERACAO_ABERTA: defeito: entrada REAL recusada: uid 1450 ja tem a operacao 9 aberta") {
		t.Fatalf("alerta uma vez: %v", n.textos)
	}
	if r := m.recusaOperacaoAberta(35); r != nil {
		t.Fatalf("outro uid: %+v", r)
	}
	m.ops[1].Estado = dominio.Zerada
	m.ops = append(m.ops, op(3, 1450, 10, dominio.Entrando))
	if m.recusaOperacaoAberta(1450) == nil || len(n.textos) != 2 {
		t.Fatalf("outra operação aberta alerta de novo: %v", n.textos)
	}
	m.ops[2].Estado = dominio.EncerradaSemEntrada
	if r := m.recusaOperacaoAberta(1450); r != nil {
		t.Fatalf("todas finais: %+v", r)
	}
	m.amb = dominio.AmbienteSimulador
	m.ops[2].Estado = dominio.Protegida
	if r := m.recusaOperacaoAberta(1450); r != nil {
		t.Fatalf("SIMULADOR: %+v", r)
	}
}
