# Spec de Implementação — `kalman-ordens` — v1.4

Revisão da [SPEC-ORDENS-v1.3.md](SPEC-ORDENS-v1.3.md) (que revisa a v1.2, a v1.1 e a v1). Lista
**só o que mudou**; onde a v1.4 não diz nada, vale a v1.3.

**Revista pela [v1.5](SPEC-ORDENS-v1.5.md)** (15/10): histórico para a tela de admin, registro da sessão,
retenção.

Tema: **ajuste da proteção pelo gestor** (replace da 1014), **textos em ASCII** e uma pergunta
nova ao PnT.

Data: 2026-10-15. Itens vindos do responsável (lote de ajustes de 15/10).

---

## 1. Ajuste da proteção (K)

| # | Regra | Substitui | Roteiro |
|---|---|---|---|
| K1 | **`POST /operacoes/{id}/stop`** com o novo stop em **preço** (`preco_stop`) **ou** em **pontos a partir do preço médio da entrada** (`stop_pts`), um dos dois; `modulo` opcional (ausente = KALMAN); `uid` opcional (se vier, tem de ser o da operação). O serviço arredonda ao tick para o lado mais perto do mercado (comprado: para cima; vendido: para baixo — como o gatilho calculado na entrada), recalcula o limite com a mesma folga (`FOLGA_STOP_PTS`) e faz o **35=G da 1014** (20828/20829, 44 = limite). | — (o stop só vinha do `stop_pts` da entrada) | 56 |
| K2 | **Validação**: comprado → stop **< bid** atual; vendido → stop **> ask** atual (depois do arredondamento); senão recusa **`STOP_ALEM_DO_PRECO`** — a 1014 dispararia na hora; quem sai, nesse caso, é o gestor. Sem bid/ask do símbolo da posição com até `PRECO_IDADE_MAX_S` → `SEM_PRECO`. Sem posição (ou operação inexistente/encerrada) → `SEM_POSICAO`; operação em saída ou sem 1014 viva → `SEM_PROTECAO`; 1014 já disparada no PnT (v1.3 J2) → `PROTECAO_DISPARADA`; campos → `CAMPO_INVALIDO`. Nenhuma recusa gera alerta nem conta para a parada por limite. | — | 57, 59 |
| K3 | **Um replace por vez; o último pedido vale.** O pedido aceito fica na operação (`stop_ajustado`, migração `0005_stop_ajustado.sql`). Com um pedido da 1014 sem resposta (envio, replace, cancel, erro), nada sai agora (`situacao=AGUARDANDO`); quando ele responder, sai **só o último**. **Degrau mínimo `AJUSTE_DEGRAU_MIN_PTS` = 25** em relação ao gatilho que está no PnT: menos que isso → aceito e ignorado (`situacao=IGNORADO`, sem replace); a mesma regra vale para o pedido guardado na hora de mandá-lo. Exatamente 25 sai. | — | 58 |
| K4 | **O ajuste é o stop da operação daí em diante**: proteção nova, recriada (P8, invariante, conciliação, restart) ou ajustada pela quantidade usa o gatilho ajustado, não o `stop_pts` da entrada. Na recriação, vale o **último pedido** (ex.: ajuste pendente e o PnT cancela a 1014 → a recriada já vem com o gatilho pedido). | P8 da v1.1 ("mesmo gatilho e limite") quando há ajuste | 56 |
| K5 | **Eventos**: **`PROTECAO_AJUSTADA`** depois do **39=5** (`preco_stop`, `preco_limite`, `qtd`, `preco_stop_anterior`, `preco_limite_anterior`) — no lugar do `PROTEGIDA` que o replace da 1014 publicava (o replace de quantidade continua com `PROTEGIDA`). **Recusa do PnT** ao replace de ajuste (35=9, ou 35=3/35=j ligados ao ClOrdID do replace) → **`PROTECAO_AJUSTE_RECUSADO`** (`motivo` = `[102] 58`, `preco_stop_pedido`, `preco_stop`/`preco_limite` mantidos, `qtd`); o stop que está no PnT fica e passa a ser o pedido da operação (nada tenta de novo sozinho), o pedido guardado cai; `RECUSA_PNT` como sempre. **Não** é a 1014 recusada: nada de `PROTECAO_REJEITADA` nem da zeragem da 2ª rejeição (Parte 3.4). | — | 56, 60 |
| K6 | **Disparo ao mesmo tempo** (v1.3 J2): 1014 disparada (39=B com 20807>0) não recebe replace — nem o pedido guardado, nem o ajuste pela quantidade; quem decide são os fills. O 35=9 do replace que cruzou com o disparo vira `PROTECAO_AJUSTE_RECUSADO`; um 39=5 que chegue depois do disparo vira `PROTECAO_AJUSTADA`, e nada mais sai. | — | 59 |

