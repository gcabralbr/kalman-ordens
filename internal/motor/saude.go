package motor

import (
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
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
	precoDia, _ := m.precoDoDia(l)
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
		"simbolo":            m.vigente,
		"simbolo_divergente": m.algumaDivergente(),
		"ambiente":           m.p.Ambiente,
		"idade_preco_ms":     idade,
		"ordens_vivas":       vivas,
		"alertas_criticos":   m.al.Ativos(),
		"limites": map[string]any{
			"entradas_hoje":                m.entradasHoje,
			"entradas_ultimo_minuto":       len(m.entradasRec),
			"mensagens_ultimo_minuto":      len(m.enviosRec),
			"recusas_limite_ultimo_minuto": len(m.recusasLimite),
			"ultima_entrada":               ult,
			"resultado_dia_pts":            m.resultadoDoDia(precoDia),
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
	return Resposta{"existe": true, "modulo": op.Modulo, "operacao_id": op.OperacaoID, "estado": op.Estado, "divergente": op.Divergente, "posicao": op.posicao(),
		"uid": op.UID, "conta": op.Conta, "destino": op.Destino, "simbolo": op.Simbolo,
		"preco_medio_entrada": pe, "preco_medio_saida": ps, "motivo_saida": op.MotivoSaida, "origem_saida": op.OrigemSaida,
		"pnl_pts": op.PnL, "preco_stop_ajustado": op.StopAjustado, "ordens": os, "sinal": sinalDe(op)}
}

// sinalDe (v1.10 §6): a posição da entrada e da saída nas rajadas dos sinais; nulo = avulsa.
func sinalDe(op *operacao) any {
	if op.SinalEntrada.Vazia() && op.SinalSaida.Vazia() {
		return nil
	}
	f := func(r dominio.Rajada) any {
		if r.Vazia() {
			return nil
		}
		x := map[string]any{"sinal_id": r.SinalID, "k": nil, "posicao": nil, "tamanho": nil, "deslocamento": nil, "criterio": r.Criterio}
		if r.Posicao > 0 {
			x["k"], x["posicao"], x["tamanho"], x["deslocamento"] = r.K, r.Posicao, r.Tamanho, r.Deslocamento
		}
		return x
	}
	return map[string]any{"entrada": f(op.SinalEntrada), "saida": f(op.SinalSaida)}
}

func (m *Motor) algumaDivergente() bool {
	for _, op := range m.ops {
		if !op.Estado.Final() && op.posicao() != 0 && m.divergenteOp(op) {
			return true
		}
	}
	return false
}
