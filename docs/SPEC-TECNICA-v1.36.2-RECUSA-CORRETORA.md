# SPEC técnica v1.36.2: a recusa da corretora na entrada (margem de day trade)

17/11/2026. Branch `v1.36.2` (worktree `../kalman-ordens-v1362`), a partir do `8a8fff8` da
`v1.36.1`. **Não codificada.** Para o George aprovar antes. A v1.36.1 e a suíte das 18:31 não são
tocadas.

## 1. O que o teste de 17/11 mostrou

Às 10:55, no REAL, com a margem de day trade da 4582351 retirada no OMS `BTGDAYTRADE`, houve três
entradas do Kalman (234777, 234778 e 234779). As três seguiram o mesmo fio:

| t | do PnT | 58=Text |
|---|---|---|
| 0 | 35=8, 150=A, 39=A | `NEW DSA Order request sent to Robot` |
| +0,3 s | 35=8, 150=0, 39=0 (aceita pelo robô) | — |
| +9,6 a +10,1 s | 35=8, **150=4, 39=4** | `This strategy was changed to Error state, because offer RealOrder Id: 178312978 is Rejected.` |

- **Sem 39=8 e sem 103.** A palavra "margem" não aparece.
- **Hoje o serviço trata isso como um cancelamento comum.**
  - Encerra a entrada com `ENTRADA_ENCERRADA{motivo:"CANCELADA"}`, 8 s depois do cancelamento
    (`ESPERA_ENCERRAMENTO_S`).
  - O 58 fica só na mensagem crua, sem ir para o `motivo_rejeicao`.
  - A tela do usuário recebe "SEM_EXECUCAO / ENCERRADA_SEM_ENTRADA".
- **A 234779 deu um alarme falso.** O nosso cancelamento por fim do prazo da entrada saiu aos 10 s
  (11:02:08.754), antes do erro do robô. O PnT respondeu com um 35=9:
  `[1] Order cannot be canceled. It has ended already (VO status: Cancelled)`. O 39=4 com o texto do
  erro chegou só 137 ms depois. O 35=9 virou um `RECUSA_PNT` ("cancel rejeitado ..."), que foi ao
  Telegram e à tela.

## 2. As decisões (George, 17/11)

| # | decisão |
|---|---|
| D1 | Um 39=4 de entrada com o texto "changed to Error state … is Rejected" termina a entrada como **`REJEITADA_CORRETORA`**. O 58 vai para o `motivo_rejeicao`, e o `ENTRADA_ENCERRADA` leva o `motivo` e o `texto` |
| D2 | O nosso cancelamento recusado com "ended already (VO status: Cancelled)" é um encerramento esperado e **não gera `RECUSA_PNT`** |
| D3 | O contrato com o CC do Kalman: o motivo `REJEITADA_CORRETORA` e o `texto` no `ENTRADA_ENCERRADA` (§4) |
| D4 | **Sem alerta no Telegram** para a recusa da corretora, nem uma vez por dia: margem faltando é problema da conta do usuário, não defeito do sistema. **O admin vê na ficha** (§4.2) |
| D5 | A regra de "2 recusas seguidas" é do Kalman, que desliga o "Enviar ordens" do usuário no dia. Do lado do kalman-ordens, só o evento com o motivo certo |

**Três pontos que esta SPEC propõe e que precisam do ok do George:**
- **P1, a corrida do prazo (§3.1):** a D1 fala de "um 39=4 que não pedimos". Na 234779, porém,
  **nós tínhamos pedido** o cancelamento (o prazo de 10 s) quando o erro do robô chegou. Proponho que
  **o texto decida, com ou sem cancelamento nosso pendente.** Sem isso, a mesma falta de margem sairia
  ora como `REJEITADA_CORRETORA`, ora como `CANCELADA`, conforme quem chegasse primeiro aos 10 s.
