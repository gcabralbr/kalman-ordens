# SPEC técnica v1.36: a conta livre e o REAL sem limites de negócio

16/11/2026, para o George aprovar antes de codar.

**Revisão 2.**
- Entram o D7 (sem limites de negócio) e as respostas ao §10, da revisão anterior: P1 a P4.
- O D11 está no §7, só leitura.
- A SPEC funcional com D2, D3, D7, D11 e D12 revistos não está no disco: nem o `docs/` do
  `main` nem o da `v1.36` têm D11/D12. Esta revisão segue o que o George escreveu no pedido. Falta
  conferir contra o texto quando ele for salvo.

Base:
- `SPEC-REAL-PELA-TELA-F1.md`;
- a v1.35 instalada (`09872ead98f2`);
- a SPEC técnica da v1.35 (`SPEC-TECNICA-REAL-PELA-TELA-F1.md`), que continua valendo no que esta não
  muda.

Em resumo:
- **Aprovação:** diz só "este uid pode usar o REAL". Guarda quem, quando, motivo e histórico. Sem
  conta e sem limites.
- **Cadastro:** conta, corretora e conector vêm do GQ a cada entrada, sem comparação. O cadastro que
  o sistema não atende recusa a entrada e avisa uma vez.
- **Travas:** no REAL ficam só as travas contra defeito.

## 1. O que sai da v1.35

| sai | onde está hoje | na v1.36 |
|---|---|---|
| conta e corretora na aprovação | `aprovar` grava `corretora`/`conta` do GQ; a entrada manda nessa conta | `aprovar` não grava conta nem corretora. A entrada usa a do GQ |
| conector na aprovação (`conectores`) | o `aprovar` grava o conector do GQ; a entrada recusa outro (`CONECTOR_NAO_HABILITADO`) | no REAL, a coluna é ignorada (P1). O conector é o do GQ, se a máquina o atende (§3.1) |
| o `visto` do aprovar | `CADASTRO_MUDOU` (409) se o GQ mudou entre a ficha e o clique | aceito e ignorado (a tela F1 manda). Sem `CADASTRO_MUDOU` |
| limites na aprovação (`qtd_max_ordem`, `posicao_max`) | obrigatórios no `aprovar`; a ação `limites`; conferidos contra a instância | saem (D7). O `aprovar` aceita e ignora; a ação `limites` sai das ações e da rota |
| `REAL_LIMITE_*` | os 5 obrigatórios com o REAL permitido; viram os `LIMITE_*` da instância REAL | não são exigidos nem aplicados (§2) |
| a comparação a cada 30 s | `aprovacao.DecidirCadastro` grava a transição pelo avaliador | sai. O verificador do §3.3 só lê, não grava nada |
| o estado `CONTA_DIVERGENTE` | transição automática, `habilitado=0` | não é mais gerado. Fica como valor antigo no histórico |
| o alerta e a recusa `REAL_CONTA_DIVERGENTE` | crítico por uid, repete; entrada e `/destino` | saem do catálogo |
| `desbloquear` que volta divergente | resposta `codigo=CONTA_DIVERGENTE` | `desbloquear` sempre volta a `APROVADO` |
| `REAL_CADASTRO_ILEGIVEL` (recusa e alerta) | só o JSON ilegível | absorvido por `REAL_CADASTRO_INUTILIZAVEL` (§3) |

**As colunas não saem.** Na `kalmanordemusuario` e na `...hist` ficam `corretora`, `conta`, `gq_*`,
`divergente_desde`, `conectores`, `qtd_max_ordem` e `posicao_max`. No REAL, a v1.36 deixa de usá-las:
- o `aprovar` grava `NULL` (ou 0, nas que são `NOT NULL`);
- as outras ações copiam o que estava lá, como a v1.35 já fazia;
- a leitura ignora essas colunas.

As linhas antigas do histórico saem na ficha como estão, só como registro. Isso inclui as de
`CONTA_DIVERGENTE` do teste de 16/11. O SIMULADOR continua com `conectores` e os limites por uid da
CLI, como hoje.

