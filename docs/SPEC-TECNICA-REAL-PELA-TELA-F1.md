# SPEC técnica — REAL pela tela, fase 1 (kalman-ordens)

16/11/2026 · base: v1.34 (`911caf424346`) · referência funcional: `docs/SPEC-REAL-PELA-TELA-F1.md`
(aprovada pelo George em 16/11). **Aprovada pelo George em 16/11**, com as respostas do §9 já
incorporadas no texto. Implementação na branch `v1.35`, uma etapa por vez (§10); nada é instalado na
.53 sem roteiro aprovado.

Esta SPEC diz como o kalman-ordens implementa a parte dele da fase 1. Isso inclui a aprovação do
REAL por uid, os estados e a troca de conta, as rotas que a tela consome, o fim do `REAL_UIDS` e do
`REAL_CONTAS`, e a stop na corretora. As telas são do CC do Kalman, sobre estas rotas.

## 0. Resumo

- A aprovação é a linha **REAL** da `kalmanordemusuario`, que já existe por `(uid, ambiente)` desde a
  0014. Ela ganha colunas de estado e do cadastro aprovado. O histórico continua na
  `kalmanordemusuariohist`, só com INSERT. Os limites por uid e o conector já estão nessa linha, e
  o motor já os lê a cada entrada. Só o SIMULADOR continua como hoje.
- A migração **0019** põe toda linha REAL em `SEM_APROVACAO`, com `habilitado=0` e uma linha de
  histórico. Um uid sem linha REAL também conta como `SEM_APROVACAO`.
- Quem decide é um **avaliador de aprovações no nível do serviço**, não da instância REAL. Ele
  funciona com o REAL fora do `AMBIENTES_PERMITIDOS`, o que permite a aceitação "pela tela, fora do
  pregão" com o alerta no Telegram.
- As ordens de uma operação aberta já usam a conta gravada na operação (§4). Isso não muda.
- O serviço não sobe com `REAL_UIDS` ou `REAL_CONTAS` no env.

## 1. Onde a aprovação fica gravada

### 1.1 Tabela

A tabela é a `kalmanordemusuario`, na linha `ambiente='REAL'`. Os campos que já existem mudam de
papel no REAL:

| coluna | no REAL |
|---|---|
| `habilitado` | **derivado**: 1 só com `estado='APROVADO'`, para os leitores antigos (`usuario listar`, v1.34 na volta) |
| `posicao_max`, `qtd_max_ordem` | limites aprovados, iguais aos de hoje: valem junto com os da instância (`entrada.go:336-343`) |
| `conectores` | **o conector aprovado**, um só (`PNT` ou `FLEXSCAN`) |
| `alterado_por`, `motivo`, `alterado_em` | a última mudança, de admin ou do serviço |

Colunas novas (0019), todas com padrão neutro para as linhas do SIMULADOR:

| coluna | tipo | papel |
|---|---|---|
| `estado` | `VARCHAR(20) NOT NULL DEFAULT ''` | `SEM_APROVACAO`, `APROVADO`, `BLOQUEADO`, `CONTA_DIVERGENTE`, `REVOGADO`; `''` no SIMULADOR |
| `corretora` | `INT NULL` | `Robo.CORSeqId` aprovado (85 BTG) |
| `conta` | `VARCHAR(20) NULL` | conta aprovada (`Futuros.CCONroContaFut` ou `Robo.CCONroConta`, a mesma regra de `usuario.go:270`) |
| `versao` | `INT NOT NULL DEFAULT 0` | sobe a cada mudança; a tela manda a versão vista (concorrência otimista) |
| `aprovado_por`, `aprovado_em`, `aprovado_motivo` | `VARCHAR(64)`, `DATETIME(3)`, `VARCHAR(200)`, NULL | a última aprovação (a ficha mostra "por quem, quando, motivo" mesmo depois de um bloqueio) |
| `gq_conector`, `gq_corretora`, `gq_conta` | NULL | o cadastro do GQ visto na última divergência (a ficha e o alerta) |
| `divergente_desde` | `DATETIME(3) NULL` | início da `CONTA_DIVERGENTE` |
| `origem` | `VARCHAR(10) NOT NULL DEFAULT ''` | quem fez a última mudança: `TELA` (rota do serviço), `CLI`, `SERVICO` (transição automática), `MIGRACAO` |

A `kalmanordemusuariohist` ganha as mesmas colunas e mais `evento VARCHAR(20)`: `MIGRACAO`, `APROVAR`,
`BLOQUEAR`, `DESBLOQUEAR`, `REVOGAR`, `LIMITES`, `DIVERGENCIA`, `VOLTOU`. Ela também ganha
`estado_antes`. Cada linha guarda o retrato completo depois da mudança. Nada é apagado nem alterado.

**Por que reaproveitar a tabela e não criar uma `kalmanordemaprovacao`:** o cache
(`usuarios.go:13`), o repositório (`mariadb.go:460-535`), a rota `/usuarios/{uid}/habilitacao` e os
limites da entrada já leem essa linha por `(uid, ambiente)`. Uma tabela nova obrigaria a juntar as
duas em cada entrada e criaria duas fontes para o mesmo uid no REAL, o contrário da D4.

### 1.2 Migração 0019 (`migracoes/0019_aprovacao_real.sql`)