- **P2, o 39=8 continua avisando:** o `RECUSA_PNT` do 39=8 de verdade fica como está. Um
  `[DSA-3] Robot not found` (13/11) é defeito de configuração nosso, e não deve ficar calado. A D4
  vale só para o `REJEITADA_CORRETORA`.
- **P3, a rede de segurança da D2 (§3.2):** se o relatório final não chegar em
  `ESPERA_FILL_POS_RECUSA_S` (3 s), o `RECUSA_PNT` volta a sair como hoje, com um mass status.

## 3. O que muda no kalman-ordens

### 3.1 D1: o 39=4 com o erro do robô (`internal/motor/recebe.go`, caso `150=4`)

Uma regra nova reconhece o texto:
```go
// recusa da corretora por dentro do robô DSA (17/11, margem de day trade): o PnT cancela a
// estratégia, sem 39=8 nem 103
var reRecusaCorretora = regexp.MustCompile(`(?i)changed to Error state.*is Rejected`)
```

**No `case "4"`:** quando a ordem é a **entrada** e o 58 casa com a regra (com ou sem cancelamento
nosso pendente, pela P1):
- `o.MotivoRejeicao` recebe o 58, aparado e cortado em 500. Ele é gravado na coluna `motivo_rejeicao`
  da `kalmanordem`, que já existe, e aparece em `GET /historico/ordens` e no histórico da operação;
- o estado da ordem continua `CANCELADA`, porque é o que o PnT diz. Muda só o motivo do encerramento;
- `m.ordemEncerrada(op, o, "REJEITADA_CORRETORA")`, com o texto guardado na operação
  (`op.textoEncerramento`, novo campo em memória), em vez de `"CANCELADA"`.

**O `encerrarEntrada`** continua esperando `ESPERA_ENCERRAMENTO_S` depois do 39=4, como todo
cancelamento (regra A3 da v1.1: um fill que chegue tarde ainda conta).
- O `ENTRADA_ENCERRADA` sai com `motivo: "REJEITADA_CORRETORA"` e `texto: <o 58>`.
- **Com execução parcial antes do erro** (`qtd_final > 0`), o evento sai igual. A posição segue o
  fluxo de hoje: o Kalman abre com a quantidade executada.
- **Se o serviço reiniciar durante a espera,** o motivo em memória se perde e a conciliação encerra
  como hoje. É aceito: são 8 s, e o `motivo_rejeicao` já está gravado no banco.

**Sem alerta (D4).** Não sai `RECUSA_PNT` nem `RECUSADA`, e nada vai ao Telegram. A recusa não conta
para as 50 recusas da Parada; isso já é assim, porque o contador só soma as recusas do próprio
serviço antes do envio.

**Só a entrada.** Um 39=4 com esse texto numa 1014 ou numa saída segue o caminho de hoje: recriar
a proteção ou tentar a saída de novo. Lá, é defeito que exige atenção.

### 3.2 D2: o cancelamento recusado porque a ordem já terminou (`cancelRejeitado`, ramo `default`)

**Hoje:** um 35=9 de cancelamento da entrada vira sempre um `RECUSA_PNT` "cancel rejeitado (...)".

