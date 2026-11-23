# REAL, 24/11 (terça), entre 09:00 e a ligação do 1450 e do 35: a listagem pela D-4582351 com uma ordem viva

O §2.3 do `docs/REAL-2026-11-20-conta-D-5725.md`, que ficou de fora em 23/11. Sem código. Roda
com a v1.36.3 (`947b81422b04`, instalada em 23/11 às 14:28).

`K=/opt/kalman-ordens/kalman-ordens`. Todo comando que mostra o service.env termina no filtro de segredos.

## 0. Por quê, e a janela

- **O que já está provado (23/11, op 1123103041):** a ordem pela `D-4582351` vai ao OMS de day
  trade (`route 1`, `ORDPROXY0801BTG.DTBTG…`).
- **O que falta:** a **listagem da conciliação** (`GET /orders/filter?account=D-4582351`) achar uma
  ordem viva da D-. Se não achar, a ordem é julgada "viva aqui e ausente lá": DESCONHECIDA,
  DIVERGENTE e **Parada BLOQUEAR da instância REAL inteira**, inclusive para o 1450 e o 35 pelo PnT.
- **Quando a listagem roda:** só no login da ponte (partida, reconexão) e só para as contas com ordem
  viva ou posição. Por isso a prova é um restart com a ordem viva.
- **A janela:** a boleta só aceita entradas **a partir das 09:00** (`JANELA_INICIO`; antes disso,
  `FORA_JANELA`). Então tudo acontece entre 09:00 e a hora em que o George e o Ferro ligam o "Enviar
  ordens" do 1450 e do 35. **Até o fim do §4, os dois ficam desligados**, e o 5725 também (só a
  boleta). O restart derruba a GAMMAQ2 por ~2 s; sem operação aberta, isso não tem efeito.
- **O tempo:** ~10 min. Se até 09:20 não houver conclusão, cancele (§4) e siga o §5.

## 1. Antes das 09:00 (sem mudar nada)

```bash
date; $K versao                                              # 947b81422b04 (v1.36.3)
journalctl -u kalman-ordens --since '08:00' --no-pager | grep -E 'flexscan: login|login recusado|pnt: envio liberado|conector de volta|chmod|permiss|ERROR|CRITICO' | cut -c1-200
#   08:02 os logins da flx_gammaquant e da btg_gammaquant OK (nenhum "login recusado"); 08:30 a GAMMAQ2
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL | {criticos: .alertas_criticos, sessao_logada, posicao, divergente, parada, ordens_vivas, canais: (.canais|map_values(.estado))}'
#   criticos {}; sessao_logada true; posicao 0; divergente false; parada null; ordens_vivas null; PNT e FLEXSCAN:BTG NO_AR
for u in 1450 35 5725; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=REAL" | jq -c --arg u $u '{uid: $u} + (.ambientes.REAL | {conector, conta, pode_enviar, codigo})'; done
#   1450 PNT 4582351, 35 PNT 5094181, 5725 FLEXSCAN D-4582351; os três pode_enviar true
```
**Com o login da `btg_gammaquant` recusado de novo:** pare aqui. Siga o ESTADO-2026-11-23 §7.1 e me chame.

## 2. 09:00: a limitada pela D- (George)

```bash
curl -s '172.16.110.53:9090/precos/atual?ativo=WINFUT' | jq -c '.WINFUT | {ticker, bid, ask, status84}'
BID=<o bid de agora>; P=$(( (BID - 1000) / 5 * 5 )); echo $P
$K boleta enviar -uid 5725 -ambiente REAL -conta D-4582351 -corretora BTG -lado C -qtd 1 -stop 300 -preco $P -por 5725 -motivo "listagem D- 24/11"
OP=<operacao_id da resposta>
sleep 3; $K boleta estado -op $OP | jq -c '{estado, conta, ordens: [.ordens[] | {clordid, estado, orderid37, preco, qtd_exec}]}'
#   ENTRANDO; a E viva (PENDENTE/ATIVA), com orderid37 (o id da Flexscan), qtd_exec 0. ANOTE o orderid37
curl -s "127.0.0.1:9092/flexscan/trafego?ambiente=REAL&corretora=BTG&limite=10" | jq -r '.mensagens[] | "\(.em) \(.tipo) \(.status_http) \(.resumo)"' | tail -3
#   POST 202 "C 1 WINZ26 limite $P conta D-4582351"; CONSULTA 200 "VIVA exec 0/1"
curl -s "127.0.0.1:9092/operacoes/$OP/historico?modulo=BOLETA&cru=1" | jq -r '.mensagens[] | select(.msgtype=="FLX-GET") | .fix_raw | fromjson | "\(.account) status=\(.orderStatus) route=\(.route) \(.strategyId)"' | sort | uniq -c
#   D-4582351 status=0 route=1 ORDPROXY0801BTG.DTBTG...
```
**Com o POST recusado** (`REJEITADA`, 400): anote o texto e pare. A prova fica para outro dia, e o
5725 não opera pela D- até ela.

