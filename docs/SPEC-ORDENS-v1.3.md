# Spec de Implementação — `kalman-ordens` — v1.3

Revisão da [SPEC-ORDENS-v1.2.md](SPEC-ORDENS-v1.2.md) (que revisa a v1.1 e a v1). Lista **só o
que mudou**; onde a v1.3 não diz nada, vale a v1.2.

Tema único: **resultado da rodada 1 da F1** na .53 (14/10/2026, HML do PnT, binário `03c7e05`) e
as correções que ela pediu antes de qualquer novo start na .53.

Data: 2026-10-14. Itens A–G vindos do responsável; A2, F2 (propostas desta implementação) e H1
(a pendência do ExecID) confirmados pelo responsável no mesmo dia.

---

## 1. Confirmado na HML

| Item (`deploy/LEIA.md` 2.x) | Resultado | Fecha |
|---|---|---|
| 2.1 Logon e mass status | `585=9` aceito. Sem ordens, a resposta é `35=j 380=101 "No orders found."`, **sem a 372** (E). | F10 da v1.1 |
| 2.3 1031 longe | `39=A` → `39=0`; cancel → `39=6` → `39=4`. Cancel com 1 e 116 aceito. | pendência 3 da v1.2 |
| 2.5 / 2.11 1014 | **A 1014 funciona no DSA**: enviada 6 ms depois do fill, `39=A` → `39=0`, ecoa 20828/20829. O dicionário do PnT não tem as tags, mas o PnT aceita. A decisão D2 da v1 fica. | [PnT-17], pendência 4 da v1.2 |
| 2.10b `20856=Y` | aceito na 1031 e na 1014. | regra (1) da F14 |
| 2.10e Conta e símbolo | `1`/`116`/`142`/`55=WINZ26` como esperado. | — |
| 2.2 Restart com logout limpo | logon com a sequência contínua, `141=N`; o buraco do lado do PnT coberto por ResendRequest/SequenceReset. O PnT não exige reset. | [PnT-11] |
| 2.6 Cancel-on-Disconnect | **parcial**: com logout limpo a 1014 sobrevive. Em queda abrupta não deu para verificar (A); novo teste depois do deploy desta versão. | — |
| 2.7 Mass status | traz **todas** as ordens do dia, inclusive as encerradas (`39=4`; `39=2` com `14=1`, `6=211560` corretos), com 911/912. | [PnT-13], pendência 3 da v1.1 |
| Execução no livro | venda limite 208355 executou a 211560: a HML é o pregão de ontem (~3400 pts acima de hoje). Preço de teste na rodada 2 vem do replay. | — |

## 2. Sessão FIX (A–D)

