package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	flxfalso "gammaquant.com.br/kalman-ordens/internal/flexscan/falso"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// PNT_DESLIGADO (28/10): sobe sem as chaves FIX_* e sem ORDENS_CONTAS_HML, não lê a fonte do PnT
// (PRECOS_URL quando há PRECOS_URL_FLEXSCAN), loga só na Flexscan e recusa a entrada de uid PNT
// com CONECTOR_DESLIGADO. Sem FLEXSCAN_URL, não sobe.
func TestServirPnTDesligado(t *testing.T) {
	dsn := os.Getenv("KALMAN_TESTE_MARIADB")
	if dsn == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	r, err := repo.AbrirMariaDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Fechar()
	if err := r.LimparTeste(); err != nil {
		t.Fatal(err)
	}
	if err := r.Migrar(); err != nil {
		t.Fatal(err)
	}
	if err := r.PrepararUsuariosTeste(); err != nil {
		t.Fatal(err)
	}
	if err := r.DefinirUsuario(7, "hml", `{"Robo":{"CORSeqId":85,"CCONroConta":"12345"}}`); err != nil {
		t.Fatal(err)
	}
	if err := r.Executar(`INSERT INTO kalmanordemusuario (uid, habilitado, posicao_max, qtd_max_ordem, conectores, alterado_por, motivo, alterado_em)
		VALUES (7, 1, 0, 0, 'PNT', 'teste', 'pnt desligado', ?)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var lidasPnT atomic.Int64
	pnt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		lidasPnT.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer pnt.Close()
	md := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ts := time.Now().In(time.FixedZone("BRT", -3*3600)).Format(time.RFC3339)
		w.Write([]byte(`{"WINFUT":{"ticker":"WINZ26","preco":170370.0,"bid":170365.0,"ask":170370.0,"status84":0,"ts":"` + ts + `","idade_seg":0.1}}`))
	}))
	defer md.Close()
	flx := flxfalso.Novo("flx_desligado", "senha-desligado", time.Now)
	defer flx.Parar()
	livre := func() string {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer ln.Close()
		return ln.Addr().String()
	}
	endereco := livre()
	dir, _ := os.MkdirTemp("", "pnt-desligado")
	base := map[string]string{
		"DB_DSN": dsn, "PNT_DESLIGADO": "1", "ORDENS_AMBIENTE": "HML", "ACUSUARIO_TABELA": "acusuario_teste",
		"HTTP_ENDERECO": endereco, "PRECOS_URL": pnt.URL, "PRECOS_URL_FLEXSCAN": md.URL, "MASS_STATUS_SILENCIO_S": "0.5",
		"FLEXSCAN_USUARIO": "flx_desligado", "FLEXSCAN_SENHA": "senha-desligado",
		"FLEXSCAN_CONTAS_HML": "S-1117150", "FLEXSCAN_ESTADO_LOGIN": dir + "/flexscan-login.json",
	}

	// sem FLEXSCAN_URL: não sobe
	if err := servir(context.Background(), config.Novo(base)); err == nil || !strings.Contains(err.Error(), "FLEXSCAN_URL") {
		t.Fatalf("PNT_DESLIGADO sem FLEXSCAN_URL: %v", err)
	}

	base["FLEXSCAN_URL"] = flx.URL()
	base["FLEXSCAN_JANELA"] = "sempre" // relógio real: o teste não depende da hora em que roda (v1.28)
	ok := make(chan struct{})
	pronto = func(string) { close(ok) }
	ctx, desligar := context.WithCancel(context.Background())
	fim := make(chan error, 1)
	go func() { fim <- servir(ctx, config.Novo(base)) }()
	select {
	case <-ok:
	case err := <-fim:
		t.Fatalf("não subiu com PNT_DESLIGADO: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("servir não ficou pronto")
	}

	// o login da Flexscan (assíncrono) e a conciliação dele fecham; a entrada de uid PNT é recusada
	// com o motivo claro
	corpo := []byte(`{"operacao_id":2801,"uid":7,"papel":"ENTRADA","lado":"C","qtd":1,"stop_pts":500,"motivo":"TESTE"}`)
	var resp map[string]any
	limite := time.Now().Add(10 * time.Second)
	for {
		rr, err := http.Post("http://"+endereco+"/ordens", "application/json", bytes.NewReader(corpo))
		if err != nil {
			t.Fatal(err)
		}
		resp = map[string]any{}
		_ = json.NewDecoder(rr.Body).Decode(&resp)
		rr.Body.Close()
		if (resp["codigo"] != "CONCILIANDO" && resp["codigo"] != "SEM_SESSAO") || time.Now().After(limite) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if resp["aceito"] != false || resp["codigo"] != "CONECTOR_DESLIGADO" {
		t.Fatalf("entrada de uid PNT: %v", resp)
	}
	var s map[string]any
	var f map[string]any
	for limite := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		s = map[string]any{}
		obter(t, "http://"+endereco+"/saude", &s)
		f, _ = s["flexscan"].(map[string]any)
		if (f["logado"] == true && s["simbolo"] == "WINZ26") || time.Now().After(limite) {
			break
		}
	}
	if s["sessao_logada"] != false || s["pnt_desligado"] != true || s["simbolo"] != "WINZ26" || s["idade_preco_ms"] == nil || f["logado"] != true {
		t.Fatalf("/saude: %v", s)
	}
	if n := lidasPnT.Load(); n != 0 {
		t.Fatalf("a fonte do PnT foi lida %d vezes", n)
	}
	if flx.Logins() != 1 {
		t.Fatalf("logins na Flexscan: %d", flx.Logins())
	}
	desligar()
	if err := <-fim; err != nil {
		t.Fatal(err)
	}
}
