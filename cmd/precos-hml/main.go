// precos-hml: ferramenta de teste da HML (só na .53). Serve o /precos/atual do marketdata a partir
// do tick file do Kalman do pregão que a HML reproduz (o dia útil anterior), alinhado pela hora do
// dia. Sem banco e sem Cedro. Uso em deploy/LEIA.md (1.9).
//
//	precos-hml -ticker WINZ26 [-data AAAA-MM-DD] [-porta 9095]
//
// Os parâmetros também vêm do ambiente (PRECOS_HML_TICKER, PRECOS_HML_DATA, PRECOS_HML_PORTA): é
// assim que a unidade kalman-precos-hml os recebe.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata"

	"gammaquant.com.br/kalman-ordens/internal/precoshml"
)

var versao = "dev"

var reTicker = regexp.MustCompile(`^WIN[FGHJKMNQUVXZ]\d\d$`)

func main() {
	porta0, _ := strconv.Atoi(env("PRECOS_HML_PORTA", "9095"))
	ticker := flag.String("ticker", os.Getenv("PRECOS_HML_TICKER"), "contrato vigente (ex.: WINZ26), devolvido em ticker")
	data := flag.String("data", os.Getenv("PRECOS_HML_DATA"), "dia do tick file AAAA-MM-DD (vazio: dia útil anterior, recalculado na virada)")
	porta := flag.Int("porta", porta0, "porta em 127.0.0.1")
	dir := flag.String("dir", "/var/lib/kalman/tick", "diretório dos tick files do Kalman")
	feriados := flag.String("feriados", "/var/lib/kalman/feriados.json", "feriados (gerado pelo Kalman da anferiado)")
	adianto0, err := strconv.Atoi(env("PRECOS_HML_ADIANTO_MS", "0"))
	if err != nil {
		log.Fatalf("precos-hml: PRECOS_HML_ADIANTO_MS %q: inteiro em ms (negativo atrasa)", os.Getenv("PRECOS_HML_ADIANTO_MS"))
	}
	adianto := flag.Int("adianto-ms", adianto0, "a leitura do arquivo vai N ms à frente da hora de agora (negativo: atrás)")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if !reTicker.MatchString(*ticker) {
		log.Fatalf("precos-hml: -ticker %q não é contrato de WIN (ex.: WINZ26)", *ticker)
	}
	if *data != "" {
		if _, err := time.Parse("2006-01-02", *data); err != nil {
			log.Fatalf("precos-hml: -data %q: use AAAA-MM-DD", *data)
		}
	}
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		log.Fatal(err)
	}

	var diaCache, arqCache string // o arquivo só muda na virada do dia (chamado sob a trava do Servidor)
	srv := &precoshml.Servidor{
		Ticker:  *ticker,
		Agora:   func() time.Time { return time.Now().In(loc) },
		Log:     log.Printf,
		Adianto: time.Duration(*adianto) * time.Millisecond,
		Arquivo: func(hoje time.Time) (string, error) {
			if dia := hoje.Format("2006-01-02"); dia == diaCache {
				return arqCache, nil
			}
			d := *data
			if d == "" {
				f, err := precoshml.LerFeriados(*feriados)
				if err != nil {
					return "", fmt.Errorf("feriados: %w", err)
				}
				d = precoshml.DiaUtilAnterior(hoje, f).Format("2006-01-02")
			}
			arq, err := precoshml.ArquivoDoDia(*dir, d)
			if err != nil {
				return "", err
			}
			diaCache, arqCache = hoje.Format("2006-01-02"), arq
			return arq, nil
		},
	}
	// carrega já na subida: arquivo ausente ou ilegível derruba aqui, não no primeiro pedido
	if _, err := srv.Atual(srv.Agora()); err != nil {
		log.Fatalf("precos-hml: %v", err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *porta))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("precos-hml %s: ticker %s, adianto %d ms, em http://%s/precos/atual?ativo=%s", versao, *ticker, *adianto, ln.Addr(), precoshml.Conceito)
	log.Fatal((&http.Server{Handler: srv, ReadHeaderTimeout: 5 * time.Second}).Serve(ln))
}

func env(k, padrao string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return padrao
}
