# SPEC — cadastro de contas, boleta do admin e migração em massa (desenho, 01/11/2026)

**Revisão 01/11 (tarde):** respostas P1–P7 incorporadas (§5); a `acparam` real (§1.2–1.3); a boleta
revista pela P4 e implementada na v1.31 (`docs/SPEC-ORDENS-v1.31.md`), que substitui o §2 abaixo.

**Revisão 03/11:** o "Conta nao contratada" vem do OMS do BTG, não da Flexscan, e quer dizer que a
conta não tem o Flexscan contratado na corretora. Isso virou o pré-requisito de cada conta (§0.1). A
ferramenta avisa dele (§3.1, §3.3), e o relatório da migração separa essas contas como pendência na
corretora (§4). O teste REAL de 03/11 está pausado até o BTG liberar a 4582351.

Base: v1.30 (instalada na .53). Três ferramentas, nesta ordem de entrega:

| # | ferramenta | onde | pacote | prazo |
|---|---|---|---|---|
| 1 | **boleta do admin** | no serviço (`POST /ordens` com `modulo: BOLETA` + CLI); a tela de admin depois | v1.31 | antes do pregão de terça, 03/11 (segunda é Finados: B3 fechada) |
| 2 | **cadastro de conta** | binário separado `flexscan-cadastro` | cadastro v1 | depois da v1.31 |
| 3 | **migração em massa** | subcomando do `flexscan-cadastro` | cadastro v1.1 | quando a execução for só pela Flexscan |

A boleta vem primeiro porque o teste REAL de terça vai por ela. A conta 4582351 já está criada e
associada ao `btg_gammaquant` (feito à mão em 01/11), então o teste não depende da ferramenta 2.

## 0. O que aprendemos na produção (01/11, `btg_gammaquant`, conta 4582351)

- **O cadastro tem duas etapas, nesta ordem:**
  1. **criar a conta com os dados do titular:** `POST /accounts` no endereço de cadastro da AWS
     (`https://2nqoqyemr1.execute-api.sa-east-1.amazonaws.com/v1`), com o token do admin e a
     `x-api-key`. Corpo: `{"accounts":[{"name","email","cblc","docType","docNumber"}]}`;
  2. **associar ao guarda-chuva:** `POST /users/{user}/accountlinks` no apidma (`DELETE` desassocia;
     `GET /users/{user}/accounts` lista).
- **Associar antes de criar** gera um registro provisório `D-<conta>` sem o titular, e as ordens são
  recusadas com "Invalid client document".
