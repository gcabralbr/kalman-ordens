#!/usr/bin/env bash
# Teste do deploy/pnt-md-hml-horario.sh: /saude do kalman-ordens falso local (python, responde em
# sequência as linhas de um arquivo; a última se repete), systemctl falso (estado num arquivo), relógio
# (PARADA_AGORA_HHMM, HOJE), feriados e var/ temporários, Telegram falso.
#   scripts/teste-pnt-md-hml-horario.sh
set -euo pipefail
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'kill $SRV 2>/dev/null || true; rm -rf "$T"' EXIT

python3 -I - "$T/respostas" "$T/telegram" > "$T/porta" <<'PY' &
import sys, urllib.parse
from http.server import BaseHTTPRequestHandler, HTTPServer
resp, tele = sys.argv[1], sys.argv[2]
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/telegram/send_alert"):  # o webhook (bot local): GET ?msg=
            q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
            with open(tele, "a") as f:
                f.write("webhook " + q["chat_id"][0] + " " + q["msg"][0] + "\n")
            self.send_response(200); self.end_headers(); self.wfile.write(b'[{"status":"ok"}]'); return
        linhas = open(resp).read().splitlines()
        corpo = linhas.pop(0) if len(linhas) > 1 else linhas[0]
        open(resp, "w").write("\n".join(linhas) + "\n")
        if corpo == "FORA":
            self.send_response(503); self.end_headers(); return
        self.send_response(200); self.end_headers(); self.wfile.write(corpo.encode())
    def do_POST(self):
        corpo = self.rfile.read(int(self.headers["Content-Length"])).decode()
        with open(tele, "a") as f:
            f.write(urllib.parse.parse_qs(corpo)["text"][0] + "\n")
        self.send_response(200); self.end_headers(); self.wfile.write(b'{"ok":true}')
    def log_message(self, *a): pass
s = HTTPServer(("127.0.0.1", 0), H)
print(s.server_address[1], flush=True)
s.serve_forever()
PY
SRV=$!
for _ in $(seq 50); do [ -s "$T/porta" ] && break; sleep 0.1; done
P=$(cat "$T/porta")

# systemctl falso: is-active lê $T/ativo; stop o apaga (ou não, com $T/stop-falha) e registra
cat > "$T/systemctl" <<EOF
#!/usr/bin/env bash
case "\$1" in
  is-active) [ -e "$T/ativo" ] ;;
  stop) echo "stop \$2" >> "$T/chamadas"; [ -e "$T/stop-falha" ] || rm -f "$T/ativo" ;;
esac
EOF
chmod +x "$T/systemctl"

falhas=0
ok() { echo "ok: $1"; }
falha() { echo "FALHOU: $1"; [ -n "${2:-}" ] && printf '  %s\n' "$2"; falhas=$((falhas + 1)); }

Z='{"sessao_logada":true,"conciliando":false,"divergente":false,"posicao":0,"ordens_vivas":null}'
Z_VAZIA='{"sessao_logada":true,"conciliando":false,"divergente":false,"posicao":0,"ordens_vivas":[]}'
POS='{"sessao_logada":true,"conciliando":false,"divergente":false,"posicao":1,"ordens_vivas":null}'
VIVA='{"sessao_logada":true,"conciliando":false,"divergente":false,"posicao":0,"ordens_vivas":[{"clordid":"KM1"}]}'
SESSAO='{"sessao_logada":false,"conciliando":false,"divergente":false,"posicao":0,"ordens_vivas":null}'
CONC='{"sessao_logada":true,"conciliando":true,"divergente":false,"posicao":0,"ordens_vivas":null}'
DIV='{"sessao_logada":true,"conciliando":false,"divergente":true,"posicao":0,"ordens_vivas":null}'
VIVAS2='{"sessao_logada":true,"conciliando":false,"divergente":false,"posicao":0,"ordens_vivas":[{"clordid":"KM1"},{"clordid":"KM2"}]}'
FORA=FORA
RUIM='{nao json'

