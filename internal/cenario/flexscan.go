package cenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/flexscan/falso"
	"gammaquant.com.br/kalman-ordens/internal/roteiro"
)

// Verbos da Flexscan falsa (SPEC-FLEXSCAN-v1 §10; cenarios/FORMATO.md, seção Flexscan).
func (e *Executor) flexscan(p roteiro.Passo) error {
	if p.Verbo == "flx_ambiente" {
		amb, err := dominio.LerAmbiente(p.Args[0])
		if err != nil || e.flxs[amb] == nil {
			return fmt.Errorf("flx_ambiente %s: sem a Flexscan falsa desse ambiente (REAL: config REAL_FLEXSCAN_URL=falso)", p.Args[0])
		}
		e.ocioso()
		e.mirarFlexscan(amb)
		return nil
	}
	if e.flx == nil {
		return fmt.Errorf("%s sem a Flexscan falsa (config FLEXSCAN_URL=falso)", p.Verbo)
	}
	args := make([]string, len(p.Args))
	for i, a := range p.Args {
		if (p.Verbo == "flx_recebe" || p.Verbo == "flx_cria") && strings.Contains(a, "=>") {
			args[i] = a // captura: resolvida no flxRecebe
			continue
		}
		v, err := e.substituir(a)
		if err != nil {
			return err
		}
		args[i] = v
	}
	switch p.Verbo {
	case "flx_recebe":
		return e.flxRecebe(args)
	case "flx_nada":
		e.ocioso()
		if ps := e.flx.Pedidos(); len(ps) > 0 {
			return fmt.Errorf("pedido não esperado na Flexscan: %s %s %v", ps[0].Metodo, ps[0].Caminho, ps[0].Corpo)
		}
		return nil
	case "flx_executa":
		// flx_executa ID qtd=N preco=P
		if len(args) < 1 {
			return fmt.Errorf("flx_executa ID [qtd=N] preco=P")
		}
		qtd, px := 0, int64(0)
		for _, a := range args[1:] {
			k, v, _ := strings.Cut(a, "=")
			switch k {
			case "qtd":
				qtd, _ = strconv.Atoi(v)
			case "preco":
				px, _ = strconv.ParseInt(v, 10, 64)
			default:
				return fmt.Errorf("flx_executa: %q", a)
			}
		}
		o := e.flx.Ordem(args[0])
		if o == nil {
			return fmt.Errorf("flx_executa: ordem %s não existe", args[0])
		}
		if qtd == 0 {
			qtd = o.Qtd - o.Exec
		}
		return e.flx.Executar(args[0], qtd, px)
	case "flx_webhook":
		// a lista de retratos das ordens, na ordem dada
		return e.flxWebhook(e.flx.CorpoWebhook(args...), http.StatusOK)
	case "flx_webhook_em":
		// v1.26: os retratos da falsa do alvo entregues na rota do webhook de outro ambiente
		amb, err := dominio.LerAmbiente(args[0])
		if err != nil || e.svc.receptores[amb] == nil {
			return fmt.Errorf("flx_webhook_em %s: sem o webhook desse ambiente", args[0])
		}
		alvo := e.svc.receptor
		caminho := e.svc.caminho
		e.svc.receptor, e.svc.caminho = e.svc.receptores[amb], e.svc.caminhos[amb]
		defer func() { e.svc.receptor, e.svc.caminho = alvo, caminho }()
		return e.flxWebhook(e.flx.CorpoWebhook(args[1:]...), http.StatusOK)
	case "flx_webhook_corpo":
		return e.flxWebhook([]byte(args[0]), http.StatusOK)
	case "flx_webhook_http":
		// flx_webhook_http METODO /caminho [corpo]: a resposta vira a última (resposta _status=...)
		corpo := ""
		if len(args) > 2 {
			corpo = args[2]
		}
		req := httptest.NewRequest(args[0], args[1], bytes.NewBufferString(corpo))
		w := httptest.NewRecorder()
		e.svc.receptor.ServeHTTP(w, req)
		e.ocioso()
		if w.Code == http.StatusOK || w.Code == http.StatusNotFound {
			if w.Body.Len() != 0 {
				return fmt.Errorf("receptor respondeu %d com corpo: %q", w.Code, w.Body.String())
			}
			e.resposta = map[string]any{"_status": json.Number(strconv.Itoa(w.Code))}
			return nil
		}
		return e.lerResposta(args[0]+" "+args[1], w)
	case "flx_falha":
		// flx_falha timeout|500|400|401|403 [cria=1] [texto=...] [codigo=N]
		if len(args) < 1 {
			return fmt.Errorf("flx_falha TIPO [cria=1] [texto=...] [codigo=N]")
		}
		fa := falso.Falha{Tipo: args[0]}
		for _, a := range args[1:] {
			k, v, _ := strings.Cut(a, "=")
			switch k {
			case "cria":
				fa.Cria = v == "1"
			case "texto":
				fa.Texto = v
			case "codigo":
				fa.Codigo, _ = strconv.Atoi(v)
			default:
				return fmt.Errorf("flx_falha: %q", a)
			}
		}
		e.flx.Programar(fa)
		return nil
	case "flx_recusa_edicao":
		if len(args) < 1 {
			return fmt.Errorf("flx_recusa_edicao ID [texto]")
		}
		texto := "Sell order stop price must be below referential price"
		if len(args) > 1 {
			texto = strings.Join(args[1:], " ")
		}
		e.flx.RecusarEdicao(args[0], texto)
		return nil
	case "flx_cancela":
		e.flx.Cancelar(args[0])
		return nil
	case "flx_token_expira":
		e.flx.InvalidarTokens()
		return nil
	case "flx_webhooks":
		return e.flxWebhooks(args)
	case "flx_nao_achada":
		// flx_nao_achada 250ms: o GET da ordem dá 404 até esse tempo depois do POST (0 = desliga)
		d, err := time.ParseDuration(args[0])
		if err != nil {
			return err
		}
		e.flx.NaoAchadaPor(d)
		return nil
	case "flx_trafego":
		return e.flxTrafego(args)
	case "flx_login":
		// flx_login recusa|aceita
		switch args[0] {
		case "aceita":
			e.flx.RecusarLogin(0, "")
		case "senha":
			e.flx.RecusarLogin(3402, "invalid password") // definitivo: a ponte não tenta sozinha
		default:
			e.flx.RecusarLogin(7001, "authentication service unavailable") // transitório
		}
		return nil
	case "flx_altera":
		// flx_altera ID [preco=N] [stop=N] [status=X] [texto=...]: o que a Flexscan faria sozinha
		if len(args) < 2 {
			return fmt.Errorf("flx_altera ID campo=valor ...")
		}
		var errA error
		e.flx.AlterarOrdem(args[0], func(o *falso.Ordem) {
			for _, a := range args[1:] {
				k, v, _ := strings.Cut(a, "=")
				switch k {
				case "preco":
					o.Preco, _ = strconv.ParseInt(v, 10, 64)
				case "stop":
					o.Stop, _ = strconv.ParseInt(v, 10, 64)
				case "status":
					o.Status = v
				case "texto":
					o.Texto = v
				default:
					errA = fmt.Errorf("flx_altera: %q", a)
				}
			}
			o.Transact = e.rel.Agora()
		})
		return errA
	case "flx_cria":
		// flx_cria NOME [conta=S-1117150] [lado=1] [tipo=2] [qtd=1] [preco=130000] [status=0] [id=>CAPTURA]
		if len(args) < 1 {
			return fmt.Errorf("flx_cria NOME [campo=valor ...]")
		}
		o := falso.Ordem{Nome: args[0], Conta: "S-1117150", Simbolo: "WINZ26", Lado: "1", Tipo: "2", Qtd: 1, Preco: 130000}
		captura := ""
		for _, a := range args[1:] {
			if k, nome, ok := strings.Cut(a, "=>"); ok && k == "id" {
				captura = nome
				continue
			}
			k, v, _ := strings.Cut(a, "=")
			switch k {
			case "conta":
				o.Conta = v
			case "lado":
				o.Lado = v
			case "tipo":
				o.Tipo = v
			case "qtd":
				o.Qtd, _ = strconv.Atoi(v)
			case "preco":
				o.Preco, _ = strconv.ParseInt(v, 10, 64)
			case "status":
				o.Status = v
			default:
				return fmt.Errorf("flx_cria: %q", a)
			}
		}
		id := e.flx.Criar(o)
		if captura != "" {
			e.capturas[captura] = id
		}
		return nil
	case "flx_ordem":
		// flx_ordem ID campo=valor ...: confere o estado na Flexscan falsa
		if len(args) < 1 {
			return fmt.Errorf("flx_ordem ID campo=valor ...")
		}
		e.ocioso()
		o := e.flx.Ordem(args[0])
		if o == nil {
			return fmt.Errorf("ordem %s não existe na Flexscan falsa", args[0])
		}
		vals := map[string]string{"status": o.Status, "preco": strconv.FormatInt(o.Preco, 10), "stop": strconv.FormatInt(o.Stop, 10),
			"exec": strconv.Itoa(o.Exec), "qtd": strconv.Itoa(o.Qtd), "nome": o.Nome, "conta": o.Conta}
		for _, a := range args[1:] {
			k, v, _ := strings.Cut(a, "=")
			if got, ok := vals[k]; !ok || got != v {
				return fmt.Errorf("flx_ordem %s: %s=%q, esperado %q", args[0], k, got, v)
			}
		}
		return nil
	}
	return fmt.Errorf("verbo sem execução: %s", p.Verbo)
}

