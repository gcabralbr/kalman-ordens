# Webhook da Flexscan no REAL (BTG): o segredo novo, a rota no nginx e o teste

Preparado em 24/11. **Revisto em 26/11 para o dia D = 27/11** (decisão do George: o webhook REAL liga
**antes** do HMAC, sem modo de assinatura; a `PRECOS_SAUDE_URL` sai no mesmo restart). **Executado
antes, em 26/11 (D=2611), com o E5 às 15:25:48; T1–T3 em 27/11 às 09:00. O registro está no §7**: onde o
texto diz 27/11, leia 26/11; onde diz 30/11, leia 27/11.
Os comandos com `sudo` são do George: **N** no host do proxy público, **F** e **E** na .53,
**V** a volta; a lista está no §6. Todo comando que mostra o `service.env` termina no filtro de
segredos, **aqui com `WEBHOOK_URL` a mais**, porque a URL do webhook carrega o segredo (§2.3).

**As datas:**
- **27/11 (sexta), dia D:** N1 e E0–E4 a qualquer hora; o restart (E5) **depois da zeragem (14:30) e
  antes das 17:55**, com posição 0; E6 e E7 em seguida.
- **28/11 e 29/11:** fim de semana, sem pregão; nada a fazer.
- **30/11 (segunda), o pregão seguinte:** a conferência da abertura às 08:02 (§4.0) e T1–T3 às 09:00
  (§4.1), no mesmo dia. O "Enviar ordens" do 5725 no REAL **fica desligado** (desde 26/11 09:26) **até
  o fim do T3**.

```bash
F=/etc/kalman-ordens/service.env
K=/opt/kalman-ordens/kalman-ordens
W=/home/gammaquant/kalman-ordens/scripts/segredo-webhook.sh   # ler antes: roda com sudo
D=2711
```

## 0. Por quê e o que muda

- **O segredo do webhook REAL vazou** (apareceu numa conversa; George, 24/11). Ele é a única
  autenticação do webhook: quem tem o segredo pode mandar um POST que o serviço trata como da
  Flexscan. O pior caso é um "stop executado" forjado, que o serviço aplica direto: a operação fica
  zerada para ele, com a posição aberta na BTG e sem stop. (O fill de limitada não, esse só vale pela
  consulta, `FLEXSCAN_CONFIRMAR_FILL=1`.) Um forjado precisa também do id da Flexscan da ordem viva
  (a ponte confere o `ClOrdID`), da conta e do símbolo. Isso dificulta, mas não basta.
- **O segredo do caminho é nosso, não da Flexscan.** São 44 caracteres base64url gerados aqui, gravados no env e
  mandados no cadastro (dentro da URL). O `secret` da assinatura HMAC é outra coisa, da Flexscan (abaixo). Ninguém precisa pedir nada ao suporte (corrige o ESTADO-2026-11-20 §1).
  O vazado nunca foi cadastrado na Flexscan: a `REAL_FLEXSCAN_BTG_WEBHOOK_URL` está vazia desde 19/11.
- **O ganho do webhook** (a resposta de 24/11): nenhum na entrada (o fill de limitada continua pela
  consulta, ~200 ms depois do 202). Na **stop executada**: de 0–5 s (a consulta periódica, ~2,5 s em
  média) para ~0,3–1 s (o webhook aplicado direto). O preço não muda; muda saber antes (ZERADA, o
  resultado na tela, a próxima entrada liberada).
- **Se o webhook falhar no pregão:** a consulta continua (a periódica de 5 s e a de depois de cada
  ação). Vêm `FLEXSCAN_WEBHOOK_ATRASADO` (aviso), depois de 3 faltas `FLEXSCAN_WEBHOOK_SEM_CHEGAR`
  (crítico) e a conferência do cadastro (`FLEXSCAN_WEBHOOK_AUSENTE` se sumiu). Nenhum bloqueia
  entrada nem dá parada.
- **Sem binário novo, sem migração.** O cadastro na Flexscan é o próprio serviço que faz: na partida
  e todo dia às 08:02, porque a Flexscan limpa os cadastros de madrugada. O binário é o instalado
  (a v1.36.5, `7a0e7f419bf2`, se entrou em 26/11).
