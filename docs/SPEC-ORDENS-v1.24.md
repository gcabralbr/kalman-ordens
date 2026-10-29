# SPEC kalman-ordens v1.24 — a consulta do webhook espera o 202, e o 404 de ordem já final é "ainda não registrada" (29/10/2026)

Base: v1.23 (`38c17747d88d`, instalada em 29/10 às 14:10). Origem: o 404 de 29/10 às 11:02:00.624 (op 234413).

| hora | o quê |
|---|---|
| 11:02:00.519 | `POST` da KM-E-234413-1 (202 em 48 ms) |
| 11:02:00.613 | webhook: EXECUTADA 1/1 (94 ms depois do POST) |
| 11:02:00.624 | consulta **404** "Order not found": a de confirmação do fill, disparada pelo webhook 11 ms depois dele, sem a espera de 200 ms |
| 11:02:00.832 | consulta 200 EXECUTADA: a agendada pelo POST (313 ms) |

O webhook chegou antes de a API de consulta da Flexscan conhecer a ordem. A espera de
`FLEXSCAN_PRIMEIRA_CONSULTA_MS` (v1.21) só valia para a consulta agendada pelo POST.

## 1. A consulta pedida pelo webhook espera o 202

Vale para as duas consultas que o webhook dispara:
- a `confirma`: o fill de uma ordem limitada pelo webhook (o que vale é a consulta, SPEC §5.2 item 6),
  ou um FINISHED sem a quantidade toda;
- a `webhook_invalido`: o webhook com forma desconhecida.

Elas seguem a mesma espera da primeira consulta depois do POST:
- **POST em curso** (o webhook chegou antes do 202): a consulta não sai.
- **Depois do 202:** sai quando passam `FLEXSCAN_PRIMEIRA_CONSULTA_MS` (200), soltas pelo Tick (100 ms).
- **Ordem sem POST nosso neste processo** (aprendida na partida): sai na hora, como antes.
- **Sem consultas repetidas:**
  - a consulta do webhook consome o passo da consulta ativa do POST que venceu junto;
  - se outra consulta já trouxe o estado final, a do webhook não sai.

**Efeito:** com o webhook mais rápido que 200 ms, o fill da ordem limitada chega ao motor 200 ms
depois do 202, e não mais na hora. Em 29/10 ele chegou aos 313 ms, pela consulta agendada, porque a
imediata deu 404.

## 2. 404 de ordem já final pelo webhook: "ainda não registrada"

Vale quando o webhook já trouxe o estado final (executada ou cancelada) e uma consulta qualquer
(confirma, ativa ou periódica) dá 404 até `JanelaRegistro` (2 s) depois do 202. Nesse caso:
- **Registro:** `kalmanordemevento` `FLX-404`, texto `GET ainda nao registrada: <erro>`.
- **Tráfego:** a linha CONSULTA 404 com resumo `<origem> -> ainda nao registrada: <erro>` e
  `"nao_registrada": true`. A recarga da partida também a marca.
- **Não conta como erro.** O campo `nao_registrada` existe nas linhas de todos os tipos e é `false`
  nas outras.
- **Repetição:** no próximo passo da consulta ativa (0,5 / 0,8 / 1,4 s depois do 202). Sem passo pela
  frente, 300 ms depois.

**Depois dos 2 s,** o 404 volta a ser o de sempre (erro). Sem o webhook final (por exemplo, a proteção
recém-criada), o 404 também é o de sempre.

**Para o Kalman:** o filtro "só erros" e a contagem de erros da aba Flexscan ao vivo (`flxErro`, hoje
`status_http >= 400`) devem ignorar a linha com `nao_registrada: true`.

## 3. Contrato para o Kalman

O contrato da v1.23 §3 continua valendo, com `nao_registrada` a mais em cada linha.

### 3.1 `GET /flexscan/trafego`

