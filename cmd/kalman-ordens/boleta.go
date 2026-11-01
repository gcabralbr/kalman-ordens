package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

const usoBoleta = `uso: kalman-ordens boleta <subcomando> [opções]   (v1.31, pelo HTTP local do serviço)

  destino  -uid N -ambiente A [-conta C] [-corretora X]
           o destino do uid naquele ambiente (GET /usuarios/{uid}/destino) e se a conta e a
           corretora esperadas batem
  enviar   -uid N -ambiente A -lado C|V -qtd Q -stop PTS -por UID -motivo X [-preco P] [-conta C]
           [-corretora X] [-op N] [-sim]
           uma ENTRADA do módulo BOLETA pelo POST /ordens: as travas e os limites do uid; o contrato
           é o vigente; -preco = limitada nesse preço (sem ele, a agressiva de sempre); a entrada
           fica viva até executar ou o "cancelar" (sem o timeout da entrada); -op padrão
           MMDDHHMMSS; no REAL pede "sim" (-sim pula)
  estado   -op N [-historico]          GET /ordens/{id}?modulo=BOLETA (e a linha do tempo)
  cancelar -op N -por UID -motivo X    cancela a entrada que ainda não executou (cancelar-entrada)
  encerrar -op N -por UID -motivo X    zera a posição da boleta (encerrar)
`

// cliBoleta (SPEC-ORDENS-v1.31): a boleta do admin, sem o Kalman, pelo mesmo caminho das ordens.
func cliBoleta(cfg config.Config, args []string, entrada io.Reader, saida io.Writer, agora time.Time) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usoBoleta)
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("boleta "+sub, flag.ContinueOnError)
	fs.SetOutput(saida)
	endereco := fs.String("endereco", cfg.Texto("HTTP_ENDERECO", "127.0.0.1:9092"), "HTTP local do serviço")
	uid := fs.Int("uid", 0, "uid do cliente (habilitado no ambiente)")
	amb := fs.String("ambiente", "", "SIMULADOR ou REAL")
	conta := fs.String("conta", "", "a conta esperada na coluna do GQ")
	corretora := fs.String("corretora", "", "a corretora esperada (nome ou código)")
	lado := fs.String("lado", "", "C ou V")
	qtd := fs.Int("qtd", 0, "contratos")
	stop := fs.Int64("stop", 0, "stop em pontos")
	preco := fs.Int64("preco", 0, "preço limite da entrada (pontos); sem ele, a agressiva")
	por := fs.Int("por", 0, "uid do admin")
	motivo := fs.String("motivo", "", "motivo")
	op := fs.Int64("op", 0, "operacao_id da boleta")
	sim := fs.Bool("sim", false, "não pergunta no REAL")
	historico := fs.Bool("historico", false, "inclui a linha do tempo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b := &boletaHTTP{base: "http://" + *endereco, saida: saida}
	switch sub {
	case "destino":
		if *uid <= 0 || *amb == "" {
			return fmt.Errorf("boleta destino exige -uid e -ambiente")
		}
		_, err := b.destino(*uid, *amb, *conta, *corretora)
		return err
	case "enviar":
		if *uid <= 0 || *amb == "" || (*lado != "C" && *lado != "V") || *qtd <= 0 || *stop <= 0 || *por <= 0 || strings.TrimSpace(*motivo) == "" {
			return fmt.Errorf("boleta enviar exige -uid, -ambiente, -lado C|V, -qtd, -stop, -por e -motivo")
		}
		d, err := b.destino(*uid, *amb, *conta, *corretora)
		if err != nil {
			return err
		}
		id := *op
		if id <= 0 {
			id, _ = strconv.ParseInt(agora.In(relogio.Fuso).Format("0102150405"), 10, 64)
		}
		if strings.EqualFold(*amb, dominio.AmbienteReal) && !*sim {
			px := "a mercado (agressiva)"
			if *preco > 0 {
				px = fmt.Sprintf("limitada a %d", *preco)
			}
			fmt.Fprintf(saida, "\nREAL: %s %d no contrato vigente, %s, stop %d pts, uid %d, %v conta %v (boleta %d). Digite sim para enviar: ",
				map[string]string{"C": "COMPRA", "V": "VENDA"}[*lado], *qtd, px, *stop, *uid, d["corretora"], d["conta"], id)
			l, _ := bufio.NewReader(entrada).ReadString('\n')
			if strings.TrimSpace(l) != "sim" {
				return fmt.Errorf("boleta não enviada")
			}
		}
		corpo := map[string]any{"modulo": dominio.ModuloBoleta, "operacao_id": id, "uid": *uid, "papel": "ENTRADA",
			"lado": *lado, "qtd": *qtd, "stop_pts": *stop, "motivo": *motivo, "por": *por,
			"ts_decisao": agora.In(relogio.Fuso).Format("2006-01-02T15:04:05.000-07:00"), "ambiente": strings.ToUpper(*amb)}
		if *conta != "" {
			corpo["conta"] = *conta
		}
		if *corretora != "" {
			corpo["corretora"] = *corretora
		}
		if *preco > 0 {
			corpo["preco"] = *preco
		}
		r, status, err := b.pedir("POST", "/ordens", corpo)
		if err != nil {
			return err
		}
		if status != http.StatusOK || r["aceito"] != true {
			return fmt.Errorf("boleta %d recusada (HTTP %d)", id, status)
		}
		fmt.Fprintf(saida, "boleta %d aceita: acompanhe com  kalman-ordens boleta estado -op %d\n", id, id)
		return nil
	case "estado":
		if *op <= 0 {
			return fmt.Errorf("boleta estado exige -op")
		}
		if _, _, err := b.pedir("GET", fmt.Sprintf("/ordens/%d?modulo=%s", *op, dominio.ModuloBoleta), nil); err != nil {
			return err
		}
		if *historico {
			_, _, err := b.pedir("GET", fmt.Sprintf("/operacoes/%d/historico?modulo=%s", *op, dominio.ModuloBoleta), nil)
			return err
		}
		return nil
	case "cancelar", "encerrar":
		if *op <= 0 || *por <= 0 || strings.TrimSpace(*motivo) == "" {
			return fmt.Errorf("boleta %s exige -op, -por e -motivo", sub)
		}
		rota := map[string]string{"cancelar": "cancelar-entrada", "encerrar": "encerrar"}[sub]
		_, status, err := b.pedir("POST", fmt.Sprintf("/operacoes/%d/%s?modulo=%s", *op, rota, dominio.ModuloBoleta),
			map[string]any{"motivo": *motivo, "por": *por})
		if err == nil && status != http.StatusOK {
			err = fmt.Errorf("HTTP %d", status)
		}
		return err
	}
	return fmt.Errorf("boleta: subcomando desconhecido %q\n\n%s", sub, usoBoleta)
}

