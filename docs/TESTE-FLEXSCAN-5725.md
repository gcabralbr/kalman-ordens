# Teste manual da ponte Flexscan pelo uid 5725 (módulo TESTE, .53)

27/10/2026. Para quando o motor da principal não deve gerar sinal: o caminho completo pela ponte com
curl no `127.0.0.1:9092`, sem o Kalman. Binário instalado: `4e0cb1924fcf` (v1.18).

Estado conferido às 09:32: `/saude` com `posicao` 0, `ordens_vivas` null, `flexscan.logado` true,
`fonte.travado` false, `webhooks_recebidos` 0. 5725: `habilitado` true, `conectores ["FLEXSCAN"]`,
`posicao_max` 0 e `qtd_max_ordem` 0 (sem limite próprio; valem os globais). Conta: `S-1117150`
(simulador, D0); preço: a 9090 (WINZ26, 179.080 às 09:32, `status84` 0).

`operacao_id` na faixa **991027001+** (99 + MMDD + sequência). O módulo TESTE não tem operação em
nenhum dia de 16/10 a 26/10 e os ids do KALMAN estão na casa de 234.000: sem colisão. Um id usado
não serve de novo, nem em outro dia (`DUPLICADA`).

## Pontos de atenção do formato

- O JSON é **estrito**: campo desconhecido → 400 `CAMPO_INVALIDO`.
- O `modulo` vai **no corpo** do `POST /ordens` e do `POST /operacoes/{id}/stop`, e **na query**
  (`?modulo=TESTE`) do `encerrar`/`cancelar-entrada` e dos GETs. Sem ele, o GET procura no KALMAN
  e o encerrar responde `OPERACAO_INEXISTENTE`. No corpo do encerrar, o `modulo` dá
  `CAMPO_INVALIDO` (o corpo aceita só `motivo` e `por`).
- `por` = uid do admin (inteiro > 0), obrigatório no encerrar.

```
B=127.0.0.1:9092; POR=<uid do admin>; ID=991027001
entrada() { curl -s -X POST $B/ordens -H 'Content-Type: application/json' -d "{\"modulo\":\"TESTE\",\"operacao_id\":$1,\"uid\":5725,\"papel\":\"ENTRADA\",\"lado\":\"$2\",\"qtd\":1,\"stop_pts\":500,\"motivo\":\"TESTE FLEXSCAN\"}"; echo; }
stop()    { curl -s -X POST $B/operacoes/$1/stop -H 'Content-Type: application/json' -d "{\"modulo\":\"TESTE\",\"uid\":5725,$2}"; echo; }
encerrar(){ curl -s -X POST "$B/operacoes/$1/encerrar?modulo=TESTE" -H 'Content-Type: application/json' -d "{\"motivo\":\"teste flexscan 5725\",\"por\":$POR}"; echo; }
ver()     { curl -s "$B/ordens/$1?modulo=TESTE" | python3 -m json.tool; }
saude()   { curl -s $B/saude | python3 -c "import json,sys; d=json.load(sys.stdin); print({k: d[k] for k in ('posicao','posicao_por_conector','ordens_vivas','parada','divergente','alertas_criticos')}); print('flexscan', d['flexscan']); print('fonte', d['fonte'])"; }
flx()     { curl -s "$B/operacoes/$1/historico?modulo=TESTE&cru=1" | python3 -c "
import json,sys
d=json.load(sys.stdin)
for m in d['mensagens']:
    print(m['em'][11:23], m['direcao'], m['msgtype'], m['clordid'], m['orderid'], 'st='+str(m['ordstatus']), 'cum='+str(m['cumqty']), 'px='+str(m['lastpx']), (m.get('texto') or '')[:60], (m.get('fix_raw') or '')[:200])
for e in d['eventos']: print('EV', e['em'][11:23], e['tipo'], json.dumps(e.get('dados'))[:200])"; }
```

Em outro terminal, o journal:

```
journalctl -u kalman-ordens -f | grep -E 'TESTE|99102700|flexscan|ALERTA'
```

