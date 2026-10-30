# SPEC kalman-ordens v1.28 — a agenda da Flexscan, o caminho gravado na operação e o canal fora do ar (30/10/2026)

Base: v1.27 (`b37d869f2672`, instalada em 30/10 às 10:36). Pedidos de 30/10 (aprovados sem SPEC
prévia; esta registra o que foi feito).

## 1. A agenda da Flexscan

O tráfego mostrava a conferência do webhook a cada 10 min a madrugada inteira e a renovação do login
às 04:33 (10 h depois do último). Fora do pregão isso é só ruído.

| chave (`service.env`) | padrão | o quê |
|---|---|---|
| `FLEXSCAN_JANELA` | `08:00-18:30` | a janela da ponte, nos dias de pregão (os feriados de `FERIADOS_ARQ`); `sempre` = como antes |
| `FLEXSCAN_RENOVAR_ABERTURA` | `08:15` | a renovação do login antes da abertura; vazio = sem |
| `FLEXSCAN_CONFERIR_ABERTURA` | `08:30` | a conferência obrigatória do webhook antes da abertura; vazio = sem |

No REAL, `REAL_FLEXSCAN_JANELA` etc. quando diferir (sem elas, as do SIMULADOR). Formato errado: o
serviço não sobe.

1. **Dentro da janela:** como antes. A conferência do cadastro do webhook a cada
   `FLEXSCAN_WEBHOOK_CONFERIR_MIN`, a renovação do token `FLEXSCAN_RENOVAR_ANTES_MIN` antes de vencer.
2. **Conferência da abertura:** uma por dia de pregão, no horário (a primeira volta da ponte a
   partir dele, antes do fim da janela), ainda que fora da janela. Pega a limpeza noturna da
   Flexscan (~03:45) e recadastra antes do pregão. Um restart depois do fim da janela não a faz.
3. **Renovação da abertura:** uma por dia de pregão, no horário, se o token não chega ao fim da
   janela. Um login feito a partir do início da janela (ou do horário, se for antes) já vale por ela:
   a ponte que dormiu e logou às 08:00 não loga de novo às 08:15.
4. **Fora da janela, sem ordem viva: nenhuma chamada à Flexscan.** Nem conferência, nem renovação,
   nem login. O token que vence fora da janela não é renovado: a ponte fica sem sessão, **sem
   alerta** (`dormindo: true`; o `SESSAO_CAIU` é só da queda de verdade), e loga no início da
   janela (ou na conferência/renovação da abertura, o que vier primeiro). **A partida loga sempre**,
   mesmo fora da janela (ajuste de 30/10: a instalação valida as credenciais na hora); fora da
   janela, sem ordem viva, esse login é a única chamada.
5. **Com ordem viva da Flexscan** (operação não encerrada do motor, ou ordem acompanhada pela ponte),
   a qualquer hora: tudo continua (consultas, conferência, renovação), por segurança.
6. **Um envio do serviço com a ponte dormindo** (a saída, as ações do admin): a ponte loga na hora.
   Proteger e zerar nunca esperam a janela (na prática, com posição a ponte já está ativa pelo item 5).
   A entrada fora da janela não acorda a ponte: é recusada (`SEM_SESSAO`, ou antes pela janela de
   entradas).

Com um token de 10 h, o login das 08:00 vence às 18:00, e a ponte renova às 17:30, ainda dentro da
janela. Para um login só por dia, a janela tem de acabar antes do vencimento (ex.: `08:15-18:00`
com a renovação às 08:15).

O relógio do token passou a ser o do serviço (o virtual nos roteiros), o mesmo da agenda.

### 1.1 `GET /flexscan/trafego` (v1.28)

O contrato da SPEC-AMBIENTES-v1 §10.1 com os quatro campos da agenda no `saude`:
- `janela` (`"08:00-18:30"` ou `"sempre"`);
- `ativa`: a ponte pode falar com a Flexscan agora;
- `ativa_por`: `"janela"`, `"ordem viva"`, `"abertura"`, `"envio"`, `"sempre"`, ou `null`;
- `dormindo`: o token venceu fora da janela e a ponte espera a abertura.

