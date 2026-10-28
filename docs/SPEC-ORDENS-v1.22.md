# SPEC kalman-ordens v1.22 — resposta e envio antes da gravação, reconstituição na partida, webhook que não chega, latência por rota (28/10/2026)

Base: v1.21.1 (branch `v1.21.1`, `7ea3a0d`). Origem: o pregão de 28/10.

- **10:45:59, op 234352:** o `POST /operacoes/234352/stop` (TRSP) foi decidido às 10:45:59.626. O
  Kalman não recebeu a resposta em 2 s e repetiu. A volta do motor grava o lote antes de responder,
  e essa gravação levou ~2,2 s: um dos dois lotes lentos do dia (`lote_segundos`). No mesmo minuto,
  o `marketdata` da mesma máquina foi de `lag=258ms` para `lag=1.87s`. O PUT da proteção também
  esperou o banco.
- **Pedido de 28/10:**
  - a resposta e o envio da 1014 e da saída antes da gravação;
  - a conciliação da partida tratando a ordem KM da corretora sem linha no banco;
  - o alerta de webhook que não chega;
  - a latência do handler por rota;
  - os textos com o nome do conector.

## 1. A volta do motor: responde e envia antes de gravar

A volta (`fecharVolta`) passa a fazer, nesta ordem:

1. Envia a 1014 (nova, recriada, replace), a saída, os cancels e o mass status. Antes, isso esperava
   a gravação. Continua valendo a regra de sempre: 1014 > saída/cancel; a saída respeita o
   `maxEnviosPorVolta`.
2. Responde os pedidos HTTP da volta: `POST /ordens`, `POST /operacoes/{id}/stop`, `POST /sinais`,
   as ações do admin e a habilitação. A resposta vem da memória do motor, a mesma que o
   `GET /ordens/{id}` lê.
3. Grava o lote (uma transação, como antes). Acima de 1 s: `WARN "gravação do lote lenta"`, com
   `duracao_ms` e `linhas`.
4. Envia as entradas. Para a entrada continua valendo "gravar antes de enviar" (v1.10 B): ela não
   sai sem a operação no banco.
5. Responde os pedidos que esperavam a entrada e roda o que espera o commit (`Fazer`, `Parar`).

**Exceção:** um pedido que deixou uma entrada para sair nesta volta (`POST /ordens` de entrada, um
`/sinais` com entrada aceita) responde só depois do passo 4. A entrada que não sai muda a resposta
para `ENVIO_FALHOU`, como antes. O `stop`, a saída e o `/sinais` só de saídas respondem no passo 2.

**Risco aceito (decisão de 28/10):** se o processo cai entre o envio (1) e a gravação (3), a 1014
ou a saída fica na corretora sem a linha no banco. A conciliação da partida trata esse caso (§1.1).

### 1.1 Conciliação da partida: ordem KM na corretora sem linha no banco

Antes de aplicar o mass status, a conciliação procura, em cada resposta, uma ordem que o serviço
não conhece (nem pelo id da corretora, nem pelo ClOrdID atual ou original).

- **Reconstitui** quando o ClOrdID é do motor (`KM-[MOD-]E|P|S-<operacao>-<n>`) e a operação existe
  e não está encerrada. A ordem é recriada com o que a resposta traz: lado, quantidade, preço, o
  gatilho e o limite (20828/20829) e o id. O papel vem da letra.
  - O contador de ClOrdID do papel na operação passa do `<n>` dela: o próximo não repete.
  - Uma saída reconstituída viva (39 = 0, 1, 5, 6, A ou E) põe a operação em `SAINDO`. Assim, a
    1014 cancelada pela própria saída não parece "cancelada sem pedido nosso" (sem recriar a 1014).
  - Sai `ORDEM_RECONSTITUIDA` (**crítico**), com o ClOrdID, a corretora, a operação, o lado, a
    quantidade e o 39.
- **As reconstituídas são aplicadas primeiro.** O fill de uma saída executada zera a operação antes
  de a 1014 cancelada ser julgada. Depois vêm as outras respostas, como antes.
- **Sem a operação, com a operação encerrada ou com o ClOrdID fora do formato:** `DESCONHECIDA` e
  `DIVERGENTE`, como antes (roteiro 103).
- **Replace que saiu e não foi gravado:** a ordem é achada pelo id da corretora, como antes (na
  Flexscan o `name` não muda no PUT).

A ponte da Flexscan passa a pôr 20828 (gatilho) e 20829 (limite) no ExecutionReport do mass status
das stops, como o PnT.

## 2. Webhook que não chega

Duas coisas abrem uma espera pelo webhook de uma ordem:

- **Ação nossa:** um POST, PUT ou DELETE nosso respondido com **202**. A espera conta **do envio**,
  porque o webhook pode chegar antes de a ponte ler o 202 (os dois levam ~0,3 s). Se um webhook da
  ordem já chegou depois do envio, a espera não abre. Recusa (400) e "não sei" não abrem espera.
- **Mudança espontânea:** a consulta periódica vê uma mudança que o webhook não trouxe (fill, stop
  executado, cancelamento). Substitui o aviso imediato da SPEC-FLEXSCAN-v1 §2 item 6 e o "10 min
  sem webhook com ordem viva", recusado em 28/10.

O que acontece depois:

- **Webhook da ordem chegou:** a espera fecha.
- **Passou `FLEXSCAN_WEBHOOK_ESPERA_S` (5) sem ele:** `FLEXSCAN_WEBHOOK_ATRASADO` (não crítico, um
  por falta, com a ação ou a mudança e o `name`) e uma falta.
