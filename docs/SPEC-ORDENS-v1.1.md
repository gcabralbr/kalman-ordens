# Spec de Implementação — `kalman-ordens` — v1.1

Revisão da [SPEC-ORDENS-v1.md](SPEC-ORDENS-v1.md) (mantida intacta ao lado). Este documento
lista **só o que mudou** e, para cada mudança, a parte da v1 afetada e o roteiro que a cobre.
Onde a v1.1 não diz nada, vale a v1.

**Revista pela [v1.2](SPEC-ORDENS-v1.2.md):** conta, destino e símbolo vêm da operação, não do
service.env (B3 e a parte de símbolo de B5, P7 e as chaves `ORDENS_*` abaixo foram substituídas).

Os roteiros (`cenarios/*.fix`, formato em `cenarios/FORMATO.md`) são a especificação executável:
cada decisão abaixo está afirmada em pelo menos um deles.

Data: 2026-10-11. Decisões confirmadas pelo responsável em oito rodadas (stack e envio;
roteiros e preço; fill perdido por 35=3 e saída com ticker divergente; banco, idempotência e
Telegram; invariante de proteção, sql_mode, permissões e -race; recriação imediata com limite,
build e preparação da F1; 9/10 errados e F1 em duas rodadas; log real de HML do PT).

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
| F10 | **Enum 585=9 no dicionário.** O mass status por conta usa `585=9` (RoE 2025), valor que o `dsa_FIX44.xml` de 2020 não declara; a validação estrita reprovava o próprio AF do serviço. A cópia ganhou o valor 9 (terceira diferença, `pnt/dicionario/LEIA.md`). Achado pelo teste da sessão contra o PnT falso. | 7.2 | todos (INICIO) |
| F11 | **CheckSum (10) errado é aceito.** A config antiga do PT na HML tinha `ValidateLengthAndChecksum=N` e os exemplos do RoE têm 9/10 inconsistentes. O QuickFIX/Go v0.9.12 não confere o valor do 10 na recepção (só usa o `10=` para achar o fim da mensagem): a mensagem é aplicada normalmente, sem 35=3. Não há opção equivalente ao `ValidateLengthAndChecksum=N`. | 7.1 | 28 |
| F12 | **BodyLength (9) errado: nunca perda silenciosa.** A biblioteca usa o 9 para enquadrar e, se não bate, **descarta a mensagem em silêncio** (só `Msg Parse Error` no log de eventos; sem 35=3, nada chega à aplicação). 9 menor que o real perde aquela mensagem; 9 maior perde aquela e a seguinte (enquadramento emendado). A recuperação vem quando a mensagem ou o heartbeat seguinte revela o buraco de sequência: ResendRequest e reenvio com 43=Y, aplicado uma vez (F7). Como isso pode levar até um `HeartBtInt` (30 s), a camada PnT intercepta o evento do log da biblioteca e avisa o motor: alerta crítico **`MENSAGEM_ILEGIVEL`** + mass status na hora (resolvido ao fim da conciliação). Risco aberto: se o reenvio do PnT também vier com 9 errado, a biblioteca entra em laço de ResendRequest (pendência 9). | 7.1, 9 | 29 |
| F13 | **Listeners de teste só em 127.0.0.1.** O QuickFIX/Go lê o `SocketAcceptHost` só nas configurações globais (na sessão é ignorado e o acceptor escuta em todas as interfaces, o que disparava o aviso do Firewall do Windows a cada `go test`). Acceptor do PnT falso, proxy de queda, marketdata falso e HTTP do serviço escutam em loopback; `pnt/loopback` confere em tempo de execução (conexão por um IP externo da máquina tem de falhar) e varre o código (todo listener com `127.0.0.1` literal). | — | `pnt/loopback`, `pnt/falso`, fumaça do executável |
| F14 | **Estado de erro do PnT (39=7) é transitório; `20856=Y` em toda NewOrderSingle.** No log real de HML do PT (`cenarios/referencia/`), com `20856=Y` (ActivateErrorStatusStrategy, RoE 2025: "the order will be automatically activated upon entering in error status") a ordem entra em erro e volta sozinha em 0–16 ms: `5/5 → 7/7 → D/0 → F/1 → F/2` (14 casos, todos multileg 35=AB depois de um replace, 58 "Sel. Coef. … is too agressive for market Coef. …"). Regras: (1) `20856=Y` em toda NewOrderSingle, 1031 e 1014 (não no replace: o PnT fala em "DSA creation order"); a cópia do dicionário declara o campo no 35=D (quarta diferença, `pnt/dicionario/LEIA.md`); o RoE só lista a tag na Sniper e no multileg → teste da rodada 1 (`deploy/LEIA.md` 2.11), senão pergunta ao PnT. (2) 39=7 → aguardar `ESPERA_ERRO_S` (2 s) por qualquer outro 39 (150=D/39=0 ou fill); se vier, só registra. Se não vier: **1014** = proteção ausente → cancela e, com o 39=4, recria com o mesmo gatilho e limite sob o limite da P8 + alerta crítico `PROTECAO_EM_ERRO`; **entrada** → cancela + `ENTRADA_EM_ERRO`; **saída** → cancela + `SAIDA_EM_ERRO` (crítico) e a saída seguinte sai com o próximo repreço. Ordem em erro não recebe replace (nem repreço da saída, nem ajuste da 1014). (3) `150=D` (Restated) só atualiza o estado — antes era tratado como confirmação de replace. (4) O texto 58 do 39=7 vai sempre no alerta. Evidência só de multileg: o log não tem nenhum 35=D; para 1031/1014 a regra é defensiva. | 3.1, 3.4, 3.5, 7.2 | 30 (caso real: reativada, nada acontece), 31 (1014 → recriada), 32 (entrada → cancelada), 33 (saída → cancelada, segue o repreço) |
| F15 | **Senha fora de qualquer arquivo.** O QuickFIX/Go grava a mensagem como foi ao fio no log da sessão **e no store** (`.body`, para reenvio): o logon levaria o 554 em texto puro, como o log antigo do PT gravava 553/554. A camada PnT mascara 554 e 925 (`***`) no log de mensagens, no log de eventos e no store; no fio a senha vai inteira. O logon mascarado no store nunca é reenviado (mensagem de sessão: no ResendRequest vai GapFill). **O 9 do logon fica como foi ao fio** (o log é fiel; revela o tamanho da senha): por isso o diretório FIX (log e store) é só do usuário `kalman-ordens`, diretórios 700 e arquivos 600 — `UMask=0077` e `StateDirectoryMode=0700` na unidade, e o serviço aperta o que já existia ao subir (o QuickFIX/Go cria o log com `0777 & ~umask` e o store com `0660`). | 7.1, 12 | `pnt` TestSenhaForaDosArquivos (falha sem a máscara: log e store), fumaça do executável (FIX_DIR e log do serviço) |
| F16 | **Observado no log real de HML (out/nov 2025, 370 mensagens):** 9 e 10 corretos em 100% das mensagens do PnT (F12: o detector fica, o normalizador não é necessário); `37` no formato `dsaGQ2.N` para o nosso usuário de HML; `198` estável por `37`, inclusive em 20 replaces; `39=A` vem sem `198` (o motor já guarda o 198 quando aparece); cancel sem 37 (`41=0`) volta `35=9` `[DSA-40] … Robot Order not found` — o 37 continua obrigatório no cancel e no replace, como já está (F4). Todas as 194 mensagens de aplicação do PnT no log (inclusive multileg com grupos que o dicionário de 2020 não descreve) passam pela sessão do serviço sem 35=3 e sem descarte. | 7.1, 7.2 | `pnt/logreal_test.go` |
| F17 | **Cancel rejeitado (35=9) da 1014 em erro.** "Não encontrada" (`37=NONE` ou "Robot Order not found", o `[DSA-40]` do log real) → a 1014 não existe mais → recria com o mesmo gatilho e limite sob o limite da P8. Outro 35=9 → mass status imediato e o estado real decide: ausente (não conta como divergência) ou terminal → recria sob a P8; **ainda viva em 39=7** → não recria (pode reativar sozinha pelo 20856, e seriam duas proteções) e não zera: alerta crítico repetido `PROTECAO_EM_ERRO_PRESA` + `BLOQUEAR`, decisão humana; o alerta resolve se ela sair do erro (a parada fica até o `retomar`); reativada antes do mass status → a proteção voltou, nada a fazer. | 3.4, 7.2 | 34 (não encontrada → recriada), 35 (viva em 39=7 → BLOQUEAR, sem recriar nem zerar), 36 (ausente no mass status → recriada) |
| F9 | **Todo 35=3 enviado pelo serviço** → alerta crítico `SESSAO_REJEITOU_MENSAGEM` + mass status para reconciliar (a mensagem descartada pode ter sido um fill). | 7.1, 9 | 22 |