```json
{
  "ambiente": "SIMULADOR",
  "mensagens": [
    {"seq": 812, "em": "2026-10-27T09:37:40.794-03:00", "tipo": "POST", "direcao": "ENV",
     "status_http": 202, "duracao_ms": 61, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "C 1 WINZ26 limite 179130 conta S-1117150",
     "nao_registrada": false, "ip": null},
    {"seq": 814, "em": "2026-10-27T09:42:41.120-03:00", "tipo": "WEBHOOK", "direcao": "REC",
     "status_http": 200, "duracao_ms": null, "clordid": "KM-TESTE-P-991027001-1",
     "flexscan_id": "9a0a6ca1-...", "operacao_id": 991027001, "modulo": "TESTE", "uid": 5725,
     "resumo": "1 retrato: KM-TESTE-P-991027001-1 estado 5 exec 0/1",
     "nao_registrada": false, "ip": "177.71.196.212"}
  ],
  "ultimo_seq": 814,
  "saude": {
    "logado": true, "token_expira": "2026-10-27T19:20:21-03:00", "receptor": true,
    "webhooks_recebidos": 7, "ultimo_webhook": "2026-10-27T09:45:59-03:00", "ultimo_erro": "",
    "ordens_acompanhadas": 6, "ordens_vivas": 0,
    "webhook_cadastrado": true, "webhook_conferido_em": "2026-10-27T09:52:40-03:00",
    "webhook_recadastrado_em": null, "webhook_faltas_seguidas": 0,
    "ultimo_login": {"em": "2026-10-27T09:20:21.004-03:00", "motivo": "partida", "resultado": "OK"},
    "janela": "08:00-18:30", "ativa": true, "ativa_por": "janela", "dormindo": false,
    "boot": "2026-10-27T09:20:20.512-03:00", "dia": "2026-10-27", "recarregadas": 0, "msgs_min": 12, "consultas_min": 40,
    "webhook_ips": {"177.71.196.212": 7},
    "latencia_post_p50_ms": 58, "latencia_post_max_ms": 140
  }
}
```

### 1.2 `GET /saude` → `flexscan` (v1.28)

```json
{
  "logado": true, "token_expira": "2026-10-27T19:20:21-03:00", "receptor": true,
  "webhooks_recebidos": 7, "ultimo_webhook": "2026-10-27T09:45:59-03:00", "ultimo_erro": "",
  "ordens_acompanhadas": 6, "ordens_vivas": 0,
  "webhook_cadastrado": true, "webhook_conferido_em": "2026-10-27T09:52:40-03:00",
  "webhook_recadastrado_em": null, "webhook_faltas_seguidas": 0,
  "ultimo_login": {"em": "2026-10-27T09:20:21.004-03:00", "motivo": "partida", "resultado": "OK"},
  "janela": "08:00-18:30", "ativa": true, "ativa_por": "janela", "dormindo": false,
  "simbolo": "WINZ26", "idade_preco_ms": 412,
  "fonte": {"degradada": false, "ruim_desde": null, "motivo": "", "janela": "09:00-14:30",
            "dia_de_pregao": true, "travado": false, "travado_desde": null, "travado_motivo": "",
            "url": "http://172.16.110.53:9090", "compartilhada": true}
}
```

## 2. O caminho gravado na operação

**A operação usa sempre o caminho gravado na entrada, nunca a configuração do momento.** Isso já era
a regra (v1.2 U4, SPEC-FLEXSCAN-v1 §14.1, SPEC-AMBIENTES-v1 §1.2). O roteiro 120 passa a confirmar.

**Gravados na operação** (`kalmanordemoperacao`, na entrada, fixos até o fim):

