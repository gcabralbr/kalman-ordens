#!/bin/bash
# Retrato das migrações da versão instalada na .53, para o teste de ATUALIZAÇÃO (TestServirAtualizaDoInstalado):
# o binário novo tem de subir em cima do banco que a versão instalada deixou (23/10: o eb65ec2 só foi
# testado com o banco vazio e não subiu na .53). Rodar depois de cada instalação e commitar.
#
#   scripts/instalado-migracoes.sh            # o commit de /opt/kalman-ordens/kalman-ordens versao
#   scripts/instalado-migracoes.sh 7755090    # um commit dado
set -euo pipefail
cd "$(dirname "$0")/.."
commit=${1:-$(/opt/kalman-ordens/kalman-ordens versao)}
commit=${commit:0:12}
dest=cmd/kalman-ordens/testdata/instalado
rm -rf "$dest" && mkdir -p "$dest"
for f in $(git ls-tree --name-only "$commit" migracoes/ | grep '\.sql$'); do
  git show "$commit:$f" > "$dest/$(basename "$f")"
done
echo "$commit" > "$dest/VERSAO"
echo "instalado: $commit ($(ls "$dest"/*.sql | wc -l) migrações) em $dest"
