#!/usr/bin/env bash
# Cópia diária do log FIX compactado para fora da máquina (v1.6 E3): na .51 (produção), para a
# .53. Só os arquivos já rotacionados e compactados (*.gz, deploy/logrotate-kalman-ordens); o
# destino espelha os 90 dias da origem (--delete: o que o logrotate apagou na origem sai do destino).
# Protege contra a perda do disco da .51; não contra apagar na .51.
#
# Configuração em /etc/kalman-ordens/copia-log.env (deploy/copia-log.env.example). Rodado pela
# unidade kalman-ordens-copia-log (timer diário) como o usuário kalman-ordens (o único que lê o
# diretório do log). COPIA_DESTINO sem ":" = diretório local (testes).
set -euo pipefail

ORIGEM="${COPIA_ORIGEM:-/var/lib/kalman-ordens/fix/log}"
DESTINO="${COPIA_DESTINO:?COPIA_DESTINO vazio (ex.: kalmancopia@172.16.110.53:)}"
CHAVE="${COPIA_CHAVE:-/etc/kalman-ordens/copia-log_ed25519}"
HOSTS="${COPIA_KNOWN_HOSTS:-/etc/kalman-ordens/copia-log_known_hosts}"

[ -d "$ORIGEM" ] || { echo "origem $ORIGEM não existe" >&2; exit 1; }

opcoes=(-a --delete --include='*.gz' --exclude='*' --itemize-changes)
if [[ "$DESTINO" == *:* ]]; then
  # chave própria, host conhecido obrigatório; no destino a chave só roda rrsync num diretório
  opcoes+=(-e "ssh -i $CHAVE -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$HOSTS")
fi
echo "copia-log-fix: $ORIGEM/*.gz -> $DESTINO"
rsync "${opcoes[@]}" "$ORIGEM/" "$DESTINO"
echo "copia-log-fix: ok ($(find "$ORIGEM" -maxdepth 1 -name '*.gz' | wc -l) arquivo(s) na origem)"
