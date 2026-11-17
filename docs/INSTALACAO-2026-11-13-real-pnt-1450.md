# REAL pelo PnT, uid 1450, na .53: comunicação pela boleta e o Kalman (sexta, 13/11)

Este roteiro não instala binário: fica a v1.31 (`93245099bd13`). Mudam só o `service.env`, a habilitação do
1450 e o store FIX do REAL. No fim, tudo volta ao que era, e o fix-integration do GQ volta a rodar.

**Decisões (13/11):**
- a sessão FIX é a `GAMMAQ2` → `PNT_DSA_BTG2`. Ela é exclusiva da .53 e não haverá sessão nova;
- o kalman-ordens assume a sessão no lugar do fix-integration do GQ (`~/servicos/PNT/BTG`), que fica
  parado durante o teste. Os dois **nunca** podem estar logados ao mesmo tempo;
- a sequência não é zerada: o kalman-ordens continua a numeração deixada pelo Java;
- só o 1450, só pelo PnT, com 1 contrato. A Flexscan do REAL fica fora hoje (§3);
- depois da ordem de comunicação pela boleta (§6), o Kalman entra no REAL com o ok do usuário na hora
  (§6.2). A janela do 1450 vai até as 13:35.

**Quando:** com o mercado aberto (entradas das 09:00 às 14:00) e sem ordem viva em nenhum módulo. A
sessão FIX do REAL do kalman-ordens só loga entre 08:30 e 17:55 (`REAL_FIX_INICIO` e `REAL_FIX_FIM`).
O kalman-ordens reinicia duas vezes, no §5 e no §7, e cada restart leva uns segundos. O Kalman perde
o `GET /eventos` e volta sozinho. A Flexscan do SIMULADOR loga de novo, como `partida`.

```bash
K=/opt/kalman-ordens/kalman-ordens
F=/etc/kalman-ordens/service.env
B=$F.antes-real-pnt-1311
J=/home/gammaquant/servicos/PNT/BTG          # o fix-integration do GQ
S=/var/lib/kalman-ordens/fix-real             # o store FIX do REAL (REAL_FIX_DIR padrão)
P=FIX.4.4-GAMMAQ2-PNT_DSA_BTG2                # o prefixo dos arquivos do store
```

## 1. O fix-integration parado, sem religar e sem ordem viva

**Situação às 09:02 de 13/11:** ele já estava parado. Às 08:44:41 houve logout limpo dos dois lados:
`35=5` enviado com 34=61 e a resposta `35=5` recebida com 34=62 ("Responding Logout"). O processo não
existe mais. Não há cron, unidade systemd nem outro mecanismo que o religue: ele só sobe à mão, pelo
`$J/startfix`.

Confira de novo antes de seguir:

```bash
ps -eo pid,lstart,cmd | grep '[f]ix-integration'          # nada
ss -tln | grep ':10085'                                    # nada (a porta em que o GQ entrega as ordens)
tail -4 $J/fixpnt.log | tr '\001' '|' | cut -c1-200        # o último bloco é o logout de 08:44:41
```

Se o processo estiver rodando, pare-o com logout: `kill <pid>`, um SIGTERM, nunca `-9`. O log tem
de mostrar "Initiated logout request" e "Received logout response". Depois, leia de novo os números
do §2.

**Ordens vivas nele: nenhuma.** O log tem só duas ordens, as duas terminais:
- `GQ-LS-...:9046691`, de 12/11 às 18:48: recusada (`39=8`, DSA-28, ExpireTime já passado);
- `GQ-LS-...:9046692`, de 13/11: aceita às 08:19:16 e cancelada às 08:19:32 (`39=4`, sem execução).

Confira também na plataforma da BTG, conta 4582351, que não há nada vivo.

**Durante o teste, não mande nada pelo GQ.** Com o Java parado, o envio do GQ para a porta 10085
falha. O L&S do GQ volta no §7.

## 2. A sequência, continuando a do Java

> **Em 13/11 este passo não valeu (§8.2).** O store foi montado em `$S`, mas o QuickFIX/Go o lê em
> `$S/store` (`FileStorePath` = `REAL_FIX_DIR` + `/store`). O logon saiu com 34=1 e o ajuste automático
> levou a 62. Para o próximo teste, ver §9.1: não se monta store à mão.

Os números saem do fim do `fixpnt.log`, que grava com o horário do cabeçalho FIX em UTC:

| | último | o próximo (o que vai no store) |
|---|---|---|
| enviado pelo GQ (`49=GAMMAQ2`) | 34=61 (o logout, 11:44:41 UTC) | **sender 62** |
| recebido do PnT (`49=PNT_DSA_BTG2`) | 34=62 (a resposta do logout) | **target 63** |

Para conferir os mesmos números de novo:

```bash
tr '\001' '|' < $J/fixpnt.log | grep 'outgoing>' | tail -1 | grep -oE '\|34=[0-9]+'   # |34=61
tr '\001' '|' < $J/fixpnt.log | grep 'incoming>' | tail -1 | grep -oE '\|34=[0-9]+'   # |34=62
```

