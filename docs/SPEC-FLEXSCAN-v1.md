# SPEC kalman-ordens — conector Flexscan (REST) — v1 (desenho, rev. 23/10/2026 tarde)

**Estado: desenho, não implementado.** Revisão depois da sondagem do apisim de 23/10 (usuário
`flx_gammaquant`, conta `S-1117150`; arquivos em `var/flex-sim/`: respostas `01–16`, ações
`acoes*.json`, webhooks reais `webhook/003–017.json`). A versão da manhã supunha o corpo do
webhook e a semântica do stop; esta usa o que foi observado. Fonte da API:
`docs/referencia/flexscan-algotrading-1.0.0.json`.

**Contrato com o Kalman inalterado:** `POST /ordens`, `POST /sinais`, eventos, recusas e estados
publicados são os mesmos; o Kalman não sabe qual corretora executou.

**Régua:** apisim observado > OpenAPI. O que ainda não foi observado está marcado **[SIM-n]**
(próxima sondagem, §10.3) ou **[FX-n]** (pergunta à Flexscan, §11).

---

## 0. Resumo

1. **Ponte, não segundo motor.** O motor continua falando FIX por dentro. A ponte Flexscan
   implementa `Envio` (traduz a `NewOrderSingle`/`Replace`/`Cancel` que o motor já monta para
   REST) e devolve ao motor ExecutionReports sintéticos. Limites, travas, disjuntor, alertas,
   TRSP e conciliação são o mesmo código para as duas corretoras.
2. **Duas fontes, um estado.** A consulta (`GET /orders/{id}`, um `order_record`) e o webhook
   (lista de **retratos de estratégia `sorder`**) são normalizados para o mesmo `estadoFlex` por
   `ClOrdID` (o `id` deles) e fundidos por uma regra **monótona e idempotente** (§4.3): a ordem
   de chegada e a ordem dentro da lista não importam.
3. **Caminho crítico pela consulta ativa:** depois de cada POST/PUT/DELETE, a ponte consulta a
   ordem até ver o efeito (~0,3 s por consulta). **O webhook cobre os eventos espontâneos** (stop
   executado pela B3, limite em repouso executada), que chegam em < 1 s, e cada um dispara uma
   consulta daquela ordem.
4. **Saída por replace descartada para a Flexscan:** o `PUT` com gatilho ultrapassado é recusado
   (`razão: 4. Sell order stop price must be below referential price`, webhook 013). Saída =
   `DELETE` da proteção + limite (~550 ms medidos).
5. **Armadilha de vocabulário:** `5` no webhook é **FINISHED** (executada); `5` na consulta é
   **REPLACED** (viva). As duas tabelas ficam separadas no código (§4.1, §4.2), com teste.
6. **Mínimo útil** (§12): entrada, proteção, TRSP, saída, stop, webhook + consulta, conciliação no
   boot, emergência ordem a ordem. **Estimativa pelo ritmo do repositório: 1 dia de sessões para
   o mínimo útil + 1 pregão no apisim com a kalman-hml + instalação**; o completo, mais meio dia.

---

## 1. O que a sondagem mostrou

| Tema | Observado | Arquivo |
|---|---|---|
| POST | 202 `{status:"PENDING NEW", id}`; o `id` (UUID) é o `ClOrdID` do retrato e o `clordid` das execuções | 01, 07, 09 |
| Conta / símbolo | `S-1117150` (letra e hífen: a validação "só dígitos" do PnT não serve), `WINZ26` | 02 |
| Bolsa | mandamos `XBMF`; a consulta devolve `exchange:"BVMF"`, o retrato `LegSecurityExchange:"XBMF"` | 02, 003 |
| Números | consulta: `price` número, **`stopPrice` e `avgPrice` texto** (`"207460.00000"`); retrato: tudo número | 11, 08, 007 |
| Horário | retrato `TimeStamp` em **Brasília**, resolução de 1 s; consulta `transactTime`/`sendingTime` em **UTC** | 003 × 02 |
| Consulta: estados | 0 viva → (PUT) **5 viva, valores novos** → (DELETE) 4; execução total: 2, `execType F`, `uniqueTradeId`, `avgPrice` | 02, 04, 06, 08 |
| Retrato: estados | **3** viva (aceita, editada **e** edição recusada), **5** executada, **7** cancelada; `ExecutedQtty` e `AvgPrice` acumulados; `CustomParameters.PriceLimit`/`StopPrice` atuais; `Text` legível ("Ordem aceita, 1@…", "Editada, …", "Cancelada,…", "Executada, 1/1@…") | 003–017 |
| Retrato: execução | `Legs[0].LegExecQty`, `LegLastPx`, `LegLastQty`; **sem `uniqueTradeId`** | 006 |
| Edição recusada | retrato: `OrdStatus 3`, valores antigos, `Text "Edicao rejeitada, razão: 4. …"`; consulta: estado e valores do último aceito (0 ou 5) com `clxRejReason "4"` | 013, 15 |
| Stop executado pela B3 | retrato 3 → 5 direto, sem retrato de "disparo"; `LegWorkingIndicator` N (aceita) → **Y** (no retrato da execução) | 016, 017 |
| Ordem das listas | webhook 009 com 2 itens: a saída executada (ação 11:42:05.749) **antes** do cancel da proteção (ação 11:42:05.482), mesmo `TimeStamp` ao segundo | 009 |
| Um webhook por ação | sim (com `order.all` + `algo.changed` cadastrados) | `receptor.log` |
| Latência | webhook 0,3–1,1 s depois da ação; consulta ~0,3 s; PUT/DELETE ~300 ms; saída DELETE + limite ~550 ms | `acoes*.json`, `receptor.log` |
| Origem do webhook | `177.71.196.212`, `User-Agent python-requests/2.19.1`, sem autenticação nem assinatura | cabeçalhos |
| Cadastro | a Flexscan testa a URL com um não-POST e recusa o cadastro se receber 4xx; o nginx responde 200 a não-POST e só repassa POST | — |
| Execuções | `GET /executions` devolve `clordid` = o `id` | 09 |
| Túnel | compra 15% abaixo do preço **aceita** no simulador. **Informado pela Flex (23/10):** ordem com preço fora do túnel da B3 não é recusada — a Flex ajusta o preço até o limite indicado no erro da B3 e reenvia. Recusa real (estado 8 ou 4xx no POST) é rara (conta, ativo, horário), com o motivo no `Text` | `acoes-testes-123.json`, 010 |
| Caminho | o nginx repassa `/flexscan/webhook/<segredo>`, **sem** o prefixo `/kalman-ordens`, para `.53:9098` | `receptor.py` |

