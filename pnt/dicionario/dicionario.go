// Package dicionario embute o dicionário FIX do PnT DSA: a cópia do serviço (dsa_FIX44.xml),
// derivada do dicionário recebido do PnT (Original) por Derivar.
//
// O QuickFIX/Go lê o dicionário de um caminho de arquivo (configuração DataDictionary), então
// Gravar materializa a cópia embutida num diretório. Ver LEIA.md.
package dicionario

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/quickfixgo/quickfix/datadictionary"
)

//go:generate go run ./gerar

//go:embed dsa_FIX44.xml
var XML []byte

// Nome do arquivo gravado por Gravar.
const Nome = "dsa_FIX44.xml"

// Gravar escreve o dicionário embutido em dir e devolve o caminho completo.
// Se o arquivo já existe com o mesmo conteúdo, não reescreve.
func Gravar(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("dicionário: %w", err)
	}
	caminho := filepath.Join(dir, Nome)
	if atual, err := os.ReadFile(caminho); err == nil && bytes.Equal(atual, XML) {
		return caminho, nil
	}
	if err := os.WriteFile(caminho, XML, 0o644); err != nil {
		return "", fmt.Errorf("dicionário: %w", err)
	}
	return caminho, nil
}

// Carregar devolve o dicionário embutido já interpretado.
func Carregar() (*datadictionary.DataDictionary, error) {
	dd, err := datadictionary.ParseSrc(bytes.NewReader(XML))
	if err != nil {
		return nil, fmt.Errorf("dicionário: %w", err)
	}
	return dd, nil
}