**Atenção, um fato a pesar.** O fix-integration **não** continua a numeração dele. Todo logon do Java
sai com `141=Y` (ResetSeqNumFlag) e `34=1`: assim foi em 12/11 às 21:44 UTC e em 13/11 às 11:15 UTC,
e o PnT aceitou as duas vezes. Ou seja, nessa sessão o PnT aceita o reset no logon. A alternativa ao
store montado à mão é `REAL_FIX_RESET_ON_LOGON=1`, que faz o kalman-ordens logar exatamente como o
Java. Ela fica como plano B, no fim deste §2.

**O store**, montado antes do restart do §5. O QuickFIX/Go lê três arquivos:
- `.senderseqnums` e `.targetseqnums`: o número com 19 dígitos;
- `.session`: a criação do store, em RFC 3339.

A data da criação precisa ser **hoje, depois das 08:30**. Se não for, o QuickFIX/Go considera o store
de outra sessão (`REAL_FIX_INICIO`, no fuso de São Paulo), zera para 1 e loga **sem** `141=Y`.

```bash
sudo ls -la $S 2>&1
#   "Arquivo ou diretório inexistente": normal (o REAL pelo PnT nunca rodou aqui)
#   se existir com arquivos: sudo mv $S $S.antes-1311 (guarda, não apaga)
sudo install -d -o kalman-ordens -g kalman-ordens -m 700 $S
printf '%019d' 62 | sudo tee $S/$P.senderseqnums >/dev/null
printf '%019d' 63 | sudo tee $S/$P.targetseqnums >/dev/null
date --iso-8601=seconds | tr -d '\n' | sudo tee $S/$P.session >/dev/null
sudo touch $S/$P.header $S/$P.body
sudo chown kalman-ordens:kalman-ordens $S/$P.*; sudo chmod 600 $S/$P.*
sudo sh -c "cd $S && head -c 40 $P.senderseqnums $P.targetseqnums $P.session; echo; ls -la"
#   0000000000000000062, 0000000000000000063, 2026-11-13T09:xx:xx-03:00; .header e .body vazios
```

O `.header` e o `.body` vazios significam que o store não tem nenhuma das mensagens do Java. Se o PnT
pedir reenvio (`35=2`) de algum número até 61, o QuickFIX/Go responde com gap fill, que é o certo:
nada do Java deve ser reenviado.

**No logon (§5), o que pode acontecer:**
- **bom:** `35=A` enviado com 34=62, sem `141`, e o `35=A` do PnT com 34=63;
- **o PnT espera outro número no envio** ("MsgSeqNum too low, expecting N"): o kalman-ordens ajusta
  sozinho a próxima sequência para N e tenta de novo. No journal sai "o PnT espera outra sequência;
  ajustada";
- **o PnT recusa de outro jeito, ou fica em laço de logon:** vá ao plano B.

**Plano B (reset, como o Java):** pare o serviço (`sudo systemctl stop kalman-ordens`). No
`service.env` (mesmo método do §3: `$F.novo`, `diff`, `cp`), acrescente `REAL_FIX_RESET_ON_LOGON=1`.
Mova o store de lado (`sudo mv $S $S.seq-1311`) e suba (`sudo systemctl start kalman-ordens`). O
logon sai com 34=1 e `141=Y`.

## 3. O `service.env`

**Antes, a cópia e o que já existe.** Esta leitura não mostra a senha:

```bash
sudo cp -a $F $B
sudo grep -nE '^(AMBIENTES_PERMITIDOS|AMBIENTE_PADRAO|ORDENS_AMBIENTE|REAL_|PRECOS_|ORDENS_SENDER_LOCATION|ORDENS_BOLSA|ORDENS_CONTAS_HML|FLEXSCAN_CONTAS_HML|PNT_DESLIGADO)' $F \
  | sed -E 's/^([^=]*(SENHA|SEGREDO|TOKEN|CHAVE|KEY|USUARIO|DSN)[^=]*=).*/\1(oculto)/I'
```

Mande-me essa saída antes de seguir. Eu confiro três coisas:
- que 4582351 está em `REAL_CONTAS` e não está em `ORDENS_CONTAS_HML` nem em `FLEXSCAN_CONTAS_HML`;
- o preço do PnT. Sem `REAL_PRECOS_URL_PNT`, o REAL recua para `PRECOS_URL_PNT` e depois para
  `PRECOS_URL`, e isso pode apontar para a fonte de homologação (`precos-hml`, porta 9095). Por isso o
  REAL recebe a dele explícita;
- qualquer `REAL_FIX_*` ou `REAL_PNT_*` que já exista.

**O arquivo novo, montado ao lado a partir da cópia.** As chaves abaixo são trocadas, se existirem, ou
acrescentadas no fim. As `REAL_FLEXSCAN_*` ficam comentadas hoje (`#pnt1311#`). As seis `REAL_FIX_*`
entram vazias, e você digita os valores no arquivo novo, num editor.

