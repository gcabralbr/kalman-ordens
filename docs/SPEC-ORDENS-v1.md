# Spec de Implementação — Serviço de Ordens Reais do Kalman (`kalman-ordens`)

## Contexto

O Kalman (Python, `/opt/kalman` na .51) opera WINFUT em paper trade para ~186
usuários. O gestor (`usuarios.py`) decide entradas e saídas. Esta spec cria um
**serviço novo e independente** que transforma as decisões do gestor de **um único
usuário** em ordens reais no PnT, via DSA (FIX 4.4).

Base documental (ler antes de codar):

- `DIAGNOSTICO-ORDENS.md` e `docs/ADENDO-DIAGNOSTICO-ORDENS.md` — o que o serviço
  antigo (pntfixrepo) faz, o que reaproveitar e o que **não** replicar.
- RoE do PnT revisado em 31/10/2025 (`docs/pnt/`) — formato das mensagens.
- **Régua de confiança:** comportamento em produção do PT > `dsa_FIX44.xml` > RoE.
  O RoE está atrás da prática (ex.: ClOrdID > 28 caracteres funciona hoje).

**Escrito sem as respostas do PnT.** Cada suposição que depende delas está marcada
`[PnT-n]` (numeração da lista enviada ao PnT) e tem um padrão seguro. Quando a
resposta chegar, ajusta-se só o ponto marcado.

**Regra de escopo:** o serviço não decide *quando* operar. Ele executa, protege,
limita e registra. Toda decisão de negócio continua no gestor.

### Decisões assumidas (confirmar com o Fernando)

| # | Decisão | Assumido nesta spec |
|---|---|---|
| D1 | Gestor do usuário real usa fills simulados ou reais? | **Reais.** Posição, preço médio e PnL do usuário real vêm do serviço. Rejeição por margem ou fill parcial mudam a operação no gestor. |
| D2 | Proteção no servidor: 1014 ou SwingTrade (2028)? | **1014** só com stop (sem gain). SwingTrade fica para a v2, depois do teste em HML. |
| D3 | Estratégia da entrada e da saída | **1031 limite DAY** (o "modo 2" do `createSO`, caminho já usado em produção), com preço agressivo e colar. |
| D4 | TRSP, STOP_GAIN e META | Continuam no gestor. Na v1 a 1014 é só o stop por operação (rede de segurança). Mover o stop da 1014 pelo TRSP fica para a v2. |

---

## Parte 1 — Processo e isolamento

- **Stack:** Java 21 + QuickFIX/J 2.3.x. HTTP leve (Javalin ou Spring Boot sem
  extras; o CC escolhe e justifica). Nada de GX, BLK, socket 6555 ou `EXIT`.
- **Repositório novo** (`kalman-ordens`), sem histórico do ironbotrepo.
- **Processo próprio:** systemd `kalman-ordens`, diretório `/opt/kalman-ordens`,
  dados em `/var/lib/kalman-ordens/` (store e log FIX).
- **HTTP só em `127.0.0.1:9092`.** O backend do Kalman (9091) é o único cliente.
  9090 é o marketdata Go, 9091 o Kalman.
- **Credenciais** em `/etc/kalman-ordens/service.env` (modo 600, fora do repo):
  SenderCompID, senha, conta (com dígito), token do bot do Telegram.
- **Sessão FIX própria** (usuário novo do PnT quando sair `[PnT-1]`; em HML usa o
  usuário de homologação). **Nunca** compartilha sessão com o PT.
- **Mesma conta do PT.** Consequências obrigatórias:
  - a posição de referência é a **do Kalman**, montada pelos fills das ordens dele,
    nunca a posição da conta;
  - todo mass status (que é por conta) traz ordens do PT: filtrar pelo prefixo do
    ClOrdID e ignorar o resto;
  - nenhum comando "zerar conta". Zerar = zerar a posição do Kalman com ordens do
    Kalman.

---

## Parte 2 — Contrato Kalman ↔ serviço

