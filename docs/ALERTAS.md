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
| `MENSAGEM_ILEGIVEL` | a biblioteca descartou em silêncio bytes do PnT que não conseguiu ler (ex.: BodyLength errado); dispara mass status | sim |
| `PROTECAO_EM_ERRO` | 1014 em estado de erro (39=7) sem reativação em `ESPERA_ERRO_S`: cancelada e recriada sob o limite da P8; leva o texto 58; resolvido quando a recriada é aceita | sim |
| `PROTECAO_EM_ERRO_PRESA` | 1014 em erro, cancel rejeitado e o mass status mostra que ela continua viva em 39=7: não é recriada nem a posição zerada (pode reativar pelo 20856); entradas bloqueadas, decisão humana; resolvido quando ela sai do erro | sim |
| `SAIDA_EM_ERRO` | saída em estado de erro (39=7) sem reativação: cancelada, a saída segue com o próximo repreço; leva o texto 58 | sim |
| `ENTRADA_EM_ERRO` | entrada em estado de erro (39=7) sem reativação: cancelada; leva o texto 58 | não |
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
| `PROTECAO_RECRIADA` | 1014 recriada com o mesmo gatilho e limite: cancelada/expirada pelo PnT sem pedido nosso (na hora), recriada rejeitada, ou invariante violada (posição ≠ 0 sem 1014 e sem saída viva por mais de 2 s) | sim |
| `PROTECAO_INSTAVEL` | 1014 falhou de novo depois de 2 recriações em menos de 30 s: a posição é zerada | sim |
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
| `CONTRATO_VIGENTE` | o ticker do marketdata (contrato vigente) mudou: entradas novas vão no novo (v1.2 S1) | não |
| `SIMBOLO_DIVERGENTE_COM_POSICAO` | posição aberta num contrato que deixou de ser o vigente (por operação); a 1014 continua, a saída usa o símbolo da posição; resolve ao zerar ou se o ticker voltar | sim |
| `SIMBOLO_INVALIDO` | o ticker do marketdata não é um contrato de WIN: entrada recusada | não |
| `USUARIO_CONFIG` | configuração do usuário no GQ inutilizável na entrada: JSON inválido, sem `Robo`, uid sem linha em `acusuario`, destino ≠ 85 e ≠ 88, conta não numérica, `acusuario` ou habilitação ilegíveis | não |
| `CONTA_AMBIENTE` | trava cruzada: conta fora de `ORDENS_CONTAS_HML` numa instância HML, ou conta da lista numa instância PRODUCAO; por uid, resolvido na próxima entrada válida dele | sim |
| `CONTA_ALTERADA_COM_POSICAO` | a conta/destino do usuário no GQ mudou com posição aberta; a posição segue pela conta gravada na entrada; resolve ao zerar ou se voltar | sim |
| `SAIDA_DIVERGENTE_NAO_EXECUTOU` | saída com ticker divergente não executou no timeout: não repete, decisão humana | sim |
| `SAIDA_BLOQUEADA_PRECO_ANTIGO` | saída com ticker divergente e último preço do símbolo certo com mais de 60 s: não enviada, 1014 fica | sim |
| `FIM_DIA_POSICAO` | conciliação diária: posição final do Kalman ≠ 0 | sim |
| `RESUMO_DIA` | resumo da conciliação diária (operações, contratos, PnL, recusas, alertas) | não |
| `VIRADA_COM_OPERACAO` | virada de dia com operação não encerrada | sim |
| `ERRO_INTERNO` | exceção no motor | sim |
| `RESOLVIDO` | um alerta crítico deixou de valer | não |

## Códigos de recusa do `POST /ordens`

"Por limite" = conta para a parada automática de 3 recusas em 1 min.

| Código | Quando | Por limite | Alerta |
|---|---|---|---|
| `UID_NAO_HABILITADO` | ENTRADA de uid sem habilitação ativa em `kalmanordemusuario` (só a CLI altera) | não | — |
| `CONFIG_USUARIO_INDISPONIVEL` | `acusuario` ou a habilitação ilegíveis (banco) | não | `USUARIO_CONFIG` |
| `CONFIG_USUARIO_INVALIDA` | JSON do ambiente inválido, sem `Robo.CORSeqId`, ou uid sem linha em `acusuario` | não | `USUARIO_CONFIG` |
| `DESTINO_NAO_SUPORTADO` | `Robo.CORSeqId` = 88 (Capital → Flexscan) | não | — |
| `DESTINO_INVALIDO` | `Robo.CORSeqId` = 9999 (simulador do GQ) ou outro ≠ 85 | não | `USUARIO_CONFIG` |
| `CONTA_INVALIDA` | `Robo.CCONroConta` ausente ou não só dígitos (o simulador usa `QUANT`) | não | `USUARIO_CONFIG` |
| `CONTA_AMBIENTE` | trava cruzada (`ORDENS_AMBIENTE` × `ORDENS_CONTAS_HML`) | não | `CONTA_AMBIENTE` (crítico) |
| `SIMBOLO_INVALIDO` | ticker do marketdata não é contrato de WIN | não | `SIMBOLO_INVALIDO` |
| `CAMPO_INVALIDO` | campo ausente ou inválido; SAIDA com uid diferente do da operação | não | — |
| `SEM_STOP` | ENTRADA sem `stop_pts` | não | — |
| `DUPLICADA` | `operacao_id` (e papel) já recebido | não | `DUPLICADA_DIFERENTE` se o conteúdo difere |
| `PARADA` | serviço em parada | não | — |
| `DIVERGENTE` | estado divergente | não | — |
| `SEM_SESSAO` | sessão FIX não logada | não | — |
| `CONCILIANDO` | conciliação do logon em andamento | não | — |
| `SEM_CONFIRMACAO` | há ordem sem confirmação do PnT | não | — |
| `SIMBOLO_DIVERGENTE` | SAIDA: a posição está num contrato que não é mais o vigente e o último preço dele tem mais de 60 s (a entrada nunca diverge: vai no vigente) | não | `SAIDA_BLOQUEADA_PRECO_ANTIGO` |
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

Precedência na ENTRADA: habilitação → campos → DUPLICADA → PARADA → DIVERGENTE → SEM_SESSAO →
SEM_CONFIRMACAO → CONCILIANDO → conta e destino (acusuario) → SIMBOLO_INVALIDO → limites (globais
e do uid) → preço. A SAIDA não olha habilitação nem a conta atual do GQ: usa o que a entrada gravou.
