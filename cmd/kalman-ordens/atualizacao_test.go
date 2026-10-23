package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	flxfalso "gammaquant.com.br/kalman-ordens/internal/flexscan/falso"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/migracoes"
	"gammaquant.com.br/kalman-ordens/pnt/falso"
)

// ATUALIZAÇÃO (23/10): o binário novo sobe em cima do banco que a versão instalada na .53 deixou
// (testdata/instalado, gerado por scripts/instalado-migracoes.sh), com dados no formato antigo, e só
// então faz login na Flexscan. O eb65ec2 só tinha sido testado com o banco vazio: na .53 pulou a
// 0011 (já aplicada sem a coluna nova) e não subiu. Segunda parte: uma partida que falha antes do
// fim não faz login na Flexscan (37 partidas em 3 min não podem virar 37 logins).
func TestServirAtualizaDoInstalado(t *testing.T) {
	dsn := os.Getenv("KALMAN_TESTE_MARIADB")
	if dsn == "" {
		t.Skip("KALMAN_TESTE_MARIADB vazio")
	}
	versao, err := os.ReadFile("testdata/instalado/VERSAO")
	if err != nil {
		t.Fatal(err)
	}
	instalado, err := migracoes.De(os.DirFS("testdata/instalado"))
	if err != nil || len(instalado) == 0 {
		t.Fatalf("testdata/instalado: %v", err)
	}
	r, err := repo.AbrirMariaDB(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Fechar()
	if err := r.LimparTeste(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MigrarCom(instalado); err != nil {
		t.Fatalf("migrações do instalado (%s): %v", strings.TrimSpace(string(versao)), err)
	}
	if err := r.PrepararUsuariosTeste(); err != nil {
		t.Fatal(err)
	}
	if err := r.DefinirUsuario(7, "hml", `{"Robo":{"CORSeqId":85,"CCONroConta":"12345"}}`); err != nil {
		t.Fatal(err)
	}
	// dados no formato do instalado: a habilitação e uma operação encerrada de hoje, gravadas só com
	// as colunas que ele conhecia
	hoje := time.Now().In(time.FixedZone("BRT", -3*3600)).Format("2006-01-02")
	agora := time.Now().UTC()
	if err := r.Executar(`INSERT INTO kalmanordemusuario (uid, habilitado, posicao_max, qtd_max_ordem, alterado_por, motivo, alterado_em)
		VALUES (7, 1, 0, 0, 'teste', 'instalado', ?)`, agora); err != nil {
		t.Fatal(err)
	}
	if err := r.Executar(`INSERT INTO kalmanordemoperacao (data, operacao_id, uid, conta, destino, simbolo, lado, qtd_pedida, stop_pts, estado,
		criado_em, atualizado_em) VALUES (?, 8801, 7, '12345', 85, 'WINZ26', 'C', 1, 500, 'ZERADA', ?, ?)`, hoje, agora, agora); err != nil {
		t.Fatal(err)
	}

	f, err := falso.Novo(falso.Config{Contraparte: "KALMAN_ATUALIZA"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Iniciar(); err != nil {
		t.Fatal(err)
	}
	defer f.Parar()
	flx := flxfalso.Novo("flx_atualiza", "senha-atualiza", time.Now)
	defer flx.Parar()
	md := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ts := time.Now().In(time.FixedZone("BRT", -3*3600)).Format(time.RFC3339)
		w.Write([]byte(`{"WINFUT":{"ticker":"WINZ26","preco":170370.0,"bid":170365.0,"ask":170370.0,"status84":0,"ts":"` + ts + `","idade_seg":0.1}}`))
	}))
	defer md.Close()
	livre := func() string {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer ln.Close()
		return ln.Addr().String()
	}
	endereco := livre()
	dirFix, _ := os.MkdirTemp("", "atualiza-fix")
	cfg := config.Novo(map[string]string{
		"DB_DSN": dsn, "FIX_SENDER_COMP_ID": "KALMAN_ATUALIZA", "FIX_HOST": "127.0.0.1", "FIX_PORTA": strconv.Itoa(f.Porta()),
		"FIX_DIR": dirFix, "FIX_INICIO": "00:00:00", "FIX_FIM": "00:00:00", "FIX_RECONEXAO_S": "1",
		"ORDENS_AMBIENTE": "HML", "ORDENS_CONTAS_HML": "12345", "ACUSUARIO_TABELA": "acusuario_teste",
		"HTTP_ENDERECO": endereco, "PRECOS_URL": md.URL, "MASS_STATUS_SILENCIO_S": "0.5",
		"FLEXSCAN_URL": flx.URL(), "FLEXSCAN_USUARIO": "flx_atualiza", "FLEXSCAN_SENHA": "senha-atualiza",
		"FLEXSCAN_CONTAS_HML": "S-1117150", "FLEXSCAN_ESTADO_LOGIN": dirFix + "/flexscan-login.json",
		"FLEXSCAN_WEBHOOK_ENDERECO": livre(), "FLEXSCAN_WEBHOOK_SEGREDO": "segredo-da-atualizacao-0123456789abcdef",
	})
	subir := func() (chan error, context.CancelFunc, chan struct{}) {
		ok := make(chan struct{})
		pronto = func(string) { close(ok) }
		ctx, desligar := context.WithCancel(context.Background())
		fim := make(chan error, 1)
		go func() { fim <- servir(ctx, cfg) }()
		return fim, desligar, ok
	}

	fim, desligar, ok := subir()
	select {
	case <-ok:
	case err := <-fim:
		t.Fatalf("o binário novo não subiu sobre o banco do instalado %s: %v", strings.TrimSpace(string(versao)), err)
	case <-time.After(15 * time.Second):
		t.Fatal("servir não ficou pronto")
	}
	limite := time.Now().Add(5 * time.Second)
	for flx.Logins() == 0 && time.Now().Before(limite) {
		time.Sleep(10 * time.Millisecond)
	}
	if flx.Logins() != 1 {
		t.Fatalf("logins na Flexscan depois de subir: %d", flx.Logins())
	}
	var op map[string]any
	obter(t, "http://"+endereco+"/ordens/8801", &op)
	if op["estado"] != "ZERADA" || op["conector"] != "PNT" || op["conta"] != "12345" {
		t.Fatalf("operação do instalado: %v", op)
	}
	var h map[string]any
	obter(t, "http://"+endereco+"/usuarios/7/habilitacao", &h)
	if cs, _ := h["conectores"].([]any); len(cs) != 1 || cs[0] != "PNT" {
		t.Fatalf("habilitação do instalado: %v", h)
	}
	desligar()
	if err := <-fim; err != nil {
		t.Fatal(err)
	}

	// uma partida que falha antes do fim (aqui: o schema quebrado) não faz login na Flexscan
	if err := r.Executar("ALTER TABLE kalmanordemoperacao CHANGE estado estado_x VARCHAR(32) NOT NULL"); err != nil {
		t.Fatal(err)
	}
	defer r.Executar("ALTER TABLE kalmanordemoperacao CHANGE estado_x estado VARCHAR(32) NOT NULL")
	for i := 0; i < 3; i++ {
		fim, desligar, _ := subir()
		select {
		case err := <-fim:
			if err == nil {
				t.Fatal("subiu com o schema quebrado")
			}
		case <-time.After(15 * time.Second):
			t.Fatal("servir não terminou com o schema quebrado")
		}
		desligar()
	}
	time.Sleep(200 * time.Millisecond)
	if flx.Logins() != 1 {
		t.Fatalf("partidas que falharam fizeram login na Flexscan: %d logins", flx.Logins())
	}
}