## T0. Antes (e a hora)

- Entradas só entre 09:00 e `JANELA_FIM` (14:00 pelo padrão; conferir o `service.env`). A
  `HORA_ZERAGEM` (14:30) com **qualquer** posição aberta ativa a parada **ZERAR global** (todos os
  módulos): o teste termina bem antes.
- `saude`: `posicao` 0, `parada` null, `divergente` false, `flexscan.logado` true, `fonte.degradada`
  e `fonte.travado` false.
- `curl -s "$B/operacoes?dia=$(date +%F)&modulo=TESTE"` → `[]` (ou só ids anteriores encerrados).

## T1. ENTRADA (compra de 1 WINZ26)

```
entrada $ID C
```

Corpo enviado (o `ts_decisao` é opcional):
`{"modulo":"TESTE","operacao_id":991027001,"uid":5725,"papel":"ENTRADA","lado":"C","qtd":1,"stop_pts":500,"motivo":"TESTE FLEXSCAN"}`

Resposta esperada: `{"aceito":true,"operacao_id":991027001,"clordid":"KM-TESTE-E-991027001-1","preco":<ask da 9090 + 15>}`.

Recusas possíveis e o que significam: `SEM_SESSAO` (conector Flexscan sem token), `LEILAO`,
`MERCADO_TRAVADO`, `PRECO_VELHO`/`SEM_PRECO` (9090), `BOOK_DESCOLADO`, `FORA_JANELA`,
`INTERVALO_MINIMO_UID` (outra entrada do 5725 no mesmo sentido há < 30 s). Recusa não cria
operação: o mesmo id pode ser reenviado.

Conferir:
1. `flx $ID`: `ENV FLX-POST` da limite de compra (name `KM-TESTE-E-...`), `REC FLX-200` com o `id`
   da Flexscan; se a Flexscan ajustou ao túnel, `FLX-PRECO` com o efetivo. Depois o fill
   (ExecutionReport traduzido, `cum=1`). Eventos: `ENTRADA_ENVIADA`, `ENTRADA_EXECUTADA`.
2. `saude`: `posicao` 1, `posicao_por_conector.FLEXSCAN` 1; **`flexscan.webhooks_recebidos` subiu
   e `ultimo_webhook` com a hora**. É o primeiro webhook do dia: se não subir, a consulta periódica
   cobre e sai o aviso `FLEXSCAN_WEBHOOK_ATRASADO` (anotar).
3. Sem fill em `TIMEOUT_ENTRADA_S` (3 s): o serviço cancela a entrada sozinho (`FLX-DELETE`) e a
   operação encerra sem entrada (`ENTRADA_ENCERRADA`). Usar o id seguinte.

## T2. A proteção criada pelo serviço (stop-limite na B3)

```
ver $ID
flx $ID
```

Conferir:
- estado `PROTEGIDA`; evento `PROTEGIDA`;
- a ordem de proteção `KM-TESTE-P-991027001-1`: venda, stop-limite, **gatilho = médio − 500** (ao
  tick, para cima) e **limite = gatilho − 100** (`FOLGA_STOP_PTS`); no histórico, `ENV FLX-POST` e
  `REC FLX-200`, depois o retrato com a ordem aceita (estado 0, `isInMarket`);
- na plataforma da Flexscan (conta `S-1117150`), a mesma ordem com o mesmo gatilho e limite;
- `saude`: `ordens_vivas` com a proteção (`conector` FLEXSCAN), sem `SEM_PROTECAO` nos
  `alertas_criticos`.

## T3. Ajuste do gatilho (como o TRSP)

O Kalman manda `preco_stop` (o nível do trailing); `stop_pts` conta a partir do médio. Um dos dois,
nunca os dois. Subir o stop de médio − 500 para médio − 300:

```
stop $ID '"stop_pts":300'
# ou, em preço:  stop $ID '"preco_stop":<médio − 300>'
```

Corpo: `{"modulo":"TESTE","uid":5725,"stop_pts":300}`.