```sql
ALTER TABLE kalmanordemusuario
    ADD COLUMN IF NOT EXISTS estado VARCHAR(20) NOT NULL DEFAULT '' AFTER conectores,
    ADD COLUMN IF NOT EXISTS corretora INT NULL AFTER estado,
    ADD COLUMN IF NOT EXISTS conta VARCHAR(20) NULL AFTER corretora,
    ADD COLUMN IF NOT EXISTS versao INT NOT NULL DEFAULT 0 AFTER conta,
    ADD COLUMN IF NOT EXISTS aprovado_por VARCHAR(64) NULL, ADD COLUMN IF NOT EXISTS aprovado_em DATETIME(3) NULL,
    ADD COLUMN IF NOT EXISTS aprovado_motivo VARCHAR(200) NULL,
    ADD COLUMN IF NOT EXISTS gq_conector VARCHAR(10) NULL, ADD COLUMN IF NOT EXISTS gq_corretora INT NULL,
    ADD COLUMN IF NOT EXISTS gq_conta VARCHAR(20) NULL, ADD COLUMN IF NOT EXISTS divergente_desde DATETIME(3) NULL,
    ADD COLUMN IF NOT EXISTS origem VARCHAR(10) NOT NULL DEFAULT '';
-- kalmanordemusuariohist: as mesmas colunas + evento VARCHAR(20) NOT NULL DEFAULT '' + estado_antes VARCHAR(20) NULL

-- D5: nenhuma habilitação antiga vale (1450, 5725 e qualquer outra linha REAL)
INSERT INTO kalmanordemusuariohist (uid, ambiente, habilitado, posicao_max, qtd_max_ordem, conectores,
        estado, estado_antes, evento, origem, versao, alterado_por, motivo, alterado_em)
    SELECT uid, ambiente, 0, posicao_max, qtd_max_ordem, conectores, 'SEM_APROVACAO',
           IF(habilitado=1,'HABILITADO_V134','DESABILITADO_V134'), 'MIGRACAO', 'MIGRACAO', versao+1,
           'migracao-0019', 'SPEC-REAL-PELA-TELA-F1 D5: nenhuma habilitacao antiga vale', NOW(3)
    FROM kalmanordemusuario WHERE ambiente='REAL' AND estado='';
UPDATE kalmanordemusuario SET habilitado=0, estado='SEM_APROVACAO', origem='MIGRACAO', versao=versao+1,
       alterado_por='migracao-0019', motivo='SPEC-REAL-PELA-TELA-F1 D5: nenhuma habilitacao antiga vale', alterado_em=NOW(3)
    WHERE ambiente='REAL' AND estado='';
```

- **Idempotente:** `IF NOT EXISTS` e `WHERE estado=''`. Rodada de novo, não muda nada nem duplica o
  histórico. O INSERT vem antes do UPDATE pelo mesmo filtro.
- **Os limites antigos ficam na linha** (o 1/1 do 1450 de 13/11), mas não valem. A aprovação pela
  tela regrava os limites.
- O SIMULADOR fica intocado (`estado=''`).
- O retrato do banco instalado (`cmd/kalman-ordens/testdata/instalado/`) ganha a 0019, como em
  toda migração.
- A volta para a v1.34 com a 0019 aplicada continua possível: a v1.34 lê colunas nomeadas, e as
  linhas REAL ficam com `habilitado=0`.

## 2. Estados no caminho da entrada

### 2.1 Onde é avaliado

Hoje, no REAL, `pedirEntrada` (`entrada.go:23`) passa por:
- `tetoAmbiente`: `REAL_UIDS`, em `ambiente.go:48`;
- `verificarHabilitacao`: `habilitado`, em `entrada.go:237`;
- `contaParaEntrada`: conta, conector e `REAL_CONTAS`, em `entrada.go:96` e `usuario.go:252`.

Com a mudança, no REAL:

1. `teto` (`ambiente.go:37`) perde o teste de `REAL_UIDS`. Fica só `AMBIENTES_PERMITIDOS` e a
   instância.
2. `verificarHabilitacao` vira `verificarAprovacao(uid)` para `amb == REAL`. Lê o estado do cache,
   antes dos campos do pedido, na mesma posição de hoje:

| estado | código da recusa | `motivo` (ASCII, a tela mostra como vem) |
|---|---|---|
| sem linha / `SEM_APROVACAO` | `REAL_SEM_APROVACAO` | `REAL nao aprovado para este usuario` |
| `BLOQUEADO` | `REAL_BLOQUEADO` | `REAL bloqueado pelo admin: <motivo do bloqueio>` |
| `REVOGADO` | `REAL_REVOGADO` | `REAL revogado pelo admin: precisa de nova aprovacao` |
| `CONTA_DIVERGENTE` | `REAL_CONTA_DIVERGENTE` | `conta alterada no GQ (aprovada <corretora>/<conta>, atual <corretora>/<conta>): aguardando aprovacao` |
| `APROVADO`, com a `prd` ilegível | `REAL_CADASTRO_ILEGIVEL` | `cadastro ilegivel no GQ (<o que falta>): entradas recusadas ate o cadastro ser corrigido` |
| `APROVADO` | segue | — |

3. Em `contaParaEntrada`, no REAL e com `APROVADO`, o cadastro resolvido da `prd` é **comparado** com
   o aprovado (conector, corretora, conta). Se diferir, transita para `CONTA_DIVERGENTE` (§3) e
   recusa com `REAL_CONTA_DIVERGENTE`. Se for igual, a operação grava a **conta aprovada**. É o
   mesmo valor, mas a fonte passa a ser a aprovação (D "a conta que vale é sempre a aprovada").
   `ResolverCom` deixa de olhar `RealContas` (`usuario.go:252`). A trava `CONTA_AMBIENTE` (conta
   de simulador no REAL) continua.