## 3. Decisões dos roteiros (Parte A)

| # | Decisão | Status | Parte da v1 | Roteiro |
|---|---|---|---|---|
| A1 | Cancel só com tags do dicionário (= F4). | confirmada | 3.1 | 01 |
| A2 | Validação estrita das mensagens do serviço em todos os roteiros (= F3). | confirmada | 11.1 | todos |
| A3 | **Fill depois do cancelamento.** `ENTRADA_ENCERRADA` fica retido por `ESPERA_ENCERRAMENTO_S` (inicial 5 s) depois do 39=4 (e do 39=C). Fill dentro da espera = entrada normal, sem `ENTRADA_ENCERRADA` intermediário; o único sai no fim da espera com a quantidade executada. Fill depois da espera = `ENTRADA_TARDIA`: o serviço cria a 1014 e zera na hora (Parte 3.5), com alerta; o gestor recebe `ENTRADA_EXECUTADA tardio=true` e `SAIDA_EXECUTADA origem=ENTRADA_TARDIA` só para registro e **nunca reabre a operação**. Rejeição (39=8) e execução total (39=2) encerram sem espera. | **ajustada** | 3.3, 4, 2.2, 2.3 | 04, 04b (efeito no tempo: 02, 13, 17, 19) |
| A4 | **Origens da saída:** `GESTOR`, `STOP_SERVIDOR`, `STOP_FURADO`, `PARADA`, `ZERAGEM`, `PROTECAO_REJEITADA`, `PROTECAO_INSTAVEL` (P8), `ENTRADA_TARDIA`. O gestor trata tudo o que não é `GESTOR` como operação fechada pelo serviço, com esse motivo. | confirmada | 2.2, 2.3 | 01, 05, 06, 07, 04 |
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