Resposta: `{"aceito":true,"situacao":"ENVIADO","preco_stop":<novo>,"preco_limite":<novo − 100>,"preco_stop_pnt":<antigo>,"preco_limite_pnt":<antigo − 100>,...}`.
(`preco_stop_pnt` é o que está na corretora, mesmo na Flexscan.)

- `IGNORADO`: menos de 25 pts do que está lá (`AJUSTE_DEGRAU_MIN_PTS`); nada sai.
- `AGUARDANDO`: há um pedido da proteção sem resposta; o último ajuste sai quando ele responder.
- `STOP_ALEM_DO_PRECO`: o gatilho não está abaixo do bid; quem sai, nesse caso, é o gestor.

Conferir: `flx $ID` com `ENV FLX-PUT` da `KM-TESTE-P-...` e a confirmação (consulta com
`orderStatus 5` e `transactTime` posterior, ou retrato "Editada"); evento `PROTECAO_AJUSTADA`;
`ver $ID` com o gatilho novo; a mesma ordem na plataforma com o gatilho novo. Recusa da Flexscan, ou
sem confirmação em `FLEXSCAN_CONFIRMA_S`: `PROTECAO_AJUSTE_RECUSADO` e o stop anterior fica (anotar o
texto).

Opcional: um segundo ajuste (`"stop_pts":200`) para ver o PUT repetido.

## T4. Saída pelo encerrar (DELETE da stop + limite)

```
encerrar $ID
```

Corpo: `{"motivo":"teste flexscan 5725","por":<POR>}`, com `?modulo=TESTE` na URL.

Resposta: `{"aceito":true,"codigo":"ENCERRANDO","acao":"ENCERRAR","posicao":1,...}`.

Conferir, nesta ordem (`flx $ID` e o journal):
1. evento `ADMIN_ACAO` (acao ENCERRAR, por, motivo);
2. `ENV FLX-DELETE` da `KM-TESTE-P-...` e a confirmação (estado 4); a operação passa por
   `CANCELANDO_PROTECAO`;
3. só depois do cancelamento confirmado: `ENV FLX-POST` da limite de venda `KM-TESTE-S-...` a
   **bid − 50** (`COLAR_SAIDA_PTS`), evento `SAIDA_ENVIADA`;
4. fill: `SAIDA_EXECUTADA`, `ZERADA` com `origem ADMIN`;
5. `saude`: `posicao` 0, `ordens_vivas` null, nada novo em `alertas_criticos`; na plataforma, a conta
   sem ordem viva e sem posição.

Sem fill da limite em `TIMEOUT_SAIDA_S` (2 s): repreço (cancel + nova, mais agressiva), até
`MAX_REPRECO_SAIDA` (3); depois `SAIDA_ESGOTOU` e continua reapregoando.

Variante (o caminho do Kalman em vez do admin), numa operação nova:
`curl -s -X POST $B/ordens -H 'Content-Type: application/json' -d '{"modulo":"TESTE","operacao_id":991027002,"uid":5725,"papel":"SAIDA","lado":"V","qtd":1,"motivo":"MANUAL"}'`
— o mesmo fluxo, com `origem GESTOR`.

## Fim

```
curl -s "$B/operacoes?dia=$(date +%F)&modulo=TESTE" | python3 -m json.tool
saude
```

Todas as operações TESTE em estado final, `posicao` 0. Anotar: tempos POST → fill da entrada, POST →
aceite da proteção, PUT → confirmação, DELETE → confirmação → POST da saída → fill, e se os webhooks
chegaram (`webhooks_recebidos` antes e depois).

## Se algo travar: zerar à mão

Na ordem, do menos ao mais invasivo:

1. **Entrada sem execução, operação em `ENTRANDO`:**
   `curl -s -X POST "$B/operacoes/$ID/cancelar-entrada?modulo=TESTE" -H 'Content-Type: application/json' -d "{\"motivo\":\"teste travou\",\"por\":$POR}"`
   (`CANCELANDO` ou `JA_CANCELANDO`; com execução parcial ele recusa `COM_POSICAO`: usar o encerrar).
