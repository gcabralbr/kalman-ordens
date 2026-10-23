package usuario

import (
	"fmt"
	"strings"
	"testing"
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
		{"simulador do GQ", HML, `{"Robo":{"CORSeqId":9999,"CCONroConta":"QUANT"}}`, "", "DESTINO_INVALIDO"},
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
	r, f := ResolverCom(hml, btg, "FLEXSCAN")
	if f != nil || r.Conta != "S-1117150" || r.Destino != 85 || r.Conector != "FLEXSCAN" {
		t.Fatalf("BTG pela Flexscan: %+v %v", r, f)
	}
	// a mesma conta pelo PnT: regra do PnT
	if _, f := ResolverCom(hml, btg, "PNT"); f == nil || f.Codigo != "CONTA_INVALIDA" || !strings.Contains(f.Motivo, "conector PNT") {
		t.Fatalf("conta da Flexscan pelo PnT: %v", f)
	}
	// BTG pelo PnT, como sempre
	if r, f := ResolverCom(hml, `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`, "PNT"); f != nil || r.Conector != "PNT" || r.Destino != 85 {
		t.Fatalf("BTG pelo PnT: %+v %v", r, f)
	}
	// Capital pela Flexscan em produção: conta numérica
	if r, f := ResolverCom(Regras{Ambiente: Producao, Flexscan: true}, `{"Robo":{"CORSeqId":88,"CCONroConta":"715124"}}`, "FLEXSCAN"); f != nil || r.Conta != "715124" || r.Destino != 88 {
		t.Fatalf("Capital em produção: %+v %v", r, f)
	}
	if _, f := ResolverCom(Regras{Ambiente: HML, ContasHML: []string{"99999"}}, btg, "FLEXSCAN"); f == nil || f.Codigo != "CONECTOR_NAO_CONFIGURADO" {
		t.Fatalf("Flexscan sem FLEXSCAN_URL: %v", f)
	}
	if _, f := ResolverCom(Regras{Ambiente: HML, ContasHML: []string{"S-1117150"}, Flexscan: true}, btg, "FLEXSCAN"); f == nil || f.Codigo != "CONTA_AMBIENTE" {
		t.Fatalf("conta na lista do PnT e fora da da Flexscan: %v", f)
	}
	if _, f := ResolverCom(Regras{Ambiente: Producao, ContasHMLFlx: []string{"S-1117150"}, Flexscan: true}, btg, "FLEXSCAN"); f == nil || f.Codigo != "CONTA_AMBIENTE" {
		t.Fatalf("conta de HML da Flexscan em PRODUCAO: %v", f)
	}
	for _, conta := range []string{"S 11/7", "S-123456789012345678901", ""} {
		if _, f := ResolverCom(hml, `{"Robo":{"CORSeqId":85,"CCONroConta":"`+conta+`"}}`, "FLEXSCAN"); f == nil || f.Codigo != "CONTA_INVALIDA" {
			t.Fatalf("conta Flexscan %q: %v", conta, f)
		}
	}
	if _, f := ResolverCom(hml, `{"Robo":{"CORSeqId":9999,"CCONroConta":"S-1117150"}}`, "FLEXSCAN"); f == nil || f.Codigo != "DESTINO_INVALIDO" {
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
	r, f := ResolverCom(hml, real5725, "FLEXSCAN")
	if f != nil || r.Conta != "S-1117150" || r.OrigemConta != "Futuros.CCONroContaFut" || r.Conector != "FLEXSCAN" {
		t.Fatalf("5725: %+v %v", r, f)
	}
	// as duas contas diferentes: vale a de futuros
	dif := `{"Robo":{"CORSeqId":85,"CCONroConta":"S-1","Conector":"FLEXSCAN"},"Futuros":{"CCONroContaFut":"S-2"}}`
	if r, f := ResolverCom(hml, dif, "FLEXSCAN"); f != nil || r.Conta != "S-2" {
		t.Fatalf("contas diferentes: %+v %v", r, f)
	}
	// sem o grupo Futuros (ou vazia): a do Robo, como antes
	for _, j := range []string{`{"Robo":{"CORSeqId":85,"CCONroConta":"99999"}}`, `{"Robo":{"CORSeqId":85,"CCONroConta":"99999"},"Futuros":{"CCONroContaFut":""}}`} {
		if r, f := ResolverCom(hml, j, "PNT"); f != nil || r.Conta != "99999" || r.OrigemConta != "Robo.CCONroConta" {
			t.Fatalf("sem futuros %s: %+v %v", j, r, f)
		}
	}
	// o conector do GQ diferente do da habilitação
	if _, f := ResolverCom(hml, real5725, "PNT"); f == nil || f.Codigo != "CONECTOR_DIVERGENTE" {
		t.Fatalf("conector divergente: %v", f)
	}
	// credenciais fora de toda recusa e do Lido
	casos := []string{real5725, strings.Replace(real5725, `"CORSeqId":85`, `"CORSeqId":9999`, 1),
		strings.Replace(real5725, `"CCONroContaFut":"S-1117150"`, `"CCONroContaFut":"S 1"`, 1), real5725[:len(real5725)-5]}
	for _, j := range casos {
		for _, con := range []string{"PNT", "FLEXSCAN"} {
			_, f := ResolverCom(hml, j, con)
			for _, txt := range []string{Lido(j), fmt.Sprint(f)} {
				if strings.Contains(txt, "secret") {
					t.Fatalf("credencial no texto: %s", txt)
				}
			}
		}
	}
}
