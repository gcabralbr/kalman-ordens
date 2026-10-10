# Catálogo de alertas e códigos de recusa

Fonte única. Roteiros (`alerta CODIGO`), Telegram e gestor usam estes códigos.
Todo alerta sai no Telegram e como evento `ALERTA` (`{"codigo": ..., "texto": ..., "operacao_id": ...}`).
**Crítico** = repete a cada 30 s (`ALERTA_REPETICAO_S`) até a condição se resolver; a resolução
gera um alerta `RESOLVIDO` com o código original no texto.

## Alertas

| Código | Significado | Crítico |
|---|---|---|
| `SESSAO_CAIU` | sessão FIX caiu | não |
| `SESSAO_VOLTOU` | sessão FIX logou de novo após uma queda | não |
| `SESSAO_REJEITOU_MENSAGEM` | o serviço enviou 35=3 (a biblioteca descartou uma mensagem do PnT); dispara mass status | sim |
| `CONCILIACAO` | resultado da conciliação do logon (ordens KM, ignoradas, divergências, posição) | não |
| `MASS_STATUS_REJEITADO` | mass status rejeitado por motivo diferente de 380=4; entradas bloqueadas | sim |
| `DESCONHECIDA` | ordem viva na tabela e ausente no mass status | não |
| `DIVERGENTE` | inconsistência de posição ou de ordem não resolvida; BLOQUEAR | sim |
| `SEM_CONFIRMACAO` | ordem em ENVIANDO sem nenhuma resposta do PnT em `PRAZO_CONFIRMACAO_S` | sim |
| `PENDENTE_39A` | ordem em 39=A por mais de 3 s | não |
| `RECUSA_PNT` | ordem, cancel ou replace rejeitado pelo PnT (35=8 39=8, 35=9, 35=3, 35=j) | não |
| `CANCEL_STOP_REJEITADO` | cancel da 1014 rejeitado (35=9): aguardando fills do stop | não |
| `CANCEL_SEM_RESPOSTA` | cancel enviado sem resposta em `ESPERA_STOP_S` | sim |
| `SEM_PROTECAO` | posição sem 1014 aceita por mais de 2 s | sim |
| `PROTECAO_REJEITADA` | 1014 rejeitada (1ª vez: nova tentativa; 2ª: zera a posição) | não |
| `STOP_FURADO` | último além do limite da 1014 por mais de `ESPERA_STOP_S` com posição | sim |
| `SAIDA_ESGOTOU` | saída esgotou `MAX_REPRECO_SAIDA` e continua reapregoando | sim |
| `SAIDA_SEM_PRECO` | saída sem nenhum preço para calcular o limite | sim |
| `SAIDA_PRECO_DEGRADADO` | saída calculada com preço velho ou sem bid/ask | não |
| `FILL_ORDEM_ENCERRADA` | fill recebido em ordem já encerrada (aceito e contabilizado) | não |
| `ENTRADA_TARDIA` | fill de entrada depois de `ESPERA_ENCERRAMENTO_S`: 1014 + zeragem imediata | não |
| `SEM_POSICAO` | saída pedida sem posição real | não |
| `LADO_INCOMPATIVEL` | saída com o lado que aumentaria a posição (bug no gestor) | não |
| `DUPLICADA_DIFERENTE` | pedido com `operacao_id` já recebido e conteúdo diferente | não |
| `LIMITE` | limite de mensagens por minuto ou de entradas por dia atingido | não |
| `PARADA` | parada ativada (tela, cli ou auto), com modo e motivo | não |
| `RETOMADA` | parada retirada (sempre manual) | não |
| `ZERAGEM` | `HORA_ZERAGEM` com posição: ZERAR | não |
| `SIMBOLO_DIVERGENTE` | ticker do marketdata ≠ `ORDENS_SIMBOLO`: entradas bloqueadas | sim |
| `SIMBOLO_DIVERGENTE_COM_POSICAO` | divergência detectada com posição aberta (no lugar de `SIMBOLO_DIVERGENTE`); a 1014 continua | sim |
| `SAIDA_DIVERGENTE_NAO_EXECUTOU` | saída com ticker divergente não executou no timeout: não repete, decisão humana | sim |
| `SAIDA_BLOQUEADA_PRECO_ANTIGO` | saída com ticker divergente e último preço do símbolo certo com mais de 60 s: não enviada, 1014 fica | sim |
| `VENCIMENTO` | aviso antecipado: faltam ≤ 3 pregões para o vencimento do símbolo | não |
| `FIM_DIA_POSICAO` | conciliação diária: posição final do Kalman ≠ 0 | sim |
| `RESUMO_DIA` | resumo da conciliação diária (operações, contratos, PnL, recusas, alertas) | não |
| `VIRADA_COM_OPERACAO` | virada de dia com operação não encerrada | sim |
| `ERRO_INTERNO` | exceção no motor | sim |
| `RESOLVIDO` | um alerta crítico deixou de valer | não |