- **O que fica de fora, de propósito (26/11):**
  - **a assinatura HMAC.** A Flexscan gera um `secret` a cada cadastro (só na resposta do POST); esta
    versão o ignora, como o SIMULADOR faz hoje. A validação vem na branch do HMAC;
  - **o risco aceito até lá:** um webhook forjado de "stop executada" ou "stop cancelada" é aplicado
    direto (o primeiro dá a operação como zerada; o segundo faz o serviço recriar a stop, com duas
    vivas). Para forjar é preciso, ao mesmo tempo: sair do IP da Flexscan (o N1), o segredo novo do
    caminho e o id da Flexscan da ordem viva. É a máquina de dev, só o 5725 (conta do George), 1
    contrato, testes sob controle (decisão do George, 26/11). Na produção (.51), só com a stop
    executada/cancelada confirmada pela consulta (pedido do George para a branch do HMAC).
- **O que se ganha:** a stop executada vista em ~0,3–1 s em vez de 0–5 s, o que tira a corrida da
  reserva R5 do Kalman (4 s) com a consulta (a de 26/11 09:23: a SAIDA de reserva do 5725 chegou 0,22 s
  depois do fill visto pela consulta, recusada com `SEM_POSICAO`). E mostra cedo problemas reais,
  como a conta `D-` no webhook (§4).
- **A `PRECOS_SAUDE_URL` sai no mesmo restart (§2.4).** Ela aponta para o `/saude` do pnt-md-hml
  (127.0.0.1:9096), desligado desde 28/10: hoje só põe "fonte: /saude da fonte sem resposta" no texto
  do `FONTE_DEGRADADA` do REAL (26/11, 11:20), sem mudar decisão nenhuma.

| quem | durante | depois |
|---|---|---|
| kalman-ordens (SIMULADOR e REAL) | um restart (E5), de segundos, depois da zeragem | o REAL BTG cadastra o webhook e o espera; o `FONTE_DEGRADADA` do REAL sem o "fonte: …" |
| o Kalman | nada (sem posição, sem efeito) | nada muda |
| o webhook do SIMULADOR | nada (a location dele não muda; o reload do nginx é sem queda) | nada muda |

**O dia:** 27/11, as cópias (§2) a qualquer hora e o restart (§3) **depois da zeragem e antes das
17:55**. A janela da Flexscan é 08:02–18:30, então o cadastro aparece no mesmo dia. O teste (§4) é em
30/11, às 09:00.

## 1. As conferências de 24/11: sem exposição imediata

O que foi lido em 24/11:
- **O receptor do REAL está no ar com o segredo vazado** desde 19/11, em `172.16.110.53:9098`, no
  caminho `/flexscan/webhook-real-btg/<segredo>`. A URL vazia só desliga o cadastro e a espera. O
  receptor existe porque o `FLEXSCAN_WEBHOOK_ENDERECO` é de todas as pontes, e um POST no caminho
  certo é processado.
- **Nenhum sinal de uso:** nenhuma linha `WEBHOOK` no tráfego do REAL de 19/11 a 24/11. No journal
  (desde 20/10) há um único "webhook recusado": em 29/10, um GET da própria .53.

O resultado das conferências do George:

| # | o quê | resultado |
|---|---|---|
| C3 | o segredo do REAL × o do SIMULADOR (`$W compara`) | **diferentes**: só o do REAL está vazado |
| C1 | o proxy público | **uma única location aponta para a 9098, e é exata:** `location = /kalman-ordens/flexscan/webhook/<segredo do SIMULADOR>`. O caminho do REAL não é alcançável pela internet |
| C2 | o firewall da .53 | a 9098 aberta para `172.16.110.0/24`, a rede interna do George (só máquinas dele). A 8080 e o MySQL também são só internos |

**Conclusão:** sem exposição imediata. Hoje alcançam o receptor do REAL só as máquinas da
`172.16.110.0/24` e quem está dentro da .53. A troca segue por este roteiro, sem urgência, no dia
que o George decidir. O F1 (§2.0) fecha a 9098 para o resto da rede interna.

## 2. Dia D, a qualquer hora: o firewall, o proxy e as cópias do env (sem efeito no serviço)

