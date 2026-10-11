package motor

import (
	"fmt"
	"log/slog"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/pnt"
)

func (m *Motor) aoLogar() {
	if m.jaLogou {
		m.al.Emitir("SESSAO_VOLTOU", "", "sessão FIX voltou", 0)
	}
	m.jaLogou = true
	m.conciliando = true
	m.massTentativas = 0
	m.pedirMassStatus()
}

var contadorMass int

// pedirMassStatus: um AF (585=9) por conta (v1.2 U6) — as contas das operações do dia e as dos
// usuários habilitados (resolvidas no ambiente, com a trava cruzada). Sem conta: nada a pedir, a
// conciliação fecha no silêncio.
func (m *Motor) pedirMassStatus() {
	m.massReqIDs = map[string]bool{}
	m.massRespostas = nil
	m.massCorte = m.seqOrdens
	m.massUltima = m.agora()
	m.massProxima = timeZero
	contas := m.contasMassStatus()
	if len(contas) == 0 {
		slog.Info("mass status: nenhuma conta (sem operação no dia nem usuário habilitado)")
	}
	for _, c := range contas {
		contadorMass++
		id := fmt.Sprintf("KM-MS-%d-%d", m.agora().UnixMilli(), contadorMass)
		m.massReqIDs[id] = true
		if !m.enviar(nil, pnt.MassStatus(c, id), id) {
			m.massProxima = m.agora().Add(m.p.RetentativaProtecao)
		}
	}
}

func (m *Motor) contasMassStatus() []string {
	vistas := map[string]bool{}
	var r []string
	add := func(c string) {
		if c != "" && !vistas[c] {
			vistas[c] = true
			r = append(r, c)
		}
	}
	for _, op := range m.ops {
		add(op.Conta)
	}
	hs, err := m.repo.Habilitacoes()
	if err != nil {
		slog.Warn("mass status: habilitações ilegíveis; só as contas das operações do dia", "erro", err)
	}
	for _, h := range hs {
		if !h.Habilitado {
			continue
		}
		if res, f := m.contaDoUsuario(h.UID); f == nil {
			add(res.Conta)
		} else {
			slog.Warn("mass status: conta do uid habilitado não resolvida", "uid", h.UID, "codigo", f.Codigo, "motivo", f.Motivo)
		}
	}
	return r
}

// conciliar: Parte 7.2, passos 2 a 5.
func (m *Motor) conciliar() {
	m.conciliando = false
	vistas := map[*ordem]bool{}
	ignoradas, divergencias := 0, 0
	for _, r := range m.massRespostas {
		if r == nil {
			ignoradas++
			continue
		}
		o := m.localizar(r.Texto(37), r.Texto(11), r.Texto(41))
		if o == nil {
			divergencias++
			m.registrarRecebida(nil, *r)
			m.al.Emitir("DESCONHECIDA", "", fmt.Sprintf("ordem KM no PnT sem registro: 11=%s 37=%s", r.Texto(11), r.Texto(37)), 0)
			continue
		}
		vistas[o] = true
		if m.registrarRecebida(o, *r) {
			m.aplicarExecReport(m.donoDa[o], o, *r)
		}
	}
	for _, op := range m.ops {
		for _, o := range op.ordens {
			// só julga as que já existiam quando o mass status foi pedido
			if !o.viva() || vistas[o] || o.seq > m.massCorte {
				continue
			}
			if o.decidirNoMass {
				// 1014 em erro com cancel rejeitado e ausente no mass status: não existe mais (F17)
				o.decidirNoMass = false
				o.Estado = dominio.Desconhecida
				limparPendencia(o)
				m.salvarOrdem(o)
				m.recriarProtecaoAusente(op, "1014 em erro ausente no mass status")
				continue
			}
			// [PnT-13] o mass status traz só as ativas: viva aqui e ausente lá
			divergencias++
			op.Divergente = true
			m.al.Emitir("DESCONHECIDA", "", fmt.Sprintf("ordem viva na tabela e ausente no PnT: %s", o.ClOrdAtual), op.OperacaoID)
			o.Estado = dominio.Desconhecida
			limparPendencia(o)
			m.salvarOrdem(o)
			if o.Papel == dominio.Entrada {
				m.encerrarEntrada(op, "DESCONHECIDA", true)
			}
		}
	}
	for _, op := range m.ops {
		for _, o := range op.ordens {
			if o.decidirNoMass {
				o.decidirNoMass = false
				m.decidirProtecaoEmErro(op, o)
			}
		}
	}
	// posição ≠ 0 sem 1014 viva → cria a 1014 (7.2.4)
	for _, op := range m.ops {
		if op.Estado.Final() || op.Estado.Saindo() || op.posicao() == 0 || op.viva(dominio.Protecao) != nil {
			continue
		}
		m.mudar(op, dominio.PosicionadaSemProtecao)
		op.semProtecaoDesde = m.agora()
		m.ajustarProtecao(op)
	}
	for _, op := range m.ops {
		m.al.Resolver(chaveOp("SEM_CONFIRMACAO", op))
		m.salvarOp(op)
	}
	m.al.Resolver("SESSAO_REJEITOU_MENSAGEM")
	m.al.Resolver("MENSAGEM_ILEGIVEL")
	m.al.Resolver("MASS_STATUS_REJEITADO")
	if divergencias > 0 {
		m.divergente = true
		m.al.Emitir("DIVERGENTE", "DIVERGENTE", fmt.Sprintf("conciliação com %d divergência(s)", divergencias), 0)
		m.ativarParada("BLOQUEAR", "auto", fmt.Sprintf("conciliação com %d divergência(s)", divergencias))
	}
	texto := fmt.Sprintf("%d ordem(ns) KM, %d de outros (ignoradas), %d divergência(s); posição %d", len(vistas), ignoradas, divergencias, m.posicaoTotal())
	m.al.Emitir("CONCILIACAO", "", texto, 0)
	slog.Info("conciliação", "resumo", texto)
}
