# SPEC kalman-ordens v1.21 — Flexscan ao vivo (tráfego da ponte, webhook cadastrado, fonte) (27/10/2026)

Base: v1.20 (branch `feat/stop-origem-nulo`, mesmo pacote). Pedido do usuário: uma aba "Flexscan ao
vivo" na `kalman-admin.html`, equivalente à "PnT ao vivo" (`GET /sessao/ao-vivo`, v1.10). O serviço
dá três coisas:
- o tráfego da ponte;
- no `/saude.flexscan`, o cadastro do webhook e o último login;
- a fonte de preço da Flexscan com o mesmo detalhe da do PnT.

A conferência do cadastro do webhook (SPEC-FLEXSCAN-v1 §5.4) ficou fora do mínimo útil e entra
aqui, com o recadastro pedido em 27/10.

## 1. `GET /flexscan/trafego?desde=<seq>&limite=<N>`

O monitor, como o `/sessao/ao-vivo`, guarda as últimas 2.000 linhas num buffer em memória. Nada vai
ao banco: o registro completo continua no `kalmanordemevento` (`msgtype FLX-*`, v1.19).

- `desde`: devolve só as linhas com `seq` maior (0 ou ausente = desde o começo do buffer). `limite`:
  padrão 500, máximo 2.000. As linhas vêm da mais antiga para a mais nova.
- Sem o conector Flexscan configurado (`FLEXSCAN_URL` vazio): 404 `{"erro":"flexscan nao configurada"}`.
  `desde`/`limite` inválidos: 400, como no ao-vivo.
- Uma linha por troca HTTP com a Flexscan: o pedido e a resposta, com o status e a duração. Também
  uma por webhook recebido e uma por ajuste de preço ao túnel.
- **Sem segredo:** nada de token, senha, `Authorization`, segredo do webhook (nem na URL) ou corpo de
  login. O `resumo` é montado pelo serviço a partir dos campos de negócio. O corpo cru não é
  repassado.
- **Consultas** (`GET /orders/{id}`, `/orders/filter`): só entram quando o status não é 200 (ex.: o
  404 "Order not found" logo depois do 202) ou quando mudam o estado de uma ordem. As que não trazem
  novidade só contam em `saude.consultas_min`, para não encher o buffer.

## 2. `/saude.flexscan`: webhook cadastrado e último login

- **Conferência do cadastro:** `GET /webhooks` na partida (depois do login) e a cada
  `FLEXSCAN_WEBHOOK_CONFERE_MIN` (60). Procura o nosso URL (`FLEXSCAN_WEBHOOK_URL`) com o trigger
  `order.all`.
  - **Ausente:** recadastra (`POST /webhooks`, `target {type:"user"}`, triggers `order.all`, **sem
    `remove_previous`**, sem mexer nos outros cadastros) e confere de novo. Alerta **crítico**
    `FLEXSCAN_WEBHOOK_AUSENTE`, com "recadastrado" ou com o erro do recadastro. O alerta se resolve
    na próxima conferência que achar o URL.
  - **Erro na conferência** (timeout, 5xx): `webhook_cadastrado` fica `null` (não conferido), com
    aviso não crítico no log. O 401 segue o caminho de login de sempre.
  - **Sem `FLEXSCAN_WEBHOOK_URL` configurado:** não confere. `webhook_cadastrado` fica `null`.
- **Último login:** cada login, com o motivo que a ponte já registra (`partida`, `renovacao`, `401`,
  `reconexao`, `sem token`) e o resultado.

## 3. A fonte da Flexscan no `/saude`

`/saude.flexscan` passa a ter **sempre** `simbolo`, `idade_preco_ms` e `fonte`, com os mesmos campos
da fonte do PnT (o `fonte` do topo) e mais `url` e `compartilhada`. Com a mesma URL do PnT (sem
`PRECOS_URL_FLEXSCAN` próprio), `compartilhada` vem `true` e os valores são os do PnT. **Hoje a .53
está assim:** o `/saude` de 27/10 não traz `flexscan.fonte`, ou seja, a Flexscan lê a mesma fonte do
PnT. A conferir no `service.env`: a SPEC-FLEXSCAN §14 previa PnT → 9096 (HML D-1) e Flexscan → 9090
(D0).

## 4. Contrato para o Kalman

