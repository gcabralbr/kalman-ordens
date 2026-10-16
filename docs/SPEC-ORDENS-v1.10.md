# Spec de Implementação — `kalman-ordens` — v1.10

Revisão da [SPEC-ORDENS-v1.9.md](SPEC-ORDENS-v1.9.md). Lista **só o que mudou**; onde a v1.10 não
diz nada, vale a v1.9.

Tema: **escala** — decisões do responsável sobre a seção 6 do
[relatório de carga](CARGA-2026-10-15.md) (15/10): itens 1, 2, 3 e 5 implementados; o 4 (POST em
lote por sinal) tem o contrato fechado na seção 6 — implementado no Kalman (KalmanWIN `6b74789`,
chave `KALMAN_ORDENS_SINAIS`) e no serviço (seção 6.6).
Segunda rodada de decisões (15/10, depois do teste com fsync): balde separado da entrada com
reserva para a proteção e a saída, validade da entrada na fila (P4, P5) e conciliação por 911/912
(seção 3, C6). Resultado do teste
de carga depois da mudança: [CARGA-2026-10-15.md](CARGA-2026-10-15.md) seção 7.

Data: 2026-10-15.

---

## 1. Prioridade (P)

| # | Regra | Teste |
|---|---|---|
| P1 | **Entrada do motor em três filas, prioridade estrita**: **P0** = o que vem do PnT (ERs, 35=9, 35=j, 35=3, eventos de sessão), o tick, consultas e paradas; **P1** = pedidos de SAIDA, ajuste do stop, encerrar/cancelar-entrada, habilitação, consulta ao PnT; **P2** = pedidos de ENTRADA. O laço tira sempre da mais alta que tiver item. A P0 tem capacidade de ~1 milhão de itens: a goroutine da sessão FIX nunca espera o motor. | `internal/motor` `TestFilasPorPrioridade`; roteiro 67 |
| P2 | **Envio ao PnT por prioridade**, depois do commit da volta: **1014 (nova, recriada, replace) > cancel, 1031 de saída e o replace dela, mass status > 1031 de entrada**; dentro da classe, a ordem de decisão. | `TestEnvioPorPrioridade`; roteiro 67 |
| P3 | **Prazos de confirmação contados até a chegada à sessão**: a camada de sessão carimba a chegada de cada mensagem (pelos ClOrdID 11 e 41) antes de ela entrar na fila; `SEM_CONFIRMACAO` (`PRAZO_CONFIRMACAO_S`) e `CANCEL_SEM_RESPOSTA` só disparam se nenhuma resposta **chegou** no prazo, mesmo que ainda não tratada (no `CANCEL_SEM_RESPOSTA` depois do 39=6, só uma chegada posterior a ele conta). Fila cheia deixa de gerar alerta falso e mass status em massa. | roteiro 69 |
| P4 | **Limitador de taxa da sessão** (token bucket no relógio do motor): `FIX_TAXA_MSG_S` mensagens/s, rajada `FIX_RAJADA_MSG` — **conservador até [PnT-19]: 100/s, rajada 100**. **Só a 1031 de entrada espera**; 1014, cancel, saída e mass status saem na hora (consomem tokens; o saldo pode ficar negativo e as entradas seguintes esperam) — reserva para a proteção. A entrada que espera a taxa conta como viva (limites, posição pendente); o timeout da entrada (`TIMEOUT_ENTRADA_S`) conta do envio real. Cancelada enquanto espera (saída do gestor, parada, admin), **sai da fila e nunca vai ao PnT**: `ENTRADA_ENCERRADA motivo=CANCELADA` na hora. | roteiro 68 |
| P4' | **Balde separado da entrada (decisão de 15/10)**: a entrada consome de um balde próprio que enche a `FIX_TAXA_MSG_S − FIX_RESERVA_SAIDA_MSG_S` (100 − 30 = **70/s**), rajada `FIX_RAJADA_MSG`, e só sai com saldo. 1014, cancel, saída e mass status **não passam pelo balde da entrada**: a reserva (30/s) é deles e a rajada de entradas nunca a esgota. Substitui o saldo negativo da P4. | roteiro 68 |
| P5 | **Validade da entrada na fila da taxa**: a entrada que não saiu em `ENTRADA_TTL_FILA_S` (5 s) desde a decisão é **descartada** — nunca vai ao PnT atrasada: ordem `FALHA_ENVIO` (motivo `EXPIRADA_NA_FILA`), operação `ENCERRADA_SEM_ENTRADA`, evento `ENTRADA_ENCERRADA motivo=EXPIRADA_NA_FILA`. Métrica `entradas_expiradas_fila_total`. No POST em lote (seção 6) a validade conta do fechamento do sinal. | roteiro 70 |

