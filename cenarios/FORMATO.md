# Roteiros do PnT falso

Cada arquivo `NN-nome.fix` é um cenário da Parte 11.1, escrito como troca de mensagens FIX.
O PnT falso genérico lê o roteiro, faz o papel do PnT na sessão FIX e controla o relógio e o
preço que o serviço enxerga. Antes de cada cenário é carregado `_comum.fix` (configuração,
modelos e blocos).

Os roteiros rodam nos dois backends de persistência (memória e MariaDB 10.11.15 em container):
`go test ./internal/cenario/` (memória) e, com `KALMAN_TESTE_MARIADB`, `TestRoteirosMariaDB`.

O roteiro **é a especificação do comportamento**: se o serviço e o roteiro discordam, um dos
dois está errado e a correção é discutida, não contornada no código.

## Regras gerais

- Uma instrução por linha. `#` começa comentário. Linhas em branco são ignoradas.
- Tokens separados por espaço. Valor com espaço vai entre aspas: `58="Insufficient margin"`.
- Números são comparados como número (`44=130020` casa com `44=130020.00`). O resto, como texto.
- Toda mensagem que o serviço envia é validada pelo PnT falso contra
  `pnt/dicionario/dsa_FIX44.xml` com `AllowUnknownMsgFields=N`, `ValidateUserDefinedFields=N` e
  `ValidateFieldsOutOfOrder=Y`. **Falha de validação faz o cenário falhar**, em qualquer roteiro.
  É o que garante que as ordens single-leg do serviço passam no dicionário.
- ClOrdID do serviço: `KM-<papel>-<operacao_id>-<n>` (papel E, P ou S; n conta as mensagens do
  papel dentro da operação). Os roteiros afirmam esses valores exatos.
- O `37` é inventado pelo roteiro (`37=dsa.1005.P`), como o PnT faria.

## Cabeçalho (antes do primeiro passo)

| Instrução | Efeito |
|---|---|
| `cenario NN Título` | identificação |
| `hora AAAA-MM-DD HH:MM:SS` | hora inicial do relógio do serviço (America/Sao_Paulo) |
| `config CHAVE=valor` | sobrescreve a configuração do serviço (mesmas chaves do service.env) |
| `usuario UID hml\|prd JSON` | a configuração do usuário no GQ (`usutxtconfig` / `usutxtconfigprd`; o JSON é o resto da linha, cru); `usuario UID ausente` apaga a linha. No `_comum.fix` e no cabeçalho vale antes de o serviço subir; no corpo, na hora |
| `habilita UID [posicao_max=N] [qtd_max=N]` / `desabilita UID` | a habilitação do uid (o que a CLI faria); mesma regra de quando vale |

## Relógio e preço

| Instrução | Efeito |
|---|---|
| `avanca 3s` / `avanca 500ms` / `avanca 1m` | avança o relógio do serviço em passos de 100 ms; depois de cada passo espera o serviço ficar ocioso |
| `preco ticker=T preco=N bid=N ask=N status84=S idade=D` | muda o que a fonte de preço falsa devolve (contrato em `docs/referencia/precos-atual.md`); campos omitidos ficam como estavam; `campo=-` omite o campo do JSON (como o marketdata faz sem valor de hoje); `idade` vira `idade_seg` e `ts` = relógio − idade; pode aparecer em qualquer ponto |
| `preco vazio` | corpo `{}` com 200 (sem dado de hoje) |
| `preco http=404` / `preco http=400` | motor do marketdata desabilitado / ativo desconhecido |
| `preco normal` | volta a responder 200 com os últimos campos definidos (definir qualquer campo também volta) |

O relógio só anda com `avanca`. Tempo "por mais de X" é contado a partir do primeiro passo em que
o serviço observa a condição; os roteiros deixam a folga de um passo quando isso importa.

## Mensagens FIX