**Um uid em `CONTA_DIVERGENTE` na instalação: passa a `APROVADO` (P4).**
- Hoje não há nenhum: o 1450 está `APROVADO` v5 e o 5725 está em `SEM_APROVACAO`.
- Pode aparecer até a instalação, porque o avaliador da v1.35 roda com o REAL desligado.
- Quem faz a troca é a migração **0020**: `UPDATE` da linha REAL em `CONTA_DIVERGENTE` para
  `APROVADO` com `habilitado=1`, e uma linha `MIGRACAO` no histórico ("v1.36: a conta é livre (D6);
  CONTA_DIVERGENTE extinto").
- A 0020 é idempotente e não muda o esquema.
- O "Enviar ordens" do dia continua desligado até o usuário ligar.
- O crítico `REAL_CONTA_DIVERGENTE` em memória some com o restart, sem `RESOLVIDO`; o roteiro de
  instalação avisa.

O `CONTA_ALTERADA_COM_POSICAO` continua fora do REAL (D6) e no SIMULADOR, como hoje. A posição aberta
segue na conta gravada na operação; isso já era assim (§4, roteiro 133).

## 2. D7: o REAL sem limites de negócio

**Saem, só no REAL:**
- da instância: contratos por ordem, posição máxima, perda do dia, entradas por dia e entradas por
  minuto;
- por uid: os limites da aprovação.

As recusas `QTD_MAXIMA`, `POSICAO_MAXIMA`, `ENTRADAS_POR_DIA`, `ENTRADAS_POR_MINUTO` e
`PERDA_DIARIA` deixam de sair no REAL. A **Parada automática por perda do dia** também sai (§7).

**Ficam (travas contra defeito):**

| trava | chave | o que faz |
|---|---|---|
| mensagens por minuto | `LIMITE_MENSAGENS_MIN` (3000) | `ORDENS_POR_MINUTO` + alerta `LIMITE` |
| recusas em 1 minuto | `LIMITE_RECUSAS_PARADA` (50) | Parada BLOQUEAR automática |
| **uma operação aberta por uid** (nova, P5) | sem chave | no REAL, a entrada de um uid que já tem operação não final na instância REAL é recusada |

**A trava nova: uma operação aberta por uid** (P5, George em 16/11).
- Vale para qualquer módulo, inclusive a boleta: a conta é a mesma.
- Conta toda operação não final: com posição, com a entrada trabalhando, ou em saída.
- A virada de mão não muda. O Kalman já manda a entrada oposta depois do `ZERADA`
  (`entrada.go:419`), e nesse momento a operação anterior já é final.
- Na prática, ela substitui o `posicao_max=1` por uid da v1.35, que saiu com o D7.
- A recusa é `UID_COM_OPERACAO_ABERTA`, com o `operacao_id` da aberta no `motivo`. Ela **não conta**
  para o `LIMITE_RECUSAS_PARADA`.
- **Ela gera alerta de defeito no Telegram, uma vez por operação** (George, 16/11): uma segunda
  entrada com a primeira aberta é defeito do sistema.
  - O alerta `UID_COM_OPERACAO_ABERTA` é não crítico e entra no catálogo e no `ALERTAS.md`.
  - A chave é o `operacao_id` da operação aberta. Repetir a entrada com a mesma operação aberta não
    gera mensagem nova; outra operação aberta depois gera.
  - O controle fica em memória, como o `REAL_RECUSA_ESTADO`.

**Saem também as travas de estratégia** (P5, George em 16/11). Todas valem só para a entrada:
- `LIMITE_ENTRADAS_MIN_UID`, `LIMITE_INTERVALO_UID_S` e a exceção da virada `LIMITE_VIRADA_S`
  (`verificarLimitesUID`);
- `LIMITE_INTERVALO_ENTRADAS_S`;
- `LIMITE_BOOK_DESCOLADO_PTS` (`entrada.go:519`, recusa `BOOK_DESCOLADO`). O Kalman já tem a própria
  trava de book descolado, configurável no gestor, e o serviço não deve ter uma segunda regra que
  recuse uma entrada que o Kalman decidiu.

**No REAL ficam só três travas:** `LIMITE_MENSAGENS_MIN`, as 50 recusas e uma operação aberta por
uid. O SIMULADOR continua com todas (P7).

**Como sai, no código:**
- a visão do REAL (`config.Visao`) deixa de copiar os `REAL_LIMITE_*` para os `LIMITE_*`;
- a instância REAL passa a pular essas cinco conferências. Não basta deixar 0: hoje 0 recusaria tudo,
  com `qtd > 0`;
- o `config.LerAmbientes` deixa de exigir os `REAL_LIMITE_*` com o REAL permitido.

**Os `REAL_LIMITE_*` que estiverem no env:** proponho ignorá-los, com um aviso na partida ("ignorada:
D7, sem limites de negócio no REAL"). Não recusar a subida (P6). Assim o `service.env` da .53 não muda
na instalação, e a volta para a v1.35, que exige essas chaves com o REAL ligado, é só a troca do
binário.

**O SIMULADOR não muda:** continua com os `LIMITE_*` e a perda do dia de 1500 pts (P7).

**O que isso significa:**
- O Kalman manda 1 contrato fixo. Sem `QTD_MAXIMA`, uma `qtd` errada vinda do Kalman vai como veio;
  a única barreira passa a ser o lado do Kalman.
- No 1450 e no 5725 na mesma conta (§8), o teto de 1 contrato da instância deixa de existir.

## 3. D10: o cadastro inutilizável

### 3.1 O que é inutilizável

A regra é uma só, `cadastroUtilizavel(uid)`. A entrada, o `/destino`, a ficha e o verificador chamam a
mesma função, para que a recusa e o alerta nunca discordem. Ela lê a `usutxtconfigprd` pelo cache de
30 s e resolve com a **visão do REAL lida do env**, mesmo com o REAL fora de `AMBIENTES_PERMITIDOS`.
Assim a resposta é a que a instância REAL daria.

O conector e a corretora vêm do GQ (P1). O cadastro é inutilizável quando o sistema não os atende:

| causa (texto em `motivo`) | `causa` | hoje |
|---|---|---|
| JSON vazio ou ilegível, sem `Robo.CORSeqId` | `CADASTRO_ILEGIVEL` | igual |
| uid sem linha em `acusuario` | `SEM_CADASTRO` | igual |
| sem `Robo.Conector` (o GX grava desde 23/10) | `CONECTOR_NAO_ATENDIDO` | **novo**: hoje vale o conector único da habilitação |
| conector que a máquina não tem no REAL: PnT sem `REAL_FIX_HOST` ou com `REAL_PNT_DESLIGADO`; Flexscan sem Flexscan REAL | `CONECTOR_NAO_ATENDIDO` | igual (`CONECTOR_NAO_CONFIGURADO`) |
| corretora que o kalman-ordens não conhece (nem 85/88 nem `REAL_FLEXSCAN_CORRETORAS`), ou 9999 no REAL | `CORRETORA_NAO_ATENDIDA` | igual |
| **PnT com corretora diferente de 85** (pelo PnT, só BTG) | `CORRETORA_NAO_ATENDIDA` | **novo**: hoje passa e a BTG recusaria |
| Flexscan por corretora sem credencial nesta máquina | `CORRETORA_NAO_ATENDIDA` | igual (`CORRETORA_SEM_CREDENCIAL`) |
| conta vazia, fora da regra do conector, ou de simulador no REAL | `CONTA_INVALIDA` | igual |
| conta fora do guarda-chuva Flexscan (`REAL_FLEXSCAN_<C>_CONTAS`) (P3) | `CONTA_INVALIDA` | igual (`CONTA_FORA_DA_CORRETORA`) |

O banco indisponível (`CONFIG_USUARIO_INDISPONIVEL`) não é cadastro inutilizável: continua como hoje.

### 3.2 Os códigos de recusa (entrada e `/destino`, só REAL)

| código | quando | muda? |
|---|---|---|
| `AMBIENTE_NAO_PERMITIDO` | REAL fora de `AMBIENTES_PERMITIDOS` (o teto vem primeiro) | não |
| `REAL_SEM_APROVACAO` / `REAL_BLOQUEADO` / `REAL_REVOGADO` | pelo estado | não |
| **`REAL_CADASTRO_INUTILIZAVEL`** | aprovado, mas o cadastro não serve | **novo** |

O `motivo` do `REAL_CADASTRO_INUTILIZAVEL` diz a causa, por exemplo "cadastro do GQ nao utilizavel:
corretora 107 nao atendida". O campo novo `causa` traz o código do §3.1.

A ordem é teto → estado → cadastro. Um uid bloqueado com o cadastro ruim recebe `REAL_BLOQUEADO`.

O código único substitui, **só no REAL**, os que a entrada devolvia por cadastro:
- `DESTINO_INVALIDO`;
- `CONECTOR_NAO_HABILITADO`, `CONECTOR_INDEFINIDO` e `CONECTOR_NAO_CONFIGURADO`;
- `CONTA_INVALIDA`, `CONTA_AMBIENTE` e `CONFIG_USUARIO_INVALIDA`;
- `CORRETORA_SEM_CREDENCIAL` e `CONTA_FORA_DA_CORRETORA`;
- `REAL_CADASTRO_ILEGIVEL`.

O SIMULADOR não muda.

### 3.3 Quando sai o alerta e quando sai o RESOLVIDO

O alerta é `REAL_CADASTRO_INUTILIZAVEL`, com uma chave por uid. Quem o emite é o **verificador**,
que substitui o avaliador:
- roda na instância principal a cada `CACHE_USUARIOS_S` (30 s), **só com a instância REAL na
  máquina** (E1, 16/11: com o REAL fora de `AMBIENTES_PERMITIDOS` ninguém entra no REAL, e o alerta
  seria ruído na .53 de hoje; a ficha e a lista mostram o cadastro pelas regras do env);
- olha só as linhas REAL em `APROVADO`;
- só lê; não grava nada no banco.

A entrada recusada não alerta. Ela só cutuca o verificador, para o alerta não esperar 30 s.

| evento | Telegram |
|---|---|
| uid `APROVADO` e o cadastro passa a inutilizável | **uma** mensagem: uid, a causa e o cadastro lido. Quando houver posição aberta, acrescenta "posicao aberta segue na conta da operacao" |
| continua inutilizável (passes seguintes, novas entradas recusadas) | nada |
| muda a causa, ainda inutilizável | nada. O texto guardado é atualizado e a ficha mostra a causa atual |
| volta a ser utilizável | `RESOLVIDO: REAL_CADASTRO_INUTILIZAVEL: uid 1450` |
| o uid sai do `APROVADO` com o alerta aberto | `RESOLVIDO ... (uid fora do APROVADO)` |
| restart com o cadastro ainda inutilizável | **uma** mensagem de novo, com "(visto na partida)" (P2: em memória) |

**A classe nova "uma vez", no pacote `alerta`:**
- o alerta é guardado pela chave, como um crítico: uma segunda emissão é ignorada, e o `Resolver`
  manda o `RESOLVIDO`;
- o `Tick` não repete;
- o alerta aparece em `/saude` `alertas_criticos` enquanto está aberto;
- o `docs/ALERTAS.md` define "uma vez" no cabeçalho (a tabela continua com a coluna "crítico", que o
  teste do catálogo confere).

No REAL, o cadastro ruim deixa de emitir o `USUARIO_CONFIG`, que hoje sai a cada entrada recusada, e
o `CONTA_AMBIENTE`, um crítico que repete. Os dois continuam no SIMULADOR.

## 4. Roteiros afetados

| roteiro | o que muda |
|---|---|
| **133** troca de conta com posição | reescrito. Ver abaixo |
| **119** dois ambientes | o uid 10 vira o caso da corretora 107 (`REAL_CADASTRO_INUTILIZAVEL`, `CORRETORA_NAO_ATENDIDA`). A `entrada 11905 ... qtd=2` deixa de dar `QTD_MAXIMA` no REAL e passa a ser aceita. O SIMULADOR continua recusando |
| **125** REAL por corretora | sai o `aprova 8 conta=9002` depois da troca. A saída da 12503 continua pela CMC/715124. A conta fora do guarda-chuva passa a `REAL_CADASTRO_INUTILIZAVEL`/`CONTA_INVALIDA` |
| **126** corretora sem credencial | `CORRETORA_SEM_CREDENCIAL` → `REAL_CADASTRO_INUTILIZAVEL`/`CORRETORA_NAO_ATENDIDA` |
| **132** rotas da aprovação | saem o GQ trocado durante o bloqueio, o desbloquear divergente, o `CADASTRO_MUDOU`, a ação `limites` e as recusas `LIMITE_ACIMA_DA_INSTANCIA`/`LIMITES_INSTANCIA_AUSENTES`. O `aprovar` sem `visto` e sem limites passa a ser aceito. O aprovar com o cadastro inutilizável é aceito, com o aviso. A ficha não tem conta nem limites |
| **39** conta HML em produção | `CONTA_AMBIENTE` → `REAL_CADASTRO_INUTILIZAVEL`/`CONTA_INVALIDA`, sem o crítico que repete |
| **124, 127, 131, 134** e os outros que têm `config REAL_LIMITE_*` | as linhas de config ficam e passam a ser ignoradas. O 124 passa a provar também que a máquina só REAL sobe sem elas |
| **135 (novo)** cadastro inutilizável | ver abaixo |
| **136 (novo)** REAL sem limites de negócio | ver abaixo |

O **133** reescrito prova isto:
- o uid 7 está em `APROVADO`, com posição na conta 12345, e troca para 7654321 no GQ;
- nenhum alerta;
- o `/destino` diz `pode_enviar=true` com a 7654321;
- o ajuste, a 1014 recriada, o 35=F e a saída continuam com `1=12345`/`116=12345`;
- depois de `ZERADA`, a entrada sai com `1=7654321`;
- restart com posição: a operação volta na 12345.

O **135** prova isto:
- a corretora 107 recusa a entrada com o motivo;
- **um** alerta só, sem repetição em 5 min de relógio;
- duas entradas a mais, sem alerta novo;
- a causa muda para conta vazia, e não sai mensagem;
- a volta dá `RESOLVIDO` e a entrada passa;
- de novo inutilizável e restart: uma mensagem "(visto na partida)";
- bloquear com o alerta aberto dá `RESOLVIDO (uid fora do APROVADO)`;
- PnT com corretora 88 dá `CORRETORA_NAO_ATENDIDA`;
- sem `Robo.Conector` dá `CONECTOR_NAO_ATENDIDO`.

O **136** prova isto, sem `REAL_LIMITE_*` no env:
- o REAL sobe;
- `qtd=2` é aceito;
- perda acima de 1500 pts sem Parada;
- 31 entradas no dia;
- 51 recusas em 1 min ainda dão a Parada BLOQUEAR;
- `LIMITE_MENSAGENS_MIN` ainda recusa;
- uma segunda entrada do mesmo uid com a operação aberta dá `UID_COM_OPERACAO_ABERTA` com um
  alerta. A terceira, com a mesma operação aberta, não dá alerta. Vale inclusive pela boleta. Depois
  do `ZERADA`, a virada de mão passa, e a próxima entrada duplicada alerta de novo;
- 4 entradas do mesmo uid em 1 min, sem intervalo, passam (sem `LIMITE_ENTRADAS_MIN_UID`/`INTERVALO_UID`);
- uma entrada com o book descolado em mais de 150 pts passa (sem `BOOK_DESCOLADO`);
- as recusas `UID_COM_OPERACAO_ABERTA` não ligam a Parada das 50 recusas;
- com `REAL_LIMITE_*` no env, o aviso de "ignorada" aparece na partida.

O verbo `aprova` dos roteiros passa a aceitar `conta=`/`corretora=`/`qtd_max=` só para simular uma
linha gravada pela v1.35. Sem eles, grava como a v1.36.

Unitários com `REAL_LIMITE_*`: `internal/config/ambientes_test.go` e
`cmd/kalman-ordens/instancias_test.go`.

## 5. O contrato das rotas (para o CC do Kalman)

**`GET /usuarios/{uid}/real` (a ficha):**
- `aprovado`: continua um objeto sempre que houve aprovação, com `por`, `em` e `motivo`. Os campos
  `conector`, `corretora_codigo`, `corretora`, `conta`, `qtd_max_ordem` e `posicao_max` vêm `null`.
- `limites_instancia`: `null`.
- `divergencia`: sempre `null`. O campo fica nesta versão e sai na próxima.
- `gq`: igual, só informação. O `erro` traz `codigo=REAL_CADASTRO_INUTILIZAVEL` e a `causa`.
- `historico[]`: igual. As linhas novas trazem `conta`/`gq`/limites `null`; as antigas, preenchidos.
- `acoes`: sem `limites`. Sem o estado `CONTA_DIVERGENTE`.

**`POST /usuarios/{uid}/real/aprovar`:**
- o corpo é `por`, `motivo` e `versao`;
- `visto`, `qtd_max_ordem` e `posicao_max` são aceitos e ignorados;
- sem `CADASTRO_MUDOU`, `CADASTRO_INVALIDO`, `LIMITE_ACIMA_DA_INSTANCIA` e
  `LIMITES_INSTANCIA_AUSENTES`;
- o aprovar com o cadastro inutilizável é aceito. O `motivo` da resposta avisa: "aprovado; cadastro
  do GQ hoje nao utilizavel: ...".

**`POST /usuarios/{uid}/real/limites`:** sai. Responde `400 CAMPO_INVALIDO`, como uma ação
desconhecida. Os outros `POST` não mudam, e o `desbloquear` sempre responde `DESBLOQUEADO`.

**`GET /usuarios/real` (a lista):**
- `conector`/`corretora`/`conta` são os do GQ lidos agora;
- `qtd_max_ordem`/`posicao_max` e `divergente_desde` vêm `null`;
- campo novo: `cadastro_utilizavel` (bool).

**`GET /usuarios/{uid}/destino`:** no REAL, `codigo=REAL_CADASTRO_INUTILIZAVEL` com `causa` e
`motivo`.

**As telas F1 instaladas hoje (`06faeef`) não quebram.** Elas mostram campos vazios até serem
atualizadas. Conferi no código delas:

| onde | com a v1.36 |
|---|---|
| ficha, quadro "Aprovado" | conector/corretora/conta como "—" e a linha "limites:" com os números em branco; por, em e motivo continuam |
| ficha, "Limites da instância" | "—" em todos |
| botão "Alterar limites" | some, porque `acoes` não traz mais `limites` |
| modal do Aprovar | continua pedindo limites > 0 e mandando o `visto`; a v1.36 ignora os dois e aprova |
| destaque de divergência | nunca aparece |
| tela do usuário, motivo do "Enviar ordens" | `REAL_CADASTRO_INUTILIZAVEL` cai no `motivo` do serviço (ASCII, legível) |

A atualização das telas (CC do Kalman) faz isto:
- tira o quadro "Aprovado × GQ", os limites, o modal de limites e o `visto`;
- mostra o cadastro do GQ só como informação, com a causa em vermelho;
- mapeia `REAL_CADASTRO_INUTILIZAVEL`.

### 5.1 `GET /usuarios/{uid}/real` (a ficha, v1.36)

O exemplo que o roteiro 132 confere (`contrato`). O 1450 aprovado, com o cadastro do GQ que o
sistema não atende (corretora 107). `aprovado` sem conta e sem limites; `limites_instancia` e
`divergencia` sempre `null`; o `gq` só como informação, com a causa em `erro`.

```json
{"uid": 1450, "estado": "APROVADO", "versao": 8,
 "acoes": ["bloquear", "revogar"],
 "aprovado": {"conector": null, "corretora_codigo": null, "corretora": null, "conta": null,
              "qtd_max_ordem": null, "posicao_max": null,
              "por": "admin:1450", "em": "2026-11-17T18:20:01.123-03:00", "motivo": "fase 1"},
 "gq": {"conector": "PNT", "corretora_codigo": 107, "corretora": null, "conta": "4582351",
        "lido_em": "2026-11-18T10:01:12.004-03:00", "valido": false,
        "erro": {"codigo": "REAL_CADASTRO_INUTILIZAVEL", "causa": "CORRETORA_NAO_ATENDIDA",
                 "motivo": "cadastro do GQ nao utilizavel: corretora 107 nao atendida pelo PnT (so BTG, 85)"}},
 "divergencia": null,
 "ultima_mudanca": {"evento": "APROVAR", "origem": "TELA", "por": "admin:1450", "em": "2026-11-17T18:20:01.123-03:00",
                    "motivo": "fase 1"},
 "limites_instancia": null,
 "recusas_corretora_hoje": {"quantidade": 3,
   "ultima": {"em": "2026-11-17T11:02:08.919-03:00", "operacao_id": 234779, "clordid": "KM-E-234779-1",
              "motivo": "REJEITADA_CORRETORA",
              "texto": "This strategy was changed to Error state, because offer RealOrder Id: 178314346 is Rejected."}},
 "posicao": {"instancia": "LIGADA", "total": 1,
             "operacoes": [{"operacao_id": 234754, "modulo": "KALMAN", "conta": "4582351", "simbolo": "WINZ26", "posicao": 1,
                            "stop_corretora": {"estado": "VIVA", "preco_stop": 173835, "...": "§5.5 da v1.35"}}]},
 "historico": [{"id": 90, "evento": "APROVAR", "estado_antes": "REVOGADO", "estado": "APROVADO", "versao": 8,
                "conector": null, "corretora_codigo": null, "conta": null, "qtd_max_ordem": null, "posicao_max": null,
                "gq": null, "origem": "TELA", "por": "admin:1450", "motivo": "fase 1",
                "em": "2026-11-17T18:20:01.123-03:00"}]}
```

**v1.36.2 (SPEC técnica v1.36.2 §4.2):** `recusas_corretora_hoje` são as entradas REAL do uid no
dia recusadas pela corretora ou pelo PnT (as com `motivo_rejeicao`): `quantidade` e a `ultima`
(`motivo` `REJEITADA` para o 39=8, `REJEITADA_CORRETORA` para o 39=4 com o erro do robô; `texto` é o
58). Sem recusa no dia: `{"quantidade": 0, "ultima": null}`. A contagem vem do banco e sobrevive a
restart. A recusa da corretora não vai ao Telegram (D4): o admin a vê aqui.

### 5.2 `GET /usuarios/real` (a lista, v1.36)

`conector`/`corretora`/`conta` são os do GQ lidos agora; `cadastro_utilizavel` diz se o sistema os
atende; `qtd_max_ordem`, `posicao_max` e `divergente_desde` sempre `null`. v1.36.2: `recusas_corretora_hoje`
é a `quantidade` da ficha.

```json
{"usuarios": [{"uid": 1450, "estado": "APROVADO", "conector": "PNT", "corretora_codigo": 85, "corretora": "BTG",
               "conta": "4582351", "cadastro_utilizavel": true, "qtd_max_ordem": null, "posicao_max": null,
               "versao": 8, "origem": "TELA", "alterado_por": "admin:1450", "alterado_em": "2026-11-17T18:20:01.123-03:00",
               "divergente_desde": null, "recusas_corretora_hoje": 0}]}
```

## 6. A regra de repetição dos alertas críticos

**A regra atual** (v1.15, `internal/alerta`) vale para todo crítico:
- a 1ª mensagem sai na emissão;
- repete a cada 30 s (`ALERTA_REPETICAO_S`) até completar 2 min (`ALERTA_REPETICAO_CURTA_S`);
- depois, a cada 5 min (`ALERTA_REPETICAO_LONGA_S`), até o `Resolver`, com "(continua desde
  HH:MM:SS)".

O estado fica em memória.

**As 5 mensagens em ~2 min do teste** são essa regra: T0, +30, +60, +90 e +120 s. Não foi defeito. A
próxima seria em T0+7 min, e a conta voltou antes.

**O que muda:**
- a classe "uma vez" (§3.3) para o `REAL_CADASTRO_INUTILIZAVEL`;
- os críticos de operação (`SEM_PROTECAO`, `STOP_FURADO`, `SAIDA_ESGOTOU`, `FLEXSCAN_LOGIN`...)
  continuam repetindo, porque pedem ação imediata;
- os únicos críticos por uid do REAL eram o `CONTA_AMBIENTE` e o `REAL_CONTA_DIVERGENTE`, e os dois
  saem do REAL.

## 7. D11, só leitura: o encerrar por disjuntor e o que depende da perda do dia

**Hoje a principal não manda nada ao kalman-ordens no disjuntor.** No KalmanWIN (`usuarios.py`,
`_checar_disjuntor`), o disjuntor do usuário (`pnl_dia <= -limite_dia` do perfil):
- só marca `u.disjuntor`, gravado em `kalmanposicao.KPIndDisjuntor`;
- impede o Kalman de **abrir** posição nova;
- deixa a stop, o alvo e a zeragem da posição aberta valendo ("sair é decisão explícita, não efeito
  colateral").

O kalman-ordens não sabe que o disjuntor abriu.

**Se a principal passar a encerrar no disjuntor**, há dois caminhos que já existem, sem mudança no
kalman-ordens:

1. **Saída do gestor:** `POST /ordens` ou `/sinais`, tipo saída, com `motivo` livre (ex.: `DISJUNTOR`)
   — `pedirSaida`, `saida.go`.
   - Confere o uid da operação, o canal (`CONECTOR_FORA`/`OPERACAO_SUSPENSA` recusam na hora) e o
     lado (reduce-only).
   - A entrada viva é cancelada.
   - **A 1014:**
     - no PnT com `SAIDA_POR_REPLACE=1`, o replace da 1014 é a própria saída;
     - senão, o 35=F da 1014 e depois a saída 35=D limitada, com repreço até `MAX_REPRECO_SAIDA`;
     - na Flexscan, o `DELETE` da stop e depois o `POST` da saída.
   - Tudo na conta e no canal gravados na operação.
   - Saída já em curso (stop, parada): responde `aceito` com `ja_em_saida`.
   - O `motivo` vai para `MotivoSaida` e não tem tratamento especial.
2. **Encerrar da operação:** `POST /operacoes/{id}/encerrar` (`motivo`, `por`). Faz o mesmo caminho
   com a origem `ADMIN`. É o da tela, não o do gestor.

**Bloqueio do dia por uid: não existe no kalman-ordens.** O que bloqueia entradas é:
- o estado da aprovação (`BLOQUEADO` e `REVOGADO`, permanentes até o admin);
- a Parada, que é **da instância inteira**, todos os uids.

Se o D11 pedir "o uid não entra mais hoje" do lado do kalman-ordens, isso é novo. Hoje ele vale só
pelo lado do Kalman: o disjuntor persistido não manda entrada.

**O que depende do `REAL_LIMITE_PERDA_DIA_PTS` hoje:**
- `config.LerAmbientes`: é obrigatório com o REAL permitido e tem de ser maior que zero, senão o
  serviço não sobe.
- `config.Visao`: vira o `LIMITE_PERDA_DIA_PTS` da instância REAL, que é usado em dois lugares:
  - **na entrada** (`entrada.go`, `verificarLimites`): o resultado do dia da instância, realizado mais
    aberto pelo último preço, `<= -600` recusa com `PERDA_DIARIA` e liga a **Parada BLOQUEAR
    automática**;
  - **a cada tick** (`tick.go`, `verificarPerdaDoDia`): a mesma conta, sem precisar de entrada, liga a
    Parada BLOQUEAR automática.
- A Parada fica gravada (`kalmanordemestado`, chave `REAL:parada`). **Ela sobrevive ao restart e à
  virada do dia; só sai pelo `POST /retomar`.**
- Na ficha da v1.35, `limites_instancia.perda_dia_pts`, que as telas F1 mostram.
- Os avisos de chave ignorada (`avisosReal`).

**Nada mais depende dele:**
- nenhum alerta próprio além do `PARADA`;
- a stop e a saída não dependem dele;
- o PnL que vai ao Kalman não depende dele.

Com o D7, a Parada automática por perda sai do REAL. A proteção por perda passa a ser só o disjuntor
do Kalman, por uid. A Parada manual e a das 50 recusas continuam.

### 7.1 O disjuntor por usuário do CC do Kalman: o que toca o kalman-ordens

O CC do Kalman vai implementar o D11. Só confirmado aqui; a v1.36 não muda nada por causa disso.

**1. A saída do disjuntor, com `motivo=PERDA_DIA_USUARIO`, pelo pedido de saída de hoje.**

O motivo não tem tratamento especial no serviço:
- `pedirSaida` (`internal/motor/saida.go:15`) só limita o tamanho a 60 caracteres
  (`dominio.MaxMotivoSaida`, `internal/dominio/dominio.go:327`) e o grava em `MotivoSaida`;
- a única comparação de motivo no motor é `SINAL_OPOSTO`, na virada de mão
  (`internal/motor/entrada.go:419`).

Não há confusão com o disjuntor de bolsa (`PNT_BOLSA_INDISPONIVEL`):
- ele só olha o texto `58` das rejeições do PnT, contra `DISJUNTOR_TEXTOS`
  (`internal/motor/disjuntor.go:48`);
- ele não lê o motivo de pedido nenhum;
- ele não bloqueia saídas.

A saída segue o caminho do §7: conta e canal da operação, e a 1014 cancelada ou substituída pela
saída.

**2. A stop no nível do disjuntor (D-a).**

A rota é `POST /operacoes/{id}/stop` (`internal/api/api.go:80`, `ajustarStop` em
`internal/motor/ajuste.go:80`), com `preco_stop` (ou `stop_pts`) e o `origem` opcional. Para o
disjuntor, sugiro `origem=PERDA_DIA_USUARIO`; o padrão é `^[A-Z][A-Z0-9_]{1,19}$`, `ajuste.go:35`.

Ela aceita apertar ou afrouxar a qualquer momento em que a 1014 está viva. **Não existe piso nem
regra de "só aperta" no serviço:** vale o último ajuste recebido (`ajuste.go:150`;
`calcularStop`, `internal/motor/protecao.go:202`). **Quem garante "o mais apertado entre a stop do
gestor e a do disjuntor" é o Kalman**: um ajuste do TRSP mais frouxo, mandado depois, desfaz a stop do
disjuntor.

O que pode atrasar ou recusar o ajuste:

| regra | onde | efeito |
|---|---|---|
| 1014 ocupada (envio, replace ou cancel sem resposta) | `ajuste.go:155` | aceito como `AGUARDANDO`; sai quando a 1014 libera |
| diferença menor que `AJUSTE_DEGRAU_MIN_PTS` (25) para a stop na corretora | `ajuste.go:157` | `IGNORADO`: grava o preço, mas não manda o replace. **Pode deixar a stop até 24 pts mais frouxa que o nível do disjuntor** |
| `REPLACES_1014_MAX` (100) menos a reserva de 5 para a saída | `ajuste.go:117` | `PROTECAO_LIMITE_REPLACES` (recusa) |
| sem 1014 viva: antes do fill da entrada, entre o fill e a 1014, ou operação em saída | `ajuste.go:107`, `:111` | `SEM_PROTECAO` (recusa) |
| a stop já disparou na corretora | `ajuste.go:114` | `PROTECAO_DISPARADA` (recusa) |
| sem bid/ask recente do símbolo (`IDADE_MAX_PRECO`) | `ajuste.go:139` | `SEM_PRECO` (recusa) |
| o preço já passou do bid (comprado) ou do ask (vendido) | `ajuste.go:144`, `:147` | `STOP_ALEM_DO_PRECO` (recusa). Nesse caso o Kalman manda a saída |
| canal fora ou operação suspensa | `ajuste.go:104` | `CONECTOR_FORA` / `OPERACAO_SUSPENSA` (recusa) |

**Não atrasam o ajuste:**
- o `LIMITE_MENSAGENS_MIN`, que só é conferido na entrada (`entrada.go:374`);
- o limitador de taxa da sessão: a 1014 tem prioridade, e só as entradas esperam
  (`internal/motor/volta.go:23`, `:472`).

Não há intervalo mínimo entre replaces além de "um pedido por vez".

## 8. Registro: 1450 e 5725 na mesma conta 4582351 (sem mudança)

Hoje o 5725 está em `SEM_APROVACAO`. Se os dois forem aprovados:
- **Com o D7, não há mais o teto de 1 contrato da instância.** Os dois podem ter posição ao mesmo
  tempo na mesma conta, por dois canais: o 1450 pelo PnT e o 5725 pela Flexscan.
- **O 5725 depende da Flexscan REAL ligada na máquina.** Sem ela, a entrada dele cai em
  `REAL_CADASTRO_INUTILIZAVEL` (`CONECTOR_NAO_ATENDIDO`).
- **Com os dois posicionados:**
  - a BTG compensa as posições na conta, e o kalman-ordens vê duas operações, cada uma com a sua 1014;
  - com lados opostos, a conta fica zerada na BTG com duas stops vivas, e uma stop que dispara abre
    posição real;
  - a conciliação por conta conta as ordens do outro canal como "outros sistemas";
  - a margem é uma só.

A decisão fica para antes do segundo usuário. Uma saída simples seria "uma conta, um uid no REAL":
recusar a entrada se outro uid tem operação viva na mesma conta. Ela seria uma trava contra defeito
de cadastro, não um limite de negócio.

## 9. Estimativa, instalação e volta

**Etapas:** uma branch, `v1.36`. Cada etapa só começa com o rc=0 da anterior e o seu ok.

| etapa | o quê | tempo |
|---|---|---|
| E1 | aprovação sem conta, conector e limites; `cadastroUtilizavel` e o verificador; a classe "uma vez" e o `ALERTAS.md`; a 0020; "PnT só BTG"; o REAL sem os `REAL_LIMITE_*` (visão, partida, as cinco conferências, a Parada por perda); a entrada, o `/destino` e as rotas (§5); unitários | ~1 sessão e meia |
| E2 | os roteiros do §4 (133, 119, 125, 126, 132, 39, 124 e os novos 135 e 136) e a suíte completa (~20 min) | ~meia sessão |
| E3 | roteiro de instalação e o binário | ~1 h |

**Instalação: o mesmo desenho da v1.35, uns 10 min.**
- Restart da principal (SIMULADOR, o papel do Ferro, sombras), depois da zeragem das 14:30 ou fora
  do pregão, com o REAL desligado.
- Cópia das `kalmanordem*` e a 0020.
- O `service.env` não muda: os `REAL_LIMITE_*` ficam, ignorados (P6).
- Antes, conferir as linhas REAL em `CONTA_DIVERGENTE` (esperado 0).
- Depois, conferir:
  - 1450 `APROVADO`, com a ficha mostrando `aprovado.conta=null` e os limites `null`;
  - 5725 em `SEM_APROVACAO`;
  - nenhum alerta `REAL_*`;
  - o aviso dos `REAL_LIMITE_*` ignorados no log;
  - `/saude` normal.
- O relogin da Flexscan, como no §6 do roteiro da v1.35.

As telas F1 continuam funcionando (§5), então a instalação não depende do CC do Kalman.

**A volta para a v1.35 (`09872ead98f2`) é simples: só a troca do binário.**
- A 0020 não muda o esquema.
- O env ainda tem os `REAL_LIMITE_*`, que a v1.35 exige.
- O 1450, aprovado na v1.35 com a conta gravada, volta como estava.
- **Depois de um aprovar feito na v1.36**, a linha fica com `conta NULL` e limites 0. Na v1.35:
  - o avaliador põe esse uid em `CONTA_DIVERGENTE`, com o crítico repetindo. É o lado seguro: as
    entradas ficam recusadas, e sai com um aprovar pela tela F1;
  - os limites 0 do uid não valem, mas os da instância voltam a valer.

  O roteiro deixa isso escrito.

## 10. Decisões

**Respondidas pelo George em 16/11:**
- **P1.** Sem lista de conectores por usuário. Conector e corretora vêm do GQ; o cadastro é
  inutilizável se o sistema não os atende. Pelo PnT, só BTG. Daí sai a regra de que o cadastro sem
  `Robo.Conector` é inutilizável (§3.1). Antes, ele usava o conector único da habilitação.
- **P2.** Alerta "uma vez" em memória, com no máximo uma mensagem a mais por restart.
- **P3.** Conta fora do guarda-chuva da Flexscan é cadastro inutilizável.
- **P4.** `CONTA_DIVERGENTE` na instalação passa a `APROVADO` (0020).

**Novas, do D7:**
- **P5.** Saem do REAL `LIMITE_ENTRADAS_MIN_UID`, `LIMITE_INTERVALO_UID_S`, `LIMITE_VIRADA_S`,
  `LIMITE_INTERVALO_ENTRADAS_S` e `LIMITE_BOOK_DESCOLADO_PTS`. Ficam só `LIMITE_MENSAGENS_MIN`, as 50
  recusas e a trava de uma operação aberta por uid. Essa trava gera um alerta de defeito por operação
  e não conta para as 50 recusas (§2).
- **P6.** Os `REAL_LIMITE_*` no env são ignorados com aviso, sem impedir a subida.
- **P7.** Os limites do SIMULADOR ficam como estão.

**Também decidido em 16/11:**
- o cadastro sem `Robo.Conector` é inutilizável (conector obrigatório, §3.1);
- a folga de até 24 pts do `AJUSTE_DEGRAU_MIN_PTS=25` na stop do disjuntor é aceita, e nada muda no
  serviço (§7.1).
