package flexscan

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// Tráfego recarregado na partida (28/10): as trocas do dia com a Flexscan, do kalmanordemevento
// (o que a ponte grava a cada pedido, resposta, consulta que mudou o estado, webhook, ajuste de preço,
// login e cadastro do webhook), no mesmo formato das linhas ao vivo, para a aba "Flexscan ao vivo"
// não ficar vazia depois de um restart no meio do pregão. As consultas seguem a regra de sempre: só as
// que falharam ou mudaram o estado (as outras nunca foram gravadas). Eventos de antes da 0013 (msgtype
// cortado em "FLX-") não dizem o que foram e ficam de fora.

// Tipos gravados só para o recarregamento (v1.21): o login e o cadastro do webhook.
const (
	msgLogin    = "FLX-LOGIN"
	msgCadastro = "FLX-WHCAD"
)

// CarregarTrafego: as linhas do registro do dia, antes de qualquer linha ao vivo (chamado na partida,
// antes do login). Devolve quantas entraram (no máximo a capacidade do buffer: as mais recentes; só
// as do dia corrente).
func (p *Ponte) CarregarTrafego(evs []repo.EventoFIX) int {
	ls := LinhasDoRegistro(evs)
	if len(ls) > CapacidadeTrafego {
		ls = ls[len(ls)-CapacidadeTrafego:]
	}
	return p.traf.carregar(ls)
}

// carregar: só as linhas do dia do buffer (v1.23: a de antes da meia-noite sairia na virada).
func (t *trafego) carregar(ls []LinhaTrafego) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.virar(t.rel.Agora())
	n := 0
	for _, l := range ls {
		if l.em.Before(t.dia) {
			continue
		}
		n++
		t.seq++
		l.Seq = t.seq
		pos := (t.ini + t.n) % CapacidadeTrafego
		if t.n == CapacidadeTrafego {
			t.ini = (t.ini + 1) % CapacidadeTrafego
		} else {
			t.n++
		}
		t.buf[pos] = l
	}
	t.recarregadas += n
	return n
}

// LinhasDoRegistro converte os eventos da ponte (em ordem) nas linhas do tráfego.
func LinhasDoRegistro(evs []repo.EventoFIX) []LinhaTrafego {
	type chave struct{ metodo, cl string }
	pendentes := map[chave]repo.EventoFIX{}
	var ls []LinhaTrafego
	nova := func(em time.Time, tipo, direcao string, status int, dur time.Duration, cl, id, resumo string, nomes ...string) {
		l := LinhaTrafego{Tipo: tipo, Direcao: direcao, Resumo: resumoTrafego(resumo), ClOrdIDs: nomes, em: em,
			Em: em.In(relogio.Fuso).Format(formatoEmTrafego)}
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
		ls = append(ls, l)
	}
	for _, e := range evs {
		tipo := strings.TrimPrefix(e.MsgType, "FLX-")
		switch {
		case e.Direcao == "ENV" && (tipo == "POST" || tipo == "PUT" || tipo == "DELETE"):
			pendentes[chave{tipo, e.ClOrdID}] = e
		case e.MsgType == msgLogin:
			nova(e.Em, TrafegoLogin, "ENV", statusGravado(e.Codigo), -1, "", "", e.Texto)
		case e.MsgType == msgCadastro:
			nova(e.Em, TrafegoWebhookCadastro, "ENV", statusGravado(e.Codigo), -1, "", "", e.Texto)
		case e.Direcao == "REC" && tipo == "WH":
			es, err := LerWebhook([]byte(e.Bruta))
			if err != nil {
				nova(e.Em, TrafegoWebhook, "REC", 200, -1, "", "", "forma desconhecida: "+err.Error())
			} else {
				cl, id, resumo, nomes := resumoWebhook(es)
				nova(e.Em, TrafegoWebhook, "REC", 200, -1, cl, id, resumo, nomes...)
			}
			// o IP de origem gravado desde a ponte (23/10: "origem <ip>")
			if ip := strings.TrimSpace(strings.TrimPrefix(e.Texto, "origem ")); ip != "" && strings.HasPrefix(e.Texto, "origem ") {
				ls[len(ls)-1].IP = &ip
			}
		case e.Direcao == "REC" && tipo == "PRECO":
			resumo := e.Texto
			if de, para, ok := strings.Cut(strings.TrimPrefix(e.Texto, "preço ajustado pela Flexscan: "), " -> "); ok {
				resumo = "enviado " + de + " efetivo " + para
			}
			nova(e.Em, TrafegoPreco, "REC", -1, -1, e.ClOrdID, e.OrderID, resumo)
		case e.Direcao == "REC" && tipo == "GET":
			// consulta que mudou o estado (o corpo é a consulta)
			var rec map[string]any
			resumo := "consulta"
			if json.Unmarshal([]byte(e.Bruta), &rec) == nil {
				resumo = "consulta: " + resumoEstado(LerConsulta(rec))
			}
			nova(e.Em, TrafegoConsulta, "ENV", 200, -1, e.ClOrdID, e.OrderID, resumo)
		case e.Direcao == "REC" && (tipo == "SEMRESPOSTA" || ehStatus(tipo)):
			status := -1
			if tipo != "SEMRESPOSTA" {
				status, _ = strconv.Atoi(tipo)
			}
			metodo, texto, _ := strings.Cut(e.Texto, " ")
			if metodo == "GET" {
				nova(e.Em, TrafegoConsulta, "ENV", status, -1, e.ClOrdID, e.OrderID, comTexto("consulta", status, texto))
				ls[len(ls)-1].NaoRegistrada = strings.HasPrefix(texto, PrefixoNaoRegistrada)
				continue
			}
			env, ok := pendentes[chave{metodo, e.ClOrdID}]
			if !ok {
				continue
			}
			delete(pendentes, chave{metodo, e.ClOrdID})
			resumo, id := "cancelamento", e.OrderID
			if metodo != "DELETE" {
				resumo = resumoOrdem(corpoGravado(env.Bruta))
			}
			if metodo == "POST" && status == 202 {
				var criada struct {
					ID string `json:"id"`
				}
				_ = json.Unmarshal([]byte(e.Bruta), &criada)
				id = criada.ID
			}
			dur := e.Em.Sub(env.Em)
			if status < 0 {
				dur = -1
			}
			nova(env.Em, metodo, "ENV", status, dur, e.ClOrdID, id, comTexto(resumo, status, texto))
		}
	}
	return ls
}

// corpoGravado: o "METODO /caminho k=v k=v" do registrarEnvio de volta num mapa.
func corpoGravado(bruta string) map[string]any {
	m := map[string]any{}
	for i, campo := range strings.Fields(bruta) {
		if i < 2 {
			continue
		}
		if k, v, ok := strings.Cut(campo, "="); ok {
			m[k] = v
		}
	}
	return m
}

func comTexto(resumo string, status int, texto string) string {
	texto = strings.TrimSpace(texto)
	switch {
	case status < 0:
		return strings.TrimSpace(resumo + " -> sem resposta: " + texto)
	case status >= 300 && texto != "":
		return resumo + " -> " + texto
	}
	return resumo
}

func statusGravado(c string) int {
	if n, err := strconv.Atoi(c); err == nil {
		return n
	}
	return -1
}

func ehStatus(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil && s != ""
}