## 2. Banco em lote (B)

| # | Regra | Teste |
|---|---|---|
| B1 | **Uma transação por volta**: o motor trata até 256 itens ou 20 ms das filas, acumula tudo o que mudou e grava num `GravarLote` — INSERT de várias linhas por tabela (operação, ordem, evento FIX, publicado, estado, sessão, pedido HTTP), em blocos de 200 linhas. **As mensagens ao PnT só saem depois do commit** ("gravar antes de enviar", P1 da v1.1). As respostas HTTP e os `Fazer` síncronos também só saem depois do commit e do envio. | `TestEnvioDepoisDoCommit`; `TestContratoLote` (memória e MariaDB) |
| B2 | **IDs de operação e ordem atribuídos pelo motor** (a partir do maior ID do banco na subida): o evento FIX já aponta para a ordem antes do commit. Operação/ordem **nova** = INSERT (colisão na chave única é erro, nunca a atualização de outra linha); **já gravada** = upsert pelo ID, **só se mudou** desde a última gravação (cópia comparada) — sem gravações redundantes. | `TestContratoLote` |
| B3 | **Deduplicação de fill em memória**: os pares (37, 17) do dia são carregados na subida (`ChavesFillDoDia`); o fill repetido é descartado no motor (antes, pela chave única do banco, uma ida por mensagem). No banco, `INSERT IGNORE` com a chave única de rede. | roteiros 09, 50 |
| B4 | **Habilitação e `acusuario` em memória** (`cacheUsuarios`): carregados na subida (todas as habilitações e a configuração do GQ dos habilitados, numa consulta só), **relidos a cada `CACHE_USUARIOS_S` = 30 s** em segundo plano e **invalidados pela rota do serviço** que muda a habilitação (`POST /usuarios/{uid}/habilitacao`). Uma recarga que começou antes de uma invalidação é descartada. Uid fora do cache é lido na hora e guardado. **A CLI grava direto no banco: o efeito dela chega em até 30 s.** | `TestCacheUsuarios`; roteiros 37–43 e 65 |
| B5 | **Pedido HTTP gravado no lote** (não mais pela goroutine HTTP, que disputava as 4 conexões do pool com o motor). | roteiro 62 |
| B6 | **Falha ao gravar o lote**: nova tentativa na hora; falhou de novo → alerta crítico `ERRO_INTERNO` ("gravação no banco falhou"), **o envio segue** (a proteção não espera o banco, como antes da v1.10) e o que não gravou vai na volta seguinte; resolve na primeira gravação boa. | — |

Consequência para o contrato (rara): a falha de envio da entrada (sessão caiu entre a decisão e o
envio) ainda vira a recusa `ENVIO_FALHOU` quando acontece na mesma volta; quando a entrada já
tinha sido aceita e esperava a taxa, o gestor recebe `ENTRADA_ENCERRADA motivo=REJEITADA` (e
`RECUSA_PNT` com o texto).

