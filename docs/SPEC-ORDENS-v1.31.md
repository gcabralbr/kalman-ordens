# SPEC kalman-ordens v1.31 — a boleta do admin (01/11/2026)

Base: v1.30. Ferramenta 1 da SPEC-CADASTRO-v1 (§2), com as respostas de 01/11 (P4: a boleta exige um
uid habilitado no ambiente da ordem, com as travas e os limites dele; P5: a tela de admin fica para
depois de terça). O teste REAL de terça, 03/11, vai por ela.

## 1. O que é

Uma ENTRADA mandada pelo admin, sem o Kalman, **pelo mesmo `POST /ordens`**, com `modulo: "BOLETA"`.
Tudo o que vale para uma operação do Kalman vale para ela:
- as travas e os limites do uid (`REAL_UIDS`, a habilitação do ambiente, a coluna do GQ, `REAL_CONTAS`,
  a trava simulador × real, `CORRETORA_SEM_CREDENCIAL`, `CONTA_FORA_DA_CORRETORA`, os limites);
- a proteção, o ajuste do stop, a conciliação, o restart, a parada e a zeragem;
- o histórico, o ao vivo, o `/saude` e o tráfego (a operação aparece com `modulo: "BOLETA"`).

O contrato é o vigente (o do preço), como no Kalman. O ClOrdID/`name` é `KM-BOLETA-E-<id>-<n>`.

## 2. Os campos novos do `POST /ordens`

| campo | onde | o quê |
|---|---|---|
| `por` | obrigatório na boleta | o uid do admin; fica no pedido gravado (`pedidos[]` do histórico) e no alerta |
| `conta` | opcional, só na ENTRADA da boleta | a conta que o admin espera |
| `corretora` | opcional, só na ENTRADA da boleta | a corretora que o admin espera: o nome (`BTG`, sem diferença de maiúsculas) ou o código (`85`) |
| `preco` | opcional, só na ENTRADA da boleta | a entrada limitada nesse preço (pontos, múltiplo do tick); sem ele, a agressiva de sempre (ask/bid ± `COLAR_ENTRADA_PTS`) |

- **Fora do módulo `BOLETA`**, qualquer um dos três dá `CAMPO_INVALIDO` ("por, conta e corretora so na
  boleta"). O contrato do Kalman não muda.
- **Boleta sem `por`:** `CAMPO_INVALIDO`.
- **`conta` ou `corretora` diferentes da coluna do GQ do uid** naquele ambiente: recusa nova
  `BOLETA_DESTINO_DIVERGENTE`, com as duas no motivo. A conferência é feita logo depois da coluna
  resolvida, antes da sessão e dos limites.
- A SAIDA da boleta usa o destino gravado (não aceita `conta`, `corretora` nem `preco`).
- **A entrada da boleta não tem o timeout da entrada** (`TIMEOUT_ENTRADA_S`): ela fica viva até
  executar ou até o `cancelar` (pedido de 01/11: "enviar longe do preço, estado, cancelar"). Isso vale
  para toda boleta, também a agressiva, e continua valendo depois de um restart, porque a regra é pelo
  módulo gravado.

## 3. Alerta

`BOLETA_REAL` (informativo): toda boleta aceita no REAL. O texto traz o admin, o lado, a quantidade, o
contrato, o uid, a corretora, a conta e o motivo.

## 4. CLI

Pelo HTTP local do serviço, como o `operacao confirmar`:

```
kalman-ordens boleta destino  -uid 5725 -ambiente REAL [-conta 4582351] [-corretora BTG]
kalman-ordens boleta enviar   -uid 5725 -ambiente REAL -lado C -qtd 1 -stop 300 -por 5725 -motivo "teste REAL" [-preco P] [-conta 4582351] [-corretora BTG] [-op N] [-sim]
kalman-ordens boleta estado   -op N [-historico]
kalman-ordens boleta cancelar -op N -por 5725 -motivo X
kalman-ordens boleta encerrar -op N -por 5725 -motivo X
```

- **`destino`:** o `GET /usuarios/{uid}/destino?ambiente=`. Termina com erro se o uid não pode enviar,
  ou se a conta ou a corretora esperadas não batem.
- **`enviar`:**
  1. faz o `destino`;
  2. no REAL, mostra o resumo e espera `sim` (o `-sim` pula a pergunta);
  3. manda o `POST /ordens`.

  O `operacao_id` padrão é `MMDDHHMMSS`, no fuso do pregão.
- **`estado`:** o `GET /ordens/{id}?modulo=BOLETA`; com `-historico`, também a linha do tempo.
- **`cancelar`:** o `cancelar-entrada` de sempre (só com a entrada pendente e sem posição).
- **`encerrar`:** o `encerrar` de sempre (cancela a stop e sai).

## 5. Flexscan falsa

A consulta (`GET /orders/{id}`) da falsa passa a trazer o `text` da ordem. Antes vinha sempre vazio,
e a rejeição pela consulta saía sem o motivo ("[FLX] "). A ponte já lia o campo: com a Flexscan real,
o motivo ("Conta nao contratada") aparece no `RECUSA_FLEXSCAN` e no `motivo_rejeicao` da ordem.

## 6. Testes

- **Roteiro 127** (REAL, BTG, uid 5725, conta 4582351):
  - os campos da boleta;
  - a trava do uid;
  - a conta e a corretora divergentes;
  - a boleta aceita com o `BOLETA_REAL`;
  - a limitada longe do preço: o preço fora do tick é recusado; ela segue viva depois de 30 s, sai no
    `cancelar` e não é aceita fora da boleta;
  - a rejeição "Conta nao contratada" depois do 202;
  - o cancelar da entrada pendente;
  - o restart com a boleta protegida;
  - o encerrar.
- **`TestBoletaCLI`:**
  - a confirmação;
  - a conta divergente, que para antes de perguntar;
  - o uid sem permissão;
  - o corpo do `POST /ordens`;
  - o `cancelar` com `?modulo=BOLETA`.