| # | Regra | Substitui | Roteiro / teste |
|---|---|---|---|
| A1 | **O store guarda a mensagem como foi ao fio, sem máscara.** Em 14/10, depois de um SIGKILL, cada logon recusado (D) consumiu um número nosso, gravado no store **com a senha mascarada** (F15 da v1.1). Aceito o logon, o PnT pediu ResendRequest a partir do primeiro recusado; a biblioteca releu os logons mascarados e não conseguiu interpretá-los (`Resend Msg Parse Error: Incorrect Message Length, expected 95, got 91`): desconecta → o PnT segura a sessão meio aberta → laço. Agora a máscara (554, 925) vale só para os logs (mensagens e eventos); o store fica protegido como o service.env (diretório FIX 700/600, só do usuário `kalman-ordens`). **Na subida (e a cada reinício pelo vigia, B1) o store é reparado**: mensagem que o reenvio não leria tem 9 e 10 recalculados; se ainda não se lê, vira um heartbeat com o mesmo 34 (gap fill no reenvio, como já seria com uma ordem). O índice é trocado de uma vez (arquivo novo + rename); o corpo só recebe acréscimos. Isso conserta o store da .53 no primeiro start. | parte do store da F15 (v1.1) | 47; `pnt` `TestLogonsRecusadosReenvioViraGapFill`, `TestStoreDaV12ReparadoNaSubida`, `TestSenhaSoNoStore` |
| A2 | **Envio liberado `FIX_SINCRONIA_MS` (500 ms) depois do logon** *(confirmado)*. Logo depois do logon o PnT pede o reenvio do que perdeu; o mass status enviado no `OnLogon` saía antes do nosso gap fill, com a sequência adiantada. O PnT tem de guardá-lo até o buraco fechar; o QuickFIX/Go (o PnT falso) **perde** essa mensagem (o stash do estado de reenvio só sobrevive se já existia), e ela nunca volta, porque mensagem de aplicação não se reenvia (ToApp). Sem resposta ao mass status, a conciliação daria a 1014 viva como `DESCONHECIDA` → DIVERGENTE + BLOQUEAR. Com a espera, o ResendRequest chega antes e o reenvio segura os envios até o gap fill sair. Durante a espera a sessão conta como não logada (`SEM_SESSAO`); o que chega nela (um fill reenviado, por exemplo) é entregue ao motor logo depois do `Logou`, na ordem. **Registro da janela** no log do serviço (`recebidas_na_janela`, `reenvios_na_janela`) e no log de eventos FIX ("… nos 500 ms de sincronia"): na HML, um ResendRequest do PnT que chegue *depois* da janela mostra que 500 ms não bastam. | — | 47 (reprova com `FIX_SINCRONIA_MS=1`), 09; fumaça `TestServirDePontaAPonta` (registro) |
| B1 | **Store ilegível no reenvio → alerta crítico `FIX_STORE_ILEGIVEL`**, nunca laço silencioso. A camada PnT lê o evento `error retrieving messages from store` da biblioteca, avisa o motor, derruba a sessão, repara o store (A1) e reconecta com espera crescente. Resolve no logon seguinte. | — | 49; `pnt` `TestStoreIlegivelNoReenvio` |
| C1 | **Logout `MsgSeqNum too low, expecting N`** → a próxima sequência de envio passa a N antes da próxima tentativa (a biblioteca subia de 1 em 1, uma tentativa por `FIX_RECONEXAO_S`). Só para cima; evento no log FIX. | — | 48; `pnt` `TestLogoutTooLowAjustaSequencia` |
| D1 | **Logon recusado com "already logged on"**: depois de uma queda abrupta o PnT segura a sessão (SIGKILL ~15:02:05, aceito 15:05:58: **~3,5 min**). Cada recusa → nova tentativa com **espera crescente** (`FIX_RECONEXAO_S` dobrando até `FIX_RECONEXAO_MAX_S` = 60 s: 5, 10, 20, 40, 60, 60… — a espera volta ao início depois de uma sessão que ficou de pé por 1 min). Recusando há `SESSAO_PRESA_ALERTA_S` (60 s) → alerta crítico `SESSAO_PRESA_NO_PNT`, resolvido no logon. **Depois de um travamento, contar com até ~4 min sem sessão; a 1014 cobre a posição** (fica no PnT; o serviço parado não cancela nada). | — | 47; `pnt` `TestLogonsRecusadosReenvioViraGapFill` (intervalos) |

O vigia (A1, B1, D1) é uma goroutine da camada PnT que, a cada pedido, para o initiator, repara o
store, espera e sobe outro; a biblioteca, sozinha, só sabe tentar de novo a cada
`ReconnectInterval`, para sempre. Os logs da sessão são reaproveitados entre initiators (o FileLog
do QuickFIX/Go não tem Close).

## 3. Mensagens (E, F)

| # | Regra | Substitui | Roteiro / teste |
|---|---|---|---|
| E1 | **Corpo do 35=j sem obrigatórios na cópia do dicionário** (`RefMsgType` 372 e `BusinessRejectReason` 380), como no 8 e no 9 (F8 da v1.1): o `35=j 380=101` sem 372 era rejeitado pela biblioteca — 35=3 ao PnT a cada subida sem ordens e `SESSAO_REJEITOU_MENSAGEM` falso. Diferença 2 de `pnt/dicionario/LEIA.md`, gerada por `Derivar`. | — | 45; `TestFillIncompletoNaoEDescartado` (35=j sem 372) |
| E2 | **`35=j 380=101` = mass status vazio**: registrado, sem alerta; a conciliação continua fechando no silêncio. Vale com a 372 ausente ou `AF`, com ou sem a 379. Fora da conciliação, só registro. | — | 45 |
| F1 | **Cancel: o `39=4` volta com 11 = 41 = ClOrdID original** (não o do pedido de cancel) e chega **~5 s depois do `39=6`**. A ordem é achada pelo 37 (e pelo ClOrdID original): nada muda no motor. **`ESPERA_ENCERRAMENTO_S` passa a 8 s** (contados do 39=4). | A3 da v1.1 (5 s) | 46; 02, 04, 04b, 13, 17, 19, 32 com o tempo novo |
| F2 | **O `39=6` é resposta ao cancel** *(confirmado)*. Com o `39=4` 5 s depois do `39=6`, o `CANCEL_SEM_RESPOSTA` (crítico, `ESPERA_STOP_S` = 5 s desde o cancel) disparava em todo cancel da HML. Agora: sem `39=6` nem `39=4` em `ESPERA_STOP_S` → alerta (como antes); com `39=6`, só se o `39=4` não vier em `ESPERA_ENCERRAMENTO_S` (8 s) depois dele. A pendência do cancel continua até o `39=4` (nada de novo replace/cancel no meio). | — | 46 (partes 1–3) |

