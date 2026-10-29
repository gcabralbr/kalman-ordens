# SPEC kalman-ordens v1.23 — o tráfego da Flexscan vira à meia-noite e consulta dias passados (29/10/2026)

Base: v1.22 (`9dab71f`). Origem: a aba "Flexscan ao vivo" de 29/10 mostrava o 28/10 junto. O buffer
do `/flexscan/trafego` não virava à meia-noite: depois de um restart, ele guardava o dia recarregado e
acumulava os dias seguintes.

## 1. A virada do dia no buffer

- O buffer guarda só o dia corrente, à meia-noite de Brasília.
- A conferência roda a cada linha nova, a cada consulta e na recarga da partida. Ela não depende de
  haver tráfego.
- No dia novo:
  - as linhas de antes da meia-noite saem do buffer (o `kalmanordemevento` guarda tudo, e o dia
    passado sai pelo `?dia=`, §2);
  - `saude.recarregadas` volta a 0, porque as recarregadas eram todas do dia da partida;
  - a latência dos POSTs (`latencia_post_*`) passa a contar só os do dia.
- **O `seq` não volta a 1:** segue a numeração, e um `desde` antigo continua valendo.
- **A recarga da partida** só aceita as linhas do dia corrente.
- **`saude.dia` (novo):** `"AAAA-MM-DD"`, o dia do buffer.

**Para o Kalman:** a aba guarda as linhas que já recebeu. Quando `saude.dia` mudar, ela deve limpar a
lista. O `boot` e o `ultimo_seq` não mudam na virada, então não servem de sinal.

## 2. `GET /flexscan/trafego?dia=AAAA-MM-DD`

- **O dia inteiro, do `kalmanordemevento`:** mesma conversão da recarga da partida (v1.21 §1b),
  então as consultas que não mudaram o estado não aparecem.
- **Teto:** até 50.000 eventos. Acima disso vêm os mais recentes, com `"cortado": true`.
- `desde` e `limite` não valem com `dia`.
- `seq` vai de 1 a N, na ordem do dia.
- `operacao_id` e `modulo` vêm do ClOrdID. O `uid` vem das operações gravadas daquele dia.
- **Sem bloco `saude`:** o estado da ponte só existe para o dia corrente.
- `dia` também pode ser hoje: aí a resposta vem do banco, não do buffer.

A forma está no §3.2.

- **Erros:** `dia` fora do formato dá 400 `{"erro":"dia invalido (AAAA-MM-DD)"}`. Sem a Flexscan, 404,
  como no ao vivo.

## 3. Contrato para o Kalman

O contrato da v1.22 §5 continua valendo. O `/saude` → `flexscan` (v1.22 §5.2) não muda.

### 3.1 `GET /flexscan/trafego`

`saude.dia` é o campo novo.

```json
{
  "mensagens": [
    {"seq": 812, "em": "2026-10-27T09:37:40.794-03:00", "tipo": "POST", "direcao": "ENV",
     "status_http": 202, "duracao_ms": 61, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "C 1 WINZ26 limite 179130 conta S-1117150"},
    {"seq": 813, "em": "2026-10-27T09:37:40.972-03:00", "tipo": "CONSULTA", "direcao": "ENV",
     "status_http": 404, "duracao_ms": 40, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "Order not found with requested id"},
    {"seq": 814, "em": "2026-10-27T09:42:41.120-03:00", "tipo": "WEBHOOK", "direcao": "REC",
     "status_http": 200, "duracao_ms": null, "clordid": "KM-TESTE-P-991027001-1",
     "flexscan_id": "9a0a6ca1-...", "operacao_id": 991027001, "modulo": "TESTE", "uid": 5725,
     "resumo": "1 retrato: KM-TESTE-P-991027001-1 estado 5 exec 0/1"}
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

As linhas têm o formato do §3.1. O `seq` vai de 1 a N no dia, e não há bloco `saude`.

```json
{
  "dia": "2026-10-27",
  "mensagens": [
    {"seq": 812, "em": "2026-10-27T09:37:40.794-03:00", "tipo": "POST", "direcao": "ENV",
     "status_http": 202, "duracao_ms": 61, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "C 1 WINZ26 limite 179130 conta S-1117150"},
    {"seq": 813, "em": "2026-10-27T09:37:40.972-03:00", "tipo": "CONSULTA", "direcao": "ENV",
     "status_http": 404, "duracao_ms": 40, "clordid": "KM-TESTE-E-991027001-1",
     "flexscan_id": "80b78b76-3ce7-4c15-acb8-461c865597fb", "operacao_id": 991027001,
     "modulo": "TESTE", "uid": 5725, "resumo": "Order not found with requested id"},
    {"seq": 814, "em": "2026-10-27T09:42:41.120-03:00", "tipo": "WEBHOOK", "direcao": "REC",
     "status_http": 200, "duracao_ms": null, "clordid": "KM-TESTE-P-991027001-1",
     "flexscan_id": "9a0a6ca1-...", "operacao_id": 991027001, "modulo": "TESTE", "uid": 5725,
     "resumo": "1 retrato: KM-TESTE-P-991027001-1 estado 5 exec 0/1"}
  ],
  "ultimo_seq": 814, "eventos": 1630, "cortado": false
}
```

## 4. Testes

- `TestTrafegoViradaDoDia`:
  - a recarga descarta a linha da véspera;
  - a consulta sozinha vira o dia;
  - `recarregadas` volta a 0, o `seq` segue e `saude.dia` muda;
  - o buffer cheio esvazia inteiro na virada pela linha nova.
- `TestTrafegoFlexscanDoDia`:
  - só os eventos do dia pedido, numerados de 1 a N;
  - a operação e o dono de cada linha;
  - `ErrDia` para dia inválido, e `ok=false` sem a Flexscan.
- **Roteiro 115:**
  - o contrato pelo §3.1;
  - o `?dia=` do dia do roteiro pelo §3.2, com as linhas do registro e a operação;
  - o `?dia=` de um dia sem tráfego vem vazio;
  - o dia inválido dá 400.