**Não observado:** fill parcial (todas as ordens de 1 contrato), ordem nova recusada, stop
disparado sem execução, 401/expiração do token, timeout, fora do horário.

---

## 2. Decisões (base do usuário × sondagem)

| # | Tema | Decisão |
|---|---|---|
| 1 | Seleção | Destino do `Robo.CORSeqId` do GQ (85 PnT, 88 Flexscan; congelado na entrada, U4), **mais** a lista de destinos na habilitação (`usuario habilitar -destinos pnt,flexscan`; padrão `pnt`). Destino fora da lista → `DESTINO_NAO_HABILITADO`. Limites globais **somam os dois destinos**; `BLOQUEAR`/`ZERAR` valem para os dois; disjuntor e sessão por destino. |
| 2 | Auth | Login às 08:30 e no boot; renovar pelo `exp` do JWT − 30 min (sem `exp`: 9 h); 401 → um login e um reenvio só daquele pedido; segundo 401 → `SEM_SESSAO` (Flexscan) + crítico. `LoginPushResponse` (MFA) → crítico, sem retentativa. Credenciais só no `service.env`; token, senha e `Authorization` nunca em log. |
| 3 | Entrada | `POST /orders {symbol, side, qtty, ordType:"2", price, account, exchange:"XBMF", timeInForce:"0", name}`, `name` = `ClOrdBase` (`KM-E-<op>-<n>`). Timeout/5xx/conexão caída = "não sei": procurar pelo `name` (`GET /orders/filter?account=` filtrado do nosso lado; o webhook também traz o `Name`) antes de qualquer reenvio. Entrada não achada **não é reenviada** (`FALHA_ENVIO`); proteção é recriada com `name` novo e a antiga vigiada (órfã → cancel + `FLEXSCAN_ORFA`). |
| 4 | Proteção | `ordType:"4"`, `stopPrice` = gatilho, `price` = limite (os `Stop`/`StopLim` da 1014, `calcularStop` inalterado). **O stop fica na B3** (sondagem: executado pela B3 sem ação nossa). Brackets fora. |
| 5 | TRSP e saída | TRSP = `PUT {stopPrice, price}`. Saída = `DELETE` + limite, sempre; `SAIDA_POR_REPLACE` desligada no destino 88 por código (recusa observada). Consequência: gatilho do lado errado do preço de referência é **recusado** (PUT) — no PUT do TRSP vale o caminho "replace recusado" de hoje (fica o stop anterior); no POST da proteção logo depois de um fill em mercado rápido, a recusa leva ao caminho de proteção rejeitada de hoje (recriar/zerar). |
| 6 | Execuções | Webhook para o espontâneo + consulta ativa no caminho crítico + consulta periódica das vivas (5 s) + passada completa por conta (60 s). Idempotência por (`ClOrdID`, quantidade executada, estado) — §4.3. O `uniqueTradeId` (só na consulta e em `/executions`) vai para o registro de cada negócio. |
| 7 | Conciliação | No boot e na volta da sessão: `GET /orders/filter?account=` de cada conta → ordens `name` `KM-` de hoje; `GET /executions?clordid=<id>` confere a soma. `/positions` só conferência (fica para depois do mínimo útil). |
| 8 | Emergência | Cancel ordem a ordem. Sem código para `/algos/commands`, `DELETE /webhooks` ou `remove_previous` (teste de arquitetura procura as strings). `DELETE /webhooks/{id}` não confere dono: só pela CLI, só com id que o nosso `GET /webhooks` devolveu. |
| 9 | Horário | 400 fora do horário → `FORA_DO_HORARIO`, fora do disjuntor. Formato do 400 a observar [SIM-6]. |
| 10 | Testes | Flexscan falso programado com os retratos reais de `var/flex-sim/webhook/` (viram fixtures); depois o apisim de ponta a ponta. |

