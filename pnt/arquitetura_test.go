// Package pnt_test guarda a regra da camada PnT: ela não importa nada do motor do Kalman.
// Só biblioteca padrão, QuickFIX/Go e a própria camada (pnt/...). Será reaproveitada pelo
// serviço do pair trade, em processo próprio.
package pnt_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulo = "gammaquant.com.br/kalman-ordens/"

func TestCamadaPntNaoImportaOMotor(t *testing.T) {
	err := filepath.WalkDir(".", func(caminho string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(caminho, ".go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), caminho, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(p, modulo) && !strings.HasPrefix(p, modulo+"pnt") {
				t.Errorf("%s importa %s: a camada PnT não pode depender do motor", caminho, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
