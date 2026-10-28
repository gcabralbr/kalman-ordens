# SPEC kalman-ordens v1.21.1 — webhook com os dois gatilhos, operação no webhook de vários retratos, recusa "Finalizado" (28/10/2026)

Base: v1.21 (instalada na .53, `22ca9527f630`). Origem: o pregão de 28/10.

## 1. O cadastro do webhook com `order.all` e `algo.changed`

Em 28/10, o nosso cadastro sumiu da Flexscan entre 03:40:13 e 03:50:13. A conferência recadastrou às
03:50:13, mas só com `order.all`: a v1.21 §2 e a SPEC-FLEXSCAN-v1 §5.4 diziam só esse gatilho.
O cadastro feito à mão em 27/10 tinha os dois. O retrato da estratégia `sorder` que a ponte lê é o
que a referência da API descreve para o `algo.changed` ("a snapshot whenever the strategy has some
relevant changes"). Com o recadastro à mão com os dois gatilhos, os webhooks chegaram em todos os
passos da 234339.

- **Gatilhos:** `order.all` e `algo.changed`. Substitui o "só `order.all`" da v1.21 §2 e da
  SPEC-FLEXSCAN-v1 §5.4.
- **Conferência:** soma os `triggers` de todos os cadastros com a `FLEXSCAN_WEBHOOK_URL`.
  - Tem os dois: presente.
  - Nenhum cadastro da URL: ausente (o `resumo` diz "ausente").
  - A URL sem um deles: incompleto (o `resumo` diz "incompleto, sem <gatilho>").
- **Recadastro:** `POST /webhooks` só com os gatilhos que faltam. Ausente leva os dois; incompleto
  leva só o que falta, sem repetir o que já está cadastrado. Continua sem `remove_previous`, sem
  `DELETE` e sem mexer nos outros cadastros. O `resumo` diz os gatilhos enviados.
- **`FLEXSCAN_WEBHOOK_AUSENTE`:** crítico também para o incompleto. O texto diz o que falta e se o
  recadastro deu certo. `webhook_cadastrado` só é `true` com os dois.

## 2. Webhook de vários retratos: operação, módulo e usuário

Às 09:01:45 de 28/10, um webhook chegou com a entrada executada e o stop aceito, ambos da 234339. No
tráfego ele apareceu sem operação nem usuário. A partir desta versão, quando todos os retratos são da
mesma operação (mesmo módulo e número, pelo `name` `KM-...`), a linha `WEBHOOK` traz:
- `operacao_id`, `modulo` e `uid`;
- `clordid` e `flexscan_id` continuam `null` (são de uma ordem só).

Com retratos de operações diferentes, ou algum que não é KM, os três ficam `null`, como antes. Vale
também para as linhas recarregadas do `kalmanordemevento` na partida.

## 3. 400 "Finalizado" no DELETE/PUT

O simulador recusa o DELETE do stop já executado com "order cannot be canceled because it is in the
state: Finalizado", com o estado em português. A ponte já tratava a corrida assim: antes do 35=9,
consulta a ordem, e o 39 do 35=9 sai do estado que a consulta mostrou. O texto não é lido. Consulta
com o fill: 39=2, e o motor segue a regra da v1.19 (fill já aplicado: só registro; senão, espera
`ESPERA_FILL_POS_RECUSA_S`).

Fica coberto também o caso de a consulta ainda não mostrar o fill. Antes, o 35=9 saía com o 39 da
ordem viva e o alerta (`CANCEL_STOP_REJEITADO` ou `RECUSA_FLEXSCAN`) saía na hora. Agora vale isto:
- **Quando:** o 400 tem "Finalizad", "Executad", "filled" ou "finished" (sem diferença de
  maiúsculas) e a ponte não conhece a ordem como terminal.
- **O que muda:** o 35=9 sai com 39=2. O motor espera o fill e só alerta se ele não vier no prazo.
- **O que não muda:** o caminho do motor (não manda a saída, aguarda os fills do stop) e o texto do
  35=9.

## Testes

- Roteiro 115:
  - recadastro na partida com os dois gatilhos;
  - cadastro só com `order.all`: incompleto, recadastra só o `algo.changed`, resolve;
  - webhook com dois retratos da 11501, com a operação, ao vivo e depois do restart.
- Roteiro 98: o 400 "Finalizado" com a consulta ainda sem o fill. Sem alerta, e o fill chega pelo
  webhook.
- A Flexscan falsa responde ao DELETE/PUT de ordem executada com o texto do simulador.
- `TestMesmaOperacao`.
