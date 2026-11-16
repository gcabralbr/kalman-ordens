package motor

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/aprovacao"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// As rotas da aprovação do REAL (SPEC-TECNICA-REAL-PELA-TELA-F1 §5, o contrato das telas): a ficha,
// a lista, as ações do admin e a stop na corretora. Funcionam com ou sem a instância REAL: o banco é
// lido e gravado aqui (na goroutine do HTTP, com a linha travada pelo repositório); os motores só
// invalidam o cache, publicam o ADMIN_ACAO e o avaliador alerta.

// PedidoAcaoReal: o corpo do POST /usuarios/{uid}/real/{acao}.
type PedidoAcaoReal struct {
	Por    *int   `json:"por"`
	Motivo string `json:"motivo"`
	Versao *int   `json:"versao"`
	Visto  *visto `json:"visto"`
	QtdMax *int   `json:"qtd_max_ordem"`
	PosMax *int   `json:"posicao_max"`
}

type visto struct {
	Conector  *string `json:"conector"`
	Corretora int     `json:"corretora_codigo"`
	Conta     string  `json:"conta"`
}

// ErrIndisponivel: o banco (do serviço ou do GQ) não respondeu (503).
var ErrIndisponivel = errors.New("CONFIG_USUARIO_INDISPONIVEL")

const histPadrao, histMax = 20, 200

func (s *Servico) agora() time.Time { return s.principal.agora() }

// gqDoReal: o cadastro do GQ do uid relido agora, para a ficha e o aprovar: os campos que se leem e
// se ele serve para o REAL (resolvido como a entrada resolveria, com o conector do GQ ou o
// aprovado). achou=false: uid sem linha no GQ.
func (s *Servico) gqDoReal(uid int, h dominio.Habilitacao) (cad usuario.Cadastro, achou bool, falha *usuario.Falha, err error) {
	_, prd, achou, err := s.Repo().ConfigUsuario(uid)
	if err != nil || !achou {
		return cad, achou, &usuario.Falha{Codigo: "CADASTRO_INVALIDO", Motivo: fmt.Sprintf("uid %d sem linha em acusuario", uid)}, err
	}
	cad, f := usuario.LerCadastro(prd)
	if f != nil {
		return cad, true, &usuario.Falha{Codigo: recusaCadastroIlegivel, Motivo: f.Motivo}, nil
	}
	rg := usuario.Regras{Ambiente: dominio.AmbienteReal, ContasHML: s.principal.p.ContasHML, ContasHMLFlx: s.principal.p.ContasHMLFlx,
		Flexscan: true, Corretoras: s.corretorasReal()}
	if _, f := usuario.ResolverCom(rg, prd, []string{aprovacao.ConectorDo(cad, h.ConectorAprovado())}); f != nil {
		return cad, true, &usuario.Falha{Codigo: "CADASTRO_INVALIDO", Motivo: f.Motivo}, nil
	}
	return cad, true, nil, nil
}

// corretorasReal: as corretoras declaradas no REAL (REAL_FLEXSCAN_CORRETORAS), se a instância existe.
func (s *Servico) corretorasReal() map[int]string {
	if r := s.motores[dominio.AmbienteReal]; r != nil {
		return r.corretorasDeclaradas()
	}
	return nil
}

func nomeCorretoraOuNulo(c int) any {
	if c == 0 {
		return nil
	}
	if n := usuario.Corretora(c); n != "" {
		return n
	}
	return nil
}

