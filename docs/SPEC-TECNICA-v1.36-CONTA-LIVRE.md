# SPEC técnica v1.36: a conta livre (revisão de D3, D6 e D10)

16/11/2026, para o George aprovar antes de codar. Base: `SPEC-REAL-PELA-TELA-F1.md`, com D3, D6 e D10
revistas no fim da tarde de 16/11, e a v1.35 instalada (`09872ead98f2`). A SPEC técnica da v1.35
(`SPEC-TECNICA-REAL-PELA-TELA-F1.md`) continua valendo no que esta não muda.

Em uma frase: a aprovação passa a dizer só "este uid pode usar o REAL". Ela guarda limites, quem,
quando, motivo e histórico. A conta, a corretora e o conector vêm do cadastro do GQ a cada entrada,
sem comparação. O cadastro inutilizável recusa a entrada e avisa uma vez.

## 1. O que sai da v1.35

| sai | onde está hoje | na v1.36 |
|---|---|---|
| conta e corretora na aprovação | `aprovar` grava `corretora`/`conta` com o que o GQ tinha; a entrada manda nessa conta | `aprovar` não grava conta nem corretora. A entrada usa a do GQ |
| o `visto` do aprovar | `CADASTRO_MUDOU` (409) se o GQ mudou entre a ficha e o clique | aceito e ignorado (a tela F1 manda). Sem `CADASTRO_MUDOU` |
| a comparação a cada 30 s | `aprovacao.DecidirCadastro` grava a transição pelo avaliador | sai. O verificador do §2.3 só lê, não grava nada |
| o estado `CONTA_DIVERGENTE` | transição automática, `habilitado=0` | não é mais gerado. Fica como valor antigo no histórico |
| o alerta `REAL_CONTA_DIVERGENTE` | crítico por uid, repete | sai do catálogo |
| a recusa `REAL_CONTA_DIVERGENTE` | entrada e `/destino` | sai |
| `desbloquear` que volta divergente | resposta `codigo=CONTA_DIVERGENTE` | `desbloquear` sempre volta a `APROVADO` |
| `REAL_CADASTRO_ILEGIVEL` (recusa e alerta) | só o JSON ilegível | absorvido por `REAL_CADASTRO_INUTILIZAVEL` (§2) |

**As colunas não saem.** As colunas `corretora`, `conta`, `gq_conector`, `gq_corretora`, `gq_conta`
e `divergente_desde` da `kalmanordemusuario` e da `...hist` ficam no banco, e a v1.36 deixa de usá-las:
- o `aprovar` grava `NULL` nelas;
- as outras ações (`bloquear`, `limites`...) copiam o que estava lá, como a v1.35 já fazia;
- a leitura ignora essas colunas.

As linhas antigas do histórico, inclusive as de `CONTA_DIVERGENTE` do teste de 16/11, saem na ficha
como estão, só como registro.

**O conector continua na aprovação.** A coluna `conectores` é "conector permitido ao usuário" (D10).
O 1450 tem `PNT` e o 5725 tem `FLEXSCAN`. Detalhe e pergunta no §8 (P1).

**Um uid em `CONTA_DIVERGENTE` na instalação:**
- hoje não há nenhum: o 1450 está `APROVADO` v5 e o 5725 está em `SEM_APROVACAO`;
- pode aparecer até a instalação, porque o avaliador da v1.35 roda com o REAL desligado e o 1450
  pode trocar a conta no GQ.

A migração **0020** trata esse caso:
- passa toda linha REAL em `CONTA_DIVERGENTE` para `APROVADO`, com `habilitado=1`;
- grava uma linha `MIGRACAO` no histórico: "v1.36: a conta é livre (D6); CONTA_DIVERGENTE extinto";
- é idempotente, como a 0019;
- não muda o esquema: é só `UPDATE` + `INSERT` no histórico.

Por que `APROVADO`: o admin aprovou o uid, e a única coisa que o tirou do estado foi a conta, que
deixou de importar. O "Enviar ordens" do dia continua desligado até o usuário ligar.

