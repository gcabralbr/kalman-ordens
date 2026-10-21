# Para o PnT — WB_MD da HML: AggrBook do WINZ26 em 20/10/2026

Usuário `pnt_external_gammaq`, hub `https://webbrokerMDhml.pnt.api.br/mdhub`, assinatura
`SubscribeMarketData(requestId, "WINZ26", "AggrBook")`, sem sufixo, como a documentação do WB_MD pede
("Book/AggrBook: SEM sufixo de profundidade"), feita a cada (re)conexão — a mesma, sem nenhuma mudança
no nosso cliente, desde 16/10. O tópico ecoado nas atualizações é "AggrBook" nos dois dias.

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
1. Houve mudança de configuração no WB_MD da HML entre 19/10 e 20/10? A documentação diz que a
   profundidade é a do servidor (`BookDepthSettings:MaxAggrBookDepth`) — até 19/10 chegava 10; hoje
   `MarketDepth` 0 e listas sem limite. E o AggrBook, "book agregado por preço", chegou com
   `Aggregated:false` e preços repetidos.
2. O AggrBook deveria vir agregado por preço e limitado (como até 19/10)? Pela documentação o cliente
   não escolhe a profundidade (o sufixo `_N` é ignorado); podem voltar o `MaxAggrBookDepth` da HML
   para 10?
3. As ofertas do pré-abertura que permanecem no book (222.420 / 191.345) são esperadas na HML?

Temos as mensagens gravadas (08:39–09:01) se quiserem a amostra.

---

**Atualização de 21/10/2026 — o mesmo assinante, a mesma assinatura: depende de quando a conexão abre.**

| Conexão | Primeiro AggrBook | `Aggregated` | `MarketDepth` | níveis por lado |
|---|---|---|---|---|
| 20/10 08:39:38 (antes do pré-abertura) | 08:55:00.485 | `false` | 0 | 5 → 12.112 (09:01:24) → ~30 mil |
| 20/10 10:45:41 (pregão aberto) | 10:45:41.374 | `true` | 10 | 10 |
| 21/10 08:53:06 (antes do pré-abertura) | 08:54:57.070 | `false` | 0 | 1 → ~60 (08:59) → 8,3 mil (09:00) → 20 mil (09:01:59) → 23,6 mil (09:02:26) |
| 21/10 09:02:27 (pregão aberto, reconexão nossa) | 09:02:27.140 | `true` | 10 | 10 |

Em 21/10 o book da conexão das 08:53 voltou a crescer sem limite desde o pré-abertura (08:54:57), com
o nosso atraso de recepção chegando a 1,1 s; ao reconectar às 09:02:27 — mesma chamada
`SubscribeMarketData(requestId, "WINZ26", "AggrBook")` — o AggrBook veio agregado, 10 níveis, atraso de
~10 ms. Nos dois dias: **conexão aberta antes/durante o pré-abertura → book não agregado e sem limite o
dia todo; conexão nova depois da abertura → book normal.**

Pergunta 4: o AggrBook de uma assinatura feita antes do pré-abertura (ou que atravessa o leilão de
abertura) muda de modo — deixa de agregar e de limitar à `MaxAggrBookDepth` — e não volta depois da
abertura? É esperado na HML? Na produção acontece o mesmo? Até que se corrija, estamos reconectando
depois das 09:00.
