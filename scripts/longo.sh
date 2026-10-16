#!/bin/bash
# Roda um comando longo (suíte, teste de carga) como unidade do systemd do usuário: sobrevive à
# queda da sessão SSH (o usuário tem linger). A saída vai para var/NOME-AAAAMMDD-HHMMSS.log, que
# termina com "FIM rc=N".
#
#   scripts/longo.sh suite scripts/teste-completo.sh
#   scripts/longo.sh carga env KALMAN_CARGA=4000 ... go test -count=1 -run TestCarga -timeout 60m ./teste/carga
#
# Acompanhar: tail -f var/<arquivo>.log   Parar: systemctl --user stop kalman-ordens-<nome>-<hora>
set -euo pipefail
[ $# -ge 2 ] || { echo "uso: $0 NOME comando [args...]" >&2; exit 2; }
nome=$1
shift
raiz=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$raiz/var"
quando=$(date +%Y%m%d-%H%M%S)
arq="$raiz/var/$nome-$quando.log"
unidade="kalman-ordens-$nome-${quando#*-}"
cmd=$(printf '%q ' "$@")
systemd-run --user --quiet --unit="$unidade" --working-directory="$raiz" \
  --setenv=PATH="$PATH" --setenv=HOME="$HOME" \
  /bin/bash -c "$cmd > '$arq' 2>&1; echo \"FIM rc=\$?\" >> '$arq'"
echo "unidade: $unidade"
echo "log:     $arq"