**Novo:** quando é o nosso cancelamento da **entrada** (`eraCancel`) e o 35=9 diz que a ordem já
terminou, sai só um INFO no journal ("cancel da entrada recusado: a ordem já terminou na corretora
(…); aguardando o relatório final") e o serviço espera o 35=8 final.
- **"Já terminou"** é o `39` do 35=9 em `4`, `2`, `8` ou `C`, **ou** o `102=1` (*too late to cancel*).
- **A rede de segurança (P3):** a ordem guarda `o.recusaTerminalEm`. No tick, se ela continuar não
  final depois de `ESPERA_FILL_POS_RECUSA_S` (3 s, a mesma chave da saída), sai o `RECUSA_PNT` de hoje
  ("cancel rejeitado (…), sem o relatório final em 3 s") e é pedido um mass status da conta.
- **Enquanto espera, o prazo da entrada não manda outro cancel** (E2, achado pelo roteiro 137: sem
  isso, o cancel saía de novo 1 s depois, contra uma ordem que o PnT já deu por terminada). Disparada
  a rede, volta o de antes: o cancel de novo pelo prazo, junto com o mass status.
- **Fora disso, nada muda:** um 35=9 com a ordem viva, de outro papel ou de outra ação segue o
  caminho atual.

### 3.3 O que não muda
- O 39=8 da entrada (`ordemRejeitada`): `RECUSADA`, `RECUSA_PNT` e `ENTRADA_ENCERRADA{motivo:"REJEITADA"}`.
  Pela P2, ele **ganha o `texto`** no `ENTRADA_ENCERRADA`, para o Kalman tratar os dois casos da
  mesma forma.
- O prazo da entrada (10 s), a espera de 8 s e os limites.
- A Flexscan: o texto é do robô DSA do PnT.

## 4. O contrato (D3; vale para o CC do Kalman)

### 4.1 O evento `ENTRADA_ENCERRADA`

```json
{"tipo": "ENTRADA_ENCERRADA", "dados": {"ambiente": "REAL", "modulo": "KALMAN", "operacao_id": 234777, "uid": 1450,
  "qtd_final": 0, "preco_medio": 0, "motivo": "REJEITADA_CORRETORA",
  "texto": "This strategy was changed to Error state, because offer RealOrder Id: 178312978 is Rejected."}}
```

| `motivo` | quando | `texto` |
|---|---|---|
| `EXECUTADA` | executada (inteira ou com o resto cancelado depois) | `null` |
| `CANCELADA` | cancelada (pelo prazo, pelo admin ou pelo PnT, sem o texto do §3.1) | `null` |
| `EXPIRADA` / `EXPIRADA_NA_FILA` | como hoje | `null` |
| `REJEITADA` | 39=8 do PnT (com `RECUSADA` e `RECUSA_PNT` antes) | **novo:** o 58, com o 103 entre colchetes quando vier |
| **`REJEITADA_CORRETORA`** (novo) | 39=4 com o erro do robô (§3.1). Na BTG, a causa conhecida é a **margem de day trade no OMS `BTGDAYTRADE`** | o 58, como veio |

- **O campo `texto` é novo** e vem `null` nos outros motivos. Quem lê o evento hoje ignora campos
  desconhecidos.
- **O motivo novo não quebra a v1.36 do Kalman.** Hoje o `ordens_reais.py:2505` usa o `motivo` como
  texto do aviso `SEM_EXECUCAO`, então a tela mostraria "REJEITADA_CORRETORA" até o CC do Kalman
  tratar o caso.
- **O que o CC do Kalman faz com isso** (fora deste repositório):
  - na tela do usuário: "A corretora recusou a ordem: confira a margem de day trade";
  - a regra de "2 recusas seguidas" (D5), contando `REJEITADA_CORRETORA` seguidas do mesmo usuário
    no dia.

### 4.2 A ficha do admin (`GET /usuarios/{uid}/real`, SPEC técnica v1.36 §5.1)

Um campo novo, com as recusas da corretora do dia, para o admin ver sem o Telegram (D4):
```json
"recusas_corretora_hoje": {"quantidade": 3,
  "ultima": {"em": "2026-11-17T11:02:08.919-03:00", "operacao_id": 234779, "clordid": "KM-E-234779-1",
             "motivo": "REJEITADA_CORRETORA",
             "texto": "This strategy was changed to Error state, because offer RealOrder Id: 178314346 is Rejected."}}
```
- **Sem recusa no dia:** `{"quantidade": 0, "ultima": null}`.
- **O que conta:** as entradas REAL do uid no dia com `motivo_rejeicao`, tanto o `REJEITADA` (39=8)
  quanto o `REJEITADA_CORRETORA`. A fonte é a mesma consulta do `GET /historico/ordens`, papel
  ENTRADA, e por isso a contagem sobrevive a restart.
- **Na lista** `GET /usuarios/real` entra só a `quantidade`, em `recusas_corretora_hoje`.
- **A ficha da v1.36 ignora campos novos;** a tela mostra o campo quando o CC do Kalman o puser.

### 4.3 Onde o contrato fica escrito
- **`docs/SPEC-ORDENS-v1.28.md`:** a tabela de motivos do `ENTRADA_ENCERRADA` e o campo `texto`.
- **`SPEC-TECNICA-v1.36-CONTA-LIVRE.md` §5.1/§5.2:** o `recusas_corretora_hoje`.
- **`docs/ALERTAS.md`:** a nota de que a recusa da corretora não é alerta (D4).

O CC do Kalman é avisado **antes** do código, com esta seção.

## 5. Os testes

| roteiro | o que prova |
|---|---|
| **137 (novo), a margem** | 39=A, 39=0 e, 9,6 s depois, 39=4 com o texto do robô: `ENTRADA_ENCERRADA{motivo:REJEITADA_CORRETORA, texto}` depois de 8 s; `motivo_rejeicao` no `/historico/ordens`; `sem_alerta RECUSA_PNT`; `telegram RECUSA_PNT = 0`; a ficha com `quantidade 1`; uma 2ª tentativa passa a `quantidade 2` com a `ultima` nova; posição 0, nenhuma ordem viva, `divergente false` |
| **137, a corrida** | o prazo de 10 s cancela, o PnT responde 35=9 (39=4, 102=1, "ended already") e 137 ms depois o 39=4 do robô: sem `RECUSA_PNT` e com motivo `REJEITADA_CORRETORA` (P1) |
| **137, a rede de segurança** | o 35=9 "ended already" sem relatório final: `RECUSA_PNT` depois de 3 s e o mass status (P3) |
| **137, os outros papéis** | o mesmo texto num 39=4 da 1014 com posição: recriação como hoje e o alerta de hoje |
| 03 | o 39=8 com o `texto` "[103] 58" (P2), contra o exemplo da SPEC-ORDENS v1.28 §4.1 (`contrato_evento`, verbo novo) |
| 118 e 132 | o contrato: `texto` no `ENTRADA_ENCERRADA` e `recusas_corretora_hoje` na ficha e na lista, conferidos contra os exemplos JSON do §4 |
| unidade | a regra do texto (os três textos reais de 17/11 casam; "Order cannot be canceled" não casa); o 39=8 com `texto` "[103] 58" |

**Verificação ao contrário:** com a regra desligada, o 137 tem de reprovar.

## 6. Estimativa

| etapa | o quê | tempo |
|---|---|---|
| E1 | §3.1 e §3.2, o evento, a ficha e a lista, a regra do texto, os testes de unidade | ~3 h |
| E2 | o roteiro 137 (o PnT falso já manda 35=8/35=9 com qualquer tag, pelo verbo `envia`), os ajustes do 118 e do 132, SPEC-ORDENS, SPEC v1.36 §5 e ALERTAS | ~3 h |
| suíte | completa, com os dois bancos e `-race` (~19 min). **Fora do pregão** enquanto o REAL estiver em uso | fora do pregão |
| E3 | o roteiro de instalação: só binário, sem migração, um restart | ~1 h |

Total: **~1 dia de trabalho**, em duas etapas com o ok do George entre elas, mais a suíte numa
janela fora do pregão. A instalação vem depois da v1.36.1.

## 7. Perguntas ao PnT/BTG (entram na lista pendente)

1. **O motivo real da recusa da RealOrder.** Quando a BTG recusa a ordem do robô (a RealOrder
   178312978 de 17/11), o PnT devolve só "This strategy was changed to Error state, because offer
   RealOrder Id: N is Rejected." e um 39=4. Dá para receber o motivo real (margem de day trade,
   limite de risco, outro), no 58 ou num campo próprio? De preferência num 39=8 com o 103.
2. **A latência**, a pergunta que já estava pendente (ESTADO-2026-11-16 §2).
