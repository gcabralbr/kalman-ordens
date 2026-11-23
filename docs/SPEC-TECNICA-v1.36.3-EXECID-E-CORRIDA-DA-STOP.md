# SPEC técnica v1.36.3: o ExecID das recusas da Flexscan e a corrida da stop

23/11/2026. Branch `v1.36.3` (worktree `../kalman-ordens-v1363`), a partir do `main` `3b0407b`
(o instalado é a v1.36.2 `91df575bcc22`). Escopo aprovado pelo George em 23/11: §2, §3 (item A) e
§4 (item B.1). O B.2 fica para a v1.37. Sem migração e sem mudança de contrato.

## 1. O que aconteceu em 23/11

**Op 1123094048 (5725, Flexscan, conta D-4582351).**
- O POST foi recusado às 09:40:58 com 400 "Invalid client document!".
- A operação ficou em `ENTRANDO`, com a E em `ENVIANDO`.
- O log mostra `"ExecID repetido, ignorado" execid=NONE:8:8:0:1`.

A ponte monta o ExecID dos relatórios que ela mesma gera como `id:150:39:exec:contador`. Uma ordem
recusada no POST não tem id, então o id sai `NONE`. O contador é do processo e volta a 1 a cada
restart. Antes do restart das 09:40, as recusas das 09:11 e das 09:36 tinham levado `NONE:8:8:0:1` e
`NONE:8:8:0:2`. O restart recarregou as duas chaves no `execVistos`, e a recusa das 09:40:58 repetiu
a `NONE:8:8:0:1`.

A ordem não virava `SEM_CONFIRMACAO`, porque a chegada estava registrada. O cancelamento pela boleta
(09:49) levou a uma conciliação: a ordem foi julgada ausente, deu DESCONHECIDA e uma Parada BLOQUEAR
da instância REAL.

**Op 234820 (1450, PnT), às 10:11.**

| hora | o quê |
|---|---|
| 11.380 | a stop manual vai de 165540 para 165840: replace `-3` |
| 11.423 | 150=E (replace pendente) |
| 11.493 | a saída do Kalman (STOP_MANUAL) chega com o replace pendente → `CANCELANDO_PROTECAO`, cancel guardado (`cancelarApos`) |
| 11.503 | 150=5 → o 35=F (`-4`), **o único** |
| 11.777 | 35=9 "Order could not be cancelled in time." → `CANCEL_STOP_REJEITADO`, `AGUARDANDO_STOP` |
| 11.783, 13.190, 13.202 | o mesmo 35=9, cada um com o seu MsgSeqNum, sem PossDup → um `RECUSA_PNT` cada |
| 13.444 | o fill da 1014 a 165835 → ZERADA (STOP_SERVIDOR) |
| 10:12:22 (+69 s) | mais dois 35=9 iguais → mais dois `RECUSA_PNT`, com a operação já fechada |

- **A saída não foi por replace porque o replace do ajuste estava pendente.** A
  `saidaPorReplacePossivel` (v1.11) exige a 1014 sem pedido pendente, e a saída caiu no cancel.
- **Os `RECUSA_PNT` vieram das repetições do PnT:** cada 35=9 sem pedido nosso pendente caía no
  `default` da `cancelRejeitado`.

A stop manual não estava além do preço quando foi pedida: o `STOP_ALEM_DO_PRECO`
(`internal/motor/ajuste.go:144`) já recusa uma stop que não esteja abaixo do bid (ou acima do ask).
O preço chegou a ela em menos de 100 ms. **Essa trava continua como está.** O serviço não ganha
recusa nova; a tela avisa antes.

## 2. O ExecID dos relatórios gerados pela ponte da Flexscan

**Hoje (`internal/flexscan/ponte.go:1421`):** `fmt.Sprintf("%s:%s:%s:%d:%d", id, et, st, o.exec,
seqExec.Add(1))`, com `seqExec` global no pacote.

**Muda para:** o contador e uma marca de partida ficam na `Ponte`. A marca são 4 bytes aleatórios
em hex (8 caracteres), sorteados no `Nova`:
```go
ex = fmt.Sprintf("%s:%s:%s:%d:%s.%d", id, et, st, o.exec, p.partida, p.seqExec.Add(1))
```