### 2.0 F1 (opcional, .53): a 9098 só para o IP interno do proxy
O George informa o IP na hora. **Faça fora do pregão:** se o IP estiver errado, o webhook do SIMULADOR
para de chegar (vêm `FLEXSCAN_WEBHOOK_ATRASADO` e `_SEM_CHEGAR`), e a volta é recolocar a regra da /24.
```bash
sudo firewall-cmd --zone=public --list-rich-rules | grep 9098     # a regra de hoje: anote o texto exato
P=<o IP interno do proxy>
sudo firewall-cmd --permanent --zone=public --add-rich-rule="rule family=\"ipv4\" source address=\"$P/32\" port port=\"9098\" protocol=\"tcp\" accept"
sudo firewall-cmd --permanent --zone=public --remove-rich-rule='<o texto exato da regra da 172.16.110.0/24 para a 9098>'
sudo firewall-cmd --reload
sudo firewall-cmd --zone=public --list-rich-rules | grep 9098     # só a do $P
```
- A regra nova entra antes de a velha sair, então a 9098 nunca fica fechada para o proxy.
- **Se a /24 não for uma regra só da 9098** (uma `source` da zona ou uma regra que também abre a 8080
  ou o MySQL), não remova nada: mande a saída e eu escrevo o comando.
- **A conferência:**
  - do proxy, `curl -s -o /dev/null -w '%{http_code}\n' --max-time 3 -X POST http://172.16.110.53:9098/x` dá `404` (é o receptor);
  - de outra máquina da rede, o mesmo comando dá `000` (o tempo esgota);
  - na .53, o SIMULADOR com `webhook_faltas_seguidas` 0 depois da próxima ordem dele.
- **A volta:** `--add-rich-rule` com o texto anotado da /24, mais o `--reload`.
- Isso não fecha a 9098 para quem está dentro da .53. Para esse caso, só o segredo novo resolve.

### 2.1 N1: a location do REAL no proxy público, com o filtro de IP (FX-7)
A location do SIMULADOR é **exata, com o segredo no caminho** (C1). Para o REAL, **aprovada pelo George
em 24/11: uma location de prefixo com o filtro de IP**, sem copiar o segredo para o proxy:
- o segredo novo fica num lugar só (o env da .53), sem ter de ser copiado para o proxy, o que o faria
  aparecer na tela;
- de fora, só a Flexscan chega à 9098;
- o receptor continua conferindo o segredo.
```nginx
location ^~ /kalman-ordens/flexscan/webhook-real-btg/ {
    if ($request_method != POST) { return 200; }   # o teste do cadastro da Flexscan (faça como a do SIMULADOR faz)
    allow 177.71.196.212;                          # a Flexscan (todos os webhooks desde 28/10, FX-7)
    deny all;
    proxy_pass http://172.16.110.53:9098/flexscan/webhook-real-btg/;
    # ... as outras linhas proxy_* da location do SIMULADOR, iguais (o X-Real-IP inclusive)
}
```
- No nginx, o `if/return` roda antes do `allow/deny`. O GET de teste recebe 200 de qualquer lugar e
  não chega à 9098; o POST só passa vindo da Flexscan, e os outros levam 403.
- O `proxy_pass` com caminho troca o prefixo `/kalman-ordens/flexscan/webhook-real-btg/` por
  `/flexscan/webhook-real-btg/` e mantém o segredo no fim, que é o caminho que o receptor espera.
```bash
sudo nginx -t && sudo systemctl reload nginx
curl -s -o /dev/null -w '%{http_code}\n' https://<host do proxy>/kalman-ordens/flexscan/webhook-real-btg/teste           # 200 (GET)
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://<host do proxy>/kalman-ordens/flexscan/webhook-real-btg/teste   # 403 (quem testa não é a Flexscan)
```

### 2.2 E0–E3: o arquivo novo, gerado sem o segredo aparecer

