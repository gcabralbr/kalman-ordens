package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// mesesMinimo: abaixo disso o expurgo recusa (proteção contra um prazo digitado errado).
const mesesMinimo = 3

// cliExpurgar (v1.6 E1): apaga as mensagens FIX, os eventos publicados, os eventos da sessão e os
// pedidos HTTP com mais de EXPURGO_MESES (padrão 12) — contados até o início do dia, no fuso do
// pregão. O resumo das operações e ordens fica. Usa o usuário de banco do expurgo
// (EXPURGO_DB_DSN, DELETE só nessas tabelas), nunca o DB_DSN do serviço. Rodado pelo timer mensal
// kalman-ordens-expurgo (deploy/), que passa o /etc/kalman-ordens/expurgo.env no ambiente.
//
//	kalman-ordens expurgar [-meses N] [-simular] [-env /etc/kalman-ordens/expurgo.env]
func cliExpurgar(cfg config.Config, args []string, saida io.Writer, agora time.Time) error {
	fs := flag.NewFlagSet("expurgar", flag.ContinueOnError)
	meses := fs.Int("meses", 0, "retenção em meses (padrão: EXPURGO_MESES, senão 12)")
	simular := fs.Bool("simular", false, "só conta o que seria apagado")
	arqEnv := fs.String("env", "/etc/kalman-ordens/expurgo.env", "arquivo com EXPURGO_DB_DSN, se não estiver no ambiente")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfg.Texto("EXPURGO_DB_DSN", "") == "" {
		c, err := lerServiceEnv(*arqEnv, cfg)
		if err != nil {
			return fmt.Errorf("EXPURGO_DB_DSN: %w", err)
		}
		cfg = c
	}
	dsn := cfg.Texto("EXPURGO_DB_DSN", "")
	if dsn == "" {
		return fmt.Errorf("EXPURGO_DB_DSN vazio (o expurgo usa um usuário de banco próprio, nunca o DB_DSN do serviço)")
	}
	if *meses == 0 {
		*meses = 12
		if v := cfg.Texto("EXPURGO_MESES", ""); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("EXPURGO_MESES %q", v)
			}
			*meses = n
		}
	}
	if *meses < mesesMinimo {
		return fmt.Errorf("retenção de %d meses recusada (mínimo %d)", *meses, mesesMinimo)
	}
	r, err := repo.AbrirMariaDB(dsn)
	if err != nil {
		return err
	}
	defer r.Fechar()
	if err := schemaEmDia(r); err != nil {
		return err
	}
	hoje := agora.In(relogio.Fuso)
	antes := time.Date(hoje.Year(), hoje.Month(), hoje.Day(), 0, 0, 0, 0, relogio.Fuso).AddDate(0, -*meses, 0)
	n, err := r.Expurgar(antes, 5000, *simular)
	verbo := "apagadas"
	if *simular {
		verbo = "a apagar (simulação)"
	}
	for _, t := range repo.TabelasExpurgo {
		if v, ok := n[t]; ok {
			fmt.Fprintf(saida, "%s: %d linha(s) %s, anteriores a %s (%d meses)\n", t, v, verbo, antes.Format("2006-01-02"), *meses)
		}
	}
	return err
}