## 3b. Persistência (H)

| # | Regra | Substitui | Roteiro / teste |
|---|---|---|---|
| H1 | **ExecID (17) único por ordem: chave `(orderid37, execid)`** em `kalmanordemevento` (migração `0003_execid_por_ordem.sql`, só `ALTER`). Na HML o fill trouxe 17 numérico com 1057 (cara de TradeID da bolsa), que pode se repetir entre ordens e entre dias; com a chave só no 17, o fill novo com o 17 de outro era ignorado em silêncio ("ExecID repetido"). O mesmo 17 em outra ordem agora é outro fill; na mesma ordem (reenvio, repetição), aplicado uma vez. Sem 37 (NULL) não há conflito; a posição continua idempotente pelo maior CumQty por ordem (F7 da v1.1). | chave única só no `execid` (v1, P1) | 50; `internal/repo` `TestContrato` (memória e MariaDB) |

## 4. Registro (G)

Observado na HML em 14/10; nada muda no serviço por enquanto.

- O PnT devolve **`59=6` (GTD) com `126=21:25`** para o nosso `59=0` (DAY), e **`207=XBMF`** para o
  nosso `BVMF`. O motor não lê 59, 126 nem 207 das respostas.
- O fill (`150=F`) traz **17 numérico** e **1057** (AggressorIndicator). No log de 2025 o 17 era
  `Tipo_<contador>:<sufixo>`. Ver H1.
- Os timestamps (52) da HML estão **~2 s à frente** do nosso relógio. `CheckLatency=N` (v1.1 seção
  5): nada é rejeitado por isso.

## 5. Configuração

| Chave | Inicial | Uso |
|---|---|---|
| `FIX_RECONEXAO_MAX_S` | 60 | teto da espera crescente (B1, D1); a base é `FIX_RECONEXAO_S` |
| `FIX_SINCRONIA_MS` | 500 | A2 |
| `SESSAO_PRESA_ALERTA_S` | 60 | D1 |
| `ESPERA_ENCERRAMENTO_S` | **8** (era 5) | F1, F2 |

## 6. Pendências

1. **Cancel-on-Disconnect em queda abrupta (2.6, [PnT-12])**: repetir na .53 com esta versão
   (`deploy/LEIA.md` 2.6b). Com A1–D1, a sessão volta sozinha depois dos ~3,5 min do PnT.
2. ~~ExecID (17) único no banco inteiro.~~ **Resolvida por H1.**
3. **911/912 no mass status**: o PnT manda `TotNumReports`/`LastRptRequested`; a conciliação ainda
   fecha no silêncio (`MASS_STATUS_SILENCIO_S`). Fechar pelo 912 é melhoria possível, sem roteiro.

## 7. Cobertura dos roteiros

| Roteiro | Cobre |
|---|---|
| 45 | E1, E2 (35=j 380=101 sem 372: sem 35=3, sem alerta; conciliação vazia) |
| 46 | F1, F2 (39=6 → 5 s → 39=4 com o ClOrdID original, na entrada e na 1014; 39=6 sem 39=4 → alerta em 8 s) |
| 47 | A1, A2, D1 (logons recusados consumindo sequência, `SESSAO_PRESA_NO_PNT` aos 60 s, logon aceito, ResendRequest dos logons → gap fill, conciliação com a 1014 viva) |
| 48 | C1 (logout "MsgSeqNum too low, expecting N": logon seguinte já com N) |
| 49 | B1 (store ilegível no reenvio: `FIX_STORE_ILEGIVEL`, reparo, reconexão, sessão de pé) |
| 50 | H1 (mesmo 17 na entrada e na saída → os dois fills; repetido na mesma ordem → uma vez) |
| 02, 04, 04b, 13, 17, 19, 32 | `ESPERA_ENCERRAMENTO_S` = 8 s (04b: fill aos 6 s, além dos 5 s antigos, ainda dentro da espera) |
