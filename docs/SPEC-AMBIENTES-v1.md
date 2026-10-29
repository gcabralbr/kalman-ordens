# SPEC kalman-ordens — ambientes SIMULADOR e REAL por ordem (desenho, 29/10/2026)

**Estado: aprovada em 29/10, com as decisões do §7.3.** Passo 1 (v1.25) em implementação. Base: v1.24
(`96c5575139c1`, instalada na .53 em 29/10).

## 0. A mudança

**Hoje** quem escolhe a coluna do GQ é a máquina:
- `ORDENS_AMBIENTE=HML` lê `usutxtconfig`;
- `ORDENS_AMBIENTE=PRODUCAO` lê `usutxtconfigprd` (`internal/usuario`);
- a trava cruzada (`ORDENS_CONTAS_HML`, `FLEXSCAN_CONTAS_HML`) impede conta de simulador em produção e
  vice-versa.

**Passa a ser** o ambiente do login do usuário no GQ que escolhe a coluna, e com ela o conector e a
conta:

| ambiente do login | coluna do GQ | o que é |
|---|---|---|
| `SIMULADOR` | `usutxtconfig` | o simulador interno do GQ (**paper**, nenhuma ordem vem ao serviço) ou um simulador externo na corretora (Flexscan apisim) |
| `REAL` | `usutxtconfigprd` | a conta real |

A máquina deixa de escolher a coluna e passa a ser só um **teto**:
- quais ambientes ela aceita;
- para quem o REAL vale;
- com que limites.

**Nomes:** o contrato usa `SIMULADOR` e `REAL`, os mesmos valores do `X-GQ-Ambiente` que o adpproxy
já manda ao Kalman. O Kalman repassa o valor sem traduzir. Na configuração, `SIM` é aceito como
abreviação de `SIMULADOR`.

### 0.1 SIMULADOR: paper ou simulador externo (D1)

No SIMULADOR, quem decide entre paper e ordens é o `usutxtconfig` do usuário:

| `usutxtconfig` | destino | o Kalman |
|---|---|---|
| `Robo.CORSeqId` 9999 **e** conta `QUANT` | **PAPER**, o simulador interno do GQ (o caso de todos os usuários comuns) | não manda ordem ao serviço |
| aponta para um simulador externo (ex.: o 5725: `Robo.Conector` FLEXSCAN, `CORSeqId` 85, conta `S-1117150`) | o conector, a corretora e a conta | manda as ordens com `ambiente: SIMULADOR`, como hoje |

- **A conta** é lida pela regra de hoje: `Futuros.CCONroContaFut` e, sem ela, `Robo.CCONroConta`.
- **9999 com outra conta:** `CONFIG_USUARIO_INVALIDA` ("9999 sem a conta QUANT"). O paper só vale
  com os dois.
- **No REAL,** o 9999 continua `DESTINO_INVALIDO`.
- **Ordem que chega para um destino PAPER** (o Kalman não deveria mandar): recusa `DESTINO_PAPER`,
  sem alerta.
- **O PnT não tem uso no SIMULADOR,** porque a HML do PnT é D-1. Ele só existe no REAL. O SIMULADOR
  com o conector PNT é recusado com `CONECTOR_AMBIENTE` ("o PnT só opera no REAL").
- **O serviço expõe o destino resolvido** por uid e ambiente (§4.3). O Kalman o consulta na ativação
  do dia, para decidir se manda ordens e para mostrar na tela: "Simulador · paper", "Simulador ·
  Flexscan" ou "REAL · Flexscan · Capital 715124".

## 1. O ambiente em cada ordem

### 1.1 De onde vem

1. O adpproxy manda ao Kalman `X-GQ-Ambiente` (o ambiente do login) e `X-GQ-Segredo` em toda rota,
   inclusive na ativação do dia. Isso já existe no Kalman (`gq.ambiente_de`, SPEC-KALMAN-ORDENS-REAIS
   D4/P9).
2. O Kalman grava a ativação por `(uid, ativo, ambiente)` (`kalmanativacao`) e a operação com
   `KOTxtAmbiente`.