O crítico `REAL_CONTA_DIVERGENTE` em memória some com o restart, sem `RESOLVIDO`. O roteiro de
instalação avisa isso.

O `CONTA_ALTERADA_COM_POSICAO` continua fora do REAL (D6: trocar a conta não gera alerta) e
continua no SIMULADOR, como hoje. A posição aberta segue na conta gravada na operação; isso já era
assim e não muda (§3, roteiro 133).

## 2. D10: o cadastro inutilizável

### 2.1 O que é inutilizável

A regra é uma só, `cadastroUtilizavel(uid)`. A entrada, o `/destino`, a ficha e o verificador
chamam a mesma função, para que a recusa e o alerta nunca discordem. Ela lê a `usutxtconfigprd`
pelo cache de 30 s e resolve como a entrada REAL resolveria.

| causa (texto em `motivo`) | código técnico (`causa`) | hoje |
|---|---|---|
| JSON vazio ou ilegível, sem `Robo.CORSeqId` | `CADASTRO_ILEGIVEL` | igual |
| uid sem linha em `acusuario` | `SEM_CADASTRO` | igual |
| corretora que o kalman-ordens não conhece (nem 85/88 nem `REAL_FLEXSCAN_CORRETORAS`), ou 9999 no REAL | `CORRETORA_NAO_ATENDIDA` | igual |
| **PnT com corretora diferente de 85** ("pelo PnT, só BTG") | `CORRETORA_NAO_ATENDIDA` | **novo**: hoje passa e a BTG recusaria |
| Flexscan por corretora sem credencial nesta máquina | `CORRETORA_NAO_ATENDIDA` | igual (`CORRETORA_SEM_CREDENCIAL`) |
| conector fora dos permitidos ao uid, ou sem `Robo.Conector` com dois permitidos | `CONECTOR_NAO_PERMITIDO` | igual |
| conector que a máquina não tem (Flexscan sem URL) | `CONECTOR_NAO_PERMITIDO` | igual |
| conta vazia, fora da regra do conector, ou de simulador no REAL | `CONTA_INVALIDA` | igual |
| conta fora da lista do guarda-chuva Flexscan (`REAL_FLEXSCAN_<C>_CONTAS`) | `CONTA_INVALIDA` | igual. Não está na lista do D10, mas a Flexscan recusaria; ver P3 |

O banco indisponível (`CONFIG_USUARIO_INDISPONIVEL`) não é cadastro inutilizável: continua como hoje.

### 2.2 Os códigos de recusa (entrada e `/destino`, só REAL)

| código | quando | muda? |
|---|---|---|
| `AMBIENTE_NAO_PERMITIDO` | REAL fora de `AMBIENTES_PERMITIDOS` (o teto vem primeiro) | não |
| `REAL_SEM_APROVACAO` / `REAL_BLOQUEADO` / `REAL_REVOGADO` | pelo estado | não |
| **`REAL_CADASTRO_INUTILIZAVEL`** | aprovado, mas o cadastro não serve. `motivo`: "cadastro do GQ nao utilizavel: corretora 107 nao atendida"; campo novo `causa` com o código técnico | **novo** |

Ordem: teto → estado → cadastro. Um uid bloqueado com o cadastro ruim recebe `REAL_BLOQUEADO`.

O código único substitui, **só no REAL**, os códigos que a entrada devolvia por cadastro:
`DESTINO_INVALIDO`, `CONECTOR_NAO_HABILITADO`, `CONECTOR_INDEFINIDO`, `CONECTOR_NAO_CONFIGURADO`,
`CONTA_INVALIDA`, `CONTA_AMBIENTE`, `CONFIG_USUARIO_INVALIDA`, `CORRETORA_SEM_CREDENCIAL`,
`CONTA_FORA_DA_CORRETORA` e `REAL_CADASTRO_ILEGIVEL`. O antigo vai em `causa`. O SIMULADOR não muda.

