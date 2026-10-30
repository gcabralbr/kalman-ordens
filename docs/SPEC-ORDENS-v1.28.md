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
   janela (ou na conferência/renovação da abertura, o que vier primeiro). Uma partida fora da
   janela também não loga.
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
