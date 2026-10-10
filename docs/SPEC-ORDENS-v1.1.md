# Spec de Implementação — `kalman-ordens` — v1.1

Revisão da [SPEC-ORDENS-v1.md](SPEC-ORDENS-v1.md) (mantida intacta ao lado). Este documento
lista **só o que mudou** e, para cada mudança, a parte da v1 afetada e o roteiro que a cobre.
Onde a v1.1 não diz nada, vale a v1.

Os roteiros (`cenarios/*.fix`, formato em `cenarios/FORMATO.md`) são a especificação executável:
cada decisão abaixo está afirmada em pelo menos um deles.

Data: 2026-10-10. Decisões confirmadas pelo responsável em três rodadas (stack e envio;
roteiros e preço; fill perdido por 35=3 e saída com ticker divergente).

---

## 1. Stack e estrutura

| # | Mudança | Parte da v1 | Roteiro |
|---|---|---|---|
| S1 | **Go 1.2x + QuickFIX/Go** no lugar de Java 21 + QuickFIX/J, pela homogeneidade com o marketdata na .51. O esboço em Java fica só no histórico do git (primeiro commit). | 1 | — |
| S2 | **Camada PnT separada do motor do Kalman.** A camada PnT (sessão FIX, dicionário, montagem e leitura de mensagens, mass status, store) não importa nada do motor e não conhece Kalman, uid nem operacao_id: só ClOrdID, 37, conta e mensagens. Será reaproveitada por um futuro serviço do pair trade, em processo próprio e com usuário FIX próprio. O PnT falso segue a mesma divisão: a sessão FIX dele fica na camada PnT; o executor de roteiros (HTTP, eventos, saúde) fica do lado do Kalman. | 1, 11.1 | todos |

Equivalências verificadas no QuickFIX/Go v0.9.12 (teste com o dicionário e mensagens reais):
`ValidateUserDefinedFields`, `AllowUnknownMsgFields`, `ValidateFieldsOutOfOrder`,
`RejectInvalidMessage`, `CheckLatency`, `ResetOnLogon/Logout/Disconnect` existem com os mesmos
nomes; o FileStore grava a data de criação e a sessão só zera quando ela cai fora do período
(`StartTime`/`EndTime`), como no QuickFIX/J; `ToApp` devolvendo `ErrDoNotSend` suprime o reenvio
de ordem velha no ResendRequest (vira GapFill).

## 2. Sessão FIX e dicionário

| # | Mudança | Parte da v1 | Roteiro |
|---|---|---|---|
| F1 | **Dicionário:** cópia versionada `pnt/dicionario/dsa_FIX44.xml` com a raiz `type="FIX"` (exigida pelo QuickFIX/Go); o original fica intacto ao lado (`dsa_FIX44.original.xml`). | 7.1 | todos |
| F2 | **`RejectInvalidMessage=N` na v1.** O dicionário de 2020 não descreve os grupos de pernas que o PnT ecoa (falha igual no QuickFIX/J e no QuickFIX/Go); as ordens multileg do PT que chegam no mass status por conta seriam respondidas com 35=3. Mensagens inválidas vão para o log e são descartadas pelo filtro de prefixo `KM-`. Nenhum 35=3 volta ao PnT. | 7.1, 7.2 | 20, 12 |
| F3 | **Ordens do serviço passam no dicionário.** O PnT falso valida tudo o que o serviço envia (`AllowUnknownMsgFields=N`, `ValidateUserDefinedFields=N`); falha = cenário falha. | 7.1 | todos (01 é o caso de referência) |
| F4 | **Cancel (35=F) só com tags da OrderCancelRequest do dicionário:** 1, 11, 37, 38, 41, 54, 55, 60, 207. Os exemplos do RoE 2025 reenviam a ordem inteira, mas 21/40/44/59/847 não existem nessa mensagem no dicionário. NewOrderSingle e Replace continuam como na v1 (20828/20829 passam por serem ≥ 5000). | 3.1, 3.5 | 01, 06, 07 |
| F5 | **Envio:** o estado de logon vem dos callbacks `OnLogon`/`OnLogout` e é verificado antes de enviar. No QuickFIX/Go, `SendToTarget` **não informa** se a mensagem foi ao fio (com a sessão caída ela é gravada no store e descartada da fila, sem erro). A garantia passa a ser a confirmação do PnT (F6). | 7.1 | 19 |
| F6 | **SEM_CONFIRMACAO:** ordem em `ENVIANDO` sem nenhuma resposta do PnT em `PRAZO_CONFIRMACAO_S` (inicial 3 s) → estado `SEM_CONFIRMACAO`, alerta crítico, entradas bloqueadas, mass status para reconciliar. Enquanto `SEM_CONFIRMACAO`, o timeout da entrada não manda cancel. Achada viva → volta ao fluxo normal (entrada já além do timeout é cancelada na hora). Ausente → `DESCONHECIDA`, `ENTRADA_ENCERRADA motivo=DESCONHECIDA`, DIVERGENTE + BLOQUEAR. 39=A conta como resposta. | 4, 7.1, 7.2 | 19, 17 |
| F7 | **Posição pelo maior CumQty (14) por ordem, com chave no 37** (não no ClOrdID): um replace da 1014 muda o ClOrdID e não pode zerar a conta. Fill repetido (reconexão, ResendRequest, mass status) não muda nada. | 8 | 02, 09 |
| F8 | **A biblioteca não descarta fill.** Mesmo com `RejectInvalidMessage=N`, o QuickFIX/Go rejeita com 35=3 (e não entrega à aplicação) mensagem sem campo obrigatório. Na cópia `pnt/dicionario/dsa_FIX44.xml`, o corpo de ExecutionReport (8) e OrderCancelReject (9) passa a ter **todo campo `required="N"`** (cabeçalho, trailer e MsgType intactos; lista em `pnt/dicionario/LEIA.md`). O motor valida o conteúdo e decide. A sessão do serviço também usa `ValidateFieldsHaveValues=N` (campo vazio não vira 35=3). O que ainda gera 35=3: tipo de mensagem desconhecido, cabeçalho inválido. Prova: `pnt/dicionario/dicionario_test.go`. | 7.1 | 21 |
| F9 | **Todo 35=3 enviado pelo serviço** → alerta crítico `SESSAO_REJEITOU_MENSAGEM` + mass status para reconciliar (a mensagem descartada pode ter sido um fill). | 7.1, 9 | 22 |