### 4.1 `GET /flexscan/trafego`

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
  "saude": { "...": "os campos de /saude.flexscan (4.2)", "msgs_min": 12, "consultas_min": 40,
             "latencia_post_p50_ms": 58, "latencia_post_max_ms": 140 }
}
```

| Campo | Tipo | Valor |
|---|---|---|
| `seq` | inteiro | crescente no processo; volta a 1 no restart (o `saude.boot` muda) |
| `em` | texto | hora do pedido (ENV) ou da chegada (REC), `AAAA-MM-DDTHH:MM:SS.mmm-03:00` |
| `tipo` | texto | `POST`, `PUT`, `DELETE`, `CONSULTA`, `WEBHOOK`, `FLX-PRECO`, `LOGIN`, `WEBHOOK_CADASTRO` |
| `direcao` | texto | `ENV` (serviço → Flexscan) ou `REC` (Flexscan → serviço: webhook, `FLX-PRECO`) |
| `status_http` | inteiro ou `null` | o status da resposta da Flexscan. No `WEBHOOK`, o que o serviço respondeu (200, 400, 404). `null` sem resposta (timeout: o `resumo` diz "sem resposta") e no `FLX-PRECO` |
| `duracao_ms` | inteiro ou `null` | do pedido à resposta; `null` no `WEBHOOK`, no `FLX-PRECO` e sem resposta |
| `clordid` | texto ou `null` | o `name` da ordem (`KM-...`); `null` no `LOGIN`, no `WEBHOOK_CADASTRO` e no webhook de várias ordens |
| `flexscan_id` | texto ou `null` | o id da ordem na Flexscan |
| `operacao_id`, `modulo`, `uid` | ou `null` | da operação dona da ordem; `null` quando não há (login, cadastro, ordem não KM) |
| `resumo` | texto | até 200 caracteres ASCII, sem segredo: lado, qtd, símbolo, preços e conta no POST; gatilho e limite novos no PUT; o texto de erro da Flexscan; no webhook, os retratos (name, estado, executado); no `FLX-PRECO`, "enviado X efetivo Y"; no `LOGIN`, o motivo e o resultado |

`saude` traz os campos do `/saude.flexscan` (4.2) e mais:
- `boot`: a hora da partida do processo;
- `msgs_min`: linhas no último minuto;
- `consultas_min`: consultas no último minuto, inclusive as que não entram no buffer;
- `latencia_post_p50_ms` / `latencia_post_max_ms`: a duração dos POSTs no buffer (`null` sem POST).

### 4.2 `GET /saude` → `flexscan` (os campos novos)

| Campo | Tipo | Valor |
|---|---|---|
| `webhook_cadastrado` | `true` / `false` / `null` | resultado da última conferência; `null` = ainda não conferido, a conferência falhou, ou não há `FLEXSCAN_WEBHOOK_URL` |
| `webhook_conferido_em` | texto ou `null` | hora da última conferência que respondeu |
| `webhook_recadastrado_em` | texto ou `null` | hora do último recadastro feito pelo serviço |
| `ultimo_login` | objeto ou `null` | `{"em": "...", "motivo": "partida\|renovacao\|401\|reconexao\|sem token", "resultado": "OK\|RECUSADO\|FALHOU"}` |
| `simbolo` | texto | o contrato vigente na fonte da Flexscan |
| `idade_preco_ms` | inteiro ou `null` | como o do topo, da fonte da Flexscan |
| `fonte` | objeto | os campos do `fonte` do topo (`degradada`, `ruim_desde`, `motivo`, `janela`, `dia_de_pregao`, `travado`, `travado_desde`, `travado_motivo`, `sem_pregao` quando não é dia de pregão), mais `url` (a do `/precos/atual`) e `compartilhada` (`true` = a mesma fonte do PnT) |

Os campos que já existem continuam iguais: `logado`, `token_expira`, `receptor`, `webhooks_recebidos`,
`ultimo_webhook`, `ultimo_erro`, `ordens_acompanhadas` e `ordens_vivas`.

### 4.3 Alerta novo

`FLEXSCAN_WEBHOOK_AUSENTE`, **crítico**: o webhook não está cadastrado na Flexscan. O texto diz se o
serviço recadastrou ou o erro do recadastro. Sai `RESOLVIDO` quando a conferência acha o URL. Vai
para o topo da tela, como os outros críticos do `/saude`.

## 5. Testes

- Roteiro **114** (Flexscan falsa), com `GET /webhooks` e `POST /webhooks` na falsa:
  - **partida sem o webhook:** recadastro, `FLEXSCAN_WEBHOOK_AUSENTE` e `RESOLVIDO` na conferência
    seguinte;
  - **conferência com erro:** `null`, sem alerta;
  - **`/flexscan/trafego`:** POST, CONSULTA 404, PUT, DELETE, WEBHOOK e FLX-PRECO na ordem, com
    `desde`; nenhum token ou segredo em nenhuma linha (o executor procura o token e o segredo da
    falsa no corpo inteiro);
  - **`/saude.flexscan`:** `ultimo_login`, e `fonte` com `compartilhada` `true` e `false`.
- Unidade: o buffer circular, o filtro das consultas e a máscara do `resumo`.