func (e *Executor) flxWebhook(corpo []byte, esperado int) error {
	req := httptest.NewRequest(http.MethodPost, e.svc.caminho, bytes.NewReader(corpo))
	req.Header.Set("X-Real-IP", "177.71.196.212")
	w := httptest.NewRecorder()
	e.svc.receptor.ServeHTTP(w, req)
	if w.Code != esperado {
		return fmt.Errorf("webhook: %d, esperado %d", w.Code, esperado)
	}
	e.ocioso()
	return nil
}

// flxRecebe: flx_recebe METODO [campo=valor ...] [id=>NOME] [id=ID] — o próximo pedido de alteração
// (POST, PUT, DELETE) que a Flexscan falsa recebeu casa. Campos do corpo JSON comparados como texto
// ou número; id=>NOME captura o id (no POST, o da ordem criada).
func (e *Executor) flxRecebe(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("flx_recebe METODO [campo=valor ...]")
	}
	e.ocioso()
	var ps []falso.Pedido
	limite := time.Now().Add(esperaMensagem)
	for {
		ps = e.flx.Pedidos()
		if len(ps) > 0 || time.Now().After(limite) {
			break
		}
		time.Sleep(5 * time.Millisecond)
		e.ocioso()
	}
	if len(ps) == 0 {
		return fmt.Errorf("nenhum pedido na Flexscan (esperado %s)", args[0])
	}
	pd := ps[0]
	e.flx.Devolver(ps[1:])
	if pd.Metodo != args[0] {
		return fmt.Errorf("pedido %s %s %v, esperado %s", pd.Metodo, pd.Caminho, pd.Corpo, args[0])
	}
	id := pd.ID
	if pd.Metodo == "POST" {
		id = e.flx.PorNome(fmt.Sprint(pd.Corpo["name"]))
	}
	for _, a := range args[1:] {
		if k, nome, ok := strings.Cut(a, "=>"); ok {
			if k != "id" {
				return fmt.Errorf("flx_recebe: só id=>NOME captura")
			}
			e.capturas[nome] = id
			continue
		}
		k, v, _ := strings.Cut(a, "=")
		if k == "id" {
			if id != v {
				return fmt.Errorf("flx_recebe %s: id %s, esperado %s", pd.Metodo, id, v)
			}
			continue
		}
		x, ok := pd.Corpo[k]
		if v == "-" {
			if ok {
				return fmt.Errorf("flx_recebe %s: %s presente (%v), esperado ausente", pd.Metodo, k, x)
			}
			continue
		}
		if !ok {
			return fmt.Errorf("flx_recebe %s: %s ausente em %v", pd.Metodo, k, pd.Corpo)
		}
		if !iguaisTexto(v, fmt.Sprint(x)) && !iguaisNumero(v, fmt.Sprint(x)) {
			return fmt.Errorf("flx_recebe %s: %s=%v, esperado %s (corpo %v)", pd.Metodo, k, x, v, pd.Corpo)
		}
	}
	return nil
}

