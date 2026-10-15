package alerta

import "strings"

// trocas: o que os textos do serviço usam fora do ASCII (acentos do português e símbolos).
var trocas = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "Á", "A", "À", "A", "Â", "A", "Ã", "A", "Ä", "A",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "É", "E", "È", "E", "Ê", "E", "Ë", "E",
	"í", "i", "ì", "i", "î", "i", "ï", "i", "Í", "I", "Ì", "I", "Î", "I", "Ï", "I",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o", "Ó", "O", "Ò", "O", "Ô", "O", "Õ", "O", "Ö", "O",
	"ú", "u", "ù", "u", "û", "u", "ü", "u", "Ú", "U", "Ù", "U", "Û", "U", "Ü", "U",
	"ç", "c", "Ç", "C", "ñ", "n", "Ñ", "N", "º", "o", "ª", "a", "°", "o",
	"−", "-", "–", "-", "—", "-", "→", "->", "←", "<-", "⇒", "=>", "≠", "!=", "≤", "<=", "≥", ">=",
	"×", "x", "…", "...", "∓", "-/+", "±", "+/-", "“", `"`, "”", `"`, "‘", "'", "’", "'", " ", " ",
)

// ASCII: textos de recusa, alerta e eventos só em ASCII (v1.4 L1) — o Kalman grava em latin1 e o
// "−" do BOOK_DESCOLADO quebrou a gravação. Acentos e símbolos conhecidos viram o equivalente; o
// resto, "?".
func ASCII(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	s = trocas.Replace(s)
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}
