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

// v1.5 R: consultas do histórico (tela de admin e CLI), de qualquer dia.
func TestContratoHistorico(t *testing.T) {
	for nome, novo := range backends(t) {
		t.Run(nome, func(t *testing.T) {
			r := novo(t)
			em := time.Date(2026, 10, 15, 13, 0, 0, 250e6, time.UTC)
			for _, mod := range []string{"", "PT"} {
				op := &dominio.Operacao{Data: "2026-10-15", Modulo: mod, OperacaoID: 61, UID: 7, Lado: dominio.Compra, QtdPedida: 1, StopPts: 500,
					Estado: dominio.Entrando, CriadaEm: em, ContadorN: map[string]int{}}
				if err := r.SalvarOperacao(op); err != nil {
					t.Fatal(err)
				}
			}
			if d, ok, err := r.DiaDaOperacao("", 61); err != nil || !ok || d != "2026-10-15" {
				t.Fatalf("DiaDaOperacao KALMAN: %q %v %v", d, ok, err)
			}
			if d, ok, _ := r.DiaDaOperacao("PT", 61); !ok || d != "2026-10-15" {
				t.Fatalf("DiaDaOperacao PT: %q %v", d, ok)
			}
			if _, ok, _ := r.DiaDaOperacao("XX", 61); ok {
				t.Fatal("DiaDaOperacao achou operação de módulo inexistente")
			}

			o1 := &dominio.Ordem{Data: "2026-10-15", OperacaoID: 61, Papel: dominio.Entrada, ClOrdBase: "KM-E-61-1", ClOrdAtual: "KM-E-61-1",
				Lado: dominio.Compra, Qtd: 1, Estado: dominio.Enviando, CriadaEm: em}
			o2 := &dominio.Ordem{Data: "2026-10-15", OperacaoID: 62, Papel: dominio.Entrada, ClOrdBase: "KM-E-62-1", ClOrdAtual: "KM-E-62-1",
				Lado: dominio.Compra, Qtd: 1, Estado: dominio.Enviando, CriadaEm: em}
			for _, o := range []*dominio.Ordem{o1, o2} {
				if err := r.SalvarOrdem(o); err != nil {
					t.Fatal(err)
				}
			}
			q, px := 1, decimal.RequireFromString("130010")
			for i, e := range []EventoFIX{
				{OrdemID: o1.ID, Direcao: "ENV", MsgType: "D", ClOrdID: "KM-E-61-1", Bruta: "8=FIX.4.4\x0135=D\x01", Em: em},
				{OrdemID: o1.ID, Direcao: "REC", MsgType: "8", ExecType: "F", OrdStatus: "2", ClOrdID: "KM-E-61-1", OrderID: "dsa.61", ExecID: "x1",
					LastQty: &q, LastPx: &px, CumQty: &q, AvgPx: &px, Texto: "ok", Codigo: "0", Bruta: "8=FIX.4.4\x0135=8\x01", Em: em.Add(time.Second)},
				{OrdemID: o2.ID, Direcao: "ENV", MsgType: "D", Em: em},
				{Direcao: "ENV", MsgType: "AF", Em: em},
			} {
				if _, err := r.RegistrarEvento(e); err != nil {
					t.Fatalf("evento %d: %v", i, err)
				}
			}
			evs, err := r.EventosFIXDasOrdens([]int64{o1.ID})
			if err != nil || len(evs) != 2 {
				t.Fatalf("EventosFIXDasOrdens: %d %v", len(evs), err)
			}
			f := evs[1]
			if f.ID == 0 || f.ExecType != "F" || f.OrderID != "dsa.61" || *f.LastQty != 1 || !f.LastPx.Equal(px) || *f.CumQty != 1 ||
				!f.AvgPx.Equal(px) || f.Texto != "ok" || f.Codigo != "0" || f.Bruta != "8=FIX.4.4\x0135=8\x01" || !f.Em.Equal(em.Add(time.Second)) {
				t.Fatalf("evento lido diferente: %+v", f)
			}
			if evs, _ := r.EventosFIXDasOrdens(nil); len(evs) != 0 {
				t.Fatal("EventosFIXDasOrdens(nil) devolveu eventos")
			}

			for _, p := range []struct {
				tipo, mod string
				op        int64
				em        time.Time
			}{{"ENTRADA_EXECUTADA", "", 61, em}, {"ALERTA", "", 61, em}, {"ZERADA", "PT", 61, em}, {"ALERTA", "", 0, em.Add(-24 * time.Hour)}} {
				if _, err := r.Publicar(p.tipo, p.mod, p.op, []byte(`{"a":1}`), p.em); err != nil {
					t.Fatal(err)
				}
			}
			if ps, _ := r.PublicadosDaOperacao("", 61); len(ps) != 2 || ps[0].Tipo != "ENTRADA_EXECUTADA" || ps[1].Tipo != "ALERTA" {
				t.Fatalf("PublicadosDaOperacao KALMAN: %+v", ps)
			}
			if ps, _ := r.PublicadosDaOperacao("PT", 61); len(ps) != 1 || ps[0].Tipo != "ZERADA" || !ps[0].Em.Equal(em) {
				t.Fatalf("PublicadosDaOperacao PT: %+v", ps)
			}
			if ps, _ := r.PublicadosEntre(em.Add(-time.Hour), em.Add(time.Hour), ""); len(ps) != 3 {
				t.Fatalf("PublicadosEntre: %d", len(ps))
			}
			if ps, _ := r.PublicadosEntre(em.Add(-time.Hour), em.Add(time.Hour), "ALERTA"); len(ps) != 1 {
				t.Fatalf("PublicadosEntre ALERTA: %d", len(ps))
			}

			for _, e := range []EventoSessao{{"LOGON", "logon aceito", em}, {"QUEDA", "caiu", em.Add(time.Second)}, {"LOGON", "ontem", em.Add(-24 * time.Hour)}} {
				if err := r.RegistrarSessao(e); err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range []PedidoHTTP{
				{Data: "2026-10-15", OperacaoID: 61, UID: 7, Rota: "ORDENS", Papel: "ENTRADA", HTTPStatus: 200, Aceito: true, Corpo: `{"a":"ação"}`, Resposta: `{"aceito":true}`, Em: em},
				{Data: "2026-10-15", Modulo: "PT", OperacaoID: 61, UID: 8, Rota: "STOP", HTTPStatus: 200, Codigo: "SEM_PRECO", Corpo: "{}", Resposta: "{}", Em: em.Add(time.Second)},
				{Data: "2026-10-15", Rota: "ORDENS", HTTPStatus: 400, Codigo: "CAMPO_INVALIDO", Corpo: "{x", Resposta: "{}", Em: em.Add(2 * time.Second)},
				{Data: "2026-10-14", OperacaoID: 61, UID: 7, Rota: "ORDENS", HTTPStatus: 200, Corpo: "{}", Resposta: "{}", Em: em.Add(-24 * time.Hour)},
			} {
				p := p
				if err := r.RegistrarPedido(&p); err != nil || p.ID == 0 {
					t.Fatalf("RegistrarPedido: %v", err)
				}
			}
			if ps, _ := r.PedidosDoDia("2026-10-15", 0, ""); len(ps) != 3 || ps[0].Corpo != `{"a":"ação"}` || !ps[0].Aceito || ps[0].Papel != "ENTRADA" ||
				ps[1].Modulo != "PT" || ps[1].Codigo != "SEM_PRECO" || ps[2].OperacaoID != 0 || ps[2].UID != 0 || ps[2].HTTPStatus != 400 || !ps[2].Em.Equal(em.Add(2*time.Second)) {
				t.Fatalf("PedidosDoDia: %+v", ps)
			}
			if ps, _ := r.PedidosDoDia("2026-10-15", 7, ""); len(ps) != 1 {
				t.Fatalf("PedidosDoDia uid 7: %d", len(ps))
			}
			if ps, _ := r.PedidosDoDia("2026-10-15", 0, "PT"); len(ps) != 1 {
				t.Fatalf("PedidosDoDia PT: %d", len(ps))
			}
			if ps, _ := r.PedidosDaOperacao("", 61); len(ps) != 2 {
				t.Fatalf("PedidosDaOperacao KALMAN: %d", len(ps))
			}
			ss, err := r.SessaoEntre(em.Add(-time.Hour), em.Add(time.Hour))
			if err != nil || len(ss) != 2 || ss[0].Tipo != "LOGON" || ss[1].Tipo != "QUEDA" || ss[1].Texto != "caiu" || !ss[1].Em.Equal(em.Add(time.Second)) {
				t.Fatalf("SessaoEntre: %+v %v", ss, err)
			}
		})
	}
}

// v1.8 H2: AlterarHabilitado troca só habilitado, por, motivo e em, preserva os limites e grava o
// histórico; sem linha ou já no valor pedido, nada.
func TestContratoAlterarHabilitado(t *testing.T) {
	for nome, novo := range backends(t) {
		t.Run(nome, func(t *testing.T) {
			r := novo(t)
			em := time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC)
			hist := func() int {
				t.Helper()
				switch x := r.(type) {
				case *Memoria:
					return len(x.hist)
				case *MariaDB:
					var n int
					if err := x.db.QueryRow("SELECT COUNT(*) FROM kalmanordemusuariohist WHERE uid=7").Scan(&n); err != nil {
						t.Fatal(err)
					}
					return n
				}
				return -1
			}
			if _, ok, err := r.AlterarHabilitado(7, false, "admin:1", "x", em); ok || err != nil {
				t.Fatalf("sem linha: ok=%v err=%v", ok, err)
			}
			if err := r.GravarHabilitacao(dominio.Habilitacao{UID: 7, Habilitado: true, PosicaoMax: 2, QtdMaxOrdem: 3, Por: "george", Motivo: "F1", Em: em}); err != nil {
				t.Fatal(err)
			}
			antes, ok, err := r.AlterarHabilitado(7, false, "admin:1450", "inadimplencia", em.Add(time.Minute))
			if err != nil || !ok || !antes.Habilitado || antes.PosicaoMax != 2 || antes.Por != "george" {
				t.Fatalf("antes: %+v ok=%v err=%v", antes, ok, err)
			}
			h, _, _ := r.Habilitacao(7)
			if h.Habilitado || h.PosicaoMax != 2 || h.QtdMaxOrdem != 3 || h.Por != "admin:1450" || h.Motivo != "inadimplencia" || !h.Em.Equal(em.Add(time.Minute)) {
				t.Fatalf("depois: %+v", h)
			}
			if n := hist(); n != 2 {
				t.Fatalf("histórico: %d linhas, esperava 2", n)
			}
			// já bloqueado: nada muda, nada no histórico
			if antes, ok, _ := r.AlterarHabilitado(7, false, "admin:9", "de novo", em.Add(2*time.Minute)); !ok || antes.Habilitado {
				t.Fatalf("já bloqueado: %+v", antes)
			}
			if h, _, _ := r.Habilitacao(7); h.Por != "admin:1450" || hist() != 2 {
				t.Fatalf("já bloqueado não devia gravar: %+v, histórico %d", h, hist())
			}
		})
	}
}