// flxWebhooks (v1.21): o cadastro de webhooks da Flexscan falsa.
//
//	flx_webhooks limpa                        o cadastro some
//	flx_webhooks falha=STATUS metodo=GET|POST a próxima chamada com esse método responde STATUS
//	flx_webhooks cadastra url=U [triggers=a,b] um cadastro feito por fora
//	flx_webhooks n=N [url=U]                  confere quantos cadastros há (com essa URL)
//	flx_webhooks gatilhos=a,b url=U            confere os triggers somados dos cadastros da URL
func (e *Executor) flxWebhooks(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("flx_webhooks: limpa | falha=STATUS metodo=M | cadastra url=U | n=N [url=U]")
	}
	if args[0] == "limpa" {
		e.flx.LimparWebhooks()
		return nil
	}
	kv := map[string]string{}
	for _, a := range args {
		k, v, _ := strings.Cut(a, "=")
		kv[k] = v
	}
	switch {
	case args[0] == "cadastra":
		ts := []string{"order.all"}
		if v := kv["triggers"]; v != "" {
			ts = strings.Split(v, ",")
		}
		e.flx.CadastrarWebhook(kv["url"], ts...)
		return nil
	case kv["falha"] != "":
		st, err := strconv.Atoi(kv["falha"])
		if err != nil || kv["metodo"] == "" {
			return fmt.Errorf("flx_webhooks falha=STATUS metodo=GET|POST")
		}
		e.flx.FalharWebhooks(kv["metodo"], st)
		return nil
	case kv["gatilhos"] != "":
		e.ocioso()
		quer := strings.Split(kv["gatilhos"], ",")
		slices.Sort(quer)
		if tem := e.flx.Gatilhos(kv["url"]); !slices.Equal(tem, quer) {
			return fmt.Errorf("flx_webhooks: gatilhos %v, esperava %v", tem, quer)
		}
		return nil
	case kv["n"] != "":
		e.ocioso()
		n := 0
		for _, u := range e.flx.Webhooks() {
			if kv["url"] == "" || u == kv["url"] {
				n++
			}
		}
		if strconv.Itoa(n) != kv["n"] {
			return fmt.Errorf("flx_webhooks: %d cadastro(s), esperava %s", n, kv["n"])
		}
		return nil
	}
	return fmt.Errorf("flx_webhooks: argumento %v", args)
}