- **O que não muda:** os ExecIDs que já são determinísticos, porque o motor deduplica por eles. É o
  caso do fill (`id:F:exec`) e do mass status (`MS:reqID:id`).
- **O 37 continua `NONE` quando não há id:** o motor depende disso (`recebe.go:176`, `:741`).
- **O tamanho cabe:** a coluna `execid` é `VARCHAR(80)`. Um id da Flexscan tem 36 caracteres, e o
  ExecID mais longo fica abaixo de 70.
- **Por que o contador vai para a `Ponte`:** um restart no mesmo processo (o
  `reinicia_servico` dos roteiros) passa a se comportar como o restart real. Assim o roteiro reprova
  sem a marca.

## 3. Item A: a recusa durante a execução da stop

### 3.1 A primeira recusa do cancel da 1014
Sem mudança. Com a operação em `CANCELANDO_PROTECAO`, sai um `CANCEL_STOP_REJEITADO` (não crítico)
e a operação vai a `AGUARDANDO_STOP`. A rede de segurança é a de hoje: sem o fill em
`ESPERA_STOP_S`, a saída segue.

### 3.2 As repetições e as recusas atrasadas do 35=9
Em `cancelRejeitado`, logo depois do registro da mensagem:
```go
// v1.36.3 (op 234820): o PnT repete o 35=9 do mesmo pedido, até 69 s depois do fill. Sem pedido
// nosso pendente e com a operação esperando a stop ou já encerrada, só registro.
if era == semPendencia && (op.Estado.Final() || op.Estado == dominio.AguardandoStop) {
	slog.Info("35=9 repetido ou atrasado: só registro", ...)
	return
}
```

- **O alcance:** vale para 434=1 e 434=2 e para qualquer papel.
- **A mensagem fica no histórico:** ela é registrada antes do `return`.
- **O que continua como hoje:** um 35=9 com pedido pendente, ou com a operação em outro estado.

### 3.3 A saída por replace (v1.11) recusada com a stop executando
Hoje sai um `RECUSA_PNT` ("replace de saída da 1014 rejeitado") antes de `replaceDeSaidaRecusado`.
Muda para seguir a mesma condição que essa função já usa para ir a `AGUARDANDO_STOP`. A condição
vira a função `stopEmExecucao(p, msg, texto)`: "in time" no texto, 39 em B/2/4/8, a 1014 disparada
ou não viva.
- **Quando a condição vale:** sai o `CANCEL_STOP_REJEITADO` (não crítico), com o texto "replace de
  saida da 1014 rejeitado (...): aguardando fills do stop", **sem `RECUSA_PNT`**.
- **Fora dela:** o `RECUSA_PNT` de hoje, e o cancel mais a 1031.

As repetições caem no §3.2, porque a operação já está em `AGUARDANDO_STOP`.

## 4. Item B.1: a saída pedida com o replace da 1014 pendente

**Em `prosseguirSaida`:** com a 1014 viva, um replace dela pendente (`pend == pendReplace`) e sem
saída por replace em curso (`saidaRep == nil`), **não se guarda mais o cancel**. Guarda-se a decisão:
- `CANCELANDO_PROTECAO`, como hoje, para a operação contar como saindo e recusar novos ajustes;
- `op.saidaAposReplace = true`, sem 35=F.

**Na resposta do replace, decidir de novo** (`decidirSaidaAposReplace(op, p, disparada)`):

| resposta do replace | decisão |
|---|---|
| 150=5, com o gatilho novo já ultrapassado pelo preço ou a 1014 disparada | **a stop é a saída:** `AGUARDANDO_STOP`, sem 35=F e sem 1031 |
| 150=5, sem gatilho ultrapassado | `prosseguirSaida`: a saída por replace da v1.11 (com `SAIDA_POR_REPLACE`) ou o cancel mais a 1031 |
| 35=9 com a stop executando (`stopEmExecucao`) | `AGUARDANDO_STOP`. No lugar do `RECUSA_PNT` sai o `CANCEL_STOP_REJEITADO` ("replace da 1014 rejeitado ... a stop executa"). O `PROTECAO_AJUSTE_RECUSADO` sai como hoje |
| 35=9 por outro motivo | o `RECUSA_PNT` de hoje e o `prosseguirSaida` com o gatilho antigo (ultrapassado → `AGUARDANDO_STOP`) |
| 150=4/8 ou o fill da 1014 | os caminhos de hoje (`ordemEncerrada` manda a 1031; o fill zera); a marca é limpa |

