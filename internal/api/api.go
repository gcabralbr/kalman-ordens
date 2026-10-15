// Package api é o HTTP local do serviço (127.0.0.1:9092). Único cliente: o backend do Kalman
// (e o CLI por SSH). net/http da biblioteca padrão: dez rotas, nada mais.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/motor"
)

// Handler monta as rotas. espera = quanto o POST /ordens espera o motor antes do 503.
func Handler(m *motor.Motor, espera time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ordens", func(w http.ResponseWriter, r *http.Request) {
		var p motor.Pedido
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			escrever(w, http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()})
			return
		}
		select {
		case r := <-m.PedirAssincrono(p):
			escrever(w, http.StatusOK, r)
		case <-time.After(espera):
			// o motor segue com o pedido; o gestor reenvia o mesmo corpo e recebe o resultado original
			escrever(w, http.StatusServiceUnavailable, motor.Resposta{"aceito": false, "codigo": "RESULTADO_DESCONHECIDO",
				"motivo": "o motor nao respondeu a tempo; reenviar o mesmo pedido"})
		}
	})
	// ajuste da proteção (v1.4 K1): novo stop em preço (preco_stop) ou em pontos da entrada (stop_pts)
	mux.HandleFunc("POST /operacoes/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			escrever(w, http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": "operacao_id invalido"})
			return
		}
		var a motor.AjusteStop
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			escrever(w, http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()})
			return
		}
		select {
		case r := <-m.AjustarStopAssincrono(id, a):
			escrever(w, http.StatusOK, r)
		case <-time.After(espera):
			// o motor segue com o pedido; o último ajuste vale, reenviar o mesmo é inofensivo
			escrever(w, http.StatusServiceUnavailable, motor.Resposta{"aceito": false, "codigo": "RESULTADO_DESCONHECIDO",
				"motivo": "o motor nao respondeu a tempo; consultar GET /ordens/{id} ou reenviar o mesmo ajuste"})
		}
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
	mux.HandleFunc("POST /parada", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Modo, Origem, Motivo string }
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			escrever(w, http.StatusBadRequest, motor.Resposta{"aceito": false, "codigo": "CAMPO_INVALIDO", "motivo": err.Error()})
			return
		}
		escrever(w, http.StatusOK, m.AtivarParada(b.Modo, b.Origem, b.Motivo))
	})
	mux.HandleFunc("POST /retomar", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Origem, Motivo string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		escrever(w, http.StatusOK, m.Retomar(b.Origem, b.Motivo))
	})
	mux.HandleFunc("GET /saude", func(w http.ResponseWriter, r *http.Request) {
		escrever(w, http.StatusOK, m.Saude())
	})
	return mux
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