| B7 | **Perda do dia incremental**: o resultado do dia (limite `PERDA_DIARIA`, conferido a cada entrada) era recalculado sobre todas as operações a cada entrada — O(N²) numa rajada, 40% da CPU do motor com 1.000 entradas (perfil). Agora: aberto = último × Σposição − Σ(médio × posição), com os agregados ajustados a cada fill (a parte da operação sai antes e volta depois). Mesma conta. | `TestResultadoDoDiaAgregado`; roteiro 15 |

## 3. Mass status do logon (C5)

O mass status da conciliação (logon, `SEM_CONFIRMACAO`, mensagem ilegível, 35=3) passa a ser
pedido **só para as contas com ordem viva ou posição** (pelo estado carregado do banco), não para
todos os habilitados. Sem nenhuma, nada é pedido e a conciliação fecha no silêncio. **O silêncio
(`MASS_STATUS_SILENCIO_S`) conta do envio real do último AF e da última resposta de mass status
chegada à sessão** (antes: da decisão de pedir e da última resposta tratada — com o despacho ou a
fila lentos, a conciliação fechava antes de as respostas serem vistas e marcava ordens vivas como
`DESCONHECIDA`). A consulta do
admin (`GET /pnt/ordens`, v1.7 C1) continua com as contas das operações do dia e dos habilitados.
Roteiros: `_comum.fix` (INICIO sem AF), 45 (logon sem ordens: nenhum AF), 12, 18, 20 (posição
aberta antes do restart).

**C6 — conciliação por 911/912 (decisão de 15/10).** O PnT informa em cada resposta de mass
status (35=8 150=I) o total da conta (911 `TotNumReports`) e a última (912 `LastRptRequested`=Y); a
recusa do pedido vem em 35=j (380=101, 379 = o 584 do pedido). O serviço controla **por conta**: a
conta fica respondida quando chega a 912=Y, quando o número de respostas chega ao 911, ou com a
recusa 35=j (marcada feita, com alerta como antes). A conciliação fecha **quando todas as contas
pedidas responderam** — sem esperar o silêncio. Se alguma resposta trouxe 911/912, o silêncio deixa
de valer: espera-se até `MASS_STATUS_ESPERA_MAX_S` (30 s) do envio; vencido, a conciliação fecha e
**as contas que não responderam não são julgadas** (as ordens delas continuam como estão, sem
`DESCONHECIDA`; o texto do `CONCILIACAO` lista as contas). Sem 911/912 em nenhuma resposta, vale o
silêncio (como até aqui). Roteiros 71 (fechamento por 912/911, conta sem resposta não julgada) e 20
(1014 por último com 912=Y).

## 4. Configuração

| Chave | Inicial | Uso |
|---|---|---|
| `FIX_TAXA_MSG_S` | 100 | P4 (conservador até [PnT-19]) |
| `FIX_RAJADA_MSG` | 100 | P4 (rajada do balde da entrada) |
| `FIX_RESERVA_SAIDA_MSG_S` | 30 | P4': parte da taxa reservada a 1014, cancel, saída e mass status; o balde da entrada enche a `FIX_TAXA_MSG_S` − esta |
| `ENTRADA_TTL_FILA_S` | 5 | P5 |
| `MASS_STATUS_ESPERA_MAX_S` | 30 | C6: espera máxima da conciliação quando o PnT manda 911/912 |
| `CACHE_USUARIOS_S` | 30 | B4 |
| `FIX_STORE_SYNC` | `Y` | fsync do store FIX a cada mensagem (o padrão do QuickFIX/Go, como até aqui). Com `N`, sem fsync: ver o relatório, seções 7.2 e 8. **Decisão de 15/10: `N` em produção, depois dos testes de SIGKILL e de queda da máquina na HML** (com o banco em `innodb_flush_log_at_trx_commit=1` o custo é nenhum — seção 8) |
| `DEBUG_PPROF` | — | `1` expõe `/debug/pprof/` no HTTP local (diagnóstico; o teste de carga usa) |
| `SINAL_ESPERA_PARTES_MS` | 500 | §6: fechamento do sinal em partes |
| `SINAL_MAX_ITENS_PARTE` | 1000 | §6 |
| `SINAL_MAX_PARTES` | 10 | §6 |

