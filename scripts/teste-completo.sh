#!/usr/bin/env bash
# Critério da F0: a suíte inteira nos dois backends (memória e MariaDB 10.11) e com -race.
#
#   scripts/teste-completo.sh                    nativo: go local + MariaDB local, com -race (cgo + gcc)
#   scripts/teste-completo.sh --sem-race         nativo, sem -race
#   scripts/teste-completo.sh --container        Docker: mariadb:10.11.15 em container, go local
#   scripts/teste-completo.sh --container --race Docker: o mesmo com -race em golang:1.24-bookworm
#
# Nativo (a .53): schema kalmanordens_teste no MariaDB local, usuário de teste com GRANT só nesse
# schema (deploy/LEIA.md seção 4). DSN em KALMAN_TESTE_MARIADB ou, se vazio, no arquivo
# ~/.config/kalman-ordens/teste-mariadb.dsn (600, fora do repositório). Sem privilégio global, os
# dois testes que provam recusas (LimparTeste fora de *_teste; sql_mode global diferente) se pulam:
# o MariaDB da .53 é compartilhado e o sql_mode global não pode ser mexido. Eles rodam no modo
# container.
set -euo pipefail
cd "$(dirname "$0")/.."

MODO=nativo
RACE_PEDIDO=auto
for a in "$@"; do
  case "$a" in
    --container) MODO=container ;;
    --race) RACE_PEDIDO=sim ;;
    --sem-race) RACE_PEDIDO=nao ;;
    *) echo "uso: $0 [--sem-race] | --container [--race]" >&2; exit 2 ;;
  esac
done

if [ "$MODO" = nativo ]; then
  ARQ_DSN="${XDG_CONFIG_HOME:-$HOME/.config}/kalman-ordens/teste-mariadb.dsn"
  if [ -z "${KALMAN_TESTE_MARIADB:-}" ]; then
    [ -r "$ARQ_DSN" ] || { echo "sem KALMAN_TESTE_MARIADB e sem $ARQ_DSN (deploy/LEIA.md seção 4)" >&2; exit 1; }
    KALMAN_TESTE_MARIADB="$(tr -d '[:space:]' < "$ARQ_DSN")"
  fi
  export KALMAN_TESTE_MARIADB
  # usuario:senha@tcp(host:porta)/schema[?params]
  if [[ ! "$KALMAN_TESTE_MARIADB" =~ ^([^:]+):(.*)@tcp\(([^:\)]+):([0-9]+)\)/([^?]+) ]]; then
    echo "DSN fora do formato usuario:senha@tcp(host:porta)/schema" >&2; exit 1
  fi
  BD_USUARIO="${BASH_REMATCH[1]}" BD_SENHA="${BASH_REMATCH[2]}" BD_HOST="${BASH_REMATCH[3]}"
  BD_PORTA="${BASH_REMATCH[4]}" BD_SCHEMA="${BASH_REMATCH[5]}"
  # os testes apagam as tabelas do schema: nunca fora de *_teste
  [[ "$BD_SCHEMA" == *_teste ]] || { echo "schema $BD_SCHEMA não termina em _teste" >&2; exit 1; }
  MYSQL_PWD="$BD_SENHA" mariadb -h"$BD_HOST" -P"$BD_PORTA" -u"$BD_USUARIO" -N \
    -e 'select version(), @@GLOBAL.sql_mode, current_user()' "$BD_SCHEMA"

  RACE=""
  case "$RACE_PEDIDO" in
    nao) ;;
    sim|auto)
      if [ "$(go env CGO_ENABLED)" = "1" ] && command -v "$(go env CC)" >/dev/null 2>&1; then
        RACE="-race"
      elif [ "$RACE_PEDIDO" = sim ]; then
        echo "-race pedido sem cgo/compilador C" >&2; exit 1
      fi ;;
  esac
  go version
  echo "go test ${RACE:-(sem -race)} ./... (memória e MariaDB $BD_HOST:$BD_PORTA/$BD_SCHEMA)"
  go vet ./...
  # -p 1: cmd, cenario e repo usam o mesmo schema de teste (cada um limpa as tabelas ao começar)
  go test $RACE -p 1 -count=1 ./...
  exit
fi

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

if [ "$RACE_PEDIDO" = sim ]; then
  docker network inspect "$REDE" >/dev/null 2>&1 || docker network create "$REDE" >/dev/null
  docker network connect "$REDE" "$NOME" 2>/dev/null || true
  docker volume create kalman-gomod >/dev/null
  echo "go test -race ./... em golang:1.24-bookworm"
  MSYS_NO_PATHCONV=1 docker run --rm --network "$REDE" -v "$(pwd -W 2>/dev/null || pwd):/src" -v kalman-gomod:/go/pkg/mod \
    -w /src -e CGO_ENABLED=1 -e "KALMAN_TESTE_MARIADB=kalman:kalman@tcp($NOME:3306)/kalmanordens_teste" \
    golang:1.24-bookworm bash -c 'go version && go vet ./... && go test -race -p 1 -count=1 ./...'
  exit
fi

export KALMAN_TESTE_MARIADB="kalman:kalman@tcp(127.0.0.1:$PORTA)/kalmanordens_teste"
RACE=""
if [ "$RACE_PEDIDO" != nao ] && [ "$(go env CGO_ENABLED)" = "1" ] && command -v gcc >/dev/null 2>&1; then RACE="-race"; fi
echo "go test ${RACE:-(sem -race: sem cgo; use --container --race)} ./..."
go vet ./...
# -p 1: cmd, cenario e repo usam o mesmo schema de teste (cada um limpa as tabelas ao começar)
go test $RACE -p 1 -count=1 ./...