## 3. O restart com a ordem viva (George)

```bash
sudo systemctl restart kalman-ordens
```
Sem sudo, ~20 s depois:
```bash
sleep 20; systemctl is-active kalman-ordens
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|no ar|flexscan: login|mass status|concilia|DESCONHECIDA|DIVERGENTE|PARADA|chmod|permiss|ERROR' | cut -c1-220
```
**Esperado:**
- `subindo` e `no ar` com a mesma versão;
- os logins `partida` OK das duas credenciais;
- **a conciliação do REAL: `1 ordem(ns) KM, 0 de outros (ignoradas), 0 divergência(s); posição 0`**;
- nenhum "mass status sem resposta", "contas sem resposta completa", DESCONHECIDA, DIVERGENTE, PARADA,
  chmod ou ERROR.

```bash
$K boleta estado -op $OP | jq -c '{estado, ordens: [.ordens[] | {clordid, estado, orderid37}]}'
#   ainda ENTRANDO; a MESMA E, viva, com o MESMO orderid37
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL | {criticos: .alertas_criticos, divergente, parada, posicao, ordens_vivas, canais: (.canais|map_values(.estado))}'
#   criticos {}; divergente false; parada null; posicao 0; ordens_vivas = a E; PNT e FLEXSCAN:BTG NO_AR
```
**Com isso, a listagem pela D- está provada.**

## 4. O cancelamento e a liberação

```bash
$K boleta cancelar -op $OP -por 5725 -motivo "listagem D- 24/11: fim"
sleep 10; $K boleta estado -op $OP | jq -c '{estado, ordens: [.ordens[] | {clordid, estado}]}'
#   ENCERRADA_SEM_ENTRADA, a E CANCELADA
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL | {criticos: .alertas_criticos, divergente, parada, posicao, ordens_vivas}'
#   criticos {}; divergente false; parada null; posicao 0; ordens_vivas null
```
**No Profit:** a limitada cancelada, posição 0 e nada pendente na 4582351 (day trade). Depois disso,
o George e o Ferro ligam o "Enviar ordens" do 1450 e do 35.

## 5. Se der errado

| o que aparece no §3 | o que fazer |
|---|---|
| **DESCONHECIDA + DIVERGENTE + PARADA BLOQUEAR** (a listagem não achou a ordem) | A operação fecha como DESCONHECIDA, **mas a limitada continua viva na BTG**: cancele no Profit e confira posição 0. Me chame antes do `retomar`: eu confiro a conciliação e o `/saude`. Depois, `$K retomar -ambiente REAL -motivo "listagem D- falhou 24/11"`. **O 5725 volta a `4582351` no GQ** até a Flexscan responder (o §6 do ESTADO-2026-11-23) |
| "contas sem resposta completa ... D-4582351" (a listagem falhou) | A ordem não foi julgada e continua `ENTRANDO`. Cancele pelo §4. **Não provado**: o 5725 não opera pela D- até repetir a prova |
| o serviço não volta, ou não loga | No Profit, cancele a limitada (ela está 1.000 pts abaixo: não executa sozinha) e me chame. O 1450 e o 35 continuam desligados |
| 09:20 sem conclusão | Cancele (§4) e libere o 1450 e o 35 só com o `/saude` limpo |

## 6. O que me mandar
- do §2: o `orderid37`, a linha do POST e a do FLX-GET;
- do §3: a linha da conciliação e o `/saude`;
- do §4: o estado final e o que o Profit mostra.