---

## 3. Arquitetura

```
Kalman ──HTTP──► motor (goroutine única)
                   │ Envio por destino (op.Destino)
          ┌────────┴──────────┐
      pnt.Sessao           flexscan.Ponte ──REST──► apisim / produção
      (FIX, 85)            (88)   ▲  ▲
                                  │  └── consulta ativa / periódica (order_record)
   nginx ─► 172.16.110.53:9098 ───┘ webhook (retratos sorder)
                     │
            normalizar → fundir por ClOrdID (§4.3) → ER sintético → fila do motor
```

- `internal/flexscan`: cliente REST, auth, limite de taxa, tradução de envio, normalização dos
  dois formatos, fusão, ERs sintéticos, receptor do webhook, consultas.
- O motor **nunca espera HTTP**: `Enviar` só enfileira; respostas e estados entram na fila do
  motor como as mensagens FIX.
- **Mudanças no motor** (pequenas e localizadas): `m.envio[destino]` em vez de `m.fix`;
  sessão, conciliação e disjuntor por destino; validação de conta por destino (88:
  `^[A-Za-z0-9-]{1,20}$`); trava cruzada `FLEXSCAN_CONTAS_HML` (hoje `S-1117150`); coluna
  `destinos` na habilitação (migração só `ALTER`); destino 88 aceito só com `FLEXSCAN_URL`.
- **Identificadores:** `name` = `ClOrdBase`; o `id` deles vai no **37 dos ERs sintéticos** e o
  motor já o grava em `kalmanordem.order_id` e indexa (`por37`) — sem migração. Replace e
  cancel: o motor gera ClOrdID novo; a Flexscan mantém o `id`; a ponte guarda o ClOrdID do
  pedido pendente e responde `11` = novo, `41` = antigo, como o PnT. O `marketOrderId` (muda a
  cada edição, 02 × 04 × 06) e o `orderId` só vão para o registro.
- **Não emulado:** `39=7` (erro do PnT), `20864` (túnel), mass status FIX. O `150=I/39=B`
  (disparo da 1014) tem candidato: `LegWorkingIndicator` N → Y com `LegExecQty 0` [SIM-3].

---

## 4. Tradução

### 4.1 Consulta (`order_record`, `GET /orders/{id}`)

| `orderStatus` | Significado | Estado normalizado |
|---|---|---|
| 0 | NEW | viva |
| 1 | PARTIALLY_FILLED | viva |
| **5** | **REPLACED (viva, valores novos)** | viva |
| 2 | FILLED | executada |
| 4 | CANCELLED | cancelada |
| 8 | REJECTED | rejeitada (`text`, `clxRejReason`) |
| C | EXPIRED | expirada |

Executado = `quantityExecuted`, médio = `avgPrice` (texto), preço/gatilho = `price`/`stopPrice`
(texto), negócio = `uniqueTradeId`/`lastPrice`/`lastQuantity`. Edição aceita = `orderStatus 5`
com `transactTime` depois do PUT; recusada = `clxRejReason` ≠ "" que apareceu depois do PUT
[SIM-4: o `clxRejReason` some num PUT aceito seguinte?]. O `price` **não** decide: a Flex pode
tê-lo ajustado ao túnel (§4.3, regra 3).

### 4.2 Webhook (retrato de estratégia, `Status_t`)

| `OrdStatus` | Significado | Estado normalizado |
|---|---|---|
| 0, 1, 2, 10 | NEW, WAITING_APPROVAL, SCHEDULED, PENDINGNEW | pendente |
| 3 | RUNNING (aceita, editada, edição recusada) | viva |
| **5** | **FINISHED (executada)** | executada |
| 6 | CANCELING | cancel pendente |
| 7 | CANCELED | cancelada |
| 4, 8, 9 | UNBALANCED, SUSPENDING, SUSPENDED | inesperado: consulta imediata + crítico `FLEXSCAN_ESTADO_INESPERADO` |

Executado = `ExecutedQtty`, médio = `AvgPrice`, preço/gatilho = `CustomParameters.PriceLimit`/
`StopPrice`, último negócio = `Legs[0].LegLastPx`/`LegLastQty`. Edição aceita = `Text`
começando por "Editada"; recusada = `Text` começando por "Edicao rejeitada". `5` com
`ExecutedQtty` < `TargetQtty` (se acontecer) → consulta decide. Ordem nova recusada (rara: conta,
ativo, horário; motivo no `Text`): sem exemplo [SIM-1]; até lá, `OrdStatus` fora da tabela ou
`Text` com "rejeit" → consulta imediata, e a consulta (`orderStatus 8`) decide.

