package pnt

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/datadictionary"

	"gammaquant.com.br/kalman-ordens/pnt/dicionario"
)

// O store (FileStore do QuickFIX/Go) guarda cada mensagem enviada como foi ao fio, para o
// reenvio: num ResendRequest a biblioteca relê e interpreta cada uma (inclusive as de sessão, que
// viram gap fill). Uma mensagem gravada que não se lê derruba o reenvio — "Resend Msg Parse Error"
// — e a sessão cai; o PnT pede de novo no logon seguinte: laço. Foi o que aconteceu na HML em
// 14/10 com os logons gravados com a senha mascarada (o 9 não batia). SPEC v1.3 A1, B1.

// ReparoStore diz o que RepararStore mudou.
type ReparoStore struct {
	Lidas        int // mensagens no store
	Recalculadas int // 9 e 10 recalculados (ex.: logon com a senha mascarada pela v1.1/v1.2)
	Trocadas     int // ilegíveis mesmo assim: trocadas por um heartbeat com o mesmo 34 (vira gap fill)
}

func (r ReparoStore) Mudou() bool { return r.Recalculadas+r.Trocadas > 0 }

func (r ReparoStore) String() string {
	return fmt.Sprintf("%d mensagem(ns), %d com 9/10 recalculados, %d trocada(s) por gap fill", r.Lidas, r.Recalculadas, r.Trocadas)
}

// prefixoStore é o nome dos arquivos do FileStore para a sessão (store/file/util.go do
// QuickFIX/Go; a sessão do serviço não usa SubID nem LocationID).
func prefixoStore(id quickfix.SessionID) string {
	return fmt.Sprintf("%s-%s-%s", id.BeginString, id.SenderCompID, id.TargetCompID)
}

// RepararStore confere cada mensagem do store da sessão (diretório dir) do jeito que o reenvio a
// lê e conserta a que não se lê: primeiro recalcula 9 e 10; se ainda não se lê, troca por um
// heartbeat com o mesmo MsgSeqNum (mensagem de sessão: no reenvio vira gap fill, como já seria
// com uma ordem — ToApp suprime o reenvio de aplicação). Só roda com o store fechado (antes de
// a sessão subir). O corpo só recebe acréscimos; o índice (.header) é trocado de uma vez
// (arquivo novo + rename): uma queda no meio deixa o store antigo intacto.
func RepararStore(dir string, id quickfix.SessionID) (ReparoStore, error) {
	var r ReparoStore
	pre := filepath.Join(dir, prefixoStore(id))
	nomeIndice, nomeCorpo := pre+".header", pre+".body"
	indice, err := os.ReadFile(nomeIndice)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	corpo, err := os.ReadFile(nomeCorpo)
	if err != nil {
		return r, err
	}
	dd, err := dicionario.Carregar()
	if err != nil {
		return r, err
	}
	type entrada struct {
		seq, tam int
		pos      int64
	}
	var es []entrada
	var novas [][]byte
	fimCorpo := int64(len(corpo))
	sc := bufio.NewScanner(bytes.NewReader(indice))
	for sc.Scan() {
		var e entrada
		if _, err := fmt.Sscanf(sc.Text(), "%d,%d,%d", &e.seq, &e.pos, &e.tam); err != nil {
			return r, fmt.Errorf("store: índice ilegível %q: %w", sc.Text(), err)
		}
		r.Lidas++
		var msg []byte
		if e.pos >= 0 && e.tam >= 0 && e.pos+int64(e.tam) <= int64(len(corpo)) {
			msg = corpo[e.pos : e.pos+int64(e.tam)]
		}
		if legivel(msg, dd) {
			es = append(es, e)
			continue
		}
		nova := recalcular(msg)
		if legivel(nova, dd) {
			r.Recalculadas++
		} else {
			nova = heartbeat(id, e.seq)
			r.Trocadas++
		}
		es = append(es, entrada{seq: e.seq, pos: fimCorpo, tam: len(nova)})
		fimCorpo += int64(len(nova))
		novas = append(novas, nova)
	}
	if err := sc.Err(); err != nil {
		return r, err
	}
	if !r.Mudou() {
		return r, nil
	}
	f, err := os.OpenFile(nomeCorpo, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return r, err
	}
	for _, m := range novas {
		if _, err := f.Write(m); err != nil {
			f.Close()
			return r, err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return r, err
	}
	if err := f.Close(); err != nil {
		return r, err
	}
	var b bytes.Buffer
	for _, e := range es {
		fmt.Fprintf(&b, "%d,%d,%d\n", e.seq, e.pos, e.tam)
	}
	tmp := nomeIndice + ".reparo"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return r, err
	}
	return r, os.Rename(tmp, nomeIndice)
}

// legivel: o que o reenvio da biblioteca faz com a mensagem gravada (in_session.go, resendMessages).
func legivel(msg []byte, dd *datadictionary.DataDictionary) bool {
	if len(msg) == 0 {
		return false
	}
	m := quickfix.NewMessage()
	return quickfix.ParseMessageWithDataDictionary(m, bytes.NewBuffer(append([]byte(nil), msg...)), dd, dd) == nil
}

var reCabecalho = regexp.MustCompile("^8=[^\x01]*\x019=[0-9]*\x01")

// recalcular refaz 9 e 10 sobre os bytes da mensagem (o corpo fica como está).
func recalcular(msg []byte) []byte {
	ini := reCabecalho.FindIndex(msg)
	fim := bytes.LastIndex(msg, []byte("\x0110="))
	if ini == nil || fim < ini[1]-1 {
		return nil
	}
	corpo := msg[ini[1] : fim+1]
	inicio := msg[:bytes.Index(msg, []byte("\x019="))+1]
	return comTrailer(append(append(append([]byte(nil), inicio...), "9="+strconv.Itoa(len(corpo))+"\x01"...), corpo...))
}

// heartbeat com o MsgSeqNum de uma mensagem gravada que não se lê.
func heartbeat(id quickfix.SessionID, seq int) []byte {
	corpo := fmt.Sprintf("35=0\x0134=%d\x0149=%s\x0152=%s\x0156=%s\x01", seq, id.SenderCompID,
		time.Now().UTC().Format("20060102-15:04:05.000"), id.TargetCompID)
	return comTrailer([]byte(fmt.Sprintf("8=%s\x019=%d\x01%s", id.BeginString, len(corpo), corpo)))
}

func comTrailer(b []byte) []byte {
	soma := 0
	for _, c := range b {
		soma += int(c)
	}
	return append(b, fmt.Sprintf("10=%03d\x01", soma%256)...)
}
