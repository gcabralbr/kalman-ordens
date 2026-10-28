package api

import (
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Latência dos pedidos do gestor (SPEC-ORDENS v1.22 §3): do pedido recebido à resposta escrita, por
// rota (STOP, ENTRADA, SAIDA, SINAIS). O /saude mostra o p50 e o máximo das últimas
// janelaLatencia respostas e o máximo do dia, com a hora; acima de respostaLenta, WARN no journal.
// O histograma http_segundos (todas as rotas) continua no /metrics.

const (
	janelaLatencia = 500
	respostaLenta  = time.Second
)

// rotulosLatencia: as rotas do mux que entram no /saude (o POST /ordens é rotulado pelo papel).
var rotulosLatencia = map[string]string{
	"POST /operacoes/{id}/stop": "STOP",
	"POST /sinais":              "SINAIS",
	"POST /ordens":              "ORDENS",
}

type serieLatencia struct {
	ultimas  [janelaLatencia]time.Duration
	ini, n   int
	maxDia   time.Duration
	maxEm    time.Time
	dia      string
	contagem int64
}

type latencias struct {
	mu     sync.Mutex
	series map[string]*serieLatencia
}

func novasLatencias() *latencias { return &latencias{series: map[string]*serieLatencia{}} }

func (l *latencias) observar(rotulo string, d time.Duration, status int, agora time.Time) {
	if d >= respostaLenta {
		slog.Warn("resposta HTTP lenta", "rota", rotulo, "duracao_ms", d.Milliseconds(), "status", status)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.series[rotulo]
	if s == nil {
		s = &serieLatencia{}
		l.series[rotulo] = s
	}
	pos := (s.ini + s.n) % janelaLatencia
	if s.n == janelaLatencia {
		s.ini = (s.ini + 1) % janelaLatencia
	} else {
		s.n++
	}
	s.ultimas[pos] = d
	s.contagem++
	if dia := agora.Format("2006-01-02"); dia != s.dia {
		s.dia, s.maxDia, s.maxEm = dia, 0, time.Time{}
	}
	if d > s.maxDia {
		s.maxDia, s.maxEm = d, agora
	}
}

// resumo: o bloco http do /saude.
func (l *latencias) resumo(agora time.Time) map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := map[string]any{}
	for rotulo, s := range l.series {
		ds := make([]time.Duration, s.n)
		for i := range s.n {
			ds[i] = s.ultimas[(s.ini+i)%janelaLatencia]
		}
		slices.Sort(ds)
		b := map[string]any{"n": s.n, "p50_ms": ms(ds[len(ds)/2]), "max_ms": ms(ds[len(ds)-1]), "max_dia_ms": nil, "max_dia_em": nil}
		if s.dia == agora.Format("2006-01-02") {
			b["max_dia_ms"], b["max_dia_em"] = ms(s.maxDia), s.maxEm.Format("2006-01-02T15:04:05.000-07:00")
		}
		r[rotulo] = b
	}
	return r
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// rotular: o handler do POST /ordens diz o papel (ENTRADA, SAIDA) depois de ler o corpo.
func rotular(w http.ResponseWriter, rotulo string) {
	if c, ok := w.(*comStatus); ok && rotulo != "" {
		c.rotulo = rotulo
	}
}
