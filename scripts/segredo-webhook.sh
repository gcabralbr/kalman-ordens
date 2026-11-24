#!/bin/bash
# O segredo do webhook da Flexscan REAL sem aparecer na tela (24/11: o segredo REAL vazou numa conversa).
# O segredo nunca vai para a saída, para argumento de comando (o ps mostra) nem para arquivo fora do
# env: passa de um processo a outro pelo ambiente (o /proc/<pid>/environ é só do dono). Roda com sudo,
# porque o service.env é do root. Roteiro: docs/REAL-2026-11-24-webhook-flexscan-real.md.
#
#   segredo-webhook.sh novo ARQ CORRETORA BASE      gera o segredo novo (44 caracteres base64url) e grava
#                                                   em ARQ a REAL_FLEXSCAN_<C>_WEBHOOK_SEGREDO e a
#                                                   _WEBHOOK_URL = BASE + o prefixo + o segredo
#   segredo-webhook.sh sem-url ARQ CORRETORA        esvazia a _WEBHOOK_URL (a volta: sem cadastro)
#   segredo-webhook.sh compara ARQ1 CHAVE1 ARQ2 CHAVE2     "iguais" ou "diferentes" (ou "ausente")
#   segredo-webhook.sh confere-url ARQ CORRETORA    a URL termina no prefixo + o segredo? (o host aparece)
#   segredo-webhook.sh procura ARQ CHAVE DIR...     os arquivos em DIR... que contêm o valor da CHAVE
#                                                   de ARQ (só os nomes)
#
# Só mexe em linhas ativas (sem # na frente). Cada chave tem de aparecer ativa uma vez só.
set -euo pipefail
umask 077

erro() { echo "segredo-webhook: $*" >&2; exit 2; }

# valor CHAVE ARQ: o valor da linha ativa CHAVE=..., sem aspas em volta; vazio se não houver
valor() {
  awk -v k="$1" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2); n++ }
    END { if (n > 1) exit 3; gsub(/^["\x27]|["\x27]$/, "", v); printf "%s", v }' "$2" ||
    erro "$1 ativa mais de uma vez em $2"
}

# troca ARQ: lê o par CHAVE/VALOR das variáveis T_CHAVE1/T_VALOR1 (e T_CHAVE2/T_VALOR2, se houver) e
# reescreve as linhas ativas; o arquivo mantém dono e modo (cat por cima, não mv)
troca() {
  local arq=$1 tmp
  tmp=$(mktemp "$arq.XXXXXX")
  trap 'rm -f "$tmp"' EXIT
  awk 'BEGIN { k1 = ENVIRON["T_CHAVE1"]; v1 = ENVIRON["T_VALOR1"]; k2 = ENVIRON["T_CHAVE2"]; v2 = ENVIRON["T_VALOR2"] }
    k1 != "" && index($0, k1 "=") == 1 { print k1 "=" v1; n1++; next }
    k2 != "" && index($0, k2 "=") == 1 { print k2 "=" v2; n2++; next }
    { print }
    END { if (n1 != 1 || (k2 != "" && n2 != 1)) exit 3 }' "$arq" > "$tmp" ||
    erro "em $arq, cada chave tem de estar ativa uma vez só (${T_CHAVE1:-} ${T_CHAVE2:-}); nada mudou"
  cat "$tmp" > "$arq"
  rm -f "$tmp"
  trap - EXIT
}

prefixo() { # ARQ C: o prefixo da rota do REAL dessa corretora (o padrão do config/corretoras.go)
  local p
  p=$(valor "REAL_FLEXSCAN_$2_WEBHOOK_PREFIXO" "$1")
  printf "%s" "${p:-/flexscan/webhook-real-$(echo "$2" | tr '[:upper:]' '[:lower:]')/}"
}

cmd=${1:-}
case "$cmd" in
novo)
  [ $# -eq 4 ] || erro "novo ARQ CORRETORA BASE (ex.: novo \$F.webhook-2511 BTG https://host)"
  arq=$2 c=$3 base=${4%/}
  [ -f "$arq" ] || erro "$arq não existe"
  case "$base" in https://*) ;; *) erro "BASE começa com https://" ;; esac
  case "$base" in */flexscan*) erro "BASE é https://host[/caminho do proxy], sem o /flexscan (vem do prefixo)" ;; esac
  T_VALOR1=$(head -c 33 /dev/urandom | base64 -w0 | tr '+/' '-_')
  [ ${#T_VALOR1} -eq 44 ] || erro "segredo gerado com ${#T_VALOR1} caracteres"
  export T_VALOR1 T_CHAVE1="REAL_FLEXSCAN_${c}_WEBHOOK_SEGREDO" T_CHAVE2="REAL_FLEXSCAN_${c}_WEBHOOK_URL"
  export T_VALOR2="$base$(prefixo "$arq" "$c")$T_VALOR1"
  troca "$arq"
  echo "ok: $T_CHAVE1 novo (44 caracteres); $T_CHAVE2=$base$(prefixo "$arq" "$c")(oculto)"
  ;;
sem-url)
  [ $# -eq 3 ] || erro "sem-url ARQ CORRETORA"
  export T_CHAVE1="REAL_FLEXSCAN_$3_WEBHOOK_URL" T_VALOR1="" T_CHAVE2=""
  troca "$2"
  echo "ok: $T_CHAVE1 vazia em $2"
  ;;
compara)
  [ $# -eq 5 ] || erro "compara ARQ1 CHAVE1 ARQ2 CHAVE2"
  a=$(valor "$3" "$2")
  b=$(valor "$5" "$4")
  if [ -z "$a" ] || [ -z "$b" ]; then echo "ausente"
  elif [ "$a" = "$b" ]; then echo "iguais"
  else echo "diferentes"; fi
  ;;
confere-url)
  [ $# -eq 3 ] || erro "confere-url ARQ CORRETORA"
  s=$(valor "REAL_FLEXSCAN_$3_WEBHOOK_SEGREDO" "$2")
  u=$(valor "REAL_FLEXSCAN_$3_WEBHOOK_URL" "$2")
  p=$(prefixo "$2" "$3")
  if [ -z "$u" ]; then echo "URL vazia (sem webhook)"; exit 0; fi
  host=${u%%"$p"*}
  if [ -n "$s" ] && [ "$u" = "$host$p$s" ]; then echo "sim: $host$p(segredo, ${#s} caracteres)"
  else echo "NÃO: a URL não é $host$p + o segredo da _WEBHOOK_SEGREDO"; exit 1; fi
  ;;
procura)
  [ $# -ge 4 ] || erro "procura ARQ CHAVE DIR..."
  arq=$2 chave=$3; shift 3
  v=$(valor "$chave" "$arq")
  [ ${#v} -ge 32 ] || erro "$chave de $arq vazia ou curta (${#v}); nada procurado"
  pad=$(mktemp)
  trap 'rm -f "$pad"' EXIT
  printf "%s\n" "$v" > "$pad"
  achou=$(grep -rlF -f "$pad" "$@" 2>/dev/null | grep -vxF "$pad" | sort -u || true)
  echo "${achou:-(nenhum arquivo)}"
  ;;
*)
  sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