3. O Kalman manda o `ambiente` em cada pedido a este serviço (§7).

### 1.2 Gravado na operação

- **Banco:** a migração nova (0014) cria a coluna `ambiente` em `kalmanordemoperacao`
  (`VARCHAR(10) NOT NULL DEFAULT 'SIMULADOR'`). As operações de hoje ficam `SIMULADOR`, o que é
  correto: a .53 só tem contas de simulador. Nenhuma migração instalada muda.
- **Na entrada:** o `ambiente` do pedido vira o da operação. A coluna do GQ, o conector e a conta são
  lidos desse ambiente e gravados na operação, como hoje.
- **Fixo enquanto houver posição.** Tudo o que vem depois da entrada usa o ambiente **gravado**:
  - a proteção, o ajuste do stop, o TRSP, a saída, a zeragem, o encerramento pelo admin, a
    conciliação e o restart;
  - a mudança da coluna do GQ no meio não muda a operação. A conferência de conta (`CONFERE_CONTA_S`)
    passa a ler a coluna do ambiente da operação.
- **Pedido com outro ambiente para uma operação existente:**
  - **entrada** (mesmo `operacao_id`, outro ambiente): recusa `AMBIENTE_DIVERGENTE`;
  - **saída, stop ou ação do admin:** **não é recusado**, porque recusar uma saída deixa a posição
    aberta. Executa no ambiente gravado, com alerta crítico `AMBIENTE_PEDIDO_DIVERGENTE` (operação,
    ambiente pedido e ambiente gravado).
- **Idempotência:** a chave continua `(modulo, operacao_id, papel)`. O `operacao_id` do Kalman é
  único entre os dois ambientes, porque vem da mesma `kalmanoperacao`.
- **ClOrdID:** não muda (`KM-E-<op>-<n>`). As sessões dos dois ambientes são separadas, então não há
  colisão na corretora.

### 1.3 Pedido sem `ambiente` (só na transição)

- **`AMBIENTE_PADRAO`:** é o ambiente de quem não manda o campo. Só aceita `SIMULADOR`; com `REAL` o
  serviço não sobe.
  - Na .53, durante a transição, `SIMULADOR` mantém o comportamento de hoje.
  - Vazio (o padrão): o pedido sem `ambiente` é recusado com `AMBIENTE_AUSENTE`.
- **Fim da transição:** a chave sai depois que o Kalman passar a mandar o campo (§8, passo 3).

## 2. Os dois ambientes no mesmo serviço

Um processo atende os dois ambientes ao mesmo tempo. **Cada par (ambiente, conector) é uma
instância separada.** Elas compartilham o processo, o banco, a API e a fonte de preço, e mais nada.

### 2.1 Por (ambiente, conector)

| | SIMULADOR | REAL |
|---|---|---|
| **Flexscan** | URL, usuário, senha, estado de login, webhook (segredo, URL pública, cadastro) | os mesmos, das chaves `REAL_` |
| **PnT (FIX)** | — (sem uso: a HML do PnT é D-1) | host, porta, CompIDs, usuário, senha, `FIX_DIR` (store), janela |
| **Contas** | a lista de contas de simulador do conector (a trava, §4) | `REAL_CONTAS` |
| **Estado** | login, sessão, ordens acompanhadas, mass status, conciliação da partida, disjuntor | separados |

- **Webhook:** um listener só (`FLEXSCAN_WEBHOOK_ENDERECO`), com um caminho por ambiente (prefixo e
  segredo de cada um).
  - O segredo identifica o ambiente. A ponte de um ambiente só conhece as ordens dele: o retrato de
    uma ordem REAL que chega pelo caminho do SIMULADOR vira `DESCONHECIDA`, nunca um fill.
  - O IP de origem (v1.24) fica registrado por ambiente.
- **Fonte de preço:** é a mesma, porque o mercado é um só (o `PRECOS_URL` e o `PRECOS_URL_FLEXSCAN`
  de hoje). Uma chave `REAL_PRECOS_URL_FLEXSCAN` é opcional, para o caso de a fonte do REAL ser
  outra.
