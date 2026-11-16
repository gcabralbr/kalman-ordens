// Package aprovacao decide as transições da aprovação do REAL pelo admin (SPEC-TECNICA-REAL-PELA-
// TELA-F1 §5; v1.36, SPEC-TECNICA-v1.36-CONTA-LIVRE §1): aprovar, bloquear, desbloquear e revogar,
// pela rota ou, só a revogação, pela CLI. Desde a v1.36 a aprovação diz só "este uid pode usar o
// REAL": sem conta, sem conector e sem limites — conta, corretora e conector vêm do cadastro do GQ a
// cada entrada. Funções puras: quem grava é o repositório (TransicaoReal).
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
)

// Acoes: as ações, na ordem da tela. v1.36: a ação "limites" saiu (D7).
var Acoes = []string{AcaoAprovar, AcaoBloquear, AcaoDesbloquear, AcaoRevogar}

// permitidas: de que estado cada ação sai (§5, "Transições do admin"; v1.36: sem CONTA_DIVERGENTE).
var permitidas = map[string][]string{
	AcaoAprovar:     {dominio.EstadoSemAprovacao, dominio.EstadoRevogado},
	AcaoBloquear:    {dominio.EstadoAprovado},
	AcaoDesbloquear: {dominio.EstadoBloqueado},
	AcaoRevogar:     {dominio.EstadoAprovado, dominio.EstadoBloqueado},
}

var eventos = map[string]string{AcaoAprovar: dominio.EventoAprovar, AcaoBloquear: dominio.EventoBloquear,
	AcaoDesbloquear: dominio.EventoDesbloquear, AcaoRevogar: dominio.EventoRevogar}

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
}

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

// TextoCadastro: "PNT / BTG (85) / conta 4582351".
func TextoCadastro(conector string, corretora int, conta string) string {
	nome := usuario.Corretora(corretora)
	if nome == "" {
		nome = "corretora"
	}
	return fmt.Sprintf("%s / %s (%d) / conta %s", conector, nome, corretora, conta)
}

// Decidir: a ação do admin sobre a linha REAL (travada). A linha nova sobe a versão e grava quem,
// quando, o motivo e a origem. O aprovar limpa as colunas que a v1.36 não usa mais (conta, corretora,
// conectores, limites e o GQ visto da v1.35); as outras ações copiam o que estava lá.
func Decidir(antes dominio.Habilitacao, existia bool, p Pedido, agora time.Time) (dominio.Habilitacao, string, *Recusa) {
	ok := false
	for _, a := range Acoes {
		ok = ok || a == p.Acao
	}
	if !ok {
		return antes, "", recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "acao %q (aprovar, bloquear, desbloquear, revogar)", p.Acao)
	}
	motivo := strings.TrimSpace(p.Motivo)
	if n := len([]rune(motivo)); n < 3 || n > 200 {
		return antes, "", recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "motivo obrigatorio, de 3 a 200 caracteres")
	}
	if strings.TrimSpace(p.Por) == "" {
		return antes, "", recusa(http.StatusBadRequest, "CAMPO_INVALIDO", "por obrigatorio (quem faz a acao)")
	}
	estado := antes.EstadoVigente()
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

	h := antes
	h.Conectores = append([]string(nil), antes.Conectores...)
	switch p.Acao {
	case AcaoAprovar:
		h.Habilitado, h.Estado = true, dominio.EstadoAprovado
		h.Conectores, h.Corretora, h.Conta, h.QtdMaxOrdem, h.PosicaoMax = nil, 0, "", 0, 0
		h.GQConector, h.GQCorretora, h.GQConta, h.DivergenteDesde = "", 0, "", time.Time{}
		h.AprovadoPor, h.AprovadoEm, h.AprovadoMotivo = p.Por, agora, motivo
	case AcaoBloquear:
		h.Habilitado, h.Estado = false, dominio.EstadoBloqueado
	case AcaoDesbloquear:
		h.Habilitado, h.Estado = true, dominio.EstadoAprovado
	case AcaoRevogar:
		h.Habilitado, h.Estado = false, dominio.EstadoRevogado
	}
	h.Versao = antes.Versao + 1
	h.Por, h.Motivo, h.Em, h.Origem = p.Por, motivo, agora, p.Origem
	return h, eventos[p.Acao], nil
}
