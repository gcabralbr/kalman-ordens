# SPEC kalman-ordens v1.24 — o 404 de ordem já final pelo webhook é "ainda não registrada", e o IP de origem do webhook (29/10/2026)

Base: v1.23 (`38c17747d88d`, instalada em 29/10 às 14:10). Origem: o 404 de 29/10 às 11:02:00.624 (op 234413).

| hora | o quê |
|---|---|
| 11:02:00.519 | `POST` da KM-E-234413-1 (202 em 48 ms) |
| 11:02:00.613 | webhook: EXECUTADA 1/1 (94 ms depois do POST) |
| 11:02:00.624 | consulta **404** "Order not found": a de confirmação do fill, disparada pelo webhook 11 ms depois dele, sem a espera de 200 ms |
| 11:02:00.832 | consulta 200 EXECUTADA: a agendada pelo POST (313 ms) |

O webhook chegou antes de a API de consulta da Flexscan conhecer a ordem.

**Medição, 28 e 29/10:** 67 ordens limitadas executadas com webhook.
- **Em cerca de 2/3 a consulta agendada pelo POST chega antes do webhook.** Ela acha a ordem perto
  dos 210–310 ms depois do 202, e o webhook vem entre 400 e 1.100 ms.
- **Webhook antes dos 200 ms do 202:** 9 casos. A consulta imediata achou a ordem em 8, cerca de
  12 ms depois do webhook. Deu 404 só em 11:02.

**Decisão de 29/10:**
- **O fill da ordem limitada continua valendo só pela consulta** (SPEC-FLEXSCAN-v1 §5.2 item 6). O
  webhook só tem o segredo como autenticação, e um fill falso na saída deixaria a posição aberta e
  sem stop. Isso só será reavaliado com o filtro de IP ou uma assinatura (FX-7, §3).
- **A consulta que confirma não espera os 200 ms:** só um caso em 9 deu 404, e os outros 8 seriam
  atrasados em 100 a 190 ms.

## 1. A consulta pedida pelo webhook sai na hora

Vale para as duas consultas que o webhook dispara:
- a `confirma`: o fill de uma ordem limitada pelo webhook, ou um FINISHED sem a quantidade toda;
- a `webhook_invalido`: o webhook com forma desconhecida.

Elas saem na hora. **A exceção é o POST em curso** (o webhook chegou antes do 202): a consulta não
sai, e o Tick (100 ms) a solta depois do 202.

**Sem consultas repetidas:**
- a consulta do webhook consome o passo da consulta ativa do POST que venceu junto;
- se outra consulta já trouxe o estado final, a do webhook não sai.

## 2. 404 de ordem já final pelo webhook: "ainda não registrada"

Vale quando o webhook já trouxe o estado final (executada ou cancelada) e uma consulta qualquer
(confirma, ativa ou periódica) dá 404 até `JanelaRegistro` (2 s) depois do 202. Nesse caso:
- **Registro:** `kalmanordemevento` `FLX-404`, texto `GET ainda nao registrada: <erro>`.
- **Tráfego:** a linha CONSULTA 404 com resumo `<origem> -> ainda nao registrada: <erro>` e
  `"nao_registrada": true`. A recarga da partida também a marca.
- **Não conta como erro.** O campo `nao_registrada` existe nas linhas de todos os tipos e é `false`
  nas outras.
- **Repetição:** a consulta se repete 100 ms depois (a cada Tick). Em 29/10 às 11:02, ela teria
  achado a ordem perto dos 150 ms, em vez dos 313 ms.
- **Custo no pior caso:** a Flexscan sem conhecer a ordem por 2 s inteiros dá cerca de 20 consultas
  da mesma ordem.

**Depois dos 2 s,** o 404 volta a ser o de sempre (erro). Sem o webhook final (por exemplo, a proteção
recém-criada), o 404 também é o de sempre.

**Para o Kalman:** o filtro "só erros" e a contagem de erros da aba Flexscan ao vivo (`flxErro`, hoje
`status_http >= 400`) devem ignorar a linha com `nao_registrada: true`.

## 3. O IP de origem do webhook (FX-7)

