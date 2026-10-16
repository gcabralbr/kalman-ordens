package motor

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// Disjuntor de bolsa indisponível (SPEC v1.10 §9). HML 16/10: a partir de 11:11 o PnT respondeu
// "Exchange is offline" a toda ordem e a saída de uma operação gerou 6.770 mensagens em 75 min.
//
// Entrada no estado: uma rejeição com um texto de DISJUNTOR_TEXTOS, ou DISJUNTOR_REJEICOES
// rejeições seguidas com o mesmo texto normalizado em DISJUNTOR_JANELA_S. No estado: entradas novas
// recusadas (PNT_BOLSA_INDISPONIVEL); saídas, cancels e 1014 retidas — uma sonda (a primeira
// retida, por prioridade) a cada DISJUNTOR_SONDA_S. Saída: o primeiro aceite ou execução limpos;
// as retidas voltam pela fila normal (1014 primeiro); entradas novas só depois de
// DISJUNTOR_ESTAVEL_S sem rejeição.

const chaveDisjuntor = "PNT_BOLSA_INDISPONIVEL"

type disjuntor struct {
	desde       time.Time              // zero = fora do estado
	texto       string                 // a rejeição que abriu o estado
	retidos     []envioPendente        // mensagens não enviadas no estado (ordem de decisão)
	proxSonda   time.Time              // próxima sonda
	sondas      int                    // sondas soltas no estado
	recuperada  time.Time              // a última saída do estado (D5)
	ultimaRej   time.Time              // a última rejeição (D5)
	iguais      map[string][]time.Time // rejeições seguidas por texto normalizado (regra genérica)
	retidasEver int
}

var reNumeros = regexp.MustCompile(`\d+`)

func normalizarRejeicao(t string) string {
	return strings.ToLower(strings.TrimSpace(reNumeros.ReplaceAllString(t, "#")))
}

// classificarRejeicao: a mensagem é uma rejeição (35=9; 35=8 com 150=8 ou 7, ou com "rejected" no
// 58), e se o texto é de gatilho. 150=I (mass status) não conta.
func (m *Motor) classificarRejeicao(msg pnt.Mensagem) (rejeicao, gatilho bool, texto string) {
	texto = msg.Texto(58)
	et := msg.Texto(150)
	baixo := strings.ToLower(texto)
	for _, g := range m.p.DisjuntorTextos {
		if g != "" && strings.Contains(baixo, strings.ToLower(g)) {
			gatilho = true
		}
	}
	if msg.Tipo == "8" && et == "I" {
		// mass status: o texto de gatilho ABRE (16/10: depois do restart, o "Exchange is offline"
		// só veio nos 150=I da conciliação); não entra na regra genérica e não fecha o estado
		return gatilho, gatilho, texto
	}
	switch {
	case msg.Tipo == "9":
		rejeicao = true
	case msg.Tipo == "8":
		rejeicao = et == "8" || et == "7" || strings.Contains(baixo, "rejected") || gatilho
	}
	return rejeicao, gatilho && rejeicao, texto
}

// observarBolsa: cada 35=8/35=9 do PnT, antes do tratamento normal.
func (m *Motor) observarBolsa(msg pnt.Mensagem) {
	if msg.Tipo != "8" && msg.Tipo != "9" {
		return
	}
	d := &m.bolsa
	agora := m.agora()
	rej, gat, texto := m.classificarRejeicao(msg)
	if rej {
		d.ultimaRej = agora
		if gat {
			m.abrirDisjuntor(texto, "texto de gatilho")
			return
		}
		// a regra genérica não conta rejeição de entrada: "sem margem" em cinco contas numa rajada é
		// problema de cada conta, não da bolsa (a bolsa fora rejeita também saídas e 1014)
		if m.p.DisjuntorRejeicoes > 0 && !m.ehEntrada(msg) {
			if d.iguais == nil {
				d.iguais = map[string][]time.Time{}
			}
			k := normalizarRejeicao(texto)
			l := aparar(append(d.iguais[k], agora), agora.Add(-m.p.DisjuntorJanela))
			d.iguais[k] = l
			if len(l) >= m.p.DisjuntorRejeicoes {
				m.abrirDisjuntor(texto, fmt.Sprintf("%d rejeições iguais em %s", len(l), m.p.DisjuntorJanela))
			}
		}
		return
	}
	// aceite (150=0 ou 5, sem texto de rejeição) ou execução: a bolsa responde
	if msg.Tipo == "8" {
		switch msg.Texto(150) {
		case "0", "5", "F":
			d.iguais = nil
			if !d.desde.IsZero() {
				m.fecharDisjuntor(fmt.Sprintf("%s 150=%s de %s", msg.Texto(35), msg.Texto(150), msg.Texto(11)))
			}
		}
	}
}