### 4.3 Fusão (idempotente, sem depender da ordem)

Por `ClOrdID`, a ponte guarda o melhor estado conhecido; um estado novo (de qualquer fonte) só
**acrescenta**:

1. **Executado só sobe:** `executado = max(conhecido, novo)`; o médio acompanha o maior executado.
2. **Terminal é definitivo:** executada/cancelada/rejeitada/expirada não voltam a viva. Entre dois
   terminais diferentes vale a consulta.
3. **Edição e preço — decide o estado, não o valor.** A Flex ajusta ao túnel da B3, sem pedido
   nosso, o preço que estiver fora dele e reenvia (informado por ela, 23/10). Portanto:
   - com PUT pendente: sinal de aceite (§4.1, §4.2) → confirmada, **mesmo que o preço seja
     outro**; sinal de recusa → recusada; nenhum dos dois → espera (até `FLEXSCAN_CONFIRMA_S`);
   - sem PUT pendente, `PriceLimit`/`price` diferente do enviado com estado e quantidade
     coerentes → **não é divergência nem edição falha**: a ponte grava o preço efetivo na ordem,
     registra o evento `FLX-PRECO-AJUSTADO` (enviado → efetivo) e não alerta;
   - divergência de verdade é de **estado, quantidade, lado, conta ou símbolo** (§5.2 item 4);
     `StopPrice` diferente sem PUT nosso → consulta e crítico `FLEXSCAN_ESTADO_INESPERADO` (o
     túnel é de preço de ordem; se a Flex também mexer no gatilho, vira regra igual à do preço,
     [FX-10]).
4. **Lista do webhook:** cada item é fundido sozinho; a ordem dos itens não é usada (009).
5. **Tempo:** só para registro e métricas (`TimeStamp` BRT → UTC); nunca para decidir.
6. Saída da fusão = diferenças em relação ao último ER entregue ao motor → ERs sintéticos:

| Diferença | ER |
|---|---|
| 202 do POST | `150=A 39=A` |
| pendente/nada → viva | `150=0 39=0` |
| executado subiu | `150=F 39=1/2 14=<executado> 6=<médio> 32/31=<último negócio>` |
| edição confirmada | `150=5`, `11` novo, `41` antigo |
| edição recusada | `35=9` (o motor já tem o caminho "replace recusado") |
| cancelada | `150=4 39=4` |
| rejeitada (ou 400 no POST) | `150=8 39=8 58=<texto> 103=<código>` |
| 400 no PUT/DELETE de ordem terminal | `35=9` + consulta (o caminho "too late" de hoje) |

ExecID (17) sintético = `<id>:<estado>:<executado>:<preço>:<gatilho>`: o mesmo estado vindo das
duas fontes gera o mesmo ExecID, e o `registrarRecebida` do motor descarta o segundo.

### 4.4 Consulta ativa (caminho crítico)

- Depois do 202 de POST/PUT/DELETE: `GET /orders/{id}` em +0,3 s, +0,6 s, +1,2 s, +2,4 s (até
  `FLEXSCAN_CONFIRMA_S` = 5 s) — para no primeiro que mostra o efeito. Um webhook da mesma
  ordem no meio do caminho é fundido e também encerra a espera.
- Sem efeito em 5 s → "não sei" (§2, item 3): `SEM_CONFIRMACAO` e o caminho de hoje.
- Timeout de pedido `FLEXSCAN_TIMEOUT_MS` (3000); taxa `FLEXSCAN_REQ_POR_S` (10, rajada 5) até
  [FX-1]; prioridade proteção > saída/cancel > entrada > consultas periódicas.

---

## 5. Webhook

### 5.1 Rota e listener

- Público → nginx → **`172.16.110.53:9098`**, caminho `/flexscan/webhook/<segredo>` (sem
  prefixo), só POST; o nginx responde 200 a não-POST (o cadastro da Flexscan exige — não mudar).
- **Listener separado** (`FLEXSCAN_WEBHOOK_ENDERECO=172.16.110.53:9098`) com **uma rota só**; o
  `127.0.0.1:9092` do admin continua só loopback (regra do `main.go` mantida). Na instalação, o
  receptor provisório (`var/flex-sim/receptor.py`) sai da 9098 antes do serviço subir.
- Firewall da .53: 9098 só para o IP do nginx. No nginx: `allow 177.71.196.212; deny all;` quando
  a Flexscan confirmar que o IP é fixo [FX-7].
- Segredo: `FLEXSCAN_WEBHOOK_SEGREDO` (≥ 32 caracteres; o atual de `var/flex-sim/segredo.txt`
  serve), comparação em tempo constante. Segredo, método ou caminho errado → **404 sem corpo**.

