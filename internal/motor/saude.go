package motor

import (
	"time"

	"gammaquant.com.br/kalman-ordens/internal/preco"
)

// Versao é o commit do binário em execução (definido pelo main).
var Versao = "dev"

// Saude (GET /saude).
func (m *Motor) Saude() Resposta {
	var r Resposta
	m.Fazer(func() { r = m.saude() })
	return r
}

func (m *Motor) saude() Resposta {
	agora := m.agora()
	m.enviosRec = aparar(m.enviosRec, agora.Add(-time.Minute))
	m.entradasRec = aparar(m.entradasRec, agora.Add(-time.Minute))
	m.recusasLimite = aparar(m.recusasLimite, agora.Add(-time.Minute))
	l := m.precos.Ler()
	var idade any
	if l.Situacao == preco.OK {
		idade = l.Idade(agora).Milliseconds()
	}
	var vivas []map[string]any
	for _, op := range m.ops {
		for _, o := range op.ordens {
			if !o.Estado.Final() {
				vivas = append(vivas, map[string]any{"clordid": o.ClOrdAtual, "orderid37": o.OrderID, "papel": o.Papel.String(),
					"estado": o.Estado, "qtd": o.Qtd, "qtd_exec": o.QtdExec})
			}
		}
	}
	var parada any
	if m.parada != nil {
		parada = m.parada
	}
	ult := ""
	if !m.ultimaEntrada.IsZero() {
		ult = m.ultimaEntrada.Format(time.RFC3339)
	}
	return Resposta{
		"versao":             Versao,
		"sessao_logada":      m.fix != nil && m.fix.Logado(),
		"conciliando":        m.conciliando,
		"posicao":            m.posicaoTotal(),
		"parada":             parada,
		"divergente":         m.divergente,
		"simbolo":            m.p.Simbolo,
		"simbolo_divergente": m.simboloDiv,
		"idade_preco_ms":     idade,
		"ordens_vivas":       vivas,
		"alertas_criticos":   m.al.Ativos(),
		"limites": map[string]any{
			"entradas_hoje":                m.entradasHoje,
			"entradas_ultimo_minuto":       len(m.entradasRec),
			"mensagens_ultimo_minuto":      len(m.enviosRec),
			"recusas_limite_ultimo_minuto": len(m.recusasLimite),
			"ultima_entrada":               ult,
			"resultado_dia_pts":            m.resultadoDoDia(m.precoConfiavel(l)),
		},
	}
}

func (m *Motor) descrever(op *operacao) Resposta {
	var os []map[string]any
	for _, o := range op.ordens {
		os = append(os, map[string]any{"papel": o.Papel.String(), "clordid": o.ClOrdAtual, "orderid37": o.OrderID, "estado": o.Estado,
			"qtd": o.Qtd, "qtd_exec": o.QtdExec, "preco": o.Preco, "preco_medio": o.PrecoMedio, "preco_stop": o.Stop, "preco_stop_lim": o.StopLim})
	}
	pe, _ := op.medio(true)
	ps, _ := op.medio(false)
	return Resposta{"existe": true, "operacao_id": op.OperacaoID, "estado": op.Estado, "divergente": op.Divergente, "posicao": op.posicao(),
		"preco_medio_entrada": pe, "preco_medio_saida": ps, "motivo_saida": op.MotivoSaida, "origem_saida": op.OrigemSaida,
		"pnl_pts": op.PnL, "ordens": os}
}
