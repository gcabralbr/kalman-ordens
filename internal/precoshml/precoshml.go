// Package precoshml serve o /precos/atual da HML a partir do tick file do Kalman do pregão que a
// HML reproduz (ferramenta de teste, cmd/precos-hml; só na .53). Mesmo contrato do marketdata
// (docs/referencia/precos-atual.md): a cada pedido, a última linha do arquivo com horário <= a
// hora do dia de agora; antes da primeira ou depois da última linha, corpo "{}".
package precoshml

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Conceito é o único ativo servido (o kalman-ordens pede ?ativo=WINFUT).
const Conceito = "WINFUT"

// Linha do tick file: só as colunas do marketdata. Ausente no arquivo = nil (omitido no JSON).
type Linha struct {
	Hora            time.Duration // desde a meia-noite (coluna hora, horário de Brasília)
	Preco, Bid, Ask *float64
	Status84        int
	IdadeSeg        float64 // idade que o marketdata informou quando o Kalman leu
}

// Pregao é o tick file carregado, em ordem de horário.
type Pregao struct {
	Arquivo string
	Linhas  []Linha
}

// Em devolve a última linha com Hora <= hora; false antes da primeira ou depois da última.
func (p *Pregao) Em(hora time.Duration) (Linha, bool) {
	n := len(p.Linhas)
	if n == 0 || hora < p.Linhas[0].Hora || hora > p.Linhas[n-1].Hora {
		return Linha{}, false
	}
	i := sort.Search(n, func(i int) bool { return p.Linhas[i].Hora > hora })
	return p.Linhas[i-1], true
}

// Carregar lê o tick file (.csv ou .csv.gz, pela extensão).
func Carregar(caminho string) (*Pregao, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(caminho, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", caminho, err)
		}
		defer gz.Close()
		r = gz
	}
	p, err := ler(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", caminho, err)
	}
	p.Arquivo = caminho
	return p, nil
}

var colunas = []string{"hora", "preco_obs", "bid", "ask", "status84", "idade_seg"}

func ler(r io.Reader) (*Pregao, error) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	if !s.Scan() {
		if err := s.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("arquivo vazio")
	}
	idx := map[string]int{}
	for i, c := range strings.Split(strings.TrimSpace(s.Text()), ",") {
		idx[c] = i
	}
	pos := make([]int, len(colunas))
	maior := 0
	for i, c := range colunas {
		j, ok := idx[c]
		if !ok {
			return nil, fmt.Errorf("cabeçalho sem a coluna %s", c)
		}
		pos[i] = j
		maior = max(maior, j)
	}
	p := &Pregao{}
	for n := 2; s.Scan(); n++ {
		txt := strings.TrimSpace(s.Text())
		if txt == "" {
			continue
		}
		campos := strings.SplitN(txt, ",", maior+2)
		if len(campos) <= maior {
			return nil, fmt.Errorf("linha %d: %d colunas", n, len(campos))
		}
		hora, err := lerHora(campos[pos[0]])
		if err != nil {
			return nil, fmt.Errorf("linha %d: %w", n, err)
		}
		l := Linha{Hora: hora}
		for k, dst := range []**float64{&l.Preco, &l.Bid, &l.Ask} {
			if *dst, err = numero(campos[pos[1+k]]); err != nil {
				return nil, fmt.Errorf("linha %d, %s: %w", n, colunas[1+k], err)
			}
		}
		if v := campos[pos[4]]; v != "" {
			if l.Status84, err = strconv.Atoi(v); err != nil {
				return nil, fmt.Errorf("linha %d, status84: %w", n, err)
			}
		}
		if id, err := numero(campos[pos[5]]); err != nil {
			return nil, fmt.Errorf("linha %d, idade_seg: %w", n, err)
		} else if id != nil {
			l.IdadeSeg = *id
		}
		p.Linhas = append(p.Linhas, l)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(p.Linhas) == 0 {
		return nil, errors.New("sem linhas de dados")
	}
	sort.SliceStable(p.Linhas, func(i, j int) bool { return p.Linhas[i].Hora < p.Linhas[j].Hora })
	return p, nil
}

func numero(v string) (*float64, error) {
	if v == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// lerHora: HH:MM:SS[.mmm] → desde a meia-noite.
func lerHora(v string) (time.Duration, error) {
	t, err := time.Parse("15:04:05.999999999", v)
	if err != nil {
		return 0, fmt.Errorf("hora %q: %w", v, err)
	}
	return HoraDoDia(t), nil
}

// HoraDoDia: tempo desde a meia-noite do dia de t (no fuso de t).
func HoraDoDia(t time.Time) time.Duration {
	h, m, s := t.Clock()
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second +
		time.Duration(t.Nanosecond())
}

// --- dia do arquivo ---

// LerFeriados lê o JSON {"AAAA-MM-DD": "descrição"} que o Kalman gera da tabela anferiado
// (/var/lib/kalman/feriados.json): a mesma fonte do marketdata, sem banco.
func LerFeriados(caminho string) (map[string]bool, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", caminho, err)
	}
	out := make(map[string]bool, len(m))
	for d := range m {
		out[d] = true
	}
	return out, nil
}