// flxTrafego (v1.21): flx_trafego campo=valor ... = N — quantas linhas do GET /flexscan/trafego
// casam (campos como texto; null = ausente; * = presente; ~texto = contém). Toda chamada confere que
// nenhum token da falsa, a senha, o segredo do webhook nem a FLEXSCAN_WEBHOOK_URL aparecem no corpo.
func (e *Executor) flxTrafego(args []string) error {
	if len(args) < 2 || args[len(args)-2] != "=" {
		return fmt.Errorf("flx_trafego campo=valor ... = N")
	}
	esperado := args[len(args)-1]
	e.ocioso()
	rec := httptest.NewRecorder()
	e.svc.http.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/flexscan/trafego?limite=2000&ambiente="+e.alvoFlx, nil))
	corpo := rec.Body.String()
	segredos := append(e.flx.Tokens(), SenhaFlexscanRoteiro, SegredoWebhookRoteiro, SenhaFlexscanRoteiro+"-real", SegredoWebhookRoteiro+"-real")
	if u := e.cfg.Texto("FLEXSCAN_WEBHOOK_URL", ""); u != "" {
		segredos = append(segredos, u)
	}
	for _, s := range segredos {
		if s != "" && strings.Contains(corpo, s) {
			return fmt.Errorf("flx_trafego: segredo no corpo do /flexscan/trafego (%d caracteres)", len(s))
		}
	}
	var r struct {
		Mensagens []map[string]any `json:"mensagens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		return fmt.Errorf("flx_trafego: %v (%s)", err, corpo)
	}
	n := 0
	var vistas []string
	for _, m := range r.Mensagens {
		casa := true
		for _, a := range args[:len(args)-2] {
			k, v, _ := strings.Cut(a, "=")
			got := "null"
			if m[k] != nil {
				got = fmt.Sprint(m[k])
			}
			switch {
			case v == "*" && m[k] != nil:
			case strings.HasPrefix(v, "~") && strings.Contains(got, v[1:]):
			case got == v:
			default:
				casa = false
			}
			if !casa {
				break
			}
		}
		if casa {
			n++
		}
		vistas = append(vistas, fmt.Sprintf("[%v %v %v %v]", m["tipo"], m["status_http"], m["clordid"], m["resumo"]))
	}
	if strconv.Itoa(n) != esperado {
		return fmt.Errorf("flx_trafego %v: %d linha(s), esperava %s; linhas: %v", args[:len(args)-2], n, esperado, vistas)
	}
	return nil
}
