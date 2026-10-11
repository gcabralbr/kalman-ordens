package pnt

import (
	"bytes"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/pnt/falso"
)

// Log real de HML do PT (out/nov 2025, QuickFIX/J, senha mascarada): cenarios/referencia.
const logReal = "../cenarios/referencia/fixpnt-hml-mascarado.log"

var reMsgLog = regexp.MustCompile(`(?s)<[^,]+, [^,]+, (incoming|outgoing)> \((8=FIX\.4\.4\x01.*?\x0110=\d{3}\x01)`)

type msgLog struct {
	entrada bool
	bruta   []byte
}

func lerLogReal(t *testing.T) []msgLog {
	t.Helper()
	b, err := os.ReadFile(logReal)
	if err != nil {
		t.Fatal(err)
	}
	var r []msgLog
	for _, m := range reMsgLog.FindAllSubmatch(b, -1) {
		r = append(r, msgLog{entrada: string(m[1]) == "incoming", bruta: m[2]})
	}
	if len(r) != 370 {
		t.Fatalf("o log tem 370 mensagens FIX; lidas %d", len(r))
	}
	return r
}

// 9 e 10 conferidos byte a byte: todas as mensagens do PnT estão certas (o normalizador da
// pendência 9 da v1.1 não é necessário). Só os dois logons enviados pelo PT não batem — por causa
// da máscara do usuário (553) e da senha (554) na fixture, não do fio.
func TestLogRealBodyLengthECheckSum(t *testing.T) {
	ruins := 0
	for _, m := range lerLogReal(t) {
		b := m.bruta
		i9 := bytes.Index(b, []byte("\x019=")) + 3
		j9 := i9 + bytes.IndexByte(b[i9:], 1)
		fim := bytes.LastIndex(b, []byte("\x0110=")) + 1
		nove, _ := strconv.Atoi(string(b[i9:j9]))
		dez, _ := strconv.Atoi(string(b[fim+3 : fim+6]))
		soma := 0
		for _, c := range b[:fim] {
			soma += int(c)
		}
		if nove == fim-(j9+1) && dez == soma%256 {
			continue
		}
		if !m.entrada && bytes.Contains(b, []byte("\x0135=A\x01")) &&
			bytes.Contains(b, []byte("\x01553=***\x01")) && bytes.Contains(b, []byte("\x01554=***\x01")) {
			continue // logon do PT com usuário e senha mascarados na fixture
		}
		ruins++
		t.Errorf("9/10 errado: %s", bytes.ReplaceAll(b[:min(len(b), 120)], []byte{1}, []byte("|")))
	}
	if ruins > 0 {
		t.Fatalf("%d mensagem(ns) com 9/10 errados", ruins)
	}
}

// Cabeçalho e trailer: o falso põe os seus. O 116 (OnBehalfOfSubID) é de cabeçalho.
var tagsDeEnvelope = map[int]bool{8: true, 9: true, 10: true, 34: true, 35: true, 43: true, 49: true, 52: true, 56: true, 97: true, 116: true, 122: true}

// Todas as mensagens de aplicação do PnT no log real (192 ExecutionReports, inclusive multileg com
// grupos que o dicionário de 2020 não descreve, e 2 OrderCancelReject) passam pela sessão do serviço:
// nenhuma descartada (35=3 ou ilegível), todas entregues à aplicação, na ordem.
func TestLogRealPassaNaSessao(t *testing.T) {
	f, _, o := subir(t)
	var esperadas []string
	for _, m := range lerLogReal(t) {
		if !m.entrada {
			continue
		}
		var campos []falso.Campo
		tipo := ""
		for _, c := range bytes.Split(bytes.TrimSuffix(m.bruta, []byte{1}), []byte{1}) {
			k, v, _ := bytes.Cut(c, []byte("="))
			tag, _ := strconv.Atoi(string(k))
			if tag == 35 {
				tipo = string(v)
			}
			if !tagsDeEnvelope[tag] {
				campos = append(campos, falso.Campo{Tag: tag, Valor: string(v)})
			}
		}
		if tipo != "8" && tipo != "9" {
			continue
		}
		campos = append([]falso.Campo{c(35, tipo)}, campos...)
		if err := f.Enviar(campos); err != nil {
			t.Fatal(err)
		}
		for _, c := range campos {
			if c.Tag == 17 || (tipo == "9" && c.Tag == 11) {
				esperadas = append(esperadas, c.Valor)
			}
		}
	}
	if len(esperadas) != 194 {
		t.Fatalf("esperava 194 mensagens de aplicação do PnT; montadas %d", len(esperadas))
	}
	esperar(t, "as 194 mensagens", func() bool { r, _ := o.copia(); return len(r) >= 194 })
	time.Sleep(200 * time.Millisecond)
	r, rej := o.copia()
	o.mu.Lock()
	ilegiveis := len(o.ilegiveis)
	o.mu.Unlock()
	if len(rej) > 0 || ilegiveis > 0 {
		t.Fatalf("mensagem real descartada: %d 35=3 enviados, %d ilegíveis", len(rej), ilegiveis)
	}
	if len(r) != 194 {
		t.Fatalf("entregues %d, esperadas 194", len(r))
	}
	for i, m := range r {
		chave := m.Texto(17)
		if m.Texto(35) == "9" {
			chave = m.Texto(11)
		}
		if chave != esperadas[i] {
			t.Fatalf("mensagem %d: %q, esperada %q", i, chave, esperadas[i])
		}
	}
}
