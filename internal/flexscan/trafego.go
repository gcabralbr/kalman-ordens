package flexscan

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// Tráfego da ponte (SPEC-ORDENS v1.21 §1): as últimas trocas com a Flexscan, num buffer em memória,
// para o GET /flexscan/trafego (a aba "Flexscan ao vivo" do admin). Nada vai ao banco: o registro
// completo continua no kalmanordemevento. Sem segredo: o resumo é montado aqui, dos campos de
// negócio; token, senha, segredo do webhook e corpo cru nunca entram.
//
// Virada do dia (v1.23): o buffer é do dia corrente (relogio.Fuso). A cada linha nova, recarga e
// consulta, as linhas de antes da meia-noite saem (o banco guarda tudo; o dia passado sai pelo
// ?dia=) e recarregadas volta a zero. O seq continua a numeração (não volta a 1).

// CapacidadeTrafego: as últimas 2.000 linhas. LimitePadraoTrafego: o ?limite= ausente.
const (
	CapacidadeTrafego   = 2000
	LimitePadraoTrafego = 500
)

// Tipos de linha.
const (
	TrafegoPOST            = "POST"
	TrafegoPUT             = "PUT"
	TrafegoDELETE          = "DELETE"
	TrafegoConsulta        = "CONSULTA"
	TrafegoWebhook         = "WEBHOOK"
	TrafegoPreco           = "FLX-PRECO"
	TrafegoLogin           = "LOGIN"
	TrafegoWebhookCadastro = "WEBHOOK_CADASTRO"
)

const formatoEmTrafego = "2006-01-02T15:04:05.000-07:00"

// LinhaTrafego: uma linha do contrato. OperacaoID, Modulo e UID são preenchidos por quem consulta
// (o motor conhece as operações); a ponte só sabe o clordid.
type LinhaTrafego struct {
	Seq        int64   `json:"seq"`
	Em         string  `json:"em"`
	Tipo       string  `json:"tipo"`
	Direcao    string  `json:"direcao"`
	StatusHTTP *int    `json:"status_http"`
	DuracaoMs  *int64  `json:"duracao_ms"`
	ClOrdID    *string `json:"clordid"`
	FlexscanID *string `json:"flexscan_id"`
	OperacaoID *int64  `json:"operacao_id"`
	Modulo     *string `json:"modulo"`
	UID        *int    `json:"uid"`
	Resumo     string  `json:"resumo"`
	// ClOrdIDs (v1.21.1): os clordid dos retratos de um webhook com vários (ClOrdID fica null); quem
	// consulta preenche a operação se todos forem da mesma.
	ClOrdIDs []string `json:"-"`

	em time.Time
}

type trafego struct {
	rel  relogio.Relogio
	boot time.Time

	mu        sync.Mutex
	buf       [CapacidadeTrafego]LinhaTrafego
	ini, n    int
	seq       int64
	consultas []time.Time // as do último minuto, inclusive as que não viram linha
	// linhas recarregadas do kalmanordemevento na partida (as trocas do dia antes do restart)
	recarregadas int
	dia          time.Time // a meia-noite do dia do buffer
}

func novoTrafego(rel relogio.Relogio) *trafego {
	agora := rel.Agora()
	return &trafego{rel: rel, boot: agora, dia: meiaNoite(agora)}
}

func meiaNoite(t time.Time) time.Time {
	t = t.In(relogio.Fuso)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, relogio.Fuso)
}

// virar (com t.mu): no dia novo, descarta as linhas de antes da meia-noite (as mais antigas estão no
// começo) e zera recarregadas (as recarregadas são todas do dia da partida).
func (t *trafego) virar(agora time.Time) {
	hoje := meiaNoite(agora)
	if !hoje.After(t.dia) {
		return
	}
	t.dia = hoje
	for t.n > 0 && t.buf[t.ini].em.Before(hoje) {
		t.buf[t.ini] = LinhaTrafego{}
		t.ini = (t.ini + 1) % CapacidadeTrafego
		t.n--
	}
	if t.n == 0 {
		t.ini = 0
	}
	t.recarregadas = 0
}

// linha: status < 0 = sem resposta (null); dur < 0 = sem duração (null); cl/id vazios = null; nomes =
// os clordid de um webhook com vários retratos.
func (t *trafego) linha(tipo, direcao string, status int, dur time.Duration, cl, id, resumo string, nomes ...string) {
	l := LinhaTrafego{Tipo: tipo, Direcao: direcao, Resumo: resumoTrafego(resumo), ClOrdIDs: nomes}
	if status >= 0 {
		s := status
		l.StatusHTTP = &s
	}
	if dur >= 0 {
		ms := dur.Milliseconds()
		l.DuracaoMs = &ms
	}
	if cl != "" {
		c := cl
		l.ClOrdID = &c
	}
	if id != "" {
		i := id
		l.FlexscanID = &i
	}
	agora := t.rel.Agora()
	l.em = agora
	l.Em = agora.In(relogio.Fuso).Format(formatoEmTrafego)
	t.mu.Lock()
	t.virar(agora)
	t.seq++
	l.Seq = t.seq
	pos := (t.ini + t.n) % CapacidadeTrafego
	if t.n == CapacidadeTrafego {
		t.ini = (t.ini + 1) % CapacidadeTrafego
	} else {
		t.n++
	}
	t.buf[pos] = l
	t.mu.Unlock()
}