| campo | coluna | de onde vem |
|---|---|---|
| conector | `conector` | `Robo.Conector` do GQ entre os da habilitação |
| ambiente | `ambiente` | o do pedido (o login do usuário no GQ) |
| corretora | `destino` | `Robo.CORSeqId` (85 BTG, 88 Capital...) |
| conta | `conta` | `Robo.CCONroConta` do ambiente: **sim, a conta também** |
| símbolo | `simbolo` | o contrato vigente na fonte do conector |

Cada ordem da operação (`kalmanordem.conta`) leva a conta da operação. No PnT ela vai no 1 e no 116;
na Flexscan, no `account`. A bolsa (`ORDENS_BOLSA`, `FLEXSCAN_BOLSA`), o `SenderLocationID` e as
credenciais do conector são da instância (`service.env`), não do usuário.

**Onde a configuração do usuário ainda é relida com operação aberta.** São dois pontos, e nenhum muda
o caminho:
1. **`conferirConta`** (`internal/motor/saida.go`). Chamada a cada `CONFERE_CONTA_S` (tick) e no
   pedido de saída. Compara o GQ de agora com o gravado e só alerta
   (`CONTA_ALTERADA_COM_POSICAO`, crítico; resolve ao zerar ou se a configuração voltar).
2. **`contasAdmin`** (`internal/motor/conciliacao.go`). É a consulta do admin `GET /pnt/ordens`, que
   pergunta também as contas atuais dos habilitados. É só leitura e não age em operação.

Fora esses dois, a configuração do usuário (habilitação e `acusuario`) só é lida na entrada e no
`/destino`. A proteção, o ajuste do stop, a saída, a zeragem, a conciliação (o mass status pergunta a
conta gravada) e o restart usam a operação gravada. A instância também é escolhida pelo ambiente
gravado (v1.26).

**Roteiro 120:** uma operação aberta no PnT e outra na Flexscan.
- O GQ troca o `Robo.Conector`, a corretora e a conta dos dois usuários.
- Os ajustes do stop seguem pelo caminho de cada entrada: um 35=G no PnT e um PUT na Flexscan.
- Depois do restart, o mass status pergunta a conta gravada.
- A saída do gestor sai no PnT, na conta 12345, e a zeragem (parada ZERAR) na Flexscan, na S-1117150.

## 3. O conector da operação fora do ar

O canal de uma operação (FIX caiu, Flexscan sem token ou sem resposta, conector desligado) pode cair
no meio dela. Nesse caso **não há saída automática**: nem pelo canal caído, nem por outro conector,
porque a stop no canal original continuaria viva na corretora.

**Na hora** (o canal fora, mesmo por um segundo):
- a saída, o ajuste do stop, o `encerrar` e o `cancelar-entrada` do admin são recusados com
  `CONECTOR_FORA`, e nada fica guardado para sair na volta;
- o `/saude` mostra o canal em `canais`; o `/destino` mostra `canal` e, fora do ar, `pode_enviar`
  falso com `SEM_SESSAO` (o código que a entrada daria);
- dentro da tolerância, as decisões automáticas que já estavam em curso seguem a regra de antes
  (o envio falha e é refeito na volta).

**Fora há mais de `CONECTOR_FORA_TOLERANCIA_S`** (padrão 30), com posição ou ordem viva na corretora,
a operação fica **SUSPENSA**:
- A suspensão é gravada (`suspensa_em`, `suspensa_motivo`, migração 0016) e sobrevive a um restart.
- O alerta crítico **`CONECTOR_FORA_COM_POSICAO`** traz tudo para o usuário agir à mão: operação,
  uid, corretora, conta, lado, quantidade, preço médio, o que continua protegendo (a stop viva na
  corretora, com gatilho e limite, ou "sem stop: desprotegida"), a entrada viva, e a orientação de
  sair pelo home broker, pelo Profit ou pela mesa da corretora.
- O evento `SUSPENSA` leva os mesmos dados.
- **Nada sai da operação suspensa.** As decisões automáticas (stop nova, repreço, saída, zeragem,
  parada ZERAR, perda do dia) não andam, e uma rede de segurança no envio descarta qualquer mensagem
  dela. Os pedidos são recusados com `OPERACAO_SUSPENSA` depois que o canal volta.
