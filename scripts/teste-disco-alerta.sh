#!/usr/bin/env bash
# Teste do deploy/disco-alerta.sh: uso simulado (DISCO_USO_ARQ), relógio simulado (DISCO_AGORA) e um
# Telegram falso local (python, registra o text de cada sendMessage).
#   scripts/teste-disco-alerta.sh
set -euo pipefail
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'kill $SRV 2>/dev/null || true; rm -rf "$T"' EXIT

python3 -I - "$T/enviadas" > "$T/porta" <<'PY' &
import sys, urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer
saida = sys.argv[1]
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        corpo = self.rfile.read(int(self.headers["Content-Length"])).decode()
        with open(saida, "a") as f:
            f.write(urllib.parse.parse_qs(corpo)["text"][0] + "\n")
        self.send_response(200); self.end_headers(); self.wfile.write(b'{"ok":true}')
    def log_message(self, *a): pass
s = HTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.serve_forever()
PY
SRV=$!
for _ in $(seq 50); do [ -s "$T/porta" ] && break; sleep 0.1; done
touch "$T/enviadas"

falhas=0
rodar() {  # agora; linhas "ponto uso" ...
  local agora=$1; shift
  printf '%s\n' "$@" > "$T/uso"
  DISCO_USO_ARQ="$T/uso" DISCO_AGORA="$agora" DISCO_ESTADO_DIR="$T/estado" ALERTA_SERVIDOR=teste \
    TELEGRAM_TOKEN=x TELEGRAM_CHAT=1 TELEGRAM_URL="http://127.0.0.1:$(cat "$T/porta")" \
    deploy/disco-alerta.sh > /dev/null
}
espera() {  # descrição; mensagens esperadas desde a última conferência
  local n; n=$(wc -l < "$T/enviadas")
  local obtido; obtido=$(tail -n +"$(( ${VISTAS:-0} + 1 ))" "$T/enviadas")
  VISTAS=$n
  local esperado; esperado=$(printf '%s\n' "${@:2}" | sed '/^$/d')
  if [ "$obtido" != "$esperado" ]; then
    echo "FALHOU: $1"; echo "  esperado: $esperado"; echo "  obtido:   $obtido"; falhas=$((falhas + 1))
  else
    echo "ok: $1"
  fi
}

rodar 1000 "/ 50" "/home 70";            espera "abaixo de 80: nada" ""
rodar 1300 "/ 50" "/home 81";            espera "80%: aviso" "[teste kalman-disco-alerta] DISCO_80 (aviso): /home em 81% (? livres)"
rodar 1600 "/ 50" "/home 82";            espera "aviso de novo antes de 30 min: nada" ""
rodar 3100 "/ 50" "/home 83";            espera "30 min depois: repete" "[teste kalman-disco-alerta] DISCO_80 (aviso): /home em 83% (? livres)"
rodar 3400 "/ 50" "/home 91";            espera "90%: crítico na hora" "[teste kalman-disco-alerta] DISCO_90 (CRITICO): /home em 91% (? livres)"
rodar 3700 "/ 92" "/home 91";            espera "dois pontos: só o novo" "[teste kalman-disco-alerta] DISCO_90 (CRITICO): / em 92% (? livres)"
rodar 5500 "/ 92" "/home 91";            espera "30 min: repete os dois" "[teste kalman-disco-alerta] DISCO_90 (CRITICO): / em 92% (? livres)" "[teste kalman-disco-alerta] DISCO_90 (CRITICO): /home em 91% (? livres)"
rodar 5800 "/ 50" "/home 60";            espera "volta: RESOLVIDO" "[teste kalman-disco-alerta] RESOLVIDO DISCO_90: / em 50% (? livres)" "[teste kalman-disco-alerta] RESOLVIDO DISCO_90: /home em 60% (? livres)"
rodar 6100 "/ 50" "/home 60";            espera "continua bem: nada" ""

# Telegram fora do ar: falha (exit 1) e manda na próxima execução
kill $SRV; wait $SRV 2>/dev/null || true
printf '%s\n' "/ 50" "/home 85" > "$T/uso"
if DISCO_USO_ARQ="$T/uso" DISCO_AGORA=6400 DISCO_ESTADO_DIR="$T/estado" ALERTA_SERVIDOR=teste TELEGRAM_TOKEN=x TELEGRAM_CHAT=1 \
   TELEGRAM_URL="http://127.0.0.1:$(cat "$T/porta")" deploy/disco-alerta.sh > /dev/null 2>&1; then
  echo "FALHOU: envio falho devia sair com erro"; falhas=$((falhas + 1))
