# Spec de Implementação — serviço de ordens no PnT — v2 (desenho)

**Estado: desenho, não implementado** (exceto a parte de dados, já na [v1.3](SPEC-ORDENS-v1.3.md)
seção 3c, M1–M3). Onde a v2 não diz nada, vale a v1.3.

Data: 2026-10-14. Diretriz do responsável.

---

## 1. Diretriz

O serviço de ordens no PnT **não é só do Kalman**: será usado por qualquer módulo do GQ. O Kalman é
o primeiro cliente (`modulo = KALMAN`, o padrão de quem não diz qual é).

## 2. O que já está feito (v1.3 M1–M3)

- Coluna `modulo` (padrão `'KALMAN'`) em `kalmanordemoperacao`, `kalmanordem`, `kalmanordemevento`,
  `kalmanordempub`, `kalmanordemusuario` e `kalmanordemusuariohist` (migração
  `0004_modulo.sql`, só `ALTER`).
- Campo `modulo` **opcional** no `POST /ordens`, no `GET /ordens/{id}?modulo=` e no
  `GET /eventos?modulo=` (sem ele: KALMAN no pedido e na consulta; todos nos eventos). Todo evento
  publicado traz `modulo`.
- Idempotência por **(modulo, operacao_id, papel)**: o mesmo `operacao_id` em módulos diferentes são
  operações diferentes.
- ClOrdID provisório: o do KALMAN não muda (`KM-E-…`); os outros módulos levam o nome depois do
  `KM-` (`KM-PT-E-…`), para não colidir — o filtro do mass status pelo `KM-` continua valendo.

## 3. Desenho da v2 (a implementar, com roteiros antes do código)

| # | Regra | Hoje (v1.3) |
|---|---|---|
| V1 | **Posição e reduce-only por (modulo, uid, conta, símbolo).** A saída de um módulo nunca reduz a posição de outro, mesmo com o mesmo uid, conta e símbolo. | posição e reduce-only por operação; limite de posição global e por uid, somando todos os módulos |
| V2 | **Habilitação e limites por (modulo, uid)**: a CLI ganha `-modulo`; `kalmanordemusuario` passa a ter a chave (modulo, uid). | por uid (a coluna `modulo` existe, sempre `KALMAN`) |
| V3 | **Prefixo de ClOrdID por módulo** (ex.: `KM-` para o Kalman, outro para cada módulo) e **filtro do mass status por lista de prefixos** (os dos módulos configurados); ordem com prefixo de fora da lista = de outro sistema na mesma conta (como as do PT hoje). | prefixo único `KM-`, módulo dentro do ClOrdID |
| V4 | **Conciliação por módulo**: cada módulo com a sua tabela de operações (o Kalman: `dbecotools.kalmanoperacao`) e o seu resumo diário. | só o Kalman (Parte 10) |
| V5 | **Alertas com módulo**: cada alerta leva o módulo da operação; parada e retomada por módulo a decidir. | o alerta traz o `operacao_id` e o módulo da primeira operação do dia com esse id |
| V6 | **Nome do serviço a rever** (hoje `kalman-ordens`: binário, unidade systemd, usuário de sistema, schema `kalmanordens`, tabelas `kalmanordem*`). | `kalman-ordens` |

O roteiro que fixa a V1 (mesmo uid, conta e símbolo em dois módulos, com posições opostas; a saída
de um não reduz a do outro e o reduce-only de cada um olha só a sua posição) entra junto com a
implementação. O roteiro 54 já cobre o que vale hoje, por operação.

## 4. Pendências

1. Nome dos módulos e quem os cadastra (hoje: qualquer nome de 2 a 8 letras maiúsculas e dígitos).
2. Parada (BLOQUEAR/ZERAR) global ou por módulo.
3. Limites globais do serviço × limites por módulo.