**E0, a `PRECOS_SAUDE_URL` de hoje** (o valor não é segredo; anote a linha, é a volta V2):
```bash
sudo grep -nE '^#?[[:space:]]*(REAL_)?PRECOS_SAUDE_URL' $F | sed -E 's/^([^=]*(SENHA|SEGREDO|TOKEN|CHAVE|KEY|USUARIO|DSN|WEBHOOK_URL)[^=]*=).*/\1(oculto)/I'
#   esperado: uma linha ativa só, PRECOS_SAUDE_URL=http://127.0.0.1:9096/saude (sem _PNT, sem REAL_)
```
Se vier outra coisa (uma `_PNT`, uma `REAL_`, outro host ou mais de uma ativa), **não siga o §2.4**:
mande a saída e eu reescrevo o passo. O resto do roteiro (o webhook) segue igual.

```bash
sudo cp -a $F $F.antes-webhook-$D        # a volta V0 (se o serviço não subir); tem o vazado: sai no E7
sudo cp -a $F $F.webhook-$D
sudo $W novo $F.webhook-$D BTG https://<host do proxy>/kalman-ordens
#   ok: REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO novo (44 caracteres); REAL_FLEXSCAN_BTG_WEBHOOK_URL=https://<host>/kalman-ordens/flexscan/webhook-real-btg/(oculto)
```
**Agora o §2.4** (a `PRECOS_SAUDE_URL` sai do `$F.webhook-$D`), e só depois:
```bash
sudo cp -a $F.webhook-$D $F.webhook-sem-url-$D     # a volta V1: o segredo novo, sem URL (e já sem a PRECOS_SAUDE_URL)
sudo $W sem-url $F.webhook-sem-url-$D BTG
```
O script (`scripts/segredo-webhook.sh`) gera o segredo e o passa ao `awk` pelo ambiente. O segredo
não aparece na saída, no `ps` nem num arquivo temporário fora de `/etc/kalman-ordens`. O arquivo
mantém dono e modo. Se uma chave não estiver ativa uma vez só, ele não muda nada.

**E3, as conferências:**
```bash
sudo $W compara $F.webhook-$D REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO $F FLEXSCAN_WEBHOOK_SEGREDO                          # diferentes (do SIMULADOR)
sudo $W compara $F.webhook-$D REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO $F REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO                 # diferentes (do vazado)
sudo $W compara $F.webhook-sem-url-$D REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO $F.webhook-$D REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO  # iguais (a volta tem o novo)
sudo $W confere-url $F.webhook-$D BTG              # sim: https://<host>/kalman-ordens/flexscan/webhook-real-btg/(segredo, 44 caracteres)
sudo $W confere-url $F.webhook-sem-url-$D BTG      # URL vazia (sem webhook)
```

### 2.3 E4: o diff, com a `WEBHOOK_URL` também oculta
```bash
sudo diff $F $F.webhook-$D | sed -E 's/^([^=]*(SENHA|SEGREDO|TOKEN|CHAVE|KEY|USUARIO|DSN|WEBHOOK_URL)[^=]*=).*/\1(oculto)/I'
#   < e > REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO=(oculto)
#   < e > REAL_FLEXSCAN_BTG_WEBHOOK_URL=(oculto)
#   < PRECOS_SAUDE_URL=http://127.0.0.1:9096/saude   (só do lado de hoje: a linha que sai)
#   nada mais
sudo diff $F.webhook-$D $F.webhook-sem-url-$D | sed -E 's/^([^=]*(SENHA|SEGREDO|TOKEN|CHAVE|KEY|USUARIO|DSN|WEBHOOK_URL)[^=]*=).*/\1(oculto)/I'
#   só a REAL_FLEXSCAN_BTG_WEBHOOK_URL=(oculto) dos dois lados (a V1 só tira a URL)
```
O filtro único já tem o `WEBHOOK_URL` (desde 24/11).

### 2.4 E3b: a `PRECOS_SAUDE_URL` sai do arquivo novo
Só com o E0 como esperado. O arquivo mantém dono, modo e contexto (o `cat` por cima, como o script);
o temporário nasce com `umask 077`, porque o env tem segredos.
```bash
sudo sh -c 'umask 077; grep -v "^PRECOS_SAUDE_URL=" "$1" > "$1.tmp" && cat "$1.tmp" > "$1" && rm -f "$1.tmp"' _ $F.webhook-$D
sudo grep -cE '^(REAL_)?PRECOS_SAUDE_URL' $F.webhook-$D      # 0
sudo ls -lZ $F $F.webhook-$D                                  # mesmo dono, modo e contexto
```
O `PNT_DESLIGADO=1` e as `PRECOS_URL*` ficam como estão. Sem a chave, o serviço não lê nenhum `/saude`
de fonte no REAL (o SIMULADOR já não lia).

