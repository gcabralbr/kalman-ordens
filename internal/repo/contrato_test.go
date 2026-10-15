package repo

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// DSN do MariaDB de teste (container mariadb:10.11.15). Sem ela, só a memória é testada.
// Ex.: KALMAN_TESTE_MARIADB=kalman:kalman@tcp(127.0.0.1:33306)/kalmanordens_teste
func dsnTeste() string { return os.Getenv("KALMAN_TESTE_MARIADB") }

// NovoMariaDBTeste abre o banco de teste limpo e migrado.
func NovoMariaDBTeste(t testing.TB) *MariaDB {
	t.Helper()
	r, err := AbrirMariaDB(dsnTeste())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.LimparTeste(); err != nil {
		t.Fatal(err)
	}
	if err := r.Migrar(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Fechar() })
	return r
}

func backends(t *testing.T) map[string]func(t *testing.T) Repositorio {
	b := map[string]func(t *testing.T) Repositorio{"memoria": func(*testing.T) Repositorio { return NovaMemoria() }}
	if dsnTeste() != "" {
		b["mariadb"] = func(t *testing.T) Repositorio { return NovoMariaDBTeste(t) }
	} else {
		t.Log("KALMAN_TESTE_MARIADB vazio: MariaDB não testado")
	}
	return b
}

