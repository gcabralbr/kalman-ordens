// Comando gerar regenera a cópia do serviço (dsa_FIX44.xml) a partir do dicionário do PnT:
// `go generate ./pnt/dicionario`. Ver ../LEIA.md.
package main

import (
	"log"
	"os"

	"gammaquant.com.br/kalman-ordens/pnt/dicionario"
)

func main() {
	pnt, err := os.ReadFile(dicionario.Original)
	if err != nil {
		log.Fatal(err)
	}
	copia, err := dicionario.Derivar(pnt)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(dicionario.Nome, copia, 0o644); err != nil {
		log.Fatal(err)
	}
}