```bash
sudo awk -v OFS='' '
BEGIN {
  v["AMBIENTES_PERMITIDOS"]="SIM,REAL"; v["REAL_UIDS"]="1450"
  v["REAL_LIMITE_QTD_ORDEM"]="1"; v["REAL_LIMITE_POSICAO_MAX"]="1"; v["REAL_LIMITE_ENTRADAS_DIA"]="3"
  v["REAL_LIMITE_PERDA_DIA_PTS"]="500"
  v["REAL_PRECOS_URL_PNT"]="http://172.16.110.53:9090"; v["REAL_ORDENS_SENDER_LOCATION"]="172.16.110.1"
  v["REAL_FIX_SENDER_COMP_ID"]=""; v["REAL_FIX_TARGET_COMP_ID"]=""; v["REAL_FIX_HOST"]=""
  v["REAL_FIX_PORTA"]=""; v["REAL_FIX_USUARIO"]=""; v["REAL_FIX_SENHA"]=""
  n=split("AMBIENTES_PERMITIDOS REAL_UIDS REAL_LIMITE_QTD_ORDEM REAL_LIMITE_POSICAO_MAX REAL_LIMITE_ENTRADAS_DIA REAL_LIMITE_PERDA_DIA_PTS REAL_PRECOS_URL_PNT REAL_ORDENS_SENDER_LOCATION REAL_FIX_SENDER_COMP_ID REAL_FIX_TARGET_COMP_ID REAL_FIX_HOST REAL_FIX_PORTA REAL_FIX_USUARIO REAL_FIX_SENHA", ordem, " ")
}
/^REAL_FLEXSCAN_/ { print "#pnt1311# ", $0; next }
{ k=$0; sub(/=.*/, "", k) }
(k in v) && /^[A-Z_]+=/ { print k, "=", v[k]; feito[k]=1; next }
{ print }
END {
  print ""; print "# 13/11: teste REAL pelo PnT, uid 1450 (INSTALACAO-2026-11-13-real-pnt-1450.md); volta pela copia"
  for (i=1; i<=n; i++) if (!(ordem[i] in feito)) print ordem[i], "=", v[ordem[i]]
}' $B | sudo tee $F.novo >/dev/null
sudo chmod 600 $F.novo
sudo vi $F.novo        # preencha só as seis REAL_FIX_* (abaixo)
```

| chave | valor (a sessão do fix-integration, `quickfixj-client-pnt.cfg`) |
|---|---|
| `REAL_FIX_SENDER_COMP_ID` | `GAMMAQ2` |
| `REAL_FIX_TARGET_COMP_ID` | `PNT_DSA_BTG2` (o padrão do serviço é `PNT_DSA`, que está errado aqui) |
| `REAL_FIX_HOST` | `192.168.151.13` |
| `REAL_FIX_PORTA` | `42051` |
| `REAL_FIX_USUARIO` | o 553 do Java (`GEORGEG`) |
| `REAL_FIX_SENHA` | a senha da sessão (o 554), que só você digita |

**O porquê das outras chaves:**
- `REAL_LIMITE_PERDA_DIA_PTS=500` (decidido em 13/11): R$ 100 em 1 contrato de WIN. Ao chegar lá, o
  serviço faz `PARADA BLOQUEAR` sozinho. Isso não zera a posição, mas a stop da corretora já está posta.
- `REAL_LIMITE_ENTRADAS_DIA=3`: o total do dia na instância REAL, e a entrada da boleta (§6) conta.
  Sobram 2 entradas para o Kalman até as 13:35. Depois da terceira, a entrada seguinte é recusada.
- `REAL_ORDENS_SENDER_LOCATION=172.16.110.1`: o 142 que o Java manda em toda ordem. É bom repetir o
  que o PnT já aceita.
- `REAL_PRECOS_URL_PNT`: a mesma fonte da Flexscan do SIMULADOR (o marketdata da .53).
- As `REAL_FLEXSCAN_*` comentadas: hoje o REAL fica só com o PnT (ver "A Flexscan do REAL na v1.31",
  no fim).

**Conferir e gravar.** No `diff` não podem aparecer senhas: nem a nova nem as das `REAL_FLEXSCAN_*` comentadas. Por isso elas são filtradas:

```bash
sudo diff $B $F.novo | sed -E 's/^([^=]*(SENHA|SEGREDO|TOKEN|CHAVE|KEY|USUARIO|DSN)[^=]*=).*/\1(oculto)/I'   # segredos ocultos, comentados ou não
#   esperado: AMBIENTES_PERMITIDOS SIM → SIM,REAL; REAL_UIDS 5725 → 1450; os 4 limites; as
#   REAL_FLEXSCAN_* com #pnt1311#; no fim, o comentário e as chaves novas (REAL_FIX_* preenchidas)
sudo grep -c '^REAL_FIX_SENHA=.' $F.novo                   # 1 (preenchida)
sudo cp $F.novo $F && sudo rm $F.novo
```

Se o `diff` mostrar mais do que isso, não copie. Apague com `sudo rm $F.novo` e me mande o `diff`.