Resposta aceita: `{"aceito": true, "modulo", "operacao_id", "situacao": "ENVIADO"|"AGUARDANDO"|"IGNORADO",
"preco_stop", "preco_limite"` (o pedido, já no tick) `, "preco_stop_pnt", "preco_limite_pnt"}` (o que
está no PnT agora). 503 `RESULTADO_DESCONHECIDO` como no `POST /ordens` (o motor segue com o
pedido; reenviar o mesmo ajuste é inofensivo). `GET /ordens/{id}` traz `preco_stop_ajustado` (0 =
sem ajuste) e o `preco_stop` de cada ordem (o da 1014 = o que está no PnT).

O ajuste não tem limite próprio de frequência: o degrau e o "um por vez" limitam as mensagens; os
35=G contam no limite de mensagens por minuto das entradas, como os demais envios.

## 2. Textos em ASCII (L)

| # | Regra | Teste |
|---|---|---|
| L1 | **Textos de recusa (`motivo`), de alerta (Telegram e evento `ALERTA`) e os campos de texto de todo evento publicado saem só em ASCII**: acentos viram a letra sem acento, `−`/`–`/`—` → `-`, `→` → `->`, `≠` → `!=`, `≥`/`≤` → `>=`/`<=`, `×` → `x`, `…` → `...`; o resto, `?`. Motivo: o Kalman grava em latin1 e o `−` do `BOOK_DESCOLADO` ("\|meio − preco\|") quebrou a gravação. Vale também para o texto 58 do PnT repassado nos alertas e eventos. Os logs do serviço (journal) não mudam. | `internal/alerta` `TestASCII`, `TestEmitirEmASCII`; o executor de roteiros reprova qualquer evento, alerta, Telegram ou `motivo` de resposta fora do ASCII, em todos os roteiros |

## 3. Pergunta ao PnT [PnT-18]

**"Depois do 39=6 de uma 1014 sem ordem filha (20807=0), ela ainda pode disparar?"**

Hoje, para sair (gestor, stop furado, parada), o serviço cancela a 1014 e só manda a 1031 de saída
depois do **39=4** (Parte 3.5), que na HML chega **~5 s depois do 39=6** (v1.3 F1). Se a resposta for
**não** (com 20807=0 no 39=6 a 1014 não dispara mais), a saída pode sair já no 39=6 e a virada de mão
ganha ~3–4 s. Se for **sim**, fica como está (sair no 39=6 arriscaria duas saídas: a 1014 e a 1031).
Até a resposta, nada muda. Na tabela de pendências da [SPEC v1](SPEC-ORDENS-v1.md).

## 4. Configuração

| Chave | Inicial | Uso |
|---|---|---|
| `AJUSTE_DEGRAU_MIN_PTS` | 25 | K3 |

Migração `0005_stop_ajustado.sql`: coluna `stop_ajustado` (NULL = sem ajuste) em
`kalmanordemoperacao`. Só `ALTER`.

## 5. Contrato com o gestor — o que muda

- Rota nova `POST /operacoes/{id}/stop` (K1–K3). Recusas novas: `STOP_ALEM_DO_PRECO`,
  `SEM_PROTECAO`, `PROTECAO_DISPARADA` (só nessa rota); `SEM_PRECO`, `SEM_POSICAO` e
  `CAMPO_INVALIDO` com o sentido de sempre.
- Eventos novos: `PROTECAO_AJUSTADA`, `PROTECAO_AJUSTE_RECUSADO` (K5). O replace da 1014 por
  ajuste não publica mais `PROTEGIDA`.
- Textos de recusa, alerta e evento só em ASCII (L1).

## 6. Cobertura dos roteiros

| Roteiro | Cobre |
|---|---|
| 56 | K1, K4, K5: ajuste em preço, em pontos e com arredondamento ao tick; `PROTECAO_AJUSTADA` só no 39=5; restart e conciliação mantêm o ajuste; ajuste pendente e a 1014 cancelada pelo PnT → recriada com o gatilho pedido |
| 57 | K2: além do preço (comprado e vendido, com o arredondamento), sem preço, campos, outro módulo, sem posição, em saída |
| 58 | K3: degrau (ignorado, exatamente 25), três pedidos com o replace pendente → só o último; guardado a menos do degrau do novo stop do PnT não sai |
| 59 | K6: ajuste cruzando com o disparo — replace recusado (pedido guardado cai, `PROTECAO_DISPARADA` na recusa seguinte) e replace aceito depois do disparo (nada mais sai); fills zeram com `STOP_SERVIDOR`, sem stop furado |
| 60 | K5: recusa do PnT por 35=9 (duas seguidas, sem zerar) e por 35=j; ajuste aceito depois |
