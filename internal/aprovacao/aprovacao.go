// Package aprovacao decide as transições da aprovação do REAL (SPEC-TECNICA-REAL-PELA-TELA-F1 §3 e
// §5): as do admin (aprovar, bloquear, desbloquear, revogar, limites; pela rota ou, só a revogação,
// pela CLI) e a automática do cadastro do GQ (a divergência e a volta). Funções puras: quem grava é
// o repositório (TransicaoReal), quem alerta é o motor.
package aprovacao

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// Ações do admin.
const (
	AcaoAprovar     = "aprovar"
	AcaoBloquear    = "bloquear"
	AcaoDesbloquear = "desbloquear"
	AcaoRevogar     = "revogar"
	AcaoLimites     = "limites"
)

// Acoes: as ações, na ordem da tela.
var Acoes = []string{AcaoAprovar, AcaoBloquear, AcaoDesbloquear, AcaoRevogar, AcaoLimites}

// permitidas: de que estado cada ação sai (§5, "Transições do admin").
var permitidas = map[string][]string{
	AcaoAprovar:     {dominio.EstadoSemAprovacao, dominio.EstadoRevogado, dominio.EstadoContaDivergente},
	AcaoBloquear:    {dominio.EstadoAprovado, dominio.EstadoContaDivergente},
	AcaoDesbloquear: {dominio.EstadoBloqueado},
	AcaoRevogar:     {dominio.EstadoAprovado, dominio.EstadoBloqueado, dominio.EstadoContaDivergente},
	AcaoLimites:     {dominio.EstadoAprovado, dominio.EstadoBloqueado, dominio.EstadoContaDivergente},
}

var eventos = map[string]string{AcaoAprovar: dominio.EventoAprovar, AcaoBloquear: dominio.EventoBloquear,
	AcaoDesbloquear: dominio.EventoDesbloquear, AcaoRevogar: dominio.EventoRevogar, AcaoLimites: dominio.EventoLimites}

// Recusa: por que a ação não grava; HTTP é o status da rota.
type Recusa struct {
	Codigo, Motivo string
	HTTP           int
}

func (r *Recusa) Error() string { return r.Codigo + ": " + r.Motivo }

func recusa(http int, codigo, formato string, a ...any) *Recusa {
	return &Recusa{Codigo: codigo, Motivo: fmt.Sprintf(formato, a...), HTTP: http}
}

// Pedido: a ação do admin.
type Pedido struct {
	Acao   string
	Por    string // "admin:1450" (a rota) ou o usuário do sistema (a CLI)
	Origem string // dominio.OrigemTela ou OrigemCLI
	Motivo string
	Versao *int // a versão que a tela viu; nil = não confere (a revogação de emergência da CLI)
	// aprovar: o cadastro que o admin viu na ficha (tem de ser o do GQ agora)
	Visto *usuario.Cadastro
	// aprovar e limites
	QtdMax, PosMax int
}

// Instancia: os limites do REAL da máquina (REAL_LIMITE_QTD_ORDEM e _POSICAO_MAX); zero = ausente.
type Instancia struct{ QtdOrdem, PosicaoMax int }

// AcoesPermitidas: as ações possíveis no estado (os botões da ficha).
func AcoesPermitidas(estado string) []string {
	var r []string
	for _, a := range Acoes {
		for _, e := range permitidas[a] {
			if e == estado {
				r = append(r, a)
			}
		}
	}
	return r
}

// ConectorDo: o conector do cadastro; sem Robo.Conector no GQ, o aprovado (§3.1) ou, sem aprovado, o
// PnT (o de uma habilitação sem conector).
func ConectorDo(c usuario.Cadastro, aprovado string) string {
	switch {
	case c.Conector != "":
		return c.Conector
	case aprovado != "":
		return aprovado
	}
	return dominio.ConectorPnT
}

// Campos: os campos do cadastro do GQ que diferem do aprovado (conector, corretora, conta).
func Campos(h dominio.Habilitacao, c usuario.Cadastro) []string {
	var r []string
	if ConectorDo(c, h.ConectorAprovado()) != h.ConectorAprovado() {
		r = append(r, "conector")
	}
	if c.Corretora != h.Corretora {
		r = append(r, "corretora")
	}
	if c.Conta != h.Conta {
		r = append(r, "conta")
	}
	return r
}

// Diverge: o cadastro do GQ não é o aprovado.
func Diverge(h dominio.Habilitacao, c usuario.Cadastro) bool { return len(Campos(h, c)) > 0 }

// TextoCadastro: "PNT / BTG (85) / conta 4582351".
func TextoCadastro(conector string, corretora int, conta string) string {
	nome := usuario.Corretora(corretora)
	if nome == "" {
		nome = "corretora"
	}
	return fmt.Sprintf("%s / %s (%d) / conta %s", conector, nome, corretora, conta)
}