- **Ajustes de tempo** (`TIMEOUT_*`, `ESPERA_*`, `FLEXSCAN_CONSULTA_S` e semelhantes): compartilhados.

### 2.2 Posição e conciliação

- **Posição** por ambiente (e, dentro dele, por conector, como hoje o `posicao_por_conector`). A
  invariante, a zeragem das 14:30 e o alerta de posição sem operação são verificados por ambiente.
- **Conciliação da partida e mass status:** por (ambiente, conector). A operação gravada diz qual
  sessão a concilia.
- **Parada:**
  - a `POST /parada` sem `ambiente` para os dois, como hoje;
  - com `{"ambiente":"REAL"}` para só um;
  - o disjuntor de bolsa indisponível é por (ambiente, conector).
- **Perda do dia, entradas por dia e por minuto, recusas para parada:** contadas por ambiente. A perda
  do REAL bloqueia só o REAL.

### 2.3 O que a API mostra

- **`ambiente`:** no `POST /ordens`, no `GET /ordens/{id}`, no `GET /operacoes` (com o filtro
  `?ambiente=`), no histórico e em todo evento publicado.
- **`/saude`:** os blocos de sessão, Flexscan, posição e parada passam a vir por ambiente
  (`"ambientes": {"SIMULADOR": {...}, "REAL": {...}}`). Os campos de hoje continuam, com os números
  do SIMULADOR, até o Kalman migrar.
- **`/flexscan/trafego?ambiente=`:** sem o parâmetro, o SIMULADOR (como hoje). O `saude` do tráfego é
  o do ambiente pedido.
- **`/sessao/ao-vivo?ambiente=`:** o mesmo para o PnT.

## 3. O teto da máquina

### 3.1 Chaves

| chave | padrão | o quê |
|---|---|---|
| `AMBIENTES_PERMITIDOS` | `SIM` | `SIM`, `REAL` ou `SIM,REAL`. O REAL só entra se for escrito. |
| `REAL_UIDS` | vazio | uids que podem operar REAL: uma lista, ou `*` para todos os habilitados no REAL. Vazio = ninguém. |
| `REAL_CONTAS` | vazio | contas aceitas no REAL: uma lista, ou `*` para qualquer conta que passe a trava (§4). Vazio = nenhuma. |
| `REAL_LIMITE_QTD_ORDEM` | **obrigatória com REAL** | contratos por ordem no REAL |
| `REAL_LIMITE_PERDA_DIA_PTS` | **obrigatória com REAL** | perda máxima do dia no REAL, mesma regra do `LIMITE_PERDA_DIA_PTS` |
| `REAL_LIMITE_ENTRADAS_DIA` | **obrigatória com REAL** | entradas por dia no REAL |
| `REAL_LIMITE_POSICAO_MAX` | `REAL_LIMITE_QTD_ORDEM` | posição máxima no REAL |
| `REAL_LIMITE_ENTRADAS_MIN` | `LIMITE_ENTRADAS_MIN` | entradas por minuto no REAL |
| `AMBIENTE_PADRAO` | vazio | só na transição (§1.3) |

- **Os limites do SIMULADOR** são as chaves `LIMITE_*` de hoje, sem mudança.
- **Com o REAL em `AMBIENTES_PERMITIDOS`, o serviço não sobe** (erro claro no journal) quando:
  - falta uma das três chaves obrigatórias;
  - `REAL_UIDS` ou `REAL_CONTAS` está vazio;
  - nenhum conector REAL está configurado.

  O REAL nunca entra ligado por um padrão.
- **Sem o REAL permitido**, as chaves `REAL_*` são ignoradas, com um aviso no journal se alguma
  estiver preenchida.
- **O limite efetivo de cada uid** é o menor entre o teto da máquina e a habilitação do uid naquele
  ambiente (§4.2).
