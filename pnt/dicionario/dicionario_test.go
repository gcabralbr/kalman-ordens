package dicionario

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/datadictionary"
)

// Validação de recepção do serviço (SPEC v1.1 F2): RejectInvalidMessage=N, campos vazios e fora
// de ordem aceitos. O que ainda reprova aqui vira 35=3 e a mensagem não chega à aplicação.
var recepcao = quickfix.ValidatorSettings{
	CheckFieldsOutOfOrder:     false,
	RejectInvalidMessage:      false,
	AllowUnknownMessageFields: true,
	CheckUserDefinedFields:    false,
	CheckFieldsHaveValues:     false,
}

func montar(pipe string) []byte {
	var corpo []string
	for _, p := range strings.Split(pipe, "|") {
		if p != "" {
			corpo = append(corpo, p)
		}
	}
	b := strings.Join(corpo, "\x01") + "\x01"
	s := fmt.Sprintf("8=FIX.4.4\x019=%d\x01%s", len(b), b)
	soma := 0
	for i := 0; i < len(s); i++ {
		soma += int(s[i])
	}
	return []byte(fmt.Sprintf("%s10=%03d\x01", s, soma%256))
}

func validar(t *testing.T, dd *datadictionary.DataDictionary, pipe string) error {
	t.Helper()
	m := quickfix.NewMessage()
	if err := quickfix.ParseMessageWithDataDictionary(m, bytes.NewBuffer(montar(pipe)), dd, dd); err != nil {
		return err
	}
	if rej := quickfix.NewValidator(recepcao, dd, nil).Validate(m); rej != nil {
		return rej
	}
	return nil
}

// Um fill sem 6, 151, 54, 55 e 17 não pode ser descartado pela biblioteca (SPEC v1.1 F8):
// passa na cópia do serviço e seria rejeitado pelo dicionário original.
func TestFillIncompletoNaoEDescartado(t *testing.T) {
	cab := "35=8|34=2|49=PNT_DSA|52=20261013-13:15:00.000|56=KALMAN|"
	casos := map[string]string{
		"fill sem 6/151/54/55/17": cab + "11=KM-E-1-1|37=dsa.1|150=F|39=2|14=1|32=1|31=130010",
		"ER só com 11 e 39":       cab + "11=KM-E-1-1|39=0",
		"35=9 sem 37/41/434":      strings.Replace(cab, "35=8", "35=9", 1) + "11=KM-P-1-2|58=x",
	}
	atual, err := Carregar()
	if err != nil {
		t.Fatal(err)
	}
	original := doPnT(t)
	for nome, msg := range casos {
		if err := validar(t, atual, msg); err != nil {
			t.Errorf("%s: rejeitada pela cópia do serviço: %v", nome, err)
		}
		if err := validar(t, original, msg); err == nil {
			t.Errorf("%s: o original também aceitaria (o teste não está provando nada)", nome)
		}
	}
}

// O dicionário do PnT com o mínimo para o QuickFIX/Go carregar (diferenças 1 e 5).
func doPnT(t *testing.T) *datadictionary.DataDictionary {
	t.Helper()
	orig, err := os.ReadFile(Original)
	if err != nil {
		t.Fatal(err)
	}
	orig = bytes.Replace(orig, []byte("<fix major="), []byte("<fix type=\"FIX\" major="), 1)
	orig = bytes.Replace(orig, []byte(`name="LegLastQty" type="QTY"`), []byte(`name="LegLastShares" type="INT"`), 1)
	dd, err := datadictionary.ParseSrc(bytes.NewReader(orig))
	if err != nil {
		t.Fatal(err)
	}
	return dd
}

