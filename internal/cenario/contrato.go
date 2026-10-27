package cenario

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// contrato ARQUIVO SECAO [caminho] (27/10, v1.21): a última resposta HTTP tem a mesma forma do
// exemplo JSON do contrato — o primeiro bloco ```json depois do título "### SECAO" do ARQUIVO
// (relativo à raiz do repositório). caminho (ex.: flexscan) desce na resposta antes de comparar.
// A forma: as mesmas chaves em cada objeto, nos dois sentidos; numa lista, todo item da resposta
// com as chaves dos itens do exemplo; o tipo JSON igual quando os dois lados não são null. Assim o
// exemplo que o Kalman lê não diverge do serviço (a §4.1 da v1.21 divergiu em 27/10).
func (e *Executor) contrato(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("contrato ARQUIVO SECAO [caminho]")
	}
	exemplo, err := exemploDoContrato(filepath.Join("..", "..", args[0]), args[1])
	if err != nil {
		return err
	}
	var real any = e.resposta
	if len(args) > 2 {
		for _, k := range strings.Split(args[2], ".") {
			m, ok := real.(map[string]any)
			if !ok {
				return fmt.Errorf("contrato: %q não é objeto na resposta", k)
			}
			real = m[k]
		}
	} else {
		r := map[string]any{}
		for k, v := range e.resposta {
			if k != "_status" {
				r[k] = v
			}
		}
		real = r
	}
	// a resposta foi lida com UseNumber; o exemplo, com float64: normaliza os dois
	b, _ := json.Marshal(real)
	_ = json.Unmarshal(b, &real)
	if difs := compararForma("", exemplo, real); len(difs) > 0 {
		return fmt.Errorf("contrato %s §%s diverge da resposta: %s", args[0], args[1], strings.Join(difs, "; "))
	}
	return nil
}

func exemploDoContrato(arquivo, secao string) (any, error) {
	f, err := os.Open(arquivo)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	achou, dentro := false, false
	var bloco strings.Builder
	for s.Scan() {
		l := s.Text()
		switch {
		case !achou:
			achou = strings.HasPrefix(l, "### "+secao+" ") || l == "### "+secao
		case !dentro && strings.HasPrefix(l, "### "):
			return nil, fmt.Errorf("contrato: §%s sem bloco ```json", secao)
		case !dentro && strings.TrimSpace(l) == "```json":
			dentro = true
		case dentro && strings.TrimSpace(l) == "```":
			var v any
			if err := json.Unmarshal([]byte(bloco.String()), &v); err != nil {
				return nil, fmt.Errorf("contrato: o exemplo da §%s não é JSON: %v", secao, err)
			}
			return v, nil
		case dentro:
			bloco.WriteString(l + "\n")
		}
	}
	return nil, fmt.Errorf("contrato: §%s não encontrada em %s", secao, arquivo)
}

func compararForma(onde string, ex, real any) []string {
	if ex == nil || real == nil {
		return nil
	}
	switch x := ex.(type) {
	case map[string]any:
		r, ok := real.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: objeto no contrato, %T na resposta", nome(onde), real)}
		}
		var difs []string
		for _, k := range chaves(x) {
			if _, ok := r[k]; !ok {
				difs = append(difs, fmt.Sprintf("%s.%s: no contrato, ausente na resposta", nome(onde), k))
				continue
			}
			difs = append(difs, compararForma(onde+"."+k, x[k], r[k])...)
		}
		for _, k := range chaves(r) {
			if _, ok := x[k]; !ok {
				difs = append(difs, fmt.Sprintf("%s.%s: na resposta, ausente no contrato", nome(onde), k))
			}
		}
		return difs
	case []any:
		r, ok := real.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: lista no contrato, %T na resposta", nome(onde), real)}
		}
		if len(x) == 0 || len(r) == 0 {
			return nil
		}
		var difs []string
		for i, item := range r {
			difs = append(difs, compararForma(fmt.Sprintf("%s[%d]", onde, i), x[0], item)...)
			if len(difs) > 0 {
				return difs // um item basta para mostrar a divergência
			}
		}
		return nil
	}
	if fmt.Sprintf("%T", ex) != fmt.Sprintf("%T", real) {
		return []string{fmt.Sprintf("%s: %T no contrato, %T na resposta", nome(onde), ex, real)}
	}
	return nil
}

func chaves(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func nome(onde string) string {
	if onde == "" {
		return "(raiz)"
	}
	return strings.TrimPrefix(onde, ".")
}
