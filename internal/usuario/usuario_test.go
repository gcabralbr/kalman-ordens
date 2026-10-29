package usuario

import (
	"fmt"
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// Exemplo real (HML, usuário 1450), com o resto do JSON que o serviço ignora.
const real1450 = `{"AmbienteDef":"BTG_Simulador","Robo":{"CORSeqId":85,"CCONroConta":"99999","Outro":"x"},"Tela":{"a":1}}`

func TestResolver(t *testing.T) {
	hml := []string{"99999"}
	casos := []struct {
		nome, ambiente, texto string
		conta, codigo         string
	}{
		{"real 1450 em HML", HML, real1450, "99999", ""},
		{"conta de HML em PRODUCAO", Producao, real1450, "", "CONTA_AMBIENTE"},
		{"conta real em PRODUCAO", Producao, `{"Robo":{"CORSeqId":85,"CCONroConta":"1234567"}}`, "1234567", ""},
		{"conta real em HML", HML, `{"Robo":{"CORSeqId":85,"CCONroConta":"1234567"}}`, "", "CONTA_AMBIENTE"},
		{"Capital pelo PnT (a corretora não decide o caminho)", HML, `{"Robo":{"CORSeqId":88,"CCONroConta":"99999"}}`, "99999", ""},
		// SPEC-AMBIENTES-v1 §0.1: no SIMULADOR, 9999 com a conta QUANT é o paper; no REAL, inválido
		{"simulador do GQ (paper)", HML, `{"Robo":{"CORSeqId":9999,"CCONroConta":"QUANT"}}`, "", "DESTINO_PAPER"},
		{"simulador do GQ no REAL", Producao, `{"Robo":{"CORSeqId":9999,"CCONroConta":"QUANT"}}`, "", "DESTINO_INVALIDO"},
		{"destino desconhecido", HML, `{"Robo":{"CORSeqId":7,"CCONroConta":"99999"}}`, "", "DESTINO_INVALIDO"},
		{"conta não numérica", HML, `{"Robo":{"CORSeqId":85,"CCONroConta":"QUANT"}}`, "", "CONTA_INVALIDA"},
		{"conta ausente", HML, `{"Robo":{"CORSeqId":85}}`, "", "CONTA_INVALIDA"},
		{"conta como número", HML, `{"Robo":{"CORSeqId":85,"CCONroConta":99999}}`, "", "CONTA_INVALIDA"},
		{"JSON inválido", HML, `{"Robo":{"CORSeqId":85,`, "", "CONFIG_USUARIO_INVALIDA"},
		{"vazio", HML, ``, "", "CONFIG_USUARIO_INVALIDA"},
		{"sem Robo", HML, `{"AmbienteDef":"X"}`, "", "CONFIG_USUARIO_INVALIDA"},
		{"CORSeqId texto", HML, `{"Robo":{"CORSeqId":"85","CCONroConta":"99999"}}`, "", "CONFIG_USUARIO_INVALIDA"},
		{"ambiente inválido", "PROD", `{"Robo":{"CORSeqId":85,"CCONroConta":"1234567"}}`, "", "CONTA_AMBIENTE"},
	}
	for _, c := range casos {
		r, f := Resolver(c.ambiente, c.texto, hml)
		switch {
		case c.codigo == "" && f != nil:
			t.Errorf("%s: falha inesperada %v", c.nome, f)
		case c.codigo != "" && (f == nil || f.Codigo != c.codigo):
			t.Errorf("%s: esperava %s, veio %v %+v", c.nome, c.codigo, f, r)
		case c.codigo == "" && (r.Conta != c.conta || r.Conector != "PNT"):
			t.Errorf("%s: %+v", c.nome, r)
		}
	}
	if _, f := Resolver(Producao, real1450, hml); f.Alerta != "CONTA_AMBIENTE" {
		t.Error("trava cruzada: alerta crítico CONTA_AMBIENTE")
	}
}

func TestLista(t *testing.T) {
	if l := Lista(" 99999, 88888;77777 "); len(l) != 3 || l[0] != "99999" || l[2] != "77777" {
		t.Fatal(l)
	}
	if l := Lista(""); len(l) != 0 {
		t.Fatal(l)
	}
}

// SPEC-FLEXSCAN-v1 §14: o conector vem da habilitação; a corretora (CORSeqId) só é registrada. A
// conta segue o conector: PnT só dígitos; Flexscan letras, dígitos e hífen (até 20), na HML presente
// em FLEXSCAN_CONTAS_HML.
func TestResolverFlexscan(t *testing.T) {
	hml := Regras{Ambiente: HML, ContasHML: []string{"99999"}, ContasHMLFlx: []string{"S-1117150"}, Flexscan: true}
	// o 5725 (23/10): BTG no GQ, pela Flexscan
	btg := `{"Robo":{"CORSeqId":85,"CCONroConta":"S-1117150"}}`
	r, f := ResolverCom(hml, btg, []string{"FLEXSCAN"})
	if f != nil || r.Conta != "S-1117150" || r.Destino != 85 || r.Conector != "FLEXSCAN" {
		t.Fatalf("BTG pela Flexscan: %+v %v", r, f)
	}
	// a mesma conta pelo PnT: regra do PnT
	if _, f := ResolverCom(hml, btg, []string{"PNT"}); f == nil || f.Codigo != "CONTA_INVALIDA" || !strings.Contains(f.Motivo, "conector PNT") {
		t.Fatalf("conta da Flexscan pelo PnT: %v", f)
	}
	// BTG pelo PnT, como sempre
	if r, f := ResolverCom(hml, `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`, []string{"PNT"}); f != nil || r.Conector != "PNT" || r.Destino != 85 {
		t.Fatalf("BTG pelo PnT: %+v %v", r, f)
	}
	// Capital pela Flexscan em produção: conta numérica
	if r, f := ResolverCom(Regras{Ambiente: Producao, Flexscan: true}, `{"Robo":{"CORSeqId":88,"CCONroConta":"715124"}}`, []string{"FLEXSCAN"}); f != nil || r.Conta != "715124" || r.Destino != 88 {
		t.Fatalf("Capital em produção: %+v %v", r, f)
	}
	if _, f := ResolverCom(Regras{Ambiente: HML, ContasHML: []string{"99999"}}, btg, []string{"FLEXSCAN"}); f == nil || f.Codigo != "CONECTOR_NAO_CONFIGURADO" {
		t.Fatalf("Flexscan sem FLEXSCAN_URL: %v", f)
	}
	if _, f := ResolverCom(Regras{Ambiente: HML, ContasHML: []string{"S-1117150"}, Flexscan: true}, btg, []string{"FLEXSCAN"}); f == nil || f.Codigo != "CONTA_AMBIENTE" {
		t.Fatalf("conta na lista do PnT e fora da da Flexscan: %v", f)
	}
	if _, f := ResolverCom(Regras{Ambiente: Producao, ContasHMLFlx: []string{"S-1117150"}, Flexscan: true}, btg, []string{"FLEXSCAN"}); f == nil || f.Codigo != "CONTA_AMBIENTE" {
		t.Fatalf("conta de HML da Flexscan em PRODUCAO: %v", f)
	}
	for _, conta := range []string{"S 11/7", "S-123456789012345678901", ""} {
		if _, f := ResolverCom(hml, `{"Robo":{"CORSeqId":85,"CCONroConta":"`+conta+`"}}`, []string{"FLEXSCAN"}); f == nil || f.Codigo != "CONTA_INVALIDA" {
			t.Fatalf("conta Flexscan %q: %v", conta, f)
		}
	}
	if _, f := ResolverCom(hml, `{"Robo":{"CORSeqId":9999,"CCONroConta":"S-1117150"}}`, []string{"FLEXSCAN"}); f == nil || f.Codigo != "CONFIG_USUARIO_INVALIDA" {
		t.Fatalf("simulador do GQ pela Flexscan: %v", f)
	}
	if got := Lido(btg); got != `CORSeqId=85 Conector=- CCONroConta="S-1117150"` {
		t.Fatalf("Lido: %s", got)
	}
}

// 23/10: o JSON real do 5725 (o GX gera Robo.Conector; a conta de futuros no grupo Futuros, que vale
// para WIN), com as credenciais do Robo, que nunca aparecem em recusa nem no Lido.
const real5725 = `{"Robo":{"CiaCodCIa":1,"UsuCodUsuario":"5725","CORSeqId":85,"CCONroConta":"S-1117150","Conector":"FLEXSCAN",` +
	`"CCOTxtUser":"usuario-secreto-x","StrSenhaRobo":"senha-secreta-y"},"Futuros":{"CCONroContaFut":"S-1117150"}}`

func TestContaDeFuturosEConectorDoGQ(t *testing.T) {
	hml := Regras{Ambiente: HML, ContasHML: []string{"99999"}, ContasHMLFlx: []string{"S-1117150", "S-2"}, Flexscan: true}
	r, f := ResolverCom(hml, real5725, []string{"FLEXSCAN"})
	if f != nil || r.Conta != "S-1117150" || r.OrigemConta != "Futuros.CCONroContaFut" || r.Conector != "FLEXSCAN" {
		t.Fatalf("5725: %+v %v", r, f)
	}
	// as duas contas diferentes: vale a de futuros
	dif := `{"Robo":{"CORSeqId":85,"CCONroConta":"S-1","Conector":"FLEXSCAN"},"Futuros":{"CCONroContaFut":"S-2"}}`
	if r, f := ResolverCom(hml, dif, []string{"FLEXSCAN"}); f != nil || r.Conta != "S-2" {
		t.Fatalf("contas diferentes: %+v %v", r, f)
	}
	// sem o grupo Futuros (ou vazia): a do Robo, como antes
	for _, j := range []string{`{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`, `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"},"Futuros":{"CCONroContaFut":""}}`} {
		if r, f := ResolverCom(hml, j, []string{"PNT"}); f != nil || r.Conta != "99999" || r.OrigemConta != "Robo.CCONroConta" {
			t.Fatalf("sem futuros %s: %+v %v", j, r, f)
		}
	}
	// o conector do GQ fora da habilitação
	if _, f := ResolverCom(hml, real5725, []string{"PNT"}); f == nil || f.Codigo != "CONECTOR_NAO_HABILITADO" {
		t.Fatalf("conector do GQ fora da habilitação: %v", f)
	}
	// credenciais fora de toda recusa e do Lido
	casos := []string{real5725, strings.Replace(real5725, `"CORSeqId":85`, `"CORSeqId":9999`, 1),
		strings.Replace(real5725, `"CCONroContaFut":"S-1117150"`, `"CCONroContaFut":"S 1"`, 1), real5725[:len(real5725)-5]}
	for _, j := range casos {
		for _, con := range []string{"PNT", "FLEXSCAN"} {
			_, f := ResolverCom(hml, j, []string{con})
			for _, txt := range []string{Lido(j), fmt.Sprint(f)} {
				if strings.Contains(txt, "secret") {
					t.Fatalf("credencial no texto: %s", txt)
				}
			}
		}
	}
}

// SPEC-FLEXSCAN-v1 §14.3: com mais de um conector na habilitação, o GQ escolhe (Robo.Conector),
// desde que esteja na lista; sem Robo.Conector, só com um conector habilitado.
func TestConectorEscolhidoPeloGQ(t *testing.T) {
	hml := Regras{Ambiente: HML, ContasHML: []string{"99999"}, ContasHMLFlx: []string{"99999"}, Flexscan: true}
	j := func(con string) string {
		if con == "" {
			return `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`
		}
		return `{"Robo":{"CORSeqId":85,"CCONroConta":"99999","Conector":"` + con + `"}}`
	}
	dois := []string{"PNT", "FLEXSCAN"}
	casos := []struct {
		gq          string
		habilitados []string
		conector    string
		codigo      string
	}{
		{"FLEXSCAN", dois, "FLEXSCAN", ""},
		{"flexscan", dois, "FLEXSCAN", ""},
		{"PNT", dois, "PNT", ""},
		{"", dois, "", "CONECTOR_INDEFINIDO"},
		{"", []string{"FLEXSCAN"}, "FLEXSCAN", ""},
		{"", nil, "PNT", ""},
		{"FLEXSCAN", nil, "", "CONECTOR_NAO_HABILITADO"},
		{"FLEXSCAN", []string{"PNT"}, "", "CONECTOR_NAO_HABILITADO"},
		{"PNT", []string{"FLEXSCAN"}, "", "CONECTOR_NAO_HABILITADO"},
		{"XP", dois, "", "CONECTOR_NAO_HABILITADO"},
	}
	for _, c := range casos {
		r, f := ResolverCom(hml, j(c.gq), c.habilitados)
		switch {
		case c.codigo == "" && (f != nil || r.Conector != c.conector):
			t.Errorf("GQ %q, habilitados %v: %+v %v", c.gq, c.habilitados, r, f)
		case c.codigo != "" && (f == nil || f.Codigo != c.codigo):
			t.Errorf("GQ %q, habilitados %v: esperava %s, veio %v", c.gq, c.habilitados, c.codigo, f)
		}
	}
}

// SPEC-AMBIENTES-v1 §0.1 e §4.1: a coluna é a do ambiente da ordem; o paper (9999 + QUANT) só no
// SIMULADOR; o PnT só no REAL (salvo na equivalência HML); a trava nos dois sentidos e REAL_CONTAS.
func TestResolverAmbientes(t *testing.T) {
	sim := Regras{Ambiente: dominio.AmbienteSimulador, ContasHML: []string{"99999"}, ContasHMLFlx: []string{"S-1117150"}, Flexscan: true}
	real := Regras{Ambiente: dominio.AmbienteReal, ContasHML: []string{"99999"}, ContasHMLFlx: []string{"S-1117150"}, Flexscan: true,
		RealContas: []string{"715124"}}
	flx := []string{"FLEXSCAN"}
	casos := []struct {
		nome   string
		rg     Regras
		texto  string
		hab    []string
		codigo string
		conta  string
	}{
		{"simulador externo (o 5725)", sim, `{"Robo":{"Conector":"FLEXSCAN","CORSeqId":85,"CCONroConta":"S-1117150"}}`, flx, "", "S-1117150"},
		{"paper", sim, `{"Robo":{"CORSeqId":9999,"CCONroConta":"QUANT"}}`, nil, "DESTINO_PAPER", ""},
		{"paper com a conta de futuros", sim, `{"Robo":{"CORSeqId":9999,"CCONroConta":"x"},"Futuros":{"CCONroContaFut":"QUANT"}}`, nil, "DESTINO_PAPER", ""},
		{"9999 sem QUANT", sim, `{"Robo":{"CORSeqId":9999,"CCONroConta":"S-1117150"}}`, flx, "CONFIG_USUARIO_INVALIDA", ""},
		{"PnT no SIMULADOR", sim, `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`, nil, "CONECTOR_AMBIENTE", ""},
		{"conta real no SIMULADOR", sim, `{"Robo":{"CORSeqId":88,"CCONroConta":"715124"}}`, flx, "CONTA_AMBIENTE", ""},
		{"REAL na Capital", real, `{"Robo":{"CORSeqId":88,"CCONroConta":"715124"}}`, flx, "", "715124"},
		{"conta de simulador no REAL", real, `{"Robo":{"CORSeqId":85,"CCONroConta":"S-1117150"}}`, flx, "CONTA_AMBIENTE", ""},
		{"conta de simulador do outro conector no REAL", real, `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`, flx, "CONTA_AMBIENTE", ""},
		{"fora de REAL_CONTAS", real, `{"Robo":{"CORSeqId":88,"CCONroConta":"715999"}}`, flx, "REAL_CONTA_NAO_PERMITIDA", ""},
		{"PnT no REAL", Regras{Ambiente: dominio.AmbienteReal, RealContasTodas: true}, `{"Robo":{"CORSeqId":85,"CCONroConta":"123"}}`, nil, "", "123"},
		{"9999 no REAL", real, `{"Robo":{"CORSeqId":9999,"CCONroConta":"QUANT"}}`, nil, "DESTINO_INVALIDO", ""},
		{"ambiente inválido", Regras{Ambiente: "X"}, `{"Robo":{"CORSeqId":85,"CCONroConta":"1"}}`, nil, "CONTA_AMBIENTE", ""},
	}
	for _, c := range casos {
		r, f := ResolverCom(c.rg, c.texto, c.hab)
		got := ""
		if f != nil {
			got = f.Codigo
		}
		if got != c.codigo || (c.codigo == "" && r.Conta != c.conta) {
			t.Errorf("%s: esperava %q (conta %q), veio %q %+v %v", c.nome, c.codigo, c.conta, got, r, f)
		}
		if c.codigo == "DESTINO_PAPER" && (!f.Paper() || f.Alerta != "") {
			t.Errorf("%s: o paper não é erro de configuração: %+v", c.nome, f)
		}
	}
	if _, f := ResolverCom(real, `{"Robo":{"CORSeqId":88,"CCONroConta":"715999"}}`, flx); f.Alerta != "REAL_CONTA_NAO_PERMITIDA" {
		t.Errorf("REAL_CONTA_NAO_PERMITIDA sem alerta: %+v", f)
	}
}