### 2.1 Pedido de ordem (Kalman → serviço)

    POST /ordens
    {
      "operacao_id": 9045770,        // id da kalmanoperacao
      "uid": 7,
      "papel": "ENTRADA" | "SAIDA",
      "lado": "C" | "V",
      "qtd": 1,
      "stop_pts": 500,               // só na ENTRADA: stop por operação do perfil
      "motivo": "SINAL" | "STOP" | "TRSP" | "STOP_GAIN" | "META" | "FIM_JANELA"
               | "SINAL_OPOSTO" | "STOP_MANUAL",
      "ts_decisao": "2026-10-13T10:15:02.123-03:00"
    }

Resposta **síncrona** = aceito ou recusado pelo serviço (limites, parada,
idempotência). **Não** é fill. Recusa vem com `codigo` e `motivo` legíveis.

- `uid` diferente de `ORDENS_UID_REAL` → recusa (`UID_NAO_HABILITADO`).
- `stop_pts` ausente na ENTRADA → recusa. Sem stop, não há ordem.
- O serviço **não** recebe preço do gestor. Ele lê `/precos/atual` do marketdata
  (bid, ask, último, status84, idade) por conta própria.

### 2.2 Eventos (serviço → Kalman)

    GET /eventos?desde=<seq>

Lista ordenada por `seq` (monotônico, persistido). O gestor consulta no próprio
loop (a cada 200–500 ms) e guarda o último `seq` aplicado. Pull é escolha
deliberada: se o Kalman reiniciar, ele recupera tudo a partir do último `seq`.

Tipos:

| Tipo | Quando | Campos |
|---|---|---|
| `ENTRADA_EXECUTADA` | cada fill da entrada | qtd, preço, qtd_total, preço_médio |
| `ENTRADA_ENCERRADA` | entrada terminou (total, parcial ou nada) | qtd_final, preço_médio, motivo |
| `PROTEGIDA` | 1014 aceita (39=0) | preço_stop, preço_limite |
| `SAIDA_EXECUTADA` | cada fill de saída (inclusive da 1014) | qtd, preço, origem (`GESTOR` / `STOP_SERVIDOR`) |
| `ZERADA` | posição do Kalman voltou a 0 | preço_médio_saída, pnl_pts |
| `RECUSADA` | ordem rejeitada pelo PnT | motivo (texto 58 + código 103) |
| `ALERTA` | qualquer situação da Parte 7 | texto |

### 2.3 O que muda no gestor (só para o usuário real)

- Decisão de entrada → `POST /ordens` (ENTRADA). A operação fica "aguardando
  execução" e **não** existe posição simulada até chegar `ENTRADA_EXECUTADA`.
- Preço de entrada, quantidade e PnL da operação = os do serviço (D1).
- `ENTRADA_ENCERRADA` com qtd 0 → operação cancelada (motivo gravado).
- Saída (qualquer motivo da cascata) → `POST /ordens` (SAIDA). A operação só fecha
  quando chega `ZERADA`.
- `SAIDA_EXECUTADA` com origem `STOP_SERVIDOR` → o gestor fecha a operação com
  motivo `STOP_SERVIDOR`.
- Os outros ~185 usuários seguem em paper, sem mudança.
- O CC confirma as colunas da `kalmanoperacao` e propõe onde gravar `orderid37`,
  preço real e motivo (sem alterar o paper dos demais).

---

## Parte 3 — Ordens no fio

### 3.1 Identificação

- **ClOrdID:** `KM-<papel>-<operacao_id>-<n>`, com papel `E` (entrada),
  `S` (saída) ou `P` (proteção 1014), e `n` = contador da mensagem dentro do papel
  (1, 2, 3…; cada replace/cancel gera o próximo `n`).
  - Determinístico: um reenvio da mesma entrada gera o mesmo ClOrdID base e é
    barrado pela idempotência (Parte 5).
  - **Sem `:` e sem `_`.** O GX do PT extrai o número da operação do 3º campo
    separado por `:` antes do `_`. Se alguma ordem do Kalman chegar ao PT (mass
    status por conta), ela **não pode** ser confundida com uma operação do GQ.
    Tarefa do CC: confirmar no bytecode se o PT faz 35=AF e como o GX trata um
    ClOrdID fora do padrão (ignora ou lança exceção).
- **Correlação:** pelo **37 (OrderID)** depois do primeiro ACK; antes dele, pelo
  ClOrdID. O 198 é guardado só como informação, **nunca** usado como chave.