- Um conector **desligado** (PnT com `PNT_DESLIGADO`, Flexscan sem `FLEXSCAN_URL`) com operação
  aberta nele suspende na hora.

**A Flexscan sem resposta:** `FLEXSCAN_SEM_RESPOSTA_N` (padrão 3) pedidos seguidos sem resposta (erro
de rede ou HTTP 502/503/504) derrubam a sessão da ponte. É o mesmo `SESSAO_CAIU` do token. A ponte
tenta logar a cada `FLEXSCAN_ESPERA_LOGIN_S`, e com uma ordem viva a consulta de 5 s acha a falha
em cerca de 15 s.

**Na volta:**
- A conciliação de sempre roda (mass status, `GET /orders/filter`).
- As ordens da operação suspensa **não são julgadas**: nada de `DESCONHECIDA`, nada de `DIVERGENTE`,
  nada de parada automática por elas, e a stop não é recriada.
- Um fill que chega (a stop executou na queda) zera a operação normalmente (`ZERADA`, origem
  `STOP_SERVIDOR`), e isso desfaz a suspensão.
- Senão, a conciliação **sugere** o caso, no alerta crítico `OPERACAO_SUSPENSA`, no evento
  `SUSPENSA_CONFERIR` e no `/saude`:
  - "stop viva na corretora, sem fill: a posição provavelmente continua aberta";
  - "stop cancelada fora sem fill: provável zeragem manual";
  - "entrada cancelada fora" / "entrada viva na corretora";
  - "sem stop na corretora antes da queda: não dá para saber";
  - "a corretora não respondeu à consulta: confira à mão".
- **Nunca fecha sozinha.** As ordens de outros (a venda do home broker) seguem ignoradas, como sempre.

**A confirmação do admin:** `POST /operacoes/{id}/confirmar-posicao` com
`{"posicao": N, "motivo": "...", "por": UID}`, ou `kalman-ordens operacao confirmar -op N -posicao P
-motivo X -por UID`. `posicao` é a posição na corretora, com sinal: `1` comprado, `-1` vendido, `0`
zerada. Só vale com o canal de volta e a conciliação fechada (senão, `CONECTOR_FORA` ou
`CONCILIANDO`).

| posição informada | o serviço | resposta |
|---|---|---|
| `0`, sem ordem viva na corretora | fecha `ENCERRADA_FORA`, sem ordem; evento `ENCERRADA_FORA` | `ENCERRADA_FORA` |
| `0`, com a stop do serviço viva lá | cancela a stop (a **única** ordem); fecha `ENCERRADA_FORA` quando o cancelamento confirma | `CANCELANDO_STOP` |
| a do sistema | volta ao fluxo normal; a stop que sumiu é recriada; evento `RETOMADA` | `RETOMADA` |
| outra | segue SUSPENSA; alerta crítico `DIVERGENTE` com os números | `aceito: false`, `POSICAO_DIVERGENTE` |

`ENCERRADA_FORA` é um estado final novo da operação. A posição é 0, a origem da saída `FORA`, o motivo
"encerrada fora do sistema: ..." e o `pnl_pts` só o que o serviço viu. O preço da saída manual ele não
conhece.

**Roteiro 121:** o PnT caído com a posição aberta.
- Na hora, a saída, o ajuste e o encerrar são recusados, e o canal aparece no `/saude` e no `/destino`.
- Aos 30 s, a operação fica SUSPENSA, com o alerta para agir à mão.
- Na volta, sem a stop no mass status, a conciliação sugere "provável zeragem manual". Não sai nada e
  não há divergência.
- Depois do restart, a operação continua suspensa.
- Na confirmação: −1 dá divergência, e 0 encerra fora.
- Numa segunda operação, a stop ficou viva na corretora: a confirmação de 0 manda só o cancelamento.
- Numa terceira, nada mudou: a confirmação de 1 retoma, e a saída do gestor volta a andar.

