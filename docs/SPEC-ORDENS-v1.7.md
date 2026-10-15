# Spec de Implementação — `kalman-ordens` — v1.7

Revisão da [SPEC-ORDENS-v1.6.md](SPEC-ORDENS-v1.6.md). Lista **só o que mudou**; onde a v1.7 não
diz nada, vale a v1.6.

Tema: **tela de admin — ordens no PnT e ações de operação** (contrato oficial do responsável,
15/10; o lado do Kalman implementa igual). Tudo em 127.0.0.1.

**Revista pela [v1.8](SPEC-ORDENS-v1.8.md)** (15/10): habilitação do usuário pelo admin; CLI
`usuario` preserva os limites.

Data: 2026-10-15.

---

## 1. Ordens no PnT (C)

**`GET /pnt/ordens`**

| # | Regra | Roteiro |
|---|---|---|
| C1 | Dispara um **35=AF (585=9) por conta** — as das operações do dia e as dos usuários habilitados (as mesmas do mass status do logon, v1.2 U6) —, ReqID `KM-AD-<ms>-<n>`, e espera as respostas. Uma conta está respondida com `912=Y` (LastRptRequested), com o `911` (TotNumReports) atingido ou com um 35=j ligado ao AF pelo 379 (380=101: sem ordens). Fecha quando todas responderam ou em **`CONSULTA_PNT_ESPERA_S` = 5 s**. Sem sessão logada → 503 `{"erro": "SEM_SESSAO"}`. | 64 |
| C2 | **Só leitura**: as respostas são gravadas (`kalmanordemevento`, ligadas à ordem quando ela existe: aparecem no histórico da operação), mas **não mexem no estado** — nem posição, nem ordens, nem alerta. A conciliação automática do logon continua a mesma (ReqID `KM-MS-…`). | 64 |
| C3 | Resposta: `{consultado_em, cache, completo, contas_sem_resposta, contas: [conta...], ordens: [...], outros: {conta: N}, posicao: [{simbolo, qtd, preco_medio}]}` (`completo` e `contas_sem_resposta`: C8). `consultado_em` = quando os AF saíram. | 64 |
| C4 | `ordens`: cada ordem **`KM-`** do PnT: `{origem: "KM", operacao_id, modulo, papel (E/P/S), simbolo, lado (C/V), qtd (38), preco (44), gatilho (20828; nulo fora da 1014), cumqty (14), ordstatus (39), ordstatus_texto, orderid (37), clordid (11), estado_servico (o estado da ordem no serviço; nulo se não há registro), confere, divergencia}`. `operacao_id`/`modulo`/`papel` vêm do serviço ou, sem registro, do ClOrdID. **Diverge** (`confere: false`, `divergencia` com os motivos separados por `; `): sem registro no serviço; viva no PnT e encerrada no serviço (ou o contrário); `cumqty` ≠ o executado no serviço; e, viva dos dois lados e sem pedido em curso no serviço, quantidade, gatilho ou preço diferentes. **Com todas as contas respondidas**, uma ordem viva no serviço (criada antes da consulta) que não veio do PnT entra com os dados do serviço, `ordstatus: null` e `divergencia: "viva no servico e ausente no PnT"`; com o tempo esgotado, a ausência não é acusada. | 64 |
| C5 | `outros`: ordens de outros sistemas na mesma conta (sem `KM-`), **só contadas**, por conta (PT/L&S na próxima versão). | 64 |
| C6 | `posicao`: pelo que o PnT mostra das ordens KM — por operação (o ClOrdID diz qual), a posição líquida (Σ lado × cumqty); das operações com posição, por símbolo, a soma e o preço médio da entrada (ponderado). | 64 |
| C7 | **Intervalo mínimo de 10 s** (`CONSULTA_PNT_INTERVALO_S`, desde o `consultado_em` da última): dentro dele, devolve a última consulta com `cache: true` (fora dele, `cache: false`), sem AF. Pedidos durante uma consulta em curso recebem o resultado dela. | 64 |
| C8 | **`completo`** (aprovado 15/10, aditivo): `true` quando todas as contas responderam, `false` quando a consulta fechou pelo tempo; **`contas_sem_resposta`**: as contas que não responderam (`[]` se completa). Com `completo: false`, a ausência de ordens vivas no PnT não é acusada (C4). No cache, os valores da consulta guardada. | 64 |

## 2. Ações de admin (A)

