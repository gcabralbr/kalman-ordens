# SPEC admin por conector e ambiente — v1 (13/11/2026; aprovada e feita na v1.34)

Para a tela `kalman-admin.html` (KalmanWIN) servir aos dois conectores (PnT e Flexscan) nos dois
ambientes (SIMULADOR e REAL), inclusive com uma instância desligada (o REAL fora de
`AMBIENTES_PERMITIDOS`). Lado do serviço: endpoints novos ou ajustados, uma migração. A tela é do CC
do Kalman.

## 1. O que acontece hoje e por quê

| aba | sintoma | causa |
|---|---|---|
| Sessão com o PnT (`GET /sessao/eventos?dia=`) | eventos dos dois conectores misturados: `FLEXSCAN_WEBHOOK_AUSENTE` junto do logon da `GAMMAQ2` | `kalmanordemsessao` tem só `tipo, texto, em`. Ela recebe os eventos da sessão FIX (LOGON, LOGOUT, QUEDA, RESEND, LOGON_RECUSADO), BOLSA_FORA/VOLTOU, a CONCILIACAO e **todo alerta sem operação, de qualquer instância e conector**. Não há coluna de ambiente nem de conector |
| Ordens no PnT (`GET /pnt/ordens`) | 503 "ambiente REAL sem instancia nesta maquina" com o REAL desligado; não mostra as ordens do dia | a rota só faz o 35=AF (mass status) pela sessão viva. Sem instância, o serviço não tem a quem perguntar e responde 503 (o mesmo código de "a consulta não terminou a tempo") |
| PnT ao vivo (`GET /sessao/ao-vivo`) | 400 pelo mesmo motivo | o monitor é só memória da instância (as últimas 2.000 mensagens do fio). Sem instância, a resposta é 400 "ambiente invalido ou sem instancia", o mesmo texto de um parâmetro errado |

O mesmo vale para `GET /flexscan/trafego?dia=`. Ele lê o banco, mas passa pela instância
(`flexscanDo`), e com o REAL desligado responde 404 "flexscan nao configurada".

## 2. O que já está gravado, por conector

| | PnT (FIX) | Flexscan (HTTP) |
|---|---|---|
| operações e ordens do dia | `kalmanordemoperacao` (com `ambiente`, `conector`, `conta`, `destino`, `modulo`) e `kalmanordem` (ClOrdID, 37, 198, estratégia, preços, estado, qtd executada, motivo da rejeição). Já lidas por `GET /operacoes?dia=&ambiente=` e `GET /ordens/{id}` sem passar pela sessão | as mesmas tabelas (o conector é coluna da operação) |
| mensagens | `kalmanordemevento`: os D/G/F enviados e os 8/9 recebidos das ordens KM, com `fix_raw`, `ambiente` e os campos do ER. Também o 3 (reject de sessão), o 5 recusado e os "?" ilegíveis. **Não** grava logon, heartbeat, test request, resend ou mass status. A API mostra o `fix_raw` com o 554 mascarado (`FixLegivel`) | `kalmanordemevento` com `msgtype` `FLX-*`: o POST/DELETE e a resposta, o webhook, a consulta que mudou o estado ou falhou, o ajuste de preço, `FLX-LOGIN`, `FLX-WHCAD`. Tem `ambiente` e `canal` (a corretora no REAL: BTG, CMC; NULL na ponte única). É o que `/flexscan/trafego?dia=` já monta |
| eventos da sessão | `kalmanordemsessao`, sem ambiente/conector (§1) | os alertas da ponte sem operação caem na mesma tabela (§1) |
| o fio inteiro | o log de arquivo do QuickFIX em `<FIX_DIR>/log` (`fix/` e `fix-real/`), com tudo, inclusive heartbeats. Pertence ao usuário do serviço, só no disco da .53, sem índice | não existe (a Flexscan não tem "sessão" além do token) |
| pedidos do gestor | `kalmanordempedido` (o POST como chegou e a resposta), dos dois conectores | idem |

Tudo isso sai no `expurgar` (retenção) como hoje.

## 3. A regra

1. **Histórico = banco, com qualquer instância** (ligada, desligada ou nunca configurada nesta
   máquina). Rotas com `dia` (padrão: hoje). Não passam pela fila do motor nem pela sessão.
2. **Ao vivo e "consultar agora" = instância ligada.** Sem ela, a resposta diz exatamente o que falta
   (§3.6) e aponta para o histórico.
3. **Todo item leva `ambiente` e `conector`**, e a Flexscan do REAL leva `canal` (a corretora).
   `conector`: `PNT`, `FLEXSCAN` ou `SERVICO` (o que não é de conector: conciliação, bolsa fora,
   limites, configuração).

### 3.1 `GET /conectores` (novo): o mapa para a tela montar as abas

```json
{"ambientes": [
  {"ambiente": "SIMULADOR", "instancia": "LIGADA",
   "conectores": [{"conector": "FLEXSCAN", "canal": null, "estado": "NO_AR"},
                  {"conector": "PNT", "estado": "DESLIGADO", "motivo": "PNT_DESLIGADO=1"}]},
  {"ambiente": "REAL", "instancia": "DESLIGADA", "motivo": "REAL fora de AMBIENTES_PERMITIDOS",
   "conectores": [], "historico": true}
]}
```

