# SPEC kalman-ordens v1.19 — origem do stop; recusas "já executada" sem alerta (27/10/2026)

Base: v1.18 (instalada na .53, `4e0cb1924fcf`). Origem: 27/10, uid 5725 (Flexscan). A 234335 saiu
pela stop da corretora, movida antes pelo TRSP, e o Kalman mostrou `STOP_SERVIDOR`, o motivo genérico.
O serviço não sabia de onde vinha cada ajuste: o corpo do `POST /operacoes/{id}/stop` só tinha o
preço. Na mesma saída, a SAIDA `TRSP` do Kalman chegou 23 ms antes do fill da stop. O cancel da stop
voltou "ordem já executada" e gerou um `RECUSA_PNT`, que era rotina e não problema, e ainda trazia o
nome do conector errado. O Kalman vai estender a regra R5 ao TRSP e ao STOP_MANUAL: quem sai é a stop
na corretora, e a SAIDA do Kalman fica só como reserva.

Vale para os dois conectores (PnT: 35=G da 1014; Flexscan: PUT da stop).

## 1. Origem do ajuste do stop

1. **`origem` opcional no `POST /operacoes/{id}/stop`.** Texto de 2 a 20 caracteres: letras
   maiúsculas, dígitos e `_`, começando por letra. Sem o campo, a origem é `GESTOR`. `INICIAL` é do
   serviço e é recusado. Fora do formato, a recusa é `CAMPO_INVALIDO` (`aceito:false`, HTTP 200,
   como as outras recusas do ajuste). O valor não é conferido contra uma
   lista: o Kalman manda `TRSP` e `STOP_MANUAL`, e um valor novo passa sem mudar o serviço.
2. **A origem vale quando a corretora confirma, não quando o pedido chega.** O que conta é a stop que
   estava na corretora quando executou:
   - a 1014 nova (entrada, recriação) leva a origem do gatilho com que foi calculada: `INICIAL` com o
     `stop_pts` da entrada, ou a origem do último ajuste pedido (`StopAjustado`);
   - o replace do ajuste leva a origem do pedido que o gerou. Confirmado (150=5 / PUT confirmado), a
     operação passa a ter essa origem;
   - um ajuste `IGNORADO` (menor que o degrau), recusado pela corretora ou ainda `AGUARDANDO` quando a
     stop executa **não muda** a origem. Com dois ajustes em sequência (o segundo `AGUARDANDO`), a
     confirmação do primeiro dá a origem do primeiro;
   - o ajuste recusado (`PROTECAO_AJUSTE_RECUSADO`) devolve ao pedido guardado a origem da stop que
     ficou na corretora;
   - o replace de quantidade (entrada parcial) não muda a origem; o replace de saída (v1.11, PnT) não
     é ajuste.
3. **Na operação:** `stop_origem` (confirmada) e `stop_ajustado_origem` (a do `StopAjustado`, para
   a 1014 recriada), na migração **0013**, com `ADD COLUMN IF NOT EXISTS`. As operações antigas
   ficam nulas. Na v1.19 elas valiam `INICIAL`; na v1.20 valem desconhecido, e o campo vem `null`.
4. **Nos eventos e no GET:** o campo `stop_origem` em `PROTEGIDA`, `PROTECAO_AJUSTADA` e
   `PROTECAO_DISPARADA` (a origem da stop na corretora naquele momento), e em `SAIDA_EXECUTADA` e
   `ZERADA` quando a origem da saída é `STOP_SERVIDOR`. Também `stop_origem` no
   `GET /ordens/{id}`, no `GET /operacoes` e no histórico. A `origem` e o `motivo` do `ZERADA`
   continuam como hoje: o `motivo` pode ser o da SAIDA do gestor que chegou antes do fill (234335:
   `motivo TRSP`, `origem STOP_SERVIDOR`). Quem classifica é o par `origem STOP_SERVIDOR` +
   `stop_origem`.

## 2. Nome do alerta de recusa pelo conector

`RECUSA_PNT` passa a ser só do PnT. O que vem da Flexscan sai como **`RECUSA_FLEXSCAN`** (não
crítico, como o `RECUSA_PNT`). O conector é o da operação. Sem operação identificada, é o conector
de onde veio a mensagem. O texto não muda.

