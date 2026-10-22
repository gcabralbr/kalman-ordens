# Catálogo de alertas e códigos de recusa

Fonte única. Roteiros (`alerta CODIGO`), Telegram e gestor usam estes códigos.
**Textos só em ASCII** (SPEC v1.4 L1): o `texto` dos alertas, o `motivo` das recusas e os textos
dos eventos saem sem acento nem símbolo fora do ASCII (o Kalman grava em latin1).
Todo alerta sai no Telegram (direto ou pelo `ALERTA_WEBHOOK_URL`, com o texto `[<ALERTA_SERVIDOR> kalman-ordens] ...`) e como evento `ALERTA` (`{"codigo": ..., "texto": ..., "operacao_id": ...}`).
**Crítico** = repete a cada 30 s (`ALERTA_REPETICAO_S`) até a condição se resolver; a resolução
gera um alerta `RESOLVIDO` com o código original no texto.

## Alertas

| Código | Significado | Crítico |
|---|---|---|
| `SESSAO_CAIU` | sessão FIX caiu | não |
| `SESSAO_VOLTOU` | sessão FIX logou de novo após uma queda | não |
| `SESSAO_REJEITOU_MENSAGEM` | o serviço enviou 35=3 (a biblioteca descartou uma mensagem do PnT); dispara mass status | sim |
| `MENSAGEM_ILEGIVEL` | a biblioteca descartou em silêncio bytes do PnT que não conseguiu ler (ex.: BodyLength errado); dispara mass status | sim |
| `SESSAO_PRESA_NO_PNT` | o PnT recusa o logon há `SESSAO_PRESA_ALERTA_S` (60 s) por ainda segurar a sessão anterior ("already logged on", depois de uma queda abrupta; na HML, ~3,5 min); a camada PnT tenta de novo com espera crescente; a 1014 continua protegendo a posição no PnT; resolvido no logon (v1.3 D1) | sim |
| `FIX_STORE_ILEGIVEL` | o reenvio pedido pelo PnT (ResendRequest) não conseguiu ler o store FIX; a camada PnT derrubou a sessão, reparou o store e reconecta com espera crescente; resolvido no logon seguinte (v1.3 B1) | sim |
| `PROTECAO_EM_ERRO` | 1014 em estado de erro (39=7) sem reativação em `ESPERA_ERRO_S`: cancelada e recriada sob o limite da P8; leva o texto 58; resolvido quando a recriada é aceita | sim |
| `PROTECAO_EM_ERRO_PRESA` | 1014 em erro, cancel rejeitado e o mass status mostra que ela continua viva em 39=7: não é recriada nem a posição zerada (pode reativar pelo 20856); entradas bloqueadas, decisão humana; resolvido quando ela sai do erro | sim |
| `SAIDA_EM_ERRO` | saída em estado de erro (39=7) sem reativação: cancelada, a saída segue com o próximo repreço; leva o texto 58 | sim |
| `PNT_BOLSA_INDISPONIVEL` | disjuntor de bolsa indisponível (SPEC v1.10 §9): rejeição do PnT com texto de `DISJUNTOR_TEXTOS` ("Exchange is offline"), ou `DISJUNTOR_REJEICOES` rejeições iguais em `DISJUNTOR_JANELA_S`; entradas recusadas, saídas e 1014 em sonda a cada `DISJUNTOR_SONDA_S`; resolve no primeiro aceite ou execução limpos | sim |
| `ENTRADA_EM_ERRO` | entrada em estado de erro (39=7) sem reativação: cancelada; leva o texto 58 | não |
| `BANCO_QUARENTENA` | (v1.13) linha do lote recusada pelo banco por erro de dado (1406, 1366, 1264, 1292, 1048, 1452): foi para a `kalmanordemquarentena` e o resto do lote gravou; o serviço segue. Na subida com linha pendente: PARADA (BLOQUEAR, origem QUARENTENA). Resolve no `POST /retomar` depois de `kalman-ordens quarentena resolver` | sim |
| `PROTECAO_RESERVA_REPLACES` | (v1.13) a 1014 chegou a `REPLACES_1014_MAX − REPLACES_1014_RESERVA` replaces (MaxReplaces 20771 do PnT, padrão 100): ajustes do stop recusados daqui em diante (`PROTECAO_LIMITE_REPLACES`); o trailing segue pelo TRSP do gestor (saída por replace, na reserva) | não |
| `FONTE_DEGRADADA` | (v1.14) fonte de preço ruim por mais de `FONTE_DEGRADADA_S` (30) dentro de `FONTE_JANELA` (09:00-18:25): sem resposta, `{}` (no pnt-md-hml: book degenerado ou atraso acima da base), 404/400; em negociação, sem bid/ask, book cruzado ou preço mais velho que `PRECO_IDADE_MAX_S`; leilão com preço não conta. Motivo da própria fonte com `PRECOS_SAUDE_URL` (o `/saude` do pnt-md-hml). No `/saude`: `fonte`. Resolve quando a fonte volta ou a janela fecha | sim |
| `TUNNEL_RECUSA_ENTRADA` | (v1.13) entrada recusada pelo PnT com "tunnel" no 58: na primeira recusa falsa, ligar `IGNORE_TUNNEL_ENTRADA` | não |
| `CONCILIACAO` | resultado da conciliação do logon (ordens KM, ignoradas, divergências, posição); o mass status vazio (35=j 380=101 "No orders found.") é só registrado (v1.3 E2) | não |
| `MASS_STATUS_REJEITADO` | mass status rejeitado por motivo diferente de 380=4; entradas bloqueadas | sim |
| `DESCONHECIDA` | ordem viva na tabela e ausente no mass status | não |
| `DIVERGENTE` | inconsistência de posição ou de ordem não resolvida; BLOQUEAR | sim |
| `SEM_CONFIRMACAO` | ordem em ENVIANDO sem nenhuma resposta do PnT em `PRAZO_CONFIRMACAO_S` | sim |
| `PENDENTE_39A` | ordem em 39=A por mais de 3 s | não |
| `RECUSA_PNT` | ordem, cancel ou replace rejeitado pelo PnT (35=8 39=8, 35=9, 35=3, 35=j) | não |
| `CANCEL_STOP_REJEITADO` | cancel da 1014 rejeitado (35=9): aguardando fills do stop | não |
| `CANCEL_SEM_RESPOSTA` | cancel enviado sem 39=6 nem 39=4 em `ESPERA_STOP_S`, ou com 39=6 e sem o 39=4 em `ESPERA_ENCERRAMENTO_S` depois dele (v1.3 F2) | sim |
| `SEM_PROTECAO` | posição sem 1014 aceita por mais de 2 s | sim |
| `PROTECAO_REJEITADA` | 1014 rejeitada (1ª vez: nova tentativa; 2ª: zera a posição) | não |
| `PROTECAO_RECRIADA` | 1014 recriada com o mesmo gatilho e limite: cancelada/expirada pelo PnT sem pedido nosso (na hora), recriada rejeitada, ou invariante violada (posição ≠ 0 sem 1014 e sem saída viva por mais de 2 s) | sim |
| `PROTECAO_INSTAVEL` | 1014 falhou de novo depois de 2 recriações em menos de 30 s: a posição é zerada | sim |
| `STOP_FURADO` | a 1014 disparou no PnT (39=B com 20807>0) e nenhum fill chegou em `ESPERA_STOP_S` (v1.3 J2); reserva: último além do limite da 1014 por mais de `ESPERA_STOP_S` com posição | sim |
| `SAIDA_ESGOTOU` | saída esgotou `MAX_REPRECO_SAIDA` e continua reapregoando | sim |
| `SAIDA_SEM_FILL` | o PnT recusou o replace da saída dizendo que a ordem já está terminal (35=9 com 39=2/4/8), não veio fill nem 39=4 em `ESPERA_FILL_POS_RECUSA_S` e o mass status não mostrou um estado coerente (a ordem viva ou ausente): a saída fica suspensa, decisão humana; resolve quando a ordem encerra ou a posição zera (v1.3 I1) | sim |
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
| `PNT_BOLSA_INDISPONIVEL` | disjuntor de bolsa indisponível aberto, ou a bolsa voltou há menos de `DISJUNTOR_ESTAVEL_S` sem rejeição (v1.10 §9) | não | — |
| `CONCILIANDO` | conciliação do logon em andamento | não | — |
| `SEM_CONFIRMACAO` | há ordem sem confirmação do PnT | não | — |
| `SIMBOLO_DIVERGENTE` | SAIDA: a posição está num contrato que não é mais o vigente e o último preço dele tem mais de 60 s (a entrada nunca diverge: vai no vigente) | não | `SAIDA_BLOQUEADA_PRECO_ANTIGO` |
| `FORA_JANELA` | fora de 09:00–14:00 | sim | — |
| `QTD_MAXIMA` | qtd > limite por ordem | sim | — |
| `POSICAO_MAXIMA` | posição + entradas pendentes + qtd > máximo (global: soma de todos os usuários, `LIMITE_POSICAO_MAX`; ou o do uid na habilitação) | sim | — |
| `INTERVALO_MINIMO` | menos de `LIMITE_INTERVALO_ENTRADAS_S` desde a última entrada de qualquer usuário (global; padrão 0 = desligado) | sim | — |
| `ENTRADAS_POR_MINUTO` | `LIMITE_ENTRADAS_MIN` entradas no último minuto, somando todos os usuários (global) | sim | — |
| `ORDENS_POR_MINUTO` | `LIMITE_MENSAGENS_MIN` mensagens ao PnT no último minuto (global); recusa só entradas, nunca saídas | sim | `LIMITE` |
| `ENTRADAS_POR_DIA` | `LIMITE_ENTRADAS_DIA` entradas no dia (global) | sim | `LIMITE` |
| `ENTRADAS_POR_MINUTO_UID` | o uid já teve `LIMITE_ENTRADAS_MIN_UID` entradas no último minuto (v1.10 L; não vale na virada de mão) | não | — |
| `INTERVALO_MINIMO_UID` | menos de `LIMITE_INTERVALO_UID_S` desde a última entrada do uid no mesmo sentido (v1.10 L) | não | — |
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