### 2.3 Quando sai o alerta e quando sai o RESOLVIDO

O alerta é `REAL_CADASTRO_INUTILIZAVEL`, com uma chave por uid. Quem o emite é o **verificador**,
que substitui o avaliador:
- roda na instância principal a cada `CACHE_USUARIOS_S` (30 s), como hoje, com ou sem a instância
  REAL;
- olha só as linhas REAL em `APROVADO`;
- só lê; não grava nada no banco.

A entrada recusada não alerta. Ela só cutuca o verificador (como a v1.35 já faz), para o alerta não
esperar 30 s.

| evento | Telegram |
|---|---|
| uid `APROVADO` e o cadastro passa a inutilizável | **uma** mensagem: uid, a causa, o cadastro lido e "posicao aberta segue na conta da operacao" quando houver |
| continua inutilizável (passes seguintes, novas entradas recusadas) | nada |
| muda a causa, ainda inutilizável (ex.: corretora 107 → conta vazia) | nada. O texto guardado é atualizado e a ficha mostra a causa atual |
| volta a ser utilizável | `RESOLVIDO: REAL_CADASTRO_INUTILIZAVEL: uid 1450` |
| o uid sai do `APROVADO` (bloqueado, revogado) com o alerta aberto | `RESOLVIDO ... (uid fora do APROVADO)`, para não ficar aberto para sempre |
| restart com o cadastro ainda inutilizável | **uma** mensagem de novo, com "(visto na partida)" (ver P2) |

**Um só por ocorrência** vem de uma classe nova no pacote `alerta`, a "uma vez":
- o alerta é guardado pela chave, como um crítico: a segunda emissão é ignorada e o `Resolver` manda
  o `RESOLVIDO`;
- o `Tick` não repete o alerta;
- ele aparece em `/saude` `alertas_criticos` enquanto está aberto;
- o `docs/ALERTAS.md` ganha a coluna "repete".

No REAL, o cadastro ruim deixa de emitir os alertas antigos:
- o `USUARIO_CONFIG`, que hoje sai a cada entrada recusada;
- o `CONTA_AMBIENTE`, um crítico que repete.

Os dois continuam valendo no SIMULADOR.

O `REAL_RECUSA_ESTADO` continua: um aviso por uid, estado e versão quando uma entrada chega num
estado que a tela já desabilita.

## 3. Roteiros afetados

| roteiro | o que muda |
|---|---|
| **133** troca de conta com posição | reescrito. O uid 7 está em `APROVADO`, com posição na conta 12345, e troca para 7654321 no GQ: nenhum alerta, nem `REAL_CONTA_DIVERGENTE` nem `CONTA_ALTERADA_COM_POSICAO`; o `/destino` diz `pode_enviar=true` com a conta 7654321. O ajuste da stop (35=G), a 1014 recriada, o cancelamento (35=F) e a saída (35=D) continuam com `1=12345`/`116=12345`. Depois de `ZERADA`, a entrada nova sai com `1=7654321`. Restart com a posição aberta: a operação volta na 12345. |
| **119** dois ambientes | o uid 10 (aprovado, com outra conta no GQ) passa a entrar na conta do GQ. Ele vira um caso de cadastro inutilizável (corretora 107): `REAL_CADASTRO_INUTILIZAVEL` com `causa=CORRETORA_NAO_ATENDIDA` |
| **125** REAL por corretora | sai o `aprova 8 conta=9002` depois da troca: a troca para a BTG/9002 já vale sozinha. A saída da 12503 continua pela CMC/715124. A conta fora do guarda-chuva (uid 11) passa a `REAL_CADASTRO_INUTILIZAVEL` com `causa=CONTA_INVALIDA` |
| **126** corretora sem credencial | `CORRETORA_SEM_CREDENCIAL` → `REAL_CADASTRO_INUTILIZAVEL` com `causa=CORRETORA_NAO_ATENDIDA` |
| **132** rotas da aprovação | sai o bloco do GQ trocado durante o bloqueio, do desbloquear divergente e do aprovar do cadastro novo. Sai o `CADASTRO_MUDOU`. O `aprovar` sem `visto` passa a ser aceito. O aprovar com o cadastro inutilizável é aceito, com o aviso (§4). A ficha tem `aprovado` sem conta |
| **39** conta HML em produção | na equivalência `ORDENS_AMBIENTE` (REAL): `CONTA_AMBIENTE` → `REAL_CADASTRO_INUTILIZAVEL`, `causa=CONTA_INVALIDA`, sem o crítico que repete |
| **135 (novo)** cadastro inutilizável | uid aprovado com corretora 107: <ul><li>entrada recusada com o motivo;</li><li>**um** alerta, sem repetição em 5 min de relógio;</li><li>duas entradas a mais sem alerta novo;</li><li>a causa muda para conta vazia: sem mensagem;</li><li>o cadastro volta: `RESOLVIDO` e a entrada passa;</li><li>de novo inutilizável e restart: uma mensagem "(visto na partida)";</li><li>bloquear com o alerta aberto: `RESOLVIDO (uid fora do APROVADO)`;</li><li>PnT com corretora 88: `CORRETORA_NAO_ATENDIDA`.</li></ul> |
| **134** stop na corretora, 118, 124 | sem mudança esperada; a suíte confirma |