- **`ORDENS_AMBIENTE`** vale na transição: `HML` equivale a `AMBIENTES_PERMITIDOS=SIM` com
  `AMBIENTE_PADRAO=SIMULADOR`. Junto com `AMBIENTES_PERMITIDOS`, o serviço não sobe (duas fontes para
  a mesma coisa). Ela sai no passo 5 do §8.

### 3.2 As duas máquinas

| | .53 (DEV) | .51 (PROD) |
|---|---|---|
| `AMBIENTES_PERMITIDOS` | `SIM`. `SIM,REAL` só durante os testes de REAL, depois volta | `SIM,REAL` (D2): o SIMULADOR (paper para os comuns, simulador externo para quem tiver um) e o REAL |
| `REAL_UIDS` | um uid de teste (ex.: `5725`) | `*`, e quem decide é a habilitação REAL de cada uid |
| `REAL_CONTAS` | a conta real desse uid | `*`, com a trava cruzada (§4) |
| `REAL_LIMITE_QTD_ORDEM` | `1` | o de produção, informado pelo responsável antes do passo 5 (D3) |
| `REAL_LIMITE_PERDA_DIA_PTS` / `REAL_LIMITE_ENTRADAS_DIA` | baixos (ex.: 300 / 10) | os de produção |

## 4. Validação da conta e da habilitação por ambiente

### 4.1 A ordem das verificações na entrada

As primeiras recusas são mais baratas e mais claras. Nenhuma lê a credencial do GQ, e o JSON da
coluna continua não indo inteiro para log, recusa ou banco.

| # | verificação | recusa | alerta |
|---|---|---|---|
| 1 | `ambiente` presente (ou `AMBIENTE_PADRAO`) | `AMBIENTE_AUSENTE` | — |
| 2 | `ambiente` é `SIMULADOR` ou `REAL` | `AMBIENTE_INVALIDO` | — |
| 3 | `ambiente` em `AMBIENTES_PERMITIDOS` | `AMBIENTE_NAO_PERMITIDO` ("REAL não permitido nesta máquina") | crítico, se for REAL |
| 4 | REAL: uid em `REAL_UIDS` | `REAL_UID_NAO_PERMITIDO` | crítico |
| 5 | habilitação do uid **naquele ambiente** | `UID_NAO_HABILITADO` (o motivo diz o ambiente) | — |
| 6 | a coluna do ambiente: JSON, destino, conector da habilitação do ambiente, conta e formato da conta pelo conector; no SIMULADOR, o PAPER e o PnT (§0.1) | as de hoje (`CONFIG_USUARIO_INVALIDA`, `DESTINO_INVALIDO`, `CONECTOR_NAO_HABILITADO`, `CONECTOR_INDEFINIDO`, `CONTA_INVALIDA`), com o ambiente no motivo; `DESTINO_PAPER`; `CONECTOR_AMBIENTE` | as de hoje |
| 7 | o conector configurado **naquele ambiente** | `CONECTOR_NAO_CONFIGURADO` (ex.: "FLEXSCAN no REAL sem REAL_FLEXSCAN_URL") | — |
| 8 | **trava da conta:** SIMULADOR → a conta está na lista de simulador do conector. REAL → a conta **não** está em nenhuma lista de simulador **e** está em `REAL_CONTAS` (ou `*`) | `CONTA_AMBIENTE` (simulador × real); `REAL_CONTA_NAO_PERMITIDA` (fora da `REAL_CONTAS`) | crítico |
| 9 | os limites do ambiente (máquina e habilitação) | as de hoje (`POSICAO_MAXIMA`, `ENTRADAS_POR_MINUTO`, `PERDA_DIARIA`...), com o ambiente no motivo | as de hoje |

A trava do passo 8 é a de hoje (v1.2), aplicada por ambiente e não mais pela máquina. Ela pega a
conta real posta por engano no `usutxtconfig`, e a de simulador no `usutxtconfigprd`.

### 4.2 Habilitação por ambiente

- **Banco:** a habilitação (`kalmanordemusuario` e o histórico) ganha a coluna `ambiente`, na mesma
  migração 0014, e a chave passa a `(uid, ambiente)`. As linhas de hoje ficam `SIMULADOR`.