### 5.2 Validação

1. Corpo ≤ 64 KiB, JSON, **lista** de objetos (objeto solto também aceito). Outra forma → 200,
   corpo gravado, `FLEXSCAN_WEBHOOK_INVALIDO` e consulta das vivas.
2. Item com `StrategyCode` ≠ `sorder`, ou `Name` sem `KM-` → ignorado (contador).
3. `Name` KM sem ordem conhecida → `DESCONHECIDA` (de hoje) + consulta.
4. `ClOrdID` ≠ o `id` gravado para aquele `Name`, ou `ILegAllocAccount`/`LegSymbol`/`LegSide`
   ≠ os da ordem, ou `UserName` ≠ `FLEXSCAN_USUARIO` → descartado + crítico
   `FLEXSCAN_WEBHOOK_INCOERENTE`.
5. `ExecutedQtty` > `TargetQtty` ou negativo → descartado + crítico.
6. **Fill espontâneo** (executado subiu sem ação nossa em curso): com `FLEXSCAN_CONFIRMAR_FILL=1`
   o fill aplicado é o da consulta que o webhook dispara (+~0,3 s até a proteção), porque o
   webhook não tem autenticação além do segredo. Com o filtro de IP no nginx ligado, passa a 0
   (aplica o retrato direto; a consulta segue em paralelo e uma diferença vira `DIVERGENTE`).
   Stop executado não gera ordem nova: aplica direto nos dois modos.
7. Resposta **200 depois de validar e enfileirar** (antes do motor); fila cheia → 503.

### 5.3 Log

- Cada corpo em `kalmanordemevento`, `REC`/`FLX-WH`, `clordid` = `Name`, `order_id` = `ClOrdID`,
  com IP de origem; pedidos/respostas REST como `ENV`/`FLX-POST|PUT|DELETE|GET` e
  `REC`/`FLX-<status>`. Campos pessoais do retrato (`DocNumber`) mascarados.
- **Nunca gravados:** o segredo (o caminho aparece como `/flexscan/webhook/***`), `Authorization`,
  corpo do login, `auth_token`. Regras no `pnt/mascara.go`; teste procura o segredo e a senha de
  teste em toda a saída dos roteiros (journal e banco).
- 404: só contador `flexscan_webhook_recusados_total{motivo}` e no máximo uma linha por minuto
  (IP, motivo; sem caminho).

### 5.4 Cadastro

CLI `kalman-ordens flexscan webhook listar` (URL com segredo mascarado) e `registrar`
(`target {type:"user", id:<usuário>}`, triggers `order.all`, **sem `remove_previous`**). O de
hoje (feito à mão, `order.all` + `algo.changed`) fica; o serviço confere no boot e a cada hora
(`GET /webhooks`) e, sem o nosso URL, alerta crítico `FLEXSCAN_WEBHOOK_AUSENTE` (a consulta
periódica segura as execuções). Rotação de segredo (cabem 2 por alvo) fica fora do mínimo útil.

---

## 6. Configuração

| Variável | Padrão | Uso |
|---|---|---|
| `FLEXSCAN_URL` | — | `https://apisim.flexscan.com.br/v1` na HML; vazio = sem Flexscan (88 recusado como hoje) |
| `FLEXSCAN_USUARIO` / `FLEXSCAN_SENHA` | — | login |
| `FLEXSCAN_WEBHOOK_SEGREDO` | — | ≥ 32 caracteres |
| `FLEXSCAN_WEBHOOK_ENDERECO` | — | `172.16.110.53:9098`; vazio = só consulta |
| `FLEXSCAN_CONTAS_HML` | — | `S-1117150` |
| `FLEXSCAN_BOLSA` | `XBMF` | |
| `FLEXSCAN_REQ_POR_S` | 10 | até [FX-1] |
| `FLEXSCAN_TIMEOUT_MS` | 3000 | |
| `FLEXSCAN_CONFIRMA_S` | 5 | consulta ativa |
| `FLEXSCAN_CONSULTA_S` | 5 | vivas |
| `FLEXSCAN_CONFIRMAR_FILL` | 1 | §5.2 item 6 |
| `FLEXSCAN_LOGIN_HORA` | 08:30 | |

## 7. Alertas novos (`docs/ALERTAS.md`)