- **O que conta como gatilho ultrapassado:** o preço da fonte do conector (`precosDo`), com o
  `ticker` da operação e a idade dentro de `IDADE_MAX_PRECO`. Na venda, `bid ≤ gatilho` ou
  `preco ≤ gatilho`; na compra, `ask ≥ gatilho` ou `preco ≥ gatilho`. Sem preço utilizável, a
  resposta é "não", e vale o caminho de sempre.
- **O que um restart faz:** a marca é só da memória. Depois de um restart, a operação em
  `CANCELANDO_PROTECAO` sem pedido pendente cancela pelo tick, como hoje.
- **Com a regra, a 234820** teria ido direto a `AGUARDANDO_STOP` às 11.503, sem 35=F e sem nenhum
  35=9.

## 5. Os testes

| teste | o que prova |
|---|---|
| **roteiro 138 (novo), a recusa repetida** | saída com a 1014 ativa e o PnT respondendo 39=6, 35=9 "in time" ×2, 39=B, 35=9 ×2, o fill e mais 35=9 ×2 depois da ZERADA: um `CANCEL_STOP_REJEITADO`, `sem_alerta RECUSA_PNT`, nenhuma ordem nova (`nada`) |
| **138, a saída por replace recusada** | `SAIDA_POR_REPLACE=1`, 35=9 "replaced in time" ×5: um `CANCEL_STOP_REJEITADO`, sem `RECUSA_PNT`, `SAIDA_REPLACE_FALHOU seguinte=AGUARDANDO_STOP` |
| **138, B.1 com o gatilho ultrapassado** | stop manual, 35=G pendente, saída STOP_MANUAL, o preço passa do gatilho, 150=5: nenhum 35=F (`nada`), `AGUARDANDO_STOP`, o fill e ZERADA STOP_SERVIDOR |
| **138, B.1 sem gatilho ultrapassado** | o mesmo com o preço longe: no 150=5 sai a saída por replace (`SAIDA_POR_REPLACE`) |
| **138, B.1 com o replace recusado "in time"** | o 35=9 do ajuste com 39=B: `AGUARDANDO_STOP`, sem 35=F e sem `RECUSA_PNT` |
| **roteiro 139 (novo), o restart entre duas recusas** | Flexscan: POST recusado (400) → `ENTRADA_ENCERRADA REJEITADA`; `reinicia_servico`; o segundo POST recusado → também `REJEITADA` e posição 0, sem ordem viva (sem a correção, a 2ª fica em `ENVIANDO`) |
| unidade (`internal/flexscan`) | duas pontes, uma recusa em cada: os ExecIDs diferem; o fill continua `id:F:exec` |
| roteiros existentes | 06, 86, 87, 98, 112 e 113 sem mudança de resultado |

**Verificação ao contrário:**
- com a marca de partida tirada, o 139 tem de reprovar;
- com a regra do §3.2 tirada, o 138 reprova pelo `RECUSA_PNT`;
- com a do §4 tirada, o 138 reprova pelo 35=F.

**A suíte completa** (os dois bancos e `-race`) roda fora da janela do Kalman (depois das 14:30, com
o REAL sem posição). **A instalação** só com o roteiro e depois da zeragem: só binário, sem migração,
um restart.

## 6. Pergunta ao PnT (entra na lista pendente, com as do ESTADO-2026-11-20 §6 e da v1.36.2 §7)

4. **Por que o robô repete o 35=9 do mesmo cancelamento?** Na op 234820 (23/11, GAMMAQ2), um único
   35=F (`11=KM-P-234820-4`, 10:11:11.509) recebeu **seis** 35=9 "Order could not be cancelled in
   time." Eles vieram em pares, com o mesmo SendingTime (13:11:11.764, 13:11:13.167 e
   13:12:22.705 UTC), cada um com o seu MsgSeqNum (213, 215, 217, 219, 224, 225) e sem PossDup.
   O último par chegou **69 s depois do fill** da 1014 (10:11:13.444). É esperado? Dá para receber
   uma resposta só por pedido? O par vem de duas pernas do robô (DSA e Robot)?
