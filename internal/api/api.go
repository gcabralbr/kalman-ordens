// Package api é o HTTP local do serviço (127.0.0.1:9092). Único cliente: o backend do Kalman
// (e o CLI por SSH). net/http da biblioteca padrão, nada mais.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/motor"
)

// Handler monta as rotas. espera = quanto o POST /ordens espera o motor antes do 503.
func Handler(m *motor.Motor, espera time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ordens", func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(io.LimitReader(r.Body, limiteCorpo))
		var p motor.Pedido
		status, resp := http.StatusOK, motor.Resposta(nil)
		if err := decodificar(corpo, &p); err != nil {
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()}
		} else {
			select {
			case resp = <-m.PedirAssincrono(p):
			case <-time.After(espera):
				// o motor segue com o pedido; o gestor reenvia o mesmo corpo e recebe o resultado original
				status, resp = http.StatusServiceUnavailable, motor.Resposta{"aceito": false, "codigo": "RESULTADO_DESCONHECIDO",
					"motivo": "o motor nao respondeu a tempo; reenviar o mesmo pedido"}
			}
		}
		// v1.6 P1: o pedido como chegou e a resposta dada, inclusive 400 e 503
		m.RegistrarPedidoHTTP("ORDENS", p.Modulo, valor(p.OperacaoID), valor(p.UID), p.Papel, corpo, status, resp)
		escrever(w, status, resp)
	})
	// POST em lote por sinal (v1.10 §6): o motor responde por parte; cada item fica no histórico de
	// pedidos da operação (rota "POST /sinais", gravado pelo motor). Aqui só a recusa do lote
	// inteiro, o 503 e o reenvio (repetido) viram uma linha do lote.
	mux.HandleFunc(motor.RotaSinais, func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(io.LimitReader(r.Body, limiteCorpoSinais+1))
		var p motor.PedidoSinal
		status, resp := http.StatusOK, motor.Resposta(nil)
		switch err := decodificar(corpo, &p); {
		case len(corpo) > limiteCorpoSinais:
			corpo = corpo[:limiteCorpoSinais]
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "LIMITE_LOTE", "motivo": "corpo acima de 1 MB"}
		case err != nil:
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()}
		default:
			select {
			case resp = <-m.SinalAssincrono(p):
				if resp["aceito"] != true {
					status = http.StatusBadRequest
				}
			case <-time.After(espera):
				// o motor segue com a parte; o gestor reenvia o mesmo corpo e recebe a resposta original
				status, resp = http.StatusServiceUnavailable, motor.Resposta{"aceito": false, "codigo": "RESULTADO_DESCONHECIDO",
					"motivo": "o motor nao respondeu a tempo; reenviar o mesmo corpo"}
			}
		}
		if status != http.StatusOK || resp["repetido"] == true {
			m.RegistrarPedidoHTTP(motor.RotaSinais, p.Modulo, 0, 0, "", corpo, status, resp)
		}
		escrever(w, status, resp)
	})
	// ajuste da proteção (v1.4 K1): novo stop em preço (preco_stop) ou em pontos da entrada (stop_pts)
	mux.HandleFunc("POST /operacoes/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(io.LimitReader(r.Body, limiteCorpo))
		var a motor.AjusteStop
		id, errID := strconv.ParseInt(r.PathValue("id"), 10, 64)
		status, resp := http.StatusOK, motor.Resposta(nil)
		switch err := decodificar(corpo, &a); {
		case errID != nil:
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": "operacao_id invalido"}
		case err != nil:
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()}
		default:
			select {
			case resp = <-m.AjustarStopAssincrono(id, a):
			case <-time.After(espera):
				// o motor segue com o pedido; o último ajuste vale, reenviar o mesmo é inofensivo
				status, resp = http.StatusServiceUnavailable, motor.Resposta{"aceito": false, "codigo": "RESULTADO_DESCONHECIDO",
					"motivo": "o motor nao respondeu a tempo; consultar GET /ordens/{id} ou reenviar o mesmo ajuste"}
			}
		}
		m.RegistrarPedidoHTTP("STOP", a.Modulo, id, valor(a.UID), "", corpo, status, resp)
		escrever(w, status, resp)
	})
	mux.HandleFunc("GET /ordens/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			escrever(w, http.StatusBadRequest, motor.Resposta{"existe": false})
			return
		}
		escrever(w, http.StatusOK, m.Consultar(r.URL.Query().Get("modulo"), id))
	})
	// histórico para a tela de admin (v1.5 R): só leitura, direto do banco, qualquer dia
	mux.HandleFunc("GET /operacoes", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		uid := 0
		if v := q.Get("uid"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				escrever(w, http.StatusBadRequest, motor.Resposta{"erro": "uid invalido"})
				return
			}
			uid = n
		}
		l, err := motor.OperacoesDoDia(m.Repo(), q.Get("dia"), uid, q.Get("modulo"))
		responderHistorico(w, l, err)
	})
	mux.HandleFunc("GET /operacoes/{id}/historico", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			escrever(w, http.StatusBadRequest, motor.Resposta{"erro": "operacao_id invalido"})
			return
		}
		h, err := motor.HistoricoOperacao(m.Repo(), r.URL.Query().Get("modulo"), id, r.URL.Query().Get("cru") == "1")
		responderHistorico(w, h, err)
	})
	mux.HandleFunc("GET /sessao/eventos", func(w http.ResponseWriter, r *http.Request) {
		l, err := motor.EventosDaSessao(m.Repo(), r.URL.Query().Get("dia"))
		responderHistorico(w, l, err)
	})
	mux.HandleFunc("GET /eventos", func(w http.ResponseWriter, r *http.Request) {
		desde, _ := strconv.ParseInt(r.URL.Query().Get("desde"), 10, 64)
		limite, _ := strconv.Atoi(r.URL.Query().Get("limite"))
		if limite <= 0 || limite > 1000 {
			limite = 500
		}
		evs, err := m.Eventos(desde, limite, r.URL.Query().Get("modulo"))
		if err != nil {
			escrever(w, http.StatusInternalServerError, motor.Resposta{"erro": err.Error()})
			return
		}
		if evs == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		escrever(w, http.StatusOK, evs)
	})
	// parada e retomada (v1.7 A2): aceitam e gravam motivo e por (uid do admin; a CLI não manda)
	mux.HandleFunc("POST /parada", func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(io.LimitReader(r.Body, limiteCorpo))
		var b struct {
			Modo, Origem, Motivo string
			Por                  *int
		}
		status, resp := http.StatusOK, motor.Resposta(nil)
		if err := json.Unmarshal(corpo, &b); err != nil {
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()}
		} else {
			resp = m.AtivarParada(b.Modo, b.Origem, b.Motivo, valor(b.Por))
		}
		m.RegistrarPedidoHTTP("PARADA", "", 0, 0, "", corpo, status, resp)
		escrever(w, status, resp)
	})
	mux.HandleFunc("POST /retomar", func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(io.LimitReader(r.Body, limiteCorpo))
		var b struct {
			Origem, Motivo string
			Por            *int
		}
		_ = json.Unmarshal(corpo, &b)
		resp := m.Retomar(b.Origem, b.Motivo, valor(b.Por))
		m.RegistrarPedidoHTTP("RETOMAR", "", 0, 0, "", corpo, http.StatusOK, resp)
		escrever(w, http.StatusOK, resp)
	})
	// ações de admin sobre uma operação (v1.7 A1): {motivo, por} obrigatórios; ?modulo= (ausente = KALMAN)
	for rota, acao := range map[string]string{"encerrar": motor.AcaoEncerrar, "cancelar-entrada": motor.AcaoCancelarEntrada} {
		acao := acao
		mux.HandleFunc("POST /operacoes/{id}/"+rota, func(w http.ResponseWriter, r *http.Request) {
			corpo, _ := io.ReadAll(io.LimitReader(r.Body, limiteCorpo))
			modulo := r.URL.Query().Get("modulo")
			var a motor.AcaoAdmin
			id, errID := strconv.ParseInt(r.PathValue("id"), 10, 64)
			status, resp := http.StatusOK, motor.Resposta(nil)
			switch err := decodificar(corpo, &a); {
			case errID != nil:
				status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": "operacao_id invalido"}
			case err != nil:
				status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()}
			default:
				select {
				case resp = <-m.AcaoOperacaoAssincrona(acao, modulo, id, a):
				case <-time.After(espera):
					status, resp = http.StatusServiceUnavailable, motor.Resposta{"aceito": false, "codigo": "RESULTADO_DESCONHECIDO",
						"motivo": "o motor nao respondeu a tempo; consultar GET /ordens/{id}"}
				}
			}
			m.RegistrarPedidoHTTP(acao, modulo, id, 0, "", corpo, status, resp)
			escrever(w, status, resp)
		})
	}
	// habilitação do usuário pelo admin (v1.8 H): consulta e bloqueio/desbloqueio
	mux.HandleFunc("GET /usuarios/{uid}/habilitacao", func(w http.ResponseWriter, r *http.Request) {
		uid, err := strconv.Atoi(r.PathValue("uid"))
		if err != nil || uid <= 0 {
			escrever(w, http.StatusBadRequest, motor.Resposta{"erro": "uid invalido"})
			return
		}
		h, err := motor.HabilitacaoDoUsuario(m.Repo(), uid)
		responderHistorico(w, h, err)
	})
	mux.HandleFunc("POST /usuarios/{uid}/habilitacao", func(w http.ResponseWriter, r *http.Request) {
		corpo, _ := io.ReadAll(io.LimitReader(r.Body, limiteCorpo))
		var a motor.HabilitacaoAdmin
		uid, errUID := strconv.Atoi(r.PathValue("uid"))
		status, resp := http.StatusOK, motor.Resposta(nil)
		switch err := decodificar(corpo, &a); {
		case errUID != nil || uid <= 0:
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": "uid invalido"}
		case err != nil:
			status, resp = http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()}
		default:
			select {
			case resp = <-m.AlterarHabilitacaoAssincrono(uid, a):
			case <-time.After(espera):
				status, resp = http.StatusServiceUnavailable, motor.Resposta{"aceito": false, "codigo": "RESULTADO_DESCONHECIDO",
					"motivo": "o motor nao respondeu a tempo; consultar GET /usuarios/{uid}/habilitacao"}
			}
		}
		m.RegistrarPedidoHTTP("HABILITACAO", "", 0, uid, "", corpo, status, resp)
		escrever(w, status, resp)
	})
	// ordens no PnT (v1.7 C): um 35=AF por conta, só leitura; intervalo mínimo com cache
	mux.HandleFunc("GET /pnt/ordens", func(w http.ResponseWriter, r *http.Request) {
		select {
		case resp := <-m.ConsultarPnTAssincrono():
			if resp["erro"] != nil {
				escrever(w, http.StatusServiceUnavailable, resp)
				return
			}
			escrever(w, http.StatusOK, resp)
		case <-time.After(espera + esperaConsultaPnT):
			escrever(w, http.StatusServiceUnavailable, motor.Resposta{"erro": "RESULTADO_DESCONHECIDO", "motivo": "a consulta nao terminou a tempo"})
		}
	})
	// métricas no formato do Prometheus (v1.9 M); o HTTP inteiro só escuta em 127.0.0.1
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metricas.Escrever(w)
	})
	mux.HandleFunc("GET /saude", func(w http.ResponseWriter, r *http.Request) {
		escrever(w, http.StatusOK, m.Saude())
	})
	if os.Getenv("DEBUG_PPROF") == "1" {
		// perfil de CPU (diagnóstico, teste de carga); o HTTP inteiro só escuta em 127.0.0.1
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	}
	return medirHTTP(mux)
}