- **Cancel e replace:** mandam o 37 **e** o 41 verdadeiro (ClOrdID corrente).
  Não usar `41=0` como o PT.

### 3.2 Campos comuns (NewOrderSingle)

Copiar a identificação que o `createSO` usa hoje em produção (funciona):
`1` (conta com dígito), `116`, `142`, `21=4`, `60` em UTC, `207=BVMF`. Se o PnT
exigir PartyIDs ou 35508/35509 para o usuário novo `[PnT-3]`, acrescenta-se.

- **Símbolo:** o PnT recebe o vencimento (ex.: `WINZ26`), não `WINFUT`. Config
  `ORDENS_SIMBOLO`, com verificação diária: na abertura, alertar se faltarem
  ≤ 3 pregões para o vencimento, e **recusar entradas** no dia do vencimento até
  o símbolo ser trocado.
- **Preços** sempre arredondados ao tick do WIN (5 pts), no sentido que torna a
  ordem mais agressiva (entrada/saída) ou mais conservadora (stop limite).

### 3.3 Entrada — 1031 limite DAY

`847=1031`, `40=2`, `59=0`, `44` = preço agressivo:

- compra: `ask + COLAR_ENTRADA_PTS`; venda: `bid − COLAR_ENTRADA_PTS`
  (inicial: 15 pts).
- Se não executar tudo em `TIMEOUT_ENTRADA_S` (inicial: 3 s), **cancela o
  restante**. Não persegue o preço: entrada perdida é entrada perdida.
- Fill parcial vale como posição (e a 1014 cobre o que executou).

### 3.4 Proteção — 1014 só com stop

Enviada **no tratamento do primeiro fill da entrada**, sem esperar o fim da
entrada.

- `847=1014`, `40=2`, `59=0`, lado oposto à entrada, `38` = posição atual.
- `20828` (gatilho) = preço médio real ∓ `stop_pts` (venda abaixo, compra acima).
- `20829` (limite) = gatilho ∓ `FOLGA_STOP_PTS` (inicial: 100 pts). **Nunca**
  limite igual ao gatilho: o exemplo de SwingTrade do RoE 2025 mostra o stop
  disparando e a saída ficando com 0 porque o limite estava longe do mercado.
  Também **não** usar `40=1`: o exemplo da 1014 mostra o PnT devolvendo `40=2`
  `[PnT-6]`.
- Sem gain (20830/20831) e sem stop móvel (20832–20834) na v1.
- Fills adicionais da entrada → **replace (35=G)** da 1014 com a nova quantidade e
  o gatilho recalculado sobre o novo preço médio. Um replace por vez (o PnT rejeita
  replace com outro pendente): se houver novo fill com replace pendente, enfileira
  e manda só o estado final.
- 1014 rejeitada → alerta, uma nova tentativa em 1 s. Se rejeitar de novo, **zera
  a posição** (saída da Parte 3.5) e alerta. Posição sem proteção não fica aberta.

### 3.5 Saída do gestor

Sequência obrigatória, sem atalho:

1. **Reduce-only:** `qtd_saida = min(qtd pedida, |posição real do Kalman|)`.
   Posição real zero → recusa (`SEM_POSICAO`) e alerta. Uma saída nunca abre
   posição.
2. Cancela a 1014 (35=F). Espera:
   - `39=4` → segue para o passo 3;
   - `35=9` (cancelamento rejeitado) → a 1014 provavelmente já disparou. **Não**
     manda a saída. Espera fills por `ESPERA_STOP_S` (inicial: 5 s). Se a posição
     zerar, fim (origem `STOP_SERVIDOR`). Se não zerar, segue para o passo 3 com a
     posição que restou.
3. Recalcula a posição pelos fills (pode ter mudado durante o cancelamento).
4. Envia 1031 limite DAY com `COLAR_SAIDA_PTS` (inicial: 50 pts) sobre
   bid/ask.
5. Sem execução total em `TIMEOUT_SAIDA_S` (inicial: 2 s) → replace com preço mais
   agressivo (+`COLAR_SAIDA_PTS` a cada vez), até `MAX_REPRECO_SAIDA` (inicial: 3).
   Esgotou → alerta crítico e **continua** reapregoando a cada `TIMEOUT_SAIDA_S`.
   Saída não desiste.

