# SPEC kalman-ordens v1.13 — aprovada em 19/10/2026 (com três ajustes)

Base: v1.12 instalada (`cb88b96`). Referência do PnT: Fast Trader DSA FIX Specification 1.1.0
(`docs/referencia/PnT-DSA-Fix-RoE-English.docx`, fora do git), citada como RoE §.

## 1. Reserva de replaces da 1014 (MaxReplaces)

RoE §4.3.7: a 1014 aceita `20771 MaxReplaces`, "Defaults to 100", Repl=Y. Nas ERs da 1014 de hoje o
PnT **não** ecoa a 20771 nem informa quantos replaces já houve (ecoa só 20807, 20806, 20871, 20882,
20828, 20829). **Contamos nós.**

- **O que conta:** todo 35=G enviado para a 1014 (o mesmo OrderID 37), aceito ou não — conservador,
  porque não sabemos se o PnT conta o pedido ou o aceite. Entram os ajustes do gestor (`POST
  /operacoes/{id}/stop`), o replace de quantidade (fills parciais da entrada) e os da saída por replace.
  A 1014 recriada (nova ordem, novo 37) começa do zero.
- **Onde fica:** em memória na ordem; no restart, recalculado do `kalmanordemevento` (35=G enviados
  com o `ordem_id` da 1014) — sem coluna nova nem migração.
- **Reserva:** `REPLACES_1014_MAX` (100) e `REPLACES_1014_RESERVA` (5). Com `replaces ≥ MAX −
  RESERVA` (95), o ajuste do gestor é recusado e a saída por replace continua cabendo (até
  `MAX_REPRECO_SAIDA + 1` = 4 replaces). O replace de quantidade (proteção da posição) não para.
  Com `replaces ≥ MAX`, a saída vai pelo cancel + 1031.
- **Recusa do ajuste: código novo `PROTECAO_LIMITE_REPLACES`** (decidido; não reaproveita
  `SEM_PROTECAO`), combinado com o lado do Kalman para entrar em `AJUSTE_RECUSAS_ESPERADAS`. Depois da
  reserva, **o trailing segue pelo TRSP do gestor** (a saída por replace, que cabe na reserva).
- **Evento/alerta:** `PROTECAO_RESERVA_REPLACES` (operacao_id, replaces, max) na primeira vez que a
  reserva é atingida; alerta não crítico. Métrica do número de replaces por 1014 no fechamento.
- Se o PnT confirmar que a 20771 pode ser enviada maior (pergunta 3, seção 4), a 1014 passa a mandar
  `20771=REPLACES_1014_MAX` explícito e o limite vira configuração.

## 2. IgnoreTunnel (20864) na 1014

RoE §4.2: `20864 IgnoreTunnel`, aceito em toda criação (D, AB) e no replace (G), Repl=Y, "Defaults to
False". Decisão (19/10): **`20864=Y` no 35=D da 1014 e em todo 35=G dela** (ajuste, quantidade, saída
por replace) **e na 1031 de saída** (35=D do caminho cancel + nova, e o repreço dela). Motivo: o túnel
do PnT tem um defeito conhecido (limite desatualizado depois da entrada, ex. limite 210.000 com o
preço em 209.990), visto no PTF há anos. **Só as entradas ficam no padrão** (`IGNORE_TUNNEL_ENTRADA`,
desligada), com a contagem e o alerta.

**Contagem de túnel:** toda mensagem do PnT (35=8, 35=9, 35=3, 35=j) com "tunnel" no 58 conta na
métrica `kalman_ordens_tunnel_total{papel, tipo}` (`ignorado` = "ignored tunnel lim: X"; `recusa` =
qualquer outro texto com tunnel) e vira evento `TUNNEL` (operacao_id, papel, texto, preço da ordem,
último e bid/ask do /precos/atual no instante). Recusa de **entrada** por túnel: alerta não crítico
`TUNNEL_RECUSA_ENTRADA` — na primeira recusa falsa, ligamos o Y também nas entradas (configuração
`IGNORE_TUNNEL_ENTRADA`, padrão N). Texto conhecido: "Sent Buy price: 5111 ignored tunnel lim:
5316.5;" (PT 2025, multileg com 20864=Y, em 150=E/5/D). Nenhuma mensagem com "tunnel" em 15, 16 e
19/10 (13.549 mensagens FIX das 50 operações).