## 4b. Contrato HTTP: idempotência e 503 (Parte C)

| # | Regra | Parte da v1 | Roteiro |
|---|---|---|---|
| C1 | **Idempotência do POST /ordens por `(operacao_id, papel)`.** Corpo idêntico ao do pedido original (todos os campos, inclusive `ts_decisao`) → **200 com o resultado original**, `repetido=true` e `estado_atual` (a operação como está agora: estado, posição, ordens); sem alerta, sem ordem nova. Vale para ENTRADA e SAIDA, inclusive depois de a operação zerar (o reenvio de uma SAIDA aceita devolve o original, não `SEM_POSICAO`). Corpo diferente → recusa `DUPLICADA` + alerta `DUPLICADA_DIFERENTE` (bug no gestor). Pedido recusado (limite, preço...) não cria o par: o reenvio é avaliado de novo. O pedido e a resposta originais ficam gravados na operação (sobrevivem a restart). | 2.1, 5 | 23, 25 |
| C2 | **503 `RESULTADO_DESCONHECIDO`:** a API espera o motor por `HTTP_ESPERA_S` (inicial 5 s); estourou, responde 503 e **o motor processa o pedido mesmo assim**. Contrato com o gestor: diante de 503 (ou de timeout/erro de rede no POST), **reenviar o mesmo corpo**; a idempotência (C1) devolve o resultado original. Nunca mandar um corpo novo para a mesma operação. | 2.1 | 24 |

## 4c. Persistência, executável e alertas