- `instancia`: `LIGADA` ou `DESLIGADA`.
- `estado` do conector: `NO_AR`, `FORA` ou `DESLIGADO`. Com a instância ligada, vem de
  `/saude.canais`.
- `historico: true`: o banco tem registro do ambiente (alguma operação, evento ou sessão). A tela
  mostra as abas de histórico mesmo com a instância desligada.

### 3.2 `GET /sessao/eventos?dia=&ambiente=&conector=` (ajuste)

- **Filtros novos:** `ambiente` e `conector`, ambos opcionais. Sem eles, vem tudo, como hoje.
- **Cada item:** `{em, tipo, texto, ambiente, conector, canal}`.
- **Migração 0018** (só `ALTER ... ADD COLUMN IF NOT EXISTS`): `ambiente`, `conector` e `canal` em
  `kalmanordemsessao`, mais um índice `(em, ambiente, conector)`.
- **Quem grava:** o `registrarSessao` passa a gravar o ambiente da instância. O conector vem da origem:
  - os eventos da sessão FIX e o LOGON_RECUSADO são `PNT`;
  - os alertas da ponte são `FLEXSCAN` (com o canal);
  - o resto é `SERVICO`. Para isso, o alerta sem operação ganha o conector de quem o emitiu, sem
    adivinhar pelo texto.
- **Linhas antigas** (antes da 0018): a API infere, e marca `inferido: true`:
  - `LOGON`/`LOGOUT`/`QUEDA`/`RESEND`/`LOGON_RECUSADO` → `PNT`;
  - código ou texto `FLEXSCAN_*`/`[flexscan]` → `FLEXSCAN`;
  - o resto → `SERVICO`.
  O ambiente antigo vem `null`.

A aba vira "Sessão" com um filtro de conector. "Sessão com o PnT" = `conector=PNT`.

### 3.3 `GET /historico/ordens?dia=&ambiente=&conector=&uid=&modulo=` (novo)

As ordens do dia como estão gravadas, com a instância desligada. Uma linha por ordem (`kalmanordem` +
`kalmanordemoperacao`):

```json
{"dia": "2026-11-13", "ambiente": "REAL", "conector": "PNT", "instancia": "DESLIGADA",
 "ordens": [{"operacao_id": 234754, "modulo": "KALMAN", "uid": 1450, "conta": "4582351",
   "papel": "P", "clordid": "KM-P-234754-2", "orderid37": "...", "estrategia": 1014, "lado": "C",
   "qtd": 1, "preco": 173935, "preco_stop": 173835, "estado": "EXECUTADA", "qtd_exec": 1,
   "preco_medio": 173790, "motivo_rejeicao": null, "criada_em": "...", "atualizada_em": "...",
   "ultima_mensagem": {"em": "...", "msgtype": "8", "exectype": "F", "ordstatus": "2", "texto": null}}]}
```

- Vale para os dois conectores. Na Flexscan, o `orderid37` é o id da Flexscan.
- `estado` é o último que o serviço registrou, não uma consulta à corretora. A tela mostra isso com o
  rótulo "registrado".
- Mostra só as ordens do serviço (KM). As de outros sistemas na mesma conta (o L&S do GQ) só aparecem
  no "consultar agora".

### 3.4 `GET /pnt/ordens?ambiente=` (ajuste): o "consultar agora"

Continua o 35=AF pela sessão viva, com cache e intervalo mínimo. Só mudam as respostas:

| caso | hoje | proposta |
|---|---|---|
| instância desligada | 503 `AMBIENTE_NAO_PERMITIDO` | **409** `INSTANCIA_DESLIGADA` (§3.6) |
| instância sem PnT (`PNT_DESLIGADO`) | 503 `SEM_SESSAO` | **409** `CONECTOR_AUSENTE` |
| sessão FIX caída | 503 `SEM_SESSAO` | **503** `SEM_SESSAO` (transitório: tentar de novo) |
| a consulta não terminou a tempo | 503 `RESULTADO_DESCONHECIDO` | igual |

Para a Flexscan, o equivalente seria um `GET /flexscan/ordens?ambiente=&canal=` (consulta das ordens
do dia na API da Flexscan). **Fica fora desta v1**: a ponte só consulta ordem por ordem hoje, e a
tela usa o §3.3.

### 3.5 O fio do dia

**Histórico, `GET /historico/fio?dia=&ambiente=&conector=&canal=&clordid=&desde=&limite=`** (novo):
- lê `kalmanordemevento` do dia, filtrado, em ordem de `em, id`, paginado por `desde` (o id) e
  `limite` (padrão 500, máximo 2.000);
- **PnT:** cada item é `{seq, em, direcao, msgtype, exectype, ordstatus, clordid, orderid37, texto,
  fix_raw}`, com o 554 mascarado;
