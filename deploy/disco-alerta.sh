#!/usr/bin/env bash
# Monitor de disco da .53 (proteção contra falha de fonte, item 3; 20/10: o /home encheu com a
# gravação do pnt-md-hml). Para cada ponto de montagem (DISCO_PONTOS): uso >= DISCO_CRITICO (90) =
# DISCO_90 (crítico), >= DISCO_AVISO (80) = DISCO_80 (aviso). Manda ao Telegram na mudança de nível e,
# enquanto durar, no máximo a cada DISCO_REPETIR_MIN (30) minutos; a volta abaixo do aviso manda
# RESOLVIDO. Independe dos serviços (timer do systemd, kalman-disco-alerta.timer, a cada 5 min).
#
# Configuração em /etc/kalman-ordens/disco.env (deploy/disco.env.example). Envio pelo ALERTA_WEBHOOK_URL
# (o bot local, GET ?msg=) ou, sem ele, pelo TELEGRAM_TOKEN/TELEGRAM_CHAT; sem nenhum, só no journal.
# Texto: "[<ALERTA_SERVIDOR> kalman-disco-alerta] ...", em ASCII (como os alertas do kalman-ordens).
# Testes: DISCO_USO_ARQ = arquivo com "ponto uso%" por linha no lugar do df (scripts/teste-disco-alerta.sh).
set -euo pipefail

PONTOS="${DISCO_PONTOS:-/ /home}"
AVISO="${DISCO_AVISO:-80}"
CRITICO="${DISCO_CRITICO:-90}"
REPETIR_S=$(( ${DISCO_REPETIR_MIN:-30} * 60 ))
ESTADO="${DISCO_ESTADO_DIR:-${STATE_DIRECTORY:-/var/lib/kalman-disco-alerta}}"
ROTULO="${ALERTA_SERVIDOR:-$(hostname -s)} kalman-disco-alerta"
URL="${TELEGRAM_URL:-https://api.telegram.org}"
AGORA="${DISCO_AGORA:-$(date +%s)}"

mkdir -p "$ESTADO"

uso() {  # uso em % (inteiro) do ponto $1
  if [ -n "${DISCO_USO_ARQ:-}" ]; then
    awk -v p="$1" '$1 == p { print $2 }' "$DISCO_USO_ARQ"
  else
    df --output=pcent "$1" | tail -1 | tr -dc '0-9'
  fi
}

livre() {
  if [ -n "${DISCO_USO_ARQ:-}" ]; then echo "?"; else df -h --output=avail "$1" | tail -1 | tr -d ' '; fi
}

enviar() {  # texto; 0 = enviado (ou sem destino configurado)
  echo "$1"
  if [ -n "${ALERTA_WEBHOOK_URL:-}" ]; then
    local r
    if ! r=$(curl -fsS -m 10 -G "$ALERTA_WEBHOOK_URL" --data-urlencode "msg=$1" 2>/dev/null) || grep -q '"error"' <<<"$r"; then
      echo "webhook: falha de envio; nova tentativa na proxima execucao" >&2
      return 1
    fi
    return 0
  fi
  if [ -z "${TELEGRAM_TOKEN:-}" ] || [ -z "${TELEGRAM_CHAT:-}" ]; then
    echo "telegram sem token/chat; alerta so no log"
    return 0
  fi
  if ! curl -fsS -m 10 -o /dev/null "$URL/bot$TELEGRAM_TOKEN/sendMessage" \
       --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$1" 2>/dev/null; then
    echo "telegram: falha de envio; nova tentativa na proxima execucao" >&2
    return 1
  fi
}

falhou=0
for p in $PONTOS; do
  u=$(uso "$p")
  if [ -z "$u" ]; then echo "disco: sem uso de $p" >&2; falhou=1; continue; fi
  if [ "$u" -ge "$CRITICO" ]; then nivel=DISCO_90; elif [ "$u" -ge "$AVISO" ]; then nivel=DISCO_80; else nivel=OK; fi
  arq="$ESTADO/$(echo "$p" | tr '/' '_').estado"
  antes=OK; enviado=0
  if [ -r "$arq" ]; then read -r antes enviado < "$arq" || true; fi
  echo "disco: $p ${u}% ($nivel)"

  if [ "$nivel" = OK ]; then
    if [ "$antes" != OK ]; then
      enviar "[$ROTULO] RESOLVIDO $antes: $p em ${u}% ($(livre "$p") livres)" || { falhou=1; continue; }
    fi
    echo "OK 0" > "$arq"
    continue
  fi
  if [ "$nivel" != "$antes" ] || [ $(( AGORA - enviado )) -ge "$REPETIR_S" ]; then
    tipo=aviso; [ "$nivel" = DISCO_90 ] && tipo=CRITICO
    if enviar "[$ROTULO] $nivel ($tipo): $p em ${u}% ($(livre "$p") livres)"; then
      echo "$nivel $AGORA" > "$arq"
    else
      falhou=1
    fi
  fi
done
exit "$falhou"
