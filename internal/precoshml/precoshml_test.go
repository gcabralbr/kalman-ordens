package precoshml

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/preco"
)

// Trecho no formato do tick file do Kalman (cabeçalho real encurtado; colunas extras depois das
// do marketdata, como no arquivo). A primeira linha é a de abertura real: preço sem bid/ask/status.
const tickCSV = `ts,hora,preco_obs,bid,ask,status84,idade_seg,preco_filtrado,velocidade
1791979238.915,09:00:38.915,210500.0,,,,,210407.9524,1721.3003
1791979238.969,09:00:38.968,210465.0,210500.0,210500.0,3,0.001,210436.124,1735.3302
1791979239.022,09:00:39.022,210395.0,210500.0,210500.0,0,0.001,210422.7459,1728.4317
1791979291.951,09:01:31.951,211020.0,211005.0,211020.0,0,0.2,210988.1329,1247.5034
1791997259.976,14:00:59.976,210995.0,210990.0,210995.0,0,0.001,210984.2208,576.36
`

var sp = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		panic(err)
	}
	return l
}()

func hoje(h, m, s, ms int) time.Time {
	return time.Date(2026, 10, 15, h, m, s, ms*int(time.Millisecond), sp)
}

func escrever(t *testing.T, dir, nome string, gz bool) string {
	t.Helper()
	p := filepath.Join(dir, nome)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if gz {
		w := gzip.NewWriter(f)
		w.Write([]byte(tickCSV))
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	f.WriteString(tickCSV)
	return p
}

func TestCarregarCSVeGZ(t *testing.T) {
	dir := t.TempDir()
	for _, gz := range []bool{false, true} {
		nome := "winfut-2026-10-14.csv"
		if gz {
			nome += ".gz"
		}
		p, err := Carregar(escrever(t, dir, nome, gz))
		if err != nil {
			t.Fatalf("%s: %v", nome, err)
		}
		if len(p.Linhas) != 5 {
			t.Fatalf("%s: %d linhas", nome, len(p.Linhas))
		}
		l0 := p.Linhas[0]
		if l0.Hora != 9*time.Hour+38*time.Second+915*time.Millisecond || *l0.Preco != 210500 ||
			l0.Bid != nil || l0.Ask != nil || l0.Status84 != 0 {
			t.Fatalf("%s: primeira linha %+v", nome, l0)
		}
		if l := p.Linhas[1]; *l.Bid != 210500 || *l.Ask != 210500 || l.Status84 != 3 || l.IdadeSeg != 0.001 {
			t.Fatalf("%s: segunda linha %+v", nome, l)
		}
	}
}

func TestCarregarRecusaArquivoSemColuna(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.csv")
	os.WriteFile(p, []byte("ts,hora,preco_obs,bid,ask\n1,09:00:00.000,1,2,3\n"), 0o600)
	if _, err := Carregar(p); err == nil || !strings.Contains(err.Error(), "status84") {
		t.Fatalf("esperado erro de coluna, veio %v", err)
	}
}

// Alinhamento pela hora do dia: a última linha com horário <= agora.
func TestAlinhamentoPorHorario(t *testing.T) {
	p, err := Carregar(escrever(t, t.TempDir(), "w.csv", false))
	if err != nil {
		t.Fatal(err)
	}
	casos := []struct {
		agora time.Time
		preco float64 // 0 = fora do horário
	}{
		{hoje(9, 0, 38, 914), 0},       // antes da primeira
		{hoje(9, 0, 38, 915), 210500},  // exatamente a primeira
		{hoje(9, 0, 38, 967), 210500},  // entre a 1ª e a 2ª
		{hoje(9, 0, 38, 968), 210465},  // exatamente a 2ª
		{hoje(9, 1, 0, 0), 210395},     // buraco longo: fica a última anterior
		{hoje(13, 59, 0, 0), 211020},   // idem
		{hoje(14, 0, 59, 976), 210995}, // exatamente a última
		{hoje(14, 0, 59, 977), 0},      // depois da última
		{hoje(8, 0, 0, 0), 0},          // antes da abertura
		{hoje(18, 0, 0, 0), 0},         // depois do fim
	}
	for _, c := range casos {
		l, ok := p.Em(HoraDoDia(c.agora))
		switch {
		case c.preco == 0 && ok:
			t.Errorf("%s: esperado fora do horário, veio %v", c.agora.Format("15:04:05.000"), *l.Preco)
		case c.preco != 0 && (!ok || *l.Preco != c.preco):
			t.Errorf("%s: esperado %v, veio ok=%v %+v", c.agora.Format("15:04:05.000"), c.preco, ok, l)
		}
	}
}

func servidor(t *testing.T, agora *time.Time, arquivo string) *Servidor {
	return &Servidor{
		Ticker:  "WINZ26",
		Agora:   func() time.Time { return *agora },
		Arquivo: func(time.Time) (string, error) { return arquivo, nil },
	}
}

func get(t *testing.T, s http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

// Contrato do JSON: o mesmo do marketdata, lido pelo leitor do próprio serviço (preco.Interpretar).
func TestContratoJSON(t *testing.T) {
	agora := hoje(10, 0, 0, 0)
	s := servidor(t, &agora, escrever(t, t.TempDir(), "w.csv.gz", true))

	w := get(t, s, "/precos/atual?ativo=WINFUT")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, %q", w.Code, w.Header().Get("Content-Type"))
	}
	// campos e formato exatos (fixture do marketdata: cenarios/referencia/precos-atual-exemplo.json)
	var bruto map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &bruto); err != nil {
		t.Fatal(err)
	}
	var chaves []string
	for k := range bruto["WINFUT"] {
		chaves = append(chaves, k)
	}
	if len(bruto) != 1 || len(chaves) != 7 {
		t.Fatalf("corpo %s", w.Body)
	}
	// linha de 09:01:31.951 com idade 0.2: ts = 09:01:31.751 (RFC3339, segundos), idade até 10:00
	l := preco.Interpretar(w.Code, w.Body.Bytes(), "WINFUT", agora)
	if l.Situacao != preco.OK || l.Ticker != "WINZ26" || l.Status84 != 0 {
		t.Fatalf("leitura %+v", l)
	}
	for nome, par := range map[string][2]any{"preco": {l.Preco, 211020.0}, "bid": {l.Bid, 211005.0}, "ask": {l.Ask, 211020.0}} {
		d := par[0].(*decimal.Decimal)
		if d == nil || !d.Equal(decimal.NewFromFloat(par[1].(float64))) {
			t.Errorf("%s = %v", nome, d)
		}
	}
	if bruto["WINFUT"]["ts"] != "2026-10-15T09:01:31-03:00" {
		t.Errorf("ts = %v", bruto["WINFUT"]["ts"])
	}
	if !l.IdadeSeg.Equal(decimal.RequireFromString("3508.2")) { // 10:00:00 − 09:01:31.751
		t.Errorf("idade_seg = %v", l.IdadeSeg)
	}

	// campo vazio no arquivo é omitido (nunca herdado), status84 vazio = 0
	agora = hoje(9, 0, 38, 950)
	w = get(t, s, "/precos/atual?ativo=WINFUT")
	if b := w.Body.String(); strings.Contains(b, `"bid"`) || strings.Contains(b, `"ask"`) || !strings.Contains(b, `"status84":0`) {
		t.Fatalf("abertura: %s", b)
	}
	l = preco.Interpretar(w.Code, w.Body.Bytes(), "WINFUT", agora)
	if l.Situacao != preco.OK || l.Bid != nil || l.Ask != nil || l.Preco == nil {
		t.Fatalf("abertura: %+v", l)
	}
	if !l.IdadeSeg.Equal(decimal.RequireFromString("0")) { // 38.950 − 38.915 arredonda a 0.0
		t.Errorf("abertura: idade_seg = %v", l.IdadeSeg)
	}
}