func intOuNulo(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

// FichaReal: GET /usuarios/{uid}/real (§5.1).
func (s *Servico) FichaReal(uid, nHist int) (Resposta, error) {
	h, existia, err := s.Repo().Habilitacao(uid, dominio.AmbienteReal)
	if err != nil {
		return nil, ErrIndisponivel
	}
	hist, err := s.Repo().HistoricoHabilitacao(uid, dominio.AmbienteReal, nHist)
	if err != nil {
		return nil, ErrIndisponivel
	}
	cad, achou, falha, err := s.gqDoReal(uid, h)
	if err != nil {
		return nil, ErrIndisponivel
	}
	estado := dominio.EstadoSemAprovacao
	if existia {
		estado = h.EstadoReal()
	}
	r := Resposta{"uid": uid, "estado": estado, "versao": h.Versao, "acoes": orVazia(aprovacao.AcoesPermitidas(estado)),
		"aprovado": nil, "ultima_mudanca": nil, "divergencia": nil}
	if existia && h.Conta != "" {
		r["aprovado"] = map[string]any{"conector": h.ConectorAprovado(), "corretora_codigo": h.Corretora, "corretora": nomeCorretoraOuNulo(h.Corretora),
			"conta": h.Conta, "qtd_max_ordem": h.QtdMaxOrdem, "posicao_max": h.PosicaoMax,
			"por": nuloSeVazio(h.AprovadoPor), "em": emBROuNulo(h.AprovadoEm), "motivo": nuloSeVazio(h.AprovadoMotivo)}
	}
	if existia {
		ev := any(nil)
		if len(hist) > 0 {
			ev = hist[0].Evento
		}
		r["ultima_mudanca"] = map[string]any{"evento": ev, "origem": nuloSeVazio(h.Origem), "por": h.Por, "em": emBR(h.Em), "motivo": h.Motivo}
	}
	gq := map[string]any{"conector": nil, "corretora_codigo": nil, "corretora": nil, "conta": nil, "lido_em": emBR(s.agora()), "valido": falha == nil, "erro": nil}
	if achou && (falha == nil || falha.Codigo != recusaCadastroIlegivel) {
		gq["conector"], gq["corretora_codigo"], gq["corretora"], gq["conta"] = nuloSeVazio(cad.Conector), intOuNulo(cad.Corretora), nomeCorretoraOuNulo(cad.Corretora), nuloSeVazio(cad.Conta)
	}
	if falha != nil {
		gq["erro"] = map[string]any{"codigo": falha.Codigo, "motivo": alerta.ASCII(falha.Motivo)}
	}
	r["gq"] = gq
	if existia && h.Conta != "" && achou && (falha == nil || falha.Codigo != recusaCadastroIlegivel) {
		if campos := aprovacao.Campos(h, cad); len(campos) > 0 {
			r["divergencia"] = map[string]any{"desde": emBROuNulo(h.DivergenteDesde), "campos": campos}
		}
	}
	l := s.principal.p.LimitesReal
	r["limites_instancia"] = map[string]any{"qtd_ordem": intOuNulo(l.QtdOrdem), "posicao_max": intOuNulo(l.PosicaoMax),
		"perda_dia_pts": intOuNulo(int(l.PerdaDiaPts)), "entradas_dia": intOuNulo(l.EntradasDia), "entradas_min": intOuNulo(l.EntradasMin)}
	r["posicao"] = s.posicaoRealDaFicha(uid)
	hs := make([]map[string]any, 0, len(hist))
	for _, x := range hist {
		var gqh any
		if x.GQConta != "" {
			gqh = map[string]any{"conector": nuloSeVazio(x.GQConector), "corretora_codigo": intOuNulo(x.GQCorretora), "conta": x.GQConta}
		}
		hs = append(hs, map[string]any{"id": x.ID, "evento": nuloSeVazio(x.Evento), "estado_antes": nuloSeVazio(x.EstadoAntes), "estado": x.EstadoReal(),
			"versao": x.Versao, "conector": x.ConectorAprovado(), "corretora_codigo": intOuNulo(x.Corretora), "conta": nuloSeVazio(x.Conta),
			"qtd_max_ordem": x.QtdMaxOrdem, "posicao_max": x.PosicaoMax, "gq": gqh, "origem": nuloSeVazio(x.Origem),
			"por": x.Por, "motivo": x.Motivo, "em": emBR(x.Em)})
	}
	r["historico"] = hs
	return r, nil
}

func orVazia(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// posicaoRealDaFicha: as operações vivas do uid na instância REAL, com a stop na corretora.
func (s *Servico) posicaoRealDaFicha(uid int) map[string]any {
	r := s.motores[dominio.AmbienteReal]
	if r == nil {
		return map[string]any{"instancia": "DESLIGADA", "total": nil, "operacoes": nil}
	}
	ops, total := r.operacoesComStop(uid)
	for _, o := range ops {
		delete(o, "ambiente")
	}
	return map[string]any{"instancia": "LIGADA", "total": total, "operacoes": ops}
}

// operacoesComStop: as operações não finais do uid com posição ou ordem viva, com o stop_corretora.
func (m *Motor) operacoesComStop(uid int) ([]map[string]any, int) {
	ops := []map[string]any{}
	total := 0
	m.Fazer(func() {
		for _, op := range m.ops {
			if op.UID != uid || op.Estado.Final() || (op.posicao() == 0 && !op.temVivaNaCorretora()) {
				continue
			}
			total += op.posicao()
			ops = append(ops, map[string]any{"operacao_id": op.OperacaoID, "modulo": op.Modulo, "conta": op.Conta, "simbolo": op.Simbolo,
				"posicao": op.posicao(), "stop_corretora": m.stopCorretora(op)})
		}
	})
	return ops, total
}

// ListaReal: GET /usuarios/real (§5.2).
func (s *Servico) ListaReal() (Resposta, error) {
	hs, err := s.Repo().Habilitacoes()
	if err != nil {
		return nil, ErrIndisponivel
	}
	l := []map[string]any{}
	for _, h := range hs {
		if dominio.AmbienteDe(h.Ambiente) != dominio.AmbienteReal {
			continue
		}
		x := map[string]any{"uid": h.UID, "estado": h.EstadoReal(), "conector": nil, "corretora_codigo": nil, "corretora": nil, "conta": nil,
			"qtd_max_ordem": h.QtdMaxOrdem, "posicao_max": h.PosicaoMax, "versao": h.Versao, "origem": nuloSeVazio(h.Origem),
			"alterado_por": h.Por, "alterado_em": emBR(h.Em), "divergente_desde": emBROuNulo(h.DivergenteDesde)}
		if h.Conta != "" {
			x["conector"], x["corretora_codigo"], x["corretora"], x["conta"] = h.ConectorAprovado(), h.Corretora, nomeCorretoraOuNulo(h.Corretora), h.Conta
		}
		l = append(l, x)
	}
	return Resposta{"usuarios": l}, nil
}

// recusaRota: a resposta de erro das rotas da aprovação.
func recusaRota(http int, codigo, motivo string) (int, Resposta) {
	return http, Resposta{"aceito": false, "codigo": codigo, "motivo": alerta.ASCII(motivo)}
}

var codigoSucesso = map[string]string{aprovacao.AcaoAprovar: "APROVADO", aprovacao.AcaoBloquear: "BLOQUEADO",
	aprovacao.AcaoDesbloquear: "DESBLOQUEADO", aprovacao.AcaoRevogar: "REVOGADO", aprovacao.AcaoLimites: "LIMITES_ALTERADOS"}

// AcaoReal: POST /usuarios/{uid}/real/{acao} (§5.3). Devolve o status HTTP e o corpo.
func (s *Servico) AcaoReal(uid int, acao string, p PedidoAcaoReal) (int, Resposta) {
	valida := false
	for _, a := range aprovacao.Acoes {
		valida = valida || a == acao
	}
	switch {
	case !valida:
		return recusaRota(http.StatusBadRequest, "CAMPO_INVALIDO", fmt.Sprintf("acao %q (aprovar, bloquear, desbloquear, revogar, limites)", acao))
	case p.Por == nil || *p.Por <= 0:
		return recusaRota(http.StatusBadRequest, "CAMPO_INVALIDO", "por (uid do admin) obrigatorio")
	case p.Versao == nil:
		return recusaRota(http.StatusBadRequest, "CAMPO_INVALIDO", "versao (a da ficha) obrigatoria")
	}
	ped := aprovacao.Pedido{Acao: acao, Por: "admin:" + strconv.Itoa(*p.Por), Origem: dominio.OrigemTela, Motivo: p.Motivo, Versao: p.Versao}
	if p.QtdMax != nil {
		ped.QtdMax = *p.QtdMax
	}
	if p.PosMax != nil {
		ped.PosMax = *p.PosMax
	}
	if p.Visto != nil {
		v := usuario.Cadastro{Corretora: p.Visto.Corretora, Conta: p.Visto.Conta}
		if p.Visto.Conector != nil {
			v.Conector = *p.Visto.Conector
		}
		ped.Visto = &v
	}
	l := s.principal.p.LimitesReal
	inst := aprovacao.Instancia{QtdOrdem: l.QtdOrdem, PosicaoMax: l.PosicaoMax}
	agora := s.agora()
	// o GQ só importa ao aprovar e ao desbloquear; lido antes da transação (o callback não faz E/S),
	// com o conector aprovado de agora — a versão conferida dentro dela barra uma mudança no meio
	var cad usuario.Cadastro
	var falha *usuario.Falha
	if acao == aprovacao.AcaoAprovar || acao == aprovacao.AcaoDesbloquear {
		h0, _, err := s.Repo().Habilitacao(uid, dominio.AmbienteReal)
		if err != nil {
			return recusaRota(http.StatusServiceUnavailable, "CONFIG_USUARIO_INDISPONIVEL", "banco do servico indisponivel")
		}
		c, achou, f, err := s.gqDoReal(uid, h0)
		if err != nil {
			return recusaRota(http.StatusServiceUnavailable, "CONFIG_USUARIO_INDISPONIVEL", "banco do GQ indisponivel")
		}
		cad, falha = c, f
		if !achou {
			falha = &usuario.Falha{Codigo: "CADASTRO_INVALIDO", Motivo: fmt.Sprintf("uid %d sem linha em acusuario", uid)}
		}
	}
	var rec *aprovacao.Recusa
	t, err := s.Repo().TransicaoReal(uid, func(antes dominio.Habilitacao, existia bool) (dominio.Habilitacao, string, bool, error) {
		h, ev, r := aprovacao.Decidir(antes, existia, ped, cad, falha, inst, agora)
		if r != nil {
			rec = r
			return antes, "", false, nil
		}
		return h, ev, true, nil
	})
	if err != nil {
		slog.Warn("aprovação do REAL: banco", "uid", uid, "acao", acao, "erro", err)
		return recusaRota(http.StatusServiceUnavailable, "CONFIG_USUARIO_INDISPONIVEL", "banco do servico ou do GQ indisponivel")
	}
	atual := t.Antes
	if rec != nil {
		st, resp := recusaRota(rec.HTTP, rec.Codigo, rec.Motivo)
		if t.Existia {
			resp["estado"], resp["versao"] = atual.EstadoReal(), atual.Versao
		}
		return st, resp
	}
	d := t.Depois
	codigo := codigoSucesso[acao]
	motivo := ""
	switch {
	case acao == aprovacao.AcaoAprovar:
		motivo = fmt.Sprintf("uid %d aprovado no REAL: %s, limites %d/%d", uid, aprovacao.TextoAprovado(d), d.QtdMaxOrdem, d.PosicaoMax)
	case acao == aprovacao.AcaoDesbloquear && d.Estado == dominio.EstadoContaDivergente:
		codigo = "CONTA_DIVERGENTE"
		motivo = fmt.Sprintf("uid %d desbloqueado, mas o cadastro do GQ mudou: %s", uid, aprovacao.MotivoDivergente(d, cad))
	case acao == aprovacao.AcaoLimites:
		motivo = fmt.Sprintf("uid %d: limites do REAL %d/%d", uid, d.QtdMaxOrdem, d.PosicaoMax)
	default:
		motivo = fmt.Sprintf("uid %d no REAL: %s -> %s", uid, atual.EstadoReal(), d.Estado)
	}
	slog.Warn("aprovação do REAL pela tela", "uid", uid, "acao", acao, "de", atual.EstadoReal(), "para", d.Estado, "versao", d.Versao, "por", d.Por)
	s.depoisDaAcaoReal(uid, acao, d)
	return http.StatusOK, Resposta{"aceito": true, "codigo": codigo, "motivo": alerta.ASCII(motivo), "estado": d.Estado, "versao": d.Versao}
}

// depoisDaAcaoReal: o cache das instâncias relê o uid, o ADMIN_ACAO sai pela principal e o
// avaliador confere o uid já (o alerta da divergência no desbloquear, o RESOLVIDO no aprovar).
func (s *Servico) depoisDaAcaoReal(uid int, acao string, d dominio.Habilitacao) {
	for _, m := range s.lista {
		m := m
		m.Fazer(func() { m.invalidarUsuario(uid) })
	}
	p := s.principal
	p.Fazer(func() {
		p.publicar("ADMIN_ACAO", nil, map[string]any{"acao": "REAL_" + map[string]string{aprovacao.AcaoAprovar: "APROVAR", aprovacao.AcaoBloquear: "BLOQUEAR",
			aprovacao.AcaoDesbloquear: "DESBLOQUEAR", aprovacao.AcaoRevogar: "REVOGAR", aprovacao.AcaoLimites: "LIMITES"}[acao],
			"uid": uid, "ambiente": dominio.AmbienteReal, "motivo": d.Motivo, "por": d.Por})
		p.cutucarAvaliador(uid)
	})
}

// StopDoUsuario: GET /usuarios/{uid}/stop?ambiente= (§5.5).
func (s *Servico) StopDoUsuario(uid int, amb string) (Resposta, *Indisponivel) {
	m, ind := s.instanciaDe(amb, "", "")
	if ind != nil {
		return nil, ind
	}
	ops, _ := m.operacoesComStop(uid)
	for _, o := range ops {
		delete(o, "conta")
	}
	return Resposta{"uid": uid, "ambiente": amb, "operacoes": ops}, nil
}

// HistoricoPadrao: o ?historico= da ficha (1 a 200, padrão 20); ok=false fora da faixa.
func HistoricoPadrao(v string) (int, bool) {
	if v == "" {
		return histPadrao, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n >= 1 && n <= histMax
}

// stopCorretora (§5.5): o estado da 1014 da operação na corretora, para o cartão da tela.
func (m *Motor) stopCorretora(op *operacao) map[string]any {
	r := map[string]any{"estado": nil, "clordid": nil, "orderid37": nil, "preco_stop": nil, "preco_limite": nil, "qtd": nil,
		"preco_stop_pedido": nil, "preco_limite_pedido": nil, "disparada": false, "desde": nil}
	pos := op.posicao()
	p := op.viva(dominio.Protecao)
	switch {
	case pos == 0:
		r["estado"] = "SEM_POSICAO"
	case p != nil && p.pend == pendCancel:
		r["estado"] = "SAINDO"
	case p != nil:
		r["clordid"], r["orderid37"], r["preco_stop"], r["preco_limite"], r["qtd"] = p.ClOrdAtual, nuloSeVazio(p.OrderID), p.Stop, p.StopLim, p.Qtd
		r["disparada"] = !p.disparadaEm.IsZero()
		switch {
		case p.pend == pendReplace:
			r["estado"], r["desde"] = "AJUSTANDO", emBROuNulo(p.pendDesde)
			if p.stopPend != 0 {
				r["preco_stop_pedido"], r["preco_limite_pedido"] = p.stopPend, p.stopLimPend
			}
		case p.Estado == dominio.Enviando || p.Estado == dominio.SemConfirmacao:
			r["estado"], r["desde"] = "AJUSTANDO", emBROuNulo(p.CriadaEm)
		default:
			r["estado"] = "VIVA"
		}
	case op.viva(dominio.Saida) != nil || op.Estado == dominio.CancelandoProtecao || op.Estado == dominio.Saindo:
		r["estado"] = "SAINDO"
	default:
		r["estado"], r["desde"] = "DESPROTEGIDA", emBROuNulo(op.semProtecaoDesde)
	}
	return r
}