Migração **0008** (§6): `kalmanordemoperacao.sinal_entrada` e `sinal_saida`.

## 5. Métricas novas

`lote_segundos`, `lote_linhas`, `envio_espera_segundos{prioridade}` (da decisão ao envio: commit e
taxa), `fila_tamanho_p0|p1|p2`, `envios_aguardando_taxa`, `entradas_expiradas_fila_total`; `fila_espera_segundos{origem}` com
`http_entrada` e `http_saida`; `banco_segundos{operacao="GravarLote"}`. §6: `sinais_partes_total{codigo}`,
`sinais_itens_total{papel,resultado}`, `sinais_fechados_total{como}` (`TODAS_AS_PARTES`, `PRAZO`),
`sinal_itens_aceitos` (histograma do tamanho da rajada). `alertas_total` passa a
contar só os alertas que saem (um crítico já ativo não conta de novo).

## 6. Contrato — POST em lote por sinal (fechado em 15/10; implementado nos dois lados)

O Kalman manda **as entradas e/ou as saídas de um sinal** em lote; o serviço valida cada item como
no `POST /ordens`, **ordena de forma rotativa** (deslocamento áureo) e registra a posição de cada
operação na rajada.

### 6.1 Requisição

```
POST /sinais
{
  "sinal_id": "KALMAN-20261015-093712-0042",   texto, até 64 [A-Za-z0-9._:-], único por módulo
  "modulo": "KALMAN",                           opcional (ausente = KALMAN)
  "ts_decisao": "2026-10-15T09:37:12.481-03:00",
  "parte": 1, "partes": 4,                      1..partes; partes de 1 a 10
  "entradas": [
    {"operacao_id": 900000001, "uid": 1450, "lado": "C", "qtd": 1, "stop_pts": 500, "motivo": "SINAL"}
  ],
  "saidas": [
    {"operacao_id": 900000377, "uid": 1450, "lado": "V", "motivo": "SINAL_OPOSTO"}
  ]
}
```

- **Limite: 1.000 itens (entradas + saídas) por requisição; até 10 partes; 10.000 itens por
  sinal.** Corpo até 1 MB. Um sinal maior que 1.000 vai em partes com o mesmo `sinal_id`
  (`parte`/`partes`), mandadas em seguida.
- Cada item tem os campos do `POST /ordens` do papel correspondente (sem `papel`, que vem da lista;
  `ts_decisao` é o do sinal). Campos desconhecidos → `CAMPO_INVALIDO` no item.

### 6.2 Processamento

1. Cada parte é validada **na hora**, item a item, como o `POST /ordens` (entrada: habilitação,
   campos, duplicada, parada, sessão, conta, limites do uid, preço — **uma leitura de preço para o
   sinal**; saída: reduce-only, posição, uid da operação). A resposta da parte sai depois do commit.
2. O sinal **fecha** quando chegam todas as `partes`, ou `SINAL_ESPERA_PARTES_MS` (500 ms) depois da
   primeira; uma parte que chega depois do fechamento é recusada inteira (`SINAL_FECHADO`). **Parte
   que já chegou ao serviço e ainda espera o motor (fila ou em tratamento) segura o prazo** (até
   30 s): o teste de carga mostrou a 4ª parte de 1.000 recusada por ter esperado as outras três.
3. Fechado o sinal, as **entradas aceitas** de todas as partes são ordenadas por **uid crescente**
   (empate: `operacao_id`) e **giradas**: com N aceitas e k = o contador de sinais do módulo
   (persistente entre dias, `kalmanordemestado` `sinal_k:<modulo>`; sobe a cada sinal que fecha com
   pelo menos um item novo aceito — entrada ou saída, para as saídas também girarem),
   `deslocamento = ⌊k · 0,6180339887 · N⌋ mod N`; a fila de envio começa na posição `deslocamento`
   da lista ordenada e dá a volta. **As saídas aceitas** do sinal: a mesma regra, com o seu N (e o
   mesmo k).