| # | Regra | Parte da v1 | Teste |
|---|---|---|---|
| P1 | **MariaDB 10.11** (produção 10.11.15 na .51). SQL restrito ao que roda no 10.11; testes em container `mariadb:10.11.15`, nunca no MySQL 5.7 do note. Schema próprio do serviço (`kalmanordens` em produção, a confirmar; `kalmanordens_teste` nos testes), nunca o dbecotools. DDL em `migracoes/0001_inicial.sql` (reescrito para o modelo em Go; nunca foi aplicado), aplicado pelo próprio serviço na subida (`kalmanordem_migracao`). Horários em UTC (`DATETIME(3)`); `data` é o dia do pregão. A limpeza de tabelas dos testes só roda em schema `*_teste`. | 8 | `internal/repo/contrato_test.go` (mesma bateria na memória e no MariaDB) |
| P2 | **Executável:** `kalman-ordens servir` (systemd), `parar`/`zerar`/`retomar -motivo` e `saude` pelo HTTP local; o HTTP recusa subir fora de loopback; SIGTERM faz logout FIX e não cancela nada no PnT. `deploy/` tem a unidade do systemd, o `service.env.example` e o roteiro de instalação. | 1, 6 | `cmd/kalman-ordens/main_test.go` (ponta a ponta com MariaDB) |
| P3 | **Telegram direto** (`api.telegram.org`, token e chat no service.env). Fila própria, timeout de 3 s, nunca bloqueia o motor. Falha → log + nova tentativa com espera crescente (1, 2, 5, 10, 30, 60 s). **Crítico nunca é descartado**; não crítico desiste após 3 tentativas ou quando a fila passa de 1000. 4xx permanente não se retenta; 429 respeita `retry_after`. O token nunca vai para o log. | 9 | `internal/alerta/telegram_test.go` |
| P5 | **Invariante de proteção:** "posição ≠ 0 e nenhuma ordem de saída viva ⇒ 1014 viva", verificada a cada 1 s. Violada por mais de 2 s → o serviço recria a 1014 com o **mesmo gatilho e limite da original** (não depende do feed; sem 1014 anterior, calcula sobre o preço médio da entrada) e emite o alerta crítico `PROTECAO_RECRIADA` (resolvido quando a 1014 é aceita). Pega qualquer caminho que deixe a posição sem proteção, previsto ou não. Consequências: (a) 1014 cancelada/expirada pelo PnT sem pedido nosso não tem mais recriação própria — é a invariante que recria; (b) saída com ticker divergente que não executou (B5.c) é **cancelada** no timeout, sem nova saída (decisão humana), e a invariante recria a 1014 (resolve a pendência 1); (c) saída sem preço (B2) deixa a 1014 voltar; quando o preço volta, a saída cancela a 1014 recriada antes de sair (Parte 3.5). | 3.4, 3.5, 4 | 26 (caminho não previsto), 16i, 16b |
| P8 | **1014 cancelada/expirada pelo PnT sem pedido nosso → recriação imediata, com limite** (substitui a consequência (a) de P5). Recria **na hora**, com o mesmo gatilho e limite, + alerta crítico `PROTECAO_RECRIADA` (resolvido quando a recriada é aceita). Recriada que cai de novo em menos de 30 s, ou que é **rejeitada** → mais uma tentativa. Falhou de novo → alerta crítico **`PROTECAO_INSTAVEL`** e **zera a posição** (origem `PROTECAO_INSTAVEL`; mesma regra da 3.4: posição sem proteção possível não fica aberta). Uma recriada que sobrevive 30 s zera o contador. **A invariante (P5) continua como rede para os demais caminhos e usa o mesmo contador**: não há laço cria/cancela por nenhum caminho. | 3.4, 4 | 26 (recriação imediata; contador zera após 30 s), 27 (PnT cancelando em laço → PROTECAO_INSTAVEL + zeragem) |
| P9 | **Build e deploy:** `scripts/build.sh` gera binário estático (`CGO_ENABLED=0`, linux/amd64) com o commit embutido (`-X main.versao`), visível em `kalman-ordens versao`, no `/saude` (`versao`) e no log ao subir; árvore suja → sufixo `-sujo`. Roda direto no servidor (Go ≥ 1.24; a .53 tem 1.26) ou com `--container` (`golang:1.24-bookworm`). Deploy = `git clone`/`pull` no servidor + `build.sh` + instalar. Passo a passo da F1 na .53, checklist da terça e diferenças da .51 (F2) em `deploy/LEIA.md`. | 1, 12 | `cmd/kalman-ordens/main_test.go` (versão no /saude) |
| P6 | **sql_mode fixo na conexão:** `STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION` (valor da .51, conferido; padrão do container), aplicado pelo driver em cada conexão nova. Não depende da configuração global do servidor. | 8 | `internal/repo/contrato_test.go` (global vazio, sessão continua certa) |
| P7 | **Permissões:** usuário de sistema `kalman-ordens`; `service.env` `root:kalman-ordens 640`; `/var/lib/kalman-ordens` do usuário do serviço. Banco: schema `kalmanordens`, usuário `kalman_ordens` sem DROP/DELETE; **SELECT só em `dbecotools.kalmanoperacao`** (conciliação diária), nada mais no dbecotools. Em `deploy/LEIA.md`. | 1, 8, 10 | — |
| P4 | **Leitor do /precos/atual** testado com as fixtures reais (`precos-atual-exemplo.json`, `precos-atual-vazio.json`) e contra um servidor HTTP: 404/400 = indisponível, falha de rede nunca mantém o preço antigo. | — | `internal/preco/preco_test.go` |