- **Habilitação REAL:**
  - é uma linha própria, com `habilitado`, `posicao_max`, `qtd_max_ordem` e `conectores`;
  - nasce desligada;
  - habilitar o SIMULADOR nunca habilita o REAL.
- **CLI:**
  - `usuario habilitar -uid N -ambiente REAL ...` (sem `-ambiente`, o SIMULADOR, como hoje);
  - `usuario conferir <uid>` mostra as duas colunas resolvidas (conector, conta e a trava de cada
    uma), as duas habilitações e se esta máquina permite o REAL para esse uid e essa conta.
- **API:** `GET/POST /usuarios/{uid}/habilitacao?ambiente=REAL`. Sem o parâmetro, o SIMULADOR.

### 4.3 O destino resolvido: `GET /usuarios/{uid}/destino`

**Para que serve:** o Kalman decide na ativação do dia se manda ordens e mostra na tela para onde
elas vão.

**Como responde:**
- `?ambiente=SIMULADOR` ou `?ambiente=REAL`; sem o parâmetro, os dois.
- Roda as verificações 3 a 8 do §4.1, sem limites e sem posição, e devolve o resultado em vez de
  recusar.
- Nunca lê nem devolve credencial. Do GQ, só o destino, o conector e a conta.
- **A resposta é sempre 200.** Mesmo quando não dá para mandar ordem, ela traz `pode_enviar: false`
  e o motivo. Uid sem linha no GQ: os dois ambientes com `CONFIG_USUARIO_INVALIDA`.

```json
{"uid": 5725, "ambientes": {
  "SIMULADOR": {"destino": "CONECTOR", "conector": "FLEXSCAN", "corretora": "BTG", "corretora_codigo": 85,
                "conta": "S-1117150", "habilitado": true, "permitido": true, "pode_enviar": true,
                "codigo": null, "motivo": null, "rotulo": "Simulador · Flexscan"},
  "REAL":      {"destino": null, "conector": null, "corretora": null, "corretora_codigo": null,
                "conta": null, "habilitado": false, "permitido": false, "pode_enviar": false,
                "codigo": "AMBIENTE_NAO_PERMITIDO", "motivo": "REAL nao permitido nesta maquina",
                "rotulo": null}}}
```

**Os campos:**
- **`destino`:**
  - `PAPER`: o 9999 com a conta QUANT; `pode_enviar` falso, `rotulo` "Simulador · paper";
  - `CONECTOR`: o conector, a corretora e a conta preenchidos;
  - `null`: a configuração não resolve, e `codigo` e `motivo` dizem por quê.
- **`permitido`:** a máquina aceita esse ambiente, esse uid e essa conta (§3).
- **`habilitado`:** a habilitação do uid naquele ambiente (§4.2).
- **`pode_enviar`:** destino `CONECTOR`, habilitado e permitido.
- **`rotulo`:** o texto da tela. No REAL, "REAL · Flexscan · Capital 715124".

## 5. Recusas e alertas novos

| código | tipo | quando |
|---|---|---|
| `AMBIENTE_AUSENTE` | recusa | pedido sem `ambiente` e sem `AMBIENTE_PADRAO` |
| `AMBIENTE_INVALIDO` | recusa | valor diferente de `SIMULADOR` e `REAL` |
| `AMBIENTE_NAO_PERMITIDO` | recusa (+ crítico se REAL) | fora de `AMBIENTES_PERMITIDOS` |
| `REAL_UID_NAO_PERMITIDO` | recusa + crítico | uid fora de `REAL_UIDS` |
| `REAL_CONTA_NAO_PERMITIDA` | recusa + crítico | conta fora de `REAL_CONTAS` |
| `AMBIENTE_DIVERGENTE` | recusa | entrada repetida (mesmo `operacao_id`) com outro ambiente |
| `AMBIENTE_PEDIDO_DIVERGENTE` | crítico (não recusa) | saída, stop ou ação com ambiente diferente do gravado: executa no gravado |
| `DESTINO_PAPER` | recusa | entrada no SIMULADOR para um uid cujo destino é o paper (9999 com a conta QUANT) |
| `CONECTOR_AMBIENTE` | recusa | o conector não opera nesse ambiente (o PnT no SIMULADOR) |

