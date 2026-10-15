# Spec de Implementação — `kalman-ordens` — v1.5

Revisão da [SPEC-ORDENS-v1.4.md](SPEC-ORDENS-v1.4.md). Lista **só o que mudou**; onde a v1.5 não
diz nada, vale a v1.4.

Tema: **suporte a reclamação de cliente** — histórico para a tela de admin do Kalman (contrato
oficial do responsável, 15/10), o que fica gravado de cada mensagem FIX e a retenção.

Data: 2026-10-15.

---

## 1. O que fica gravado (R1)

Conferido no código (`internal/motor`: `enviar`, `registrarRecebida`). Toda linha de
`kalmanordemevento` leva a mensagem inteira em `fix_raw`, mais as colunas já extraídas (150, 39,
11, 37, 17, 32, 31, 14, 6, 58, 102/103/380/373).

| Mensagem | Gravada com `fix_raw`? |
|---|---|
| ENV `D`, `F`, `G` (1031, 1014, cancel, replace) | **sim**, todas, inclusive as que a biblioteca recusou antes do fio (`texto = NAO_ENVIADA`) |
| ENV `AF` (mass status) | **sim** |
| ENV `3` (a nossa biblioteca descartou uma mensagem do PnT) | **sim** (o 35=3 enviado) |
| REC `8` das ordens KM | **sim**. A repetição do mesmo (37, 17) — reenvio, mass status — não é gravada de novo (chave única, v1.3 H1) |
| REC `8` do mass status (150=I) das ordens KM | **sim**. **Corrigido na v1.5**: antes era gravada com o horário do fim da conciliação; agora com o da chegada |
| REC `8` de ordens de outros sistemas na mesma conta (PT) | **não** (filtro `KM-`, por desenho): só no log FIX em arquivo |
| REC `9` | **sim**. **Corrigido na v1.5**: o 35=9 de um ClOrdID `KM-` sem ordem conhecida não era gravado |
| REC `j` | **sim** (380=101 do mass status vazio, AF rejeitado, os demais) |
| REC `3` | **sim** |
| Mensagens de sessão (`A`, `0`, `1`, `2`, `4`, `5`) | **não** no `kalmanordemevento`: só no log FIX em arquivo. Logon, logout, queda, logon recusado e ResendRequest passam a ter um registro resumido em `kalmanordemsessao` (R3) |
| Bytes ilegíveis descartados pela biblioteca (F12) | só o texto do evento da biblioteca (`msgtype = ?`) |

A senha só existe no logon (mensagem de sessão), que não vai ao banco; no log FIX em arquivo sai
`554=***` (F15). O `fix_raw` do histórico ainda passa pela máscara (554/925).

**Retenção atual: nenhuma.** O banco só cresce (o usuário do serviço não tem `DELETE`, por
desenho). O log FIX em arquivo são dois arquivos (`FIX.4.4-<sender>-<target>.messages.current.log`
e `.event.current.log`, em `/var/lib/kalman-ordens/fix/log`) que o QuickFIX/Go abre em modo append
e mantém abertos enquanto o processo vive, **sem rotação**. O journal segue a configuração do
journald da máquina.

## 2. Histórico para a tela de admin (R2) — contrato oficial

Só leitura, em `127.0.0.1:9092` (o HTTP continua recusando subir fora de loopback), direto do
banco (qualquer dia; não passa pela fila do motor). Todo `em` em RFC3339 com o fuso do pregão e
milissegundos (`2026-10-15T11:00:03.000-03:00`). Campo sem valor sai `null`.

**`GET /operacoes?dia=AAAA-MM-DD[&uid=N][&modulo=KALMAN]`** → lista, em ordem de criação:
`{operacao_id, uid, modulo, estado, lado, qtd, preco_medio_entrada, preco_medio_saida, pnl_pts,
motivo_saida, origem_saida, criada_em, zerada_em, alertas_n}`. `lado` = `C`/`V` da entrada; `qtd`
= a quantidade pedida na entrada; `preco_medio_*` nulo sem execução; `zerada_em` = o horário do
evento `ZERADA` (nulo se não zerou); `alertas_n` = alertas publicados com a operação. Sem `uid`
ou sem `modulo`: todos. `dia` ausente ou inválido → 400.

**`GET /operacoes/{id}/historico[?modulo=KALMAN][&cru=1]`** →
`{operacao, eventos, mensagens}`:
- `operacao`: o mesmo objeto do `GET /ordens/{id}` (mesma função);
- `eventos`: o que foi publicado ao gestor para a operação (inclusive `ALERTA`):
  `{seq, tipo, dados, em}`;
- `mensagens`: cada mensagem FIX de aplicação das ordens da operação, em ordem:
  `{em, direcao, msgtype, exectype, ordstatus, clordid, orderid, lastqty, lastpx, cumqty, avgpx,
  texto, codigo}` e, só com `cru=1`, `fix_raw` (senha mascarada, SOH como `|`).
- Operação inexistente no módulo → 404 `{"existe": false}`.