Se a 1014 dispara sozinha, os fills chegam como `SAIDA_EXECUTADA` com origem
`STOP_SERVIDOR` `[PnT-5]`.

### 3.6 Stop furado (gap)

Se o último negócio passar do **limite** da 1014 (`20829`) com posição aberta por
mais de `ESPERA_STOP_S`, o stop furou. O serviço cancela a 1014 e executa a saída
da Parte 3.5 a partir do passo 3, com alerta crítico.

---

## Parte 4 — Máquina de estados da operação real

    NOVA ─(envia 1031)→ ENTRANDO
    ENTRANDO ─(1º fill)→ POSICIONADA_SEM_PROTECAO ─(1014 39=0)→ PROTEGIDA
    ENTRANDO ─(rejeição / timeout sem fill)→ ENCERRADA_SEM_ENTRADA
    PROTEGIDA ─(POST SAIDA)→ CANCELANDO_PROTECAO ─(39=4)→ SAINDO ─(posição 0)→ ZERADA
    CANCELANDO_PROTECAO ─(35=9)→ AGUARDANDO_STOP ─(posição 0)→ ZERADA (STOP_SERVIDOR)
    AGUARDANDO_STOP ─(timeout, posição ≠ 0)→ SAINDO
    PROTEGIDA ─(fills da 1014)→ ZERADA (STOP_SERVIDOR)
    qualquer ─(inconsistência)→ DIVERGENTE  (alerta, bloqueia novas entradas)

- `POSICIONADA_SEM_PROTECAO` por mais de 2 s → alerta.
- `ENTRANDO` aceita fills depois do timeout de cancelamento (o cancelamento pode
  chegar depois de um fill).
- **Fill em ordem já encerrada é aceito e contabilizado**, com alerta. O RoE 2025
  mostra o PnT entregando um fill real depois de considerar a ordem encerrada.
- Um 39=A que dure mais de 3 s → alerta.

---

## Parte 5 — Limites de risco (no serviço, antes de qualquer envio)

Valores iniciais para a fase de 1 contrato; todos configuráveis no service.env.

| Limite | Inicial | Ação ao violar |
|---|---|---|
| Posição máxima absoluta (contratos) | 1 | recusa |
| Quantidade por ordem | 1 | recusa |
| Idempotência: `(operacao_id, papel)` já existe | — | recusa (`DUPLICADA`), sem alerta se for reenvio idêntico |
| Intervalo mínimo entre entradas | 60 s | recusa |
| Entradas por minuto | 1 | recusa |
| Ordens (todas as mensagens) por minuto | 10 | recusa + alerta |
| Entradas por dia | 15 | recusa + alerta |
| Perda do dia (realizada + aberta), em pts × contratos | 1.500 pts | **parada BLOQUEAR** automática + alerta |
| Janela de entradas | 09:00–14:00 | recusa |
| status84 em leilão ou preço com idade > 3 s | — | recusa entrada (saída segue) |
| Book descolado (\|meio − último\| > 150 pts) | — | recusa entrada (saída segue) |
| 3 recusas por limite em 1 min | — | **parada BLOQUEAR** automática |

- A perda aberta usa o último negócio do `/precos/atual`. Sem preço confiável
  (idade > 3 s), **bloqueia entradas** em vez de usar o último PnL conhecido.
- Saídas nunca são barradas por limite, só pela regra reduce-only.
- O intervalo mínimo entre entradas existe para cortar o laço de reentrada pós-STOP
  (pendência ALTA do Estado-Mestre) mesmo que o gestor ainda não tenha carência.

---

## Parte 6 — Parada

    POST /parada   { "modo": "BLOQUEAR" | "ZERAR", "origem": "tela" | "cli" | "auto", "motivo": "..." }
    POST /retomar  { "origem": "...", "motivo": "..." }

- **BLOQUEAR:** recusa novas entradas, cancela entradas pendentes, **mantém** a
  1014 e aceita saídas do gestor.
- **ZERAR:** BLOQUEAR + saída da posição do Kalman (Parte 3.5) com motivo
  `PARADA`.
- O estado da parada é **persistido** e sobrevive a restart. Serviço reiniciado em
  parada continua em parada.