## 4. Habilitar o 1450 no REAL, só pelo PnT

A habilitação vale só quando o REAL é permitido, então pode ser feita antes do restart:

```bash
sudo $K usuario habilitar -uid 1450 -ambiente REAL -conectores pnt -qtd-max 1 -posicao-max 1 -motivo "teste REAL PnT 13/11"
sudo $K usuario conferir -uid 1450
#   REAL: habilitado, qtd_max 1, posicao_max 1, conectores pnt
#   acusuario (usutxtconfigprd): CORSeqId 85 (BTG), conta 4582351, Conector vazio ou PNT
#   Conector=FLEXSCAN no GQ: dá CONECTOR_NAO_HABILITADO; grave no GQ "Conector":"PNT" ou me chame
```

## 5. Restart e conferência

```bash
sudo systemctl restart kalman-ordens
sleep 10; systemctl is-active kalman-ordens                 # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -iE 'ambientes|pnt|fix|logon|sequ|concilia|mass|ERROR' | cut -c1-220
#   bom: "ambientes" permitidos=[SIMULADOR REAL] real_uids=[1450]; o logon da sessão GAMMAQ2;
#        "mass status: nenhuma conta" ou a conciliação 0 ordem(ns), posição 0
#   ruim: o serviço não sobe (o erro diz a chave) → §7.3 já (env de volta) e me mande o journal
#   logon recusado ou em laço → plano B do §2
curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos, real: .ambientes.REAL | {canais, conciliando, divergente, posicao, sessao_logada, pnt_desligado, fonte, idade_preco_ms}}'
#   ambientes_permitidos ["SIMULADOR","REAL"]; canais.PNT.estado "NO_AR"; sessao_logada true;
#   conciliando false, divergente false, posicao 0; pnt_desligado false; idade_preco_ms baixa
sudo sh -c "head -c 19 $S/$P.senderseqnums; echo; head -c 19 $S/$P.targetseqnums; echo"
#   os números andaram a partir de 62 e 63 (o logon e os heartbeats)
curl -s '127.0.0.1:9092/sessao/ao-vivo' | jq -r '.[-6:][]? // .' | cut -c1-200     # as últimas mensagens do fio
$K boleta destino -uid 1450 -ambiente REAL -conta 4582351 -corretora BTG
#   pode_enviar true, conector PNT, corretora BTG, conta 4582351
curl -s 127.0.0.1:9092/saude | jq '.ambientes.SIMULADOR.flexscan | {logado, ultimo_login}'   # SIMULADOR intacto
```

## 6. A ordem de comunicação, pela boleta

Uma compra de 1 contrato, 1.000 pontos abaixo do bid (múltiplo de 5), com stop de 300:

```bash
curl -s '172.16.110.53:9090/precos/atual?ativo=WINFUT' | jq '.WINFUT | {ticker, bid, ask, idade_seg}'
BID=<o bid de agora>; PR=$(( (BID - 1000) / 5 * 5 ))
$K boleta enviar -uid 1450 -ambiente REAL -conta 4582351 -corretora BTG -lado C -qtd 1 -stop 300 -preco $PR -por 1450 -motivo "teste REAL PnT"
#   mostra o destino (REAL · PnT · BTG 4582351) e pede "sim"; a resposta traz aceito true e operacao_id
OP=<operacao_id>
$K boleta estado -op $OP
#   ENTRANDO; a ordem E com o 37 do PnT, preço $PR, qtd_exec 0
```

Confira na plataforma da BTG, conta 4582351, a compra de 1 WIN a $PR. A stop (1014) só é posta
depois da execução da entrada, então com a ordem longe do preço **não** aparece stop na BTG. Isso é o
esperado.

```bash
$K boleta cancelar -op $OP -por 1450 -motivo "fim do teste"
$K boleta estado -op $OP
#   em poucos segundos: ENCERRADA_SEM_ENTRADA, a ordem CANCELADA
curl -s 127.0.0.1:9092/saude | jq '.ambientes.REAL.posicao'          # 0
```

Confira na BTG que a ordem está cancelada.

**Se a ordem executar antes do cancelamento** (o mercado caindo 1.000 pontos): a stop de 300 é
posta sozinha, e você sai com `$K boleta encerrar -op $OP -por 1450 -motivo "fim do teste"`.

**Para parar tudo na hora**, em qualquer momento:
- `sudo $K parar -ambiente REAL -motivo "..."` recusa entradas novas e cancela as vivas;
- `sudo $K zerar -ambiente REAL -motivo "..."` faz isso e também sai da posição, com uma limitada colada
  no bid/ask e repreçada. Não é ordem a mercado.

### 6.2 O Kalman no REAL (só com o ok do usuário, depois do §6 passar)

Condições para começar:
- a ordem da boleta aceita na BTG e cancelada;
- a posição REAL 0;
- `boleta destino` ainda com `pode_enviar` true;
- antes das 13:35.

