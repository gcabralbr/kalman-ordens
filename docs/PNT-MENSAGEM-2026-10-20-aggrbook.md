# Para o PnT — WB_MD da HML: AggrBook do WINZ26 em 20/10/2026

Usuário `pnt_external_gammaq`, hub `https://webbrokerMDhml.pnt.api.br/mdhub`, assinatura
`SubscribeMarketData(requestId, "WINZ26", "AggrBook")` (sem sufixo), a mesma de 16 e 19/10.

**Até 19/10** o AggrBook do WINZ26 vinha com `"Aggregated": true`, `"MarketDepth": 10`, 10 níveis por
lado, ordenados (melhor primeiro), ~3,4 kB por mensagem, e o book nunca cruzava (1 em 5.397
mensagens às 09:10).

**Em 20/10, desde as 08:55**, o mesmo assinante recebe:

- `"Aggregated": false` e `"MarketDepth": 0`;
- listas crescendo sem limite: 63 níveis às 08:55, 12.112 às 09:01:24 e, depois, ~30 mil níveis por
  lado (~9,5 MB por mensagem);
- **o mesmo preço repetido dezenas de vezes** (ex.: 09:01:24.923 — 6.016 entradas de bid com 168
  preços distintos; 6.096 de offer com 181);
- listas **fora de ordem** (`ListBids[0]` é o maior bid em 1 de 488 mensagens);
- **ordens do pré-abertura que não saem** (UpdateTime 2026-10-20T08:55:00): bid 222.420 / 222.000 /
  220.000 e offer 191.345 / 191.950 — com elas o book fica **cruzado em 100% das mensagens** (maior
  bid ≥ menor offer).

Os `LastTrade` estão normais (pregão de 19/10, preços coerentes com a fita). Pela documentação, cada
`OnUpdateMarketData` de AggrBook é o estado completo e o `MarketDepth` indica a profundidade; os
payloads acima não podem ser usados como book.

Perguntas:
1. Houve mudança de configuração no WB_MD da HML entre 19/10 e 20/10 (profundidade máxima,
   agregação, feeder)?
2. O AggrBook deveria vir agregado por preço e limitado (como até 19/10)? Há como pedir uma
   profundidade fixa (ex.: 10)?
3. As ofertas do pré-abertura que permanecem no book (222.420 / 191.345) são esperadas na HML?

Temos as mensagens gravadas (08:39–09:01) se quiserem a amostra.
