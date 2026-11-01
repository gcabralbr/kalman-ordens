package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
)

// A boleta da CLI (v1.31): confere o /destino antes, pede "sim" no REAL e manda o POST /ordens do
// módulo BOLETA com o por, a conta e a corretora esperadas.
func TestBoletaCLI(t *testing.T) {
	var pedidos []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/usuarios/5725/destino":
			io.WriteString(w, `{"uid":5725,"ambientes":{"REAL":{"pode_enviar":true,"corretora":"BTG","corretora_codigo":85,"conta":"4582351","codigo":null}}}`)
		case r.Method == "GET" && r.URL.Path == "/usuarios/8/destino":
			io.WriteString(w, `{"uid":8,"ambientes":{"REAL":{"pode_enviar":false,"codigo":"REAL_UID_NAO_PERMITIDO","motivo":"uid 8 fora de REAL_UIDS"}}}`)
		case r.Method == "POST" && r.URL.Path == "/ordens":
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			pedidos = append(pedidos, p)
			io.WriteString(w, `{"aceito":true,"operacao_id":1103101500}`)
		case r.Method == "POST" && r.URL.Path == "/operacoes/1103101500/cancelar-entrada":
			if r.URL.Query().Get("modulo") != "BOLETA" {
				t.Errorf("cancelar sem ?modulo=BOLETA: %s", r.URL)
			}
			io.WriteString(w, `{"aceito":true,"codigo":"CANCELANDO"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := config.Novo(map[string]string{"HTTP_ENDERECO": strings.TrimPrefix(srv.URL, "http://")})
	agora := time.Date(2026, 11, 3, 13, 15, 0, 0, time.UTC) // 10:15:00 em São Paulo
	enviar := []string{"enviar", "-uid", "5725", "-ambiente", "REAL", "-lado", "C", "-qtd", "1", "-stop", "300", "-por", "1", "-motivo", "teste", "-conta", "4582351", "-corretora", "BTG"}
	var saida bytes.Buffer

	// sem "sim", nada sai
	if err := cliBoleta(cfg, enviar, strings.NewReader("nao\n"), &saida, agora); err == nil || len(pedidos) != 0 {
		t.Fatalf("sem confirmação: err=%v pedidos=%d", err, len(pedidos))
	}
	// a conta esperada diferente da coluna: para antes de perguntar
	errado := append(append([]string{}, enviar[:len(enviar)-4]...), "-conta", "9999")
	if err := cliBoleta(cfg, errado, strings.NewReader("sim\n"), &saida, agora); err == nil || !strings.Contains(err.Error(), "4582351") || len(pedidos) != 0 {
		t.Fatalf("conta divergente: err=%v pedidos=%d", err, len(pedidos))
	}
	// o uid que não pode enviar
	if err := cliBoleta(cfg, []string{"destino", "-uid", "8", "-ambiente", "REAL"}, nil, &saida, agora); err == nil || !strings.Contains(err.Error(), "REAL_UID_NAO_PERMITIDO") {
		t.Fatalf("uid sem permissão: %v", err)
	}
	// com "sim": o corpo do módulo BOLETA, o operacao_id MMDDHHMMSS do fuso do pregão
	if err := cliBoleta(cfg, enviar, strings.NewReader("sim\n"), &saida, agora); err != nil {
		t.Fatal(err)
	}
	if len(pedidos) != 1 {
		t.Fatalf("pedidos: %d", len(pedidos))
	}
	p := pedidos[0]
	for k, v := range map[string]any{"modulo": "BOLETA", "papel": "ENTRADA", "uid": 5725.0, "por": 1.0, "conta": "4582351",
		"corretora": "BTG", "ambiente": "REAL", "lado": "C", "qtd": 1.0, "stop_pts": 300.0, "operacao_id": 1103101500.0} {
		if p[k] != v {
			t.Errorf("%s = %v, esperava %v", k, p[k], v)
		}
	}
	if !strings.HasPrefix(p["ts_decisao"].(string), "2026-11-03T10:15:00.000-03:00") {
		t.Errorf("ts_decisao = %v", p["ts_decisao"])
	}
	if err := cliBoleta(cfg, []string{"cancelar", "-op", "1103101500", "-por", "1", "-motivo", "x"}, nil, &saida, agora); err != nil {
		t.Fatal(err)
	}
}