## 3. Já combinados

1. **Quarentena do lote em erro de dado** (HML 19/10 10:41, seção 12 do ESTADO): erro de dado
   (MySQL 1406, 1366, 1264, 1292, 1048) não retenta para sempre. O lote que falha com erro de dado é
   regravado linha a linha (cada linha na sua transação, na ordem de criação); a linha que falha vai
   para `kalmanordemquarentena` (tabela, linha em JSON, erro, em — migração 0010) com alerta crítico
   `BANCO_QUARENTENA`; o resto grava e o `BANCO_LOTE` resolve. Erro que não é de dado (conexão,
   deadlock) continua como hoje (volta ao lote seguinte). **Os textos são cortados ao tamanho da
   coluna antes da gravação** (em caracteres, sem UTF-8 inválido): o 1406 não ocorre mais. **Com linha
   não resolvida na quarentena, o `servir` sobe em PARADA (BLOQUEAR, origem QUARENTENA) com alerta
   crítico**, até ação explícita: `kalman-ordens quarentena listar` / `resolver -id N -por UID -motivo X`
   e o `POST /retomar`, que recusa com `QUARENTENA_PENDENTE` enquanto houver linha pendente. A 0010
   também amplia `kalmanordempub.tipo` para `VARCHAR(40)` (`PROTECAO_RESERVA_REPLACES` tem 25).
2. **O `servir` registra no journal as migrações aplicadas** (`MigrarRelatando` já devolve a lista):
   `migrações aplicadas: 0009_motivo_60.sql` ou `banco na versão N, nenhuma pendente`.
3. **Convenção de fuso das tabelas:** levantar, coluna por coluna, o que é gravado em UTC
   (`utc()`), em horário de Brasília e em `DATE` do pregão, e quem lê cada uma (API, admin do Kalman,
   expurgo); registrar a convenção no LEIA e corrigir o que destoar.

## 4. Perguntas para o PnT

1. Qual a largura do túnel de preço da 1014 (e da 1031) e há restrição para `IgnoreTunnel=Y`?
2. A `20856 ActivateErrorStatusStrategy` (Repl=N) continua valendo depois de um replace? Depois do 35=G,
   as ERs da 1014 deixam de ecoá-la.
3. O `MaxReplaces` (20771) da 1014 pode ser maior que 100 se enviado no 35=D? Há máximo?
4. (de 19/10, seção 15 do ESTADO) A 1014 com `40=1` envia a ordem disparada a mercado? O exemplo da
   §4.3.7 manda 40=1 e recebe 40=2 com 44.
5. (20/10) A `20864 IgnoreTunnel=Y` enviada no 35=D (e no 35=G) da 1014 é respeitada mesmo sem ser
   devolvida nas respostas? Na 1014 da op 900000037 (20/10 10:54:06.943, `KM-KHML-P-900000037-1`) ela foi
   enviada e o 150=A/150=0 não a trazem.
6. (20/10) Duas sessões FIX na mesma conta (99999: kalman-ordens e PTF): cada sessão só vê as próprias
   ordens e posição no 35=AF. Há limite de risco por conta (posição máxima, perda, ordens abertas) que
   as duas sessões dividem? A 1014 de uma sessão olha a posição da conta inteira ou só a da sessão?

## 5. Testes (roteiros)

- 91: 20864=Y na 1014, no replace dela e na 1031 de saída; ausente na entrada; TUNNEL ignorado e
  recusa de entrada (alerta e métrica).
- 92: reserva de replaces (MAX 6, reserva 2): evento no 4º, recusa no 5º, a contagem sobrevive ao
  restart, a saída por replace cabe.
- 93: quarentena da linha da operação (erro de dado injetado), o serviço segue, o banco fica atrás;
  restart em PARADA; `/retomar` recusado até a resolução.