## 3. "Ordem já executada" com o fill chegando: só registro

Um 35=9 (cancel ou replace recusado) com `39=2` diz que a ordem já executou na corretora. O PnT manda
isso com `[IS-72]`, e a ponte da Flexscan com "[flexscan] ordem já executada". O fill pode ter
chegado antes do 35=9 (234335) ou chegar depois (HML 14/10: 100 ms).

1. **Fill já aplicado** (a ordem já está `EXECUTADA` no motor): só registro (log e evento FIX), sem
   alerta.
2. **Fill ainda não chegou:** sem alerta na hora. Se a ordem não estiver executada em
   `ESPERA_FILL_POS_RECUSA_S` (3 s), sai o `RECUSA_<CONECTOR>` com o texto do 35=9 e "sem fill em
   3s". O resto continua como hoje:
   - na saída, o repreço suspenso, o mass status em 3 s e o `SAIDA_SEM_FILL` (v1.3 I1);
   - no cancel da proteção durante a saída, `AGUARDANDO_STOP` (espera o fill por `ESPERA_STOP_S`).
     O `CANCEL_STOP_REJEITADO` só sai se o fill não chegar em 3 s;
   - no ajuste recusado, o `PROTECAO_AJUSTE_RECUSADO` (evento, não alerta).
3. **Recusa com outro 39** (ex.: `39=0`/`B`, "being executed by the robot", roteiro 06): não muda;
   alerta como hoje.

## 4. SAIDA depois do ZERADA pela stop: só registro

O `POST /ordens` SAIDA de uma operação que já zerou pela stop responde `SEM_POSICAO`, como hoje, sem o
alerta `SEM_POSICAO`. Isso já valia para a `origem STOP_SERVIDOR` (234334: SAIDA TRSP 228 ms depois
do ZERADA, recusada sem alerta) e passa a valer também para `STOP_FURADO`. Outra operação sem posição
continua alertando.

## 5. `msgtype` do histórico

A coluna `kalmanordemevento.msgtype` passa de `VARCHAR(4)` para `VARCHAR(20)` na mesma 0013. O
repositório deixa de cortar em 4. Cabem `FLX-POST`, `FLX-PUT`, `FLX-DELETE`, `FLX-WH`, `FLX-PRECO`,
`FLX-SEMRESPOSTA` e o status HTTP (`FLX-200`, `FLX-404`): o histórico distingue o webhook da resposta
da REST. Os eventos já gravados ficam como estão (`FLX-`). Aumentar um `VARCHAR` dentro de 255 bytes
não reescreve a tabela.

Ficam para o pacote seguinte (SPEC-FLEXSCAN): a conferência do cadastro do webhook e a espera antes
da primeira consulta depois do 202.

## 6. Contrato para o Kalman

**Pedido: `POST /operacoes/{id}/stop`.** Um campo novo, opcional:

```
{"modulo": "KALMAN", "uid": 5725, "preco_stop": 178420.0, "origem": "TRSP"}
```

| Campo | Valores |
|---|---|
| `origem` | `TRSP` (trailing), `STOP_MANUAL` (edição à mão), ou outro com 2–20 de `A–Z`, `0–9`, `_`, começando por letra. Ausente = `GESTOR`. `INICIAL` é recusado (é do serviço). Formato inválido → `{"aceito":false,"codigo":"CAMPO_INVALIDO"}` (HTTP 200) |

O corpo continua estrito. **O Kalman só pode mandar `origem` depois de a v1.19 estar instalada:**
antes dela, o campo dá 400 `CAMPO_INVALIDO` e o ajuste não sai.

**Eventos (`GET /eventos`):** o campo **`stop_origem`** traz a origem da stop que está (ou estava) na
corretora. Os valores são `INICIAL`, `GESTOR` ou o que o Kalman mandou. Pela v1.20, também pode vir
**`null` = desconhecida** (operação anterior à v1.19): o Kalman mantém `STOP_SERVIDOR`.

| Evento | `stop_origem` |
|---|---|
| `PROTEGIDA` | sempre (`INICIAL` na 1014 da entrada; a origem do ajuste numa recriada; `null` se desconhecida) |
| `PROTECAO_AJUSTADA` | sempre: a origem do ajuste confirmado |
| `PROTECAO_DISPARADA` | sempre: a origem da stop que disparou |
| `SAIDA_EXECUTADA` | só com `origem` = `STOP_SERVIDOR` (fill da stop) |
| `ZERADA` | só com `origem` = `STOP_SERVIDOR` |