else
  echo "ok: envio falho sai com erro"
fi
if [ "$(cat "$T/estado/_home.estado")" = "OK 0" ]; then echo "ok: não marcou como enviado"; else echo "FALHOU: marcou como enviado"; falhas=$((falhas + 1)); fi

# sem Telegram configurado: só no log, sai bem
saida=$(DISCO_USO_ARQ="$T/uso" DISCO_AGORA=6500 DISCO_ESTADO_DIR="$T/estado2" deploy/disco-alerta.sh)
if grep -q "telegram sem token/chat" <<<"$saida" && grep -q "DISCO_80 (aviso): /home em 85%" <<<"$saida"; then
  echo "ok: sem Telegram, só no log"
else
  echo "FALHOU: sem Telegram"; falhas=$((falhas + 1))
fi
# webhook (o bot local): vence o Telegram direto; entrega com "error" na resposta = falha
PORTA=$(cat "$T/porta")
printf '%s\n' "/ 50" "/home 95" > "$T/uso"
python3 -I - "$T/enviadas" "$T/porta2" <<'PY' &
import sys, urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer
saida = sys.argv[1]
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        corpo = b'[{"chat_id":"9","error":"x"}]' if "ERRO_BOT" in q.get("chat_id", [""])[0] else b'[{"chat_id":"9","status":"ok"}]'
        if b"error" not in corpo:
            open(saida, "a").write("webhook " + q["chat_id"][0] + " " + q["msg"][0] + "\n")
        self.send_response(200); self.end_headers(); self.wfile.write(corpo)
    def log_message(self, *a): pass
s = HTTPServer(("127.0.0.1", 0), H)
open(sys.argv[2], "w").write(str(s.server_address[1]))
s.serve_forever()
PY
SRV2=$!; trap 'kill $SRV2 2>/dev/null || true; rm -rf "$T"' EXIT
for _ in $(seq 50); do [ -s "$T/porta2" ] && break; sleep 0.1; done
: > "$T/enviadas"
DISCO_USO_ARQ="$T/uso" DISCO_AGORA=7000 DISCO_ESTADO_DIR="$T/estado4" ALERTA_SERVIDOR=.53 TELEGRAM_TOKEN=x TELEGRAM_CHAT=1 \
  ALERTA_WEBHOOK_URL="http://127.0.0.1:$(cat "$T/porta2")/telegram/send_alert?chat_id=9" deploy/disco-alerta.sh > /dev/null
if [ "$(cat "$T/enviadas")" = "webhook 9 [.53 kalman-disco-alerta] DISCO_90 (CRITICO): /home em 95% (? livres)" ]; then
  echo "ok: webhook com servidor e servico no texto"
else
  echo "FALHOU: webhook"; cat "$T/enviadas"; falhas=$((falhas + 1))
fi
if DISCO_USO_ARQ="$T/uso" DISCO_AGORA=7000 DISCO_ESTADO_DIR="$T/estado5" \
   ALERTA_WEBHOOK_URL="http://127.0.0.1:$(cat "$T/porta2")/telegram/send_alert?chat_id=ERRO_BOT" deploy/disco-alerta.sh > /dev/null 2>&1; then
  echo "FALHOU: erro do bot devia sair com erro"; falhas=$((falhas + 1))
else
  [ ! -e "$T/estado5/_home.estado" ] || [ "$(cat "$T/estado5/_home.estado")" = "OK 0" ] && echo "ok: erro do bot = falha, nao marca como enviado" || { echo "FALHOU: erro do bot marcou"; falhas=$((falhas + 1)); }
fi

# df de verdade
DISCO_ESTADO_DIR="$T/estado3" DISCO_PONTOS=/ deploy/disco-alerta.sh | grep -qE '^disco: / [0-9]+% ' && echo "ok: df real" || { echo "FALHOU: df real"; falhas=$((falhas + 1)); }

[ "$falhas" -eq 0 ] && echo "TUDO OK" || { echo "$falhas falha(s)"; exit 1; }