// Fora do horário do arquivo: 200 com "{}", como o marketdata sem dado de hoje.
func TestForaDoHorario(t *testing.T) {
	agora := hoje(8, 59, 0, 0)
	s := servidor(t, &agora, escrever(t, t.TempDir(), "w.csv", false))
	for _, a := range []time.Time{hoje(8, 59, 0, 0), hoje(14, 1, 0, 0), hoje(17, 0, 0, 0)} {
		agora = a
		w := get(t, s, "/precos/atual?ativo=WINFUT")
		if w.Code != 200 || w.Body.String() != "{}" {
			t.Fatalf("%s: %d %q", a.Format("15:04"), w.Code, w.Body)
		}
		if l := preco.Interpretar(w.Code, w.Body.Bytes(), "WINFUT", a); l.Situacao != preco.Vazio {
			t.Fatalf("%s: situação %v", a.Format("15:04"), l.Situacao)
		}
	}
}

func TestOutrosPedidos(t *testing.T) {
	agora := hoje(10, 0, 0, 0)
	s := servidor(t, &agora, escrever(t, t.TempDir(), "w.csv", false))
	if w := get(t, s, "/precos/atual?ativo=WDOFUT"); w.Code != 400 {
		t.Errorf("ativo desconhecido: %d", w.Code)
	}
	if w := get(t, s, "/precos"); w.Code != 404 {
		t.Errorf("caminho: %d", w.Code)
	}
	s.Arquivo = func(time.Time) (string, error) { return filepath.Join(t.TempDir(), "nada.csv"), nil }
	if w := get(t, s, "/precos/atual?ativo=WINFUT"); w.Code != 503 {
		t.Errorf("sem arquivo: %d", w.Code)
	}
}