// ehEntrada: a rejeição é de uma ordem de entrada (pelo ClOrdID 11 ou 41).
func (m *Motor) ehEntrada(msg pnt.Mensagem) bool {
	if o := m.localizar(msg.Texto(37), msg.Texto(11), msg.Texto(41)); o != nil {
		return o.Papel == dominio.Entrada
	}
	return false
}

func (m *Motor) abrirDisjuntor(texto, porque string) {
	d := &m.bolsa
	if !d.desde.IsZero() {
		return
	}
	agora := m.agora()
	d.desde, d.texto, d.proxSonda, d.sondas = agora, texto, agora.Add(m.p.DisjuntorSonda), 0
	m.bolsaVista.Store(agora.UnixNano())
	metricas.Disjuntor.Inc("abriu")
	t := fmt.Sprintf("bolsa indisponível no PnT (%s): %q; entradas novas recusadas, saídas e 1014 em sonda a cada %s",
		porque, cortarTexto(texto, 200), m.p.DisjuntorSonda)
	slog.Error("disjuntor: "+t, "desde", agora)
	m.registrarSessao("BOLSA_FORA", t)
	m.al.Emitir(chaveDisjuntor, chaveDisjuntor, t, 0)
}

func (m *Motor) fecharDisjuntor(porque string) {
	d := &m.bolsa
	agora := m.agora()
	dur := agora.Sub(d.desde).Truncate(time.Second)
	t := fmt.Sprintf("bolsa voltou (%s) depois de %s e %d sonda(s); %d mensagem(ns) retida(s) voltam pela fila (1014 primeiro); entradas novas depois de %s sem rejeição",
		porque, dur, d.sondas, len(d.retidos), m.p.DisjuntorEstavel)
	slog.Warn("disjuntor: " + t)
	m.registrarSessao("BOLSA_VOLTOU", t)
	m.al.Resolver(chaveDisjuntor)
	metricas.Disjuntor.Inc("fechou")
	for _, e := range d.retidos {
		if e.o != nil {
			e.o.retida = false
		}
		e.daRetencao = true
		m.esperandoTaxa = append(m.esperandoTaxa, e)
	}
	d.retidos = nil
	d.desde, d.recuperada = time.Time{}, agora
	m.bolsaVista.Store(0)
	m.agendarVolta()
}

// bolsaRecusaEntrada: no estado, e até DISJUNTOR_ESTAVEL_S sem rejeição depois da volta (D5).
func (m *Motor) bolsaRecusaEntrada() *recusa {
	d := &m.bolsa
	if !d.desde.IsZero() {
		return simples(chaveDisjuntor, "bolsa indisponivel no PnT desde "+emBR(d.desde)+": "+cortarTexto(d.texto, 120))
	}
	if !d.recuperada.IsZero() && m.p.DisjuntorEstavel > 0 {
		base := d.recuperada
		if d.ultimaRej.After(base) {
			base = d.ultimaRej
		}
		if m.agora().Sub(base) < m.p.DisjuntorEstavel {
			return simples(chaveDisjuntor, fmt.Sprintf("bolsa voltou ha pouco: entradas novas depois de %s sem rejeicao", m.p.DisjuntorEstavel))
		}
	}
	return nil
}

// reter: no estado, a mensagem (não entrada, não mass status) não sai; fica para a sonda ou a volta.
func (m *Motor) reter(e envioPendente) bool {
	if m.bolsa.desde.IsZero() || e.o == nil || e.prio == prioEntrada {
		return false
	}
	e.o.retida = true
	m.bolsa.retidos = append(m.bolsa.retidos, e)
	m.bolsa.retidasEver++
	return true
}

// tickDisjuntor: a sonda — a primeira retida por prioridade (1014 antes), uma a cada
// DISJUNTOR_SONDA_S, direto à sessão (sem esperar a taxa).
func (m *Motor) tickDisjuntor() {
	d := &m.bolsa
	if d.desde.IsZero() || len(d.retidos) == 0 || m.agora().Before(d.proxSonda) {
		return
	}
	i := 0
	for j, e := range d.retidos {
		if e.prio < d.retidos[i].prio {
			i = j
		}
	}
	e := d.retidos[i]
	d.retidos = append(d.retidos[:i:i], d.retidos[i+1:]...)
	if e.o != nil {
		e.o.retida = false
	}
	d.sondas++
	d.proxSonda = m.agora().Add(m.p.DisjuntorSonda)
	metricas.Disjuntor.Inc("sonda")
	slog.Info("disjuntor: sonda", "clordid", e.clordid, "prioridade", nomePrio(e.prio), "retidas", len(d.retidos))
	m.despacharUm(e)
}

// retirarRetida: a ordem retida não sai mais (cancelada antes, por exemplo).
func (m *Motor) retirarRetida(o *ordem) bool {
	for i, e := range m.bolsa.retidos {
		if e.o == o {
			m.bolsa.retidos = append(m.bolsa.retidos[:i:i], m.bolsa.retidos[i+1:]...)
			o.retida = false
			return true
		}
	}
	return false
}
