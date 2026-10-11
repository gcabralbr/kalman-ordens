package usuario

import "testing"

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
		{"Capital → Flexscan", HML, `{"Robo":{"CORSeqId":88,"CCONroConta":"99999"}}`, "", "DESTINO_NAO_SUPORTADO"},
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
		case c.codigo == "" && (r.Conta != c.conta || r.Destino != DestinoPnT):
			t.Errorf("%s: %+v", c.nome, r)
		}
	}
	if _, f := Resolver(HML, `{"Robo":{"CORSeqId":88,"CCONroConta":"99999"}}`, hml); f.Alerta != "" {
		t.Error("destino 88: só recusa, sem alerta")
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
