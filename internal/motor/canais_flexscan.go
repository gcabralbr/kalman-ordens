package motor

import (
	"fmt"
	"strconv"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// v1.30 (SPEC-AMBIENTES-v1 D4 revisada, passo 2b): a instância tem uma ponte Flexscan só (a de
// sempre) ou, no REAL com REAL_FLEXSCAN_CORRETORAS, uma por corretora (um usuário guarda-chuva por
// corretora, com a conta do cliente em cada ordem). A operação usa a ponte da corretora GRAVADA na
// entrada (Robo.CORSeqId, op.Destino), nunca a do momento. Cada ponte é um canal: "PNT", "FLEXSCAN"
// (a única) ou "FLEXSCAN:CMC" — a chave da sessão, da queda, da conciliação e do /saude.

// canalFlx: uma ponte Flexscan da instância.
type canalFlx struct {
	chave   string // FLEXSCAN ou FLEXSCAN:<NOME>
	nome    string // vazio = a ponte única
	codigo  int    // o CORSeqId (0 = a única)
	contas  []string
	e       Envio
	jaLogou bool
}

func chaveFlx(nome string) string {
	if nome == "" {
		return dominio.ConectorFlexscan
	}
	return dominio.ConectorFlexscan + ":" + nome
}

// temFlexscan: a instância tem alguma ponte Flexscan.
func (m *Motor) temFlexscan() bool { return len(m.flxs) > 0 }

// porCorretora: as pontes Flexscan são por corretora (REAL_FLEXSCAN_CORRETORAS).
func (m *Motor) porCorretora() bool { return len(m.flxs) > 0 && m.flxs[0].nome != "" }

func (m *Motor) canalFlxDe(chave string) *canalFlx {
	for _, c := range m.flxs {
		if c.chave == chave {
			return c
		}
	}
	return nil
}

func (m *Motor) canalFlxDoCodigo(codigo int) *canalFlx {
	for _, c := range m.flxs {
		if c.codigo == codigo {
			return c
		}
	}
	return nil
}

// canalFlxDoNome: a ponte da corretora pelo nome (vazio = a única).
func (m *Motor) canalFlxDoNome(nome string) *canalFlx { return m.canalFlxDe(chaveFlx(nome)) }

// chaveDe: o canal de um conector numa corretora — o PnT; a Flexscan única; a Flexscan da corretora
// (sem credencial dela: um canal que não existe, DESLIGADO).
func (m *Motor) chaveDe(conector string, destino int) string {
	if conector != dominio.ConectorFlexscan {
		return dominio.ConectorPnT
	}
	if !m.porCorretora() {
		return dominio.ConectorFlexscan
	}
	if c := m.canalFlxDoCodigo(destino); c != nil {
		return c.chave
	}
	return dominio.ConectorFlexscan + ":" + strconv.Itoa(destino)
}

// canalOp: o canal da operação, pelo conector e pela corretora gravados na entrada.
func (m *Motor) canalOp(op *operacao) string { return m.chaveDe(op.conector(), op.Destino) }

// chavesCanais: os canais da instância (o /saude e o tickCanais).
func (m *Motor) chavesCanais() []string {
	r := []string{dominio.ConectorPnT}
	if len(m.flxs) == 0 {
		return append(r, dominio.ConectorFlexscan)
	}
	for _, c := range m.flxs {
		r = append(r, c.chave)
	}
	return r
}

// nomeCanal: o canal nos textos ("PnT", "Flexscan", "Flexscan CMC").
func nomeCanal(chave string) string {
	switch {
	case chave == dominio.ConectorPnT:
		return "PnT"
	case chave == dominio.ConectorFlexscan:
		return "Flexscan"
	}
	return "Flexscan " + chave[len(dominio.ConectorFlexscan)+1:]
}

// recusaCorretora (v1.30): no modo por corretora, a corretora da coluna do GQ tem de ter um
// guarda-chuva e a conta tem de estar na lista dele (a entrada e o /destino).
func (m *Motor) recusaCorretora(conector string, destino int, conta string) *recusa {
	if conector != dominio.ConectorFlexscan || !m.porCorretora() {
		return nil
	}
	c := m.canalFlxDoCodigo(destino)
	if c == nil {
		return simples("CORRETORA_SEM_CREDENCIAL", fmt.Sprintf("sem credencial Flexscan para a corretora %d nesta maquina (REAL_FLEXSCAN_CORRETORAS)", destino))
	}
	for _, x := range c.contas {
		if x == conta {
			return nil
		}
	}
	return simples("CONTA_FORA_DA_CORRETORA", fmt.Sprintf("conta %s fora das contas permitidas no usuario Flexscan da %s (REAL_FLEXSCAN_%s_CONTAS)", conta, c.nome, c.nome))
}

// NovoCanalFlexscan: registra a ponte de uma corretora (antes de criá-la) e devolve o ouvinte dela.
func (m *Motor) NovoCanalFlexscan(codigo int, nome string, contas []string) *OuvinteFlexscan {
	c := &canalFlx{chave: chaveFlx(nome), nome: nome, codigo: codigo, contas: contas}
	m.flxs = append(m.flxs, c)
	return &OuvinteFlexscan{m: m, c: c}
}

// OuvinteFlexscan: o ouvinte da ponte única (criada se ainda não houver).
func (m *Motor) OuvinteFlexscan() *OuvinteFlexscan {
	c := m.canalFlxDoNome("")
	if c == nil {
		c = &canalFlx{chave: dominio.ConectorFlexscan}
		m.flxs = append(m.flxs, c)
	}
	return &OuvinteFlexscan{m: m, c: c}
}

// UsarFlexscan: a ponte única.
func (m *Motor) UsarFlexscan(e Envio) {
	m.OuvinteFlexscan()
	m.LigarFlexscan("", e)
}

// LigarFlexscan: a ponte da corretora (vazio = a única), já registrada.
func (m *Motor) LigarFlexscan(nome string, e Envio) {
	c := m.canalFlxDoNome(nome)
	if c == nil {
		panic("LigarFlexscan sem NovoCanalFlexscan: " + nome)
	}
	c.e = e
	// v1.28: a agenda da ponte usa o dia de pregão do motor (com os feriados)
	if cal, ok := e.(interface{ UsarCalendario(func(time.Time) bool) }); ok {
		cal.UsarCalendario(func(t time.Time) bool { ok, _ := m.pregao.DiaDePregao(t); return ok })
	}
}

// marcarVivasFlexscan (v1.28): cada ponte fica ativa fora da janela enquanto houver operação não
// encerrada nela.
func (m *Motor) marcarVivasFlexscan() {
	for _, c := range m.flxs {
		mv, ok := c.e.(interface{ MarcarVivas(bool) })
		if !ok {
			continue
		}
		vivas := false
		for _, op := range m.ops {
			if !op.Estado.Final() && m.canalOp(op) == c.chave {
				vivas = true
				break
			}
		}
		mv.MarcarVivas(vivas)
	}
}

// corretorasDeclaradas (v1.30): as de REAL_FLEXSCAN_CORRETORAS (CORSeqId → NOME); qualquer CORSeqId
// declarado ali vale, além das de sempre (85 BTG, 88 Capital): corretora nova sem binário novo.
func (m *Motor) corretorasDeclaradas() map[int]string {
	if !m.porCorretora() {
		return nil
	}
	r := map[int]string{}
	for _, c := range m.flxs {
		r[c.codigo] = c.nome
	}
	return r
}

// nomeCorretora: o nome nos textos e no /destino (as de sempre; depois, as declaradas).
func (m *Motor) nomeCorretora(destino int) string {
	return usuario.NomeCorretora(destino, m.corretorasDeclaradas())
}