## 4d. Decisões a revisar quando a posição máxima passar de 1

| # | Decisão (v1) | Revisar quando |
|---|---|---|
| R1 | **Saída parcial vira total:** uma SAIDA com `qtd` menor que a posição zera a posição toda (com aviso no log). Reduce-only continua valendo. | **revisar quando posição máxima > 1** |
| R2 | **Fill tardio em operação ainda aberta** (só possível com qtd > 1) é tratado como fill normal, com alerta `ENTRADA_TARDIA`; a regra A3 de zerar vale para operação já encerrada. | **revisar quando posição máxima > 1** |

## 5. Configuração nova

| Chave | Inicial | Uso |
|---|---|---|
| `PRAZO_CONFIRMACAO_S` | 3 | F6 |
| `ESPERA_ERRO_S` | 2 | F14 |
| `ESPERA_ENCERRAMENTO_S` | 5 | A3 |
| `PRECOS_URL` | `http://172.16.110.51:9090` | B |
| `IDADE_MAX_PRECO_DIVERGENTE_S` | 60 | B5.d |
| `HTTP_ESPERA_S` | 5 | C2 |
| `HTTP_ENDERECO` | 127.0.0.1:9092 | P2 (só loopback) |
| `DB_DSN` | — | P1 |
| `TELEGRAM_TOKEN`, `TELEGRAM_CHAT` | — | P3 |
| `PRECOS_ATIVO` | WINFUT | B |

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
  `SEM_PRECO`, `BOOK_INVALIDO`, `RESULTADO_DESCONHECIDO` (HTTP 503).
- **Reenvio (C1/C2):** diante de 503, timeout ou erro de rede no POST, o gestor reenvia **o mesmo
  corpo** (mesmo `ts_decisao`). Resposta com `repetido=true` = o pedido original já tinha sido
  aceito; `estado_atual` traz a operação como está. `DUPLICADA` = corpo diferente para o mesmo par:
  bug no gestor. `GET /ordens/{id}` continua disponível para consulta.

## 7. Critérios de fase acrescentados

| Fase | Critério novo |
|---|---|
| F0 | **Todos os roteiros passando nos dois backends** (memória e MariaDB 10.11.15 em container); com banco real são obrigatórios 08, 09, 10, 11, 19, 22 e todos os de restart. Mais: a suíte inteira com `go test -race ./...` sem achado, numa máquina com cgo (na .51 ou equivalente; a máquina de desenvolvimento atual não tem cgo). Tudo pelo `scripts/teste-completo.sh`. Antes da HML. |

### F0 fechada — 2026-10-10, commit `f5bfb8a` (`f5bfb8aed7fde814eea8767c1663dd9303194929`)

Verificado sobre o commit, com a árvore limpa, pelo `scripts/teste-completo.sh`:

| Verificação | Resultado |
|---|---|
| Suíte inteira, Go local (1.26, sem cgo), backends memória e MariaDB `mariadb:10.11.15` (`sql_mode` da .51) | todos os pacotes `ok` |
| Suíte inteira com `-race` em `golang:1.24-bookworm` (Go 1.24.13, cgo), contra o `kalman-mariadb-teste` | todos os pacotes `ok`, nenhum DATA RACE |
| Roteiros com `-race` | 36/36 na memória, 36/36 no MariaDB (inclui 08, 09, 10, 11, 19, 22 e todos os de restart) |
| Camada PnT com `-race`, 10 rodadas seguidas | `ok` |
| Teste de mutação do executor | 9/9 mutações reprovadas |

Achado do `-race` durante a F0: o `handleLogon` do QuickFIX/Go lê a sequência do store sem trava
antes do `OnLogon`; um `SendToTarget` nesse intervalo corre com ele (`memoryStore` sem
sincronização). A produção já só envia depois do `OnLogon` (F5); o cliente de teste do PnT falso
não esperava e foi corrigido. Próxima fase: F1 (HML do PnT, Parte 11.2).