4. Os limites por uid continuam em `verificarLimites` (`entrada.go:336`), com `qtd_max_ordem` e
   `posicao_max` da linha aprovada.

Nenhuma dessas recusas conta para a parada automática (são `simples`, não `limite`). Cada tentativa
gera um alerta **não crítico**, uma vez por uid e estado (`REAL_RECUSA_ESTADO`): com a tela
desabilitando o "Enviar ordens", uma entrada aqui é um defeito do outro lado.

### 2.2 O `/destino`

`GET /usuarios/{uid}/destino` (`destinoDaEntrada`) devolve os mesmos códigos e motivos na mesma
ordem, sem alerta. É o que a tela usa para o `pode_enviar=false`. **Mudança de contrato:** no REAL,
`REAL_UID_NAO_PERMITIDO`, `REAL_CONTA_NAO_PERMITIDA` e `UID_NAO_HABILITADO` deixam de existir e
dão lugar aos quatro acima. O CC do Kalman confere se a tela testa código específico (§8).

## 3. A troca de conta

### 3.1 Quando compara

O cadastro do GQ é comparado com o aprovado em três momentos:
- **a cada entrada REAL** (§2.1 item 3, pelo cache);
- **a cada recarga do cache** (30 s, `CACHE_USUARIOS_S`);
- **a cada leitura da ficha ou do `/destino`**, que relê o GQ na hora.

A recarga passa a carregar a `prd` também dos uids com linha REAL em `APROVADO`, `CONTA_DIVERGENTE`
ou `BLOQUEADO`. Hoje carrega só a dos `habilitado` (`usuarios.go:47`).

Quem persiste e alerta a transição é o **avaliador do serviço** (`internal/motor/aprovacao.go`, novo).
Ele roda no laço do `Servico` (`servico.go`), com a leitura própria dos uids REAL, e por isso existe
com ou sem a instância REAL. A instância REAL, quando existe, avalia na entrada e pede ao avaliador a
transição. O avaliador é um só, com uma gravação por transição.

A comparação usa uma função pura nova, `usuario.Cadastro(texto) (conector, corretora, conta, falha)`.
Ela lê os mesmos campos de `configGQ` e nunca o JSON inteiro:

- **`Robo.Conector` ausente no GQ:** vale o aprovado. É o comportamento de hoje com um só conector
  habilitado (`usuario.go:214`).
