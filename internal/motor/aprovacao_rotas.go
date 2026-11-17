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
	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// As rotas da aprovação do REAL (SPEC-TECNICA-REAL-PELA-TELA-F1 §5, o contrato das telas; v1.36,
// SPEC-TECNICA-v1.36-CONTA-LIVRE §5): a ficha, a lista, as ações do admin e a stop na corretora.
// Funcionam com ou sem a instância REAL: o banco é lido e gravado aqui (na goroutine do HTTP, com a
// linha travada pelo repositório); os motores só invalidam o cache, publicam o ADMIN_ACAO e o
// verificador confere o uid.

// PedidoAcaoReal: o corpo do POST /usuarios/{uid}/real/{acao}. v1.36: visto, qtd_max_ordem e
// posicao_max (as telas F1 mandam) são aceitos e ignorados — a aprovação não tem conta nem limites.
type PedidoAcaoReal struct {
	Por    *int            `json:"por"`
	Motivo string          `json:"motivo"`
	Versao *int            `json:"versao"`
	Visto  *map[string]any `json:"visto"`
	QtdMax *int            `json:"qtd_max_ordem"`
	PosMax *int            `json:"posicao_max"`
}

// ErrIndisponivel: o banco (do serviço ou do GQ) não respondeu (503).
var ErrIndisponivel = errors.New("CONFIG_USUARIO_INDISPONIVEL")

const histPadrao, histMax = 20, 200

func (s *Servico) agora() time.Time { return s.principal.agora() }

// DefinirRegrasReal (v1.36 §3.1): o que a máquina atenderia no REAL, lido do env, para a ficha e a
// lista com o REAL fora de AMBIENTES_PERMITIDOS. Com a instância REAL, valem as dela.
func (s *Servico) DefinirRegrasReal(rg usuario.RegrasReal) {
	s.rgReal = &rg
	for _, m := range s.lista {
		m.rgRealEnv = &rg
	}
}

// RegrasRealDoEnv (v1.36 §3.1): o que a máquina atenderia no REAL, pelas chaves REAL_ do env (as
// mesmas que montariam a instância REAL), para quando ela não existe.
func RegrasRealDoEnv(c config.Config) usuario.RegrasReal {
	rg := usuario.RegrasReal{PnT: c.Texto("REAL_FIX_HOST", "") != "" && !c.Bool("REAL_PNT_DESLIGADO", false),
		Flexscan:  c.Texto("REAL_FLEXSCAN_URL", "") != "" || c.Texto("REAL_FLEXSCAN_CORRETORAS", "") != "",
		ContasHML: c.Parametros().ContasHML, ContasHMLFlx: c.Parametros().ContasHMLFlx}
	if crs, err := c.CorretorasFlexscan(); err == nil && len(crs) > 0 {
		rg.Corretoras, rg.Contas = map[int]string{}, map[int][]string{}
		for _, cr := range crs {
			rg.Corretoras[cr.Codigo], rg.Contas[cr.Codigo] = cr.Nome, cr.Contas
		}
	}
	return rg
}

func (s *Servico) regrasReal() usuario.RegrasReal {
	if r := s.motores[dominio.AmbienteReal]; r != nil {
		return r.regrasReal()
	}
	if s.rgReal != nil {
		return *s.rgReal
	}
	return usuario.RegrasReal{}
}

// cadastroGQ: o cadastro do GQ de um uid lido agora, para a ficha, a lista e o aviso do aprovar.
type cadastroGQ struct {
	cad   usuario.Cadastro // os campos que se leem (zero se ilegível)
	legal bool             // os campos se leem
	falha *usuario.Falha   // a causa no Codigo; nil = utilizável no REAL
}

