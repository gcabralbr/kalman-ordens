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
					"estado": o.Estado, "qtd": o.Qtd, "qtd_exec": o.QtdExec, "conector": op.conector()})
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
	r := Resposta{
		"versao":        Versao,
		"sessao_logada": m.fix != nil && m.fix.Logado(),
		"pnt_desligado": m.p.PnTDesligado,
		"conciliando":   m.conciliando,
		"posicao":       m.posicaoTotal(),
		// SPEC-FLEXSCAN-v1: o desligamento do pnt-md-hml (14:35) só olha o PnT
		"posicao_por_conector": m.posicaoPorConector(),
		"parada":               parada,
		"divergente":           m.divergente,
		"simbolo":              m.vigente,
		"simbolo_divergente":   m.algumaDivergente(),
		"ambiente":             m.p.Amb.Legado, // ORDENS_AMBIENTE (a equivalência da transição); vazio fora dela
		"ambientes_permitidos": m.p.Amb.Lista(),
		"idade_preco_ms":       idade,
		"ordens_vivas":         vivas,
		"alertas_criticos":     m.al.Ativos(),
		"fonte":                m.saudeFonte(),
		// v1.28 §3: o estado de cada conector e as operações suspensas (o Kalman não pede saída
		// nem ajuste enquanto o canal da operação estiver fora)
		"canais":              m.canaisSaude(),
		"operacoes_suspensas": m.suspensasSaude(),
		"limites": map[string]any{
			"entradas_hoje":                m.entradasHoje,
			"entradas_ultimo_minuto":       len(m.entradasRec),
			"mensagens_ultimo_minuto":      len(m.enviosRec),
			"recusas_limite_ultimo_minuto": len(m.recusasLimite),
			"ultima_entrada":               ult,
			"resultado_dia_pts":            m.resultadoDoDia(precoDia),
		},
	}
	// SPEC-FLEXSCAN-v1: o conector Flexscan (sessao_logada continua sendo a do PnT: o desligamento do
	// pnt-md-hml lê esse campo)
	// v1.30: no modo por corretora, um bloco por corretora em "flexscan_corretoras"
	porCor := map[string]any{}
	for _, c := range m.flxs {
		if c.e == nil {
			continue
		}
		f := map[string]any{"logado": c.e.Logado()}
		if s, ok := c.e.(interface{ Saude() map[string]any }); ok {
			for k, v := range s.Saude() {
				f[k] = v
			}
		}
		// v1.21 §3: a fonte da Flexscan sempre, com o mesmo detalhe da do PnT; com a fonte única
		// (sem PRECOS_URL_FLEXSCAN próprio, ou PNT_DESLIGADO), compartilhada e os valores do topo
		if m.precosFlx != nil {
			fo := m.saudeFonteFlx()
			fo["url"], fo["compartilhada"] = textoOuNulo(m.urlPrecosFlx), false
			f["simbolo"], f["fonte"] = m.vigenteFlx, fo
			f["idade_preco_ms"] = nil
			if lf := m.precosFlx.Ler(); lf.Situacao == preco.OK {
				f["idade_preco_ms"] = lf.Idade(agora).Milliseconds()
			}
		} else {
			fo := m.saudeFonte()
			fo["url"], fo["compartilhada"] = textoOuNulo(m.urlPrecos), true
			f["simbolo"], f["fonte"], f["idade_preco_ms"] = m.vigente, fo, idade
		}
		if c.nome == "" {
			r["flexscan"] = f
		} else {
			f["corretora_codigo"], f["contas"] = c.codigo, c.contas
			porCor[c.nome] = f
		}
	}
	if m.porCorretora() {
		r["flexscan_corretoras"] = porCor
	}
	return r
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
		"uid": op.UID, "conta": op.Conta, "destino": op.Destino, "conector": op.conector(), "ambiente": dominio.AmbienteDe(op.Ambiente), "simbolo": op.Simbolo,
		"preco_medio_entrada": pe, "preco_medio_saida": ps, "motivo_saida": op.MotivoSaida, "origem_saida": op.OrigemSaida,
		"pnl_pts": op.PnL, "preco_stop_ajustado": op.StopAjustado, "stop_origem": op.stopOrigem(), "ordens": os, "sinal": sinalDe(op),
		"suspensa": suspensaDe(op)}
}

// suspensaDe (v1.28 §3): nulo = a operação não está suspensa.
func suspensaDe(op *operacao) any {
	if op.SuspensaEm.IsZero() {
		return nil
	}
	return map[string]any{"desde": emBR(op.SuspensaEm), "motivo": op.SuspensaMotivo, "sugestao": nuloSeVazio(op.sugestao)}
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

func (m *Motor) posicaoPorConector() map[string]int {
	r := map[string]int{dominio.ConectorPnT: 0}
	if m.temFlexscan() {
		r[dominio.ConectorFlexscan] = 0
	}
	for _, op := range m.ops {
		if p := op.posicao(); p != 0 {
			r[op.conector()] += p
		}
	}
	return r
}

func (m *Motor) algumaDivergente() bool {
	for _, op := range m.ops {
		if !op.Estado.Final() && op.posicao() != 0 && m.divergenteOp(op) {
			return true
		}
	}
	return false
}