| Instrução | Efeito |
|---|---|
| `recebe TAG=VAL ...` | a próxima mensagem de aplicação enviada pelo serviço (e 35=3/35=j) tem de casar com as tags listadas; as não listadas são livres |
| `TAG=>NOME` (dentro de `recebe`) | captura o valor da tag em `NOME`; vale para tags de cabeçalho (`34=>S1`) |
| `envia TAG=VAL ...` | o PnT falso envia; `$NOME` usa uma captura; o cabeçalho de sessão é automático; `60` e, em 35=8, `17` são preenchidos se ausentes; a ordem das tags do corpo é preservada (grupos repetidos saem como escritos) |
| `envia @MODELO TAG=VAL ...` | parte de um modelo e acrescenta/sobrescreve tags |
| `envia_offline ...` | com a sessão caída: grava no store do PnT falso (consome sequência) sem transmitir; chega ao serviço por ResendRequest |
| `recebe_sessao TAG=VAL ...` | confere uma mensagem de sessão (35=2, 35=4, 35=5...) enviada pelo serviço |
| `nada` / `nada TAG=VAL ...` | depois de o serviço ficar ocioso, não há mensagem de aplicação pendente (ou nenhuma que case com o filtro) |
| `em_qualquer_ordem` ... `fim` | os `recebe` do bloco casam em qualquer ordem |

Mensagens de sessão (0, 1, 2, 4, 5, A) não entram na fila de `recebe`/`nada`; só em `recebe_sessao`.

## Contrato HTTP e eventos

| Instrução | Efeito |
|---|---|
| `entrada OP LADO [qtd=1] [stop=500] [motivo=SINAL] [ts=...] [uid=N] [modulo=M]` | `POST /ordens` papel ENTRADA, uid 7 (o habilitado no `_comum.fix`; `uid=` troca; `modulo=` põe o campo no corpo — sem ele, KALMAN); `ts_decisao` = hora do relógio no primeiro pedido da operação e **o mesmo** nos seguintes (o gestor reenvia o mesmo corpo); `ts=` força outro |
| `saida OP LADO MOTIVO [qtd=1] [modulo=M]` | `POST /ordens` papel SAIDA; LADO é o lado da ordem de saída |
| `stop OP preco=N\|pts=N [uid=N] [modulo=M] [origem=O]` | `POST /operacoes/OP/stop` (ajuste da proteção, SPEC v1.4 K): `preco=` vira `preco_stop`, `pts=` vira `stop_pts`, `origem=` vira `origem` (v1.19) |
| `sinal ID [parte=N] [partes=N] [modulo=M] [ts=T] e=OP:C\|V[:UID] ... s=OP:C\|V:MOTIVO[:UID] ...` | `POST /sinais` (SPEC v1.10 §6): `parte`/`partes` 1/1 se ausentes; entrada com qtd 1, stop_pts 500, motivo SINAL; uid 7 se ausente; `ts_decisao` = hora do relógio no primeiro envio do sinal e o mesmo nos reenvios |
| `http METODO /caminho [json]` | requisição livre |
| `http_async METODO /caminho [json]` / `http_aguarda` | a requisição fica em curso enquanto o roteiro segue (ex.: `GET /pnt/ordens`, que espera as respostas do PnT falso ao AF); `http_aguarda` espera a resposta (tempo real, até 15 s) e ela vira a "última resposta" |
| `metrica NOME [rotulo=valor ...] OP N` | `GET /metrics` (texto do Prometheus): a série `NOME` com exatamente esses rótulos (fora o `le`) tem valor `OP N` (`=`, `>=`, `>`, `<=`); os contadores somam em todos os roteiros do processo, então os roteiros usam `>=` |
| `resposta campo=valor ...` | confere o corpo da última resposta HTTP (`parada.modo=BLOQUEAR`; `null`; `*` = presente; numa lista, `mensagens.0.msgtype` e `mensagens.#` = tamanho; resposta que é uma lista fica em `_lista`); `_status` é o código HTTP |
| `saude campo=valor ...` | `GET /saude` e confere |
| `evento TIPO campo=valor ...` | existe em `GET /eventos` um evento desse tipo, ainda não casado, que bate; ele passa a contar como casado |
| `sem_evento TIPO [campo=valor ...]` | nenhum evento não casado desse tipo |
| `conta_eventos TIPO [campo=valor ...] = N` | total de eventos desse tipo (casados ou não) |
| `alerta CODIGO` | um alerta com esse código, ainda não casado (Telegram e evento ALERTA levam `codigo` e `texto`) |
| `sem_alerta CODIGO` | nenhum alerta não casado com esse código |