## Códigos de recusa do `POST /operacoes/{id}/stop` (ajuste da proteção, SPEC v1.4 K)

Nenhum conta para a parada automática; nenhum gera alerta.

| Código | Quando |
|---|---|
| `CAMPO_INVALIDO` | nem `preco_stop` nem `stop_pts`, ou os dois; `preco_stop` ≤ 0; `uid` diferente do da operação; `modulo` inválido |
| `SEM_POSICAO` | operação inexistente (no módulo), encerrada ou com posição zero |
| `SEM_PROTECAO` | operação em saída, ou sem 1014 viva |
| `PROTECAO_DISPARADA` | a 1014 já disparou no PnT (39=B com 20807>0): quem decide são os fills |
| `PROTECAO_LIMITE_REPLACES` | (v1.13) a 1014 já teve `REPLACES_1014_MAX − REPLACES_1014_RESERVA` replaces (95 de 100): a reserva fica para a saída por replace; o trailing segue pelo TRSP do gestor |
| `SEM_PRECO` | sem bid/ask do símbolo da posição com até `PRECO_IDADE_MAX_S` para validar |
| `STOP_ALEM_DO_PRECO` | comprado com stop >= bid, ou vendido com stop <= ask (depois do arredondamento ao tick): a 1014 dispararia na hora; quem sai é o gestor |
| `RESULTADO_DESCONHECIDO` | (HTTP 503) motor não respondeu a tempo; o pedido segue; reenviar o mesmo é inofensivo |

