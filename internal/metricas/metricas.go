// Package metricas: contadores, medidores e histogramas no formato de texto do Prometheus
// (GET /metrics, só 127.0.0.1). Sem dependência externa; registro global do processo.
package metricas

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Prefixo de todas as métricas do serviço.
const Prefixo = "kalman_ordens_"

// BaldesSegundos: de 0,5 ms a 60 s (filas, FIX, banco, HTTP).
var BaldesSegundos = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

type metrica interface {
	escrever(w io.Writer)
	nomeCompleto() string
}

var (
	muRegistro sync.Mutex
	registro   []metrica
)

func registrar(m metrica) {
	muRegistro.Lock()
	defer muRegistro.Unlock()
	registro = append(registro, m)
}

// Escrever: todas as métricas registradas, em ordem de nome.
func Escrever(w io.Writer) {
	muRegistro.Lock()
	ms := append([]metrica(nil), registro...)
	muRegistro.Unlock()
	sort.Slice(ms, func(i, j int) bool { return ms[i].nomeCompleto() < ms[j].nomeCompleto() })
	for _, m := range ms {
		m.escrever(w)
	}
}

func chave(vs []string) string { return strings.Join(vs, "\x00") }

func rotulos(nomes, valores []string, extra ...string) string {
	var p []string
	for i, n := range nomes {
		v := ""
		if i < len(valores) {
			v = valores[i]
		}
		p = append(p, n+`="`+escapar(v)+`"`)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		p = append(p, extra[i]+`="`+extra[i+1]+`"`)
	}
	if len(p) == 0 {
		return ""
	}
	return "{" + strings.Join(p, ",") + "}"
}

func escapar(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func num(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// ---------------------------------------------------------------- contador

// Contador: só cresce, por combinação de rótulos.
type Contador struct {
	nome, ajuda string
	rotulos     []string
	mu          sync.Mutex
	series      map[string]*serieC
}

type serieC struct {
	valores []string
	v       float64
}

func NovoContador(nome, ajuda string, rotulos ...string) *Contador {
	c := &Contador{nome: Prefixo + nome, ajuda: ajuda, rotulos: rotulos, series: map[string]*serieC{}}
	registrar(c)
	return c
}

func (c *Contador) Somar(v float64, valores ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := chave(valores)
	s := c.series[k]
	if s == nil {
		s = &serieC{valores: append([]string(nil), valores...)}
		c.series[k] = s
	}
	s.v += v
}

func (c *Contador) Inc(valores ...string) { c.Somar(1, valores...) }

// Valor (testes).
func (c *Contador) Valor(valores ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.series[chave(valores)]; s != nil {
		return s.v
	}
	return 0
}

func (c *Contador) nomeCompleto() string { return c.nome }

func (c *Contador) escrever(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", c.nome, c.ajuda, c.nome)
	ks := make([]string, 0, len(c.series))
	for k := range c.series {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		s := c.series[k]
		fmt.Fprintf(w, "%s%s %s\n", c.nome, rotulos(c.rotulos, s.valores), num(s.v))
	}
}

// ---------------------------------------------------------------- medidor

// Medidor: valor lido na hora da coleta (a função tem de ser segura entre goroutines).
type Medidor struct {
	nome, ajuda string
	ler         func() float64
}

func NovoMedidor(nome, ajuda string, ler func() float64) *Medidor {
	m := &Medidor{nome: Prefixo + nome, ajuda: ajuda, ler: ler}
	registrar(m)
	return m
}

func (m *Medidor) nomeCompleto() string { return m.nome }

func (m *Medidor) escrever(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", m.nome, m.ajuda, m.nome, m.nome, num(m.ler()))
}

// ---------------------------------------------------------------- histograma

// Histograma de durações (segundos), por combinação de rótulos.
type Histograma struct {
	nome, ajuda string
	rotulos     []string
	baldes      []float64
	mu          sync.Mutex
	series      map[string]*serieH
}

type serieH struct {
	valores   []string
	contagens []uint64 // por balde (não acumulado)
	soma      float64
	n         uint64
}

func NovoHistograma(nome, ajuda string, rotulos ...string) *Histograma {
	return NovoHistogramaBaldes(nome, ajuda, BaldesSegundos, rotulos...)
}

// NovoHistogramaBaldes: com baldes próprios (contagens, não segundos).
func NovoHistogramaBaldes(nome, ajuda string, baldes []float64, rotulos ...string) *Histograma {
	h := &Histograma{nome: Prefixo + nome, ajuda: ajuda, rotulos: rotulos, baldes: baldes, series: map[string]*serieH{}}
	registrar(h)
	return h
}

func (h *Histograma) Observar(segundos float64, valores ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := chave(valores)
	s := h.series[k]
	if s == nil {
		s = &serieH{valores: append([]string(nil), valores...), contagens: make([]uint64, len(h.baldes))}
		h.series[k] = s
	}
	for i, b := range h.baldes {
		if segundos <= b {
			s.contagens[i]++
			break
		}
	}
	s.soma += segundos
	s.n++
}

// Contagem (testes).
func (h *Histograma) Contagem(valores ...string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.series[chave(valores)]; s != nil {
		return s.n
	}
	return 0
}

func (h *Histograma) nomeCompleto() string { return h.nome }

func (h *Histograma) escrever(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.nome, h.ajuda, h.nome)
	ks := make([]string, 0, len(h.series))
	for k := range h.series {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		s := h.series[k]
		var acum uint64
		for i, b := range h.baldes {
			acum += s.contagens[i]
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.nome, rotulos(h.rotulos, s.valores, "le", num(b)), acum)
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.nome, rotulos(h.rotulos, s.valores, "le", "+Inf"), s.n)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.nome, rotulos(h.rotulos, s.valores), num(s.soma))
		fmt.Fprintf(w, "%s_count%s %d\n", h.nome, rotulos(h.rotulos, s.valores), s.n)
	}
}