parar() {  # agora; respostas do /saude ... → saída em $T/saida, código em $RC
  local agora=$1; shift
  printf '%s\n' "$@" > "$T/respostas"
  rm -f "$T/chamadas"; : > "$T/telegram"
  RC=0
  PARADA_SAUDE_URL="http://127.0.0.1:$P/saude" PARADA_SYSTEMCTL="$T/systemctl" PARADA_INTERVALO_S=0 \
    PARADA_AGORA_HHMM="$agora" PARADA_VAR="$T/var" ALERTA_SERVIDOR=teste \
    TELEGRAM_TOKEN=x TELEGRAM_CHAT=1 TELEGRAM_URL="http://127.0.0.1:$P" \
    deploy/pnt-md-hml-horario.sh parar > "$T/saida" 2>&1 || RC=$?
}
parou() { [ ! -e "$T/ativo" ] && grep -q "^stop kalman-pnt-md-hml.service$" "$T/chamadas" 2>/dev/null; }
nao_parou() { [ -e "$T/ativo" ] && [ ! -e "$T/chamadas" ]; }

# --- parar
mkdir -p "$T/var"
printf 'a\n' > "$T/var/pnt-md-hml-WINZ26-20261022.jsonl"
printf 'b\n' > "$T/var/pnt-md-hml-WINZ26-20261021.jsonl"
printf 'velho\n' | gzip > "$T/var/pnt-md-hml-WINZ26-20261020.jsonl.gz"
printf 'outro\n' > "$T/var/suite.log"

touch "$T/ativo"; parar 14:35 "$Z"
if [ $RC = 0 ] && parou && grep -q "conta zerada" "$T/saida"; then ok "zerado (ordens_vivas null): para"; else falha "zerado null" "$(cat "$T/saida")"; fi
if [ ! -e "$T/var/pnt-md-hml-WINZ26-20261022.jsonl" ] && [ "$(zcat "$T/var/pnt-md-hml-WINZ26-20261022.jsonl.gz")" = a ] \
   && [ "$(zcat "$T/var/pnt-md-hml-WINZ26-20261021.jsonl.gz")" = b ] && [ "$(zcat "$T/var/pnt-md-hml-WINZ26-20261020.jsonl.gz")" = velho ] \
   && [ -e "$T/var/suite.log" ]; then
  ok "gravações compactadas (a de ontem esquecida também), .gz antigo e outros arquivos intactos"
else
  falha "compactação" "$(ls -l "$T/var")"
fi

touch "$T/ativo"; parar 14:35 "$Z_VAZIA"
[ $RC = 0 ] && parou && ok "zerado (ordens_vivas []): para" || falha "zerado []" "$(cat "$T/saida")"

