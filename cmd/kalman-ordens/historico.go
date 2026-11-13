package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/motor"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// cliHistorico: o mesmo conteúdo das rotas de histórico da tela de admin (SPEC v1.5 R), em JSON,
// direto do banco (funciona com o serviço parado). Como a CLI usuario, lê o DB_DSN do ambiente ou
// do service.env (-env).
//
//	kalman-ordens historico operacao -id N [-modulo M] [-cru]   GET /operacoes/{id}/historico
//	kalman-ordens historico dia [-dia D] [-uid N] [-modulo M]   GET /operacoes?dia=
//	kalman-ordens historico sessao [-dia D] [-ambiente A] [-conector C]   GET /sessao/eventos?dia=&ambiente=&conector=
func cliHistorico(cfg config.Config, args []string, saida io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("historico operacao | dia | sessao")
	}
	acao, args := args[0], args[1:]
	fs := flag.NewFlagSet("historico "+acao, flag.ContinueOnError)
	id := fs.Int64("id", 0, "operacao_id")
	modulo := fs.String("modulo", "", "módulo (operacao: ausente = KALMAN; dia: ausente = todos)")
	cru := fs.Bool("cru", false, "inclui a mensagem FIX inteira (senha mascarada)")
	dia := fs.String("dia", time.Now().In(relogio.Fuso).Format("2006-01-02"), "dia do pregão AAAA-MM-DD")
	uid := fs.Int("uid", 0, "uid (ausente = todos)")
	ambiente := fs.String("ambiente", "", "sessao: SIMULADOR ou REAL (ausente = todos)")
	conector := fs.String("conector", "", "sessao: PNT, FLEXSCAN ou SERVICO (ausente = todos)")
	arqEnv := fs.String("env", "/etc/kalman-ordens/service.env", "service.env, se DB_DSN não estiver no ambiente")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, _, err := abrirBancoCLI(cfg, *arqEnv)
	if err != nil {
		return err
	}
	defer r.Fechar()
	if err := schemaEmDia(r); err != nil {
		return err
	}
	var v any
	switch acao {
	case "operacao":
		if *id <= 0 {
			return fmt.Errorf("historico operacao exige -id")
		}
		v, err = motor.HistoricoOperacao(r, *modulo, *id, *cru)
	case "dia":
		v, err = motor.OperacoesDoDia(r, *dia, *uid, *modulo, "")
	case "sessao":
		amb := ""
		if *ambiente != "" {
			if amb, err = dominio.LerAmbiente(*ambiente); err != nil {
				return err
			}
		}
		con, ok := motor.ConectorDaQuery(*conector, true)
		if !ok {
			return fmt.Errorf("-conector: PNT, FLEXSCAN ou SERVICO (veio %q)", *conector)
		}
		v, err = motor.EventosDaSessaoFiltrados(r, *dia, amb, con)
	default:
		return fmt.Errorf("historico operacao | dia | sessao (veio %q)", acao)
	}
	if err != nil {
		return err
	}
	enc := json.NewEncoder(saida)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// schemaEmDia: a CLI não migra sozinha; com o schema atrás do binário, recusa.
func schemaEmDia(r *repo.MariaDB) error {
	pend, err := r.MigracoesPendentes()
	if err != nil {
		return err
	}
	if len(pend) == 0 {
		return nil
	}
	var nomes []string
	for _, m := range pend {
		nomes = append(nomes, m.Nome)
	}
	return fmt.Errorf("schema do banco atrás do binário (%d migração(ões) pendente(s): %s): rode kalman-ordens migrar",
		len(pend), strings.Join(nomes, ", "))
}