- **Conta:** `Futuros.CCONroContaFut`, e sem ela `Robo.CCONroConta` (a regra de hoje).
- **`prd` ilegível** (vazia, JSON inválido, sem `Robo`, `CORSeqId` ou conta) — decisão 3: **não
  muda o estado**. A entrada nova é recusada com `REAL_CADASTRO_ILEGIVEL` e motivo próprio ("cadastro
  ilegivel"), e sai o crítico `REAL_CADASTRO_ILEGIVEL` (chave por uid), que se resolve quando a
  `prd` volta a ser lida. A posição aberta segue na conta da operação. A ficha mostra `gq.erro`.
- **GQ indisponível** (erro de banco): **não transita**. Uma queda do banco não vira divergência
  em massa.

### 3.2 Transições automáticas

| de | condição | para | grava | alerta |
|---|---|---|---|---|
| `APROVADO` | cadastro ≠ aprovado | `CONTA_DIVERGENTE` | linha + hist `DIVERGENCIA` (`por=servico`), `gq_*`, `divergente_desde` | crítico `REAL_CONTA_DIVERGENTE`, chave `REAL_CONTA_DIVERGENTE:<uid>` |
| `CONTA_DIVERGENTE` | cadastro = aprovado | `APROVADO` | hist `VOLTOU` (`por=servico`), `gq_*` e `divergente_desde` limpos | `RESOLVIDO: REAL_CONTA_DIVERGENTE` |
| `CONTA_DIVERGENTE` | cadastro ≠ aprovado, mas mudou de novo | igual | `gq_*` atualizados, sem hist novo a cada 30 s (hist só se mudou) | o texto do crítico passa a ter o atual novo |
| `BLOQUEADO` / `REVOGADO` | cadastro ≠ aprovado | igual | `gq_*` atualizados (a ficha mostra) | nenhum: o estado do admin vale |
| `APROVADO` / `CONTA_DIVERGENTE` | `prd` ilegível | igual | nada | crítico `REAL_CADASTRO_ILEGIVEL:<uid>`, resolvido quando volta a ler |

O alerta crítico repete como todo crítico (30 s por 2 min, depois a cada 5 min) até resolver. O texto
é em ASCII, com o prefixo `[REAL]`:

```
uid 1450: cadastro do GQ diferente do aprovado. Aprovado PNT / BTG (85) / conta 4582351; atual PNT / BTG (85) / conta 1234567.
Entradas novas recusadas ate nova aprovacao (ou o cadastro voltar). Posicao aberta: 1 WINZ26 na operacao 234754, segue na conta 4582351 (stop 173835 viva).
```

Sem posição: `Sem posicao aberta.` O `CONTA_ALTERADA_COM_POSICAO` (`saida.go:511`, a cada 60 s
por operação) **não é emitido no REAL**: a posição está no texto acima. No SIMULADOR, ele continua
como hoje.

### 3.3 Volta a `APROVADO` pelo admin

Por aprovar o cadastro novo (`aprovar`, §5), que grava o atual como o aprovado, com motivo e uma
versão nova. Não há outro caminho (funcional: "qualquer outro caminho pede nova aprovação").

## 4. Resposta: as ordens de uma operação aberta usam a conta gravada

**Usam a conta gravada na operação, sem reler o cadastro.** Confirmado no código:

- A conta é lida **uma vez**, na entrada (`entrada.go:96`), e congelada na operação em
  `entrada.go:135` (`Conta: conta.Conta, Destino: conta.Destino, Conector: conta.Conector`). O
  comentário em `entrada.go:95` diz: "lidos só aqui e congelados na operação (v1.2 U4)".
- **Toda mensagem ao PnT** sai com `idDe(op)` (`motor.go:189-191`): `Conta: op.Conta` no tag 1 e no
  116. Isso vale para:
  - a entrada e a 1014 nova (`entrada.go:554`, `pnt.NovaOrdem(m.idDe(op), …)`);
  - o cancelamento (`saida.go:443`);
  - o replace da 1014 e o ajuste da stop (`saida.go:470`, `pnt.Replace(m.idDe(op), …)`).

  A saída usa os mesmos construtores.
- Cada ordem nasce com `Conta: op.Conta` (`entrada.go:539`). A reconstituída na volta também
  (`conciliacao.go:394`). A Flexscan confere a conta da ordem contra a da operação
  (`ponte.go:1744`).
- No restart, a operação volta do banco com a conta gravada (`kalmanordemoperacao.conta`).
- O cadastro só é relido com posição aberta em `conferirConta` (`saida.go:511-531`), e só para
  **alertar** (`CONTA_ALTERADA_COM_POSICAO`), nunca para mandar ordem.

Nada a mudar aqui nesta entrega. Um teste novo (§7.1, `motor`) passa a fixar isso para as quatro
mensagens.

Fora da conta, uma observação para a D9: o **20858/20859** (`RobotID`, `EnteringFirm`) vem da instância
(`m.p.RobotID`, `motor.go:191`), não da operação. Para o Day Trade × Position dos beta testers, o
OMS terá de ser congelado na operação, como a conta.

## 5. Rotas novas

Valem para todas:
- JSON, textos de resposta em ASCII;
- `por` é o uid do admin, que o proxy do KalmanWIN preenche e confere o perfil, como nas ações de
  hoje (`servico.py:1288`);
- `motivo` é obrigatório nas escritas, com 3 a 200 caracteres;
- as rotas escrevem no banco pelo avaliador do serviço (§3.1), com ou sem a instância REAL;
- toda escrita roda numa transação com `SELECT … FOR UPDATE` na linha, confere a `versao` mandada
  (`409 VERSAO_MUDOU` se outra pessoa mudou antes), grava a linha e o histórico, invalida o cache
  do uid (`invalidarUsuario`) e publica `ADMIN_ACAO`.

Erros comuns:

| HTTP | `codigo` | quando |
|---|---|---|
| 400 | `CAMPO_INVALIDO` | uid, motivo, `por`, limites |
| 409 | `TRANSICAO_INVALIDA` | ação não permitida no estado (motivo traz o estado atual) |
| 409 | `VERSAO_MUDOU` | a `versao` mandada não é a atual |
| 409 | `CADASTRO_MUDOU` | aprovar: o GQ agora não é o que o admin viu |
| 422 | `CADASTRO_INVALIDO` | aprovar: a `prd` não resolve (corretora desconhecida, conta de simulador, conector desconhecido) |
| 422 | `LIMITE_ACIMA_DA_INSTANCIA` | limite do uid maior que o `REAL_LIMITE_*` da instância |
| 422 | `LIMITES_INSTANCIA_AUSENTES` | aprovar/limites sem `REAL_LIMITE_QTD_ORDEM` ou `REAL_LIMITE_POSICAO_MAX` no env |
| 503 | `CONFIG_USUARIO_INDISPONIVEL` | banco do GQ ou do serviço ilegível |

Transições do admin:

| ação | de | para |
|---|---|---|
| `aprovar` | `SEM_APROVACAO`, `REVOGADO`, `CONTA_DIVERGENTE` | `APROVADO` |
| `bloquear` | `APROVADO`, `CONTA_DIVERGENTE` | `BLOQUEADO` |
| `desbloquear` | `BLOQUEADO` | `APROVADO`, ou `CONTA_DIVERGENTE` se o GQ já difere (com o alerta) |
| `revogar` | `APROVADO`, `BLOQUEADO`, `CONTA_DIVERGENTE` | `REVOGADO` |
| `limites` | `APROVADO`, `BLOQUEADO`, `CONTA_DIVERGENTE` | o mesmo, com versão nova |

`aprovar` em `BLOQUEADO` é recusado: primeiro desbloquear (funcional: sai só por desbloquear). Nenhuma
ação zera ou cancela ordem (funcional: revogar ou bloquear não zera).

### 5.1 `GET /usuarios/{uid}/real?historico=50` — a ficha

```json
{"uid": 1450, "estado": "CONTA_DIVERGENTE", "versao": 4,
 "aprovado": {"conector": "PNT", "corretora_codigo": 85, "corretora": "BTG", "conta": "4582351",
              "qtd_max_ordem": 1, "posicao_max": 1,
              "por": "1450", "em": "2026-11-17T18:20:01.123-03:00", "motivo": "fase 1, conta conferida na BTG"},
 "gq": {"conector": "PNT", "corretora_codigo": 85, "corretora": "BTG", "conta": "1234567",
        "lido_em": "2026-11-18T10:01:12.004-03:00", "erro": null},
 "divergencia": {"desde": "2026-11-18T10:00:41.870-03:00", "campos": ["conta"]},
 "ultima_mudanca": {"evento": "DIVERGENCIA", "por": "servico", "em": "...", "motivo": "..."},
 "acoes": ["aprovar", "bloquear", "revogar", "limites"],
 "limites_instancia": {"qtd_ordem": 1, "posicao_max": 1, "perda_dia_pts": 600, "entradas_dia": 30, "entradas_min": 2},
 "posicao": {"total": 1, "operacoes": [{"operacao_id": 234754, "modulo": "KALMAN", "conta": "4582351", "posicao": 1}]},
 "historico": [{"id": 88, "evento": "DIVERGENCIA", "estado_antes": "APROVADO", "estado": "CONTA_DIVERGENTE", "versao": 4,
                "conector": "PNT", "corretora_codigo": 85, "conta": "4582351", "qtd_max_ordem": 1, "posicao_max": 1,
                "gq": {"conector": "PNT", "corretora_codigo": 85, "conta": "1234567"},
                "por": "servico", "motivo": "cadastro do GQ diferente do aprovado", "em": "..."}]}
```

- Sem linha REAL: `estado` vem `SEM_APROVACAO`, com `aprovado: null` e `versao: 0`.
- O `gq` é relido na hora (como o `/destino`, `ambiente.go:96`).
- `posicao` vem da instância REAL quando existe; sem ela, vem `null` e `instancia: "DESLIGADA"`.
- `limites_instancia` vem do env, com ou sem o REAL permitido; a chave ausente vem `null`.
- `historico` vem do mais recente para o mais antigo.

### 5.2 `GET /usuarios/real` — a lista

Ela existe já nesta fase porque a tela do admin precisa achar quem tem linha. A tela da lista
completa é dos beta testers:

```json
{"usuarios": [{"uid": 1450, "estado": "APROVADO", "conta": "4582351", "corretora": "BTG", "conector": "PNT",
               "qtd_max_ordem": 1, "posicao_max": 1, "versao": 3, "alterado_em": "...", "divergente": false}]}
```

Ela traz só os uids com linha REAL. O 5725 aparece como `SEM_APROVACAO` depois da 0019.

### 5.3 Escritas: `POST /usuarios/{uid}/real/{acao}`

`acao` ∈ `aprovar`, `bloquear`, `desbloquear`, `revogar`, `limites`.

```json
// aprovar
{"por": 1450, "motivo": "fase 1, conta conferida na BTG", "versao": 2,
 "visto": {"conector": "PNT", "corretora_codigo": 85, "conta": "4582351"},
 "qtd_max_ordem": 1, "posicao_max": 1}
// bloquear | desbloquear | revogar
{"por": 1450, "motivo": "...", "versao": 3}
// limites
{"por": 1450, "motivo": "...", "versao": 3, "qtd_max_ordem": 1, "posicao_max": 1}
```

Resposta 200:

```json
{"aceito": true, "codigo": "APROVADO", "motivo": "uid 1450 aprovado no REAL: PNT / BTG / 4582351, limites 1/1",
 "estado": "APROVADO", "versao": 3}
```

- **`aprovar`** relê o GQ na hora. O `visto` tem de ser igual ao relido, senão `409 CADASTRO_MUDOU`
  com o atual no motivo: o admin aprova o que viu, nunca o que mudou depois.
- **A conta aprovada é sempre a do GQ.** Não existe aprovar uma conta digitada.
- **Os limites** são obrigatórios na `aprovar` e na `limites`, maiores que 0 e **no máximo os da
  instância** (decisão 5): `qtd_max_ordem ≤ REAL_LIMITE_QTD_ORDEM` e `posicao_max ≤
  REAL_LIMITE_POSICAO_MAX`, senão `422 LIMITE_ACIMA_DA_INSTANCIA` com os dois valores no motivo.
  Os `REAL_LIMITE_*` são lidos do env **também com o REAL fora do `AMBIENTES_PERMITIDOS`** (hoje
  eles só são lidos com o REAL permitido). Sem eles no env, a aprovação é recusada com
  `422 LIMITES_INSTANCIA_AUSENTES`.

Códigos de sucesso: `APROVADO`, `BLOQUEADO`, `DESBLOQUEADO` (ou `CONTA_DIVERGENTE` no desbloquear),
`REVOGADO`, `LIMITES_ALTERADOS`.

### 5.4 A rota antiga `POST /usuarios/{uid}/habilitacao?ambiente=REAL`

Ela responde `409 USE_APROVACAO_REAL` ("no REAL, use /usuarios/{uid}/real/bloquear|desbloquear") e
não grava nada. Hoje o proxy chama essa rota com o ambiente (`admin.py:541`). Assim o botão
"bloquear usuário" da tela atual falha de forma explícita, em vez de mexer num `habilitado` que
deixou de mandar. No SIMULADOR, ela continua igual. O `GET` da mesma rota no REAL passa a trazer
`estado` e `aprovado`, sem mudar os campos de hoje.

### 5.5 A stop na corretora: `stop_corretora` em cada operação

É um campo novo em `descrever(op)` (`saude.go:127`). Ele aparece em `GET /ordens/{id}`,
`GET /operacoes` e no `/saude`, os caminhos que a ficha e a tela do usuário já leem. O CC do
Kalman confirma qual dos três usa.

```json
"stop_corretora": {"estado": "VIVA", "clordid": "KM-P-234754-2", "orderid37": "dsa.…",
                   "preco_stop": 173835, "preco_limite": 173735, "qtd": 1,
                   "preco_stop_pedido": null, "disparada": false, "desde": "2026-11-17T10:31:02.441-03:00"}
```

| `estado` | regra (sobre a 1014 viva `p = op.viva(Protecao)`) | cor sugerida |
|---|---|---|
| `VIVA` | `p` confirmada (`ATIVA`/`PENDENTE`/`PARCIAL`) e `p.pend == semPendencia` | normal |
| `AJUSTANDO` | `p.pend == pendReplace` (o `G` saiu e não foi confirmado), ou `p` em `ENVIANDO`/`SEM_CONFIRMACAO` | amarelo |
| `DESPROTEGIDA` | `posição ≠ 0`, nenhuma 1014 viva e **nenhuma saída em curso** | vermelho |
| `SAINDO` | `posição ≠ 0`, sem 1014 viva, com saída em curso (a stop foi cancelada de propósito para sair) | neutra (decisão 2) |
| `SEM_POSICAO` | `posição == 0` | — |

- `preco_stop` e `preco_limite` são os **confirmados pela corretora** (`o.Stop` e `o.StopLim`, que
  só mudam no `39=5`, `recebe.go:517`).
- Em `AJUSTANDO`, `preco_stop_pedido` traz o pedido (`o.stopPend`).
- `disparada` é `!p.disparadaEm.IsZero()`: a filha já está no mercado.
- `desde`: em `DESPROTEGIDA`, é `op.semProtecaoDesde`.
- **O alerta da `DESPROTEGIDA`** já existe: `SEM_PROTECAO`, crítico depois de 2 s, e
  `PROTECAO_RECRIADA`. Não entra alerta novo.

Também entra `GET /usuarios/{uid}/stop?ambiente=REAL`, com o `stop_corretora` das operações vivas
do uid, para a tela do usuário não precisar filtrar `/operacoes`:

```json
{"uid": 1450, "ambiente": "REAL", "operacoes": [{"operacao_id": 234754, "modulo": "KALMAN", "posicao": 1, "stop_corretora": {}}]}
```

## 6. Fim do `REAL_UIDS` e do `REAL_CONTAS`

Em `config.LerAmbientes` (`ambientes.go`):

- **Qualquer** valor não vazio em `REAL_UIDS` ou `REAL_CONTAS`, com ou sem o REAL permitido, faz o
  serviço não subir com:
  `REAL_UIDS/REAL_CONTAS nao existem mais (v1.35, SPEC-REAL-PELA-TELA-F1 D4): a aprovacao do REAL e
  pela tela; tire as duas chaves do service.env`.
  O `conferirConfig` da subida já para com erro de config, e o systemd mostra a linha no journal.
- Elas saem da lista obrigatória (`ambientes.go:107`) e dos avisos (`ambientes.go:140`). Os campos
  `RealUIDs`, `RealUIDsTodos`, `RealContas` e `RealContasTodas` saem de `config.Ambientes`, de
  `usuario.Regras` e do log `ambientes` (`main.go:152`).
- **Equivalência `ORDENS_AMBIENTE=PRODUCAO`** (`ambientes.go:64`), que hoje põe "todos" nos dois:
  passa a exigir a aprovação como o REAL. Ela não é usada na .53 (`ordens_ambiente=""`).
- **CLI:** `usuario habilitar|desabilitar -ambiente REAL` responde `use a tela (kalman-admin):
  a aprovacao do REAL e por /usuarios/{uid}/real`. O `usuario listar` mostra `estado`, `conta` e
  `versao` nas linhas REAL. **Emergência** (decisão 4): `usuario revogar -uid N -motivo "…"`
  (o REAL é implícito). Só restringe: de `APROVADO`, `BLOQUEADO` ou `CONTA_DIVERGENTE` para
  `REVOGADO`, nunca o contrário; em `SEM_APROVACAO` ou `REVOGADO` não grava nada. Entra no
  histórico com `origem=CLI` e `alterado_por` = o usuário do sistema que rodou (`SUDO_USER`, senão
  `USER`). Sem `versao` (é emergência: vale sobre o que estiver). O efeito chega à entrada em até
  30 s (a CLI grava direto no banco, como hoje), ou na hora se a instância REAL relê pela rota.
- **Fica no env:** o `AMBIENTES_PERMITIDOS` e os `REAL_LIMITE_*`, como na D2.
  `REAL_FLEXSCAN_<NOME>_CONTAS` (`canais_flexscan.go:118`) é do REAL pela Flexscan, fora desta
  fase, e continua como está.

**Atenção na instalação:** o `service.env` da .53 **tem hoje** `REAL_UIDS` e `REAL_CONTAS`. A subida
de 14/11 avisou "ignorada: REAL fora de AMBIENTES_PERMITIDOS". Sem tirar as duas antes do restart, a
v1.35 não sobe. O roteiro de instalação faz isso pelo método de sempre (cópia, arquivo novo, `diff`
filtrado, `cp`).

## 7. Testes

### 7.1 Unitários (Go, na suíte)

| pacote | o que prova |
|---|---|
| `internal/config` (`ambientes_test.go`) | `REAL_UIDS` ou `REAL_CONTAS` não vazios → erro com a mensagem do §6, com e sem o REAL permitido; o REAL permitido sem eles sobe; `PRODUCAO` sem "todos" |
| `internal/usuario` | `Cadastro()`: os três campos; `Futuros` × `Robo`; `Conector` ausente = aprovado; JSON inválido, vazio e sem `Robo` = `ilegivel`; nunca devolve credencial. `ResolverCom` sem `RealContas`, com a trava `CONTA_AMBIENTE` intacta |
| `internal/motor` (`aprovacao_test.go`, novo) | a matriz completa de transições do §5 (permitidas e `TRANSICAO_INVALIDA`); `VERSAO_MUDOU`; `CADASTRO_MUDOU`; `LIMITE_ACIMA_DA_INSTANCIA`; as automáticas do §3.2, incluindo "GQ indisponível não transita" e "BLOQUEADO não vira divergente"; o histórico com um INSERT por transição e nenhum a cada recarga sem mudança |
| `internal/motor` (a conta congelada, item 4) | operação aberta no REAL com a conta A; troca para B no GQ; o ajuste da stop (`G`), um cancelamento (`F`), a 1014 recriada (`D`) e a saída levam `1=A` e `116=A`; `CONTA_DIVERGENTE` e o crítico com a posição; sem `CONTA_ALTERADA_COM_POSICAO` no REAL |
| `internal/motor` (`stop_corretora`) | `VIVA`, `AJUSTANDO` (com `preco_stop_pedido`), `DESPROTEGIDA`, `SAINDO`, `SEM_POSICAO`, `disparada` |
| `internal/repo` (MariaDB em container) | a 0019 numa base com linhas REAL `habilitado=1` e `0` e linhas SIMULADOR: REAL → `SEM_APROVACAO`, `habilitado=0`, uma linha `MIGRACAO` cada; SIMULADOR intocado; rodar duas vezes não muda nada; a escrita com `FOR UPDATE` e versão |
| `cmd/kalman-ordens` | o retrato `instalado` com 19 migrações; a CLI `habilitar -ambiente REAL` recusada; `listar` com o estado; a subida recusada com `REAL_UIDS` (o teste de subida que já existe) |

### 7.2 Roteiros (`cenarios/*.fix`, PnT e Flexscan falsos)

- **132-aprovacao-real:** sem linha → `REAL_SEM_APROVACAO` na entrada e no `/destino`; aprovar com
  `visto` errado → `CADASTRO_MUDOU`; aprovar → entrada aceita com a conta aprovada; bloquear →
  `REAL_BLOQUEADO`, e a 1014 e a saída da operação aberta seguem; desbloquear; revogar →
  `REAL_REVOGADO`; aprovar de novo; limites 1/1 → a 2ª entrada barra em `POSICAO_MAXIMA`.
- **133-troca-de-conta-com-posicao:** o item 4 de ponta a ponta, mais a volta automática e o
  `RESOLVIDO`.
- **134-stop-na-corretora:** os estados do `stop_corretora` ao longo de entrada, ajuste,
  cancelamento pelo PnT e recriação, e saída.
- **Os seis roteiros que usam `REAL_UIDS`/`REAL_CONTAS`** (119, 124, 125, 126, 127, 131): trocam as
  chaves por um comando novo `aprova UID conta=… conector=… qtd=… pos=…` no executor
  (`internal/cenario/executor.go`, ao lado de `habilita`). O resultado esperado não muda.
- O roteiro 11 e os demais do SIMULADOR passam iguais: é a prova de que o SIMULADOR não mudou.

Tudo pelo `scripts/longo.sh` (suíte completa, os dois backends), log em `var/`, em worktree limpo
da branch `v1.35`.

### 7.3 Roteiro na .53 com o REAL desligado (o "SIMULADOR" da aceitação)

Com a v1.35 instalada fora do pregão, `AMBIENTES_PERMITIDOS=SIM`, sem `REAL_UIDS`/`REAL_CONTAS`, e
o SIMULADOR rodando como hoje:

1. A recusa com `REAL_UIDS`, sem restart: numa cópia do env com a chave de volta,
   `sudo sh -c 'set -a; . <copia>; kalman-ordens usuario conferir -uid 1450'`. A CLI lê o teto
   pelo mesmo `LerAmbientes` (`usuario.go:180`) e recusa com a mensagem do §6.
2. `GET /usuarios/real`: o 1450 e o 5725 em `SEM_APROVACAO`, com a linha `MIGRACAO` no histórico.
3. `GET /usuarios/1450/destino?ambiente=REAL`: `AMBIENTE_NAO_PERMITIDO`, porque o teto vem antes.
   A ficha (`/usuarios/1450/real`) mostra `SEM_APROVACAO` e o GQ atual.
4. Pela tela, ou pelo `curl` do roteiro: aprovar o 1450 (4582351, 1/1); bloquear; desbloquear;
   revogar; aprovar de novo; cada passo com o motivo no histórico.
5. **A troca de conta, no 1450** (decisão 1: é admin, tem cadastro completo, e na .53 só o Kalman
   usa esse cadastro). O **George** troca a conta na tela "Perfil do usuário – Robô" do GQ, o
   caminho real. O roteiro nunca grava na `usutxtconfigprd` por SQL nem por script. Antes, anota-se
   o cadastro original pela ficha (`gq`). Em até 30 s: estado `CONTA_DIVERGENTE`, crítico `[REAL]`
   no Telegram, a ficha com aprovado × atual. Depois o George volta o cadastro ao original pela
   mesma tela, e o roteiro confere: a ficha com `gq` igual ao anotado, o estado de volta a
   `APROVADO`, o `RESOLVIDO` no Telegram e as linhas `DIVERGENCIA` e `VOLTOU` no histórico.
   Funciona com o REAL desligado porque o avaliador é do serviço (§3.1).
6. O SIMULADOR não muda: a entrada da Flexscan do 1450, a abertura das 08:02 e a tela atual
   (Registradas, Fio do dia).

Os itens "no REAL, 1 contrato" da aceitação (stop viva igual à da BTG, restart com posição,
sobrevivência da stop) ficam para o 1º dia REAL, pelo roteiro dele.

## 8. Riscos e o que pode quebrar

| risco | efeito | como evitar |
|---|---|---|
| `REAL_UIDS`/`REAL_CONTAS` ainda no `service.env` da .53 | **a v1.35 não sobe** | passo explícito do roteiro de instalação, antes do restart; a volta (v1.34) não precisa delas com o REAL desligado |
| o roteiro de 13/11 §9 (CLI `habilitar -ambiente REAL` + `REAL_UIDS`) | passa a falhar | reescrito pelo roteiro do 1º dia REAL; marcado como obsoleto no `ESTADO` |
| a tela atual: "bloquear/desbloquear usuário" no REAL (`admin.py:541`) | `409 USE_APROVACAO_REAL` | o CC do Kalman troca pelas rotas do §5; até lá, bloquear o REAL é pela Parada BLOQUEAR |
| o `/destino` com códigos novos (§2.2) | se a tela testa `REAL_UID_NAO_PERMITIDO` por nome, o motivo pode sair genérico | o CC do Kalman confere; o `pode_enviar` já é falso para qualquer código |
| a 1ª recarga depois da 0019 com o 1450 em `SEM_APROVACAO` | o 1450 não manda REAL até a aprovação | é o desejado (D5) |
| a volta para a v1.34 depois da 0019 | as linhas REAL ficam `habilitado=0`; a v1.34 exige `REAL_UIDS` para o REAL | o REAL fica desligado na volta; a aprovação refeita pela tela some para a v1.34 (ela lê só `habilitado`, que é 1 com `APROVADO`) |
| o avaliador do serviço e a instância REAL decidindo a mesma transição | dupla gravação | a transição grava com `FOR UPDATE` + `versao`; a segunda vê o estado novo e não faz nada |
| a `prd` relida a cada 30 s para mais uids | carga | um SELECT por recarga para todos os uids REAL (já é em lote, `mariadb_lote.go:256`) |
| o crítico da divergência repetindo enquanto o usuário edita o Perfil no GQ | ruído | a volta automática resolve; o texto diz o que fazer |
| o 13/11 (conta congelada, limites, conciliação, 1014) | não muda: o caminho do PnT e as mensagens são os mesmos | os roteiros 01–131 continuam e a §7.1 fixa a conta das quatro mensagens |
| o SIMULADOR | só a recarga lê a coluna `estado` (vazia) | os roteiros do SIMULADOR passam iguais |

## 9. Estimativa e o que preciso do George

**Estimativa** (CC do ordens, só o backend):

| etapa | tempo |
|---|---|
| implementação | 2 a 3 sessões de trabalho: a 0019 e o repositório; o avaliador e as transições; as rotas; o `stop_corretora`; config e CLI |
| testes | +1 sessão: unitários, os três roteiros novos e o ajuste dos seis |
| suíte completa pelo `longo.sh` | ~1 h de máquina por rodada |
| roteiro de instalação e conferência na .53 (§7.3), fora do pregão | 1 sessão |

Total realista: **4 a 5 sessões**, mais o tempo das suas revisões. As telas (CC do Kalman) vêm depois.

**Decisões do George (16/11), incorporadas acima:**
1. Uid de teste da troca de conta: o **1450**, com a troca feita pelo George na tela do GQ e o
   passo de volta conferido; nada grava na `usutxtconfigprd` por SQL ou script (§7.3 item 5).
2. **`SAINDO`** no cartão da stop, em cor neutra; `DESPROTEGIDA` só sem 1014 viva e sem saída em
   curso (§5.5).
3. **`prd` ilegível:** recusa entradas novas com `REAL_CADASTRO_ILEGIVEL` ("cadastro ilegível") e
   alerta; o estado não muda e a posição aberta segue na conta da operação (§2.1, §3.1, §3.2).
4. **Revogar pela CLI em emergência:** sim, só restringe, com `origem=CLI` no histórico (§6).
5. **Limite do uid preso ao da instância:** acima, a aprovação é recusada com o motivo (§5.3).
6. **`REAL_LIMITE_ENTRADAS_DIA=30` e `REAL_LIMITE_ENTRADAS_MIN=2`** nesta fase (entram no
   `service.env` pelo roteiro de instalação).
7. **Comandos com sudo** ficam no roteiro de instalação, que o George lê antes.

## 10. Etapas da implementação

Uma etapa por vez, cada uma com os próprios testes verdes antes da próxima, na branch `v1.35`
(worktree `../kalman-ordens-v135`):

| etapa | conteúdo |
|---|---|
| E1 | a 0019 e o retrato `instalado`; o repositório (ler, gravar com `FOR UPDATE` + `versao`, histórico); `usuario.Cadastro`; o fim do `REAL_UIDS`/`REAL_CONTAS` na config; os `REAL_LIMITE_*` lidos sem o REAL permitido |
| E2 | os estados na entrada e no `/destino`; o avaliador do serviço (recarga, transições automáticas, alertas `REAL_CONTA_DIVERGENTE` e `REAL_CADASTRO_ILEGIVEL`); o `CONTA_ALTERADA_COM_POSICAO` fora do REAL; a CLI (`habilitar -ambiente REAL` recusado, `revogar`, `listar`) |
| E3 | as rotas do §5 (ficha, lista, escritas, a antiga com 409); o `stop_corretora` e `/usuarios/{uid}/stop` |
| E4 | os roteiros 132–134 e o ajuste dos seis; a suíte completa pelo `longo.sh`; o roteiro de instalação para o George |