// TextoAprovado e TextoGQ: os dois lados da divergência.
func TextoAprovado(h dominio.Habilitacao) string {
	return TextoCadastro(h.ConectorAprovado(), h.Corretora, h.Conta)
}

func TextoGQ(h dominio.Habilitacao, c usuario.Cadastro) string {
	return TextoCadastro(ConectorDo(c, h.ConectorAprovado()), c.Corretora, c.Conta)
}

// MotivoDivergente: a recusa da entrada com a conta divergente (§2.1).
func MotivoDivergente(h dominio.Habilitacao, c usuario.Cadastro) string {
	return fmt.Sprintf("conta alterada no GQ (aprovada %s, atual %s): aguardando aprovacao", TextoAprovado(h), TextoGQ(h, c))
}

// comGQ: o cadastro do GQ visto na divergência (a ficha e o alerta).
func comGQ(h *dominio.Habilitacao, c usuario.Cadastro, desde time.Time) {
	h.GQConector, h.GQCorretora, h.GQConta = ConectorDo(c, h.ConectorAprovado()), c.Corretora, c.Conta
	if h.DivergenteDesde.IsZero() {
		h.DivergenteDesde = desde
	}
}

func semGQ(h *dominio.Habilitacao) {
	h.GQConector, h.GQCorretora, h.GQConta, h.DivergenteDesde = "", 0, "", time.Time{}
}

// Decidir: a ação do admin sobre a linha REAL (travada). gq e gqFalha: o cadastro do GQ lido agora
// (aprovar e desbloquear); gqFalha também traz a coluna que não resolve (corretora desconhecida,
// conta de simulador), que quem chama confere antes. A linha nova sobe a versão e grava quem, quando,
// o motivo e a origem.
func Decidir(antes dominio.Habilitacao, existia bool, p Pedido, gq usuario.Cadastro, gqFalha *usuario.Falha, inst Instancia, agora time.Time) (dominio.Habilitacao, string, *Recusa) {
	ok := false
	for _, a := range Acoes {
		ok = ok || a == p.Acao
	}
	if !ok {
		return antes, "", recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "acao %q (aprovar, bloquear, desbloquear, revogar, limites)", p.Acao)
	}
	motivo := strings.TrimSpace(p.Motivo)
	if n := len([]rune(motivo)); n < 3 || n > 200 {
		return antes, "", recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "motivo obrigatorio, de 3 a 200 caracteres")
	}
	if strings.TrimSpace(p.Por) == "" {
		return antes, "", recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "por obrigatorio (quem faz a acao)")
	}
	estado := antes.EstadoReal()
	if !existia {
		estado = dominio.EstadoSemAprovacao
	}
	if p.Versao != nil && *p.Versao != antes.Versao {
		return antes, "", recusa(http.StatusConflict, "VERSAO_MUDOU", "a aprovacao mudou (versao %d, a tela viu %d): recarregue a ficha", antes.Versao, *p.Versao)
	}
	dentro := false
	for _, e := range permitidas[p.Acao] {
		dentro = dentro || e == estado
	}
	if !dentro {
		return antes, "", recusa(http.StatusConflict, "TRANSICAO_INVALIDA", "%s nao se aplica ao estado %s", p.Acao, estado)
	}
	if p.Acao == AcaoAprovar || p.Acao == AcaoLimites {
		if r := conferirLimites(p, inst); r != nil {
			return antes, "", r
		}
	}

	h := antes
	h.Conectores = append([]string(nil), antes.Conectores...)
	switch p.Acao {
	case AcaoAprovar:
		if gqFalha != nil {
			return antes, "", recusa(http.StatusUnprocessableEntity, "CADASTRO_INVALIDO", "o cadastro do GQ nao serve para o REAL: %s", gqFalha.Motivo)
		}
		con := ConectorDo(gq, "")
		if p.Visto == nil {
			return antes, "", recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "visto (o cadastro que o admin conferiu) obrigatorio")
		}
		if ConectorDo(*p.Visto, "") != con || p.Visto.Corretora != gq.Corretora || p.Visto.Conta != gq.Conta {
			return antes, "", recusa(http.StatusConflict, "CADASTRO_MUDOU", "o cadastro do GQ mudou: agora %s, a tela viu %s",
				TextoCadastro(con, gq.Corretora, gq.Conta), TextoCadastro(ConectorDo(*p.Visto, ""), p.Visto.Corretora, p.Visto.Conta))
		}
		h.Habilitado, h.Estado, h.Conectores = true, dominio.EstadoAprovado, []string{con}
		h.Corretora, h.Conta = gq.Corretora, gq.Conta
		h.QtdMaxOrdem, h.PosicaoMax = p.QtdMax, p.PosMax
		h.AprovadoPor, h.AprovadoEm, h.AprovadoMotivo = p.Por, agora, motivo
		semGQ(&h)
	case AcaoBloquear:
		h.Habilitado, h.Estado = false, dominio.EstadoBloqueado
	case AcaoDesbloquear:
		h.Habilitado, h.Estado = true, dominio.EstadoAprovado
		semGQ(&h)
		if gqFalha == nil && Diverge(antes, gq) {
			// o GQ mudou durante o bloqueio: volta já divergente (o avaliador alerta)
			h.Habilitado, h.Estado = false, dominio.EstadoContaDivergente
			comGQ(&h, gq, agora)
		}
	case AcaoRevogar:
		h.Habilitado, h.Estado = false, dominio.EstadoRevogado
	case AcaoLimites:
		h.QtdMaxOrdem, h.PosicaoMax = p.QtdMax, p.PosMax
	}
	h.Versao = antes.Versao + 1
	h.Por, h.Motivo, h.Em, h.Origem = p.Por, motivo, agora, p.Origem
	return h, eventos[p.Acao], nil
}