func lerCadastroGQ(texto string, achou bool, rg usuario.RegrasReal) cadastroGQ {
	if !achou {
		return cadastroGQ{falha: &usuario.Falha{Codigo: usuario.CausaSemCadastro, Motivo: "uid sem linha em acusuario"}}
	}
	cad, f := usuario.LerCadastro(texto)
	c := cadastroGQ{cad: cad, legal: f == nil}
	if _, f := usuario.CadastroReal(texto, rg); f != nil {
		c.falha = f
	}
	return c
}

// gqDoReal: o cadastro do GQ do uid relido agora.
func (s *Servico) gqDoReal(uid int) (cadastroGQ, error) {
	_, prd, achou, err := s.Repo().ConfigUsuario(uid)
	if err != nil {
		return cadastroGQ{}, err
	}
	return lerCadastroGQ(prd, achou, s.regrasReal()), nil
}

func nomeCorretoraOuNulo(c int, declaradas map[int]string) any {
	if c == 0 {
		return nil
	}
	if n := usuario.NomeCorretora(c, declaradas); n != "" {
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

// estadoDaFicha: o estado que vale (o CONTA_DIVERGENTE da v1.35 vale como APROVADO).
func estadoDaFicha(h dominio.Habilitacao, existia bool) string {
	if !existia {
		return dominio.EstadoSemAprovacao
	}
	return h.EstadoVigente()
}

// gqDaFicha: o bloco "gq" da ficha e da lista — só informação; a causa em erro.
func (s *Servico) gqDaFicha(c cadastroGQ) map[string]any {
	gq := map[string]any{"conector": nil, "corretora_codigo": nil, "corretora": nil, "conta": nil, "lido_em": emBR(s.agora()), "valido": c.falha == nil, "erro": nil}
	if c.legal {
		gq["conector"], gq["corretora_codigo"], gq["conta"] = nuloSeVazio(c.cad.Conector), intOuNulo(c.cad.Corretora), nuloSeVazio(c.cad.Conta)
		gq["corretora"] = nomeCorretoraOuNulo(c.cad.Corretora, s.regrasReal().Corretoras)
	}
	if c.falha != nil {
		gq["erro"] = map[string]any{"codigo": recusaInutilizavel, "causa": c.falha.Codigo, "motivo": alerta.ASCII(prefixoMotivoCadastro + c.falha.Motivo)}
	}
	return gq
}

// FichaReal: GET /usuarios/{uid}/real (§5.1; v1.36 §5: sem conta e sem limites aprovados).
func (s *Servico) FichaReal(uid, nHist int) (Resposta, error) {
	h, existia, err := s.Repo().Habilitacao(uid, dominio.AmbienteReal)
	if err != nil {
		return nil, ErrIndisponivel
	}
	hist, err := s.Repo().HistoricoHabilitacao(uid, dominio.AmbienteReal, nHist)
	if err != nil {
		return nil, ErrIndisponivel
	}
	cad, err := s.gqDoReal(uid)
	if err != nil {
		return nil, ErrIndisponivel
	}
	estado := estadoDaFicha(h, existia)
	r := Resposta{"uid": uid, "estado": estado, "versao": h.Versao, "acoes": orVazia(aprovacao.AcoesPermitidas(estado)),
		"aprovado": nil, "ultima_mudanca": nil, "divergencia": nil, "limites_instancia": nil}
	if existia && h.AprovadoPor != "" {
		r["aprovado"] = map[string]any{"conector": nil, "corretora_codigo": nil, "corretora": nil, "conta": nil, "qtd_max_ordem": nil, "posicao_max": nil,
			"por": nuloSeVazio(h.AprovadoPor), "em": emBROuNulo(h.AprovadoEm), "motivo": nuloSeVazio(h.AprovadoMotivo)}
	}
	if existia {
		ev := any(nil)
		if len(hist) > 0 {
			ev = hist[0].Evento
		}
		r["ultima_mudanca"] = map[string]any{"evento": ev, "origem": nuloSeVazio(h.Origem), "por": h.Por, "em": emBR(h.Em), "motivo": h.Motivo}
	}
	r["gq"] = s.gqDaFicha(cad)
	r["posicao"] = s.posicaoRealDaFicha(uid)
	hs := make([]map[string]any, 0, len(hist))
	for _, x := range hist {
		var gqh any
		if x.GQConta != "" {
			gqh = map[string]any{"conector": nuloSeVazio(x.GQConector), "corretora_codigo": intOuNulo(x.GQCorretora), "conta": x.GQConta}
		}
		// as linhas antigas (v1.35) trazem a conta e os limites que gravaram; as da v1.36, null
		hs = append(hs, map[string]any{"id": x.ID, "evento": nuloSeVazio(x.Evento), "estado_antes": nuloSeVazio(x.EstadoAntes), "estado": x.EstadoReal(),
			"versao": x.Versao, "conector": nuloSeVazio(conectorDaLinha(x)), "corretora_codigo": intOuNulo(x.Corretora), "conta": nuloSeVazio(x.Conta),
			"qtd_max_ordem": intOuNulo(x.QtdMaxOrdem), "posicao_max": intOuNulo(x.PosicaoMax), "gq": gqh, "origem": nuloSeVazio(x.Origem),
			"por": x.Por, "motivo": x.Motivo, "em": emBR(x.Em)})
	}
	r["historico"] = hs
	return r, nil
}

// conectorDaLinha: o conector que uma linha da v1.35 aprovou (com conta); as da v1.36, nenhum.
func conectorDaLinha(x dominio.HabilitacaoHist) string {
	if x.Conta == "" {
		return ""
	}
	return x.ConectorAprovado()
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

// ListaReal: GET /usuarios/real (§5.2; v1.36: o cadastro do GQ lido agora e se ele é utilizável).
func (s *Servico) ListaReal() (Resposta, error) {
	hs, err := s.Repo().Habilitacoes()
	if err != nil {
		return nil, ErrIndisponivel
	}
	var reais []dominio.Habilitacao
	var uids []int
	for _, h := range hs {
		if dominio.AmbienteDe(h.Ambiente) == dominio.AmbienteReal {
			reais, uids = append(reais, h), append(uids, h.UID)
		}
	}
	cfg := map[int][2]string{}
	if len(uids) > 0 {
		if cfg, err = s.Repo().ConfigUsuarios(uids); err != nil {
			return nil, ErrIndisponivel
		}
	}
	rg := s.regrasReal()
	l := []map[string]any{}
	for _, h := range reais {
		c, achou := cfg[h.UID]
		gq := lerCadastroGQ(c[1], achou, rg)
		x := map[string]any{"uid": h.UID, "estado": h.EstadoVigente(), "conector": nil, "corretora_codigo": nil, "corretora": nil, "conta": nil,
			"cadastro_utilizavel": gq.falha == nil, "qtd_max_ordem": nil, "posicao_max": nil, "versao": h.Versao, "origem": nuloSeVazio(h.Origem),
			"alterado_por": h.Por, "alterado_em": emBR(h.Em), "divergente_desde": nil}
		if gq.legal {
			x["conector"], x["corretora_codigo"], x["conta"] = nuloSeVazio(gq.cad.Conector), intOuNulo(gq.cad.Corretora), nuloSeVazio(gq.cad.Conta)
			x["corretora"] = nomeCorretoraOuNulo(gq.cad.Corretora, rg.Corretoras)
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
	aprovacao.AcaoDesbloquear: "DESBLOQUEADO", aprovacao.AcaoRevogar: "REVOGADO"}

// AcaoReal: POST /usuarios/{uid}/real/{acao} (§5.3). Devolve o status HTTP e o corpo.
func (s *Servico) AcaoReal(uid int, acao string, p PedidoAcaoReal) (int, Resposta) {
	valida := false
	for _, a := range aprovacao.Acoes {
		valida = valida || a == acao
	}
	switch {
	case !valida:
		return recusaRota(http.StatusBadRequest, "CAMPO_INVALIDO", fmt.Sprintf("acao %q (aprovar, bloquear, desbloquear, revogar)", acao))
	case p.Por == nil || *p.Por <= 0:
		return recusaRota(http.StatusBadRequest, "CAMPO_INVALIDO", "por (uid do admin) obrigatorio")
	case p.Versao == nil:
		return recusaRota(http.StatusBadRequest, "CAMPO_INVALIDO", "versao (a da ficha) obrigatoria")
	}
	ped := aprovacao.Pedido{Acao: acao, Por: "admin:" + strconv.Itoa(*p.Por), Origem: dominio.OrigemTela, Motivo: p.Motivo, Versao: p.Versao}
	agora := s.agora()
	var rec *aprovacao.Recusa
	t, err := s.Repo().TransicaoReal(uid, func(antes dominio.Habilitacao, existia bool) (dominio.Habilitacao, string, bool, error) {
		h, ev, r := aprovacao.Decidir(antes, existia, ped, agora)
		if r != nil {
			rec = r
			return antes, "", false, nil
		}
		return h, ev, true, nil
	})
	if err != nil {
		slog.Warn("aprovação do REAL: banco", "uid", uid, "acao", acao, "erro", err)
		return recusaRota(http.StatusServiceUnavailable, "CONFIG_USUARIO_INDISPONIVEL", "banco do servico indisponivel")
	}
	atual := t.Antes
	if rec != nil {
		st, resp := recusaRota(rec.HTTP, rec.Codigo, rec.Motivo)
		if t.Existia {
			resp["estado"], resp["versao"] = atual.EstadoVigente(), atual.Versao
		}
		return st, resp
	}
	d := t.Depois
	motivo := fmt.Sprintf("uid %d no REAL: %s -> %s", uid, estadoDaFicha(atual, t.Existia), d.Estado)
	if acao == aprovacao.AcaoAprovar || acao == aprovacao.AcaoDesbloquear {
		motivo = fmt.Sprintf("uid %d aprovado no REAL", uid)
		if acao == aprovacao.AcaoDesbloquear {
			motivo = fmt.Sprintf("uid %d desbloqueado no REAL", uid)
		}
		// o cadastro do GQ não entra na aprovação; só avisa se hoje ele não serve (§5)
		if c, err := s.gqDoReal(uid); err == nil && c.falha != nil {
			motivo += "; " + prefixoMotivoCadastro + "hoje " + c.falha.Motivo + " (entradas recusadas ate corrigir)"
		}
	}
	slog.Warn("aprovação do REAL pela tela", "uid", uid, "acao", acao, "de", estadoDaFicha(atual, t.Existia), "para", d.Estado, "versao", d.Versao, "por", d.Por)
	s.depoisDaAcaoReal(uid, acao, d)
	return http.StatusOK, Resposta{"aceito": true, "codigo": codigoSucesso[acao], "motivo": alerta.ASCII(motivo), "estado": d.Estado, "versao": d.Versao}
}

// depoisDaAcaoReal: o cache das instâncias relê o uid, o ADMIN_ACAO sai pela principal e o
// verificador confere o uid já (o alerta no aprovar/desbloquear com o cadastro ruim, o RESOLVIDO no
// bloquear/revogar).
func (s *Servico) depoisDaAcaoReal(uid int, acao string, d dominio.Habilitacao) {
	for _, m := range s.lista {
		m := m
		m.Fazer(func() { m.invalidarUsuario(uid) })
	}
	p := s.principal
	p.Fazer(func() {
		p.publicar("ADMIN_ACAO", nil, map[string]any{"acao": "REAL_" + map[string]string{aprovacao.AcaoAprovar: "APROVAR", aprovacao.AcaoBloquear: "BLOQUEAR",
			aprovacao.AcaoDesbloquear: "DESBLOQUEAR", aprovacao.AcaoRevogar: "REVOGAR"}[acao],
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