- Funciona com o Kalman fora do ar: há um CLI (`kalman-ordens parar|zerar|retomar`)
  que chama o HTTP local, para uso por SSH.
- Retomar é sempre manual (nunca automático).
- Parar o processo (systemd stop) **não** cancela a 1014: a posição continua
  protegida no PnT. Isso é intencional.

---

## Parte 7 — Sessão FIX e recuperação

### 7.1 Configuração QuickFIX/J

- Initiator, FIX.4.4, `HeartBtInt=30`, `TimeZone=America/Sao_Paulo`.
- `StartTime`/`EndTime` cobrindo o pregão, terminando antes do desligamento diário
  do PnT (o GQ desliga às 18:00; usar 08:30–17:55 como inicial).
- **FileStoreFactory** e **FileLogFactory** em `/var/lib/kalman-ordens/fix`.
  Nada de MemoryStore nem ScreenLog.
- **Sequência:** reset só na virada do período da sessão (comportamento natural do
  QuickFIX/J com StartTime/EndTime). `ResetOnLogon=N`, `ResetOnDisconnect=N`,
  `ResetOnLogout=N`. Numa queda no meio do dia, a reconexão mantém a sequência e
  usa ResendRequest `[PnT-11]`. Se o PnT exigir reset a cada logon, troca-se para
  `ResetOnLogon=Y` e a recuperação depende só do mass status (7.2).
- **Dicionário:** `dsa_FIX44.xml` atual, com `ValidateUserDefinedFields=N` e
  `AllowUnknownMsgFields=Y` na recepção, registrando no log as tags desconhecidas.
  As respostas de 2025 trazem tags fora do dicionário (20828–20835, 20844–20883,
  5600, 911…). Troca-se pelo `FIX44.xml` novo quando chegar `[PnT-2]`.
- `sendToTarget` só com `isLoggedOn()`; o retorno de `sendToTarget` é verificado.
  Ordem que não saiu = recusa ao gestor, nunca "OK".
- **Gravar antes de enviar:** a ordem entra na tabela (estado `ENVIANDO`) antes do
  `sendToTarget`.
- **Reject de sessão (35=3)** e **Business Reject (35=j)** chegam por `fromAdmin`/
  `fromApp` e são tratados: ordem ligada pelo `45` (RefSeqNum) vira `RECUSADA`.
- Processamento das mensagens **fora da thread do QuickFIX/J** (fila única,
  processada em ordem).

### 7.2 Ao logar (inclusive na reconexão)

1. Envia `35=AF` com `585=9` e a conta. Se vier `35=j` com `380=4` (sistema
   subindo), tenta de novo com espera crescente (1, 2, 5, 10 s…).
2. Filtra as respostas pelo prefixo `KM-`.
3. Concilia com a tabela:
   - ordem viva no PnT e encerrada na tabela → alerta, atualiza a tabela;
   - ordem viva na tabela e ausente no PnT → alerta, estado `DESCONHECIDA`
     (o mass status pode não trazer ordens encerradas `[PnT-13]`);
   - fills novos no 150=I (quantidade executada maior que a registrada) → aplica.
4. **Posição ≠ 0 sem 1014 viva → cria a 1014 imediatamente.**
5. Qualquer divergência não resolvida → `DIVERGENTE` + parada BLOQUEAR.

### 7.3 Ao subir o processo

Carrega da tabela o estado do dia (ordens vivas, posição, parada, contadores dos
limites) **antes** de logar. Os contadores de limites são reconstruídos a partir das
tabelas, não zerados.

### 7.4 Ao fim do dia

- `HORA_ZERAGEM` (inicial: 14:30): se a posição do Kalman ≠ 0, ZERAR + alerta.
  O gestor fecha tudo às 14:01 (`HORA_FIM_LIMITE`); chegar aqui com posição já é
  anomalia.
- Logout limpo antes do `EndTime`.

---

## Parte 8 — Persistência (MariaDB `dbecotools`)

O CC propõe o DDL final; os campos abaixo são o mínimo.

