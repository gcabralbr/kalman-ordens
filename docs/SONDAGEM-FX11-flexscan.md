# Sondagem FX-11: o `PUT` do `price` numa stop-limite já disparada (Flexscan REAL, 1 contrato)

Preparado em 19/11 para o George ler antes. **Nada foi executado. O script ainda não existe**: ele é
escrito depois do ok deste roteiro, no molde do `scripts/flexscan-cb.py` de 26/10.

## 0. Por que e o que se quer saber

**Por quê.** A SPEC v1.37 (D2.1) quer trocar a própria stop furada pela saída agressiva, sem cancelar,
para que nunca haja um instante sem ordem nem duas vendas. No PnT esse é o replace da 1014 (v1.11, em
produção). Na Flexscan, a stop-limite disparada vira uma limitada em repouso, e a pergunta é se o
`PUT /orders/{id}` aceita mudar o `price` dela.

**O que já sabemos:**
- em 23/10, no apisim, a Flexscan recusou o `PUT` do **`stopPrice`** além do preço (razão 4);
- em 19/11, no REAL, o `PUT` de `stopPrice` + `price` de uma stop **não disparada** foi aceito (o
  ajuste do passo e);
- o caso da stop **disparada** nunca foi visto.

**As três respostas que a sondagem dá:**
1. Que estado a consulta mostra numa stop-limite disparada e sem fill (o `ordStatus` e o
   `isInMarket`; a SPEC-FLEXSCAN §14.5 viu "3 → 5 direto").
2. O `PUT` do `price` (para bid − 300) nessa ordem é aceito? Em quanto tempo ela executa?
3. Se for recusado: com qual texto e razão. A saída vira o DELETE + POST, o caminho de hoje.

## 1. Quando e com quem

- **Quando:** depois das 14:30 (fora da janela do Kalman, com o REAL zerado) e antes das 17:30, com o
  mercado contínuo (`status84` 0) e sem notícia marcada.
- **A conta:** a 4582351, pela credencial `btg_gammaquant`. **O 1450 e o 5725 com o "Enviar ordens"
  desligado** e `.ambientes.REAL.posicao` 0. A sondagem abre uma posição fora do serviço.
- **Os nomes:** as ordens levam o nome **`SD-…`**, não `KM-`. Assim a conciliação do serviço as ignora
  ("de outros").
- **O login:** o script faz **um** login (o token fica em `var/flex-fx11-AAAAMMDD/token`, 0600) e
  nenhum outro. Não sabemos se um login novo derruba o token do serviço (a FX-3, aberta). Se derrubar,
  a ponte do REAL reloga sozinha (a guarda é de 5 logins em 10 min) e concilia. Depois das 14:30 isso
  não atrapalha nada.

## 2. O passo a passo (o script, um subcomando por passo, com retratos entre eles)

| passo | o quê | o que conferir |
|---|---|---|
| S1 `login` | um login | o token salvo |
| S2 `checar` | `GET /orders/filter` da conta: nada `SD-` vivo | lista vazia de `SD-` |
| S3 `comprar` | compra 1 WIN, limitada no ask + 15 (`SD-E-…`) | executou (consulta e Profit) |
| S4 `stop` | stop-limite de venda **com folga 0**: gatilho = limite = bid − 5 (`SD-P-…`). Com folga 0, o primeiro negócio no gatilho dispara a stop, e é provável que ela fique em repouso no gatilho, porque o bid já desceu | aceita; estado de stop não disparada |
| S5 `vigiar` | consulta a cada 200 ms, até 5 min: a stop disparou e não executou? | o estado e o `isInMarket` depois do disparo: **a resposta 1** |
| S6 `put` | com a stop disparada e sem fill: `PUT` do `price` para bid − 300 | aceito (202, `Editada`) e executou? Em quanto tempo? **a resposta 2**. Recusado: o texto e o `clxRejReason`, **a resposta 3**, e segue o S7 |
| S7 `zerar` | se ainda houver posição: DELETE da `SD-P-`, confirmado, e venda limitada no bid − 300 | executou |
| S8 `limpar` | DELETE de todo `SD-` vivo | nada `SD-` vivo; no Profit, posição 0 e nenhuma ordem |

**Variações que o script trata:**
- **A stop executa inteira no disparo (sem repouso):** o S6 não se aplica. Repete-se S3–S5 uma vez. Na
  segunda vez sem repouso, encerra: a resposta fica para a Flexscan (FX-11).
- **A stop não dispara em 5 min:** S7 com DELETE da stop e venda, e encerra.
- **Execução parcial:** não acontece com 1 contrato.

**O risco:**
- **O tamanho:** 1 contrato, por no máximo uns 5 minutos, com a stop de folga 0 colada no preço. O
  pior caso é o S6 recusado com o preço fugindo: aí a saída é a do S7, a mesma da v1.37 (bid − 300).
- **O custo esperado** é de algumas dezenas de pontos (compra no ask, saída abaixo do bid). Com 1 WIN,
  isso é menos de R$ 20.

## 3. O cartão

- **O script travou ou caiu:** no Profit, **primeiro cancele a `SD-P-`, depois zere**, e confira
  posição 0 e nenhuma ordem pendente. É a mesma regra do cartão da Flexscan REAL.
- **O serviço não é afetado:** as ordens `SD-` não são dele. Se ele relogar por causa do token, a
  conciliação deve dar "N de outros (ignoradas)".

## 4. O que me mandar

- **A saída do script:** um jsonl em `var/flex-fx11-AAAAMMDD/`, sem o token.
- **O que o Profit mostrou** em S4–S7.

Com as três respostas, a E2 da v1.37 escolhe o caminho da Flexscan no D2:
- **o `PUT` do `price`:** se o S6 for aceito;
- **o DELETE + POST sem espera de 5 s:** se o S6 for recusado.