func conferirLimites(p Pedido, inst Instancia) *Recusa {
	if p.QtdMax <= 0 || p.PosMax <= 0 {
		return recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "qtd_max_ordem e posicao_max obrigatorios e maiores que zero")
	}
	if inst.QtdOrdem <= 0 || inst.PosicaoMax <= 0 {
		return recusa(http.StatusUnprocessableEntity, "LIMITES_INSTANCIA_AUSENTES", "sem REAL_LIMITE_QTD_ORDEM ou REAL_LIMITE_POSICAO_MAX no service.env: os limites do uid nao tem com o que comparar")
	}
	if p.QtdMax > inst.QtdOrdem || p.PosMax > inst.PosicaoMax {
		return recusa(http.StatusUnprocessableEntity, "LIMITE_ACIMA_DA_INSTANCIA", "limites do uid %d/%d acima dos da instancia %d/%d (REAL_LIMITE_QTD_ORDEM/REAL_LIMITE_POSICAO_MAX)",
			p.QtdMax, p.PosMax, inst.QtdOrdem, inst.PosicaoMax)
	}
	return nil
}

// DecidirCadastro (§3.2): a transição automática pelo cadastro do GQ (legível) lido agora.
//   - APROVADO e o GQ diferente: CONTA_DIVERGENTE (habilitado 0), com o GQ visto.
//   - CONTA_DIVERGENTE e o GQ de volta ao aprovado: APROVADO; com o GQ diferente de novo (outro
//     valor): só o GQ visto atualizado.
//   - BLOQUEADO e REVOGADO: só o GQ visto (a ficha), quando muda; o estado do admin vale.
//
// gravar=false quando nada muda (a recarga de 30 s sem mudança não grava histórico).
func DecidirCadastro(antes dominio.Habilitacao, existia bool, gq usuario.Cadastro, agora time.Time) (dominio.Habilitacao, string, bool) {
	if !existia {
		return antes, "", false
	}
	h := antes
	h.Conectores = append([]string(nil), antes.Conectores...)
	diverge := Diverge(antes, gq)
	mudouGQ := func() bool {
		return !diverge && antes.GQConta != "" ||
			diverge && (antes.GQConector != ConectorDo(gq, antes.ConectorAprovado()) || antes.GQCorretora != gq.Corretora || antes.GQConta != gq.Conta)
	}
	evento := dominio.EventoDivergencia
	switch estado := antes.EstadoReal(); {
	case estado == dominio.EstadoAprovado && diverge:
		h.Habilitado, h.Estado = false, dominio.EstadoContaDivergente
		comGQ(&h, gq, agora)
		h.Motivo = "cadastro do GQ diferente do aprovado: " + TextoGQ(antes, gq)
	case estado == dominio.EstadoContaDivergente && !diverge:
		h.Habilitado, h.Estado = true, dominio.EstadoAprovado
		semGQ(&h)
		h.Motivo, evento = "cadastro do GQ de volta ao aprovado", dominio.EventoVoltou
	case estado == dominio.EstadoContaDivergente && mudouGQ():
		comGQ(&h, gq, agora)
		h.Motivo = "cadastro do GQ diferente do aprovado: " + TextoGQ(antes, gq)
	case (estado == dominio.EstadoBloqueado || estado == dominio.EstadoRevogado) && mudouGQ():
		if diverge {
			comGQ(&h, gq, agora)
			h.Motivo = "cadastro do GQ diferente do aprovado (estado do admin mantido): " + TextoGQ(antes, gq)
		} else {
			semGQ(&h)
			h.Motivo, evento = "cadastro do GQ de volta ao aprovado (estado do admin mantido)", dominio.EventoVoltou
		}
	default:
		return antes, "", false
	}
	h.Versao = antes.Versao + 1
	h.Por, h.Em, h.Origem = "servico", agora, dominio.OrigemServico
	return h, evento, true
}
