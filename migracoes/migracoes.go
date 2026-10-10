// Package migracoes embute o DDL do serviço (NNNN_nome.sql, aplicados em ordem).
package migracoes

import (
	"embed"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var arquivos embed.FS

// Migracao é um arquivo de DDL.
type Migracao struct {
	Versao     int
	Nome       string
	Instrucoes []string
}

// Todas devolve as migrações em ordem de versão.
func Todas() ([]Migracao, error) {
	nomes, err := fs.Glob(arquivos, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(nomes)
	var r []Migracao
	for _, n := range nomes {
		v, err := strconv.Atoi(strings.SplitN(n, "_", 2)[0])
		if err != nil {
			continue
		}
		b, err := arquivos.ReadFile(n)
		if err != nil {
			return nil, err
		}
		r = append(r, Migracao{Versao: v, Nome: n, Instrucoes: instrucoes(string(b))})
	}
	return r, nil
}

// instrucoes separa por ';' no fim da linha, sem comentários (o DDL não tem procedures).
func instrucoes(s string) []string {
	var r []string
	var cur strings.Builder
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "--") {
			continue
		}
		cur.WriteString(l)
		cur.WriteString("\n")
		if strings.HasSuffix(t, ";") {
			r = append(r, strings.TrimSuffix(strings.TrimSpace(cur.String()), ";"))
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		r = append(r, strings.TrimSpace(cur.String()))
	}
	return r
}
