# SPEC kalman-ordens v1.20 — `stop_origem` nulo = origem desconhecida (27/10/2026)

Base: v1.19 (instalada na .53, `6b9a6434796f`, 27/10 13:44). Origem: depois da instalação, o
`GET /ordens/234335` trouxe `stop_origem INICIAL`, mas a 234335 saiu por uma stop movida pelo
TRSP. Na v1.19, a origem vazia (operação anterior, sem a coluna preenchida) valia `INICIAL`, e o
Kalman exibiria `STOP` numa saída de trailing. Decisão do usuário: o que não se sabe sai nulo.

## Regras

1. **`stop_origem` nulo = desconhecido.** Isso vale para a operação anterior à v1.19 e para a operação
   sem proteção aceita ainda. Fica nulo no `GET /ordens/{id}`, no `GET /operacoes`, no histórico e
   nos eventos (`PROTEGIDA`, `PROTECAO_AJUSTADA`, `PROTECAO_DISPARADA`, e `SAIDA_EXECUTADA`/`ZERADA`
   com `origem STOP_SERVIDOR`): o campo vem, com `null`.
2. **`INICIAL` só para a proteção criada a partir da v1.19** com o `stop_pts` da entrada (a 1014 que
   este processo enviou e a corretora aceitou).
3. **Ajuste sem origem gravada** (pedido antes da v1.19) também fica desconhecido. Antes, saía como
   `GESTOR`. `GESTOR` fica só para o ajuste pedido sem o campo `origem` a partir da v1.19.
4. **Restart:** a 1014 de antes do restart mantém a origem gravada na operação. O replace confirmado
   depois do restart, sem o pedido em memória, leva a origem do gatilho ajustado se for ele. Se não
   for, fica desconhecido. Na v1.19, ficava a origem anterior ou a calculada.

Sem migração: as colunas da 0013 já aceitam `NULL`.

## Contrato para o Kalman (completa a §6 da v1.19)

| `stop_origem` no `ZERADA` com `origem STOP_SERVIDOR` | Motivo exibido |
|---|---|
| `null` (desconhecido: operação anterior à v1.19) | `STOP_SERVIDOR` (como hoje) |
| `INICIAL` | `STOP` |
| `TRSP` | `TRSP_SERVIDOR` |
| `STOP_MANUAL` | `STOP_MANUAL_SERVIDOR` |
| `GESTOR` (ajuste sem `origem`) | `STOP_SERVIDOR` |

## Testes

- `motor.TestStopOrigemDesconhecidaENula`: no `GET /operacoes`, a operação sem a origem gravada traz
  `stop_origem` nulo, e a com `TRSP` traz `TRSP`. O ajuste sem origem gravada fica desconhecido no
  cálculo.
- Os roteiros 112 e 113 seguem iguais: as operações deles nascem na v1.19 (`INICIAL`, `TRSP`,
  `GESTOR`).
