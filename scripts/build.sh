#!/usr/bin/env bash
# Binários estáticos linux/amd64 (kalman-ordens e a ferramenta de teste precos-hml) com o commit
# embutido (aparece no /saude, no log ao subir e em `kalman-ordens versao`). Árvore com alterações não commitadas → sufixo -sujo.
#
#   scripts/build.sh               go do sistema (mínimo 1.24; a .53 tem 1.26)
#   scripts/build.sh --container   dentro de golang:1.24-bookworm (nada instalado na máquina)
#
# Deploy: git clone/pull no servidor + scripts/build.sh + instalar dist/kalman-ordens
# (deploy/LEIA.md).
set -euo pipefail
cd "$(dirname "$0")/.."

COMMIT=$(git rev-parse --short=12 HEAD)
if [ -n "$(git status --porcelain)" ]; then COMMIT="$COMMIT-sujo"; fi
mkdir -p dist
LDFLAGS="-s -w -X main.versao=$COMMIT"

if [ "${1:-}" = "--container" ]; then
  docker volume create kalman-gomod >/dev/null
  MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W 2>/dev/null || pwd):/src" -v kalman-gomod:/go/pkg/mod -w /src \
    -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=amd64 golang:1.24-bookworm \
    bash -c "go build -buildvcs=false -trimpath -ldflags '$LDFLAGS' -o dist/kalman-ordens ./cmd/kalman-ordens &&
             go build -buildvcs=false -trimpath -ldflags '$LDFLAGS' -o dist/precos-hml ./cmd/precos-hml"
else
  V=$(go env GOVERSION | sed 's/^go//')
  MAIOR=${V%%.*}; RESTO=${V#*.}; MENOR=${RESTO%%.*}
  if [ "$MAIOR" -lt 1 ] || { [ "$MAIOR" -eq 1 ] && [ "$MENOR" -lt 24 ]; }; then
    echo "Go $V: o mínimo é 1.24 (use --container)" >&2
    exit 1
  fi
  for c in kalman-ordens precos-hml; do
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
      go build -buildvcs=false -trimpath -ldflags "$LDFLAGS" -o dist/$c ./cmd/$c
  done
fi

echo "dist/kalman-ordens, dist/precos-hml (ferramenta de teste da HML) — versão $COMMIT"
if command -v sha256sum >/dev/null; then sha256sum dist/kalman-ordens dist/precos-hml; fi