// Virada do dia sem data fixa: o caminho muda e o pregão é recarregado.
func TestRecarregaQuandoOArquivoMuda(t *testing.T) {
	dir := t.TempDir()
	a := escrever(t, dir, "a.csv", false)
	os.WriteFile(filepath.Join(dir, "b.csv"), []byte(strings.Replace(tickCSV, "211020.0,211005.0", "199000.0,211005.0", 1)), 0o600)
	arq := a
	agora := hoje(10, 0, 0, 0)
	s := servidor(t, &agora, "")
	s.Arquivo = func(time.Time) (string, error) { return arq, nil }
	if w := get(t, s, "/precos/atual?ativo=WINFUT"); !strings.Contains(w.Body.String(), `"preco":211020`) {
		t.Fatal(w.Body)
	}
	arq = filepath.Join(dir, "b.csv")
	if w := get(t, s, "/precos/atual?ativo=WINFUT"); !strings.Contains(w.Body.String(), `"preco":199000`) {
		t.Fatal(w.Body)
	}
}

func TestDiaUtilAnterior(t *testing.T) {
	f := map[string]bool{"2026-10-12": true, "2026-11-20": true}
	casos := map[string]string{
		"2026-10-15": "2026-10-14", // quinta → quarta
		"2026-10-13": "2026-10-09", // terça → sexta (segunda 12/10 feriado)
		"2026-10-19": "2026-10-16", // segunda → sexta
		"2026-11-23": "2026-11-19", // segunda → quinta (sexta 20/11 feriado)
	}
	for h, esperado := range casos {
		d, _ := time.ParseInLocation("2006-01-02", h, sp)
		if got := DiaUtilAnterior(d.Add(10*time.Hour), f).Format("2006-01-02"); got != esperado {
			t.Errorf("%s: %s, esperado %s", h, got, esperado)
		}
	}
}

func TestArquivoDoDia(t *testing.T) {
	dir := t.TempDir()
	if _, err := ArquivoDoDia(dir, "2026-10-14"); err == nil {
		t.Fatal("sem arquivo: esperado erro")
	}
	os.WriteFile(filepath.Join(dir, "winfut-2026-10-14.csv"), nil, 0o600)
	if p, _ := ArquivoDoDia(dir, "2026-10-14"); filepath.Base(p) != "winfut-2026-10-14.csv" {
		t.Fatal(p)
	}
	os.WriteFile(filepath.Join(dir, "winfut-2026-10-14.csv.gz"), nil, 0o600)
	if p, _ := ArquivoDoDia(dir, "2026-10-14"); filepath.Base(p) != "winfut-2026-10-14.csv.gz" {
		t.Fatal(p)
	}
}

func TestLerFeriados(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.json")
	os.WriteFile(p, []byte(`{"2026-10-12": "feriado", "2026-11-20": "Zumbi"}`), 0o600)
	f, err := LerFeriados(p)
	if err != nil || !f["2026-10-12"] || !f["2026-11-20"] || len(f) != 2 {
		t.Fatal(f, err)
	}
}

// PRECOS_HML_ADIANTO_MS: com adianto +500 ms, às 09:01:31.451 já vale a linha das 09:01:31.951; com
// −500 ms, só às 09:01:32.451. O ts acompanha (a idade é a do pregão original: 0,2 s).
func TestAdianto(t *testing.T) {
	arq := escrever(t, t.TempDir(), "w.csv", false)
	agora := hoje(9, 1, 31, 451)
	s := servidor(t, &agora, arq)
	s.Adianto = 500 * time.Millisecond
	var v map[string]map[string]any
	if err := json.Unmarshal(get(t, s, "/precos/atual?ativo=WINFUT").Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["WINFUT"]["preco"] != 211020.0 || v["WINFUT"]["idade_seg"] != 0.2 {
		t.Fatalf("adianto +500: %v", v)
	}
	s.Adianto = 0
	_ = json.Unmarshal(get(t, s, "/precos/atual?ativo=WINFUT").Body.Bytes(), &v)
	if v["WINFUT"]["preco"] != 210395.0 {
		t.Fatalf("sem adianto: %v", v)
	}
	// −500 ms: a linha das 09:01:31.951 só vale a partir das 09:01:32.451
	s.Adianto = -500 * time.Millisecond
	agora = hoje(9, 1, 32, 400)
	_ = json.Unmarshal(get(t, s, "/precos/atual?ativo=WINFUT").Body.Bytes(), &v)
	if v["WINFUT"]["preco"] != 210395.0 {
		t.Fatalf("adianto -500 antes da linha valer: %v", v)
	}
	agora = hoje(9, 1, 32, 500)
	_ = json.Unmarshal(get(t, s, "/precos/atual?ativo=WINFUT").Body.Bytes(), &v)
	if v["WINFUT"]["preco"] != 211020.0 || v["WINFUT"]["idade_seg"] != 0.2 {
		t.Fatalf("adianto -500: %v", v)
	}
}