Aceito: `situacao` = `ENVIADO` (35=G enviado), `AGUARDANDO` (sai quando o pedido em curso da 1014
responder; só o último), `IGNORADO` (menos de `AJUSTE_DEGRAU_MIN_PTS` do que está no PnT).

Precedência na ENTRADA: habilitação → campos → DUPLICADA → PARADA → DIVERGENTE → PNT_BOLSA_INDISPONIVEL → SEM_SESSAO →
SEM_CONFIRMACAO → CONCILIANDO → conta e destino (acusuario) → SIMBOLO_INVALIDO → limites (globais
e do uid) → preço. A SAIDA não olha habilitação nem a conta atual do GQ: usa o que a entrada gravou.

## `POST /sinais` (lote por sinal, SPEC v1.10 §6)

Cada item tem o resultado do `POST /ordens` do papel (os códigos acima, mais `CAMPO_INVALIDO` para
campo desconhecido no item, e `repetido: true` para o item igual a um já recebido). O lote:

| Código | Quando |
|---|---|
| `LOTE_ACEITO` | aceito (200): a parte foi tratada; o resultado de cada item em `resultados` |
| `CAMPO_INVALIDO` | (400) JSON inválido ou com campo desconhecido; `sinal_id` ausente ou fora de `[A-Za-z0-9._:-]{1,64}`; `parte`/`partes` ausentes, `parte` fora de 1..`partes`, `partes` diferente do da primeira parte; nenhum item; `modulo` inválido |
| `LIMITE_LOTE` | (400) mais de `SINAL_MAX_ITENS_PARTE` (1.000) itens na parte, mais de `SINAL_MAX_PARTES` (10) partes, mais de 10.000 no sinal, corpo acima de 1 MB |
| `SINAL_FECHADO` | (400) parte nova de um sinal já fechado (todas as partes chegaram, ou `SINAL_ESPERA_PARTES_MS` depois da primeira) |
| `DUPLICADA` | (400) mesmo `sinal_id` e `parte` com outro corpo (alerta `DUPLICADA_DIFERENTE`); com o mesmo corpo, a resposta original com `repetido: true` |
| `RESULTADO_DESCONHECIDO` | (HTTP 503) o motor não respondeu a tempo; a parte segue; reenviar o mesmo corpo |