// consulta: uma consulta feita (GET /orders/{id} ou /orders/filter), vire ela linha ou não.
func (t *trafego) consulta() {
	agora := t.rel.Agora()
	t.mu.Lock()
	t.consultas = append(aparar(t.consultas, agora.Add(-time.Minute)), agora)
	t.mu.Unlock()
}

func aparar(ts []time.Time, desde time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(desde) {
		i++
	}
	return ts[i:]
}

// Consultar: as linhas com seq > desde (no máximo limite, das mais antigas), o último seq e os
// números do bloco saude (msgs_min, consultas_min, latência dos POSTs no buffer, boot).
func (t *trafego) Consultar(desde int64, limite int) ([]LinhaTrafego, int64, map[string]any) {
	if limite <= 0 {
		limite = LimitePadraoTrafego
	}
	limite = min(limite, CapacidadeTrafego)
	agora := t.rel.Agora()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.virar(agora)
	lista := make([]LinhaTrafego, 0, min(limite, t.n))
	minuto := 0
	var posts []int64
	for i := 0; i < t.n; i++ {
		l := t.buf[(t.ini+i)%CapacidadeTrafego]
		if l.Seq > desde && len(lista) < limite {
			lista = append(lista, l)
		}
		if l.em.After(agora.Add(-time.Minute)) {
			minuto++
		}
		if l.Tipo == TrafegoPOST && l.DuracaoMs != nil {
			posts = append(posts, *l.DuracaoMs)
		}
	}
	t.consultas = aparar(t.consultas, agora.Add(-time.Minute))
	var p50, max any
	if len(posts) > 0 {
		sort.Slice(posts, func(i, j int) bool { return posts[i] < posts[j] })
		p50, max = posts[(len(posts)-1)/2], posts[len(posts)-1]
	}
	return lista, t.seq, map[string]any{"boot": t.boot.In(relogio.Fuso).Format(formatoEmTrafego), "dia": t.dia.Format("2006-01-02"), "recarregadas": t.recarregadas, "msgs_min": minuto,
		"consultas_min": len(t.consultas), "latencia_post_p50_ms": p50, "latencia_post_max_ms": max}
}

// resumoTrafego: ASCII, uma linha, até 200 caracteres.
func resumoTrafego(s string) string {
	s = strings.Join(strings.Fields(alerta.ASCII(s)), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// resumoOrdem: o corpo do POST/PUT em palavras (sem a conta inteira? a conta não é segredo).
func resumoOrdem(corpo map[string]any) string {
	var partes []string
	switch fmt.Sprint(corpo["side"]) {
	case "1":
		partes = append(partes, "C")
	case "2":
		partes = append(partes, "V")
	}
	if v, ok := corpo["qtty"]; ok {
		partes = append(partes, fmt.Sprint(v))
	}
	if v, ok := corpo["symbol"]; ok {
		partes = append(partes, fmt.Sprint(v))
	}
	if v, ok := corpo["stopPrice"]; ok {
		partes = append(partes, "gatilho "+fmt.Sprint(v))
	}
	if v, ok := corpo["price"]; ok {
		partes = append(partes, "limite "+fmt.Sprint(v))
	}
	if v, ok := corpo["account"]; ok {
		partes = append(partes, "conta "+fmt.Sprint(v))
	}
	return strings.Join(partes, " ")
}

// resumoEstado: um retrato ou consulta em palavras.
func resumoEstado(e Estado) string {
	s := fmt.Sprintf("%s exec %d/%d", e.Fase, e.Exec, e.Qtd)
	if !e.Stop.IsZero() {
		s += " gatilho " + e.Stop.String()
	}
	if !e.Preco.IsZero() {
		s += " limite " + e.Preco.String()
	}
	if e.Texto != "" {
		s += " (" + e.Texto + ")"
	}
	return s
}

// comResposta: o resumo do pedido mais o erro da resposta (nada no 2xx).
func comResposta(resumo string, r Resposta) string {
	switch {
	case r.Status == 0:
		return strings.TrimSpace(resumo + " -> sem resposta: " + r.TextoErro())
	case r.Status >= 300:
		return strings.TrimSpace(resumo + " -> " + r.TextoErro())
	}
	return resumo
}

// statusDe: o status HTTP da linha (-1 = sem resposta).
func statusDe(r Resposta) int {
	if r.Status == 0 {
		return -1
	}
	return r.Status
}