O verbo `aprova` dos roteiros passa a aceitar `conta=`/`corretora=` só para simular uma linha
gravada pela v1.35 (o 133 usa isso para provar que a conta antiga é ignorada). Sem eles, ele grava
como a v1.36 grava.

## 4. O contrato das rotas (para o CC do Kalman)

**`GET /usuarios/{uid}/real` (a ficha):**
- `aprovado`: continua um objeto sempre que houve aprovação, com `qtd_max_ordem`, `posicao_max`,
  `por`, `em`, `motivo` e o novo `conectores` (lista). Os campos `conector`, `corretora_codigo`,
  `corretora` e `conta` passam a vir `null`.
- `divergencia`: sempre `null`. O campo fica no corpo nesta versão e sai na próxima.
- `gq`: igual, só informação. O `erro` ganha o `causa` do §2.1 e `codigo=REAL_CADASTRO_INUTILIZAVEL`.
- `historico[]`: igual. Os campos `conta` e `gq` vêm `null` nas linhas novas e preenchidos nas
  antigas.
- `acoes`: `CONTA_DIVERGENTE` some dos estados. As ações por estado são as mesmas, sem ele.

**`POST /usuarios/{uid}/real/aprovar`:**
- corpo: `por`, `motivo`, `versao`, `qtd_max_ordem`, `posicao_max` e o opcional `conectores`;
- o `visto` é aceito e ignorado;
- sem `CADASTRO_MUDOU` e sem `CADASTRO_INVALIDO`. O aprovar com o cadastro inutilizável é
  aceito, e o `motivo` da resposta avisa: "aprovado; cadastro do GQ hoje nao utilizavel: ... (entradas
  recusadas ate corrigir)";
- os outros `POST` (`bloquear`, `desbloquear`, `revogar`, `limites`) não mudam. O `desbloquear`
  sempre responde `DESBLOQUEADO`, e o `limites` aceita o `conectores` opcional.

**`GET /usuarios/real` (a lista):** `conector`/`corretora`/`conta` passam a ser os do GQ lidos agora,
e não mais os aprovados. O `divergente_desde` vem sempre `null`. Campo novo `cadastro_utilizavel`
(bool).

**`GET /usuarios/{uid}/destino`:** no REAL, `codigo=REAL_CADASTRO_INUTILIZAVEL` com `causa` e
`motivo`.

**As telas F1 instaladas hoje (`06faeef`) não quebram com a v1.36.** Elas ficam com campos vazios
até serem atualizadas. Conferi no código delas:

| onde | com a v1.36 |
|---|---|
| ficha, quadro "Aprovado" | mostra conector/corretora/conta como "—"; limites, por, em e motivo continuam |
| ficha, destaque de divergência | nunca aparece (o estado não existe mais) |
| botão Aprovar | continua mandando o `visto`, que a v1.36 ignora; aprova normalmente |
| tela do usuário, motivo do "Enviar ordens" | `REAL_CADASTRO_INUTILIZAVEL` não está no mapa das F1, então ela mostra o `motivo` do serviço (ASCII, legível). O texto de `REAL_CONTA_DIVERGENTE` fica morto |
| histórico | igual. As linhas `DIVERGENCIA`/`VOLTOU` de 16/11 continuam com os rótulos que já têm |

A atualização das telas (CC do Kalman) faz isto:
- tira o quadro "Aprovado × GQ" e mostra o cadastro do GQ só como informação, com a causa em
  vermelho;
- tira o `visto`;
- mapeia `REAL_CADASTRO_INUTILIZAVEL`;
- opcional: mostra os `conectores`.

## 5. A regra de repetição dos alertas críticos

**A regra atual** (v1.15, `internal/alerta`) vale para todo crítico:
- a 1ª mensagem sai na emissão;
- repete a cada 30 s (`ALERTA_REPETICAO_S`) até completar 2 min (`ALERTA_REPETICAO_CURTA_S`);
- depois, a cada 5 min (`ALERTA_REPETICAO_LONGA_S`), até o `Resolver`.

Cada repetição traz "(continua desde HH:MM:SS)". O estado fica em memória: um restart esquece e emite
de novo se a condição continuar.

**As 5 mensagens em ~2 min do teste** são exatamente essa regra: T0, +30, +60, +90 e +120 s. Não foi
defeito de chave nem emissão duplicada. A próxima seria em T0+7 min; a conta voltou antes, e saiu o
`RESOLVIDO`.

**O que muda:**
- a classe nova "uma vez" (§2.3) para as condições de cadastro por uid: o
  `REAL_CADASTRO_INUTILIZAVEL`;
- os críticos de operação continuam repetindo como hoje: `SEM_PROTECAO`, `STOP_FURADO`,
  `SAIDA_ESGOTOU`, `FLEXSCAN_LOGIN` e os demais. Eles pedem ação imediata, e a repetição é o que
  garante que alguém vê;
- com os beta testers, o que escala é o que é por uid. Hoje só o `CONTA_AMBIENTE` e o
  `REAL_CONTA_DIVERGENTE` eram críticos por uid, e no REAL os dois saem.

## 6. Registro: 1450 e 5725 na mesma conta 4582351 (sem mudança)

Hoje o 5725 está em `SEM_APROVACAO`. Se os dois forem aprovados:
- **Com os limites de hoje, no máximo 1 contrato na conta.** O `REAL_LIMITE_POSICAO_MAX=1` é da
  instância REAL e soma todos os uids. O segundo a entrar com o primeiro posicionado recebe
  `POSICAO_MAXIMA`. Entradas no dia (30) e perda do dia (600 pts) também são da instância,
  compartilhadas.
- **O 5725 entra pela Flexscan.** Se a instância REAL da .53 não tiver Flexscan REAL configurada, a
  entrada dele cai em `REAL_CADASTRO_INUTILIZAVEL` (`CONECTOR_NAO_PERMITIDO`). Isso só muda com a
  Flexscan REAL ligada.
- **Com o teto da instância em 2** (a SPEC prevê "1 por usuário aprovado"), seriam 2 contratos na
  mesma conta por dois canais. A BTG compensa as posições na conta, mas o kalman-ordens vê duas
  operações, cada uma com a sua 1014. Com lados opostos:
  - a conta fica zerada na BTG com duas stops vivas;
  - uma stop que dispara abre posição real;
  - a conciliação por conta conta as ordens do outro canal como "outros sistemas";
  - a margem é uma só.

A decisão fica para antes do segundo usuário. Uma saída simples seria "uma conta, um uid no REAL":
recusar a entrada se outro uid tem operação viva na mesma conta.

