# SPEC kalman-ordens v1.17 — MERCADO_TRAVADO (26/10/2026)

Base: v1.16. Origem: 26/10, 09:17. O WIN abriu e ficou parado no limite de baixa (177.215, −14,90%)
só com ofertas de venda (Profit: 128,8k contratos/813 ofertas no melhor, nenhum comprador), mercado
aberto, `status84` 0. A 9090 mostrou **bid = ask = 177.215**: o bid é o último valor do feed, não
um comprador. O `FONTE_DEGRADADA:FLEXSCAN` classificou como "book cruzado". Futuros não têm circuit
breaker na B3; o que existe é o limite de oscilação do dia (e a ampliação dele, SPEC-FLEXSCAN-v1 §14.5).

## Regra

**Mercado travado** = em negociação (`status84` 0), com **bid = ask** ou **book de um lado só**
(só ask: sem compradores; só bid: sem vendedores). Num pregão contínuo bid = ask não persiste, porque
as duas ofertas executariam. Então ou o mercado está no limite de oscilação com um lado só, ou a
fonte está errada. Os dois casos são críticos.

- **Não é falha da fonte.** O `FONTE_DEGRADADA` não conta mercado travado (nem "book cruzado", nem
  "sem bid/ask"). O preço parado (sem negócio, a idade crescendo) não conta como preço velho
  enquanto o mercado estiver travado. "Book cruzado" passa a ser só bid > ask, e "sem bid/ask" só
  quando faltam os dois.
- **Alerta próprio `MERCADO_TRAVADO`**, crítico. Usa o mesmo prazo (`FONTE_DEGRADADA_S`, 30 s) e a
  mesma janela (`FONTE_JANELA`, só em dia de pregão) do `FONTE_DEGRADADA`, e tem uma chave por fonte
  (`MERCADO_TRAVADO`, `MERCADO_TRAVADO:FLEXSCAN` com o prefixo `[flexscan]`). Resolve (`RESOLVIDO`)
  quando o book volta aos dois lados ou a janela fecha. O texto traz:
  - o preço e o que a fonte mostra;
  - o lado sem contraparte, quando a fonte diz;
  - há quanto tempo a fonte não muda;
  - as posições abertas daquela fonte;
  - **qual posição não sai**.
- **Posição no lado travado não sai**: nem o stop nem a saída executam até o mercado destravar.
  Sem compradores (limite de baixa), a comprada não vende; sem vendedores (limite de alta), a vendida
  não compra. Com bid = ask a fonte não diz o lado, e o texto diz isso.
- **Entrada:** recusa `MERCADO_TRAVADO`. Vem depois de `LEILAO`, antes de `SEM_PRECO`/`BOOK_INVALIDO`
  (antes, bid = ask era `BOOK_INVALIDO` e um lado só era `SEM_PRECO`). Conta nas recusas por limite
  como as outras de preço. O Kalman não trata códigos de recusa um a um (conferido em
  `~/meus_scripts/KalmanWIN`).
- **`/saude`:** `fonte.travado`, `fonte.travado_desde`, `fonte.travado_motivo` (o mesmo em
  `flexscan.fonte`).

Exemplo (roteiro 110):

```
MERCADO_TRAVADO: mercado travado em 130000: so ofertas de venda (ask 130000, sem bid); sem compradores
(limite de baixa?); fonte sem mudanca ha 0 s (travado ha 30s); entradas recusadas; operacao 1102
comprada 1; a posicao comprada nao sai: nem o stop nem a saida executam ate o mercado destravar
```

## O que não muda (a fazer, SPEC-FLEXSCAN-v1 §14.5)

O motor só alerta. Travado, ele continua agindo como antes:
- o stop furado de reserva pode disparar com negócios no limite e trocar a proteção (que está na
  fila do limite) por uma saída limite fora do limite de oscilação, que a B3 recusa ou que perde a
  prioridade;
- a saída repreça.

Fica para o próximo pacote: com a fonte travada e a posição no lado preso, não cancelar a proteção
nem repreçar.

## Testes

- Roteiro **110** (`110-mercado-travado.fix`): bid = ask (entrada recusada, alerta em 30 s,
  `FONTE_DEGRADADA` quieto mesmo com a fonte parada há 2 min, resolve); só ask (a comprada é a presa);
  bid > ask continua `FONTE_DEGRADADA`.
- Mutação conferida: sem a exceção do travado no `motivoFonteRuim`, o roteiro falha no `/saude`
  ("book cruzado").