4. O envio segue essa ordem: saídas na fila de saída (P1; cancel da 1014, depois a 1031), entradas no
   balde da entrada (P4). **A validade na fila (`ENTRADA_TTL_FILA_S`) conta do fechamento do sinal.**
   A proteção de cada posição continua imediata.
5. Entrada aceita que é cancelada antes de o sinal fechar (saída do gestor, parada, cancelar-entrada
   do admin) encerra na hora (`ENTRADA_ENCERRADA motivo=CANCELADA`), nunca vai ao PnT e **fica fora
   da rajada** (não conta no N).

### 6.3 Resposta (por parte)

```
200
{
  "sinal_id": "...", "parte": 1, "partes": 4,
  "aceito": true, "codigo": "LOTE_ACEITO", "motivo": "...",
  "fechado": false,                      true quando esta parte fechou o sinal
  "criterio": "ROTATIVA_AUREA", "k": 1873, "deslocamento": null,   (preenchido com o sinal fechado)
  "resultados": [
    {"operacao_id": 900000001, "papel": "ENTRADA", "aceito": true, "codigo": null, "clordid": "KM-E-900000001-1",
     "preco": 130020, "posicao": null, "tamanho": null},
    {"operacao_id": 900000002, "papel": "ENTRADA", "aceito": false, "codigo": "UID_NAO_HABILITADO", "motivo": "..."}
  ]
}
```

- `posicao`/`tamanho`/`deslocamento` vêm preenchidos na resposta da parte que fecha o sinal (num
  sinal de uma parte, sempre); nas outras, `null` — a posição chega pelo evento `ENTRADA_ENVIADA` e
  fica no `GET /ordens/{id}`.
- Recusas do lote inteiro: 400 `CAMPO_INVALIDO` (JSON, `sinal_id`, `parte`/`partes`, lista vazia),
  `LIMITE_LOTE` (mais de 1.000 itens na parte, mais de 10 partes, mais de 10.000 no sinal),
  `SINAL_FECHADO`, `DUPLICADA` (mesmo `sinal_id` e `parte` com corpo diferente). Idempotência: mesmo
  `sinal_id` e `parte` com o mesmo corpo → a resposta original com `repetido: true`. 503
  `RESULTADO_DESCONHECIDO` como no `POST /ordens` (o motor segue; reenviar o mesmo corpo).
- Item que repete um `(modulo, operacao_id, papel)` já recebido: **com o mesmo conteúdo, o resultado
  original com `repetido: true`** (como o `POST /ordens`; é o que acontece no reenvio de uma parte
  depois de um restart, quando o serviço já não lembra do sinal) e fora da rajada; com outro
  conteúdo, `DUPLICADA` só nele (alerta `DUPLICADA_DIFERENTE`).
- No resultado de uma saída, `posicao` é a posição na rajada (não a posição em contratos do
  `POST /ordens`).

### 6.4 Eventos e registro

- **`ENTRADA_ENVIADA`** quando a 1031 de entrada sai ao PnT: `{operacao_id, modulo, uid, sinal_id,
  posicao, tamanho, clordid, enviada_em}`. **`SAIDA_ENVIADA`** quando a primeira mensagem da saída
  do sinal sai (o cancel da 1014, ou a 1031): o mesmo, com a posição entre as saídas. A entrada
  avulsa (`POST /ordens`) também publica `ENTRADA_ENVIADA` (`sinal_id`, `posicao` e `tamanho`
  nulos); a saída avulsa não publica `SAIDA_ENVIADA`. `enviada_em` no formato do histórico
  (`-03:00`, milissegundos).