O objetivo é ver se a Flexscan chama sempre do mesmo endereço antes de pôr o filtro de IP no nginx.
No sábado foi `177.71.196.212`.
- **Fonte:** o `X-Real-IP` do nginx (sem ele, o endereço da conexão).
- **Banco:** o IP já ia ao `kalmanordemevento` desde a ponte (23/10), no texto `origem <ip>` do
  `FLX-WH`.
- **Linha WEBHOOK do tráfego:** ganha `"ip"`. Nas outras linhas, `null`. A recarga da partida e o
  `?dia=` leem o IP do registro, então dá para ver os dias desde 23/10.
- **`saude.webhook_ips`:** `{"<ip>": n}`, os webhooks aceitos do buffer (do dia) por IP.
- **Recusados:** o webhook recusado (404) continua só no log, no máximo uma linha por minuto, com a
  origem.

## 4. Contrato para o Kalman

O contrato da v1.23 §3 continua valendo, com duas mudanças:
- `nao_registrada` e `ip` em cada linha;
- `webhook_ips` no `saude`.

### 4.1 `GET /flexscan/trafego`

```json
{
  "mensagens": [
    {"seq": 812, "em": "2026-10-27T09:37:40.794-03:00", "tipo": "POST", "direcao": "ENV",
     "status_http": 202, "duracao_ms": 61, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "C 1 WINZ26 limite 179130 conta S-1117150",
     "nao_registrada": false, "ip": null},
    {"seq": 813, "em": "2026-10-27T09:37:40.972-03:00", "tipo": "CONSULTA", "direcao": "ENV",
     "status_http": 404, "duracao_ms": 40, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "confirma -> ainda nao registrada: Order not found with requested id",
     "nao_registrada": true, "ip": null},
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
    "boot": "2026-10-27T09:20:20.512-03:00", "dia": "2026-10-27", "recarregadas": 0, "msgs_min": 12, "consultas_min": 40,
    "webhook_ips": {"177.71.196.212": 7},
    "latencia_post_p50_ms": 58, "latencia_post_max_ms": 140
  }
}
```

### 4.2 `GET /flexscan/trafego?dia=AAAA-MM-DD`

```json
{
  "dia": "2026-10-27",
  "mensagens": [
    {"seq": 812, "em": "2026-10-27T09:37:40.794-03:00", "tipo": "POST", "direcao": "ENV",
     "status_http": 202, "duracao_ms": 61, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "C 1 WINZ26 limite 179130 conta S-1117150",
     "nao_registrada": false, "ip": null},
    {"seq": 813, "em": "2026-10-27T09:37:40.972-03:00", "tipo": "CONSULTA", "direcao": "ENV",
     "status_http": 404, "duracao_ms": 40, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "confirma -> ainda nao registrada: Order not found with requested id",
     "nao_registrada": true, "ip": null},
    {"seq": 814, "em": "2026-10-27T09:42:41.120-03:00", "tipo": "WEBHOOK", "direcao": "REC",
     "status_http": 200, "duracao_ms": null, "clordid": "KM-TESTE-P-991027001-1",
     "flexscan_id": "9a0a6ca1-...", "operacao_id": 991027001, "modulo": "TESTE", "uid": 5725,
     "resumo": "1 retrato: KM-TESTE-P-991027001-1 estado 5 exec 0/1",
     "nao_registrada": false, "ip": "177.71.196.212"}
  ],
  "ultimo_seq": 814, "eventos": 1630, "cortado": false
}
```

## 5. Testes

- **Roteiro 117 (novo):** reproduz 29/10 às 11:02.
  - **Webhook final no 202:**
    - a consulta sai na hora e dá 404 "ainda não registrada" (`nao_registrada=true`);
    - ela repete aos 100 e 200 ms e acha a ordem aos 300 ms, e o fill vale;
    - a proteção recém-criada, sem webhook final, toma o 404 de sempre;
    - o IP de origem fica na linha WEBHOOK e em `saude.webhook_ips`.
  - **Restart:** traz a linha marcada e o IP.
  - **A consulta não acha a ordem por 3 s:**
    - 20 linhas marcadas até os 2 s, e a repetição seguinte já é erro e não repete;
    - o passo de 2,6 s também é erro;
    - o timeout da entrada pede o cancel, e a periódica acha a ordem executada e aplica o fill.
- **Roteiro 115:** os contratos pela v1.24 §4.1 e §4.2.