## 3. Decisões dos roteiros (Parte A)

| # | Decisão | Status | Parte da v1 | Roteiro |
|---|---|---|---|---|
| A1 | Cancel só com tags do dicionário (= F4). | confirmada | 3.1 | 01 |
| A2 | Validação estrita das mensagens do serviço em todos os roteiros (= F3). | confirmada | 11.1 | todos |
| A3 | **Fill depois do cancelamento.** `ENTRADA_ENCERRADA` fica retido por `ESPERA_ENCERRAMENTO_S` (inicial 5 s) depois do 39=4 (e do 39=C). Fill dentro da espera = entrada normal, sem `ENTRADA_ENCERRADA` intermediário; o único sai no fim da espera com a quantidade executada. Fill depois da espera = `ENTRADA_TARDIA`: o serviço cria a 1014 e zera na hora (Parte 3.5), com alerta; o gestor recebe `ENTRADA_EXECUTADA tardio=true` e `SAIDA_EXECUTADA origem=ENTRADA_TARDIA` só para registro e **nunca reabre a operação**. Rejeição (39=8) e execução total (39=2) encerram sem espera. | **ajustada** | 3.3, 4, 2.2, 2.3 | 04, 04b (efeito no tempo: 02, 13, 17, 19) |
| A4 | **Origens da saída:** `GESTOR`, `STOP_SERVIDOR`, `STOP_FURADO`, `PARADA`, `ZERAGEM`, `PROTECAO_REJEITADA`, `ENTRADA_TARDIA`. O gestor trata tudo o que não é `GESTOR` como operação fechada pelo serviço, com esse motivo. | confirmada | 2.2, 2.3 | 01, 05, 06, 07, 04 |
| A5 | **Repreço depois de esgotar:** passados os `MAX_REPRECO_SAIDA` repreços, alerta crítico e continua reapregoando a cada `TIMEOUT_SAIDA_S` no afastamento máximo (`COLAR_SAIDA_PTS × (MAX+1)`, 200 pts) sobre o mercado do momento, sem aumentar mais. | confirmada | 3.5 | 07 |
| A6 | **SEM_CONFIRMACAO** (= F6). Esclarecimento: ordem achada viva na conciliação volta ao fluxo normal; entrada viva já além do timeout é cancelada na hora. | confirmada, com esclarecimento | 4, 7.2 | 19 |
| A7 | **Precedência das recusas:** PARADA, DIVERGENTE, SEM_SESSAO, SEM_CONFIRMACAO, CONCILIANDO e SIMBOLO_DIVERGENTE vêm antes dos limites e não contam para "3 recusas por limite em 1 min". Ordem completa em `docs/ALERTAS.md`. | confirmada | 5 | 13, 18, 19, 16f |
| A8 | **Saída com o lado que aumentaria a posição** → recusa `LADO_INCOMPATIVEL` (reduce-only) **e alerta** `LADO_INCOMPATIVEL` (indica bug no gestor). O `lado` da SAIDA é o lado da ordem de saída. | **ajustada** (alerta) | 2.1, 3.5 | 14 |
| A9 | **Ordem sumida no restart:** vira `DESCONHECIDA` → DIVERGENTE + BLOQUEAR (7.2.5); a nova 1014 sai mesmo assim (7.2.4). Exige retomada manual. | confirmada | 7.2 | 11 |
| A10 | **Alertas com código fixo**, catálogo único em [`docs/ALERTAS.md`](ALERTAS.md) (código, significado, crítico ou não; também os códigos de recusa). Telegram e evento `ALERTA` levam `codigo` e `texto`; roteiros conferem o código. | **ajustada** (catálogo) | 9, 2.2 | todos |

