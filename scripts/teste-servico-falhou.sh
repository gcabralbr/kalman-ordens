#!/usr/bin/env bash
# Teste do deploy/servico-falhou.sh: journal e systemctl simulados, bot local falso (webhook GET ?msg=
# e o sendMessage do Telegram), e um curl de fachada que registra a linha de comando (o token não pode
# aparecer nela). v1.36.1: o modo --reiniciando (AVISO pelo ExecStopPost) e o CRITICO com a LEIA 1.16.
set -euo pipefail
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'kill $SRV 2>/dev/null || true; rm -rf "$T"' EXIT
python3 -I - "$T/enviadas" > "$T/porta" <<'PY' &
import sys, urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer
saida = sys.argv[1]
class H(BaseHTTPRequestHandler):
    def anotar(self, msg):
        with open(saida, "a") as f:
            f.write(msg + "\n")
        self.send_response(200); self.end_headers(); self.wfile.write(b'{"689":"ok"}')
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        self.anotar(q["msg"][0])
    def do_POST(self):
        corpo = self.rfile.read(int(self.headers["Content-Length"])).decode()
        q = urllib.parse.parse_qs(corpo)
        self.anotar(self.path + " " + q["chat_id"][0] + " " + q["text"][0])
    def log_message(self, *a): pass
s = HTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.serve_forever()
PY
SRV=$!
for _ in $(seq 50); do [ -s "$T/porta" ] && break; sleep 0.1; done
PORTA=$(cat "$T/porta")
BOT="http://127.0.0.1:$PORTA/telegram/send_alert?chat_id=689"
# o curl de fachada: anota os argumentos e passa ao de verdade
mkdir "$T/bin"
CURL=$(command -v curl)
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >> %q\nexec %q "$@"\n' "$T/argv" "$CURL" > "$T/bin/curl"
chmod +x "$T/bin/curl"
export PATH="$T/bin:$PATH"

falhas=0
confere() { # nome, esperado, obtido
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FALHOU: $1"; echo "  esperado: $2"; echo "  obtido:   $3"; falhas=$((falhas+1)); fi
}
limpa() { rm -f "$T/enviadas" "$T/argv"; }
enviadas() { cat "$T/enviadas" 2>/dev/null || true; }

# --- CRITICO (OnFailure): o systemd desistiu ---
cat > "$T/journal" <<'J'
time=2026-10-23T17:09:00.430-03:00 level=INFO msg="kalman-ordens subindo" versao=eb65ec225f1e
time=2026-10-23T17:09:00.437-03:00 level=INFO msg="migrações: nenhuma pendente"
kalman-ordens: Error 1054 (42S22): Unknown column 'conector' in 'SELECT'
J
printf 'NRestarts=4\nResult=start-limit-hit\n' > "$T/info"
limpa
SERVICO_FALHOU_JOURNAL="$T/journal" SERVICO_FALHOU_INFO="$T/info" ALERTA_SERVIDOR=.53 ALERTA_WEBHOOK_URL="$BOT" \
  deploy/servico-falhou.sh kalman-ordens.service > /dev/null
confere "CRITICO com o último erro, a versão, os reinícios e a LEIA 1.16" \
  "[.53 kalman-ordens] CRITICO: a unidade desistiu de subir (resultado start-limit-hit, 4 reinicio(s), versao eb65ec225f1e); ultimo erro: kalman-ordens: Error 1054 (42S22): Unknown column 'conector' in 'SELECT'; se caiu logo depois de instalar um binario novo, voltar ao anterior (LEIA 1.16); senao, conferir o journal" \
  "$(enviadas)"

# --- AVISO (ExecStopPost): a falha que o systemd vai reiniciar ---
mkdir "$T/estado"
echo "kalman-ordens: chmod /var/lib/kalman-ordens/fix-real/store/x.senderseqnums: no such file or directory" > "$T/estado/ultimo-erro"
printf 'NRestarts=0\nResult=success\nJob=\nStartLimitBurst=5\nRestartUSec=10s\n' > "$T/info"
limpa
SERVICE_RESULT=exit-code EXIT_CODE=exited EXIT_STATUS=1 STATE_DIRECTORY="$T/estado" SERVICO_FALHOU_INFO="$T/info" \
  ALERTA_SERVIDOR=.53 ALERTA_WEBHOOK_URL="$BOT" deploy/servico-falhou.sh --reiniciando kalman-ordens.service > /dev/null
confere "AVISO da 1ª falha, com o último erro do serviço e a tentativa" \
  "[.53 kalman-ordens] AVISO: falhou (exit-code, exited 1); ultimo erro: kalman-ordens: chmod /var/lib/kalman-ordens/fix-real/store/x.senderseqnums: no such file or directory; o systemd reinicia em 10s (tentativa 1 de 5). Se ele desistir, sai o CRITICO" \
  "$(enviadas)"