// Diferença 5: o InstrumentLeg do PnT refere a 20798 como LegLastQty, nome que <fields> não
// declara (lá ela é LegLastShares). Só com type="FIX", o arquivo não carrega.
func TestDicionarioDoPnTNaoCarregaSemADiferenca5(t *testing.T) {
	orig, err := os.ReadFile(Original)
	if err != nil {
		t.Fatal(err)
	}
	orig = bytes.Replace(orig, []byte("<fix major="), []byte("<fix type=\"FIX\" major="), 1)
	if _, err := datadictionary.ParseSrc(bytes.NewReader(orig)); err == nil || !strings.Contains(err.Error(), "LegLastQty") {
		t.Fatalf("esperava falha por LegLastQty; veio %v", err)
	}
}

// A cópia embutida é exatamente Derivar(original do PnT): nenhuma edição à mão.
func TestCopiaDerivadaDoOriginal(t *testing.T) {
	pnt, err := os.ReadFile(Original)
	if err != nil {
		t.Fatal(err)
	}
	copia, err := Derivar(pnt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copia, XML) {
		t.Fatal("dsa_FIX44.xml difere de Derivar(original): rodar go generate ./pnt/dicionario")
	}
}

// Validação estrita do que o serviço envia, mais dura que a do PnT falso: tag de usuário também
// precisa estar declarada na mensagem.
var envio = quickfix.ValidatorSettings{
	CheckFieldsOutOfOrder:     true,
	RejectInvalidMessage:      true,
	AllowUnknownMessageFields: false,
	CheckUserDefinedFields:    true,
	CheckFieldsHaveValues:     true,
}

// 1014 e replace da 1014 com 20828/20829 e 20856=Y: passam na cópia; no dicionário do PnT o 20856
// já está no 35=D, mas 20828/20829 não existem (diferença 4).
func TestProtecao1014Declarada(t *testing.T) {
	cab := "34=2|49=KALMAN|52=20261013-13:15:00.000|56=PNT_DSA|116=12345|"
	nos := "35=D|" + cab + "11=KM-P-1-1|1=12345|55=WINZ26|54=2|60=20261013-13:15:00.000|38=1|40=2|44=129900|59=0|847=1014|20828=130000|20829=129900|20856=Y"
	rep := "35=G|" + cab + "11=KM-P-1-2|41=KM-P-1-1|1=12345|55=WINZ26|54=2|60=20261013-13:15:00.000|38=2|40=2|44=129900|59=0|847=1014|20828=130000|20829=129900"
	atual, err := Carregar()
	if err != nil {
		t.Fatal(err)
	}
	pnt := doPnT(t)
	estrito := func(dd *datadictionary.DataDictionary, pipe string) error {
		m := quickfix.NewMessage()
		if err := quickfix.ParseMessageWithDataDictionary(m, bytes.NewBuffer(montar(pipe)), dd, dd); err != nil {
			return err
		}
		if rej := quickfix.NewValidator(envio, dd, nil).Validate(m); rej != nil {
			return rej
		}
		return nil
	}
	for nome, msg := range map[string]string{"35=D": nos, "35=G": rep} {
		if err := estrito(atual, msg); err != nil {
			t.Errorf("%s da 1014 rejeitado pela cópia: %v", nome, err)
		}
		if err := estrito(pnt, msg); err == nil {
			t.Errorf("%s da 1014 aceito pelo dicionário do PnT (a diferença 4 não é mais necessária?)", nome)
		}
	}
	if err := estrito(pnt, strings.NewReplacer("|20828=130000", "", "|20829=129900", "").Replace(nos)); err != nil {
		t.Errorf("35=D sem 20828/20829 rejeitado pelo dicionário do PnT (o 20856 deveria estar declarado): %v", err)
	}
}

// O que continua gerando 35=3 mesmo com a cópia relaxada: tipo de mensagem desconhecido.
func TestTipoDesconhecidoAindaRejeita(t *testing.T) {
	atual, err := Carregar()
	if err != nil {
		t.Fatal(err)
	}
	if err := validar(t, atual, "35=U9|34=2|49=PNT_DSA|52=20261013-13:15:00.000|56=KALMAN|58=x"); err == nil {
		t.Fatal("35=U9 deveria ser rejeitado")
	}
}