2. **Posição aberta:** `encerrar $ID`. `OPERACAO_EM_SAIDA` = a saída já está em curso (não repetir;
   acompanhar com `flx`). `SEM_PROTECAO` no ajuste depois do encerrar é esperado.
3. **503 `RESULTADO_DESCONHECIDO`:** o motor segue com o pedido; consultar `ver $ID` antes de repetir.
   Repetir o mesmo corpo é seguro (idempotente por `(modulo, operacao_id, papel)`).
4. **Ponte sem token / sem resposta da Flexscan** (`flexscan.logado` false, `ultimo_erro`): o motor
   marca `SEM_CONFIRMACAO` e pede o mass status à ponte, que consulta a Flexscan. Restart do
   kalman-ordens só pelo usuário (sudo); a partida concilia as ordens `KM-` da conta. Cada restart é
   um login no `flx_gammaquant` (no máximo 5 em 10 min, `FLEXSCAN_LOGIN_MAX`).
5. **Último recurso, pela plataforma da Flexscan** (conta `S-1117150`): cancelar as `KM-TESTE-*`
   vivas e zerar a posição lá. O serviço vê a diferença na conciliação → `DIVERGENTE`, que é
   **global** (barra as entradas do Kalman também) até `POST /retomar`
   (`{"origem":"admin","motivo":"...","por":<POR>}`) com a conta conferida.

Não usar:
- `POST /parada` com `ZERAR`: zera **todas** as operações, de todos os módulos e uids;
- `scripts/flexscan-cb.py limpar`: só apaga ordens `CB-` (não as `KM-`), e o `login` dele é mais um
  login da mesma credencial (o bloqueio de 23/10 veio de outro uso dela).

## O módulo TESTE e o Kalman

Isolado:
- o Kalman lê `GET /eventos?modulo=KALMAN`, e o evento é gravado com o módulo da operação (`TESTE`):
  nenhum `ENTRADA_*`, `PROTEGIDA`, `ZERADA` etc. do teste chega a ele. Alerta da operação também
  leva o módulo dela (o id 991027001+ não existe no KALMAN, então não há a ambiguidade de
  `publicarAlerta`);
- `GET /ordens`, `/operacoes`, `/operacoes/{id}/historico`, `encerrar` do Kalman usam
  `?modulo=KALMAN`; a idempotência é por `(modulo, operacao_id, papel)`;
- limites **por uid** (posição/qtd da habilitação, `LIMITE_ENTRADAS_MIN_UID`,
  `LIMITE_INTERVALO_UID_S`) contam só o uid da operação: os outros uids não veem o teste. O 5725
  conta as entradas dele em qualquer módulo (uma entrada KALMAN do 5725 no mesmo sentido em < 30 s
  seria recusada), mas o REAL do 5725 está desligado no Kalman;
- a habilitação não muda (já está `habilitado` true, FLEXSCAN): nenhum `ADMIN_ACAO` global.

Compartilhado (o que o teste pode, sim, afetar):
- **limites globais** somam todos os módulos e conectores: `LIMITE_POSICAO_MAX` (1 de 200),
  entradas por minuto e por dia, mensagens por minuto e a **perda do dia** (`LIMITE_PERDA_DIA_PTS`
  1500, global: uma perda do teste conta para a parada automática `BLOQUEAR`);
- **`/saude`**: `posicao`, `posicao_por_conector`, `ordens_vivas` e `alertas_criticos` mostram o
  teste. O Kalman lê o `/saude` para o topo da tela: um crítico da operação TESTE (ex.:
  `SEM_PROTECAO`) aparece lá como global enquanto ativo;
- **Telegram**: os alertas vão pelo `ALERTA_WEBHOOK_URL` sem filtro de módulo;
- **`DIVERGENTE`, parada e a zeragem das 14:30** valem para o serviço inteiro;
- o desligamento do pnt-md-hml às 14:35 olha só o PnT: o teste não o segura.