printf 'NRestarts=3\nJob=\nStartLimitBurst=5\nRestartUSec=10s\n' > "$T/info"
limpa
SERVICE_RESULT=signal EXIT_CODE=killed EXIT_STATUS=SEGV SERVICO_FALHOU_INFO="$T/info" \
  ALERTA_SERVIDOR=.53 ALERTA_WEBHOOK_URL="$BOT" deploy/servico-falhou.sh --reiniciando kalman-ordens.service > /dev/null
confere "AVISO sem o arquivo do último erro (morte por sinal): aponta o journal" \
  "[.53 kalman-ordens] AVISO: falhou (signal, killed SEGV); ultimo erro: sem mensagem do servico, ver o journal; o systemd reinicia em 10s (tentativa 4 de 5). Se ele desistir, sai o CRITICO" \
  "$(enviadas)"

limpa
SERVICE_RESULT=success SERVICO_FALHOU_INFO="$T/info" ALERTA_WEBHOOK_URL="$BOT" \
  deploy/servico-falhou.sh --reiniciando kalman-ordens.service > /dev/null
confere "parada normal (success): nada enviado" "" "$(enviadas)"

printf 'NRestarts=0\nJob=4711\nStartLimitBurst=5\nRestartUSec=10s\n' > "$T/info"
limpa
SERVICE_RESULT=timeout SERVICO_FALHOU_INFO="$T/info" ALERTA_WEBHOOK_URL="$BOT" \
  deploy/servico-falhou.sh --reiniciando kalman-ordens.service > /dev/null
confere "parada pedida que estourou o tempo (job em curso): nada enviado" "" "$(enviadas)"

# --- Telegram direto: o token não vai na linha de comando ---
printf 'NRestarts=0\nJob=\nStartLimitBurst=5\nRestartUSec=10s\n' > "$T/info"
limpa
SERVICE_RESULT=exit-code EXIT_CODE=exited EXIT_STATUS=1 SERVICO_FALHOU_INFO="$T/info" ALERTA_SERVIDOR=.53 \
  TELEGRAM_URL="http://127.0.0.1:$PORTA" TELEGRAM_TOKEN=123:SEGREDO-DO-BOT TELEGRAM_CHAT=-1001 \
  deploy/servico-falhou.sh --reiniciando kalman-ordens.service > /dev/null
confere "Telegram: o sendMessage com o token no caminho e o chat" \
  "/bot123:SEGREDO-DO-BOT/sendMessage -1001 [.53 kalman-ordens] AVISO: falhou (exit-code, exited 1); ultimo erro: sem mensagem do servico, ver o journal; o systemd reinicia em 10s (tentativa 1 de 5). Se ele desistir, sai o CRITICO" \
  "$(enviadas)"
if grep -q "SEGREDO-DO-BOT" "$T/argv"; then echo "FALHOU: o token apareceu na linha de comando do curl"; falhas=$((falhas+1)); else echo "ok: o token fora da linha de comando do curl"; fi
limpa
SERVICO_FALHOU_JOURNAL="$T/journal" SERVICO_FALHOU_INFO="$T/info" ALERTA_WEBHOOK_URL="$BOT" \
  deploy/servico-falhou.sh kalman-ordens.service > /dev/null
if grep -q "send_alert" "$T/argv"; then echo "FALHOU: a URL do bot na linha de comando"; falhas=$((falhas+1)); else echo "ok: a URL do bot fora da linha de comando"; fi

# --- falhas de envio e sem destino ---
kill $SRV; wait $SRV 2>/dev/null || true
if SERVICO_FALHOU_JOURNAL="$T/journal" SERVICO_FALHOU_INFO="$T/info" SERVICO_FALHOU_ESPERA_S=0 \
   ALERTA_WEBHOOK_URL="http://127.0.0.1:$PORTA/x" deploy/servico-falhou.sh kalman-ordens.service > /dev/null 2> "$T/err"; then
  echo "FALHOU: bot fora do ar devia sair com erro"; falhas=$((falhas+1))
else
  confere "CRITICO com o bot fora do ar: 3 tentativas e sai com erro" 3 "$(grep -c 'envio falhou' "$T/err")"
fi
if SERVICE_RESULT=exit-code SERVICO_FALHOU_INFO="$T/info" ALERTA_WEBHOOK_URL="http://127.0.0.1:$PORTA/x" \
   deploy/servico-falhou.sh --reiniciando kalman-ordens.service > /dev/null 2> "$T/err"; then
  echo "FALHOU: AVISO com o bot fora do ar devia sair com erro"; falhas=$((falhas+1))
else
  confere "AVISO com o bot fora do ar: uma tentativa só (não segura o reinício)" 1 "$(grep -c 'envio falhou' "$T/err")"
fi
saida=$(SERVICO_FALHOU_JOURNAL="$T/journal" SERVICO_FALHOU_INFO="$T/info" deploy/servico-falhou.sh kalman-ordens.service)
if grep -q "so no log" <<<"$saida"; then echo "ok: sem destino, só no log"; else echo "FALHOU: sem destino"; falhas=$((falhas+1)); fi
[ $falhas = 0 ] && echo "TUDO OK" || { echo "$falhas falha(s)"; exit 1; }