- **3 faltas seguidas:** `FLEXSCAN_WEBHOOK_SEM_CHEGAR` (**crítico**), e a conferência do cadastro
  (v1.21 §2 e v1.21.1 §1) roda no próximo Tick, sem esperar os 10 min.
- **Um webhook válido de qualquer ordem:** zera as faltas e resolve os dois alertas.
- **Sem o receptor ligado** (`FLEXSCAN_WEBHOOK_ENDERECO` vazio) ou com
  `FLEXSCAN_WEBHOOK_ESPERA_S=0`: nada disso.

`/saude.flexscan` e o `saude` do `/flexscan/trafego` ganham `webhook_faltas_seguidas` (inteiro).

Chave nova: `FLEXSCAN_WEBHOOK_ESPERA_S` (5). A `FLEXSCAN_WEBHOOK_ATRASO_S` nunca foi usada pelo
código e fica sem efeito.

## 3. Latência dos pedidos do gestor

**O que é medido:** do pedido recebido à resposta escrita, por rota:
- `STOP` (`POST /operacoes/{id}/stop`);
- `ENTRADA` e `SAIDA` (`POST /ordens`, pelo `papel`);
- `SINAIS` (`POST /sinais`).

Um `POST /ordens` com o corpo inválido entra como `ORDENS`.

**No `/saude`, bloco `http`:** uma chave por rota que já teve pedido.

```json
"http": {
  "STOP": {"n": 120, "p50_ms": 3.2, "max_ms": 41.0, "max_dia_ms": 2201.4,
           "max_dia_em": "2026-10-28T10:46:01.828-03:00"}
}
```

| Campo | Valor |
|---|---|
| `n` | quantas respostas há na janela (até 500, as últimas) |
| `p50_ms`, `max_ms` | da janela |
| `max_dia_ms`, `max_dia_em` | o maior do dia (relógio da máquina) e quando; `null` antes da primeira resposta do dia |

O bloco é da memória do processo e volta vazio no restart.

**No journal:** acima de 1 s, `WARN "resposta HTTP lenta"` com `rota`, `duracao_ms` e `status`.

**No `/metrics`:** o histograma `http_segundos` (todas as rotas) continua igual.

## 4. Textos com o conector

- **Log do ajuste da proteção:** `no_pnt=` passa a `na_corretora=`.
- **Textos das operações:** os alertas e logs de uma operação dizem a corretora dela ("Flexscan" ou
  "PnT"). São eles:
  - ajuste recusado;
  - cancelamento da entrada (admin);
  - estado de erro e o cancel rejeitado da 1014 em erro;
  - "1014 cancelada pela … sem pedido nosso";
  - replace da saída recusado com a ordem terminal;
  - `ENVIO_FALHOU`;
  - saída suspensa.
- **Textos sem operação:** o de banco e o da fila dizem "corretora".
- **Continuam "PnT"** os que só existem no PnT: sessão FIX, disjuntor de bolsa, 39=B do stop
  furado, túnel e a consulta `/pnt/ordens`.

## 5. Contrato para o Kalman

O contrato da v1.21 §4 continua valendo, com `webhook_faltas_seguidas` a mais nos dois blocos.

### 5.1 `GET /flexscan/trafego`

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
    "boot": "2026-10-27T09:20:20.512-03:00", "recarregadas": 0, "msgs_min": 12, "consultas_min": 40,
    "latencia_post_p50_ms": 58, "latencia_post_max_ms": 140
  }
}
```

### 5.2 `GET /saude` → `flexscan`

```json
{
  "logado": true, "token_expira": "2026-10-27T19:20:21-03:00", "receptor": true,
  "webhooks_recebidos": 7, "ultimo_webhook": "2026-10-27T09:45:59-03:00", "ultimo_erro": "",
  "ordens_acompanhadas": 6, "ordens_vivas": 0,
  "webhook_cadastrado": true, "webhook_conferido_em": "2026-10-27T09:52:40-03:00",
  "webhook_recadastrado_em": null, "webhook_faltas_seguidas": 0,
  "ultimo_login": {"em": "2026-10-27T09:20:21.004-03:00", "motivo": "partida", "resultado": "OK"},
  "simbolo": "WINZ26", "idade_preco_ms": 412,
  "fonte": {"degradada": false, "ruim_desde": null, "motivo": "", "janela": "09:00-14:30",
            "dia_de_pregao": true, "travado": false, "travado_desde": null, "travado_motivo": "",
            "url": "http://172.16.110.53:9090", "compartilhada": true}
}
```

`webhook_faltas_seguidas`: as esperas por webhook vencidas desde o último webhook (§2); 3 ou mais
= `FLEXSCAN_WEBHOOK_SEM_CHEGAR`.

## 6. Testes

- **Roteiro 116 (novo):**
  - a saída sai, o lote não chega ao banco (`banco_descarta`) e o processo reinicia;
  - a KM-S executada é reconstituída e zera a operação, sem divergência e sem recriar a 1014;
  - a KM-S viva é reconstituída, a operação fica saindo, e o fill pelo webhook zera;
  - o `/saude.http.SAIDA`.
- **Roteiro 101 (reescrito):**
  - a falta pelo POST, pelo PUT e pela mudança espontânea;
  - o crítico na terceira, com a conferência na hora;
  - um webhook resolve e zera; o webhook da ordem fecha a espera.
- **Roteiro 115:** o `/saude.http.STOP` e o `ENTRADA`; o contrato pela v1.22.
- **Verbo novo `banco_descarta`.**
- **Testes de unidade:** `TestLatencias` e `TestEsperaWebhook` (o webhook antes do 202).
- **Roteiros em memória:** todos passaram com a volta nova antes do alerta de webhook.
