#!/usr/bin/env bash
# Teste do deploy/servico-falhou.sh: journal e systemctl simulados, bot local falso (webhook GET ?msg=).
set -euo pipefail
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'kill $SRV 2>/dev/null || true; rm -rf "$T"' EXIT
python3 -I - "$T/enviadas" > "$T/porta" <<'PY' &
import sys, urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer
saida = sys.argv[1]
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        with open(saida, "a") as f:
            f.write(q["msg"][0] + "\n")
        self.send_response(200); self.end_headers(); self.wfile.write(b'{"689":"ok"}')
    def log_message(self, *a): pass
s = HTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.serve_forever()
PY
SRV=$!
for _ in $(seq 50); do [ -s "$T/porta" ] && break; sleep 0.1; done
cat > "$T/journal" <<'J'
time=2026-10-23T17:09:00.430-03:00 level=INFO msg="kalman-ordens subindo" versao=eb65ec225f1e
time=2026-10-23T17:09:00.437-03:00 level=INFO msg="migrações: nenhuma pendente"
kalman-ordens: Error 1054 (42S22): Unknown column 'conector' in 'SELECT'
J
printf 'NRestarts=5\nResult=start-limit-hit\n' > "$T/info"
falhas=0
SERVICO_FALHOU_JOURNAL="$T/journal" SERVICO_FALHOU_INFO="$T/info" ALERTA_SERVIDOR=.53 \
  ALERTA_WEBHOOK_URL="http://127.0.0.1:$(cat "$T/porta")/telegram/send_alert?chat_id=689" \
  deploy/servico-falhou.sh kalman-ordens.service > /dev/null
esperado="[.53 kalman-ordens] CRITICO: a unidade desistiu de subir (resultado start-limit-hit, 5 reinicio(s), versao eb65ec225f1e); ultimo erro: kalman-ordens: Error 1054 (42S22): Unknown column 'conector' in 'SELECT'; voltar ao binario anterior (LEIA 1.15) e conferir o journal"
if [ "$(cat "$T/enviadas")" = "$esperado" ]; then echo "ok: alerta com o último erro, a versão e os reinícios"; else echo "FALHOU: $(cat "$T/enviadas")"; falhas=$((falhas+1)); fi
kill $SRV; wait $SRV 2>/dev/null || true
if SERVICO_FALHOU_JOURNAL="$T/journal" SERVICO_FALHOU_INFO="$T/info" SERVICO_FALHOU_ESPERA_S=0 \
   ALERTA_WEBHOOK_URL="http://127.0.0.1:$(cat "$T/porta")/x" deploy/servico-falhou.sh kalman-ordens.service > /dev/null 2>&1; then
  echo "FALHOU: bot fora do ar devia sair com erro"; falhas=$((falhas+1))
else
  echo "ok: bot fora do ar, 3 tentativas e sai com erro"
fi
saida=$(SERVICO_FALHOU_JOURNAL="$T/journal" SERVICO_FALHOU_INFO="$T/info" deploy/servico-falhou.sh kalman-ordens.service)
if grep -q "so no log" <<<"$saida"; then echo "ok: sem destino, só no log"; else echo "FALHOU: sem destino"; falhas=$((falhas+1)); fi
[ $falhas = 0 ] && echo "TUDO OK" || { echo "$falhas falha(s)"; exit 1; }