## 3. 27/11, depois da zeragem e antes das 17:55: o restart

**Antes (sem sudo):** nada vivo nas duas instâncias, e o Kalman sem abrir entradas.
```bash
curl -s 127.0.0.1:9092/saude | jq -c '{versao, criticos: .alertas_criticos}'
#   a versão instalada (7a0e7f419bf2 com a v1.36.5); criticos {}
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes | map_values({posicao, ordens_vivas, divergente, parada})'
#   REAL e SIMULADOR: posicao 0, ordens_vivas null, divergente false, parada null
```

**E5:**
```bash
sudo cp -a $F.webhook-$D $F && sudo systemctl restart kalman-ordens
```

**E6, as conferências (sem sudo):**
```bash
journalctl -u kalman-ordens --since '-2min' --no-pager | grep -E 'conector ligado|webhook|ERROR|CRITICO' | cut -c1-220
#   "flexscan: conector ligado" ambiente=REAL corretora=BTG ... webhook=true webhook_esperado=true; o do SIMULADOR como antes
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL.flexscan_corretoras.BTG | {logado, receptor, webhook_esperado, webhook_cadastrado, webhook_faltas_seguidas}'
#   logado true, receptor true, webhook_esperado true, webhook_cadastrado true, webhook_faltas_seguidas 0
curl -s "127.0.0.1:9092/flexscan/trafego?ambiente=REAL&corretora=BTG&limite=10" | jq -r '.mensagens[] | "\(.em) \(.tipo) \(.status_http) \(.resumo)"' | tail -5
#   LOGIN 200 partida OK; WEBHOOK_CADASTRO 200 conferencia: ausente; recadastro (order.all,algo.changed); conferencia: presente
curl -s 127.0.0.1:9092/saude | jq -c '{sim: .ambientes.SIMULADOR.flexscan | {webhook_cadastrado}, real: .ambientes.REAL | {criticos: .alertas_criticos, posicao, canais: (.canais|map_values(.estado))}}'
#   SIMULADOR webhook_cadastrado true; REAL criticos {}, posicao 0, PNT e FLEXSCAN:BTG NO_AR
sudo grep -cE '^(REAL_)?PRECOS_SAUDE_URL' $F                 # 0 (a PRECOS_SAUDE_URL saiu)
systemctl show kalman-ordens -p NRestarts | cat              # NRestarts=0
```
**No Telegram:** as `CONCILIACAO` da partida, o aviso do vigia (o serviço fora por segundos) e,
possivelmente, o `FLEXSCAN_ABERTURA` "webhook recadastrado" do REAL. Nenhum CRITICO.

A saída da `PRECOS_SAUDE_URL` só se vê no próximo `FONTE_DEGRADADA` do REAL: o texto vem sem o
"fonte: …", como o do SIMULADOR.
**Se o cadastro falhar** (`FLEXSCAN_WEBHOOK_AUSENTE`, "recadastro falhou"): o mais provável é o teste
da URL pela Flexscan ter levado 4xx, ou seja, a location do N1. Volta V1 já (nada vivo), confira o N1 e
me chame. O serviço segue funcionando pela consulta enquanto isso.

### 3.1 E7: a limpeza do segredo vazado (no mesmo dia, depois do E6 ok)
A procura usa o valor do `$F.antes-webhook-$D` e mostra só os nomes dos arquivos:
```bash
sudo ls -la /etc/kalman-ordens/
sudo $W procura $F.antes-webhook-$D REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO /etc /opt /root /home /var/lib/kalman-ordens /var/log /tmp /var/tmp
```
- **Esperado em `/etc/kalman-ordens/`:** o `service.env.antes-webhook-$D` e as cópias antigas (as de
  17/11, 19/11 e 20/11: `antes-real-1711`, `real-1711`, `antes-flex-1911`, `flex-1911`,
  `antes-conta-d-2011`, `conta-d-2011`, e as que houver). O segredo vale para todas, comentado ou não.
  Depois do E6, nenhuma delas é volta de nada: a volta passa a ser o `webhook-sem-url-$D`.
  ```bash
  sudo rm /etc/kalman-ordens/service.env.<cada uma da lista>
  ```