**Ligar:** na tela do Kalman do 1450 (WIN), ligue as "ordens REAIS" e confirme a conta 4582351 quando a
tela pedir. A tela confere o destino no kalman-ordens antes de gravar. Se recusar, o texto diz o
código, por exemplo `CONECTOR_NAO_HABILITADO` ou `CONFIRMAR_CONTA`.

O Kalman manda 1 contrato fixo, sempre com `stop_pts`. A janela do 1450 vai até as 13:35, e o serviço
aceita entradas até as 14:00 e zera às 14:30.

**Acompanhar:**

```bash
journalctl -u kalman --since '-5min' --no-pager | grep -E 'ordens:|REAL' | tail -20 | cut -c1-200
#   "lado de ordens REAL do usuário 1450 ligado"; nas decisões, o POST da ENTRADA e a resposta
curl -s 127.0.0.1:9092/saude | jq '.ambientes.REAL | {posicao, limites, alertas_criticos, divergente, parada}'
curl -s "127.0.0.1:9092/operacoes?dia=$(date +%F)" | jq -c '.[] | {id, modulo, estado, lado, qtd, preco_medio, stop}' 2>/dev/null
```

Em cada entrada executada, a stop (1014) tem de aparecer na BTG em poucos segundos. Se não aparecer,
sai o crítico `SEM_PROTECAO` no Telegram.

**Parar na hora:**
- `sudo $K zerar -ambiente REAL -motivo "..."` sai da posição, com uma limitada colada e repreçada;
- `sudo $K parar -ambiente REAL -motivo "..."` só barra entradas novas e cancela as vivas.

Desligar as ordens REAIS na tela do Kalman barra decisões novas, mas uma operação viva continua sendo
gerida até fechar.

## 7. O fechamento (sempre, mesmo se algo falhar no meio)

**7.0 O Kalman fora do REAL** (até as 13:35, ou antes se algo der errado): desligue as "ordens REAIS"
do 1450 na tela do Kalman. Com operação viva, espere ela fechar, ou feche com `boleta encerrar` ou
`zerar -ambiente REAL`. Isso vem **antes** do 7.3: com o REAL tirado do env, um lado REAL ainda ligado
no Kalman só recebe recusas.

```bash
journalctl -u kalman --since '-5min' --no-pager | grep -E 'ordens:.*REAL' | tail -5 | cut -c1-200   # desligado
```

**7.1 Nada vivo:**

```bash
curl -s 127.0.0.1:9092/saude | jq '.ambientes.REAL | {posicao, ordens_vivas, operacoes_suspensas}'   # 0, nada, []
```

Na BTG, nenhuma ordem viva na conta 4582351. Se houver posição, primeiro `boleta encerrar`, ou
`zerar -ambiente REAL`.

**7.2 Desabilitar o 1450:**

```bash
sudo $K usuario desabilitar -uid 1450 -ambiente REAL -motivo "fim do teste 13/11"
```

**7.3 O `service.env` de volta e a sessão do kalman-ordens fora:**

```bash
sudo cp $B $F
sudo systemctl restart kalman-ordens
sleep 10; curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos}'            # ["SIMULADOR"]
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -iE 'pnt|logout|ERROR' | cut -c1-200
#   o logout da GAMMAQ2 sai no stop do serviço; o SIMULADOR sobe sem PnT, como antes
sudo sh -c "head -c 19 $S/$P.senderseqnums; echo; head -c 19 $S/$P.targetseqnums; echo"
#   anote os últimos números (só para o registro: o Java loga com 141=Y e zera)
```

**7.4 Religar o fix-integration do GQ** (só depois do 7.3, para não haver dois logados):

```bash
ps -eo pid,cmd | grep '[k]alman-ordens servir'               # o serviço novo, já sem o REAL
cd $J && ./startfix
sleep 15; tail -20 $J/fixpnt.log | tr '\001' '|' | sed -E 's/\|554=[^|]*/|554=***/' | grep -E '35=A|35=5|Exception' | cut -c1-200
#   o 35=A enviado (34=1, 141=Y) e o 35=A do PnT: o GQ volta a enviar
ss -tln | grep ':10085'                                       # LISTEN
```

O store `$S` fica onde está, guardado para um próximo teste ou para mover de lado. Ele não é usado
com o REAL desligado.

## A Flexscan do REAL na v1.31

**Na v1.31**, a ponte REAL/BTG roda sem webhook, porque a `REAL_FLEXSCAN_BTG_WEBHOOK_URL` está vazia
(D5). Mesmo assim, ela espera um webhook depois de cada 202, e 5 s depois sai o
`FLEXSCAN_WEBHOOK_ATRASADO`, que é falso (SPEC-ORDENS-v1.32 §1; aconteceu em 03/11). A v1.32
corrige isso.

**Hoje isso não aparece:** só há 202 com uma ordem REAL pela Flexscan, e o 1450 está habilitado só
com `pnt` (o 5725 sai de `REAL_UIDS`). O que sobraria é a ponte REAL logando e cumprindo a agenda dela
(login e renovações em `btg_gammaquant`), sem ordem.