type boletaHTTP struct {
	base  string
	saida io.Writer
}

// pedir: o pedido ao serviço; imprime a resposta (JSON indentado) e a devolve.
func (b *boletaHTTP) pedir(metodo, caminho string, corpo any) (map[string]any, int, error) {
	var leitor io.Reader
	if corpo != nil {
		j, _ := json.Marshal(corpo)
		leitor = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(metodo, b.base+caminho, leitor)
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("serviço não respondeu em %s: %w", b.base, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var v map[string]any
	if json.Unmarshal(out, &v) == nil {
		out, _ = json.MarshalIndent(v, "", "  ")
	}
	fmt.Fprintln(b.saida, string(out))
	return v, resp.StatusCode, nil
}

// destino: o /destino do uid no ambiente; erro se ele não pode enviar ou se a conta ou a
// corretora esperadas não batem (o serviço confere de novo na entrada).
func (b *boletaHTTP) destino(uid int, amb, conta, corretora string) (map[string]any, error) {
	amb = strings.ToUpper(amb)
	r, status, err := b.pedir("GET", fmt.Sprintf("/usuarios/%d/destino?ambiente=%s", uid, amb), nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("destino: HTTP %d", status)
	}
	ambs, _ := r["ambientes"].(map[string]any)
	d, _ := ambs[amb].(map[string]any)
	if d == nil {
		return nil, fmt.Errorf("destino: sem o ambiente %s na resposta", amb)
	}
	if d["pode_enviar"] != true {
		return nil, fmt.Errorf("o uid %d não pode enviar no %s: %v %v", uid, amb, d["codigo"], d["motivo"])
	}
	if conta != "" && fmt.Sprint(d["conta"]) != conta {
		return nil, fmt.Errorf("a coluna do GQ do uid %d aponta a conta %v, não %s", uid, d["conta"], conta)
	}
	if corretora != "" && !strings.EqualFold(fmt.Sprint(d["corretora"]), corretora) && fmt.Sprint(d["corretora_codigo"]) != corretora {
		return nil, fmt.Errorf("a coluna do GQ do uid %d aponta a corretora %v (%v), não %s", uid, d["corretora"], d["corretora_codigo"], corretora)
	}
	return d, nil
}