- **Histórico de pedidos (acréscimo do Kalman, 15/10):** cada item fica em `pedidos[]` da operação
  com **`rota` = `"POST /sinais"`** (exatamente), `papel`, `aceito`, `codigo`, `http_status` 200,
  `corpo` = a parte reduzida ao item (`sinal_id`, `modulo` se veio, `ts_decisao`, `parte`,
  `partes`, `entradas`/`saidas` com o item só) e `resposta` = o resultado do item mais `sinal_id`,
  `parte`, `partes`. O item recusado que não virou operação aparece na lista do dia como
  `RECUSADA` (com `rota`, `lado` e `qtd` do item). A recusa do lote inteiro (400) vira uma linha sem
  operação (lista do dia: `RECUSADA`); o 503 e o reenvio repetido também ficam registrados (sem
  operação; fora da lista do dia).
- Por operação (migração 0008): a posição da **entrada** e a da **saída**, cada uma
  `{sinal_id, k, posicao, tamanho, deslocamento, criterio}` (colunas JSON `sinal_entrada` e
  `sinal_saida`). No `GET /ordens/{id}`, no histórico (`operacao`) e na lista do dia:
  `"sinal": {"entrada": {...} | null, "saida": {...} | null}`, ou `"sinal": null` na operação
  avulsa; com o sinal ainda aberto, `k`, `posicao`, `tamanho` e `deslocamento` nulos.
- `POST /ordens` continua para entrada e saída avulsas (sem rajada: ordem de chegada).

### 6.5 Configuração

| Chave | Inicial | Uso |
|---|---|---|
| `SINAL_ESPERA_PARTES_MS` | 500 | fechamento do sinal em partes |
| `SINAL_MAX_ITENS_PARTE` | 1000 | limite por requisição |
| `SINAL_MAX_PARTES` | 10 | |

### 6.6 Implementação no serviço e teste

Rota `POST /sinais` (corpo até 1 MB; o resto como o `POST /ordens`: `HTTP_ESPERA_S`, 503). A parte
com saída entra na fila P1 do motor; só com entradas, na P2. **A parte é tratada em fatias de 250
itens**, cada uma numa volta do motor: entre elas o motor atende a P0 (fills, 1014), e uma parte de
1.000 não segura a proteção. O que a entrada confere varrendo as operações do dia (posição e
entradas pendentes, ordem sem confirmação, preço do resultado do dia) é calculado uma vez por fatia,
e o `DUPLICADA` de outro dia é uma consulta por fatia (`OperacoesExistentes`), não uma por item —
1.000 entradas com 3.000 operações no dia: ~700 ms → ~65 ms no motor. Um reenvio da parte durante o
tratamento dela recebe a resposta da original (`repetido`).

Envio (vale também para o `POST /ordens`): **cada volta envia no máximo 250 mensagens que não são
1014** (cancel, saída, entrada; o resto sai na volta seguinte, que o motor agenda), e **a entrada
só sai com a P0 curta** (até 200 itens esperando o motor): mais entrada com fills na fila só poria
mais fills na frente das 1014. A entrada que espera por isso conta na validade da fila (P5) como a
que espera a taxa. Os sinais ficam em memória (uma hora
depois de fechados, para o reenvio repetido); o que vai ao banco é a rajada de cada operação, o
contador k e o histórico de pedidos. Num sinal com entradas e saídas (o Kalman não manda), a
resposta traz `deslocamento` das entradas e `deslocamento_saidas`.

Roteiros: 72 (rajada de entradas, item recusado, reenvio repetido, DUPLICADA, item repetido de
outro sinal, 503 e reenvio, histórico e lista do dia), 73 (partes, fechamento pelo prazo,
`SINAL_FECHADO`, `LIMITE_LOTE`, `CAMPO_INVALIDO` no lote e no item), 74 (rajada de saídas,
`SAIDA_ENVIADA` na primeira mensagem, entrada de sinal aberto cancelada antes do fechamento).
`TestDeslocamento`, `TestSinalEnvioNoFechamento` (nada sai antes do fechamento; a validade na fila
conta dele), `TestContratoLote` (rajada gravada e relida, memória e MariaDB). Teste de carga:
`KALMAN_CARGA_SINAIS=1` ([relatório](CARGA-2026-10-15.md), seção 9).