func TestContrato(t *testing.T) {
	for nome, novo := range backends(t) {
		t.Run(nome, func(t *testing.T) {
			r := novo(t)
			em := time.Date(2026, 10, 13, 13, 15, 2, 123e6, time.UTC)

			op := &dominio.Operacao{Data: "2026-10-13", OperacaoID: 77, UID: 7, Lado: dominio.Compra, QtdPedida: 1, StopPts: 500,
				Estado: dominio.Entrando, MotivoEntrada: "SINAL", CriadaEm: em, ContadorN: map[string]int{"E": 1},
				PedidoEntrada: `{"operacao_id":77}`, RespostaEntrada: `{"aceito":true}`}
			if err := r.SalvarOperacao(op); err != nil || op.ID == 0 {
				t.Fatalf("inserir operação: id=%d err=%v", op.ID, err)
			}
			dup := &dominio.Operacao{Data: "2026-10-13", OperacaoID: 77, UID: 7, Lado: dominio.Compra, Estado: dominio.Entrando, CriadaEm: em}
			if err := r.SalvarOperacao(dup); err != ErrDuplicada {
				t.Fatalf("operação duplicada: %v", err)
			}
			// v1.3 M1: o mesmo operacao_id em outro módulo é outra operação
			pt := &dominio.Operacao{Data: "2026-10-13", Modulo: "PT", OperacaoID: 77, UID: 7, Lado: dominio.Venda, Estado: dominio.Entrando, CriadaEm: em}
			if err := r.SalvarOperacao(pt); err != nil || pt.ID == 0 || pt.ID == op.ID {
				t.Fatalf("mesmo operacao_id em outro módulo: id=%d err=%v", pt.ID, err)
			}
			if op.Modulo != dominio.ModuloPadrao {
				t.Fatalf("módulo padrão: %q", op.Modulo)
			}
			op.Estado, op.EncerrouEntrada, op.PnL = dominio.Zerada, true, decimal.RequireFromString("-505.5")
			op.ContadorN["P"] = 3
			op.SaidaPedidaEm = em.Add(time.Minute)
			op.StopAjustado = 129700 // v1.4 K1
			if err := r.SalvarOperacao(op); err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct {
				mod  string
				quer bool
			}{{"", true}, {"KALMAN", true}, {"PT", true}, {"XX", false}} {
				if ok, _ := r.OperacaoExiste(c.mod, 77); ok != c.quer {
					t.Fatalf("OperacaoExiste(%q, 77) = %v", c.mod, ok)
				}
			}

			o := &dominio.Ordem{Data: "2026-10-13", UID: 7, OperacaoID: 77, Papel: dominio.Protecao, ClOrdBase: "KM-P-77-1",
				ClOrdAtual: "KM-P-77-1", Estrategia: 1014, Lado: dominio.Venda, Qtd: 1, Preco: 129410, Stop: 129510, StopLim: 129410,
				Estado: dominio.Enviando, CriadaEm: em}
			if err := r.SalvarOrdem(o); err != nil || o.ID == 0 {
				t.Fatalf("inserir ordem: %v", err)
			}
			o.ClOrdAtual, o.OrderID, o.Estado, o.QtdExec, o.PrecoMedio = "KM-P-77-2", "dsa.9", dominio.Executada, 1, decimal.RequireFromString("129505.25")
			if err := r.SalvarOrdem(o); err != nil {
				t.Fatal(err)
			}

			ops, ords, err := r.CarregarDia("2026-10-13")
			if err != nil || len(ops) != 2 || len(ords) != 1 {
				t.Fatalf("carregar: %d ops %d ordens err=%v", len(ops), len(ords), err)
			}
			if ops[1].Modulo != "PT" || ops[1].OperacaoID != 77 || ops[1].Lado != dominio.Venda {
				t.Fatalf("operação do PT lida diferente: %+v", ops[1])
			}
			g := ops[0]
			if g.Estado != dominio.Zerada || !g.EncerrouEntrada || !g.PnL.Equal(op.PnL) || g.ContadorN["P"] != 3 ||
				!g.CriadaEm.Equal(em) || !g.SaidaPedidaEm.Equal(em.Add(time.Minute)) || g.RespostaEntrada != `{"aceito":true}` || g.Lado != dominio.Compra ||
				g.StopAjustado != 129700 || ops[1].StopAjustado != 0 {
				t.Fatalf("operação lida diferente: %+v", g)
			}
			if g.Modulo != dominio.ModuloPadrao || ords[0].Modulo != dominio.ModuloPadrao {
				t.Fatalf("módulo lido: operação %q ordem %q", g.Modulo, ords[0].Modulo)
			}
			go_ := ords[0]
			if go_.ClOrdAtual != "KM-P-77-2" || go_.OrderID != "dsa.9" || go_.Estado != dominio.Executada || !go_.PrecoMedio.Equal(o.PrecoMedio) ||
				go_.Papel != dominio.Protecao || go_.Lado != dominio.Venda || go_.Stop != 129510 {
				t.Fatalf("ordem lida diferente: %+v", go_)
			}
			if ops, _, _ := r.CarregarDia("2026-10-14"); len(ops) != 0 {
				t.Fatal("outro dia não pode vir")
			}

			q := 1
			ev := EventoFIX{OrdemID: o.ID, Direcao: "REC", MsgType: "8", OrderID: "dsa.9", ExecID: "T-1", CumQty: &q, Em: em}
			if novo, err := r.RegistrarEvento(ev); !novo || err != nil {
				t.Fatalf("evento: %v %v", novo, err)
			}
			if novo, err := r.RegistrarEvento(ev); novo || err != nil {
				t.Fatalf("execid repetido na mesma ordem aceito: %v %v", novo, err)
			}
			// v1.3 H1: o mesmo 17 em outra ordem (outro 37) é outro fill
			outra := ev
			outra.OrderID = "dsa.10"
			if novo, err := r.RegistrarEvento(outra); !novo || err != nil {
				t.Fatalf("mesmo execid em outra ordem recusado: %v %v", novo, err)
			}
			sem37 := ev
			sem37.OrderID = ""
			for i := 0; i < 2; i++ { // sem 37 não conflita (NULL na chave)
				if novo, err := r.RegistrarEvento(sem37); !novo || err != nil {
					t.Fatalf("execid sem 37: %v %v", novo, err)
				}
			}
			for i := 0; i < 2; i++ { // sem execid nunca conflita
				if novo, err := r.RegistrarEvento(EventoFIX{Direcao: "ENV", MsgType: "D", Em: em.Add(time.Duration(i) * time.Second)}); !novo || err != nil {
					t.Fatalf("evento sem execid: %v %v", novo, err)
				}
			}
			if n, _ := r.EnviosDesde(em); n != 2 {
				t.Fatalf("EnviosDesde = %d", n)
			}
			if n, _ := r.EnviosDesde(em.Add(500 * time.Millisecond)); n != 1 {
				t.Fatalf("EnviosDesde corte = %d", n)
			}

			for i := 0; i < 3; i++ {
				b, _ := json.Marshal(map[string]any{"i": i})
				if s, err := r.Publicar("ALERTA", "", 77, b, em); err != nil || s != int64(i+1) {
					t.Fatalf("publicar: seq=%d err=%v", s, err)
				}
			}
			if s, err := r.Publicar("ZERADA", "PT", 77, []byte(`{"i":3}`), em); err != nil || s != 4 {
				t.Fatalf("publicar PT: seq=%d err=%v", s, err)
			}
			ps, _ := r.Publicados(1, 10, "")
			if len(ps) != 3 || ps[0].Seq != 2 || ps[0].OperacaoID != 77 || ps[0].Modulo != dominio.ModuloPadrao || string(ps[1].Dados) != `{"i":2}` {
				t.Fatalf("publicados: %+v", ps)
			}
			// filtro por módulo (no banco: a paginação por seq continua certa)
			if ps, _ := r.Publicados(0, 10, "PT"); len(ps) != 1 || ps[0].Seq != 4 || ps[0].Modulo != "PT" {
				t.Fatalf("publicados do PT: %+v", ps)
			}
			if ps, _ := r.Publicados(0, 2, "KALMAN"); len(ps) != 2 || ps[1].Seq != 2 {
				t.Fatalf("publicados do KALMAN: %+v", ps)
			}

			if _, ok, _ := r.LerEstado("parada"); ok {
				t.Fatal("estado inexistente")
			}
			_ = r.GravarEstado("parada", "a", em)
			_ = r.GravarEstado("parada", "b", em)
			if v, ok, _ := r.LerEstado("parada"); !ok || v != "b" {
				t.Fatalf("estado: %q %v", v, ok)
			}
		})
	}
}