- **Fora de `/etc`:** se aparecer o histórico de uma conversa (`.claude/projects/...jsonl`), um
  `.bash_history` ou um log, você decide caso a caso. Com o segredo trocado, o vazado não abre mais
  nada.
- No fim, repita a procura usando o `$F.antes-webhook-$D` antes de apagá-lo, ou apague-o por último:
  o esperado é que só ele apareça.

## 4. 30/11 (segunda): a abertura e o teste das 09:00

### 4.0 A abertura, 08:02 (sem sudo)
O primeiro recadastro do REAL depois da limpeza da madrugada. O 5725 continua com o envio desligado.
```bash
# o /flexscan/trafego devolve as mais antigas primeiro: limite=200 e filtro pela hora (não head/tail com limite=10)
H=$(date +%F)T
curl -s "127.0.0.1:9092/flexscan/trafego?ambiente=REAL&corretora=BTG&limite=200" | jq -r --arg h "${H}08:00" '.mensagens[] | select(.em >= $h) | "\(.em) \(.tipo) \(.status_http) \(.resumo)"' | head -8
#   08:02 LOGIN OK; WEBHOOK_CADASTRO conferencia: ausente → recadastro → presente (a Flexscan limpou de madrugada)
journalctl -u kalman-ordens --since '08:00' --no-pager | grep -E 'ALERTA' | cut -c1-200
#   FLEXSCAN_ABERTURA "webhook recadastrado" (aviso, como o do SIMULADOR todo dia); nenhum FLEXSCAN_WEBHOOK_AUSENTE
```

### 4.1 O teste, 09:00

**Antes das 09:00:** o §1 do `REAL-2026-11-24-listagem-conta-D.md` (as sessões, o `/saude`, os três
`/destino`), com o "Enviar ordens" dos três desligado durante o teste, como em 24/11.

**T1 (09:00, George):** a limitada longe do preço pela D-, como no §2 de 24/11.
```bash
curl -s '172.16.110.53:9090/precos/atual?ativo=WINFUT' | jq -c '.WINFUT | {ticker, bid, ask, status84}'
BID=<o bid de agora>; P=$(( (BID - 1000) / 5 * 5 )); echo $P
$K boleta enviar -uid 5725 -ambiente REAL -conta D-4582351 -corretora BTG -lado C -qtd 1 -stop 300 -preco $P -por 5725 -motivo "webhook REAL D+1"
OP=<operacao_id da resposta>
```

**T2 (uns 10 s depois):**
```bash
H=$(date +%F)T
curl -s "127.0.0.1:9092/flexscan/trafego?ambiente=REAL&corretora=BTG&limite=200" | jq -r --arg h "${H}09:00" '.mensagens[] | select(.em >= $h) | "\(.em) \(.tipo) \(.status_http) \(.ip // "-") \(.resumo)"'
#   POST 202; WEBHOOK 200 177.71.196.212 "1 retrato(s): KM-BOLETA-E-<op>-1 VIVA exec 0/1 limite $P ..." até ~1–2 s depois do POST
journalctl -u kalman-ordens --since '09:00' --no-pager | grep -E 'ALERTA|webhook' | cut -c1-220
#   nenhum FLEXSCAN_WEBHOOK_INCOERENTE, _ATRASADO, _SEM_CHEGAR
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL.flexscan_corretoras.BTG | {webhook_cadastrado, webhook_faltas_seguidas}'
#   true, 0
```

**T3:** o cancelamento.
```bash
$K boleta cancelar -op $OP -por 5725 -motivo "webhook REAL D+1: fim"
sleep 3; curl -s "127.0.0.1:9092/flexscan/trafego?ambiente=REAL&corretora=BTG&limite=200" | jq -r --arg h "${H}09:00" '.mensagens[] | select(.em >= $h) | "\(.em) \(.tipo) \(.status_http) \(.ip // "-") \(.resumo)"' | tail -4
#   DELETE 202; WEBHOOK 200 177.71.196.212 "... CANCELADA ..."; a operação ENCERRADA_SEM_ENTRADA (confira no Profit)
```

