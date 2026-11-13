package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// cliFio (v1.34, SPEC-ADMIN-CONECTORES-v1 §3.5): o fio inteiro do PnT num dia, lido do log de
// arquivo do QuickFIX (<FIX_DIR>/log; o REAL em REAL_FIX_DIR), que a API não lê. Tem o que o banco
// não guarda: logon, heartbeat, test request, resend, mass status. Senha mascarada (554/925), SOH
// como "|", hora de Brasília e a direção (ENV/REC pelo 49). O diretório vem do service.env (-env;
// a leitura pede sudo) ou de -dir.
//
//	kalman-ordens fio [-dia D] [-ambiente SIMULADOR|REAL] [-dir DIR] [-clordid P] [-sem-heartbeat] [-eventos]
func cliFio(cfg config.Config, args []string, saida io.Writer) error {
	fs := flag.NewFlagSet("fio", flag.ContinueOnError)
	dia := fs.String("dia", time.Now().In(relogio.Fuso).Format("2006-01-02"), "dia do pregão AAAA-MM-DD (hora de Brasília)")
	ambiente := fs.String("ambiente", dominio.AmbienteSimulador, "SIMULADOR (FIX_DIR) ou REAL (REAL_FIX_DIR)")
	dir := fs.String("dir", "", "diretório FIX (com o log/ dentro); ausente = o do service.env")
	clordid := fs.String("clordid", "", "só as mensagens com 11= ou 41= começando por este prefixo")
	semHB := fs.Bool("sem-heartbeat", false, "sem os 35=0 (heartbeat) e 35=1 (test request)")
	eventos := fs.Bool("eventos", false, "junta o log de eventos da sessão (logon, reset, desconexão)")
	arqEnv := fs.String("env", "/etc/kalman-ordens/service.env", "service.env, para FIX_DIR/REAL_FIX_DIR")
	if err := fs.Parse(args); err != nil {
		return err
	}
	amb, err := dominio.LerAmbiente(*ambiente)
	if err != nil {
		return fmt.Errorf("-ambiente: %w", err)
	}
	de, err := time.ParseInLocation("2006-01-02", *dia, relogio.Fuso)
	if err != nil {
		return fmt.Errorf("-dia AAAA-MM-DD (veio %q)", *dia)
	}
	d := *dir
	if d == "" {
		d = dirFIX(cfg, *arqEnv, amb)
	}
	return extrairFio(d, de, de.AddDate(0, 0, 1), filtroFio{clordid: *clordid, semHB: *semHB, eventos: *eventos}, saida)
}

// dirFIX: o diretório da sessão do ambiente (os padrões do serviço: fix e fix-real).
func dirFIX(cfg config.Config, arqEnv, amb string) string {
	if c, err := lerServiceEnv(arqEnv, cfg); err == nil {
		cfg = c
	}
	if amb == dominio.AmbienteReal {
		return cfg.Texto("REAL_FIX_DIR", "/var/lib/kalman-ordens/fix-real")
	}
	return cfg.Texto("FIX_DIR", "/var/lib/kalman-ordens/fix")
}

type filtroFio struct {
	clordid        string
	semHB, eventos bool
}

type linhaFio struct {
	em    time.Time
	ordem int // a posição no arquivo (mensagens antes de eventos no mesmo instante)
	texto string
}

// formatoLogQuickFIX: o prefixo de cada linha do log de arquivo (log.Ldate|Ltime|Lmicroseconds|LUTC).
const formatoLogQuickFIX = "2006/01/02 15:04:05.000000"

func extrairFio(dir string, de, ate time.Time, f filtroFio, saida io.Writer) error {
	arqs, err := filepath.Glob(filepath.Join(dir, "log", "*.messages.current.log"))
	if err != nil {
		return err
	}
	if len(arqs) == 0 {
		return fmt.Errorf("nenhum log de mensagens em %s (rodar com sudo? -dir certo?)", filepath.Join(dir, "log"))
	}
	var ls []linhaFio
	for _, a := range arqs {
		prefixo := strings.TrimSuffix(filepath.Base(a), ".messages.current.log")
		remetente := remetenteDoLog(prefixo)
		x, err := lerLogFIX(a, de, ate, len(ls), func(corpo string) (string, bool) {
			if f.semHB && (strings.Contains(corpo, "\x0135=0\x01") || strings.Contains(corpo, "\x0135=1\x01")) {
				return "", false
			}
			if f.clordid != "" && !temClOrdID(corpo, f.clordid) {
				return "", false
			}
			dir := "REC"
			if strings.Contains(corpo, "\x0149="+remetente+"\x01") {
				dir = "ENV"
			}
			return dir + " " + strings.ReplaceAll(pnt.MascararTexto(corpo), "\x01", "|"), true
		})
		if err != nil {
			return err
		}
		ls = append(ls, x...)
		if f.eventos {
			x, err := lerLogFIX(strings.TrimSuffix(a, ".messages.current.log")+".event.current.log", de, ate, len(ls), func(corpo string) (string, bool) {
				return "EVT " + pnt.MascararTexto(corpo), true
			})
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			ls = append(ls, x...)
		}
	}
	sort.SliceStable(ls, func(i, j int) bool {
		if !ls[i].em.Equal(ls[j].em) {
			return ls[i].em.Before(ls[j].em)
		}
		return ls[i].ordem < ls[j].ordem
	})
	w := bufio.NewWriter(saida)
	for _, l := range ls {
		fmt.Fprintf(w, "%s %s\n", l.em.In(relogio.Fuso).Format("2006-01-02 15:04:05.000000"), l.texto)
	}
	return w.Flush()
}

// remetenteDoLog: o SenderCompID do prefixo "FIX.4.4-SENDER-TARGET" (o nosso lado da sessão).
func remetenteDoLog(prefixo string) string {
	s := strings.TrimPrefix(prefixo, "FIX.4.4-")
	r, _, _ := strings.Cut(s, "-")
	return r
}

func temClOrdID(corpo, prefixo string) bool {
	return strings.Contains(corpo, "\x0111="+prefixo) || strings.Contains(corpo, "\x0141="+prefixo)
}

// lerLogFIX: as linhas de [de, ate) do arquivo (o prefixo de hora é UTC); a linha sem prefixo
// legível fica de fora.
func lerLogFIX(arq string, de, ate time.Time, base int, f func(corpo string) (string, bool)) ([]linhaFio, error) {
	a, err := os.Open(arq)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	var ls []linhaFio
	s := bufio.NewScanner(a)
	s.Buffer(make([]byte, 64*1024), 4*1024*1024)
	n := base
	for s.Scan() {
		l := s.Text()
		if len(l) <= len(formatoLogQuickFIX) {
			continue
		}
		em, err := time.ParseInLocation(formatoLogQuickFIX, l[:len(formatoLogQuickFIX)], time.UTC)
		if err != nil || em.Before(de) || !em.Before(ate) {
			continue
		}
		if t, ok := f(strings.TrimPrefix(l[len(formatoLogQuickFIX):], " ")); ok {
			n++
			ls = append(ls, linhaFio{em: em, ordem: n, texto: t})
		}
	}
	return ls, s.Err()
}
