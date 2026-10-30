package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
)

// cliOperacao (v1.28 §3): operacao confirmar — a posição na corretora da operação suspensa (o
// conector caiu com posição), pelo HTTP local do serviço: POST /operacoes/{id}/confirmar-posicao.
func cliOperacao(cfg config.Config, args []string, saida io.Writer) error {
	if len(args) == 0 || args[0] != "confirmar" {
		return fmt.Errorf("uso: kalman-ordens operacao confirmar -op N -posicao P -motivo X -por UID [-modulo M]")
	}
	fs := flag.NewFlagSet("operacao confirmar", flag.ContinueOnError)
	op := fs.Int64("op", 0, "operacao_id (obrigatório)")
	posicao := fs.String("posicao", "", "a posição na corretora, com sinal: 1 comprado, -1 vendido, 0 zerada (obrigatória)")
	motivo := fs.String("motivo", "", "motivo (obrigatório)")
	por := fs.Int("por", 0, "uid do admin (obrigatório)")
	modulo := fs.String("modulo", "", "módulo da operação (vazio = KALMAN)")
	endereco := fs.String("endereco", cfg.Texto("HTTP_ENDERECO", "127.0.0.1:9092"), "HTTP local do serviço")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	var p int
	if _, err := fmt.Sscan(*posicao, &p); err != nil || *op <= 0 || *motivo == "" || *por <= 0 {
		return fmt.Errorf("operacao confirmar exige -op, -posicao (número com sinal), -motivo e -por")
	}
	b, _ := json.Marshal(map[string]any{"posicao": p, "motivo": *motivo, "por": *por})
	u := fmt.Sprintf("http://%s/operacoes/%d/confirmar-posicao", *endereco, *op)
	if *modulo != "" {
		u += "?modulo=" + url.QueryEscape(*modulo)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Post(u, "application/json", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("serviço não respondeu em %s: %w", *endereco, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var v any
	if json.Unmarshal(out, &v) == nil {
		out, _ = json.MarshalIndent(v, "", "  ")
	}
	fmt.Fprintln(saida, string(out))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