## 7. Monitor ao vivo da sessão (contrato oficial da tela de admin, 16/10)

`GET /sessao/ao-vivo?desde=<seq>[&limite=500]` (só 127.0.0.1, como todo o HTTP do serviço). **Sem
banco e sem a fila do motor**: tudo da memória, responde mesmo com o motor ocupado.

- **Buffer das últimas 2.000 mensagens FIX** que passaram no fio, de aplicação e de sessão (A, 0, 1,
  2, 4, 5, 3...), nos dois sentidos, como o QuickFIX/Go as entregou ao log da sessão (reenvios e
  gap fills incluídos). Cada uma: `seq` (interno, crescente desde a subida do processo), `em`
  (RFC3339 `-03:00`, milissegundos), `direcao` (`ENV` = do serviço ao PnT, `REC` = do PnT),
  `msgtype`, `campos` (as tags presentes entre 11, 37, 39, 150, 54, 38, 44, 31, 32, 58, como texto),
  `operacao_id` e `uid` quando o 11 (ou o 41) é um ClOrdID KM (`uid` nulo se a operação não é deste
  processo), `fix_raw` (senha 554/925 mascarada, SOH como `|`).
- Resposta: `{mensagens, ultimo_seq, saude}`. `mensagens` = as de `seq > desde`, as mais antigas
  primeiro, no máximo `limite` (padrão 500, máximo 2.000); `desde` anterior ao buffer começa na mais
  antiga que restou. O cliente pede de novo com `desde = ultimo_seq` recebido. `desde`/`limite`
  inválidos: 400.
- `saude`: `logada`; `logada_desde` (o último logon aceito; nulo se não logada);
  `ultima_recebida_em`, `ultima_enviada_em`; `seq_entrada`, `seq_saida` (o último MsgSeqNum visto
  em cada sentido); `msgs_min` (mensagens nos últimos 60 s, os dois sentidos); `fila` (itens nas
  filas do motor); `parada` (a do `GET /saude`, vista no último tick: até 100 ms de atraso);
  `latencia_aceite_p50_ms` / `latencia_aceite_max_ms` (no buffer, de cada 35=D enviada à primeira
  35=8 do mesmo 11 com 39 = 0, 1 ou 2; nulos sem par).

- **`ritmo`** (acréscimo de 16/10): os últimos 300 s, um item por segundo até o atual (o mais
  antigo primeiro; segundo sem mensagem = zeros), contadores em memória:
  `{t, enviadas:{entrada, protecao, saida, cancel, replace, af}, recebidas:{aceite_A, aceite_0,
  fill, cancelada, replace_ok, rejeicao, erro, status, outras, cancel_reject, reject_sessao, bmr_j},
  sessao:{test_request:{env, rec}, resend:{env, rec}, seq_reset:{env, rec}, logon:{env, rec},
  logout:{env, rec}},
  heartbeat_env, heartbeat_rec}`. `t` = início do segundo (RFC3339 `-03:00`). Enviadas: 35=D pelo
  papel do ClOrdID (E, P, S), 35=F, 35=G, 35=AF. Recebidas: 35=8 pelo 150 — A, 0, F, 4 (cancelada),
  5 (replace_ok), 8 (rejeicao), 7 (erro), 6/E/I/D (status), qualquer outro (outras); 35=9, 35=3,
  35=j. Sessão: 35=1, 2, 4, A, 5 em cada sentido; 35=0 nos heartbeats. (O 35=3 que a nossa
  biblioteca envia, raro, não tem contador no ritmo; aparece em `mensagens`.)

Implementação: `internal/aovivo`; a camada de sessão manda cada mensagem do fio
(`pnt.ConfigSessao.Espelho`, no espião do log da sessão). Testes: `internal/aovivo` (buffer
circular, `desde`/`limite`, senha mascarada, operação e uid, latência, `msgs_min`, ritmo: cada
tipo no seu contador e a janela de 300 s); roteiro 75.