| Código | Quando | Nível |
|---|---|---|
| `FLEXSCAN_LOGIN` | 3 logins seguidos falharam; senha bloqueada/expirada; MFA | crítico |
| `FLEXSCAN_PERMISSAO` | 403 em ordem | crítico |
| `FLEXSCAN_WEBHOOK_AUSENTE` | nosso URL fora do `GET /webhooks` | crítico |
| `FLEXSCAN_WEBHOOK_ATRASADO` | a consulta viu mudança espontânea que o webhook não trouxe em 10 s | aviso (crítico após 5 min) |
| `FLEXSCAN_WEBHOOK_INVALIDO` | corpo com segredo certo e forma desconhecida | aviso (crítico 3×/min) |
| `FLEXSCAN_WEBHOOK_INCOERENTE` | `ClOrdID`/conta/símbolo/lado/usuário não batem | crítico |
| `FLEXSCAN_ESTADO_INESPERADO` | `OrdStatus` 4, 8 ou 9; `StopPrice` mudou sem PUT nosso (preço limite ajustado ao túnel **não** alerta) | crítico |
| `FLEXSCAN_ORFA` | ordem procurada depois de timeout apareceu depois de recriada | crítico |
| `DESTINO_NAO_HABILITADO` | destino do GQ fora da habilitação | recusa, sem alerta |

Os de hoje valem para os dois destinos e levam o destino no texto (`[flexscan]`/`[pnt]`).

---

## 8. Emergência e horário

`ZERAR`/`BLOQUEAR` sem mudança: cancel ordem a ordem + saída limite. Fora do horário o 400
(inclusive no cancel) não abre o disjuntor; posição KM no destino 88 às `HORA_ZERAGEM` → crítico
`FLEXSCAN_POSICAO_NO_FIM` (depois o cancel e a saída serão recusados).

## 9. Riscos que ficam

- **Fill parcial nunca visto:** as fixtures são de 1 contrato. A fusão trata parcial por
  construção (executado só sobe), mas o formato real (consulta `1`? retrato `3` com
  `ExecutedQtty` parcial?) é [SIM-2].
- **Recusa de ordem nova sem exemplo:** rara (o túnel não recusa, a Flex ajusta); o motivo vem
  no `Text` e o caminho de proteção rejeitada de hoje cobre [SIM-1].
- **Preço ajustado ao túnel:** a Flex reenvia no limite do túnel. A suposição é que o ajuste é
  sempre para dentro (menos agressivo que o nosso limite), então o fill nunca é pior que o
  pedido; a ponte confere no fill (compra: médio ≤ nosso limite; venda: ≥) e, se não, crítico
  `FLEXSCAN_ESTADO_INESPERADO`.
- **Stop disparado sem execução** (gap além do limite): sem sinal confirmado, o "stop furado"
  (`ESPERA_STOP_S`) fica pelo preço de mercado + `LegWorkingIndicator` [SIM-3].
- **Segredo no URL:** aparece no log de acesso do nginx e no cadastro da Flexscan. Pedido: o
  nginx não registrar o caminho (ou mascarar).

---

## 10. Testes

### 10.1 Flexscan falso (`internal/flexscan/falso`)

Servidor local com login (JWT com `exp` curto, 401 programável), `/orders` (POST, PUT, DELETE,
GET, filter), `/executions`, `/webhooks`, e um emissor de webhooks para o receptor. Retratos e
`order_record` **gerados a partir das fixtures reais** (`var/flex-sim/` copiado para
`internal/flexscan/testdata/`, sem segredo, `DocNumber` trocado). Falhas programáveis: timeout
com e sem ordem criada, 5xx, 400 de horário, webhook repetido, lista fora de ordem, webhook antes
do 202, webhook perdido, forjado, malformado; edição recusada (`razão: 4`).

### 10.2 Roteiros (mínimo útil = 1–12; completo = todos)

1. entrada agressiva → executada (consulta ativa) → proteção tipo 4 → `PROTEGIDA`;
2. entrada em repouso → fill espontâneo pelo webhook (com e sem `CONFIRMAR_FILL`);
3. o mesmo retrato duas vezes + lista fora de ordem (009) + consulta e webhook do mesmo estado →
   um fill, uma proteção; `PriceLimit` ajustado ao túnel sem PUT nosso → sem alerta, preço
   efetivo gravado; PUT aceito com preço ajustado → confirmado;
4. TRSP aceito (`5` na consulta = viva) e recusado (`razão: 4`): fica o stop anterior;
5. saída: DELETE da proteção + limite; corrida (stop executa durante o DELETE → 400);
6. stop executado pela B3 (016 → 017) → zerada, sem ação nossa;
7. timeout no POST da entrada: criada (achada pelo `name`) e não criada (`FALHA_ENVIO`);
8. 401 → login + reenvio único; 401 de novo → `SEM_SESSAO`;
9. webhook mudo: a consulta periódica pega o stop executado; `FLEXSCAN_WEBHOOK_ATRASADO`;
10. segredo errado / outro método / outro caminho → 404 igual; segredo fora de todo log;
11. boot com ordem viva e com execução não vista → conciliação; KM desconhecida → `DIVERGENTE`;
12. teste de unidade das duas tabelas de estado (o `5` de cada lado) e teste de arquitetura
    (sem `/algos/commands`, `DELETE /webhooks`, `remove_previous`);