Os textos seguem o padrão de hoje: em ASCII, e com o nome do conector e do ambiente
("[flexscan REAL] ...").

## 6. Testes

- **Unidade:**
  - o resolvedor por ambiente: a coluna, a trava nos dois sentidos, `*` e as listas;
  - a leitura da configuração: não sobe com REAL sem os limites, com `ORDENS_AMBIENTE` junto de
    `AMBIENTES_PERMITIDOS` ou com `AMBIENTE_PADRAO=REAL`;
  - os limites efetivos (o menor entre a máquina e a habilitação).
- **Roteiros novos:**
  - **ambiente no contrato:**
    - SIMULADOR aceito;
    - REAL recusado pela máquina, pelo uid e pela conta;
    - habilitação SIMULADOR sem REAL;
    - pedido sem `ambiente` com e sem `AMBIENTE_PADRAO`;
    - a entrada repetida com outro ambiente;
    - a saída com o ambiente errado executa no gravado e alerta;
    - o paper: 9999 com a conta QUANT dá `DESTINO_PAPER`, e 9999 com outra conta dá
      `CONFIG_USUARIO_INVALIDA`;
    - o `GET /usuarios/{uid}/destino`: paper, simulador externo e REAL não permitido, contra o
      contrato do §4.3;
  - **os dois ambientes ao mesmo tempo** (duas Flexscan falsas, dois PnT falsos):
    - o mesmo uid com posição no SIMULADOR e no REAL;
    - conciliação e restart por ambiente;
    - o webhook do REAL no caminho do SIMULADOR vira `DESCONHECIDA`;
    - a perda do dia do REAL bloqueia só o REAL;
    - a parada só do REAL;
    - a zeragem das 14:30 nos dois;
  - **a coluna muda no meio:** a operação mantém conta e conector, e a conferência alerta;
  - **a migração:** um banco do instalado (v1.24) sobe com as operações e as habilitações como
    `SIMULADOR` (`TestServirAtualizaDoInstalado`).
- **Roteiros de hoje:** passam sem mudança com `ORDENS_AMBIENTE=HML` (a equivalência do §3.1). O
  `TestTodosOsRoteirosLeem` conta os novos.
- **Contrato:** os exemplos do §7 conferidos contra a resposta real pelo verbo `contrato`, como o
  roteiro 115.
- **Carga:** a de 190 entradas, metade SIMULADOR e metade REAL nas falsas, para medir o custo das duas
  instâncias.
- **REAL de verdade (.53, um uid, 1 contrato, no pregão, com o responsável presente):**
  - entrada, proteção, ajuste e saída;
  - restart com posição;
  - a parada só do REAL;
  - as recusas do teto: outro uid, outra conta, 2 contratos.

## 7. Contrato para o Kalman e o adpproxy

### 7.1 adpproxy → Kalman (já existe; confirmado em 29/10)

| header | valor | quando |
|---|---|---|
| `X-GQ-Usuario` | uid da sessão | sempre |
| `X-GQ-Ambiente` | `SIMULADOR` ou `REAL`, o ambiente do login no GQ | sempre, **inclusive na ativação do dia** |
| `X-GQ-Segredo` | `KALMAN_ADPPROXY_SEGREDO` | sempre que for REAL (pode ir sempre) |

Nada muda no adpproxy. O responsável confirmou em 29/10 que o `X-GQ-Ambiente` é o do login e que
vai também na ativação do dia.

### 7.2 Kalman → kalman-ordens

**`POST /ordens`**: campo novo `ambiente`, obrigatório na entrada e na saída. É o ambiente da ativação
em que a operação nasceu (`KOTxtAmbiente`).

```json
{"operacao_id": 234500, "uid": 5725, "papel": "ENTRADA", "lado": "C", "qtd": 1, "stop_pts": 500,
 "motivo": "SINAL", "ts_decisao": "2026-11-03T10:15:00.123-03:00", "ambiente": "REAL"}
```

