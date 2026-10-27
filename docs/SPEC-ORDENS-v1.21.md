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
  `FLEXSCAN_WEBHOOK_CONFERIR_MIN` (10). Procura o nosso URL com o trigger `order.all`. **Chave nova
  `FLEXSCAN_WEBHOOK_URL`:** a URL pública exatamente como cadastrada na Flexscan, com o segredo. O
  serviço só conhece o endereço de escuta (`FLEXSCAN_WEBHOOK_ENDERECO`), não o host público. Ela nunca
  vai para log, tráfego ou alerta. Só o `webhook_cadastro.go` fala com o recurso de webhooks, só com
  GET e POST (teste de arquitetura).
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
da fonte do PnT (o `fonte` do topo) e mais `url` e `compartilhada`. Com a fonte única (a mesma URL
do PnT, sem `PRECOS_URL_FLEXSCAN` próprio, ou `PNT_DESLIGADO=1` da v1.19.1), `compartilhada` vem
`true` e os valores são os do topo. **É o caso da .53 desde 27/10 15:32** (`PNT_DESLIGADO=1`): hoje o
`flexscan.simbolo` e o `flexscan.fonte` vêm nulos, com a 9090 viva (o `simbolo` do topo é o dela).

Junto (v1.19.1): a linha `kalman-ordens no ar` do journal mostra `fix=desligado` com
`PNT_DESLIGADO=1` (era o `FIX_HOST:FIX_PORTA` ignorado).

## 3b. A espera depois do 202 (27/10, "Order not found")

`FLEXSCAN_PRIMEIRA_CONSULTA_MS` (200): depois do 202 do POST, a primeira consulta (ativa) sai aos
200 ms. Depois vêm +300, +600, +1.200 e +2.400 ms sobre ela, como antes. A consulta **periódica**
também passa a começar depois da janela. A ordem nova nascia com ela vencida, e o primeiro `Tick`
(≤ 100 ms) a consultava de novo: era o segundo 404 da 991027002 (09:45:55.798 e .889). Com 0, volta
ao de antes (os roteiros antigos rodam assim).

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
  "saude": {
    "logado": true, "token_expira": "2026-10-27T19:20:21-03:00", "receptor": true,
    "webhooks_recebidos": 7, "ultimo_webhook": "2026-10-27T09:45:59-03:00", "ultimo_erro": "",
    "ordens_acompanhadas": 6, "ordens_vivas": 0,
    "webhook_cadastrado": true, "webhook_conferido_em": "2026-10-27T09:52:40-03:00",
    "webhook_recadastrado_em": null,
    "ultimo_login": {"em": "2026-10-27T09:20:21.004-03:00", "motivo": "partida", "resultado": "OK"},
    "boot": "2026-10-27T09:20:20.512-03:00", "msgs_min": 12, "consultas_min": 40,
    "latencia_post_p50_ms": 58, "latencia_post_max_ms": 140
  }
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

`saude` traz os campos **da ponte** do `/saude.flexscan` (4.2): `logado`, `token_expira`,
`receptor`, `webhooks_recebidos`, `ultimo_webhook`, `ultimo_erro`, `ordens_acompanhadas`,
`ordens_vivas`, `webhook_cadastrado`, `webhook_conferido_em`, `webhook_recadastrado_em` e
`ultimo_login`. **Não traz `simbolo`, `idade_preco_ms` nem `fonte`**: esses ficam só no
`/saude.flexscan`, porque o tráfego é lido da memória da ponte, sem a fila do motor (correção de
27/10: o exemplo anterior dizia "os campos de /saude.flexscan"). Traz também:
- `boot`: a hora da partida do processo;
- `msgs_min`: linhas no último minuto;
- `consultas_min`: consultas no último minuto, inclusive as que não entram no buffer;
- `latencia_post_p50_ms` / `latencia_post_max_ms`: a duração dos POSTs no buffer (`null` sem POST).

### 4.2 `GET /saude` → `flexscan` (os campos novos)

O bloco inteiro, como o serviço devolve (fonte única, `PNT_DESLIGADO=1`):

```json
{
  "logado": true, "token_expira": "2026-10-27T19:20:21-03:00", "receptor": true,
  "webhooks_recebidos": 7, "ultimo_webhook": "2026-10-27T09:45:59-03:00", "ultimo_erro": "",
  "ordens_acompanhadas": 6, "ordens_vivas": 0,
  "webhook_cadastrado": true, "webhook_conferido_em": "2026-10-27T09:52:40-03:00",
  "webhook_recadastrado_em": null,
  "ultimo_login": {"em": "2026-10-27T09:20:21.004-03:00", "motivo": "partida", "resultado": "OK"},
  "simbolo": "WINZ26", "idade_preco_ms": 412,
  "fonte": {"degradada": false, "ruim_desde": null, "motivo": "", "janela": "09:00-14:30",
            "dia_de_pregao": true, "travado": false, "travado_desde": null, "travado_motivo": "",
            "url": "http://172.16.110.53:9090", "compartilhada": true}
}
```

`fonte.sem_pregao` (texto) só aparece quando não é dia de pregão.

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

- Roteiro **115** (`115-flexscan-ao-vivo.fix`, Flexscan falsa com `GET`/`POST /webhooks` e o 404
  logo depois do POST). Mutações conferidas: com a espera em 0, aparece a CONSULTA 404 antes dos
  200 ms; a URL no resumo do cadastro reprova a checagem de segredo.
  - **partida sem o webhook:** recadastro, `FLEXSCAN_WEBHOOK_AUSENTE` e `RESOLVIDO` (a conferência
    logo depois do POST acha a URL). Um cadastro de outro sistema fica intacto;
  - **conferência com erro:** `null`, sem alerta;
  - **recadastro que falha:** `false` e o crítico; na conferência seguinte, recadastrado e resolvido;
  - **`/flexscan/trafego`:**
    - nenhuma consulta antes dos 200 ms, a CONSULTA 404 aos 200 ms e a 200 depois;
    - POST (com a operação, o módulo, o uid, o id e a duração), WEBHOOK, PUT, LOGIN do 401, FLX-PRECO,
      DELETE e WEBHOOK recusado (404, só o motivo);
    - `desde`/`limite` inválidos dão 400;
    - nenhum token, a senha, o segredo ou a URL do webhook no corpo (o executor procura a cada
      `flx_trafego`);
  - **`/saude.flexscan`:** `ultimo_login`, e `fonte` com `compartilhada` `true` e `false`; com
    `PNT_DESLIGADO=1` (roteiro 114), `flexscan.simbolo` e `flexscan.fonte` presentes e iguais aos do
    topo.
- **Contrato × serviço** (27/10): o verbo de roteiro `contrato ARQUIVO SECAO [caminho]` compara a
  forma da última resposta HTTP com o primeiro bloco ```json da seção: as mesmas chaves nos dois
  sentidos, cada item das listas e o tipo JSON quando os dois lados não são null. O 115 o usa no
  `/flexscan/trafego` (§4.1) e no `/saude` → `flexscan` (§4.2). Com o exemplo antigo (o `"..."` no
  `saude`), ele reprovava.
- Unidade: `TestTrafegoBufferDesdeLimite` (buffer circular, `desde`, `limite`, resumo ASCII de até
  200). `TestServirAtualizaDoInstalado`: `flexscan.fonte` com `compartilhada` false e a `url` da fonte
  própria. `TestSemChamadasProibidas`: `/webhooks` só no `webhook_cadastro.go`, sem DELETE/PUT/remove.