**`GET /sessao/eventos?dia=AAAA-MM-DD`** → `[{em, tipo, texto}]`, `tipo` = `LOGON` (logon aceito),
`LOGOUT` (a sessão terminou com 35=5, nosso ou do PnT; texto com o 58), `QUEDA` (terminou sem
logout), `LOGON_RECUSADO` (texto = o 58, ex.: "already logged on"), `RESEND` (ResendRequest
enviado ou do PnT, com 7 e 16), `CONCILIACAO` (o resumo da conciliação do logon), `ALERTA` (os
alertas sem operação: `CODIGO: texto`, inclusive `PARADA`/`RETOMADA`, `SESSAO_CAIU`/`SESSAO_VOLTOU`).
Gravados na tabela nova `kalmanordemsessao` (migração `0006_historico.sql`) — antes só existiam
no log FIX em arquivo e no journal. Texto em ASCII (v1.4 L1).

**CLI** (`kalman-ordens historico`), o mesmo conteúdo em JSON, direto do banco (funciona com o
serviço parado; lê o `DB_DSN` como a CLI `usuario`; recusa com o schema atrás do binário):

```
kalman-ordens historico operacao -id N [-modulo M] [-cru]   = GET /operacoes/{id}/historico
kalman-ordens historico dia [-dia D] [-uid N] [-modulo M]   = GET /operacoes?dia=
kalman-ordens historico sessao [-dia D]                     = GET /sessao/eventos?dia=
```

`-dia` padrão: hoje.

Migração `0006_historico.sql`: tabela `kalmanordemsessao` e índice `(modulo, operacao_id)` em
`kalmanordempub`. Só `CREATE`/`ALTER`.

## 3. Retenção (R4) — proposta, a decidir

| O quê | Onde | Proposta |
|---|---|---|
| `kalmanordemoperacao`, `kalmanordem` (o resumo de cada operação e ordem) | banco | **sem expurgo**: é o registro do que foi operado, poucas linhas por dia (centenas de bytes por operação) |
| `kalmanordemevento` (mensagens FIX com `fix_raw`), `kalmanordempub`, `kalmanordemsessao` | banco | **12 meses**. Expurgo mensal por um comando próprio (`kalman-ordens expurgar -meses 12`, a fazer) num timer do systemd, com um usuário de banco **só para isso** (`DELETE` só nessas três tabelas); o usuário do serviço continua sem `DELETE`. Ordem de grandeza: ~10–20 mensagens por operação, ~1 KB cada → ~1 MB/mês com 50 operações/dia |
| Log FIX em arquivo (`/var/lib/kalman-ordens/fix/log`) | disco da máquina do serviço | **90 dias compactado**: `logrotate` diário com `copytruncate` (o QuickFIX/Go mantém o arquivo aberto em append; o `copytruncate` não exige reabrir), `compress`, `dateext`, `rotate 90`, no mesmo diretório (700, só do usuário `kalman-ordens`: o gzip preserva o 600). É a fonte das mensagens de sessão e das ordens de outros sistemas na conta, que o banco não guarda |
| Store FIX (`/var/lib/kalman-ordens/fix/store`) | disco | **não mexer** (a sessão usa para reenvio; zera sozinha fora do período) |
| Journal | journald | não é fonte: o que importa está no banco e no log FIX |

**Onde guardar**: o banco já tem o backup do MariaDB da máquina (a confirmar se o schema
`kalmanordens` entra nele na .51). Para o log FIX compactado, sugestão: uma cópia diária para fora
da máquina (o mesmo destino dos backups da .51), porque o disco da máquina do serviço é o único
lugar onde ele existe.

Pendências: aprovar os prazos; quem é o dono do expurgo e do destino da cópia; implementar
`expurgar` (com roteiro/teste) e a configuração do logrotate em `deploy/` depois da decisão.

## 4. Proposta não implementada (aguarda decisão)

**Gravar os pedidos HTTP.** Hoje um `POST /ordens` **recusado** (limite, preço, habilitação...)
não cria operação e só aparece no journal — numa reclamação do tipo "o Kalman mandou e não foi",
é justamente o que falta. Proposta: tabela `kalmanordempedido` (dia, módulo, operação, uid, rota,
papel, aceito, código, corpo, resposta, `em`) com todo `POST /ordens` e `POST
/operacoes/{id}/stop`, inclusive reenvios, e um campo `pedidos` no `GET /operacoes/{id}/historico`
(mais `recusas` no `GET /operacoes`). Muda o contrato oficial: não entra sem aprovação.

## 5. Cobertura

| Teste | Cobre |
|---|---|
| roteiro 61 | R2, R3: linha do tempo (operação, 5 eventos, 10 mensagens com status, preço, quantidade e 37; `fix_raw` só com `cru=1`), 404, operações do dia (filtros, 400), eventos da sessão (LOGON, CONCILIACAO, QUEDA, RESEND da mensagem perdida, LOGON_RECUSADO, LOGOUT do restart, ALERTA) |
| `internal/repo` `TestContratoHistorico` | as consultas novas nos dois backends |
| `internal/motor` `TestFixLegivel`, `TestDia` | máscara da senha no `fix_raw`; o dia do pregão |
| `cmd/kalman-ordens` `TestHistoricoCLI` | a CLI com o MariaDB: os três comandos, `-cru`, filtros, schema atrás do binário |