| resultado | o que quer dizer | o que fazer |
|---|---|---|
| **sucesso:** um WEBHOOK de 177.71.196.212 depois do POST e outro depois do DELETE, nenhum alerta de webhook | o caminho Flexscan → nginx → 9098 → ponte REAL funciona, e a conta D- bate | o "Enviar ordens" volta; o caminho da stop executada se prova na primeira saída de verdade: um `WEBHOOK ... KM-P-... EXECUTADA` antes de qualquer consulta, e a `ZERADA` antes da R5 do Kalman (nenhum `SEM_POSICAO` da reserva) |
| `FLEXSCAN_WEBHOOK_INCOERENTE` "conta 4582351, esperada D-4582351" | a Flexscan escreve a conta sem o `D-` no webhook (a consulta devolve com o `D-`); todo webhook do 5725 seria descartado, com crítico | volta V1 depois do teste (nada vivo). A correção é no código (comparar a conta sem o prefixo de rota), numa versão, sem improvisar |
| nenhum WEBHOOK e um `FLEXSCAN_WEBHOOK_ATRASADO` depois de 5 s | o webhook não chegou: o proxy barrou (403) ou não repassou | no host do proxy: `sudo grep 'webhook-real' /var/log/nginx/access.log \| tail -5 \| sed -E 's#(webhook-real-btg/)[A-Za-z0-9_-]+#\1(oculto)#'`. Se o IP de origem não for 177.71.196.212, é dado da FX-7 (perguntar à Flexscan antes de mudar o `allow`). Volta V1 depois do teste |
| um WEBHOOK de outro IP | não pode, com o N1 | confira o N1 |

## 5. A volta

- **V1, desligar o webhook** (depois da zeragem, nada vivo; durante o pregão não é urgente, porque a
  consulta cobre):
  ```bash
  sudo cp -a $F.webhook-sem-url-$D $F && sudo systemctl restart kalman-ordens
  ```
  **Mais o N2 no proxy:** tirar a location do N1 e fazer o reload. O caminho do REAL volta a não
  ser alcançável, como em 24/11. Sem o N2, a Flexscan continua com o cadastro até a limpeza da madrugada e os POSTs seguem
  chegando. A URL vazia só desliga o cadastro e a espera, não o receptor.
- **Só o N2, no meio do pregão** (sem restart): se os alertas de webhook virarem barulho. Os webhooks
  param na hora. Como a URL continua no env, vêm `FLEXSCAN_WEBHOOK_ATRASADO` e, depois de 3 faltas,
  `FLEXSCAN_WEBHOOK_SEM_CHEGAR` (crítico, mas não bloqueia nada). O V1 vem depois da zeragem.
- **V0, o serviço não subiu no E5** (**não existe mais desde 26/11**: o `antes-webhook-2611` foi apagado
  no E7, depois do E6 ok; §7):
  ```bash
  sudo cp -a $F.antes-webhook-$D $F && sudo systemctl restart kalman-ordens
  ```
  Isso põe o segredo vazado de volta (e a `PRECOS_SAUDE_URL`). Por isso o E7 só roda depois do E6 ok.
- **V2, só a `PRECOS_SAUDE_URL` de volta** (não deve ser preciso; ela só muda texto). A linha
  anotada no E0, no fim do env, e um restart depois da zeragem:
  ```bash
  echo 'PRECOS_SAUDE_URL=http://127.0.0.1:9096/saude' | sudo tee -a $F > /dev/null
  sudo systemctl restart kalman-ordens
  ```
  As voltas V0 e V1 não precisam dela: a V0 já a tem, e a V1 continua sem, de propósito.

## 6. Os comandos do George

