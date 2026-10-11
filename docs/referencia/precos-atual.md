# Contrato do `/precos/atual` (marketdata Go na .51)

Fonte: `tce-marketdata-go/internal/obs/obs.go`, trecho fornecido em 2026-10-10. O serviço
não tem acesso ao código do marketdata; este arquivo é a referência do contrato.

```go
type precoPayload struct {
    Ticker   string   `json:"ticker"`
    Preco    *float64 `json:"preco,omitempty"`
    Bid      *float64 `json:"bid,omitempty"`
    Ask      *float64 `json:"ask,omitempty"`
    Status84 int      `json:"status84"`
    TS       string   `json:"ts"`        // RFC3339 com fuso
    IdadeSeg float64  `json:"idade_seg"` // agora - ts, 1 casa decimal
}
```

Resposta: `map[conceito]precoPayload`.

```
GET /precos/atual?ativo=WINFUT
200 {"WINFUT": {"ticker":"WINZ26","preco":170370.0,"bid":170365.0,"ask":170370.0,
                "status84":0,"ts":"2026-10-13T10:15:02-03:00","idade_seg":0.4}}
```

URL base em configuração (`PRECOS_URL`; na .51: `http://172.16.110.51:9090`).

## Comportamento (confirmado pelos testes do marketdata)

- `preco`, `bid` e `ask` são **omitidos** quando não há valor de hoje (nunca herdados).
- No leilão de abertura vêm `bid`, `ask` e `status84 != 0`, **sem `preco`**.
- Sem nenhum dado de hoje o ativo some: corpo `{}` com 200 (fixture
  `cenarios/referencia/precos-atual-vazio.json`, capturado num sábado).
- `404` = motor desabilitado; `400` = ativo desconhecido. Os dois = sem preço.
- `ts` / `idade_seg` = carimbo do campo **mais fresco** entre preco/bid/ask, não de cada um.
  Um bid velho pode vir com idade baixa se o preço acabou de mudar.

## Como o serviço usa (SPEC v1.1, Parte B)

| Situação | Entrada | Saída |
|---|---|---|
| preco, bid, ask presentes, `status84 == 0`, `idade_seg <= 3`, `bid < ask`, \|meio − preco\| ≤ 150 | permitida | normal |
| `{}`, 404, 400, erro, timeout | `SEM_PRECO` | sem preço: alerta `SAIDA_SEM_PRECO` e nova tentativa a cada `TIMEOUT_SAIDA_S` |
| falta preco, bid ou ask | `SEM_PRECO` (ou `LEILAO` se `status84 != 0`) | sem bid/ask: limite = preco ∓ `COLAR_SAIDA_PTS`; alerta `SAIDA_PRECO_DEGRADADO` |
| `status84 != 0` | `LEILAO` | usa o que houver |
| `idade_seg > 3` | `PRECO_VELHO` | usa o que houver; alerta `SAIDA_PRECO_DEGRADADO` |
| `bid >= ask` | `BOOK_INVALIDO` | usa o que houver |
| \|meio − preco\| > 150 | `BOOK_DESCOLADO` | usa o que houver |
| `ticker` ≠ símbolo da posição aberta (v1.2 S2) | entrada não é afetada (vai no `ticker`, o contrato vigente; S1) + alerta crítico `SIMBOLO_DIVERGENTE_COM_POSICAO` | limite sobre o último `preco` do símbolo certo, colar máximo, sem repreço; com mais de 60 s não envia (SPEC v1.1 B5) |

Ordem das verificações na entrada: resposta vazia/erro → ticker → status84 → campos ausentes →
idade → bid < ask → descolamento. O primeiro que falhar dá o código.

Preços vêm em float: o serviço converte para pontos inteiros e arredonda ao tick de 5 pela
regra 3.2 (sentido que torna a ordem mais agressiva; stop limite mais conservador).