## Códigos de recusa do `POST /ordens`

"Por limite" = conta para a parada automática de 3 recusas em 1 min.

| Código | Quando | Por limite | Alerta |
|---|---|---|---|
| `UID_NAO_HABILITADO` | uid ≠ `ORDENS_UID_REAL` | não | — |
| `CAMPO_INVALIDO` | campo ausente ou inválido | não | — |
| `SEM_STOP` | ENTRADA sem `stop_pts` | não | — |
| `DUPLICADA` | `operacao_id` (e papel) já recebido | não | `DUPLICADA_DIFERENTE` se o conteúdo difere |
| `PARADA` | serviço em parada | não | — |
| `DIVERGENTE` | estado divergente | não | — |
| `SEM_SESSAO` | sessão FIX não logada | não | — |
| `CONCILIANDO` | conciliação do logon em andamento | não | — |
| `SEM_CONFIRMACAO` | há ordem sem confirmação do PnT | não | — |
| `SIMBOLO_DIVERGENTE` | ENTRADA: ticker do marketdata ≠ `ORDENS_SIMBOLO`. SAIDA: idem e último preço do símbolo certo com mais de 60 s | não | entrada: o alerta é do estado; saída: `SAIDA_BLOQUEADA_PRECO_ANTIGO` |
| `FORA_JANELA` | fora de 09:00–14:00 | sim | — |
| `QTD_MAXIMA` | qtd > limite por ordem | sim | — |
| `POSICAO_MAXIMA` | posição + entradas pendentes + qtd > máximo | sim | — |
| `INTERVALO_MINIMO` | menos de 60 s desde a última entrada | sim | — |
| `ENTRADAS_POR_MINUTO` | limite de entradas por minuto | sim | — |
| `ORDENS_POR_MINUTO` | limite de mensagens por minuto | sim | `LIMITE` |
| `ENTRADAS_POR_DIA` | limite de entradas por dia | sim | `LIMITE` |
| `PERDA_DIARIA` | perda do dia atingida (e BLOQUEAR) | sim | `PARADA` |
| `SEM_PRECO` | sem resposta de preço, `{}`, 404, 400, ou falta preco/bid/ask | sim | — |
| `LEILAO` | `status84 != 0` | sim | — |
| `PRECO_VELHO` | `idade_seg > 3` | sim | — |
| `BOOK_INVALIDO` | `bid >= ask` | sim | — |
| `BOOK_DESCOLADO` | \|meio − preco\| > 150 | sim | — |
| `ENVIO_FALHOU` | a biblioteca recusou a mensagem antes do fio | não | `RECUSA_PNT` |
| `SEM_POSICAO` | SAIDA sem posição real | não | `SEM_POSICAO` (não quando a operação já foi zerada pelo stop) |
| `LADO_INCOMPATIVEL` | SAIDA com o lado que aumentaria a posição | não | `LADO_INCOMPATIVEL` |
| `RESULTADO_DESCONHECIDO` | (HTTP 503) motor não respondeu a tempo; consultar `GET /ordens/{id}` | não | — |

Precedência na ENTRADA: uid → campos → DUPLICADA → PARADA → DIVERGENTE → SEM_SESSAO →
SEM_CONFIRMACAO → CONCILIANDO → SIMBOLO_DIVERGENTE → limites → preço.