**`kalmanordem`** — uma linha por ordem lógica (entrada, proteção, saída) de uma
operação:
`id`, `data`, `uid`, `operacao_id`, `papel` (E/P/S), `clordid_base`,
`clordid_atual`, `orderid37`, `secid198`, `estrategia` (847), `lado`, `qtd`,
`preco`, `preco_stop`, `preco_stop_lim`, `estado`, `qtd_exec`, `preco_medio`,
`motivo_gestor`, `motivo_rejeicao`, `criado_em`, `atualizado_em`.
Único: `(data, operacao_id, papel)`.

**`kalmanordemevento`** — toda mensagem recebida ou enviada que muda estado:
`id` (é o `seq` da Parte 2.2), `ordem_id`, `direcao`, `msgtype`, `exectype` (150),
`ordstatus` (39), `execid` (17), `lastqty` (32), `lastpx` (31), `cumqty` (14),
`avgpx` (6), `texto` (58), `codigo` (102/103), `fix_raw`, `em`.
**Único em `execid`** quando presente: o mesmo fill nunca é aplicado duas vezes
(reconexão, ResendRequest e mass status podem repeti-lo).

**`kalmanordemestado`** — chave/valor: estado da parada (modo, origem, motivo,
quando), último `seq` publicado, símbolo em uso.

A posição do Kalman é **derivada** dos eventos de fill (soma por lado). Pode ser
mantida em memória, mas sempre reconstruível das tabelas.

---

## Parte 9 — Alertas e observabilidade

- **Telegram direto da .51** (token no service.env). Não usar o webhook da .53:
  com dinheiro real, o alerta não pode depender da máquina de desenvolvimento.
- Alertar: recusa do PnT, limite violado, parada (qualquer origem), sessão caiu ou
  voltou, posição sem proteção > 2 s, stop furado, fill em ordem encerrada,
  39=A > 3 s, `DIVERGENTE`, vencimento próximo, resultado da conciliação.
- Alertas críticos (posição sem proteção, stop furado, saída esgotou o repreço,
  DIVERGENTE) repetem a cada 30 s até resolver.
- `GET /saude`: sessão logada, seq de entrada e saída, posição, estado da parada,
  contadores dos limites, idade do último preço, ordens vivas.
- Logs no journal com o ClOrdID e o 37 em toda linha de ordem.

---

## Parte 10 — Conciliação diária

Job às 18:10 (após o fim da sessão):

1. Posição final do Kalman = 0. Se não, alerta crítico.
2. Para cada operação real do dia: fills da entrada e da saída nas tabelas do
   serviço × preço/quantidade/motivo gravados na `kalmanoperacao`. Diferença →
   linha no relatório.
3. Ordens que terminaram o dia em estado não final (`ENVIANDO`, `DESCONHECIDA`,
   `DIVERGENTE`) → relatório.
4. Resumo no Telegram: operações, contratos, PnL em pts e R$, recusas, alertas.

A conciliação com a nota da corretora fica para a v2.

---

## Parte 11 — Testes

### 11.1 PnT falso (obrigatório, antes de qualquer HML)

Um acceptor QuickFIX/J local que imita o PnT, roteirizável por cenário, usando as
mensagens do RoE 2025 e do log do PT como modelo. Cenários mínimos:

1. Entrada total → 1014 → saída do gestor (cancel 39=6/39=4 → saída → fills).
2. Entrada parcial (2 fills) → replace da 1014 com a nova quantidade.
3. Entrada rejeitada por margem (35=8 39=8 com 103).
4. Entrada sem fill → timeout → cancelamento → **fill chega depois do 39=4**.
5. 1014 dispara sozinha → fills → `ZERADA` com origem `STOP_SERVIDOR`.
6. Cancelamento da 1014 rejeitado (35=9) porque já disparou → nenhuma saída extra.
7. 1014 dispara e a saída não executa (gap) → stop furado → saída agressiva.
8. Queda da sessão com entrada viva → reconexão → mass status → fill recuperado.
9. Queda com fill durante a queda → ResendRequest → fill aplicado **uma** vez.
10. Restart do processo com posição aberta e 1014 viva → nada é reenviado.
11. Restart com posição aberta e **sem** 1014 → 1014 criada no logon.
12. Mass status devolvendo ordens do PT (prefixo `GQ-`) → ignoradas.
13. Laço de reentrada: 10 POST de entrada em 1 s → 1 aceito, 9 recusados, parada
    automática.