| # | onde | quando | o quê |
|---|---|---|---|
| C1–C3 | proxy / .53 | **feitas em 24/11** | sem exposição imediata (§1) |
| F1 | .53 | opcional, fora do pregão | a 9098 só para o IP interno do proxy |
| N1 | host do proxy | 27/11 | a location do REAL com `allow 177.71.196.212; deny all;` |
| E0 | .53 | 27/11 | a `PRECOS_SAUDE_URL` de hoje (anotar) |
| E1–E3, E3b, E4 | .53 | 27/11 | as cópias, o segredo novo, a `PRECOS_SAUDE_URL` fora (§2.4), as conferências, os dois diffs |
| E5 | .53 | 27/11, depois da zeragem, até 17:55 | o `cp` e o restart |
| E7 | .53 | 27/11, depois do E6 ok | a procura e a limpeza do segredo vazado |
| — | .53 (sem sudo) | 30/11, 08:02 | a abertura (§4.0); o 5725 com o envio desligado até o fim do T3 |
| T1, T3 | .53 (sem sudo) | 30/11, 09:00 | a boleta e o cancelamento |
| V0 / V1 + N2 / V2 | .53 / proxy | se der problema | a volta |

## 7. Registro da execução (26/11, D=2611)

Executado pelo George em 26/11, à tarde, com a v1.36.5 instalada (`7a0e7f419bf2`).
- **N1:** no `souquant.com.br` (`conf.d/souquant.com.br.conf`), a `location ^~ .../webhook-real-btg/`
  depois da do SIMULADOR, com `access_log off`, `allow 177.71.196.212` e `deny all`. Cópia de antes em
  `/root/souquant.com.br.conf.antes-webhook-real-2611`. `nginx -t` ok; GET 200; POST de fora 403.
- **E0:** `PRECOS_SAUDE_URL=http://127.0.0.1:9096/saude`, linha 83 (é a que o V2 devolve).
- **E1–E4 ok:** `compara` diferentes / diferentes / iguais; `confere-url` sim / vazia; os dois diffs só
  com o esperado. **E3b:** 0.
- **E5 às 15:25:48. E6 ok:**
  - REAL BTG logado; receptor, `webhook_esperado` e `webhook_cadastrado` true; faltas 0;
  - tráfego às 15:25:48: conferência ausente → recadastro (`order.all,algo.changed`) → presente;
  - um `FLEXSCAN_WEBHOOK_AUSENTE` "recadastrado" (WARN) na primeira conferência, como esperado;
  - SIMULADOR cadastrado; REAL `criticos {}`, posição 0, canais `NO_AR`;
  - `PRECOS_SAUDE_URL` 0 no env; `NRestarts=0`.
- **E7:** o segredo vazado só estava em 14 cópias em `/etc/kalman-ordens`; todas apagadas, inclusive o
  `service.env.antes-webhook-2611`. **A volta V0 deixou de existir.** Ficaram o
  `service.env.webhook-2611` e o `service.env.webhook-sem-url-2611` (a volta V1), e as cópias antigas,
  sem o vazado.
- **Nota para o §4:** o `/flexscan/trafego` devolve as mensagens mais antigas primeiro. Com `limite=10`,
  o `head`/`tail` mostra a manhã. O §4 já usa `limite=200` e o filtro pela hora.
- **O que falta:** 27/11, a abertura às 08:02 (§4.0) e T1–T3 às 09:00 (§4.1). O 5725 fica com o envio
  REAL desligado até o fim do T3.

**27/11, a abertura e T1–T3 (George): concluídos.**
- **08:02:** recadastro no REAL e no SIMULADOR; conferência presente às 08:30; o `/saude` do REAL limpo,
  na v1.36.5.
- **T1–T3:** a boleta 1127090757 (5725, D-4582351, C 1 WINZ26 lim 183000):
  - POST às 09:08:00.978, e o WEBHOOK de 177.71.196.212 às 09:08:01.965 (VIVA 0/1);
  - DELETE às 09:09:23.600, e o WEBHOOK às 09:09:23.986 (CANCELADA 0/1).
- Nenhum `FLEXSCAN_WEBHOOK_INCOERENTE`: a conta D- foi reconhecida. O único alerta foi o `BOLETA_REAL`.
- **Resultado:** a linha "sucesso" do §4.1. A stop executada pelo webhook se prova na primeira saída de
  verdade do 5725.