// LimparTeste recusa schema que não termina em _teste.
func TestLimparTesteRecusaProducao(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("sem MariaDB de teste")
	}
	r, err := AbrirMariaDB(dsnTeste())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Fechar()
	r.db.SetMaxOpenConns(1) // USE vale por conexão
	if _, err := r.db.Exec("CREATE DATABASE IF NOT EXISTS kalmanordens_prod_simulada"); err != nil {
		t.Skipf("sem permissão para criar schema de simulação: %v", err)
	}
	defer r.db.Exec("DROP DATABASE IF EXISTS kalmanordens_prod_simulada")
	if _, err := r.db.Exec("USE kalmanordens_prod_simulada"); err != nil {
		t.Fatal(err)
	}
	if err := r.LimparTeste(); err == nil {
		t.Fatal("LimparTeste aceitou um schema que não é de teste")
	}
}

// O sql_mode da sessão é o do serviço mesmo com o global do servidor diferente.
func TestSQLModeFixoNaConexao(t *testing.T) {
	if dsnTeste() == "" {
		t.Skip("sem MariaDB de teste")
	}
	adm, err := AbrirMariaDB(dsnTeste())
	if err != nil {
		t.Fatal(err)
	}
	defer adm.Fechar()
	var global string
	if err := adm.db.QueryRow("SELECT @@GLOBAL.sql_mode").Scan(&global); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.db.Exec("SET GLOBAL sql_mode=''"); err != nil {
		t.Skipf("sem permissão para mudar o sql_mode global: %v", err)
	}
	defer adm.db.Exec("SET GLOBAL sql_mode=?", global)

	r, err := AbrirMariaDB(dsnTeste()) // conexão nova: herdaria o global vazio
	if err != nil {
		t.Fatal(err)
	}
	defer r.Fechar()
	var sessao string
	if err := r.db.QueryRow("SELECT @@SESSION.sql_mode").Scan(&sessao); err != nil {
		t.Fatal(err)
	}
	if sessao != SQLMode {
		t.Fatalf("sql_mode da sessão = %q, esperado %q", sessao, SQLMode)
	}
}