**Como evitar de vez hoje:** as `REAL_FLEXSCAN_*` vão comentadas no §3. O REAL sobe só com o
conector PnT (`REAL_FIX_HOST` basta como conector REAL), e a BTG continua conhecida (o 85 é uma das
corretoras de sempre). Na volta (§7.3), a cópia restaura as linhas.

## 8. O resultado de 13/11

**8.1 O que aconteceu** (tudo na conta 4582351, WINZ26, 1 contrato; horários de Brasília):

| hora | o quê |
|---|---|
| 09:31:41 | restart com o REAL (v1.31); logon da `GAMMAQ2` com 34=1, "MsgSeqNum too low, expecting 62"; ajustado e logado (§8.2) |
| 09:35:58 | boleta 1, a ordem de comunicação: recusada, `[DSA-3] Robot not found` (faltavam o 20858 e o 20859) |
| 11:00:14 | v1.31.1 (`117b8bfefcd4`) instalada; "pnt: sessão" com `robot_id_20858=BTG_ALGO entering_firm_20859=BTGDAYTRADE`; o logon continuou a sequência do store, sem ajuste |
| 11:02:06 | boleta 2 (`KM-BOLETA-E-1113110159-1`, compra a 172550): aceita; às 11:02:24, encerrada `CANCELADA` |
| 11:14:17 | boleta 3: aceita; cancelada pela CLI às 11:16 ("fim do teste"). **A comunicação passou** |
| 11:21, 11:27 | duas entradas do Kalman recusadas, `ENTRADAS_POR_DIA` (3): as 3 boletas, contando a recusada, ocupavam o limite (§8.3) |
| 11:28:50 | restart com `REAL_LIMITE_ENTRADAS_DIA=6` |
| 11:29–11:30 | **op 234750**, compra a 173215; stop na corretora (1014, stop 172715 / limite 172615); saída `MANUAL` **por replace** da 1014 (G, degrau 1) a 173340: **+125 pts** |
| 11:33–11:36 | **op 234752**, compra; stop na corretora; **ajuste confirmado** (`PROTECAO_AJUSTADA`, stop 173155 → 173555); saída por replace (`SINAL_OPOSTO`) a 173580: **−75 pts** |
| 11:36–11:40 | **op 234754**, venda (a virada); stop na corretora; ajuste confirmado (stop 174085 → 173835); **o stop executou na corretora** (`STOP_SERVIDOR`) a 173790: **−205 pts** |
| 11:40, 11:41 | duas entradas recusadas, `ENTRADAS_POR_DIA` (6) |
| 11:42:19 | fechamento: 1450 desabilitado no REAL, env de volta pela `$B`, `ambientes_permitidos ["SIMULADOR"]` |

Resultado do Kalman: 3 operações, −155 pts (abaixo da perda do dia de 500). Confirmados no REAL:
- a entrada (D 1031) e a proteção (D 1014) aceitas com o robô e o OMS de day trade;
- a stop na corretora e o ajuste dela (G);
- a saída por replace da 1014;
- a execução do stop pela própria corretora.

O fix-integration do GQ **não** foi religado (o §7.4 não se aplica hoje). Ele estava parado antes do
teste e só tinha subido para o L&S das 08:19.

**8.2 Por que o store 62/63 não foi usado.** O QuickFIX/Go guarda o store em
`<REAL_FIX_DIR>/store/` (`pnt/sessao.go`: `FileStorePath = Dir + "/store"`). O §2 gravou os arquivos
em `$S` mesmo, um nível acima. Na partida, o QuickFIX/Go não achou store em `$S/store`, criou um novo
(sender 1, target 1) e logou com 34=1, sem `141=Y`. O PnT respondeu "MsgSeqNum too low, expecting 62",
e o ajuste automático (v1.3) acertou o próximo número para 62 e logou de novo. Do lado de lá, o PnT
mandou o 35=A com o número dele, maior que 1, e o QuickFIX/Go aceitou o salto (4 mensagens na
sincronia de 500 ms, nenhum `ResendRequest` do PnT). A prova de que o caminho era o problema: nos
restarts das 11:00 e das 11:28, o mesmo serviço continuou a sequência do `$S/store` sem ajuste nenhum.
Os arquivos de `$S` (sem o `store/`) ficaram sobrando e podem ser removidos.

**8.3 O limite de entradas e a boleta.** O contador do dia (`entradasHoje`) é um só por instância e
conta toda operação do dia, de qualquer módulo. Na partida, ele é recarregado como o número de
operações do dia (`len(m.ops)`), o que inclui a boleta recusada pelo DSA-3. Por isso as 3 boletas
encheram o limite de 3 antes da primeira entrada do Kalman. A análise de contar por módulo está no
§9.2.

**8.4 Os tempos do PnT no REAL** (pelo journal; o stop da 234754 com os horários do Profit):

