#!/usr/bin/env bash
# Critério da F0: a suíte inteira nos dois backends (memória e MariaDB 10.11.15) e com -race.
#
#   scripts/teste-completo.sh            go local (com -race se houver cgo)
#   scripts/teste-completo.sh --race     dentro de golang:1.24-bookworm (Debian, tem cgo), sem
#                                        instalar compilador nenhum na máquina
set -euo pipefail
cd "$(dirname "$0")/.."

NOME=kalman-mariadb-teste
PORTA=33306
REDE=kalman-teste
if ! docker ps --format '{{.Names}}' | grep -qx "$NOME"; then
  docker rm -f "$NOME" >/dev/null 2>&1 || true
  docker run -d --name "$NOME" \
    -e MARIADB_ROOT_PASSWORD=teste -e MARIADB_DATABASE=kalmanordens_teste \
    -e MARIADB_USER=kalman -e MARIADB_PASSWORD=kalman \
    -p 127.0.0.1:$PORTA:3306 mariadb:10.11.15 >/dev/null
  for _ in $(seq 1 60); do
    docker exec "$NOME" mariadb -ukalman -pkalman -e 'select 1' kalmanordens_teste >/dev/null 2>&1 && break
    sleep 2
  done
  # testes que provam recusas (LimparTeste fora de *_teste; sql_mode global diferente) precisam
  # criar schema e mudar o global: só no container de teste
  docker exec "$NOME" mariadb -uroot -pteste -e "GRANT ALL ON *.* TO 'kalman'@'%'; FLUSH PRIVILEGES;"
fi
docker exec "$NOME" mariadb -ukalman -pkalman -N -e 'select version(), @@GLOBAL.sql_mode' kalmanordens_teste

if [ "${1:-}" = "--race" ]; then
  docker network inspect "$REDE" >/dev/null 2>&1 || docker network create "$REDE" >/dev/null
  docker network connect "$REDE" "$NOME" 2>/dev/null || true
  docker volume create kalman-gomod >/dev/null
  echo "go test -race ./... em golang:1.24-bookworm"
  MSYS_NO_PATHCONV=1 docker run --rm --network "$REDE" -v "$(pwd -W 2>/dev/null || pwd):/src" -v kalman-gomod:/go/pkg/mod \
    -w /src -e CGO_ENABLED=1 -e "KALMAN_TESTE_MARIADB=kalman:kalman@tcp($NOME:3306)/kalmanordens_teste" \
    golang:1.24-bookworm bash -c 'go version && go vet ./... && go test -race -count=1 ./...'
  exit
fi

export KALMAN_TESTE_MARIADB="kalman:kalman@tcp(127.0.0.1:$PORTA)/kalmanordens_teste"
RACE=""
if [ "$(go env CGO_ENABLED)" = "1" ] && command -v gcc >/dev/null 2>&1; then RACE="-race"; fi
echo "go test ${RACE:-(sem -race: sem cgo; use --race)} ./..."
go vet ./...
go test $RACE -count=1 ./...