## 8. Limites globais e por usuário (L, 16/10)

Os limites da Parte 5 são **globais (soma de todos os usuários)**; os padrões até aqui (posição 1,
uma entrada por minuto, 60 s entre entradas, 10 mensagens/min, 15/dia, parada em 3 recusas) eram
de um usuário só. Na HML em 16/10 o `LIMITE_MENSAGENS_MIN=10` recusou uma entrada (#234070,
`ORDENS_POR_MINUTO`) por causa de uma saída presa (L3).

| # | Regra | Teste |
|---|---|---|
| L1 | **Globais, novos padrões** (todos configuráveis): `LIMITE_POSICAO_MAX` 200, `LIMITE_ENTRADAS_MIN` 400, `LIMITE_INTERVALO_ENTRADAS_S` 0 (desligado), `LIMITE_MENSAGENS_MIN` 3.000 (recusa só entradas, nunca saídas), `LIMITE_ENTRADAS_DIA` 3.000, `LIMITE_RECUSAS_PARADA` 50. Uma rajada de 190 usuários passa. | `TestPadroesLimites`; roteiro 78 |
| L2 | **Por usuário (novos)**, para o laço de reentrada de um uid: `LIMITE_ENTRADAS_MIN_UID` (3 entradas aceitas do uid no último minuto, em qualquer sentido) → `ENTRADAS_POR_MINUTO_UID`; `LIMITE_INTERVALO_UID_S` (30 s desde a última entrada do uid **no mesmo sentido**) → `INTERVALO_MINIMO_UID`. Barram só o uid: **não contam para a parada automática** e não emitem alerta. 0 desliga. | roteiro 76 |
| L2' | **Virada de mão**: a entrada do lado oposto ao da última operação do uid, até `LIMITE_VIRADA_S` (10 s) depois do `ZERADA` dela com motivo `SINAL_OPOSTO`, não passa pelos limites do uid (os globais valem). A janela é da memória do processo: depois de um restart não há virada até a próxima zeragem. | roteiro 77 |
| L3 | **Repreço da saída depois de `SAIDA_ESGOTOU`**: a cada `REPRECO_ESGOTADO_S` (10 s) em vez de `TIMEOUT_SAIDA_S` (2 s), mantendo o alerta crítico. HML 16/10: a saída da #234066 (compra para zerar a venda, com a HML acima da fita) foi reapregoada 43 vezes em 80 s. | roteiro 79 |

Os roteiros até o 75 foram escritos com os padrões de um usuário só: o `_comum.fix` fixa esses
valores e desliga L2 e L3; os roteiros novos ligam o que testam. Os limites do uid na habilitação
(posição e quantidade, v1.2 U5) continuam como estavam.

**`LIMITE_PERDA_DIA_PTS` (1.500) também é global** (o resultado do dia soma todos os usuários): com
190 usuários, é ~8 pts por usuário. Fica como está até decisão (não entrou nesta revisão).

### 8.1 A HML não serve para medir execução

A HML da DSA é **compartilhada** e reproduz a fita do pregão anterior (a de 15/10 em 16/10: os
fills de hoje erram 50 pts em média contra a fita de 15/10, contra 1.000+ pts contra as de 13 e
14/10), mas **o book dela se descola da fita**: até ~09:34 os fills de compra ficaram a ±25 pts do
ask da fita; a partir daí, de dezenas a ±200 pts (09:33 −110, 10:03 +105, 10:13 +205, 10:19 −55),
sem deslocamento de tempo constante — com a HML acima da fita, a compra a ask + 15 não executa e a
venda executa bem acima do limite. O relógio do PnT (o 52) está ~0,45 s atrás do nosso, o que não
explica isso. **A HML serve para validar o fluxo (mensagens, estados, proteção, conciliação), não
para medir execução, preço ou slippage.**
