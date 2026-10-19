package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
)

// cliQuarentena (v1.13): revisar e resolver as linhas que o banco recusou por erro de dado. Com linha
// pendente, o servir sobe em PARADA; resolvidas todas, o POST /retomar (ou `kalman-ordens retomar`)
// libera. Resolver não regrava a linha: registra que alguém a revisou (e o que fez, no -motivo) —
// o estado real vem da conciliação com o PnT.
//
//	kalman-ordens quarentena listar
//	kalman-ordens quarentena resolver -id N -por UID -motivo X
func cliQuarentena(cfg config.Config, args []string, saida io.Writer) error {
	if len(args) == 0 {
		return errors.New("uso: quarentena listar | quarentena resolver -id N -por UID -motivo X")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("quarentena "+sub, flag.ContinueOnError)
	arqEnv := fs.String("env", "/etc/kalman-ordens/service.env", "service.env, se DB_DSN não estiver no ambiente")
	id := fs.Int64("id", 0, "id da linha (listar)")
	por := fs.Int("por", 0, "uid de quem resolve")
	motivo := fs.String("motivo", "", "o que foi revisado/feito")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, _, err := abrirBancoCLI(cfg, *arqEnv)
	if err != nil {
		return err
	}
	defer r.Fechar()
	switch sub {
	case "listar":
		qs, err := r.QuarentenaPendente()
		if err != nil {
			return err
		}
		if len(qs) == 0 {
			fmt.Fprintln(saida, "nenhuma linha pendente na quarentena")
			return nil
		}
		enc := json.NewEncoder(saida)
		enc.SetIndent("", "  ")
		return enc.Encode(qs)
	case "resolver":
		if *id <= 0 || *por <= 0 || strings.TrimSpace(*motivo) == "" {
			return errors.New("resolver exige -id, -por (uid) e -motivo")
		}
		ok, err := r.ResolverQuarentena(*id, fmt.Sprintf("cli:%d", *por), *motivo, time.Now())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("linha %d não existe ou já foi resolvida", *id)
		}
		qs, err := r.QuarentenaPendente()
		if err != nil {
			return err
		}
		fmt.Fprintf(saida, "linha %d resolvida; pendentes: %d", *id, len(qs))
		if len(qs) == 0 {
			fmt.Fprint(saida, " — a parada da quarentena sai com `kalman-ordens retomar -motivo X` (POST /retomar)")
		}
		fmt.Fprintln(saida)
		return nil
	}
	return fmt.Errorf("quarentena: subcomando %q (listar | resolver)", sub)
}
