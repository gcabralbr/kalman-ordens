#!/usr/bin/env bash
# Alerta de unidade que desistiu de subir (23/10: o eb65ec2 reiniciou 37 vezes em 3 min sem aviso).
# Chamado pelo OnFailure= do kalman-ordens.service (kalman-ordens-falhou@%n.service): a unidade entrou
# em failed — StartLimitBurst atingido ou erro sem reinício. Manda CRITICO com o último erro do
# journal e o que fazer (voltar ao binário anterior, deploy/LEIA.md 1.15). Envio como o monitor de
# disco: /etc/kalman-ordens/disco.env (ALERTA_WEBHOOK_URL ou TELEGRAM_*; sem nenhum, só no journal).
# Testes: SERVICO_FALHOU_JOURNAL (arquivo no lugar do journalctl), SERVICO_FALHOU_INFO (no lugar do
# systemctl show).
set -euo pipefail
UNIDADE="${1:?uso: $0 UNIDADE}"
ROTULO="${ALERTA_SERVIDOR:-$(hostname -s)} ${UNIDADE%.service}"
URL="${TELEGRAM_URL:-https://api.telegram.org}"

if [ -n "${SERVICO_FALHOU_JOURNAL:-}" ]; then
  log=$(cat "$SERVICO_FALHOU_JOURNAL")
else
  log=$(journalctl -u "$UNIDADE" -n 80 -o cat --no-pager 2>/dev/null || true)
fi
if [ -n "${SERVICO_FALHOU_INFO:-}" ]; then
  info=$(cat "$SERVICO_FALHOU_INFO")
else
  info=$(systemctl show "$UNIDADE" -p NRestarts -p Result 2>/dev/null || true)
fi
ultimo=$(grep -E '^kalman-ordens: |level=ERROR' <<<"$log" | tail -1 || true)
versao=$(grep -oE 'versao=[0-9a-f]+' <<<"$log" | tail -1 | cut -d= -f2 || true)
reinicios=$(grep -oE 'NRestarts=[0-9]+' <<<"$info" | cut -d= -f2 || true)
resultado=$(grep -oE 'Result=[a-z-]+' <<<"$info" | cut -d= -f2 || true)
texto="[$ROTULO] CRITICO: a unidade desistiu de subir (resultado ${resultado:-?}, ${reinicios:-?} reinicio(s), versao ${versao:-?}); ultimo erro: ${ultimo:-sem erro no journal}; voltar ao binario anterior (LEIA 1.15) e conferir o journal"
texto=$(iconv -f UTF-8 -t ASCII//TRANSLIT <<<"$texto" 2>/dev/null || tr -cd '\11\12\15\40-\176' <<<"$texto")
texto=${texto:0:900}

enviar() {
  echo "$1"
  if [ -n "${ALERTA_WEBHOOK_URL:-}" ]; then
    local r
    r=$(curl -fsS -m 10 -G "$ALERTA_WEBHOOK_URL" --data-urlencode "msg=$1" 2>/dev/null) && ! grep -q '"error"' <<<"$r"
    return
  fi
  if [ -z "${TELEGRAM_TOKEN:-}" ] || [ -z "${TELEGRAM_CHAT:-}" ]; then
    echo "telegram sem token/chat; alerta so no log"
    return 0
  fi
  curl -fsS -m 10 -o /dev/null "$URL/bot$TELEGRAM_TOKEN/sendMessage" \
    --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$1" 2>/dev/null
}

for tentativa in 1 2 3; do
  if enviar "$texto"; then exit 0; fi
  echo "envio falhou (tentativa $tentativa)" >&2
  [ "$tentativa" = 3 ] || sleep "${SERVICO_FALHOU_ESPERA_S:-10}"
done
exit 1