// DiaUtilAnterior: o dia útil (seg–sex, fora dos feriados) antes de hoje.
func DiaUtilAnterior(hoje time.Time, feriados map[string]bool) time.Time {
	d := time.Date(hoje.Year(), hoje.Month(), hoje.Day(), 0, 0, 0, 0, hoje.Location())
	for {
		d = d.AddDate(0, 0, -1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday && !feriados[d.Format("2006-01-02")] {
			return d
		}
	}
}

// ArquivoDoDia: <dir>/winfut-AAAA-MM-DD.csv.gz ou .csv (o .gz é o arquivo fechado do dia; o .csv
// sozinho, o que o Kalman ainda não comprimiu).
func ArquivoDoDia(dir, data string) (string, error) {
	base := filepath.Join(dir, "winfut-"+data+".csv")
	for _, c := range []string{base + ".gz", base} {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("sem tick file de %s em %s (.csv.gz ou .csv)", data, dir)
}

// --- HTTP ---

// payload: o mesmo do marketdata (docs/referencia/precos-atual.md).
type payload struct {
	Ticker   string   `json:"ticker"`
	Preco    *float64 `json:"preco,omitempty"`
	Bid      *float64 `json:"bid,omitempty"`
	Ask      *float64 `json:"ask,omitempty"`
	Status84 int      `json:"status84"`
	TS       string   `json:"ts"`
	IdadeSeg float64  `json:"idade_seg"`
}

// Servidor responde GET /precos/atual?ativo=WINFUT.
type Servidor struct {
	Ticker string
	Agora  func() time.Time // hora local de Brasília
	// Arquivo devolve o tick file a servir hoje; trocado o caminho (virada do dia sem data
	// fixa), o pregão é recarregado.
	Arquivo func(hoje time.Time) (string, error)
	Log     func(format string, args ...any)

	mu     sync.Mutex
	pregao *Pregao
}

// Atual: o pregão de hoje, recarregado se o arquivo mudou.
func (s *Servidor) Atual(hoje time.Time) (*Pregao, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	caminho, err := s.Arquivo(hoje)
	if err != nil {
		return nil, err
	}
	if s.pregao == nil || s.pregao.Arquivo != caminho {
		p, err := Carregar(caminho)
		if err != nil {
			return nil, err
		}
		s.pregao = p
		if s.Log != nil {
			s.Log("precos-hml: %s, %d linhas, %s a %s", caminho, len(p.Linhas),
				fmtHora(p.Linhas[0].Hora), fmtHora(p.Linhas[len(p.Linhas)-1].Hora))
		}
	}
	return s.pregao, nil
}

func (s *Servidor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/precos/atual" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "só GET", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("ativo") != Conceito {
		http.Error(w, "ativo desconhecido", http.StatusBadRequest)
		return
	}
	agora := s.Agora()
	p, err := s.Atual(agora)
	if err != nil {
		if s.Log != nil {
			s.Log("precos-hml: %v", err)
		}
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	l, ok := p.Em(HoraDoDia(agora))
	if !ok {
		io.WriteString(w, "{}")
		return
	}
	json.NewEncoder(w).Encode(map[string]payload{Conceito: Resposta(l, s.Ticker, agora)})
}

// Resposta: a linha reposicionada em hoje. O ts é o do campo mais fresco no pregão original
// (hora da linha − idade informada naquele momento), na data de hoje; idade_seg = agora − ts.
func Resposta(l Linha, ticker string, agora time.Time) payload {
	meiaNoite := time.Date(agora.Year(), agora.Month(), agora.Day(), 0, 0, 0, 0, agora.Location())
	ts := meiaNoite.Add(l.Hora - time.Duration(l.IdadeSeg*float64(time.Second)))
	return payload{
		Ticker: ticker, Preco: l.Preco, Bid: l.Bid, Ask: l.Ask, Status84: l.Status84,
		TS:       ts.Format(time.RFC3339),
		IdadeSeg: math.Round(agora.Sub(ts).Seconds()*10) / 10,
	}
}

func fmtHora(d time.Duration) string {
	return time.Time{}.Add(d).Format("15:04:05.000")
}