## 4. Preço (Parte B)

Contrato real do `/precos/atual` em [`docs/referencia/precos-atual.md`](referencia/precos-atual.md)
(trecho da fonte do marketdata, comportamento confirmado pelos testes dele, fixtures em
`cenarios/referencia/precos-atual-*.json`). URL base em `PRECOS_URL` (na .51:
`http://172.16.110.51:9090`). Nos roteiros, a fonte é a falsa, controlada pelo verbo `preco`.

| # | Regra | Parte da v1 | Roteiro |
|---|---|---|---|
| B1 | **Entrada** exige `preco`, `bid` e `ask` presentes, `status84 == 0`, `idade_seg <= 3`, `bid < ask` e \|meio − preco\| ≤ 150. Falha → recusa com o código do catálogo: `SEM_PRECO` (`{}`, 404, 400, erro, campo ausente), `LEILAO`, `PRECO_VELHO`, `BOOK_INVALIDO`, `BOOK_DESCOLADO`. O "último negócio" da v1 é o campo `preco`. | 2.1, 5 | 16, 16b, 16c, 16d, 16e |
| B2 | **Saída** usa o que houver. Sem bid/ask, o limite é calculado sobre o `preco` com `COLAR_SAIDA_PTS`. Preço velho ou sem bid/ask → alerta `SAIDA_PRECO_DEGRADADO`. Sem nenhum preço → alerta crítico `SAIDA_SEM_PRECO` e nova tentativa a cada `TIMEOUT_SAIDA_S`. | 3.5 | 16, 16b, 16c, 16d |
| B3 | **`ticker != ORDENS_SIMBOLO`** → entradas bloqueadas enquanto durar + alerta crítico `SIMBOLO_DIVERGENTE` (não é parada; volta sozinho quando o ticker casar). O controle de vencimento por calendário (`ORDENS_VENCIMENTO`) fica **só como aviso antecipado** (`VENCIMENTO`); a v1 "recusar entradas no dia do vencimento" sai. | 3.2 | 16f |
| B4 | Preços do JSON são float: converter para pontos inteiros e arredondar ao tick de 5 pela regra 3.2. | 3.2 | 16e (e todos os preços de ordem) |
| B5.a | **Abertura com ticker divergente** → alerta crítico, sem entradas (= B3). | 3.2 | 16f |
| B5.b | **Divergência detectada com posição aberta** → alerta crítico `SIMBOLO_DIVERGENTE_COM_POSICAO` **na detecção**; a 1014 continua viva (o PnT dispara pelo mercado dele, do contrato certo). | 3.2, 3.4 | 16g |
| B5.c | **Saída pedida com ticker divergente** → Parte 3.5 normal até o passo 3; a ordem de saída usa como limite o **último `preco` conhecido do símbolo certo**, com o **colar máximo** (`COLAR_SAIDA_PTS × (MAX_REPRECO_SAIDA + 1)` = 200 pts) já na 1ª tentativa, **sem repreço**. Não executou em `TIMEOUT_SAIDA_S` → alerta crítico `SAIDA_DIVERGENTE_NAO_EXECUTOU`, não repete, decisão humana. | 3.5 | 16h (executa), 16i (não executa) |
| B5.d | **Último preço conhecido do símbolo certo com mais de 60 s** → a saída não é enviada (recusa `SIMBOLO_DIVERGENTE`), nada é cancelado, a 1014 fica; alerta crítico `SAIDA_BLOQUEADA_PRECO_ANTIGO`. | 3.5 | 16j |

"Último preço conhecido do símbolo certo" = o `preco` (e seu `ts`) da última leitura com
`ticker == ORDENS_SIMBOLO`. O motor guarda esse par a cada leitura válida.

## 5. Configuração nova

| Chave | Inicial | Uso |
|---|---|---|
| `PRAZO_CONFIRMACAO_S` | 3 | F6 |
| `ESPERA_ENCERRAMENTO_S` | 5 | A3 |
| `PRECOS_URL` | `http://172.16.110.51:9090` | B |
| `IDADE_MAX_PRECO_DIVERGENTE_S` | 60 | B5.d |