| # | Regra | Roteiro |
|---|---|---|
| A1 | **`POST /operacoes/{id}/encerrar[?modulo=]`** e **`POST /operacoes/{id}/cancelar-entrada[?modulo=]`**, corpo `{"motivo": "...", "por": <uid do admin>}` (os dois obrigatórios; campo desconhecido → 400). Toda chamada é gravada como pedido (v1.6 P1; rota `ENCERRAR`/`CANCELAR_ENTRADA`); aceita, publica **`ADMIN_ACAO`** (`acao`, `motivo`, `por`) na linha do tempo da operação antes do efeito. **Encerrar** = a saída normal (Parte 3.5: cancela a 1014, sai com a 1031, `ZERADA`) com **origem `ADMIN`** e o motivo do admin; com a entrada pendente, cancela a entrada primeiro (como a saída do gestor). **Cancelar a entrada**: só com a entrada pendente e **sem posição**; o fluxo segue o do timeout (39=4 → `ENTRADA_ENCERRADA motivo=CANCELADA` depois de `ESPERA_ENCERRAMENTO_S`); com o cancel já em curso, aceito com `situacao: "JA_CANCELANDO"` e nada de novo. | 63 |
| A2 | **`POST /parada`** e **`POST /retomar`** aceitam e gravam **`por`** (opcional: a CLI não manda) além do `motivo`; `por` fica na parada (`GET /saude`, `parada.por`) e no texto dos alertas `PARADA`/`RETOMADA`; as duas publicam `ADMIN_ACAO` (`acao` `PARADA`/`RETOMAR`, `modo`, `origem`, `motivo`, `por`) e são gravadas como pedido (rotas `PARADA`/`RETOMAR`). | 63 |
| A3 | **Recusas** (nenhuma gera alerta nem conta para a parada por limite): `CAMPO_INVALIDO` (motivo ou por ausente, corpo inválido), `OPERACAO_INEXISTENTE` (não existe hoje no módulo), `OPERACAO_ENCERRADA`, `OPERACAO_EM_SAIDA` (a saída já começou, qualquer origem), `SEM_POSICAO` (encerrar sem posição e sem entrada pendente), `SIMBOLO_DIVERGENTE` (encerrar com a posição em contrato não vigente e sem preço recente dele, como a saída do gestor), `SEM_ENTRADA_PENDENTE`, `COM_POSICAO` (cancelar a entrada que já executou em parte: usar encerrar). | 63 |
| A4 | **Resposta** (complemento do contrato, 15/10): `encerrar`, `cancelar-entrada`, `/parada` e `/retomar` respondem sempre `{"aceito": true\|false, "codigo": "<CODIGO>", "motivo": "<texto>"}` (mais os campos de antes: `operacao_id`, `acao`, `parada`...). Aceito: `codigo` = `ENCERRANDO`, `CANCELANDO`, `JA_CANCELANDO`, `PARADA_ATIVA` ou `RETOMADO`; recusado: os de A3 (na parada, `CAMPO_INVALIDO` para modo inválido). `/parada` e `/retomar` recebem `origem` (a tela manda `ADMIN`), `motivo` e `por`. | 63 |

**Origem `ADMIN`** entra na lista de origens da saída (v1.1 A4): o gestor trata como operação
fechada pelo serviço, com o motivo do admin. `ADMIN_ACAO` é evento novo no `GET /eventos`.

A lista do dia (v1.6 P3) continua com as recusas de `ORDENS` e `STOP` só; as ações de admin
recusadas ficam no histórico da operação (`pedidos`) e na tabela de pedidos.

## 3. Configuração

| Chave | Inicial | Uso |
|---|---|---|
| `CONSULTA_PNT_ESPERA_S` | 5 | C1 |
| `CONSULTA_PNT_INTERVALO_S` | 10 | C7 |

Sem migração nova.

## 4. Pendência

1. ~~Consulta incompleta.~~ **Fechada por C8** (`completo`, `contas_sem_resposta`).

## 5. Cobertura

| Roteiro | Cobre |
|---|---|
| 63 | A1–A3: recusas, encerrar (ADMIN_ACAO, cancel da 1014, saída, ZERADA origem ADMIN), em saída, encerrada, pedidos e eventos no histórico; cancelar a entrada (ADMIN_ACAO, cancel, ENTRADA_ENCERRADA, JA_CANCELANDO), COM_POSICAO; parada e retomada com motivo e por, e sem por (CLI) |
| 64 | C1–C7: consulta completa (confere, sem registro, outros, posição), só leitura (nada enviado, nenhum alerta, estado intacto), cache, gatilho divergente, ausente no PnT, 35=j 380=101, tempo esgotado, sem sessão |