- **Flexscan:** o mesmo formato das linhas de `/flexscan/trafego` (`tipo, status_http, duracao_ms,
  resumo, clordid, flexscan_id, operacao_id, modulo, uid`);
- é a mesma leitura que `/flexscan/trafego?dia=` já faz, só que sem passar pela instância. O
  `/flexscan/trafego?dia=` passa a chamar esta, e com o REAL desligado deixa de dar 404;
- **o que não tem:** heartbeats, logon e resend do PnT não estão no banco, só no log de arquivo
  (§2). Os eventos de logon/logout/queda/resend estão no §3.2. Ler o log de arquivo pela API **não**
  está proposto: ele pertence ao usuário do serviço, é grande e só existe na .53. Se a tela precisar
  do fio cru de um dia, a saída é um comando da CLI (`kalman-ordens fio -dia -ambiente`), não a API.

**Ao vivo** (sem mudar o contrato quando a instância existe):
- `GET /sessao/ao-vivo?ambiente=` (PnT) e `GET /flexscan/trafego?ambiente=&corretora=` (sem `dia`)
  continuam da memória;
- sem instância: **409** `INSTANCIA_DESLIGADA`; instância sem o conector: **409** `CONECTOR_AUSENTE`;
- parâmetro errado continua 400.

### 3.6 Respostas sem instância (todas as rotas que exigem uma)

```json
HTTP 409
{"codigo": "INSTANCIA_DESLIGADA", "ambiente": "REAL",
 "motivo": "REAL fora de AMBIENTES_PERMITIDOS nesta maquina",
 "ambientes_permitidos": ["SIMULADOR"],
 "historico": "/historico/fio?dia=2026-11-13&ambiente=REAL&conector=PNT"}
```

| código | HTTP | quando |
|---|---|---|
| `INSTANCIA_DESLIGADA` | 409 | ambiente válido, sem instância nesta máquina |
| `CONECTOR_AUSENTE` | 409 | a instância existe, mas sem esse conector (`PNT_DESLIGADO`, Flexscan sem a corretora) |
| `SEM_SESSAO` | 503 | conector configurado, sessão ou token fora agora (transitório) |
| `AMBIENTE_INVALIDO` | 400 | o parâmetro não é SIMULADOR nem REAL |

409 diz "não é erro seu nem do serviço: o estado da máquina não permite". A tela mostra a mensagem
e, se houver `historico`, o link. **Não muda** o `POST /ordens` e o resto do contrato do Kalman: lá o
`AMBIENTE_NAO_PERMITIDO` continua como está.

## 4. Abas da tela (para o CC do Kalman)

| aba | ambiente/conector escolhidos na tela | rota |
|---|---|---|
| Sessão | filtro de ambiente e conector | `/sessao/eventos?dia&ambiente&conector` |
| Ordens (registradas) | ambiente, conector | `/historico/ordens?dia&ambiente&conector` |
| Ordens: consultar agora | ambiente com PnT ligado | `/pnt/ordens?ambiente` (409/503 tratados) |
| Fio do dia | ambiente, conector, canal | `/historico/fio?...` |
| Ao vivo | ambiente, conector | `/sessao/ao-vivo` (PnT) ou `/flexscan/trafego` (Flexscan); se der 409, oferecer o "Fio do dia" |

As abas vêm de `/conectores`: ambiente com `instancia: DESLIGADA` e `historico: true` mostra só as
abas de histórico.

## 5. Entrega no serviço (para depois do ok)

- Versão: v1.34 (a v1.33 está pronta e não deve crescer).
- Migração 0018 (§3.2).
- Rotas novas `/conectores`, `/historico/ordens` e `/historico/fio`; ajustes em `/sessao/eventos`,
  `/pnt/ordens`, `/sessao/ao-vivo` e `/flexscan/trafego`.
- Roteiros: o REAL desligado com o dia gravado (as três abas de histórico respondem e as do ao vivo
  dão 409); eventos de sessão separados por conector; linha antiga inferida.
- Compatível: sem os parâmetros novos, `/sessao/eventos` e `/flexscan/trafego` respondem como hoje,
  com campos a mais. Só mudam os códigos HTTP do §3.6. A tela atual trata 503/400 como erro genérico,
  e 409 cai no mesmo caminho até ela ser atualizada.

## 6. Aprovação e implementação (13/11)

A SPEC foi aprovada com as três decisões do §5: v1.34; o log do QuickFIX fora da API, com o comando
`kalman-ordens fio` na CLI; a consulta na API da Flexscan fora.

A implementação e os pontos que ela decidiu estão em `SPEC-ORDENS-v1.34.md` §1.3:
- o `historico` do `/conectores` é "há operação gravada";
- as linhas antigas da sessão só aparecem sem o filtro de ambiente;
- os alertas da fonte de preço ficam `SERVICO`;
- a linha da Flexscan no fio é `{seq, conector, canal, linha}`;
- o `historico` do 409 depende da rota: `/pnt/ordens` → `/historico/ordens`; `/sessao/ao-vivo` e
  `/flexscan/trafego` → `/historico/fio`.