Na sessão FIX do serviço: `RejectInvalidMessage=N`, `AllowUnknownMsgFields=Y`,
`ValidateUserDefinedFields=N`, `ValidateFieldsOutOfOrder=N`, `ValidateFieldsHaveValues=N`,
`CheckLatency=N`, `ResetOnLogon/Logout/Disconnect=N` (F2, F8).

## 6. Contrato com o gestor — o que muda (Parte 2.2/2.3)

- `ALERTA` passa a ter `codigo` (catálogo) e `texto`.
- `ENTRADA_EXECUTADA` ganha `tardio` (bool). Com `tardio=true` o gestor só registra.
- `SAIDA_EXECUTADA.origem` e `ZERADA.origem` usam a lista de A4.
- `ENTRADA_ENCERRADA` pode chegar até `ESPERA_ENCERRAMENTO_S` depois do cancelamento, e
  `motivo` pode ser `EXECUTADA`, `CANCELADA`, `EXPIRADA`, `REJEITADA` ou `DESCONHECIDA`.
- Recusas novas: `LADO_INCOMPATIVEL`, `SEM_CONFIRMACAO`, `CONCILIANDO`, `SIMBOLO_DIVERGENTE`,
  `SEM_PRECO`, `BOOK_INVALIDO`, `RESULTADO_DESCONHECIDO` (HTTP 503: consultar `GET /ordens/{id}`).

## 7. Critérios de fase acrescentados

| Fase | Critério novo |
|---|---|
| F0 | Além dos roteiros verdes: a suíte inteira roda com `go test -race ./...` sem achado, numa máquina com cgo (na .51 ou equivalente). A máquina de desenvolvimento atual não tem cgo. Antes da HML. |

## 8. Pendências abertas

1. **Posição sem proteção em B5.c quando a saída não executa (16i).** O passo 2 da Parte 3.5 já
   cancelou a 1014; "não repete, decisão humana" deixa a posição sem proteção e com uma ordem
   de saída parada no livro até alguém agir. Opções: (a) manter como está; (b) no timeout,
   cancelar a saída e recriar a 1014 sobre o preço médio da entrada (o PnT dispara pelo
   mercado dele, que é o certo). O roteiro 16i segue (a), como decidido.
2. **Verificações que usam preço durante a divergência.** Perda do dia (Parte 5) e stop furado
   (3.6) leem o `preco`; com o ticker errado leriam o contrato errado. Adotado até decisão: as
   duas ficam suspensas enquanto durar a divergência (a 1014 continua protegendo), e o alerta
   de B5.b já está ativo. Sem roteiro próprio.
3. **[PnT-13] CumQty no mass status.** O mass status de HML 2025 trouxe 39=2 com 14=0. Se for
   assim em produção, o fill não é recuperável pelo 150=I e o serviço marca DIVERGENTE. O
   cenário 08 supõe 14 correto.
4. **Valores de `status84`.** A regra é "≠ 0 = não negocia"; o 2 dos roteiros é ilustrativo.
5. **Gestor (Python):** precisa tratar `tardio`, as origens de A4, o atraso do
   `ENTRADA_ENCERRADA` e a recusa `SIMBOLO_DIVERGENTE` na SAIDA (B5.d: a operação continua aberta,
   protegida pela 1014).

## 9. Cobertura dos roteiros

| Roteiro | Cobre |
|---|---|
| 01 | fluxo completo; F3, F4; origem GESTOR |
| 02 | fill parcial, replace enfileirado, F7, espera de encerramento |
| 03 | rejeição por margem |
| 04 / 04b | A3 (entrada tardia / fill dentro da espera) |
| 05 / 06 | STOP_SERVIDOR; cancel da 1014 rejeitado |
| 07 | stop furado; A5 |
| 08 / 09 | queda + mass status; ResendRequest e fill uma vez (F7) |
| 10 / 11 | restart com 1014 viva; restart sem 1014 (A9) |
| 12 / 20 | ordens do PT no mass status, single-leg e multileg (F2) |
| 13 | laço de reentrada; A7 |
| 14 | reduce-only; A8 |
| 15 | perda do dia |
| 16, 16b–16f | Parte B |
| 16g–16j | B5 (ticker divergente com posição e saída) |
| 17 | 39=A demorado |
| 18 | mass status com 380=4; CONCILIANDO |
| 19 | F6 / A6 |
| 21 | F8 (fill incompleto aplicado, sem 35=3) |
| 22 | F9 (35=3 enviado → alerta + mass status) |