13. timeout no POST da proteção: recriada; a antiga aparece → cancelada + `FLEXSCAN_ORFA`;
14. webhook forjado (conta errada) → descartado + crítico;
15. fora do horário: 400 no cancel não abre o disjuntor;
16. dois destinos: limites somados; PnT sem sessão não recusa entrada Flexscan;
17. habilitação sem `flexscan` → `DESTINO_NAO_HABILITADO`;
18. `ZERAR` com operações nos dois destinos.

Os roteiros FIX continuam passando sem mudança.

### 10.3 Próxima sondagem (curta, junto com o pregão da F3)

| # | Medir |
|---|---|
| SIM-1 | ordem nova recusada (símbolo vencido; stop do lado errado no POST) e uma limite fora do túnel em negociação: retrato e consulta com o preço ajustado (`Text`, `PriceLimit`, `price`) |
| SIM-2 | fill parcial (ordem de 2+ contratos com limite no meio do book): consulta e retrato |
| SIM-3 | stop disparado sem execução (limite colado no gatilho num movimento rápido): `LegWorkingIndicator` Y com `LegExecQty 0`? |
| SIM-4 | `clxRejReason` depois de um PUT aceito seguinte |
| SIM-5 | 401 com token vencido/inválido; `exp` do JWT |
| SIM-6 | 400 fora do horário (POST, PUT, DELETE) |

---

## 11. Perguntas à Flexscan (abertas)

| # | Pergunta | Padrão seguro |
|---|---|---|
| FX-1 | Limite de requisições? 429 com `Retry-After`? | 10/s do nosso lado |
| FX-2 | Lista dos códigos de erro 3406–8000 e das razões de `clxRejReason` | texto bruto no `58` |
| FX-3 | Duração do token; um login novo derruba o anterior? | `exp` − 30 min, troca atômica |
| FX-5 | Limite de edições (PUT) por ordem? | reserva da v1.13 com 100 |
| FX-6 | Ordens de outros usuários da API (o GX) aparecem para o nosso nas mesmas contas? | filtro por `name` `KM-` e `UserName` |
| FX-7 | `177.71.196.212` é fixo? Há outros IPs de saída do webhook? Há assinatura prevista? | segredo + `CONFIRMAR_FILL=1` |
| FX-8 | O webhook reenvia em erro/timeout? Quantas vezes? Tempo máximo de resposta? | sem reenvio (a consulta cobre) |
| FX-9 | Qual é o "referential price" da razão 4 (último? médio do book?) | tratar a recusa |
| FX-10 | O ajuste ao túnel vale também para o `stopPrice` e o limite da stop-limite? Fica no `Text`? Vale para o PUT? | só o preço limite é ajustado; gatilho mudado = crítico |

Respondidas pela sondagem: corpo do webhook (FX-9 da manhã), stop na B3 e gatilho ultrapassado
(FX-4), `clordid` das execuções, formato de conta e símbolo, prefixo do proxy.

---

## 12. Mínimo útil e estimativa

### 12.1 Ritmo real do repositório (git)

| Entrega | Tempo |
|---|---|
| Motor em Go + camada PnT + PnT falso + 32 roteiros (10/10) | 08:54 → 14:05 |
| v1.4 → v1.10 (7 versões: ajuste, histórico, admin, métricas, carga, filas, lote) (15/10) | um dia |
| v1.11 saída por replace + v1.12 + roteiros (19/10) | ~2 h |
| v1.14 FONTE_DEGRADADA + monitor de disco (21/10) | ~4 h, instalada às 08:02 do dia seguinte |
| Suíte completa | ~11 min (`var/suiteD-20261019-130419.log`) |

O que limita o calendário não é escrever o código: é **pregão** (apisim só responde no horário),
**suíte em worktree limpo** e a **instalação pelo usuário**. A estimativa da manhã (24–33 dias)
estava num ritmo que este repositório não tem; fica substituída por esta.

### 12.2 Mínimo útil (M1)

Um uid da kalman-hml no destino 88, contra o apisim:

- motor por destino, habilitação com `destinos`, conta e trava cruzada do 88;
- ponte: auth, POST/PUT/DELETE, consulta ativa, "não sei" com busca pelo `name` (sem a vigia de
  órfã), normalização das duas fontes, fusão, ERs sintéticos;
- webhook: listener na 9098, segredo, validação 1–7, log mascarado; `CONFIRMAR_FILL=1`;
- consulta periódica das vivas; conciliação no boot por `GET /orders/filter`;
- emergência ordem a ordem; alertas `FLEXSCAN_LOGIN`, `…_WEBHOOK_INCOERENTE`,
  `…_ESTADO_INESPERADO`, `…_WEBHOOK_ATRASADO`;
- falso + roteiros 1–12.

**Fora do M1:** vigia de órfã (roteiro 13), `/executions` e `/positions` na conciliação, CLI do
webhook (o cadastro manual de hoje fica), rotação de segredo, `GET /flexscan/ordens` no admin,
roteiros 13–18, `FLEXSCAN_POSICAO_NO_FIM`.

### 12.3 Estimativa

