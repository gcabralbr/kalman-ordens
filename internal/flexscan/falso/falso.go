// Package falso é a Flexscan falsa dos roteiros: a API REST (login, /orders, /orders/filter) com o
// comportamento observado no apisim em 23/10 (SPEC-FLEXSCAN-v1 §1) e o retrato de estratégia que vai
// no webhook. O roteiro controla execuções, recusas e falhas.
package falso

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Ordem na Flexscan falsa.
type Ordem struct {
	ID, Nome, Conta, Simbolo, Lado, Tipo string
	Qtd, Exec                            int
	Preco, Stop                          int64
	Medio                                float64
	UltPx                                int64
	UltQtd                               int
	TradeID                              string
	Status                               string // orderStatus da consulta: 0, 1, 2, 4, 5, 8, C
	Rejeicao                             string // clxRejReason
	Texto                                string // Text do retrato (o último evento)
	Transact                             time.Time
	Enviada                              time.Time
}

// textoTerminal: o 400 de PUT/DELETE de ordem terminal. Executada, o do simulador de 28/10 (o estado
// em português); as outras, o de antes.
func textoTerminal(o *Ordem, acao string) string {
	if o.Status == "2" {
		return "order cannot be " + acao + " because it is in the state: Finalizado"
	}
	return "order is not active"
}

func (o *Ordem) terminal() bool {
	return o.Status == "2" || o.Status == "4" || o.Status == "8" || o.Status == "C"
}

// Pedido: um pedido de alteração recebido (POST, PUT, DELETE), para o roteiro conferir.
type Pedido struct {
	Metodo, Caminho, ID string
	Corpo               map[string]any
}

// Falha programada para o próximo pedido de alteração.
type Falha struct {
	Tipo   string // "timeout" (sem resposta; Cria: a ordem nasce assim mesmo), "500", "400", "401", "403"
	Texto  string
	Codigo int
	Cria   bool
}

// Falso: o servidor.
type Falso struct {
	srv     *httptest.Server
	Usuario string
	// PrefixoID (v1.26): o começo dos ids das ordens (vazio = 00000000-0000-4000-8000-); duas falsas
	// com prefixos diferentes não repetem id (a do REAL nos roteiros dos dois ambientes)
	PrefixoID string
	Senha     string

	mu        sync.Mutex
	ordens    map[string]*Ordem
	ids       []string
	seq       int
	trade     int
	tokens    map[string]time.Time
	pedidos   []Pedido
	falhas    []Falha
	recusaPUT map[string]string // id → texto da próxima edição recusada
	expTokenS int
	logins    int
	loginErro *Falha
	espera    time.Duration // timeout: quanto o falso segura a resposta
	agora     func() time.Time

	// v1.21: o cadastro de webhooks do usuário, a falha programada da próxima chamada a ele (por
	// método) e o "Order not found" do GET logo depois do POST (o apisim de 27/10)
	webhooks      []map[string]any
	falhaWebhooks map[string]int
	naoAchadaPor  time.Duration
}

// Novo sobe a Flexscan falsa (httptest, porta livre).
func Novo(usuario, senha string, agora func() time.Time) *Falso {
	f := &Falso{Usuario: usuario, Senha: senha, ordens: map[string]*Ordem{}, tokens: map[string]time.Time{},
		recusaPUT: map[string]string{}, expTokenS: 36000, espera: 4 * time.Second, agora: agora}
	f.srv = httptest.NewServer(http.HandlerFunc(f.servir))
	return f
}

// URL base (o FLEXSCAN_URL dos roteiros).
func (f *Falso) URL() string { return f.srv.URL }

// Parar.
func (f *Falso) Parar() { f.srv.Close() }

// EsperaTimeout: quanto o falso segura um pedido programado com timeout (maior que o do cliente).
func (f *Falso) EsperaTimeout(d time.Duration) { f.mu.Lock(); f.espera = d; f.mu.Unlock() }