for caso in "POS:posicao 1" "VIVA:1 ordem(ns) KM viva(s)" "SESSAO:sessao FIX nao logada" "CONC:conciliando" \
            "DIV:divergente" "VIVAS2:2 ordem(ns) KM viva(s)" "FORA:kalman-ordens sem resposta" "RUIM:resposta ilegivel"; do
  nome=${caso%%:*}; motivo=${caso#*:}
  resp=${!nome}
  touch "$T/ativo"; parar 14:35 "$resp" "$Z"
  if [ $RC = 0 ] && parou && grep -q "<4>ALERTA pnt-md-hml: nao zerado ($motivo" "$T/saida" && grep -q "tentativa 2" "$T/saida"; then
    ok "$nome: alerta ($motivo), tenta de novo e para quando zera"
  else
    falha "$nome" "$(cat "$T/saida")"
  fi
done

touch "$T/ativo"; parar 14:35 "$POS" "$POS" "$POS" "$Z"
[ $RC = 0 ] && parou && [ "$(grep -c '<4>ALERTA' "$T/saida")" = 3 ] && ok "três tentativas sem zerar, para na quarta" || falha "três tentativas" "$(cat "$T/saida")"

touch "$T/ativo"; parar 18:25 "$POS"
if [ $RC = 1 ] && nao_parou && grep -q "<2>CRITICO pnt-md-hml: nao zerado ate 18:25 (posicao 1); desisti" "$T/saida" \
   && grep -q "^\[teste kalman-pnt-md-hml\] CRITICO pnt-md-hml: nao zerado ate 18:25 (posicao 1)" "$T/telegram"; then
  ok "18:25 sem zerar: crítico (journal e Telegram), desiste, não para, sai com 1"
else
  falha "18:25" "$(cat "$T/saida" "$T/telegram")"
fi
printf '%s\n' "$POS" > "$T/respostas"; : > "$T/telegram"; touch "$T/ativo"
PARADA_SAUDE_URL="http://127.0.0.1:$P/saude" PARADA_SYSTEMCTL="$T/systemctl" PARADA_AGORA_HHMM=18:30 PARADA_VAR="$T/var" \
  ALERTA_SERVIDOR=.53 TELEGRAM_TOKEN=x TELEGRAM_CHAT=1 ALERTA_WEBHOOK_URL="http://127.0.0.1:$P/telegram/send_alert?chat_id=689" \
  deploy/pnt-md-hml-horario.sh parar > "$T/saida" 2>&1 || true
[ "$(cat "$T/telegram")" = "webhook 689 [.53 kalman-pnt-md-hml] CRITICO pnt-md-hml: nao zerado ate 18:25 (posicao 1); continua ligado, parar na mao" ] \
  && ok "crítico pelo webhook (vence o Telegram direto), com servidor e serviço" || falha "webhook" "$(cat "$T/saida" "$T/telegram")"
touch "$T/ativo"; parar 18:25 "$Z"
[ $RC = 0 ] && parou && ok "18:25 zerado: para (o limite só vale sem zerar)" || falha "18:25 zerado" "$(cat "$T/saida")"

rm -f "$T/ativo"; parar 14:35 "$POS"
[ $RC = 0 ] && [ ! -e "$T/chamadas" ] && grep -q "ja parado" "$T/saida" && ok "já parado: não consulta nem para" || falha "já parado" "$(cat "$T/saida")"

touch "$T/ativo" "$T/stop-falha"; parar 14:35 "$Z"; rm -f "$T/stop-falha"
[ $RC = 1 ] && grep -q "<2>CRITICO pnt-md-hml: stop pedido e kalman-pnt-md-hml.service continua ativo" "$T/saida" && ok "stop sem efeito: crítico, sai com 1" || falha "stop sem efeito" "$(cat "$T/saida")"

# .gz já existe para o mesmo dia: não sobrescreve, o .jsonl fica
printf 'novo\n' > "$T/var/pnt-md-hml-WINZ26-20261020.jsonl"
touch "$T/ativo"; parar 14:35 "$Z"
[ $RC = 0 ] && [ -e "$T/var/pnt-md-hml-WINZ26-20261020.jsonl" ] && [ "$(zcat "$T/var/pnt-md-hml-WINZ26-20261020.jsonl.gz")" = velho ] \
  && grep -q "ja existe" "$T/saida" && ok ".gz já existente: não sobrescreve" || falha ".gz existente" "$(cat "$T/saida")"

# --- pregao
printf '{"2026-11-20": "Zumbi", "2026-12-25": "feriado"}\n' > "$T/feriados.json"
pregao() { HOJE=$1 PARADA_FERIADOS=$2 deploy/pnt-md-hml-horario.sh pregao > "$T/saida" 2>&1; }
pregao 2026-11-19 "$T/feriados.json" && ok "dia útil: sobe" || falha "dia útil" "$(cat "$T/saida")"
if pregao 2026-11-20 "$T/feriados.json"; then falha "feriado" "$(cat "$T/saida")"; else [ $? = 1 ] && grep -q "feriado (Zumbi)" "$T/saida" && ok "feriado: pula (1)" || falha "feriado código" "$(cat "$T/saida")"; fi
if pregao 2026-11-21 "$T/feriados.json"; then falha "sábado" "$(cat "$T/saida")"; else ok "sábado: pula"; fi
pregao 2026-11-20 "$T/nao-existe.json" && grep -q "sobe sem conferir" "$T/saida" && ok "sem feriados.json: sobe" || falha "sem arquivo" "$(cat "$T/saida")"
printf '{quebrado' > "$T/ruim.json"
pregao 2026-11-20 "$T/ruim.json" && grep -q "ilegivel" "$T/saida" && ok "feriados.json ilegível: sobe" || falha "ilegível" "$(cat "$T/saida")"
if [ -r /var/lib/kalman/feriados.json ]; then
  pregao 2026-12-25 /var/lib/kalman/feriados.json && falha "feriados.json real: 25/12" || ok "feriados.json real: 25/12 pula"
  pregao 2026-10-22 /var/lib/kalman/feriados.json && ok "feriados.json real: 22/10 sobe" || falha "feriados.json real: 22/10" "$(cat "$T/saida")"
fi

[ "$falhas" -eq 0 ] && echo "TUDO OK" || { echo "$falhas falha(s)"; exit 1; }