## 7. Estimativa, instalação e volta

**Etapas** (uma branch, `v1.36`, como na v1.35; cada uma só começa com o rc=0 da anterior e o seu
ok):

| etapa | o quê | tempo |
|---|---|---|
| E1 | `aprovacao` sem conta e sem `DecidirCadastro`; `cadastroUtilizavel`; o verificador; a classe "uma vez" no `alerta` e o `ALERTAS.md`; a 0020; a regra "PnT só BTG"; a entrada e o `/destino`; as rotas (§4); unitários | ~1 sessão |
| E2 | os roteiros do §3 (133, 119, 125, 126, 132, 39 e o 135 novo) e a suíte completa (~20 min) | ~meia sessão |
| E3 | roteiro de instalação para o George e o binário | ~1 h |

**Instalação: o mesmo desenho da v1.35.**
- Restart da principal (SIMULADOR, o papel do Ferro, sombras), depois da zeragem das 14:30 ou fora
  do pregão, com o REAL desligado.
- Uns 10 min: cópia das `kalmanordem*`, a 0020, conferências.
- Antes, conferir quantas linhas REAL estão em `CONTA_DIVERGENTE` (esperado 0).
- Depois, conferir:
  - 1450 `APROVADO`, com a ficha mostrando `aprovado.conta=null`;
  - 5725 em `SEM_APROVACAO`;
  - nenhum alerta `REAL_*`;
  - `/saude` normal.
- O relogin da Flexscan, como no §6 do roteiro da v1.35.

As telas F1 continuam funcionando (§4), então a instalação não depende do CC do Kalman. A aceitação
pela tela (o checklist da SPEC funcional: trocar a conta sem bloqueio, corretora 107 com um alerta
só) vem logo depois, com o REAL ainda desligado.

**A volta para a v1.35 (`09872ead98f2`) é simples:**
- é só a troca do binário: a 0020 não muda o esquema, e a v1.35 ignora a linha 0020 na
  `kalmanordem_migracao`;
- o 1450, aprovado na v1.35 com a conta 4582351 gravada, volta como estava, se ninguém o aprovou de
  novo na v1.36;
- **depois de um aprovar feito na v1.36**, a linha fica com `conta NULL`. Na v1.35, o avaliador
  compara e põe esse uid em `CONTA_DIVERGENTE`, com o crítico repetindo. É o lado seguro: as entradas
  ficam recusadas. Sai com um aprovar pela tela F1. O roteiro de instalação deixa isso escrito.

## 8. Perguntas para o George

- **P1. Conector permitido ao usuário.** Proposta: a coluna `conectores` da aprovação.
  - O `aprovar` e o `limites` aceitam `conectores` opcional. Sem ele, vale o conector do GQ na hora
    da aprovação, como a v1.35 fazia; o 1450 continua com `PNT`.
  - A alternativa é "permitido = os conectores que a máquina tem", sem lista por uid. Ela é mais
    simples, mas o 1450 poderia ir pela Flexscan só trocando o GQ.
  - Recomendo a lista por uid.
- **P2. O alerta "uma vez" depois de um restart.** Proposta: em memória, com no máximo uma mensagem
  a mais por restart ("visto na partida"). Restart só acontece em instalação, fora do pregão.
  - A alternativa é guardar no banco: uma tabela de avisos abertos na 0020. Isso garante um alerta
    só mesmo com restart, custa mais meia etapa e não atrapalha a volta.
  - Recomendo a memória.
- **P3. Conta fora do guarda-chuva Flexscan** (`REAL_FLEXSCAN_<C>_CONTAS`). Proposta: tratar como
  cadastro inutilizável (`CONTA_INVALIDA`), porque a Flexscan recusaria a ordem. Não está na lista do
  D10.
- **P4. `CONTA_DIVERGENTE` na instalação → `APROVADO`** (0020, §1). A alternativa conservadora é
  `BLOQUEADO`, que pede um desbloquear do admin. Recomendo `APROVADO`.