Classificação sugerida para o Kalman, no `ZERADA` com `origem STOP_SERVIDOR`, **pelo `stop_origem` e
não pelo `motivo`**:

| `stop_origem` | Motivo exibido |
|---|---|
| `null` (desconhecida: operação anterior à v1.19; v1.20) | `STOP_SERVIDOR` (como hoje) |
| `INICIAL` | `STOP` (antes: `STOP_SERVIDOR`) |
| `TRSP` | `TRSP_SERVIDOR` |
| `STOP_MANUAL` | `STOP_MANUAL_SERVIDOR` |
| `GESTOR` | `STOP_SERVIDOR` (ajuste sem origem) |

**Consultas:** `stop_origem` no `GET /ordens/{id}`, em cada operação do `GET /operacoes` e na
`operacao` do `GET /operacoes/{id}/historico`.

**Alertas (evento `ALERTA`, campo `codigo`):**
- `RECUSA_FLEXSCAN` é novo, o equivalente do `RECUSA_PNT` para as operações da Flexscan. Os textos
  informativos do `RECUSA_PNT` (`[IS-72]`, `[DSA-122]`) passam a valer para os dois;
- a SAIDA do Kalman que chega depois do ZERADA pela stop recebe `SEM_POSICAO` (resposta) sem alerta;
- a SAIDA que chega antes do fill da stop é aceita. O cancel da stop volta "já executada", a stop
  zera a operação (`origem STOP_SERVIDOR`) e não há alerta.

## 7. Testes

- Roteiro **112** (`112-origem-do-stop.fix`, PnT): 1014 da entrada `INICIAL`. Ajuste `TRSP`
  confirmado. Ajuste `STOP_MANUAL` ignorado (degrau): a origem segue `TRSP`. Dois ajustes em
  sequência, o primeiro `TRSP` confirmado e o segundo `STOP_MANUAL` ainda `AGUARDANDO` quando a stop
  dispara e executa: `PROTECAO_DISPARADA`, `SAIDA_EXECUTADA` e `ZERADA` com `stop_origem TRSP`.
  Origem inválida e `INICIAL` → `CAMPO_INVALIDO`. Sem origem → `GESTOR`. O replace do ajuste
  pendente volta 35=9 com 39=2 depois do fill da stop: sem alerta. SAIDA `TRSP` depois do ZERADA:
  `SEM_POSICAO` sem alerta. Recusa do ajuste → a origem fica a da
  stop na corretora. `GET /ordens` e `GET /operacoes` com `stop_origem`. Restart no meio: a origem
  volta do banco e a 1014 recriada leva a `stop_ajustado_origem`.
- Roteiro **113** (`113-flexscan-stop-trsp-e-corrida.fix`): a 234335 na Flexscan falsa. Ajustes
  `TRSP`, SAIDA `TRSP` do gestor aceita, DELETE da stop e a stop executando antes:
  `ZERADA origem STOP_SERVIDOR motivo TRSP stop_origem TRSP`, sem `RECUSA_FLEXSCAN` nem `RECUSA_PNT`.
  O reenvio da mesma SAIDA → a resposta original (`repetido`), sem alerta. O ajuste recusado da
  Flexscan → `RECUSA_FLEXSCAN` (roteiro 97).
- Mudam: 51 (o fill em 100 ms → sem `RECUSA_PNT`), 52 e 53 (`RECUSA_PNT` aos 3 s, não no 35=9),
  97 (`RECUSA_FLEXSCAN`); `TestTodosOsRoteirosLeem` conta 2 roteiros a mais.
- Repositório: `TestMigracao0013StopOrigemEMsgtype` (a 0013 sobre o banco até a 0012, as duas
  colunas pelo `SalvarOperacao` e pelo lote, `msgtype` `FLX-SEMRESPOSTA`, a 0013 aplicada de novo);
  0013 sobre o banco instalado (`TestServirAtualizaDoInstalado`).
- Mutações conferidas: a origem pelo último pedido (e não pela confirmação) reprova o 112; o 39=2
  alertando na hora reprova o 51, o 112 e o 113.