### 3.1 `GET /saude` (os campos novos)

```json
{"canais": {"PNT":      {"estado": "FORA", "motivo": "a sessão FIX caiu", "desde": "2026-10-30T10:30:03.000-03:00"},
            "FLEXSCAN": {"estado": "NO_AR", "motivo": null, "desde": null}},
 "operacoes_suspensas": [
   {"modulo": "KALMAN", "operacao_id": 12101, "uid": 7, "desde": "2026-10-30T10:30:33.000-03:00",
    "motivo": "PnT fora do ar desde 10:30:03: a sessão FIX caiu", "sugestao": null,
    "conector": "PNT", "corretora": "BTG", "corretora_codigo": 85, "conta": "12345", "simbolo": "WINZ26",
    "posicao": 1, "lado": "C", "qtd": 1, "preco_medio": "130010",
    "stop": {"clordid": "KM-P-12101-1", "preco_stop": 129510, "preco_limite": 129410, "qtd": 1}}]}
```

`canais.<conector>.estado`:
- `NO_AR`;
- `FORA` (sessão caída, sem token, sem resposta);
- `DESLIGADO` (o conector não existe nesta instância);
- `DORMINDO` (a Flexscan fora da janela, §1, que não é queda).

No REAL, cada instância tem o seu bloco em `ambientes.<AMB>`.

### 3.2 `GET /usuarios/{uid}/destino` (v1.28)

O contrato da SPEC-AMBIENTES-v1 §4.3 com `canal`: o estado do conector resolvido (`null` sem
conector). Fora do ar, `pode_enviar` é falso com `SEM_SESSAO`, o que a entrada daria. **O Kalman
não pede entrada, saída nem ajuste enquanto o canal estiver fora.**

```json
{"uid": 5725, "ambientes": {
  "SIMULADOR": {"destino": "CONECTOR", "conector": "FLEXSCAN", "corretora": "BTG", "corretora_codigo": 85,
                "conta": "S-1117150", "habilitado": true, "permitido": true, "pode_enviar": true,
                "codigo": null, "motivo": null, "rotulo": "Simulador · Flexscan", "canal": "NO_AR"},
  "REAL":      {"destino": null, "conector": null, "corretora": null, "corretora_codigo": null,
                "conta": null, "habilitado": false, "permitido": false, "pode_enviar": false,
                "codigo": "AMBIENTE_NAO_PERMITIDO", "motivo": "REAL nao permitido nesta maquina",
                "rotulo": null, "canal": null}}}
```

### 3.3 `GET /ordens/{id}`

O campo novo é `suspensa`: `null`, ou `{"desde", "motivo", "sugestao"}`. O `estado` pode ser
`ENCERRADA_FORA`.

### 3.4 Eventos novos (`GET /eventos`)

| tipo | quando | dados |
|---|---|---|
| `SUSPENSA` | a operação ficou suspensa | `conector`, `corretora`, `corretora_codigo`, `conta`, `simbolo`, `posicao`, `lado`, `qtd`, `preco_medio`, `stop`, `motivo` |
| `SUSPENSA_CONFERIR` | o canal voltou; a sugestão da conciliação | os mesmos, com `sugestao` |
| `RETOMADA` | a confirmação com a posição do sistema | `posicao`, `motivo`, `por` |
| `ENCERRADA_FORA` | a operação fechou fora do sistema | `posicao_sistema`, `motivo`, `conta`, `conector` |

### 3.5 Configuração

| chave | padrão | o quê |
|---|---|---|
| `CONECTOR_FORA_TOLERANCIA_S` | `30` | o canal fora por mais que isto, com posição ou ordem viva, suspende a operação (0 = na hora) |
| `FLEXSCAN_SEM_RESPOSTA_N` | `3` | pedidos seguidos sem resposta que derrubam a sessão da Flexscan (0 = não derruba) |