## 8. Pendências abertas

1. ~~Posição sem proteção em B5.c quando a saída não executa (16i).~~ **Resolvida por P5**: a
   saída parada é cancelada e a invariante recria a 1014 com o mesmo gatilho e limite.
2. **Verificações que usam preço durante a divergência.** Perda do dia (Parte 5) e stop furado
   (3.6) leem o `preco`; com o ticker errado leriam o contrato errado. Adotado até decisão: as
   duas ficam suspensas enquanto durar a divergência (a 1014 continua protegendo), e o alerta
   de B5.b já está ativo. Sem roteiro próprio.
3. **[PnT-13] CumQty no mass status.** O mass status de HML 2025 trouxe 39=2 com 14=0. Se for
   assim em produção, o fill não é recuperável pelo 150=I e o serviço marca DIVERGENTE. O
   cenário 08 supõe 14 correto.
4. **Valores de `status84`.** A regra é "≠ 0 = não negocia"; o 2 dos roteiros é ilustrativo.
5. **Gestor (Python):** precisa tratar `tardio`, as origens de A4, o atraso do
   `ENTRADA_ENCERRADA`, a recusa `SIMBOLO_DIVERGENTE` na SAIDA (B5.d: a operação continua aberta,
   protegida pela 1014) e o 503 (C2: reenviar o mesmo corpo; `repetido=true` na resposta).
6. ~~Schema de produção, sql_mode e usuário do systemd.~~ **Confirmados** (P6, P7).
7. **Ferramenta de bancada para a SwingTrade exploratória** (11.2, [PnT-9, PnT-10]): não existe.
   Por desenho fica fora do serviço. **Decidido:** fica para depois da rodada 2 da F1.
8. ~~HML com dados de ontem × marketdata da .53.~~ **Resolvida:** a HML de terça reproduz a sexta
   (`WINV26`, igual ao marketdata; casa até quarta). F1 em duas rodadas (`deploy/LEIA.md` 1.7):
   rodada 1 só protocolo, sem depender de preço; rodada 2 com o marketdata em replay da sexta
   (porta própria, ex. 9095), trocando só o `PRECOS_URL` — a detalhar.
9. ~~BodyLength errado vindo do PnT (F12).~~ **Fechada:** no log real de HML do PT, 9 e 10
   estão corretos em 100% das mensagens do PnT (F16; `pnt/logreal_test.go` confere byte a byte).
   O normalizador não é necessário; o detector `MENSAGEM_ILEGIVEL` fica como está. Os
   inconsistentes do RoE eram dos exemplos do documento, não do fio.

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
| 23 | C1 (reenvio idêntico: entrada, saída e saída depois de zerada) |
| 24 | C2 (503 e reenvio do mesmo pedido) |
| 25 | C1 (par repetido com conteúdo diferente → DUPLICADA + alerta) |
| 26 | P8 (1014 cancelada pelo PnT sem pedido nosso → recriada na hora; contador zera após 30 s) |
| 27 | P8 (PnT cancelando a 1014 em laço → PROTECAO_INSTAVEL e zeragem) |
| 28 | F11 (fill com CheckSum errado → aplicado, sem 35=3) |
| 29 | F12 (fill com BodyLength errado → MENSAGEM_ILEGIVEL + mass status; aplicado uma vez pelo reenvio) |
| 30 | F14 (entrada 7/7 → D/0 em 15 ms → fill: caso real do log, nada acontece) |
| 31 | F14 (1014 em 39=7 sem reativação → cancelada e recriada, PROTECAO_EM_ERRO) |
| 32 | F14 (entrada em 39=7 sem reativação → cancelada, ENTRADA_EM_ERRO) |
| 33 | F14 (saída em 39=7 sem reativação → cancelada, SAIDA_EM_ERRO; nova saída do repreço seguinte) |
| 34 | F17 (cancel da 1014 em erro → "Robot Order not found" → recriada sob a P8) |
| 35 | F17 (cancel rejeitado por outro motivo; mass status: viva em 39=7 → PROTECAO_EM_ERRO_PRESA + BLOQUEAR) |
| 36 | F17 (cancel rejeitado por outro motivo; mass status: ausente → recriada sob a P8) |
