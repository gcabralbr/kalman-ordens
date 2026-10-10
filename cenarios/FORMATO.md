# Roteiros do PnT falso

Cada arquivo `NN-nome.fix` é um cenário da Parte 11.1, escrito como troca de mensagens FIX.
O PnT falso genérico lê o roteiro, faz o papel do PnT na sessão FIX e controla o relógio e o
preço que o serviço enxerga. Antes de cada cenário é carregado `_comum.fix` (configuração,
modelos e blocos).

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
| `entrada OP LADO [qtd=1] [stop=500] [motivo=SINAL]` | `POST /ordens` papel ENTRADA, uid = `ORDENS_UID_REAL`, `ts_decisao` = hora do relógio |
| `saida OP LADO MOTIVO [qtd=1]` | `POST /ordens` papel SAIDA; LADO é o lado da ordem de saída |
| `http METODO /caminho [json]` | requisição livre |
| `resposta campo=valor ...` | confere o corpo da última resposta HTTP (`parada.modo=BLOQUEAR`; `null`; `*` = presente) |
| `saude campo=valor ...` | `GET /saude` e confere |
| `evento TIPO campo=valor ...` | existe em `GET /eventos` um evento desse tipo, ainda não casado, que bate; ele passa a contar como casado |
| `sem_evento TIPO [campo=valor ...]` | nenhum evento não casado desse tipo |
| `conta_eventos TIPO [campo=valor ...] = N` | total de eventos desse tipo (casados ou não) |
| `alerta CODIGO` | um alerta com esse código, ainda não casado (Telegram e evento ALERTA levam `codigo` e `texto`) |
| `sem_alerta CODIGO` | nenhum alerta não casado com esse código |

## Processo e sessão

| Instrução | Efeito |
|---|---|
| `aguarda_logon` | espera o serviço logar (tempo real, até 10 s) |
| `derruba_sessao` | o PnT falso derruba a conexão; o serviço reconecta sozinho |
| `reinicia_servico` | encerra o processo do serviço e sobe de novo com o mesmo banco e o mesmo store FIX; o relógio continua de onde estava |

## Blocos e modelos (em `_comum.fix`)

```
modelo NOME TAG=VAL ...           # usado por envia @NOME
bloco NOME [PARAM ...]            # executa NOME PARAM=valor; dentro do bloco, $PARAM
  ...
fim
```

## Códigos de alerta e de recusa

Catálogo único em `docs/ALERTAS.md`. Um roteiro só usa códigos que estão lá.

## Origem das saídas (`SAIDA_EXECUTADA.origem`, `ZERADA.origem`)

`GESTOR` (pedida pelo gestor), `STOP_SERVIDOR` (fill da 1014), `STOP_FURADO` (Parte 3.6),
`PARADA` (ZERAR), `ZERAGEM` (HORA_ZERAGEM), `PROTECAO_REJEITADA` (1014 rejeitada duas vezes),
`ENTRADA_TARDIA` (fill de entrada depois da espera de encerramento; só registro, o gestor nunca
reabre a operação). O gestor trata tudo o que não é `GESTOR` como operação fechada pelo serviço,
com esse motivo.