## Códigos de recusa das ações de admin (SPEC v1.7 A)

`POST /operacoes/{id}/encerrar` e `POST /operacoes/{id}/cancelar-entrada`. Nenhum conta para a
parada automática; nenhum gera alerta.

| Código | Quando |
|---|---|
| `CAMPO_INVALIDO` | `motivo` ou `por` ausente; corpo inválido ou com campo desconhecido; `modulo` inválido |
| `OPERACAO_INEXISTENTE` | a operação não existe hoje no módulo |
| `OPERACAO_ENCERRADA` | a operação já está zerada ou encerrada sem entrada |
| `OPERACAO_EM_SAIDA` | a saída já começou (gestor, stop, parada, admin...) |
| `SEM_POSICAO` | encerrar sem posição e sem entrada pendente |
| `SIMBOLO_DIVERGENTE` | encerrar com a posição num contrato que não é mais o vigente e sem preço recente dele |
| `SEM_ENTRADA_PENDENTE` | cancelar-entrada sem entrada viva |
| `COM_POSICAO` | cancelar-entrada com a entrada já executada em parte (usar encerrar) |
| `QUARENTENA_PENDENTE` | (v1.13, `POST /retomar`) há linha na quarentena do banco sem resolução: `kalman-ordens quarentena listar/resolver` antes |
| `RESULTADO_DESCONHECIDO` | (HTTP 503) motor não respondeu a tempo |

Respostas aceitas (`"aceito": true`, com `codigo` e `motivo`, SPEC v1.7 A4):

| Código | Quando |
|---|---|
| `ENCERRANDO` | encerrar aceito: saída iniciada com origem `ADMIN`; a operação fecha com `ZERADA` |
| `CANCELANDO` | cancelar-entrada aceito: cancelamento enviado ao PnT |
| `JA_CANCELANDO` | cancelar-entrada com o cancelamento já em curso: nada de novo |
| `PARADA_ATIVA` | `POST /parada` aceito |
| `RETOMADO` | `POST /retomar` aceito |

## Habilitação do usuário pelo admin (SPEC v1.8 H)

`POST /usuarios/{uid}/habilitacao` → `{aceito, codigo, motivo}`. Nenhum gera alerta.