| Passo | Tempo |
|---|---|
| M1: código + falso + roteiros 1–12 + suíte verde em worktree | 1 dia de sessões (6–9 h) |
| Pregão no apisim com a kalman-hml (uid no 88, `FLEXSCAN_*` no `service.env`, receptor provisório desligado) + SIM-1…6 | 1 pregão |
| Ajustes do pregão + instalação na .53 (usuário) | ½ dia |
| **M1 no ar na HML** | **2–3 dias úteis** |
| Completo (fora do M1, roteiros 13–18) | + ½–1 dia de sessões |

Faixa alta se o fill parcial ou a recusa de ordem nova vierem num formato que a fusão não cobre
(volta ao falso e aos roteiros), ou se o pregão do apisim tiver indisponibilidade. A F1 do motor
(destino) é inócua para o PnT e pode ser instalada antes, separada.

---

## 14. Mínimo útil implementado (23/10, branch `flexscan-m1`)

Decisões do usuário de 23/10, depois da aprovação:

1. **Quem decide no teste é a principal** (mercado de hoje; o simulador da Flexscan roda com o mercado
   de hoje), com um **uid de teste próprio** (não o 1450), para os limites por uid não somarem o teste
   do PnT (kalman-hml, módulo KHML) com o da Flexscan (principal, módulo KALMAN).
2. **Fonte de preço por conector de execução**, não por corretora (a mesma corretora pode ser testada
   pelos dois caminhos): `PRECOS_URL` padrão + `PRECOS_URL_<CONECTOR>` opcional (`PNT`, `FLEXSCAN`),
   só onde difere. Na .53: PnT → 9096 (HML D-1), Flexscan → `http://172.16.110.53:9090` (D0). Em
   produção, a mesma. Idem `PRECOS_SAUDE_URL_<CONECTOR>`.
3. Nome: a habilitação guarda **conectores** (`PNT`, `FLEXSCAN`), não "destinos" (§2 item 1).

O que ficou diferente do desenho (§3–§5), e por quê:

- **"Não sei" no POST pelo próprio motor:** a ponte não procura sozinha; o motor marca
  `SEM_CONFIRMACAO` em `PRAZO_CONFIRMACAO_S` e pede o mass status, e a ponte responde ao AF com as ordens
  KM da conta (`GET /orders/filter`), consultando até 3 vezes se faltar uma ordem "não sei". Não achada:
  150=8 antes das respostas (entrada encerra; proteção recriada com outro name, `PROTECAO_REJEITADA`);
  achada: o 150=I dela, com o executado. Conta sem ordens: 35=j 380=101, como o PnT.
- **Edição (PUT):** aceita = consulta com `orderStatus 5` e `transactTime` posterior ao PUT, ou retrato
  "Editada" com o gatilho pedido; recusada = retrato "Edicao rejeitada" ou `clxRejReason` novo; sem
  sinal até `FLEXSCAN_CONFIRMA_S` → 35=9 (o motor mantém o stop anterior). O mesmo para o DELETE.
- **Preço ajustado ao túnel:** a ponte grava o efetivo no evento `FLX-PRECO`; o motor guarda o pedido
  (`kalmanordem.preco`), como antes.
- **Disjuntor de bolsa e saída por replace:** só no PnT (`SAIDA_POR_REPLACE` não vale para o 88).
- **Limites globais:** somam os dois conectores; o resultado do dia avalia cada posição pelo preço da
  fonte do seu conector.
- **`/saude`:** `posicao_por_conector` e `conector` nas ordens vivas; o desligamento do pnt-md-hml
  (14:35) só olha o PnT.
- **`FLEXSCAN_WEBHOOK_ATRASADO`** saiu não crítico (aviso; não repete).
- **Fora do mínimo útil** (como na §12.2): CLI do webhook e `FLEXSCAN_WEBHOOK_AUSENTE`; vigia de órfã
  (a ordem "não achada" que aparece depois); `/executions` e `/positions` na conciliação; 503 com a fila
  cheia (a fila da ponte não tem limite); rotação do segredo; `GET /flexscan/ordens` no admin;
  `FLEXSCAN_POSICAO_NO_FIM`; roteiros 13–18 da §10.2 (os 16–17 entraram no 104).

Roteiros: 95 (entrada → fill → stop-limite → saída), 96 (parciais pelo webhook, repetido e fora de
ordem, PUT de quantidade, timeout da entrada, stop pela B3), 97 (TRSP aceito e recusado, túnel,
gatilho mexido), 98 (corrida stop × DELETE), 99 (POST "não sei"), 100 (401, login recusado, volta),
101 (webhook mudo), 102 (receptor: 404, corpo inválido, retrato forjado), 103 (restart, KM
desconhecida), 104 (dois conectores, limites somados, `DESTINO_NAO_HABILITADO`). Unidade:
`internal/flexscan` (leitores sobre os arquivos reais da sondagem, máscara do segredo/senha/token,
nada de `/algos/commands`, `DELETE /webhooks` ou `remove_previous`).