// v1.2: conta, destino e símbolo congelados na operação e na ordem sobrevivem à recarga;
// habilitação por uid (atual + histórico); configuração do usuário do GQ.
func TestContratoV12(t *testing.T) {
	for nome, novo := range backends(t) {
		t.Run(nome, func(t *testing.T) {
			r := novo(t)
			em := time.Date(2026, 10, 13, 13, 0, 0, 0, time.UTC)
			op := &dominio.Operacao{Data: "2026-10-13", OperacaoID: 1450, UID: 1450, Conta: "99999", Destino: 85, Simbolo: "WINV26",
				Lado: dominio.Compra, QtdPedida: 1, StopPts: 500, Estado: dominio.Entrando, CriadaEm: em, ContadorN: map[string]int{}}
			if err := r.SalvarOperacao(op); err != nil {
				t.Fatal(err)
			}
			o := &dominio.Ordem{Data: "2026-10-13", UID: 1450, OperacaoID: 1450, Papel: dominio.Entrada, Conta: "99999", Simbolo: "WINV26",
				ClOrdBase: "KM-E-1450-1", ClOrdAtual: "KM-E-1450-1", Estrategia: 1031, Lado: dominio.Compra, Qtd: 1, Estado: dominio.Enviando, CriadaEm: em}
			if err := r.SalvarOrdem(o); err != nil {
				t.Fatal(err)
			}
			ops, ords, err := r.CarregarDia("2026-10-13")
			if err != nil || len(ops) != 1 || len(ords) != 1 {
				t.Fatalf("%v %d %d", err, len(ops), len(ords))
			}
			if ops[0].Conta != "99999" || ops[0].Destino != 85 || ops[0].Simbolo != "WINV26" || ords[0].Conta != "99999" || ords[0].Simbolo != "WINV26" {
				t.Fatalf("congelados: %+v / %+v", ops[0], ords[0])
			}

			if _, ok, err := r.Habilitacao(1450); ok || err != nil {
				t.Fatalf("uid sem habilitação: %v %v", ok, err)
			}
			for _, h := range []dominio.Habilitacao{
				{UID: 1450, Habilitado: true, PosicaoMax: 1, QtdMaxOrdem: 1, Por: "george", Motivo: "F1", Em: em},
				{UID: 1450, Habilitado: false, Por: "george", Motivo: "fim da F1", Em: em.Add(time.Hour)},
				{UID: 7, Habilitado: true, Por: "root", Motivo: "teste", Em: em},
			} {
				if err := r.GravarHabilitacao(h); err != nil {
					t.Fatal(err)
				}
			}
			h, ok, err := r.Habilitacao(1450)
			if err != nil || !ok || h.Habilitado || h.Motivo != "fim da F1" || !h.Em.Equal(em.Add(time.Hour)) {
				t.Fatalf("habilitação atual: %+v %v %v", h, ok, err)
			}
			hs, err := r.Habilitacoes()
			if err != nil || len(hs) != 2 || hs[0].UID != 7 || hs[1].UID != 1450 {
				t.Fatalf("habilitações: %+v %v", hs, err)
			}

			if m, ok := r.(*MariaDB); ok {
				if err := m.PrepararUsuariosTeste(); err != nil {
					t.Fatal(err)
				}
			}
			u := r.(UsuariosDeTeste)
			const cfg = `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`
			if err := u.DefinirUsuario(1450, "hml", cfg); err != nil {
				t.Fatal(err)
			}
			if err := u.DefinirUsuario(1450, "prd", `{"Robo":{"CORSeqId":85,"CCONroConta":"7654321"}}`); err != nil {
				t.Fatal(err)
			}
			hml, prd, achou, err := r.ConfigUsuario(1450)
			if err != nil || !achou || hml != cfg || prd == "" {
				t.Fatalf("config: %q %q %v %v", hml, prd, achou, err)
			}
			if _, _, achou, err := r.ConfigUsuario(1451); achou || err != nil {
				t.Fatalf("uid sem linha: %v %v", achou, err)
			}
			if err := u.DefinirUsuario(1450, "ausente", ""); err != nil {
				t.Fatal(err)
			}
			if _, _, achou, _ := r.ConfigUsuario(1450); achou {
				t.Fatal("ausente")
			}
		})
	}
}