| o quê | do pedido à resposta |
|---|---|
| entrada 1031 agressiva: decidida → executada | 1,56 s; 0,99 s; 1,36 s |
| 1014 posta: decidida → aceita (39=0) | 0,23 s; 0,33 s; 0,25 s |
| ajuste da 1014 (G): decidido → confirmado | 0,09 s; 0,13 s |
| saída por replace da 1014: decidida → executada | 1,40 s; 1,81 s |
| stop da 234754: gatilho tocado (11:40:12.838) → 39=B (14.827) → execução no serviço (15.101) | 2,0 s; 2,26 s no total |

Na HML, em 19/10, o mesmo tempo foi 2,13 s. Os acks são rápidos (0,1 a 0,3 s), e toda **execução**
pelo robô leva de 1 a 1,8 s, inclusive a da entrada 1031, que não tem gatilho nenhum. O stop gasta uns
0,5 s a mais que isso. O grosso do tempo é o caminho robô → bolsa da DSA, não o gatilho da 1014.

Na 234754 (venda, stop de compra com gatilho 173835 e limite 173935), a execução saiu a 173790,
45 pts melhor que o gatilho: o preço voltou durante os 2 s. A demora pode custar ou ajudar.

**8.5 PnT × Flexscan, as mesmas etapas** (medianas do journal da .53, 20/10 a 13/11; entre
parênteses, o p90 e o n):

| etapa | PnT REAL (13/11) | PnT HML | Flexscan SIMULADOR | Flexscan REAL |
|---|---|---|---|---|
| entrada: decidida → executada | 1,36 s (n=3) | 1,94 s (p90 2,88; n=38) | 0,34 s (p90 1,56; n=73) | — |
| proteção: decidida → aceita | 0,25 s (n=3) | 0,25 s (p90 0,55; n=38) | 0,30 s (p90 0,34; n=73) | — |
| ajuste do stop: decidido → confirmado | 0,11 s (n=2) | 0,40 s (p90 0,71; n=19) | 0,37 s (p90 0,42; n=72) | — |
| saída: decidida → executada | 1,60 s, por replace (n=2) | 1,45 s, por replace (p90 2,17; n=38) | 0,30 s, ordem nova (p90 0,57; n=31) | — |
| stop: gatilho tocado → executado | 2,26 s (n=1) | 2,13 s (19/10, n=1) | não medido | — |