14. Saída pedida sem posição → recusada (reduce-only).
15. Perda do dia atingida com posição aberta → BLOQUEAR, posição mantida com 1014.
16. Preço velho (> 3 s) → entrada recusada, saída aceita.
17. 39=A que não vira 39=0 → alerta.
18. 35=j 380=4 no mass status → retry com espera.

### 11.2 Homologação do PnT

A HML roda com dados de ontem: serve para **protocolo**, não para mercado.
Não precisa do usuário novo. Verificar:

- logon, sequência e reconexão no meio do dia `[PnT-11]`;
- 1031 limite aceita, cancelamento, replace;
- 1014 só com stop: envio, replace de quantidade e preço, cancelamento; disparo com
  e sem folga `[PnT-5, PnT-6]`;
- mass status por conta: o que volta (ativas e encerradas?) `[PnT-13]`;
- comprimento real aceito para o ClOrdID `KM-…` `[PnT-4]`;
- **teste exploratório da SwingTrade** (2028) com entrada por gatilho e gain
  sentinela, enviada por uma ferramenta de bancada (não pelo fluxo do serviço),
  para a decisão D2 da v2 `[PnT-9, PnT-10]`.

---

## Parte 12 — Implantação em degraus

| Fase | O quê | Critério para avançar |
|---|---|---|
| F0 | PnT falso: os 18 cenários passando | todos verdes |
| F1 | HML do PnT: lista da 11.2 | protocolo confirmado, respostas do PnT incorporadas |
| F2 | Produção, usuário novo, **ordens que não executam**: 1031 limite longe do preço + cancel; 1014 com gatilho longe + replace + cancel; mass status | 3 pregões sem alerta inesperado |
| F3 | Produção, **1 contrato, 1 usuário**, limites da Parte 5 | 10 pregões; conciliação sem diferença |
| F4 | Revisão: TRSP na 1014, SwingTrade, mais contratos | decisão do Fernando |

Entre F2 e F3: o Kalman continua rodando o paper do usuário real **em paralelo**
por alguns dias (operação real e simulada do mesmo sinal), para medir a diferença
entre execução real e paper.

---

## Pendências que dependem do PnT (ajustar na terça)

| Ref. | Ponto da spec | Padrão adotado até a resposta |
|---|---|---|
| PnT-1 | usuário novo, IP da .51 | HML com usuário de homologação |
| PnT-2 | `FIX44.xml` atual | dicionário antigo + `ValidateUserDefinedFields=N` |
| PnT-3 | campos de identificação | os mesmos do `createSO` (116, 142, 1) |
| PnT-4 | tamanho do ClOrdID | aceito na prática (> 28) |
| PnT-5 | fills da 1014 após disparo | mesmo 37; origem detectada pelo papel `P` |
| PnT-6 | 40=1 na 1014 | não usar; sempre 40=2 com folga |
| PnT-7 | disparo em leilão | não assumir nada; stop furado (3.6) cobre o pior caso |
| PnT-8 | 1014 no fim do dia | zeragem às 14:30 torna isso irrelevante na v1 |
| PnT-11 | sequência na reconexão | sem reset intradiário |
| PnT-12 | COD | não pedir; se vier ligado por padrão, a 1014 precisa sobreviver (pedir exceção) |
| PnT-13 | mass status | assumir que traz só ativas; ordens sumidas viram `DESCONHECIDA` |
| PnT-15 | códigos de erro | gravar 102/103 e o texto 58 sem interpretar |
| PnT-16 | self-trade | registrar; tratar como rejeição comum |

---

## Ordem de implementação sugerida

1. Repositório, systemd, service.env, sessão FIX com FileStore e logon em HML.
2. Tabelas e gravação de eventos (com `execid` único).
3. Entrada 1031 + cancelamento por timeout + máquina de estados.
4. 1014 (envio, replace, cancelamento) + saída da Parte 3.5.
5. Limites, parada e CLI.
6. Mass status no logon e recuperação no restart.
7. Alertas no Telegram e `/saude`.
8. PnT falso com os 18 cenários (pode começar junto com o passo 3).
9. Integração no gestor (Parte 2.3), atrás de `ORDENS_UID_REAL` vazio por padrão.
10. Conciliação diária.
