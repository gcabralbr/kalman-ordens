package pnt

import (
	"regexp"

	"github.com/quickfixgo/quickfix"
)

// Senha fora de qualquer arquivo. O QuickFIX/Go grava a mensagem como foi ao fio no log da sessão
// (FileLog) e no store (FileStore, para reenvio): o logon levaria o 554 (Password) e o 925
// (NewPassword) em texto puro, como no log antigo do PT. A camada mascara antes de gravar; no fio
// a senha vai inteira. O logon gravado mascarado nunca é reenviado (mensagem de sessão: no
// ResendRequest a biblioteca manda GapFill no lugar).
var reSenha = regexp.MustCompile(`(^|\x01|\|)(554|925)=[^\x01|]*`)

const senhaMascarada = "***"

// Mascarar troca o valor de 554 e 925 por ***. Aceita SOH ou | como separador.
func Mascarar(b []byte) []byte {
	if !reSenha.Match(b) {
		return b
	}
	return reSenha.ReplaceAll(b, []byte("${1}${2}="+senhaMascarada))
}

// MascararTexto: Mascarar para string (eventos do log, textos de alerta).
func MascararTexto(s string) string { return string(Mascarar([]byte(s))) }

// storeMascarado grava no store a mensagem com a senha mascarada.
type storeMascarado struct {
	quickfix.MessageStore
}

func (s storeMascarado) SaveMessage(seq int, msg []byte) error {
	return s.MessageStore.SaveMessage(seq, Mascarar(msg))
}

func (s storeMascarado) SaveMessageAndIncrNextSenderMsgSeqNum(seq int, msg []byte) error {
	return s.MessageStore.SaveMessageAndIncrNextSenderMsgSeqNum(seq, Mascarar(msg))
}
