package pnt

import "regexp"

// Senha fora dos logs. O QuickFIX/Go grava a mensagem como foi ao fio no log da sessão (FileLog):
// o logon levaria o 554 (Password) e o 925 (NewPassword) em texto puro, como no log antigo do PT.
// O espião (sessao.go) mascara antes de gravar; no fio a senha vai inteira.
//
// O store não é mascarado (SPEC v1.3 A1, substitui a parte do store da v1.1 F15): o reenvio relê
// cada mensagem gravada, e um logon mascarado não se lê mais (o 9 não bate) — na HML em 14/10 isso
// derrubou o reenvio dos logons e a sessão entrou em laço. O store fica protegido pelo diretório
// FIX (700/600, só do usuário do serviço), como o service.env.
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