## Processo e sessão

| Instrução | Efeito |
|---|---|
| `aguarda_logon [N]` | espera o serviço logar (tempo real, até 10 s); com N, espera N logons novos (ex.: um que cai no reenvio e o seguinte) |
| `derruba_sessao` | o PnT falso derruba a conexão; o serviço reconecta sozinho |
| `corrompe checksum` / `corrompe bodylength N` | a próxima mensagem de aplicação do PnT falso sai no fio com o 10 errado, ou com o 9 somado de N (e o 10 recalculado); o store do falso guarda a certa, então um ResendRequest a entrega íntegra |
| `reinicia_servico` | encerra o processo do serviço e sobe de novo com o mesmo banco (no MariaDB, numa conexão nova) e o mesmo store FIX; o relógio continua de onde estava |
| `pnt_recusa_logon "TEXTO"` / `pnt_aceita_logon` | o PnT falso passa a recusar todo logon com 35=5 58=TEXTO, sem contar a sequência do logon recusado (como o PnT da HML segurando a sessão depois de uma queda abrupta) / volta a aceitar |
| `aguarda_recusas N` | espera (tempo real, até 15 s) o PnT falso ter recusado N logons desde o início |
| `pnt_espera_seq +N` / `pnt_esquece N` | com a sessão caída: o PnT falso passa a esperar o MsgSeqNum N à frente (o logon seguinte é "MsgSeqNum too low, expecting …") / N atrás (pede o reenvio das últimas N mensagens no logon) |
| `corrompe_store` | com a sessão caída: muda, no lugar, o 9 da última mensagem gravada no store FIX do serviço (o reenvio não a lê mais) |
| `descarta TAG=VAL ...` | tira da fila as mensagens de aplicação pendentes que casam (zero ou mais) e dá por perdido o que o serviço enviou numa sessão que caiu antes de o PnT falso processar; só para corridas de tempo real numa queda |
| `motor_lento` / `motor_normal` | trava / solta a fila do motor (os pedidos HTTP ficam esperando; com `HTTP_ESPERA_S` curto, dá o 503); com o motor travado, `avanca` move o relógio e só enfileira os ticks (atrás do motor parado) |

## Blocos e modelos (em `_comum.fix`)

```
modelo NOME TAG=VAL ...           # usado por envia @NOME
bloco NOME [PARAM ...]            # executa NOME PARAM=valor; dentro do bloco, $PARAM
  ...
fim
```

## Códigos de alerta e de recusa

Catálogo único em `docs/ALERTAS.md`. Um roteiro só usa códigos que estão lá.

Em todo roteiro, ao final, o executor confere que todo evento publicado (inclusive `ALERTA`), todo
texto que iria ao Telegram e todo `motivo` de resposta HTTP é ASCII (SPEC v1.4 L1).

## Origem das saídas (`SAIDA_EXECUTADA.origem`, `ZERADA.origem`)

`GESTOR` (pedida pelo gestor), `STOP_SERVIDOR` (fill da 1014), `STOP_FURADO` (Parte 3.6),
`PARADA` (ZERAR), `ADMIN` (encerrar da tela de admin, SPEC v1.7 A1), `ZERAGEM` (HORA_ZERAGEM), `PROTECAO_REJEITADA` (1014 rejeitada duas vezes),
`PROTECAO_INSTAVEL` (1014 recriada caiu de novo, v1.1 P8),
`ENTRADA_TARDIA` (fill de entrada depois da espera de encerramento; só registro, o gestor nunca
reabre a operação). O gestor trata tudo o que não é `GESTOR` como operação fechada pelo serviço,
com esse motivo.

## Flexscan (SPEC-FLEXSCAN-v1)

Com `config FLEXSCAN_URL=falso` o executor sobe a Flexscan falsa (`internal/flexscan/falso`, a API REST
com o comportamento observado no apisim em 23/10) e a ponte, ligada ao motor como o conector da
Flexscan; espera o login dela antes do primeiro passo. Os padrões dos roteiros: usuário `flx_roteiro`,
`FLEXSCAN_CONTAS_HML=S-1117150`, `FLEXSCAN_TIMEOUT_MS=300` (a falsa segura 600 ms um pedido programado
com `timeout`), sem limite de taxa, receptor do webhook ligado (chamado direto, sem listener). O uid da
Flexscan é declarado no roteiro (`usuario 8 hml {"Robo":{"CORSeqId":88,"CCONroConta":"S-1117150"}}` e
`habilita 8 conectores=flexscan`); o bloco `COMPRADO_FLX OP` do `_comum.fix` monta a posição comprada
protegida (capturas `FE` e `FP`). O `avanca` também move a ponte (consulta ativa e periódica, token).

