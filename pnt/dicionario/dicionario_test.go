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
	orig, err := os.ReadFile("dsa_FIX44.original.xml")
	if err != nil {
		t.Fatal(err)
	}
	// o original não tem type="FIX"; acrescenta só isso para o QuickFIX/Go aceitar
	orig = bytes.Replace(orig, []byte("<fix major="), []byte("<fix type=\"FIX\" major="), 1)
	original, err := datadictionary.ParseSrc(bytes.NewReader(orig))
	if err != nil {
		t.Fatal(err)
	}
	for nome, msg := range casos {
		if err := validar(t, atual, msg); err != nil {
			t.Errorf("%s: rejeitada pela cópia do serviço: %v", nome, err)
		}
		if err := validar(t, original, msg); err == nil {
			t.Errorf("%s: o original também aceitaria (o teste não está provando nada)", nome)
		}
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