**`POST /sinais`**: `ambiente` no sinal, valendo para todos os itens.
- O Kalman manda um `POST /sinais` por ambiente. O mesmo `sinal_id` pode ir nos dois: a chave passa a
  ser `(modulo, ambiente, sinal_id)`.
- A rajada rotativa é calculada por ambiente, porque as sessões são separadas.

```json
{"sinal_id": "S-20261103-101500", "ts_decisao": "...", "ambiente": "REAL", "entradas": [...]}
```

**`POST /operacoes/{id}/stop` e as ações do admin**: `ambiente` opcional. Vale o gravado, e um valor
diferente gera o alerta `AMBIENTE_PEDIDO_DIVERGENTE`.

**Respostas:** `ambiente` na resposta do `POST /ordens`, no `GET /ordens/{id}`, em cada linha do
`GET /operacoes` e em cada evento. As recusas novas são as do §5.

**`GET /usuarios/{uid}/destino`** (§4.3): consultado na ativação do dia.
- `pode_enviar` falso com `destino` PAPER: a sessão fica no paper.
- `pode_enviar` verdadeiro: as ordens vão ao serviço com aquele `ambiente`.
- O `rotulo` vai para a tela.

**O que muda no Kalman:**
- na ativação, decidir entre paper e ordens pelo `/usuarios/{uid}/destino`;
- mandar `ambiente` em todo pedido;
- filtrar as próprias telas por ele;
- ler o `/saude` por ambiente (`ambientes.SIMULADOR` e `ambientes.REAL`);
- passar `?ambiente=` no `/flexscan/trafego` e no `/sessao/ao-vivo`.

### 7.3 Decisões (29/10)

- **D1 (revisada):** no SIMULADOR, quem decide entre paper e ordens é o `usutxtconfig` (§0.1). O
  9999 com a conta QUANT é paper. Um simulador externo manda as ordens com `ambiente: SIMULADOR`.
  O serviço expõe o destino resolvido (§4.3), e o PnT só opera no REAL.
- **D2:** a .51 roda os dois ambientes: o SIMULADOR (paper para os comuns, simulador externo para
  quem tiver um) e o REAL.
- **D3:** o responsável informa os limites REAL de produção antes do passo 5. As três chaves
  continuam obrigatórias, e o serviço não sobe sem elas.
- **D4:** a Flexscan de produção terá um usuário de API **dedicado ao kalman-ordens**, nunca usado
  para logar no GQ. O GQ redefine os webhooks do usuário em que loga: foi o que apagou o nosso
  cadastro no simulador. O responsável põe as credenciais direto no `service.env` da .51. O IP de
  origem do webhook de produção será observado pelo tráfego (v1.24).

## 8. Plano de migração (a partir de hoje: .53 só SIMULADOR)

Cada passo é um pacote, com o fluxo de sempre:
- branch;
- suíte no worktree limpo;
- binário dali;
- instalação fora do pregão;
- conferência;
- merge.

