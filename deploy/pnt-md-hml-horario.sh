#!/usr/bin/env bash
# Horário do kalman-pnt-md-hml na .53 (só serve à kalman-hml). Dois modos:
#
#   pregao  ExecCondition do kalman-pnt-md-hml-inicio (timer de dias úteis, 08:40): 0 = hoje tem pregão
#           (sobe), 1 = fim de semana ou feriado do /var/lib/kalman/feriados.json (gerado pelo Kalman
#           da anferiado; pula). Sem o arquivo, ou ilegível, sobe do mesmo jeito.
#   parar   kalman-pnt-md-hml-parada (timer de dias úteis, 14:35): para o pnt-md-hml só com a conta
#           zerada no /saude do kalman-ordens (sessão logada, sem conciliação nem divergência, posição
#           0, nenhuma ordem KM viva); senão alerta no journal e tenta de novo a cada PARADA_INTERVALO_S
#           (300) até PARADA_ATE (18:25); passou disso, alerta crítico e desiste (o pnt-md-hml continua
#           ligado; sai com 1). Parado, compacta as gravações (var/pnt-md-hml-*.jsonl → .jsonl.gz).
#
# Alertas: no journal com a prioridade (prefixo <N>); o crítico também no Telegram do
# /etc/kalman-ordens/disco.env (o chat da .53; vazio = só no journal). Textos em ASCII.
# Testes: scripts/teste-pnt-md-hml-horario.sh (PARADA_SYSTEMCTL, PARADA_AGORA_HHMM, HOJE e os
# caminhos abaixo trocam o mundo de verdade).
set -euo pipefail

SAUDE="${PARADA_SAUDE_URL:-http://127.0.0.1:9092/saude}"
UNIDADE="${PARADA_UNIDADE:-kalman-pnt-md-hml.service}"
INTERVALO="${PARADA_INTERVALO_S:-300}"
ATE="${PARADA_ATE:-18:25}"
VAR="${PARADA_VAR:-/home/gammaquant/kalman-ordens/var}"
FERIADOS="${PARADA_FERIADOS:-/var/lib/kalman/feriados.json}"
SYSTEMCTL="${PARADA_SYSTEMCTL:-systemctl}"
ROTULO="${DISCO_ROTULO:-$(hostname -s)}"
URL="${TELEGRAM_URL:-https://api.telegram.org}"

log() { echo "<$1>$2"; }  # prioridade do syslog (2 crítico, 4 aviso, 6 info): o journald lê o prefixo

telegram() {
  if [ -z "${TELEGRAM_TOKEN:-}" ] || [ -z "${TELEGRAM_CHAT:-}" ]; then
    log 4 "telegram sem token/chat; alerta so no log"
    return 0
  fi
  curl -fsS -m 10 -o /dev/null "$URL/bot$TELEGRAM_TOKEN/sendMessage" \
    --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$1" 2>/dev/null \
    || log 4 "telegram: falha de envio"
}

pregao() {
  local hoje="${HOJE:-$(date +%F)}"
  if [ "$(date -d "$hoje" +%u)" -ge 6 ]; then log 6 "pnt-md-hml: $hoje e fim de semana; nao sobe"; return 1; fi
  if [ ! -r "$FERIADOS" ]; then log 4 "pnt-md-hml: sem $FERIADOS; sobe sem conferir feriado"; return 0; fi
  local nome
  if ! nome=$(jq -er --arg d "$hoje" '.[$d] // empty' "$FERIADOS" 2>/dev/null); then
    if jq -e . "$FERIADOS" > /dev/null 2>&1; then return 0; fi
    log 4 "pnt-md-hml: $FERIADOS ilegivel; sobe sem conferir feriado"; return 0
  fi
  log 6 "pnt-md-hml: $hoje e feriado ($nome); nao sobe"
  return 1
}

# motivo de não estar zerado (vazio = zerado), do /saude do kalman-ordens
nao_zerado() {
  local s
  if ! s=$(curl -fsS -m 5 "$SAUDE" 2>/dev/null); then echo "kalman-ordens sem resposta em $SAUDE"; return; fi
  jq -r '
    if (.sessao_logada != true) then "sessao FIX nao logada"
    elif (.conciliando != false) then "conciliando"
    elif (.divergente != false) then "divergente"
    elif (.posicao != 0) then "posicao \(.posicao)"
    elif ((.ordens_vivas // []) | length) != 0 then "\((.ordens_vivas | length)) ordem(ns) KM viva(s)"
    else "" end' <<<"$s" 2>/dev/null || echo "resposta ilegivel de $SAUDE"
}

compactar() {
  local f falhou=0
  shopt -s nullglob
  for f in "$VAR"/pnt-md-hml-*.jsonl; do
    if [ -e "$f.gz" ]; then log 4 "gravacao: $f.gz ja existe; $f fica como esta"; falhou=1; continue; fi
    if gzip -k "$f" && gzip -t "$f.gz"; then
      chown --reference="$f" "$f.gz" 2>/dev/null || true
      rm -f "$f"
      log 6 "gravacao: $f compactada ($(du -h "$f.gz" | cut -f1))"
    else
      rm -f "$f.gz"; log 4 "gravacao: falha ao compactar $f; fica como esta"; falhou=1
    fi
  done
  return "$falhou"
}

parar() {
  local motivo tentativa=0
  while true; do
    tentativa=$((tentativa + 1))
    if ! "$SYSTEMCTL" is-active --quiet "$UNIDADE"; then
      log 6 "pnt-md-hml: $UNIDADE ja parado"
      compactar || true
      return 0
    fi
    motivo=$(nao_zerado)
    if [ -z "$motivo" ]; then
      "$SYSTEMCTL" stop "$UNIDADE"
      if "$SYSTEMCTL" is-active --quiet "$UNIDADE"; then
        log 2 "CRITICO pnt-md-hml: stop pedido e $UNIDADE continua ativo"; return 1
      fi
      log 6 "pnt-md-hml: conta zerada; $UNIDADE parado (tentativa $tentativa)"
      compactar || true
      return 0
    fi
    local agora="${PARADA_AGORA_HHMM:-$(date +%H:%M)}"
    if ! [ "$agora" \< "$ATE" ]; then
      log 2 "CRITICO pnt-md-hml: nao zerado ate $ATE ($motivo); desisti, $UNIDADE continua ligado"
      telegram "[$ROTULO] CRITICO pnt-md-hml: nao zerado ate $ATE ($motivo); continua ligado, parar na mao"
      return 1
    fi
    log 4 "ALERTA pnt-md-hml: nao zerado ($motivo); nova tentativa em ${INTERVALO}s (tentativa $tentativa)"
    sleep "$INTERVALO"
  done
}

case "${1:-}" in
  pregao) pregao ;;
  parar) parar ;;
  *) echo "uso: $0 pregao|parar" >&2; exit 2 ;;
esac
