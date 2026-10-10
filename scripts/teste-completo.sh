#!/usr/bin/env bash
# Critério da F0: a suíte inteira nos dois backends (memória e MariaDB 10.11.15) e, com cgo, -race.
set -euo pipefail
cd "$(dirname "$0")/.."

NOME=kalman-mariadb-teste
PORTA=33306
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
  # o teste de LimparTeste cria (e apaga) um schema não-teste para provar a recusa
  docker exec "$NOME" mariadb -uroot -pteste -e "GRANT ALL ON *.* TO 'kalman'@'%'; FLUSH PRIVILEGES;"
fi
docker exec "$NOME" mariadb -ukalman -pkalman -N -e 'select version()' kalmanordens_teste

export KALMAN_TESTE_MARIADB="kalman:kalman@tcp(127.0.0.1:$PORTA)/kalmanordens_teste"
RACE=""
if [ "$(go env CGO_ENABLED)" = "1" ] && command -v gcc >/dev/null 2>&1; then RACE="-race"; fi
echo "go test ${RACE:-(sem -race: sem cgo)} ./..."
go vet ./...
go test $RACE -count=1 ./...