func (f *Falso) servir(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/connect/login" && r.Method == http.MethodPost {
		f.login(w, r)
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	exp, ok := f.tokens[tok]
	f.mu.Unlock()
	if !ok || f.agora().After(exp) {
		responder(w, http.StatusUnauthorized, map[string]any{"code": 3410, "title": "Unauthorized", "description": "invalid or expired token"})
		return
	}
	switch {
	case r.URL.Path == "/orders" && r.Method == http.MethodPost:
		f.post(w, r)
	case r.URL.Path == "/orders/filter" && r.Method == http.MethodGet:
		f.filtro(w, r)
	case strings.HasPrefix(r.URL.Path, "/orders/"):
		id := strings.TrimPrefix(r.URL.Path, "/orders/")
		switch r.Method {
		case http.MethodGet:
			f.get(w, id)
		case http.MethodPut:
			f.put(w, r, id)
		case http.MethodDelete:
			f.del(w, r, id)
		default:
			responder(w, http.StatusMethodNotAllowed, nil)
		}
	case r.URL.Path == "/webhooks" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		f.cadastroWebhooks(w, r)
	default:
		responder(w, http.StatusNotFound, map[string]any{"title": "not found"})
	}
}

func responder(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// jwt: um token com exp (a assinatura não importa ao cliente).
func jwt(exp time.Time, n int) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	p := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"n":%d}`, exp.Unix(), n)))
	return h + "." + p + ".x"
}

func (f *Falso) login(w http.ResponseWriter, r *http.Request) {
	var c struct{ User, Psswd string }
	_ = json.NewDecoder(r.Body).Decode(&c)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	if e := f.loginErro; e != nil {
		responder(w, http.StatusBadRequest, map[string]any{"code": e.Codigo, "title": "Authentication_error", "description": e.Texto})
		return
	}
	if c.User != f.Usuario || c.Psswd != f.Senha {
		responder(w, http.StatusBadRequest, map[string]any{"code": 3402, "title": "Authentication_error", "description": "invalid password"})
		return
	}
	exp := f.agora().Add(time.Duration(f.expTokenS) * time.Second)
	tok := jwt(exp, f.logins)
	f.tokens[tok] = exp
	responder(w, http.StatusOK, map[string]any{"auth_token": tok, "user_name": c.User})
}

// Logins: quantos logins o falso atendeu.
func (f *Falso) Logins() int { f.mu.Lock(); defer f.mu.Unlock(); return f.logins }

// InvalidarTokens: todo token atual passa a dar 401 (o próximo pedido faz login de novo).
func (f *Falso) InvalidarTokens() { f.mu.Lock(); f.tokens = map[string]time.Time{}; f.mu.Unlock() }

// RecusarLogin: os logins seguintes falham com o código (0 = volta a aceitar).
func (f *Falso) RecusarLogin(codigo int, texto string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if codigo == 0 {
		f.loginErro = nil
		return
	}
	f.loginErro = &Falha{Codigo: codigo, Texto: texto}
}

// Programar: a falha do próximo pedido de alteração (POST, PUT ou DELETE).
func (f *Falso) Programar(fa Falha) { f.mu.Lock(); f.falhas = append(f.falhas, fa); f.mu.Unlock() }

// RecusarEdicao: a próxima edição da ordem é recusada como a razão 4 do apisim.
func (f *Falso) RecusarEdicao(id, texto string) { f.mu.Lock(); f.recusaPUT[id] = texto; f.mu.Unlock() }

func (f *Falso) proximaFalha() *Falha {
	if len(f.falhas) == 0 {
		return nil
	}
	fa := f.falhas[0]
	f.falhas = f.falhas[1:]
	return &fa
}

// Pedidos: os pedidos de alteração recebidos (o roteiro consome).
func (f *Falso) Pedidos() []Pedido {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.pedidos
	f.pedidos = nil
	return r
}

// Devolver: põe pedidos de volta no início (o roteiro olhou e não consumiu).
func (f *Falso) Devolver(ps []Pedido) {
	f.mu.Lock()
	f.pedidos = append(ps, f.pedidos...)
	f.mu.Unlock()
}

func (f *Falso) falhar(w http.ResponseWriter, fa *Falha) bool {
	switch fa.Tipo {
	case "timeout":
		f.mu.Unlock()
		time.Sleep(f.espera)
		f.mu.Lock()
		return true // o cliente já desistiu; a resposta não importa
	case "500":
		responder(w, http.StatusInternalServerError, map[string]any{"title": "Internal server error"})
	case "400":
		responder(w, http.StatusBadRequest, map[string]any{"code": fa.Codigo, "title": "Bad Request", "description": fa.Texto})
	case "401":
		responder(w, http.StatusUnauthorized, map[string]any{"code": 3410, "title": "Unauthorized"})
	case "403":
		responder(w, http.StatusForbidden, map[string]any{"code": fa.Codigo, "title": "Forbidden", "description": fa.Texto})
	}
	return true
}

func (f *Falso) post(w http.ResponseWriter, r *http.Request) {
	var c map[string]any
	d := json.NewDecoder(r.Body)
	d.UseNumber()
	_ = d.Decode(&c)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pedidos = append(f.pedidos, Pedido{Metodo: "POST", Caminho: "/orders", Corpo: c})
	fa := f.proximaFalha()
	if fa != nil && !(fa.Tipo == "timeout" && fa.Cria) {
		f.falhar(w, fa)
		return
	}
	// validação do que a API exige (o 400 da OpenAPI)
	ot, tif, nome := fmt.Sprint(c["ordType"]), fmt.Sprint(c["timeInForce"]), fmt.Sprint(c["name"])
	if (ot != "2" && ot != "4") || tif != "0" || c["account"] == nil || c["symbol"] == nil || len(nome) > 50 {
		responder(w, http.StatusBadRequest, map[string]any{"code": 4001, "title": "Bad Request", "description": "invalid order"})
		return
	}
	f.seq++
	id := fmt.Sprintf("%s%012d", f.prefixoID(), f.seq)
	o := &Ordem{ID: id, Nome: nome, Conta: fmt.Sprint(c["account"]), Simbolo: fmt.Sprint(c["symbol"]), Lado: fmt.Sprint(c["side"]), Tipo: ot,
		Qtd: atoi(c["qtty"]), Preco: atoi64(c["price"]), Stop: atoi64(c["stopPrice"]), Status: "0", Transact: f.agora(), Enviada: f.agora()}
	o.Texto = fmt.Sprintf("Ordem aceita, %d@%d", o.Qtd, o.Preco)
	if ot == "4" {
		o.Texto += fmt.Sprintf(". Aguardando stop: %d", o.Stop)
	}
	f.ordens[id] = o
	f.ids = append(f.ids, id)
	if fa != nil {
		f.falhar(w, fa) // timeout que cria a ordem
		return
	}
	responder(w, http.StatusAccepted, map[string]any{"status": "PENDING NEW", "id": id,
		"description": "order sent to exec engine. Please check the snapshot url for status updates.", "snapshot_url": f.srv.URL + "/orders/" + id})
}

func atoi(v any) int { n, _ := strconv.Atoi(fmt.Sprint(v)); return n }
func atoi64(v any) int64 {
	if v == nil {
		return 0
	}
	x, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
	return int64(x)
}

func (f *Falso) put(w http.ResponseWriter, r *http.Request, id string) {
	var c map[string]any
	d := json.NewDecoder(r.Body)
	d.UseNumber()
	_ = d.Decode(&c)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pedidos = append(f.pedidos, Pedido{Metodo: "PUT", Caminho: r.URL.Path, ID: id, Corpo: c})
	if fa := f.proximaFalha(); fa != nil {
		if fa.Tipo == "timeout" && fa.Cria {
			f.editar(id, c)
		}
		f.falhar(w, fa)
		return
	}
	o := f.ordens[id]
	if o == nil {
		responder(w, http.StatusNotFound, map[string]any{"title": "not found"})
		return
	}
	if o.terminal() {
		responder(w, http.StatusBadRequest, map[string]any{"code": 4003, "title": "Bad Request", "description": textoTerminal(o, "replaced")})
		return
	}
	if t, ok := f.recusaPUT[id]; ok {
		delete(f.recusaPUT, id)
		// apisim (013/15): 202, valores antigos, clxRejReason 4, Text "Edicao rejeitada, razão: 4. ..."
		o.Rejeicao = "4"
		o.Texto = "Edicao rejeitada, razão: 4. " + t
		responder(w, http.StatusAccepted, map[string]any{"status": "PENDING REPLACE"})
		return
	}
	f.editar(id, c)
	responder(w, http.StatusAccepted, map[string]any{"status": "PENDING REPLACE"})
}

func (f *Falso) editar(id string, c map[string]any) {
	o := f.ordens[id]
	if o == nil {
		return
	}
	if v, ok := c["price"]; ok {
		o.Preco = atoi64(v)
	}
	if v, ok := c["stopPrice"]; ok {
		o.Stop = atoi64(v)
	}
	if v, ok := c["qtty"]; ok {
		o.Qtd = atoi(v)
	}
	o.Status = "5"
	o.Transact = f.agora().Add(time.Duration(f.seq) * time.Millisecond)
	o.Texto = fmt.Sprintf("Editada, %d@%d", o.Qtd, o.Preco)
	if o.Tipo == "4" {
		o.Texto += fmt.Sprintf(" STOP %d", o.Stop)
	}
}

func (f *Falso) del(w http.ResponseWriter, r *http.Request, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pedidos = append(f.pedidos, Pedido{Metodo: "DELETE", Caminho: r.URL.Path, ID: id})
	if fa := f.proximaFalha(); fa != nil {
		if fa.Tipo == "timeout" && fa.Cria {
			f.cancelar(id)
		}
		f.falhar(w, fa)
		return
	}
	o := f.ordens[id]
	if o == nil {
		responder(w, http.StatusNotFound, map[string]any{"title": "not found"})
		return
	}
	if o.terminal() {
		responder(w, http.StatusBadRequest, map[string]any{"code": 4003, "title": "Bad Request", "description": textoTerminal(o, "canceled")})
		return
	}
	f.cancelar(id)
	responder(w, http.StatusAccepted, map[string]any{"status": "PENDING CANCEL"})
}

func (f *Falso) cancelar(id string) {
	if o := f.ordens[id]; o != nil && !o.terminal() {
		o.Status = "4"
		o.Transact = f.agora()
		o.Texto = fmt.Sprintf("Cancelada,%d@%d", o.Qtd, o.Preco)
	}
}

// Criar: uma ordem que não passou pelo serviço (ex.: KM de outro processo, ou da sondagem manual).
func (f *Falso) Criar(o Ordem) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	o.ID = fmt.Sprintf("%s%012d", f.prefixoID(), f.seq)
	if o.Status == "" {
		o.Status = "0"
	}
	o.Transact, o.Enviada = f.agora(), f.agora()
	f.ordens[o.ID] = &o
	f.ids = append(f.ids, o.ID)
	return o.ID
}

// Cancelar: cancelamento que não foi nosso (ex.: expiração na bolsa).
func (f *Falso) Cancelar(id string) { f.mu.Lock(); f.cancelar(id); f.mu.Unlock() }

// Executar: um negócio de qtd a px (parcial ou total), como a bolsa.
func (f *Falso) Executar(id string, qtd int, px int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.ordens[id]
	if o == nil || o.terminal() {
		return fmt.Errorf("ordem %s inexistente ou terminal", id)
	}
	if o.Exec+qtd > o.Qtd {
		return fmt.Errorf("ordem %s: %d+%d > %d", id, o.Exec, qtd, o.Qtd)
	}
	o.Medio = (o.Medio*float64(o.Exec) + float64(px)*float64(qtd)) / float64(o.Exec+qtd)
	o.Exec += qtd
	o.UltPx, o.UltQtd = px, qtd
	f.trade++
	o.TradeID = strconv.Itoa(f.trade)
	if o.Exec == o.Qtd {
		o.Status = "2"
		o.Texto = fmt.Sprintf("Executada, %d/%d@%d", o.Exec, o.Qtd, px)
	} else {
		o.Status = "1"
		o.Texto = fmt.Sprintf("Parcialmente executada, %d/%d@%d", o.Exec, o.Qtd, px)
	}
	o.Transact = f.agora()
	return nil
}

// Ordem: cópia do estado (nil = não existe).
func (f *Falso) Ordem(id string) *Ordem {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o := f.ordens[id]; o != nil {
		c := *o
		return &c
	}
	return nil
}

// PorNome: o id da ordem com esse name ("" = nenhuma).
func (f *Falso) PorNome(nome string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.ids {
		if f.ordens[id].Nome == nome {
			return id
		}
	}
	return ""
}

// AlterarOrdem: muda o estado à mão (o roteiro simula o que a Flexscan faria sozinha: preço ajustado
// ao túnel, gatilho mexido, estado inesperado).
func (f *Falso) AlterarOrdem(id string, mudar func(*Ordem)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o := f.ordens[id]; o != nil {
		mudar(o)
	}
}

func fmtTS(t time.Time) string { return t.UTC().Format("20060102-15:04:05.000") }

// registro: o order_record (GET /orders/{id}), com stopPrice e avgPrice em texto, como o apisim.
func (f *Falso) registro(o *Ordem) map[string]any {
	r := map[string]any{"id": o.ID, "name": o.Nome, "account": o.Conta, "symbol": o.Simbolo, "side": o.Lado, "orderType": o.Tipo,
		"quantity": o.Qtd, "quantityExecuted": o.Exec, "quantityRemaining": o.Qtd - o.Exec, "price": float64(o.Preco),
		"orderStatus": o.Status, "exchange": "BVMF", "timeInForce": "0", "text": "", "lastQuantity": o.UltQtd,
		"uniqueTradeId": o.TradeID, "sendingTime": fmtTS(o.Transact), "transactTime": o.Transact.UTC().Format("20060102-15:04:05.000"),
		"username": f.Usuario}
	if o.Tipo == "4" {
		r["stopPrice"] = fmt.Sprintf("%d.00000", o.Stop)
	}
	if o.Exec > 0 {
		r["avgPrice"] = strconv.FormatFloat(o.Medio, 'f', 5, 64)
		r["lastPrice"] = float64(o.UltPx)
	}
	if o.Rejeicao != "" {
		r["clxRejReason"] = o.Rejeicao
	}
	return r
}

func (f *Falso) get(w http.ResponseWriter, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.ordens[id]
	if o == nil || (f.naoAchadaPor > 0 && f.agora().Sub(o.Enviada) < f.naoAchadaPor) {
		responder(w, http.StatusNotFound, map[string]any{"code": 404, "title": "Order not found with requested id"})
		return
	}
	responder(w, http.StatusOK, f.registro(o))
}

func (f *Falso) filtro(w http.ResponseWriter, r *http.Request) {
	conta := r.URL.Query().Get("account")
	f.mu.Lock()
	defer f.mu.Unlock()
	var recs []any
	for _, id := range f.ids {
		if o := f.ordens[id]; conta == "" || o.Conta == conta {
			recs = append(recs, f.registro(o))
		}
	}
	if recs == nil {
		recs = []any{}
	}
	responder(w, http.StatusOK, map[string]any{"num_records": len(recs), "records": recs, "has_next": false})
}

// statusRetrato: o OrdStatus do retrato (Status_t): 3 viva, 5 executada (FINISHED), 7 cancelada.
func statusRetrato(o *Ordem) string {
	switch o.Status {
	case "2":
		return "5"
	case "4", "C":
		return "7"
	case "8":
		return "8"
	}
	return "3"
}

// Retrato: o item do webhook (retrato de estratégia sorder), como o apisim manda.
func (f *Falso) Retrato(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.ordens[id]
	if o == nil {
		return nil
	}
	cp := map[string]any{"PriceLimit": float64(o.Preco), "SyntheticBracket": "OFF"}
	if o.Tipo == "4" {
		cp["StopPrice"] = float64(o.Stop)
	}
	leg := map[string]any{"ILegAllocAccount": o.Conta, "LegAvgPx": o.Medio, "LegExecQty": o.Exec, "LegOrdType": o.Tipo, "LegQuantity": o.Qtd,
		"LegRefID": o.ID + ":1", "LegSecurityExchange": "XBMF", "LegSide": o.Lado, "LegSymbol": o.Simbolo, "LegWorkingIndicator": "N"}
	if o.Exec > 0 {
		leg["LegLastPx"], leg["LegLastQty"] = float64(o.UltPx), o.UltQtd
		leg["LegWorkingIndicator"] = "Y"
	}
	ts := o.Transact.In(time.FixedZone("BRT", -3*3600)).Format("2006-01-02 15:04:05")
	return map[string]any{"Legs": []any{leg}, "CustomParameters": cp, "UserName": f.Usuario, "StrategyCode": "sorder",
		"ClOrdID": o.ID, "OrdStatus": statusRetrato(o), "AvgPrice": o.Medio, "ExecutedQtty": o.Exec, "TargetQtty": o.Qtd,
		"Name": o.Nome, "Text": o.Texto, "TimeInForce": "0", "TimeStamp": ts, "DocNumber": "00000000000", "Env": "SIM"}
}

// CorpoWebhook: a lista de retratos dos ids, na ordem pedida (o apisim manda fora de ordem às vezes).
func (f *Falso) CorpoWebhook(ids ...string) []byte {
	var l []any
	for _, id := range ids {
		if r := f.Retrato(id); r != nil {
			l = append(l, r)
		}
	}
	b, _ := json.Marshal(l)
	return b
}

// Ids: todos os ids, em ordem de criação.
func (f *Falso) Ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := append([]string(nil), f.ids...)
	sort.Strings(r)
	return r
}

// ---------------------------------------------------------------- v1.21: cadastro de webhooks

func (f *Falso) cadastroWebhooks(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st := f.falhaWebhooks[r.Method]; st != 0 {
		delete(f.falhaWebhooks, r.Method)
		responder(w, st, map[string]any{"code": st, "title": "webhook error", "description": "falha programada"})
		return
	}
	if r.Method == http.MethodGet {
		recs := make([]any, 0, len(f.webhooks))
		for _, c := range f.webhooks {
			recs = append(recs, c)
		}
		responder(w, http.StatusOK, map[string]any{"records": recs})
		return
	}
	var c map[string]any
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c["url"] == nil || c["triggers"] == nil || c["target"] == nil {
		responder(w, http.StatusBadRequest, map[string]any{"code": 400, "title": "invalid webhook"})
		return
	}
	c["id"] = fmt.Sprintf("wh-%d", len(f.webhooks)+1)
	c["created_at"], c["created_by"] = f.agora().UTC().Format(time.RFC3339), f.Usuario
	f.webhooks = append(f.webhooks, c)
	responder(w, http.StatusOK, c)
}

// Webhooks: as URLs cadastradas, na ordem.
func (f *Falso) Webhooks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var us []string
	for _, c := range f.webhooks {
		us = append(us, fmt.Sprint(c["url"]))
	}
	return us
}

// Gatilhos (v1.21.1): os triggers somados dos cadastros com essa URL, sem repetição, em ordem.
func (f *Falso) Gatilhos(url string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var gs []string
	for _, c := range f.webhooks {
		if fmt.Sprint(c["url"]) != url {
			continue
		}
		var ts []string
		switch x := c["triggers"].(type) {
		case []string:
			ts = x
		case []any:
			for _, t := range x {
				ts = append(ts, fmt.Sprint(t))
			}
		}
		for _, t := range ts {
			if !slices.Contains(gs, t) {
				gs = append(gs, t)
			}
		}
	}
	slices.Sort(gs)
	return gs
}

// CadastrarWebhook: um cadastro feito por fora (à mão, outro sistema).
func (f *Falso) CadastrarWebhook(url string, triggers ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webhooks = append(f.webhooks, map[string]any{"id": fmt.Sprintf("wh-%d", len(f.webhooks)+1), "url": url, "triggers": triggers,
		"target": map[string]any{"type": "user", "id": f.Usuario}, "created_at": f.agora().UTC().Format(time.RFC3339), "created_by": f.Usuario})
}

// LimparWebhooks: o cadastro some (como depois da troca de senha de 23/10).
func (f *Falso) LimparWebhooks() {
	f.mu.Lock()
	f.webhooks = nil
	f.mu.Unlock()
}

// FalharWebhooks: a próxima chamada com esse método ao cadastro de webhooks responde status.
func (f *Falso) FalharWebhooks(metodo string, status int) {
	f.mu.Lock()
	if f.falhaWebhooks == nil {
		f.falhaWebhooks = map[string]int{}
	}
	f.falhaWebhooks[metodo] = status
	f.mu.Unlock()
}

// NaoAchadaPor: o GET da ordem responde 404 "Order not found" até d depois do POST (0 = desliga).
func (f *Falso) NaoAchadaPor(d time.Duration) {
	f.mu.Lock()
	f.naoAchadaPor = d
	f.mu.Unlock()
}

// Tokens: os tokens emitidos (o executor procura segredo no tráfego).
func (f *Falso) Tokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ts := make([]string, 0, len(f.tokens))
	for t := range f.tokens {
		ts = append(ts, t)
	}
	return ts
}

func (f *Falso) prefixoID() string {
	if f.PrefixoID == "" {
		return "00000000-0000-4000-8000-"
	}
	return f.PrefixoID
}