Como ler a tabela:
- **Flexscan REAL não tem dado.** Houve uma ordem só, a boleta de 03/11, recusada ("Conta nao
  contratada") 0,35 s depois de decidida. Não houve execução, proteção nem ajuste.
- **O SIMULADOR da Flexscan executa contra um casamento simulado (apisim)**, não contra a B3. As
  execuções de lá não medem a bolsa. A comparação justa é das confirmações (proteção e ajuste), e
  nelas os dois conectores ficam em 0,1 a 0,4 s.
- **O stop não tem gatilho no journal.** O horário do toque vem de fora (o Profit), por isso só há os
  dois casos medidos à mão.
- **A comparação de execução PnT × Flexscan no REAL** só vai existir depois de um teste REAL pela
  Flexscan, com o Flexscan contratado na 4582351.

## 9. Para o próximo teste REAL pelo PnT

> **OBSOLETO desde 16/11 (v1.35 `09872ead98f2` instalada na .53, `INSTALACAO-2026-11-16-v1.35.md`).**
> Duas partes deste §9 não funcionam mais na v1.35:
> - **o `REAL_UIDS`/`REAL_CONTAS` no env:** com valor, o serviço não sobe;
> - **o `usuario habilitar -ambiente REAL`:** a CLI recusa.
>
> A aprovação do REAL agora é pela tela (`POST /usuarios/{uid}/real/aprovar`), e o 1450 está em
> `SEM_APROVACAO` desde a 0019. O próximo REAL pelo PnT segue o roteiro do 1º dia REAL, a ser escrito
> depois das telas do Kalman.
>
> Continuam valendo como referência:
> - a sequência FIX (9.1);
> - o bloco `REAL_FIX_*`/`REAL_PRECOS_*` do env, sem o `REAL_UIDS`;
> - os limites, hoje em 1/1/600/30/2;
> - o fix-integration do GQ parado antes.

**9.1 A sequência: não montar store à mão.** O §2 sai do roteiro. Em outro dia, o store guardado em
`$S/store` é de outra sessão (a criação é de 13/11): na partida depois das 08:30, o QuickFIX/Go o zera
sozinho e loga com 34=1. Daí em diante vale o que funcionou em 13/11 às 09:31:
- o PnT espera mais que 1 ("too low, expecting N"): o ajuste automático vai para N e loga;
- o PnT espera exatamente 1 (o Java deslogou com reset, ou houve reset do dia do lado deles): o logon
  passa direto.

O que olhar no journal: "o PnT espera outra sequência; ajustada" (uma vez) e "conector de volta
conector=PNT". O plano B (`REAL_FIX_RESET_ON_LOGON=1`, logon com 141=Y, como o Java) continua para o
caso de recusa de outro tipo ou laço de logon. Se o teste for no mesmo dia em que o Java rodou, vale o
mesmo: o store do kalman-ordens estará atrasado, e o ajuste cobre. Se for preciso montar o store à mão
algum dia, ele vai em `$S/store/`, nunca em `$S`.

Continua valendo a regra da sessão única: a `GAMMAQ2` logada em um lado só. Antes do restart com o
REAL, o fix-integration parado (§1).

**9.2 O limite de entradas por módulo (BOLETA x KALMAN): aprovado em 13/11, feito na v1.33 §2
(roteiro 130), ainda não instalado.** O
`REAL_LIMITE_ENTRADAS_DIA` existe para pegar um laço do cliente automático (o Kalman mandando entradas
sem parar). A boleta é manual, uma a uma, com o "sim" na CLI. Somar as duas faz o teste da corretora
consumir o limite do robô, que foi o que aconteceu às 11:21. A regra da v1.33 (SPEC-ORDENS-v1.33 §2;
só vale depois de instalada):
- o contador do dia passa a ser por módulo, e cada módulo tem o mesmo teto `LIMITE_ENTRADAS_DIA`
  (a boleta continua limitada, contra repetição por engano);
- na partida, a recarga conta as operações do dia por módulo;
- o `/saude` mostra `entradas_hoje` por módulo;
- **continuam globais da instância:** a posição máxima, a qtd por ordem, as entradas por minuto e a
  **perda do dia** (o dinheiro é um só: uma perda da boleta conta para parar o Kalman).

Até lá, no próximo teste: `REAL_LIMITE_ENTRADAS_DIA` = as boletas previstas + as entradas do Kalman
(por exemplo, 2 + 3 = 5). A boleta recusada pela corretora também conta.

**9.3 O que tem de voltar ao `service.env`.** A cópia `$B` (`service.env.antes-real-pnt-1311`) é a
base, de novo, com o mesmo método: cópia, arquivo novo ao lado, `diff` filtrado, `cp`. Sobre o awk do
§3 entram:

| chave | valor | por quê |
|---|---|---|
| `AMBIENTES_PERMITIDOS` | `SIM,REAL` | |
| `REAL_UIDS` | `1450` | |
| `REAL_FIX_SENDER_COMP_ID` | `GAMMAQ2` | as seis `REAL_FIX_*` digitadas por você (tabela do §3) |
| `REAL_FIX_TARGET_COMP_ID` | `PNT_DSA_BTG2` | o padrão `PNT_DSA` está errado aqui |
| `REAL_FIX_HOST` / `REAL_FIX_PORTA` | `192.168.151.13` / `42051` | |
| `REAL_FIX_USUARIO` / `REAL_FIX_SENHA` | o 553 / o 554 | só você digita |
| `REAL_PNT_ROBOT_ID` | `BTG_ALGO` | **novo**: sem ele, `[DSA-3] Robot not found` (v1.31.1 ou v1.32) |
| `REAL_PNT_ENTERING_FIRM` | `BTGDAYTRADE` | **novo**: o OMS de day trade; o `BTGCEDRO` é o do L&S |
| `REAL_LIMITE_QTD_ORDEM` / `REAL_LIMITE_POSICAO_MAX` | `1` / `1` | |
| `REAL_LIMITE_ENTRADAS_DIA` | boletas + Kalman (§9.2) | 3 barrou o Kalman em 13/11 |
| `REAL_LIMITE_PERDA_DIA_PTS` | `500` | |
| `REAL_PRECOS_URL_PNT` | `http://172.16.110.53:9090` | |
| `REAL_ORDENS_SENDER_LOCATION` | `172.16.110.1` | o 142 do Java |
| `REAL_FLEXSCAN_*` | comentadas (`#pnt1311#`) | o REAL só pelo PnT |

E a habilitação de novo (§4): `usuario habilitar -uid 1450 -ambiente REAL -conectores pnt -qtd-max 1
-posicao-max 1`. Ela foi desfeita no §7.2.

Na partida, confira a linha "pnt: sessão": `robot_id_20858=BTG_ALGO entering_firm_20859=BTGDAYTRADE`.
Se vier vazio, as duas `REAL_PNT_*` não entraram, e a primeira ordem volta com o DSA-3.

**9.4 A margem no OMS de day trade (a lição de 13/11).** As ordens do Kalman vão para o OMS
`BTGDAYTRADE` (o 20859), que é separado do `BTGCEDRO` do L&S. Em 13/11, a conta 4582351 não estava
preparada para day trade no REAL: não havia margem alocada nesse OMS.
- A boleta 2 (11:02:06) foi aceita pelo kalman-ordens e **recusada no registro** por falta de margem,
  mesmo longe do preço. O drop copy do Profit registrou: "This strategy was changed to Error state,
  because offer RealOrder Id: 178152086 is Rejected".
- Para o kalman-ordens, ela apareceu só como `CANCELADA` às 11:02:24, sem o motivo.

**Antes de qualquer teste REAL:** aloque margem de day trade na conta, no OMS `BTGDAYTRADE`. A margem
do L&S não serve. A ordem de comunicação (§6) confere isso: ela tem de ficar viva (39=0) até o
cancelamento.