// v1.10 B: o lote numa transação — operação e ordem novas (INSERT, ID do motor) e já gravadas
// (upsert pelo ID), eventos (o fill repetido ignorado pela chave única), publicados, estado,
// sessão e pedidos; IDs máximos; chaves de fill do dia; configuração de vários uids.
func TestContratoLote(t *testing.T) {
	for nome, novo := range backends(t) {
		t.Run(nome, func(t *testing.T) {
			r := novo(t)
			em := time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC)
			if a, b, err := r.MaioresIDs(); err != nil || a != 0 || b != 0 {
				t.Fatalf("MaioresIDs vazio: %d %d %v", a, b, err)
			}
			op := dominio.Operacao{ID: 41, Data: "2026-10-15", OperacaoID: 7001, UID: 7, Conta: "12345", Destino: 85, Simbolo: "WINZ26",
				Lado: dominio.Compra, QtdPedida: 1, StopPts: 500, Estado: dominio.Entrando, CriadaEm: em, ContadorN: map[string]int{"E": 1}}
			ord := dominio.Ordem{ID: 90, Data: "2026-10-15", UID: 7, OperacaoID: 7001, Papel: dominio.Entrada, Conta: "12345", Simbolo: "WINZ26",
				ClOrdBase: "KM-E-7001-1", ClOrdAtual: "KM-E-7001-1", Estrategia: 1031, Lado: dominio.Compra, Qtd: 1, Preco: 130020,
				Estado: dominio.Enviando, CriadaEm: em}
			q, px := 1, decimal.RequireFromString("130010")
			fill := EventoFIX{OrdemID: 90, Direcao: "REC", MsgType: "8", ExecType: "F", OrderID: "dsa.1", ExecID: "x1", LastQty: &q, LastPx: &px, Em: em}
			l := &Lote{
				NovasOperacoes: []dominio.Operacao{op}, NovasOrdens: []dominio.Ordem{ord},
				Eventos:    []EventoFIX{{OrdemID: 90, Direcao: "ENV", MsgType: "D", ClOrdID: "KM-E-7001-1", Bruta: "8=FIX", Em: em}, fill},
				Publicados: []PublicadoNovo{{Tipo: "ENTRADA_EXECUTADA", OperacaoID: 7001, Dados: []byte(`{"a":1}`), Em: em}},
				Estados:    []EstadoNovo{{Chave: "parada", Valor: "x", Em: em}},
				Sessao:     []EventoSessao{{Tipo: "LOGON", Texto: "ok", Em: em}},
				Pedidos:    []PedidoHTTP{{Data: "2026-10-15", OperacaoID: 7001, UID: 7, Rota: "ORDENS", HTTPStatus: 200, Aceito: true, Corpo: "{}", Resposta: "{}", Em: em}},
			}
			if err := r.GravarLote(l); err != nil {
				t.Fatalf("lote 1: %v", err)
			}
			// segundo lote: as mesmas operação e ordem já gravadas, mudadas; o fill repetido; mais um publicado
			op.Estado, op.StopAjustado, op.ContadorN["P"] = dominio.Protegida, 129700, 1
			// v1.10 §6: a posição na rajada do sinal (entrada) volta igual; a saída avulsa, vazia
			op.SinalEntrada = dominio.Rajada{SinalID: "KALMAN-S1", K: 7, Posicao: 2, Tamanho: 3, Deslocamento: 1, Criterio: "ROTATIVA_AUREA"}
			ord.Estado, ord.QtdExec, ord.OrderID, ord.PrecoMedio = dominio.Executada, 1, "dsa.1", px
			if err := r.GravarLote(&Lote{Operacoes: []dominio.Operacao{op}, Ordens: []dominio.Ordem{ord}, Eventos: []EventoFIX{fill},
				Publicados: []PublicadoNovo{{Tipo: "PROTEGIDA", OperacaoID: 7001, Dados: []byte(`{}`), Em: em}}}); err != nil {
				t.Fatalf("lote 2: %v", err)
			}
			ops, ords, err := r.CarregarDia("2026-10-15")
			if err != nil || len(ops) != 1 || len(ords) != 1 {
				t.Fatalf("carregar: %d %d %v", len(ops), len(ords), err)
			}
			if g := ops[0]; g.ID != 41 || g.Estado != dominio.Protegida || g.StopAjustado != 129700 || g.ContadorN["P"] != 1 || g.Conta != "12345" {
				t.Fatalf("operação: %+v", g)
			}
			if g := ops[0]; g.SinalEntrada != op.SinalEntrada || !g.SinalSaida.Vazia() {
				t.Fatalf("rajada: %+v / %+v", g.SinalEntrada, g.SinalSaida)
			}
			if ex, err := r.OperacoesExistentes("", []int64{7001, 7002}); err != nil || len(ex) != 1 || !ex[7001] {
				t.Fatalf("existentes: %v %v", ex, err)
			}
			if ex, _ := r.OperacoesExistentes("PT", []int64{7001}); len(ex) != 0 {
				t.Fatalf("existentes em outro módulo: %v", ex)
			}
			if g := ords[0]; g.ID != 90 || g.Estado != dominio.Executada || g.QtdExec != 1 || g.OrderID != "dsa.1" || !g.PrecoMedio.Equal(px) || g.Preco != 130020 {
				t.Fatalf("ordem: %+v", g)
			}
			if evs, _ := r.EventosFIXDasOrdens([]int64{90}); len(evs) != 2 {
				t.Fatalf("eventos: %d (o fill repetido não pode duplicar)", len(evs))
			}
			if ps, _ := r.PublicadosDaOperacao("", 7001); len(ps) != 2 || ps[0].Tipo != "ENTRADA_EXECUTADA" || ps[1].Tipo != "PROTEGIDA" {
				t.Fatalf("publicados: %+v", ps)
			}
			if v, ok, _ := r.LerEstado("parada"); !ok || v != "x" {
				t.Fatalf("estado: %q %v", v, ok)
			}
			if ss, _ := r.SessaoEntre(em.Add(-time.Hour), em.Add(time.Hour)); len(ss) != 1 {
				t.Fatalf("sessão: %d", len(ss))
			}
			if ps, _ := r.PedidosDaOperacao("", 7001); len(ps) != 1 {
				t.Fatalf("pedidos: %d", len(ps))
			}
			if a, b, _ := r.MaioresIDs(); a != 41 || b != 90 {
				t.Fatalf("MaioresIDs: %d %d", a, b)
			}
			k, err := r.ChavesFillDoDia(em.Add(-time.Hour), em.Add(time.Hour))
			if err != nil || !k[ChaveFill("dsa.1", "x1")] || len(k) != 1 {
				t.Fatalf("chaves de fill: %v %v", k, err)
			}
			// a operação nova com a chave única de outra: erro (nunca atualiza a outra linha)
			outra := op
			outra.ID, outra.Estado = 42, dominio.Entrando
			if err := r.GravarLote(&Lote{NovasOperacoes: []dominio.Operacao{outra}}); err == nil && nome == "mariadb" {
				t.Fatal("operação nova com (modulo, operacao_id) repetido devia falhar")
			}
			// configuração de vários uids
			if mdb, ok := r.(*MariaDB); ok {
				if err := mdb.PrepararUsuariosTeste(); err != nil {
					t.Fatal(err)
				}
			}
			if u, ok := r.(UsuariosDeTeste); ok {
				_ = u.DefinirUsuario(7, "hml", "A")
				_ = u.DefinirUsuario(8, "prd", "B")
			}
			cfg, err := r.ConfigUsuarios([]int{7, 8, 9})
			if err != nil || cfg[7][0] != "A" || cfg[8][1] != "B" || len(cfg) != 2 {
				t.Fatalf("ConfigUsuarios: %v %v", cfg, err)
			}
		})
	}
}

func TestCortarPorCaractere(t *testing.T) {
	casos := []struct {
		s    string
		n    int
		quer string
	}{
		{"abc", 5, "abc"}, {"abcdef", 3, "abc"}, {"ação", 2, "aç"}, {"ação", 4, "ação"}, {"çççç", 3, "ççç"}, {"a\xffb", 5, "ab"},
	}
	for _, c := range casos {
		if got := cortar(c.s, c.n); got != c.quer {
			t.Errorf("cortar(%q, %d) = %q, quer %q", c.s, c.n, got, c.quer)
		}
	}
}
