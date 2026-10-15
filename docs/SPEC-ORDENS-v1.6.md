# Spec de Implementação — `kalman-ordens` — v1.6

Revisão da [SPEC-ORDENS-v1.5.md](SPEC-ORDENS-v1.5.md). Lista **só o que mudou**; onde a v1.6 não
diz nada, vale a v1.5.

Tema: **decisões do responsável sobre a v1.5** (15/10): retenção aprovada, cópia do log FIX para
fora da máquina, gravação dos pedidos HTTP.

Data: 2026-10-15.

---

## 1. Pedidos HTTP (P)

| # | Regra | Teste |
|---|---|---|
| P1 | **Todo `POST /ordens` e `POST /operacoes/{id}/stop` é gravado** em `kalmanordempedido` (migração `0007_pedidos.sql`): o corpo **cru** (como chegou), a resposta dada, o status HTTP, aceito, código, rota (`ORDENS`/`STOP`), papel, módulo, `operacao_id`, uid, dia e `em`. Inclui o aceito, o repetido (idempotência), o recusado (que não cria operação), o corpo ilegível (400: o que o decodificador leu antes do erro identifica o pedido) e o 503 (o motor seguiu com o pedido; o resultado aparece no reenvio e na operação). Gravado pela API, fora da fila do motor; falha ao gravar só vai ao log. | roteiro 62; `TestContratoHistorico` |
| P2 | **`GET /operacoes/{id}/historico`** ganha `pedidos`: `[{em, rota, papel, http_status, aceito, codigo, corpo, resposta}]` (corpo e resposta como JSON quando são JSON, senão o texto). | roteiro 62 |
| P3 | **`GET /operacoes?dia=`**: o pedido **recusado que não virou operação** (limite, preço, habilitação, conta, corpo ilegível...) entra na lista com `estado: "RECUSADA"`, os campos da lista (`lado` e `qtd` do corpo; preços, `pnl_pts`, `zerada_em` nulos; `alertas_n` 0; `criada_em` = o horário do pedido) e mais `rota`, `papel`, `http_status`, `codigo`, `motivo` e `corpo`. Recusa de uma operação que existe (ex.: `LADO_INCOMPATIVEL` na saída) fica no histórico dela. Ordem da lista: por horário. Filtros `uid` e `modulo` valem para as recusas. | roteiro 62 |

A CLI `historico` traz o mesmo (é a mesma função).

## 2. Retenção (E) — aprovada

| # | Regra | Teste / arquivo |
|---|---|---|
| E1 | **Banco: 12 meses** (`EXPURGO_MESES`, mínimo 3; sobe se o BTG exigir) para `kalmanordemevento`, `kalmanordempub`, `kalmanordemsessao` e `kalmanordempedido`. O resumo (`kalmanordemoperacao`, `kalmanordem`) não tem expurgo. `kalman-ordens expurgar [-meses N] [-simular]`: apaga em lotes de 5000 as linhas com `em` antes do início do dia de hoje − N meses (fuso do pregão). Usuário de banco **só do expurgo** (`EXPURGO_DB_DSN`, `SELECT`/`DELETE` só nessas quatro tabelas e `SELECT` na de migração); nunca o `DB_DSN` do serviço, que continua sem `DELETE`. Timer mensal (dia 1, 20:00). | `TestExpurgarCLI`; `deploy/kalman-ordens-expurgo.{service,timer}`, `deploy/expurgo.env.example` |
| E2 | **Log FIX em arquivo: 90 dias compactado** — `logrotate` diário, `copytruncate` (o QuickFIX/Go mantém o arquivo aberto em append), `compress`, `dateext`, `rotate 90`/`maxage 90` (o prazo muda aí). Roda à meia-noite pelo `logrotate.timer`, fora da sessão FIX. Com SELinux em Enforcing, o diretório do log precisa do rótulo `var_log_t` (o logrotate não mexe em `var_lib_t`). | testado em modo usuário com um processo escrevendo em append: nada perdido, sem buraco, 600 preservado; `deploy/logrotate-kalman-ordens` |
| E3 | **Cópia fora da máquina**: na **.51**, `rsync` diário (03:00, depois do logrotate) dos `*.gz` do log FIX para a **.53**, por SSH com chave própria, host conhecido obrigatório e, na .53, a chave restrita ao `rrsync` num diretório. O destino **espelha** os 90 dias da origem (`--delete`): protege contra a perda do disco da .51, não contra apagar na .51. Na .53 não há cópia. Instalação na F2. | testado com destino local (só `.gz`; o apagado na origem sai do destino); `deploy/copia-log-fix.sh`, `deploy/kalman-ordens-copia-log.{service,timer}`, `deploy/copia-log.env.example` |

Instalação: `deploy/LEIA.md` seção 5.

## 3. Cobertura

| Teste | Cobre |
|---|---|
| roteiro 62 | P1–P3: 503 e reenvio, recusas da operação (saída, stop), recusas sem operação (limite, habilitação, corpo ilegível), filtros |
| `internal/repo` `TestContratoHistorico` | pedidos nos dois backends (corpo com acento, sem operação, filtros) |
| `cmd/kalman-ordens` `TestExpurgarCLI` | E1 no MariaDB: simular, apagar nas quatro tabelas, fronteira do corte, resumo intacto, mínimo de meses, sem `EXPURGO_DB_DSN` |