// medirHTTP: duração de cada requisição por rota (o padrão do mux) e status (http_segundos).
func medirHTTP(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		sw := &comStatus{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(sw, r)
		rota := r.Pattern
		if rota == "" {
			rota = "desconhecida"
		}
		metricas.HTTP.Observar(time.Since(t0).Seconds(), rota, strconv.Itoa(sw.status))
	})
}

type comStatus struct {
	http.ResponseWriter
	status int
}

func (c *comStatus) WriteHeader(s int) { c.status = s; c.ResponseWriter.WriteHeader(s) }

// esperaConsultaPnT: folga do GET /pnt/ordens sobre a espera da própria consulta (CONSULTA_PNT_ESPERA_S).
const esperaConsultaPnT = 10 * time.Second

// limiteCorpo: os pedidos têm algumas centenas de bytes; o POST /sinais, até 1.000 itens (v1.10 §6).
const (
	limiteCorpo       = 64 * 1024
	limiteCorpoSinais = 1 << 20
)

// decodificar: JSON estrito (campo desconhecido = erro), como antes da v1.6.
func decodificar(corpo []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(corpo))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func valor[T int | int64](p *T) T {
	if p == nil {
		return 0
	}
	return *p
}

// responderHistorico: 404 para operação inexistente, 400 para dia inválido, 500 para o resto.
func responderHistorico(w http.ResponseWriter, v any, err error) {
	switch {
	case err == nil:
		escrever(w, http.StatusOK, v)
	case errors.Is(err, motor.ErrNaoEncontrada):
		escrever(w, http.StatusNotFound, motor.Resposta{"existe": false})
	case errors.Is(err, motor.ErrDia):
		escrever(w, http.StatusBadRequest, motor.Resposta{"erro": "dia obrigatorio, AAAA-MM-DD"})
	default:
		escrever(w, http.StatusInternalServerError, motor.Resposta{"erro": err.Error()})
	}
}

func escrever(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