| Código | Quando |
|---|---|
| `BLOQUEADO` | aceito: o uid passou a não habilitado (entradas novas recusadas; posição, 1014 e saídas seguem) |
| `DESBLOQUEADO` | aceito: o uid voltou a habilitado |
| `JA_BLOQUEADO` | aceito: já não estava habilitado (ou não tem linha); nada gravado |
| `JA_DESBLOQUEADO` | aceito: já estava habilitado; nada gravado |
| `SEM_HABILITACAO_PREVIA` | recusa: `habilitado: true` para uid sem linha (a 1ª habilitação é pela CLI) |
| `CAMPO_INVALIDO` | recusa: `habilitado`, `motivo` ou `por` ausente; campo desconhecido; uid inválido |

## Rejeições e avisos conhecidos do PnT

Códigos e textos vistos nas respostas do PnT (o `[XXX-n]` no 58 de 35=9, 35=8 39=8 e dos avisos
em 150=I), o que significam e o que o motor faz. Todo 35=9 ou 39=8 vira alerta `RECUSA_PNT` com o
texto 58; os avisos são só informativos. Esta lista é para ler os logs e os alertas; código novo
visto na HML ou em produção entra aqui.

| Código | Texto (58) | Em resposta a | Significado | O que o motor faz | Visto em |
|---|---|---|---|---|---|
| `[DSA-40]` | `Robot Order not found` | cancel (35=F) | a ordem não existe mais no PnT (ou o cancel foi sem 37) | 1014 em erro: recria sob a P8 (v1.1 F17, roteiro 34); demais: `RECUSA_PNT` | log de HML do PT, 2025 |
| `[IS-72]` | `Replacing quantity must be bigger than current cumulative quantity. Cumulative Quantity: '1' OrderQty (38): '1'.` (35=9 com `39=2 434=2 102=99`) | replace (35=G) | a quantidade do replace não é maior que a já executada: **a ordem já executou no PnT**; o fill pode chegar depois do 35=9 (na HML, 100 ms), no ClOrdID vigente, não no do replace rejeitado `RECUSA_PNT` informativo com o 58; a ordem fica no ClOrdID vigente. **Na saída, o 39 terminal do 35=9 (2, 4 ou 8) suspende o repreço** e o motor espera o fill ou o 39=4 por `ESPERA_FILL_POS_RECUSA_S`; sem eles, mass status, e sem estado coerente `SAIDA_SEM_FILL` (v1.3 I1). O motor não decide pelo 102 (`99` = outro) | HML 14/10, saída da operação 900000002 (roteiro 51; `cenarios/referencia/saida-is72-hml-2026-10-14.log`) |
| `[DSA-56]` | `Pure status replace was not possible because of tag SenderLocationID (142);` (no mesmo 58, depois do `[IS-72]`) | replace (35=G) | consequência do IS-72: sem quantidade a mudar, o PnT tentou tratar o pedido como replace só de estado e recusou por causa do 142. Não é problema do 142 em si: o replace anterior da mesma ordem, com o mesmo 142, foi aceito | o mesmo do IS-72 (vêm juntos) | HML 14/10, idem |
| `[DSA-122]` | `Waiting for sent orders to be cancelled, before sending Status=Finished. Remainning open qty: 1.` (em `150=I 39=B`, com `20807`/`20806` > 0) | — (aviso) | a 1014 disparou: a ordem filha está no mercado (`20807` = OpenQty, `20806` = OpenPx), e o PnT espera ela terminar antes do estado final. **Informativo** | o primeiro com `20807>0` publica `PROTECAO_DISPARADA` e arma o gatilho do stop furado (sem fill em `ESPERA_STOP_S` → `STOP_FURADO`); os seguintes, nada (v1.3 J2) | HML 14/10, 1014 da operação 900000003 (roteiro 05; `cenarios/referencia/disparo-1014-hml-2026-10-14.log`) |
| — | `Delaying final state 'Finished' for 3.00 s, timer started.` / `… for 9.19 ms / 3.00 s.` (em `150=I 39=B`, `20807=0`) | — (aviso) | depois do fill, o PnT segura o estado final por 3 s; o `150=I 39=2` vem em seguida. **Informativo** | nada: quem decide são os fills (v1.3 J3) | HML 14/10, idem |