| Instrução | Efeito |
|---|---|
| `flx_recebe METODO [campo=valor ...] [id=ID] [id=>NOME]` | o próximo pedido de alteração (POST, PUT, DELETE) que a Flexscan falsa recebeu casa: campos do corpo JSON (número como número; `campo=-` = ausente); `id=` confere a ordem (PUT/DELETE); `id=>NOME` captura o id (no POST, o da ordem criada). As consultas (GET) não entram na fila |
| `flx_nada` | nenhum pedido de alteração pendente |
| `flx_executa ID [qtd=N] preco=P` | a bolsa executa N (padrão: o restante) a P |
| `flx_webhook ID [ID ...]` | o webhook com a lista de retratos (estratégia sorder) dessas ordens, na ordem dada |
| `flx_webhook_corpo CORPO` | o webhook com o corpo cru (o resto da linha; `$NOME` resolvido) |
| `flx_webhook_http METODO /caminho [corpo]` | um pedido qualquer ao receptor; `resposta _status=N` confere |
| `flx_falha timeout\|500\|400\|401\|403 [cria=1] [texto=...] [codigo=N]` | o próximo pedido de alteração falha assim; `timeout cria=1`: a ordem nasce (ou a edição/o cancel acontece) e a resposta se perde |
| `flx_recusa_edicao ID [texto]` | a próxima edição (PUT) é recusada como a razão 4 do apisim (202, valores antigos, `clxRejReason` 4, `Text` "Edicao rejeitada, ...") |
| `flx_cancela ID` | cancelamento que não foi nosso |
| `flx_altera ID [preco=N] [stop=N] [status=X] [texto=...]` | muda a ordem como a Flexscan faria sozinha (ex.: preço ajustado ao túnel) |
| `flx_cria NOME [conta=] [lado=] [tipo=] [qtd=] [preco=] [status=] [id=>NOME]` | uma ordem que não passou pelo serviço |
| `flx_ordem ID campo=valor ...` | confere o estado na Flexscan falsa (`status`, `preco`, `stop`, `exec`, `qtd`, `nome`, `conta`) |
| `flx_token_expira` | todo token atual passa a dar 401 |
| `flx_login recusa\|senha\|aceita` | os logins seguintes falham (transitório; `senha`: 3402, definitivo) ou voltam a passar |
| `flx_webhooks limpa` / `falha=STATUS metodo=GET\|POST` / `cadastra url=U [triggers=a,b]` / `n=N [url=U]` | (v1.21) o cadastro de webhooks da falsa: some; a próxima chamada com o método responde STATUS; um cadastro feito por fora; confere quantos há (com essa URL) |
| `flx_nao_achada 250ms` | (v1.21) o GET da ordem responde 404 "Order not found" até esse tempo depois do POST (`0` desliga), como o apisim de 27/10 |
| `contrato ARQUIVO SECAO [caminho]` | a última resposta HTTP (ou o `caminho` dentro dela, ex.: `flexscan`) tem a forma do primeiro bloco ```json depois de `### SECAO` no ARQUIVO (relativo à raiz): as mesmas chaves nos dois sentidos, cada item das listas, o tipo JSON quando os dois lados não são null |
| `flx_trafego campo=valor ... = N` | (v1.21) quantas linhas do `GET /flexscan/trafego` casam (texto; `null`; `*` = presente; `~x` = contém); toda chamada falha se um token da falsa, a senha, o segredo ou a `FLEXSCAN_WEBHOOK_URL` aparecerem no corpo |

Nos roteiros, `FLEXSCAN_PRIMEIRA_CONSULTA_MS` é 0 (a consulta do POST na hora, como antes da v1.21); o 115 liga os 200 ms. Sem `FLEXSCAN_WEBHOOK_URL`, não há conferência do cadastro.