| passo | o quê | a .53 depois | depende de |
|---|---|---|---|
| **1. v1.25: ambiente no contrato e na operação** | migração 0014 (`ambiente` na operação e na habilitação); campo `ambiente` aceito; `AMBIENTES_PERMITIDOS`, `REAL_UIDS`, `REAL_CONTAS` e os limites REAL; a coluna do GQ pelo ambiente; o PAPER (§0.1); o `GET /usuarios/{uid}/destino` (§4.3); as recusas do §5; `ORDENS_AMBIENTE=HML` equivalente; CLI e API da habilitação por ambiente. Os conectores de hoje são os do SIMULADOR, e o PnT continua aceito nele **só na equivalência** (`ORDENS_AMBIENTE=HML`, que os roteiros usam) | igual a hoje: `SIM` e o padrão SIMULADOR. Um pedido REAL é recusado (`AMBIENTE_NAO_PERMITIDO`) | este SPEC aprovado |
| **2. v1.26: conectores por ambiente** | chaves `REAL_` (as sem prefixo continuam SIMULADOR); duas pontes Flexscan; a sessão FIX só no REAL (`CONECTOR_AMBIENTE` no SIMULADOR); webhook por caminho; posição, conciliação, parada, disjuntor, `/saude`, tráfego e ao vivo por ambiente; os roteiros do PnT passam a rodar no REAL das falsas | igual (as chaves `REAL_` vazias; o PnT já está desligado) | passo 1 |
| **3. Kalman manda `ambiente`** | o contrato do §7.2 no Kalman: o destino na ativação (D1) e o `ambiente` em todo pedido | sai o `AMBIENTE_PADRAO` (sem o campo = recusa). Os testes no simulador da corretora entram pela sessão SIMULADOR | passo 1 (o Kalman pode começar a mandar o campo logo depois do passo 1) |
| **4. REAL de teste na .53** | `AMBIENTES_PERMITIDOS=SIM,REAL`, um uid, uma conta, 1 contrato e as credenciais REAL; o ensaio do §6 no pregão, com o responsável | volta a `SIM` no fim do teste | passos 2 e 3; D4 |
| **5. .51 (PROD)** | instalação, `service.env` de produção, habilitações REAL e os limites de produção; `ORDENS_AMBIENTE` removida (o serviço não sobe com ela) | — | passo 4; D2 e D3 |

**O que a v1.25 (passo 1) entrega e o que fica para a v1.26:**
- Com os conectores ainda num conjunto só, o serviço **não sobe** com o REAL em
  `AMBIENTES_PERMITIDOS`. Antes do passo 2, o REAL só existe na equivalência `ORDENS_AMBIENTE=PRODUCAO`.
- **Prontos:**
  - a leitura e a validação das chaves REAL;
  - o `REAL_UIDS`, o `REAL_CONTAS` e a trava;
  - a habilitação por ambiente;
  - o `/usuarios/{uid}/destino`.
- **Para a v1.26 (passo 2):**
  - os contadores de limite por ambiente;
  - o `AMBIENTE_DIVERGENTE` de ponta a ponta (exige os dois ambientes permitidos).
- **Na equivalência `ORDENS_AMBIENTE=PRODUCAO`,** a habilitação lida é a do REAL. Uma máquina que rode
  com ela precisa de `usuario habilitar -ambiente REAL`. Nenhuma roda hoje.

**O que não muda na transição:**
- os roteiros de hoje;
- o `POST /ordens` sem `ambiente` na .53 até o passo 3;
- as operações e as habilitações gravadas (viram `SIMULADOR`).

## 9. Estimativa pelo ritmo real

**O ritmo medido** (git, 13/10 a 29/10):
- 24 pacotes em 13 dias úteis, com cerca de 42 mil linhas;
- a suíte completa leva uns 12 minutos;
- a instalação é fora do pregão, no mesmo dia.

**A peça mais parecida** é a Flexscan F1–F3: o motor por conector e a ponte REST, com cerca de 6.500
linhas em 3 commits. Ela chegou num dia (23/10) a partir de um SPEC fechado. O passo 2 mexe no mesmo
lugar (o motor assume uma sessão FIX e uma ponte), multiplicado por dois.

| passo | serviço | observação |
|---|---|---|
| 1 | 1 dia | regra, migração, CLI e API; roteiros do contrato |
| 2 | 2 dias | o maior: instâncias por ambiente, conciliação e restart, `/saude`, carga |
| 3 | — (Kalman) | do lado do serviço, só tirar o `AMBIENTE_PADRAO` da .53 |
| 4 | 1 pregão | depende das credenciais REAL (D4) e do responsável presente |
| 5 | meio dia | depende da .51 pronta e de D2/D3 |

**Do lado do serviço, de 4 a 5 dias úteis até o REAL de teste na .53**, se as decisões do §7.3 não
segurarem. O caminho crítico costuma ser externo: as credenciais da Flexscan de produção (D4) e a
mudança do Kalman (passo 3).
