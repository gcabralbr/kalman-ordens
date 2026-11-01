package motor

import (
	"fmt"
	"strconv"
	"strings"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// Boleta do admin (SPEC-ORDENS-v1.31): a operação aberta sem o Kalman, pelo mesmo caminho do
// POST /ordens (modulo BOLETA). O uid é o do cliente, com todas as travas e limites dele; o admin
// fica no "por", e a conta e a corretora informadas conferem a coluna do GQ.

// camposDaBoleta: por, conta e corretora só existem na boleta; nela, o por é obrigatório e a conta
// e a corretora só valem na ENTRADA.
func camposDaBoleta(p Pedido) *recusa {
	if p.modulo() != dominio.ModuloBoleta {
		if p.Por != nil || p.Conta != "" || p.Corretora != "" {
			return simples("CAMPO_INVALIDO", "por, conta e corretora so na boleta (modulo BOLETA)")
		}
		return nil
	}
	if p.Por == nil || *p.Por <= 0 {
		return simples("CAMPO_INVALIDO", "por (uid do admin) obrigatorio na boleta")
	}
	if p.Papel != "ENTRADA" && (p.Conta != "" || p.Corretora != "") {
		return simples("CAMPO_INVALIDO", "conta e corretora so na ENTRADA da boleta (a saida vale o gravado)")
	}
	return nil
}

// destinoDaBoleta: a conta e a corretora que o admin espera batem com a coluna do GQ resolvida? A
// corretora vale pelo nome (BTG) ou pelo código (85).
func (m *Motor) destinoDaBoleta(p Pedido, c usuario.Resolvido) *recusa {
	if p.modulo() != dominio.ModuloBoleta {
		return nil
	}
	nome := m.nomeCorretora(c.Destino)
	if p.Corretora != "" && !strings.EqualFold(p.Corretora, nome) && p.Corretora != strconv.Itoa(c.Destino) {
		return simples("BOLETA_DESTINO_DIVERGENTE", fmt.Sprintf("a boleta pede a corretora %s e a coluna do GQ do uid aponta %s (%d)", p.Corretora, nome, c.Destino))
	}
	if p.Conta != "" && p.Conta != c.Conta {
		return simples("BOLETA_DESTINO_DIVERGENTE", fmt.Sprintf("a boleta pede a conta %s e a coluna do GQ do uid aponta %s", p.Conta, c.Conta))
	}
	return nil
}

// avisarBoleta: toda boleta aceita no REAL avisa (BOLETA_REAL, informativo).
func (m *Motor) avisarBoleta(p Pedido, op *operacao) {
	if op.Modulo != dominio.ModuloBoleta || op.Ambiente != dominio.AmbienteReal {
		return
	}
	lado := "compra"
	if op.Lado == dominio.Venda {
		lado = "venda"
	}
	m.al.Emitir("BOLETA_REAL", "", fmt.Sprintf("boleta %d do admin %d: %s %d %s no REAL, uid %d, %s conta %s (%s)",
		op.OperacaoID, *p.Por, lado, op.QtdPedida, op.Simbolo, op.UID, m.nomeCorretora(op.Destino), op.Conta, p.Motivo), op.OperacaoID)
}