- **Na ordem, a conta vai como o número puro** (`4582351`). É o que a ponte já manda (`account`).
- **"Conta nao contratada" (corrigido em 03/11):** a ordem é aceita pela Flexscan (202) e depois
  rejeitada pelo **OMS da corretora** com esse texto. Isso acontece quando a conta não tem o Flexscan
  contratado na corretora do cliente. Não é um prazo de liberação da Flexscan (a leitura de 01/11, "só
  no dia seguinte", estava errada), e não é um erro do cadastro: a conta pode estar criada e associada
  certo e mesmo assim ser recusada.
- A doc (`docs/referencia/flexscan-cadastro-1.0.0.json`) descreve o `POST /accounts` como "echo
  temporário". O contrato real é o da AWS (P2):
  - `POST https://2nqoqyemr1.execute-api.sa-east-1.amazonaws.com/v1/accounts` → **201**
    `{"successfully":[{"name":"…","accountCode":"4582351","rlp":"0"}],"errors":[]}`;
  - no apidma, a mesma chamada dá 403;
  - `cblc` = a própria conta (número puro), `docType` 1 = CPF e `docNumber` só com dígitos;
  - o token é o do `POST /connect/login` do apidma com o admin da corretora (`btg_gmadmin` na BTG),
    sempre com a `x-api-key`.
- **O `accountlinks` (associar, desassociar, listar)** vai ao apidma, com o token do admin e a
  `x-api-key` (P3). Na AWS a rota não existe (erro do próprio gateway). A senha do guarda-chuva não é
  usada pelo cadastro.

### 0.1 Pré-requisito de cada conta: o Flexscan contratado na corretora

Antes de qualquer ordem, **o cliente tem de ter o Flexscan contratado na corretora dele** (no BTG,
para as contas do `btg_gammaquant`). Esse contrato é feito pelo cliente, ou por nós, com a corretora.
Ele não passa pela Flexscan nem pelo cadastro. Cadastrar e associar a conta na Flexscan é necessário,
mas não basta.

- **Sem o contrato:** o cadastro e a associação funcionam. A primeira ordem é aceita (202) e depois
  vem a rejeição "Conta nao contratada": no serviço de ordens, o `RECUSA_FLEXSCAN` e a operação
  `ENCERRADA_SEM_ENTRADA`.
- **Ninguém confirma o contrato antes da ordem:** nem a Flexscan nem a corretora têm uma rota de
  consulta que conheçamos (P8). O sinal é a recusa, ou a confirmação da corretora.
- **Quem resolve é a corretora.** Para nós a conta fica como **pendência na corretora**, nunca como
  erro do cadastro.

## 1. Parâmetros: a `acparam` (GQ)

### 1.1 O que fica onde

| onde | o quê | quem lê | quando vale |
|---|---|---|---|
| `acparam` (GQ, MariaDB) | tudo do cadastro (revisão 01/11): as URLs da AWS e do apidma, as corretoras, o guarda-chuva, o admin e a senha de cada uma, o código da `x-api-key`, o tipo de documento padrão e os lotes | `flexscan-cadastro` | a cada execução (nada fixo no código; mudar não exige nada) |
| a DSN do GQ para o `flexscan-cadastro` | `/etc/flexscan-cadastro/gq.dsn` (root, 600): um usuário de banco só com `SELECT` em `acparam` | só o `flexscan-cadastro` | a cada execução |
| `service.env` do kalman-ordens | o de hoje: URLs, credenciais e contas permitidas do caminho das ordens | o serviço | **só com restart, de propósito** |

- O `flexscan-cadastro` **nunca** lê o `service.env`, e o serviço **nunca** lê os parâmetros `FXC_*`.
- A senha do admin e a `x-api-key` não vão para log, relatório, banco nem saída de erro (mesma
  máscara da ponte).
- Como a DSN é root 600, o `flexscan-cadastro` roda com `sudo` (o usuário roda; eu não tenho sudo). Ele
  recusa subir se a permissão for mais aberta que 600 ou o dono não for root.
- **O serviço de ordens continua só com o `service.env`** (P6). Se um dia ler a `acparam`, os
  parâmetros que mudam o caminho das ordens continuam valendo só no restart.

### 1.2 A `acparam` (definição de 01/11)

```
PRMCODPARAM  char(12) PK   o código do parâmetro (único no GQ inteiro)
PRMTXTPARAM  varchar(100)  a descrição
PRMTXTVALOR  varchar(2048) o valor, em texto
PRMDATATUAL  datetime      a última alteração
PRMTXTUNID   char(1)       'S' nas linhas do GQ (copiamos)
PRMTXTMODULO char(4)       o módulo ('ADM' nas do GQ)
CiaCodCia, USUCodUsuario   NULL (parâmetro global)
```

- **O código tem só 12 caracteres e é único no GQ inteiro.** Por isso todos os nossos códigos começam
  com `FXC_`, e o módulo é `FXC` (`PRMTXTMODULO`). A ferramenta lê só `PRMTXTMODULO = 'FXC'`, com
  `CiaCodCia` e `USUCodUsuario` nulos.
- **Só leitura:** o `flexscan-cadastro` não grava na `acparam`. Quem cria e muda as linhas é o GQ (ou o
  usuário, pelo SQL).
- A ferramenta lê os parâmetros a cada execução, e o relatório mostra o `PRMDATATUAL` de cada um.
- Um parâmetro obrigatório ausente ou ilegível faz a ferramenta parar antes de chamar a Flexscan, com o
  código no erro.

### 1.3 Os códigos (módulo `FXC`)

| `PRMCODPARAM` | exemplo | o quê |
|---|---|---|
| `FXC_URLCAD` | `https://2nqoqyemr1.execute-api.sa-east-1.amazonaws.com/v1` | a AWS (criar conta) |
| `FXC_URLAPI` | `https://apidma.flexscan.com.br/v1` | login, `accountlinks`, `accounts` |
| `FXC_CORRET` | `85:BTG` | as corretoras, no formato do `REAL_FLEXSCAN_CORRETORAS` (o NOME aqui com até 5 letras, para caber no código abaixo) |
| `FXC_GC_<NOME>` | `FXC_GC_BTG` = `btg_gammaquant` | o guarda-chuva da corretora |
| `FXC_DOCTIPO` | `1` | o `docType` padrão (1 = CPF) |
| `FXC_LOTE` | `20` | contas por lote na migração |
| `FXC_PAUSA_S` | `5` | pausa entre lotes, em segundos |
| `FXC_TIMEOUT` | `20` | tempo máximo por chamada, em segundos |
| `FXC_ADM_<NOME>` | `FXC_ADM_BTG` = `btg_gmadmin` | o usuário admin da corretora (login no apidma) |
| `FXC_SEN_<NOME>` | `FXC_SEN_BTG` | a senha desse admin (decisão de 01/11: na `acparam`) |
| `FXC_KEY_<NOME>` | `FXC_KEY_BTG` = `APIKEYFXBTG`; `FXC_KEY_CMC` = `APIKEYFLEX` | o **código** da `acparam` que guarda a `x-api-key` da corretora (decisão de 01/11: a chave fica na `acparam`, uma por corretora) |

```sql
INSERT INTO acparam (PRMCODPARAM, PRMTXTPARAM, PRMTXTVALOR, PRMDATATUAL, PRMTXTUNID, PRMTXTMODULO) VALUES
 ('FXC_URLCAD',  'Flexscan cadastro: URL da AWS (criar conta)', 'https://2nqoqyemr1.execute-api.sa-east-1.amazonaws.com/v1', NOW(), 'S', 'FXC'),
 ('FXC_URLAPI',  'Flexscan cadastro: URL do apidma',            'https://apidma.flexscan.com.br/v1', NOW(), 'S', 'FXC'),
 ('FXC_CORRET',  'Flexscan cadastro: corretoras (cod:NOME)',    '85:BTG,88:CMC',                      NOW(), 'S', 'FXC'),
 ('FXC_GC_BTG',  'Flexscan cadastro: guarda-chuva da BTG',      'btg_gammaquant',              NOW(), 'S', 'FXC'),
 ('FXC_DOCTIPO', 'Flexscan cadastro: docType padrao (1=CPF)',   '1',                           NOW(), 'S', 'FXC'),
 ('FXC_LOTE',    'Flexscan cadastro: contas por lote',          '20',                          NOW(), 'S', 'FXC'),
 ('FXC_PAUSA_S', 'Flexscan cadastro: pausa entre lotes (s)',    '5',                           NOW(), 'S', 'FXC'),
 ('FXC_TIMEOUT', 'Flexscan cadastro: timeout por chamada (s)',  '20',                          NOW(), 'S', 'FXC'),
 ('FXC_ADM_BTG', 'Flexscan cadastro: usuario admin BTG',        'btg_gmadmin',                 NOW(), 'S', 'FXC'),
 ('FXC_SEN_BTG', 'Flexscan cadastro: senha do admin BTG',       '<senha>',                     NOW(), 'S', 'FXC'),
 ('FXC_ADM_CMC', 'Flexscan cadastro: usuario admin CMC',        '<usuario admin CMC>',         NOW(), 'S', 'FXC'),
 ('FXC_SEN_CMC', 'Flexscan cadastro: senha do admin CMC',       '<senha>',                     NOW(), 'S', 'FXC'),
 ('FXC_KEY_BTG', 'Flexscan cadastro: codigo da x-api-key BTG',  'APIKEYFXBTG',                 NOW(), 'S', 'FXC'),
 ('FXC_KEY_CMC', 'Flexscan cadastro: codigo da x-api-key CMC',  'APIKEYFLEX',                  NOW(), 'S', 'FXC');
```

- **A `x-api-key`** está na `acparam`, uma por corretora (`APIKEYFXBTG` na BTG, `APIKEYFLEX` na CM
  Capital). O `FXC_KEY_<NOME>` diz o código, e a ferramenta lê a chave por ele. A chave nunca aparece
  em log, relatório ou erro.
- **O admin de cada corretora e a senha dele** também ficam na `acparam` (`FXC_ADM_<NOME>`,
  `FXC_SEN_<NOME>`). Com isso, não existe mais arquivo de segredos: só a DSN do GQ (§1.1). A ferramenta
  nunca mostra a senha, nem a `x-api-key`, e só precisa de leitura na `acparam`.

## 2. Ferramenta 1 — boleta do admin (v1.31)

Revista pela P4 e implementada: **`docs/SPEC-ORDENS-v1.31.md`**.
- É o `POST /ordens` com `modulo: "BOLETA"`, e o uid do cliente tem de estar habilitado no ambiente.
- Valem todas as travas e os limites desse uid (`REAL_UIDS`, `REAL_CONTAS`, a coluna do GQ...).
- O `por` (o admin) é obrigatório.
- A `conta` e a `corretora` informadas conferem a coluna do GQ (`BOLETA_DESTINO_DIVERGENTE`).
- Saíram do desenho original `BOLETA_ADMINS`, `BOLETA_LIMITE_QTD` e o `POST /boleta`.
- O passo a passo de terça está em `deploy/LEIA.md` §1.20.

## 3. Ferramenta 2 — cadastro de conta (`flexscan-cadastro`)

Binário próprio (`cmd/flexscan-cadastro`), sem acesso ao banco do kalman-ordens; lê só a `acparam` (pela DSN do §1.1).

### 3.1 Comandos

```
flexscan-cadastro listar      -corretora BTG
flexscan-cadastro conferir    -corretora BTG -conta 4582351
flexscan-cadastro cadastrar   -corretora BTG -conta 4582351 -nome "..." -email ... -doc 12345678901 [-doc-tipo 1] [-ensaio]
flexscan-cadastro desassociar -corretora BTG -conta 4582351 -motivo "..."
```

- **`listar`:** `GET /users/{guarda-chuva}/accounts`. Marca as contas `D-<conta>` como provisórias
  (associadas antes de criadas).
- **`conferir`:** associada ou não, provisória ou não. A Flexscan não tem rota que diga se a conta já
  tem o Flexscan contratado na corretora (§0.1, P8). O `conferir` mostra a data do nosso cadastro e o
  aviso do pré-requisito: "a conta só opera com o Flexscan contratado no BTG; sem ele, a 1ª ordem é
  recusada com 'Conta nao contratada' (pendência na corretora)". Se o `registro.jsonl` tiver uma
  recusa dessas para a conta (§4, `pendencias`), o `conferir` a mostra, com a data.
- **`cadastrar`:**
  1. faz o login do admin da corretora no apidma;
  2. cria a conta (`POST /accounts` na AWS). **Só passa à etapa seguinte com a criação confirmada:**
     201 com a conta em `successfully[].accountCode` e `errors` vazio. Qualquer item em `errors` para
     a ferramenta e mostra o texto;
  3. associa (`POST accountlinks`);
  4. relista e confere que a conta aparece pura, sem o `D-`.

  Se a conta já estiver associada como `D-<conta>`, o `cadastrar` mostra o conserto (desassociar o
  `D-`, criar, associar) e só o faz com `-consertar`.
- **`desassociar`:** `DELETE accountlinks` e relista. Não apaga a conta criada (a doc não tem rota
  para isso).
- **`-ensaio`** (em todos os que gravam): faz o login, lê e mostra o que faria, sem gravar.
- Todo comando que grava pede confirmação ("sim"), como a boleta.
- **O aviso do pré-requisito (§0.1):** o `cadastrar` mostra o aviso **antes** do "sim" e o repete no
  fim, junto da linha do `service.env`. O texto é "pré-requisito: o Flexscan contratado no <corretora>
  para a conta <conta>; sem ele, a 1ª ordem é recusada com 'Conta nao contratada'". A ferramenta não
  bloqueia por isso, porque não tem como conferir.

### 3.2 Registro

- Cada chamada que grava vai para `/var/lib/flexscan-cadastro/registro.jsonl` (root, 600). Cada linha
  traz quando, comando, corretora, conta, etapa, status HTTP, a resposta da Flexscan e quem rodou
  (`SUDO_USER`).
- **Nome, e-mail e documento vão mascarados no registro e na tela** (`***.456.789-**`). Eles só
  aparecem inteiros no corpo enviado.
- O arquivo é a fonte do "desde quando" do `conferir`.

### 3.3 Depois de cadastrar

Cadastrar na Flexscan **não** libera a conta no serviço de ordens. A conta ainda tem de entrar em
`REAL_FLEXSCAN_<NOME>_CONTAS` (e em `REAL_CONTAS`, se não for `*`) no `service.env`, e o serviço tem
de ser reiniciado. O `cadastrar` termina mostrando a linha exata a acrescentar e a data a partir da
qual a conta opera.

A conta só opera de fato com o Flexscan contratado na corretora (§0.1). A ordem de teste, pela boleta
(SPEC-ORDENS-v1.31), é o que confirma. Uma recusa "Conta nao contratada" nessa ordem é uma pendência na
corretora: a conta fica no `service.env` e não se desfaz o cadastro.

## 4. Ferramenta 3 — migração em massa

```
flexscan-cadastro migrar  -arquivo contas.csv [-ensaio] [-lote N]
flexscan-cadastro reverter -relatorio relatorio-AAAAMMDD-HHMM.csv [-ensaio]
```

- **Entrada:** CSV com `corretora;conta;nome;email;doc_tipo;doc`. O `doc_tipo` vazio vale o
  `FXC_DOCTIPO`, e o `doc` vai só com os dígitos.
- **Validação antes de qualquer chamada:**
  - corretora conhecida;
  - conta numérica;
  - documento com dígito verificador válido (CPF/CNPJ);
  - e-mail com formato válido;
  - conta repetida no arquivo.

  Uma linha inválida para tudo (`-pular-invalidas` só relata e segue).
- **Ensaio:** a validação, o login e a lista atual de cada guarda-chuva. O relatório diz o que seria
  feito em cada conta: criar e associar; já associada; provisória a consertar; inválida.
- **Execução:** em lotes de `FXC_LOTE` (da `acparam`), com `FXC_PAUSA_S` entre eles. Uma conta
  falhada não para o lote.
- **O aviso do pré-requisito (§0.1):** o ensaio e a execução abrem com ele, por corretora: "N contas
  no <corretora>: cada uma só opera com o Flexscan contratado no <corretora>; as sem contrato aparecem
  em PENDENTE_CORRETORA".
- **Relatório por conta** (CSV e JSON):
  - corretora, conta e doc mascarado;
  - as etapas (`criada`, `associada`, `conferida`), com o resultado e o texto da Flexscan;
  - a **situação** da conta, uma de:

    | situação | o quê | de quem é a ação |
    |---|---|---|
    | `OK` | criada, associada e conferida | ninguém |
    | `JA_ASSOCIADA` | já estava associada, pura, antes desta execução | ninguém |
    | `ERRO_CADASTRO` | falha na criação, na associação ou na conferência (com o texto) | nós: corrigir e repetir |
    | `INVALIDA` | a linha não passou na validação | nós: corrigir o arquivo |
    | `PENDENTE_CORRETORA` | recusada com "Conta nao contratada": o Flexscan não está contratado na corretora | a corretora/o cliente; **não é erro do cadastro** |

  - no fim, um resumo por situação e por corretora. As `PENDENTE_CORRETORA` vão numa lista própria
    (`pendentes-corretora-AAAAMMDD-HHMM.csv`: corretora, conta, a data e o texto da recusa), para
    mandar à corretora;
  - a linha `REAL_FLEXSCAN_<NOME>_CONTAS` de cada corretora para o `service.env`, com as `OK`, as
    `JA_ASSOCIADA` e também as `PENDENTE_CORRETORA`. A conta pendente está cadastrada certo e passa a
    operar quando a corretora liberar, sem mexer de novo no cadastro nem no `service.env`.
- **De onde vem a `PENDENTE_CORRETORA`:**
  - **No cadastro:** se alguma resposta da Flexscan, da criação ou da associação, trouxer "Conta nao
    contratada" (sem diferença de maiúsculas e acentos), a conta vai para `PENDENTE_CORRETORA`, não
    para `ERRO_CADASTRO`.
  - **Depois da migração,** porque a recusa normalmente só aparece na 1ª ordem:
    `flexscan-cadastro pendencias -relatorio R [-desde AAAA-MM-DD]` lê, só para leitura, as recusas do
    serviço de ordens da mesma máquina pelo HTTP local. São os `RECUSA_FLEXSCAN` com "Conta nao
    contratada", pela conta e pela corretora, no histórico das operações (`GET /operacoes`). O comando
    atualiza a situação dessas contas no relatório, gera a lista da corretora e grava no
    `registro.jsonl`.
  - Uma conta `PENDENTE_CORRETORA` que depois opera (uma ordem executada) volta a `OK` no próximo
    `pendencias`.
- **O `reverter` não desassocia as `PENDENTE_CORRETORA` por padrão:** o cadastro delas está certo. Só
  com `-incluir-pendentes`.
- **Volta (`reverter`):** desassocia as contas que o relatório marca como associadas por aquela
  execução. As já associadas antes ficam onde estavam.
- A execução pode ser repetida sobre o mesmo arquivo: a conta já associada (pura) é pulada, e a
  `PENDENTE_CORRETORA` continua pendente, sem nova chamada.

## 5. Respostas (01/11)

- **P1:** o `btg_gammaquant` é só do Kalman (criado na sexta; o GX não usa o BTG pela Flexscan, e no
  GQ o BTG é PnT). Terça roda na .53 **sem cadastrar webhook**, com os fills pela consulta.
- **P2:** a resposta do `POST /accounts` está no §0. Os dados: o token do `/connect/login` do apidma
  com o `btg_gmadmin`, mais a `x-api-key`; `cblc` = a conta; `docType` 1 = CPF; `docNumber` só com
  dígitos.
- **P3:** o `accountlinks` usa o token do admin e a `x-api-key`, no apidma.
- **P4:** a boleta exige um uid habilitado no ambiente da ordem (terça: o 5725 com a conta 4582351),
  com as travas e os limites dele. O admin fica no `por`.
- **P5:** a tela de admin fica para depois de terça.
- **P6:** o serviço de ordens continua só com o `service.env`.
- **P7:** respondida em 03/11. O "Conta nao contratada" não é prazo da Flexscan: é o OMS do BTG
  recusando uma conta sem o Flexscan contratado na corretora (§0.1).

**Pergunta aberta (03/11):**
- **P8:** a corretora (BTG) ou a Flexscan têm um jeito de consultar, antes de uma ordem, se a conta tem
  o Flexscan contratado (uma rota, um relatório ou uma lista)? Se tiverem, o `conferir` e o ensaio da
  migração passam a mostrar isso, e a `PENDENTE_CORRETORA` aparece antes da 1ª ordem.

**Resolvido em 01/11:**
- a `x-api-key`: na `acparam`, uma por corretora (o `FXC_KEY_<NOME>` aponta o código);
- o admin e a senha: `FXC_ADM_<NOME>` e `FXC_SEN_<NOME>`;
- o apidma: `https://apidma.flexscan.com.br/v1`.

**Consequência:** sem arquivo de segredos, quem lê a `acparam` lê a senha do admin e a `x-api-key`. O
acesso do `flexscan-cadastro` ao GQ deve ser um usuário de banco só com `SELECT` na `acparam`.

## 6. Plano

| passo | o quê | quando |
|---|---|---|
| 1 | v1.31: a boleta (CLI; `modulo: BOLETA` no `POST /ordens`; roteiro 127) | 01/11; instalar na .53 antes de terça |
| 2 | o teste REAL de terça pela boleta (`deploy/LEIA.md` §1.20) | 03/11: **pausado**. A 4582351 foi recusada com "Conta nao contratada" (o BTG sem o Flexscan contratado). Repetir o §3 da `INSTALACAO-2026-11-01-v1.31.md` quando o BTG liberar |
| 3 | `flexscan-cadastro` v1: `listar`, `conferir`, `cadastrar`, `desassociar` | depois do passo 2, com os códigos `FXC_*` na `acparam` |
| 4 | `flexscan-cadastro` v1.1: `migrar` e `reverter` | depois do 3 |
| 5 | a boleta na tela de admin | depois do 2 |
