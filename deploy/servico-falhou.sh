#!/usr/bin/env bash
# Alertas da unidade kalman-ordens que cai (23/10: o eb65ec2 reiniciou 37 vezes em 3 min sem aviso;
# v1.36.1, 17/11: a 1ª falha mandava o CRITICO com o reinício ainda previsto). Dois modos:
#
#   servico-falhou.sh UNIDADE               CRITICO. Pelo OnFailure= (kalman-ordens-falhou@%n.service).
#     Com RestartMode=direct na unidade, o OnFailure só dispara quando o systemd desiste
#     (StartLimitBurst atingido, ou erro sem reinício). Último erro e versão pelo journal (a unidade
#     do alerta tem o grupo systemd-journal); envio pelo /etc/kalman-ordens/disco.env, como o monitor
#     de disco. O que fazer: deploy/LEIA.md 1.16.
#
#   servico-falhou.sh --reiniciando UNIDADE  AVISO "falhou, reiniciando (tentativa N de M)". Pelo
#     ExecStopPost= da própria unidade: roda como o usuário do serviço, no sandbox dela, com o
#     ambiente do service.env que o serviço já tem (ALERTA_WEBHOOK_URL ou TELEGRAM_*); não lê o
#     disco.env nem o journal. Último erro em $STATE_DIRECTORY/ultimo-erro (o "servir" grava ao cair).
#     Calado na parada normal ($SERVICE_RESULT=success) e na parada pedida (a unidade com job de
#     stop/restart em curso): só a falha que o systemd vai reiniciar avisa.
#
# Envio: ALERTA_WEBHOOK_URL (o bot local, GET ?msg=) ou TELEGRAM_TOKEN + TELEGRAM_CHAT; sem nenhum,
# só no journal. O token e a URL vão ao curl pela configuração na entrada padrão (-K -), nunca na
# linha de comando (que qualquer usuário vê no ps).
# Testes (scripts/teste-servico-falhou.sh): SERVICO_FALHOU_JOURNAL (arquivo no lugar do journalctl),
# SERVICO_FALHOU_INFO (no lugar do systemctl show).
set -euo pipefail
MODO=critico
if [ "${1:-}" = --reiniciando ]; then
  MODO=reiniciando
  shift
fi
UNIDADE="${1:?uso: $0 [--reiniciando] UNIDADE}"
ROTULO="${ALERTA_SERVIDOR:-$(hostname -s)} ${UNIDADE%.service}"
URL="${TELEGRAM_URL:-https://api.telegram.org}"

mostrar() { # as propriedades da unidade (NRestarts, Result, Job, ...)
  if [ -n "${SERVICO_FALHOU_INFO:-}" ]; then
    cat "$SERVICO_FALHOU_INFO"
  else
    systemctl show "$UNIDADE" -p NRestarts -p Result -p Job -p StartLimitBurst -p RestartUSec 2>/dev/null || true
  fi
}
prop() { grep -E "^$1=" <<<"$info" | head -1 | cut -d= -f2- || true; }

info=$(mostrar)
if [ "$MODO" = reiniciando ]; then
  resultado="${SERVICE_RESULT:-}"
  if [ -z "$resultado" ] || [ "$resultado" = success ]; then
    exit 0 # parada normal
  fi
  if [ -n "$(prop Job)" ]; then
    echo "parada pedida (job $(prop Job)), resultado $resultado: sem aviso"
    exit 0
  fi
  reinicios=$(prop NRestarts)
  tentativa=$(( ${reinicios:-0} + 1 ))
  ultimo=""
  if [ -n "${STATE_DIRECTORY:-}" ] && [ -r "${STATE_DIRECTORY%%:*}/ultimo-erro" ]; then
    ultimo=$(head -c 400 "${STATE_DIRECTORY%%:*}/ultimo-erro" | head -1)
  fi
  saida="${EXIT_CODE:-?} ${EXIT_STATUS:-?}"
  espera=$(prop RestartUSec)
  limite=$(prop StartLimitBurst)
  texto="[$ROTULO] AVISO: falhou ($resultado, $saida); ultimo erro: ${ultimo:-sem mensagem do servico, ver o journal}; o systemd reinicia em ${espera:-?} (tentativa $tentativa de ${limite:-?}). Se ele desistir, sai o CRITICO"
  TENTATIVAS=1 # o ExecStopPost atrasa o reinício: um envio só, curto
else
  if [ -n "${SERVICO_FALHOU_JOURNAL:-}" ]; then
    log=$(cat "$SERVICO_FALHOU_JOURNAL")
  else
    log=$(journalctl -u "$UNIDADE" -n 80 -o cat --no-pager 2>/dev/null || true)
  fi
  ultimo=$(grep -E '^kalman-ordens: |level=ERROR' <<<"$log" | tail -1 || true)
  versao=$(grep -oE 'versao=[0-9a-f]+' <<<"$log" | tail -1 | cut -d= -f2 || true)
  reinicios=$(prop NRestarts)
  resultado=$(prop Result)
  texto="[$ROTULO] CRITICO: a unidade desistiu de subir (resultado ${resultado:-?}, ${reinicios:-?} reinicio(s), versao ${versao:-?}); ultimo erro: ${ultimo:-sem erro no journal}; se caiu logo depois de instalar um binario novo, voltar ao anterior (LEIA 1.16); senao, conferir o journal"
  TENTATIVAS=3
fi
texto=$(iconv -f UTF-8 -t ASCII//TRANSLIT <<<"$texto" 2>/dev/null || tr -cd '\11\12\15\40-\176' <<<"$texto")
texto=${texto:0:900}

enviar() {
  echo "$1"
  if [ -n "${ALERTA_WEBHOOK_URL:-}" ]; then
    local r
    r=$(printf 'url = "%s"\n' "$ALERTA_WEBHOOK_URL" | curl -fsS -m 5 -G -K - --data-urlencode "msg=$1" 2>/dev/null) && ! grep -q '"error"' <<<"$r"
    return
  fi
  if [ -z "${TELEGRAM_TOKEN:-}" ] || [ -z "${TELEGRAM_CHAT:-}" ]; then
    echo "telegram sem token/chat; alerta so no log"
    return 0
  fi
  printf 'url = "%s/bot%s/sendMessage"\n' "$URL" "$TELEGRAM_TOKEN" | curl -fsS -m 5 -o /dev/null -K - \
    --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$1" 2>/dev/null
}

for tentativa in $(seq "$TENTATIVAS"); do
  if enviar "$texto"; then exit 0; fi
  echo "envio falhou (tentativa $tentativa)" >&2
  [ "$tentativa" = "$TENTATIVAS" ] || sleep "${SERVICO_FALHOU_ESPERA_S:-10}"
done
exit 1