```json
{
  "mensagens": [
    {"seq": 812, "em": "2026-10-27T09:37:40.794-03:00", "tipo": "POST", "direcao": "ENV",
     "status_http": 202, "duracao_ms": 61, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "C 1 WINZ26 limite 179130 conta S-1117150",
     "nao_registrada": false},
    {"seq": 813, "em": "2026-10-27T09:37:40.972-03:00", "tipo": "CONSULTA", "direcao": "ENV",
     "status_http": 404, "duracao_ms": 40, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "confirma -> ainda nao registrada: Order not found with requested id",
     "nao_registrada": true},
    {"seq": 814, "em": "2026-10-27T09:42:41.120-03:00", "tipo": "WEBHOOK", "direcao": "REC",
     "status_http": 200, "duracao_ms": null, "clordid": "KM-TESTE-P-991027001-1",
     "flexscan_id": "9a0a6ca1-...", "operacao_id": 991027001, "modulo": "TESTE", "uid": 5725,
     "resumo": "1 retrato: KM-TESTE-P-991027001-1 estado 5 exec 0/1",
     "nao_registrada": false}
  ],
  "ultimo_seq": 814,
  "saude": {
    "logado": true, "token_expira": "2026-10-27T19:20:21-03:00", "receptor": true,
    "webhooks_recebidos": 7, "ultimo_webhook": "2026-10-27T09:45:59-03:00", "ultimo_erro": "",
    "ordens_acompanhadas": 6, "ordens_vivas": 0,
    "webhook_cadastrado": true, "webhook_conferido_em": "2026-10-27T09:52:40-03:00",
    "webhook_recadastrado_em": null, "webhook_faltas_seguidas": 0,
    "ultimo_login": {"em": "2026-10-27T09:20:21.004-03:00", "motivo": "partida", "resultado": "OK"},
    "boot": "2026-10-27T09:20:20.512-03:00", "dia": "2026-10-27", "recarregadas": 0, "msgs_min": 12, "consultas_min": 40,
    "latencia_post_p50_ms": 58, "latencia_post_max_ms": 140
  }
}
```

### 3.2 `GET /flexscan/trafego?dia=AAAA-MM-DD`

```json
{
  "dia": "2026-10-27",
  "mensagens": [
    {"seq": 812, "em": "2026-10-27T09:37:40.794-03:00", "tipo": "POST", "direcao": "ENV",
     "status_http": 202, "duracao_ms": 61, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "C 1 WINZ26 limite 179130 conta S-1117150",
     "nao_registrada": false},
    {"seq": 813, "em": "2026-10-27T09:37:40.972-03:00", "tipo": "CONSULTA", "direcao": "ENV",
     "status_http": 404, "duracao_ms": 40, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "confirma -> ainda nao registrada: Order not found with requested id",
     "nao_registrada": true},
    {"seq": 814, "em": "2026-10-27T09:42:41.120-03:00", "tipo": "WEBHOOK", "direcao": "REC",
     "status_http": 200, "duracao_ms": null, "clordid": "KM-TESTE-P-991027001-1",
     "flexscan_id": "9a0a6ca1-...", "operacao_id": 991027001, "modulo": "TESTE", "uid": 5725,
     "resumo": "1 retrato: KM-TESTE-P-991027001-1 estado 5 exec 0/1",
     "nao_registrada": false}
  ],
  "ultimo_seq": 814, "eventos": 1630, "cortado": false
}
```

## 4. Testes

- **Roteiro 117 (novo):** reproduz 29/10 às 11:02.
  - **Webhook final no 202:** nenhuma consulta antes dos 200 ms. A de 200 ms dá 404 "ainda não
    registrada" (`nao_registrada=true`), e a seguinte acha a ordem e aplica o fill. A proteção
    recém-criada, sem webhook final, toma o 404 de sempre.
  - **Restart:** traz a linha marcada.
  - **A consulta não acha a ordem por 3 s:**
    - 4 linhas marcadas (200, 500, 800 e 1.400 ms, uma por vez) e o passo de 2,6 s como erro;
    - o timeout da entrada pede o cancel, e a periódica acha a ordem executada e aplica o fill.
- **Roteiro 115:**
  - o fill da saída pelo webhook espera os 200 ms;
  - os contratos pela v1.24 §3.1 e §3.2.
