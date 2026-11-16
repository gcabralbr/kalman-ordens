# SPEC — REAL pela tela, fase 1 (uid 1450)

Nov 16, 2026 · @George

## Objetivo e escopo

O REAL passa a ser ligado, aprovado e acompanhado pela tela, sem CLI nem service.env por usuário, a começar pelo uid 1450 na conta 4582351 (PnT/BTG, OMS BTGDAYTRADE). A .53 é o ambiente do REAL nesta fase e é tratada como produção para tudo o que toca o REAL.

Esta SPEC cobre só o que precisa estar pronto antes do 1º dia REAL do 1450:

1. Habilitar o usuário e aprovar a conta dele pela tela do admin, no lugar do `REAL_UIDS` e do `REAL_CONTAS`.
2. Limites por usuário pela tela do admin.
3. A stop que está de fato na corretora visível na tela do usuário.

A liberação segue três etapas: 1450, depois 35 (Ferro), depois beta testers, cada um na própria conta BTG. A etapa do 35 não traz item novo de tela; os itens dos beta testers estão na última seção.

O motor e a decisão de entrada não fazem parte desta SPEC. O critério é a comunicação com a conta real funcionar com segurança.

## Estado atual

Hoje o REAL de um usuário novo só liga por CLI e por env com restart; a tela só bloqueia, zera e encerra. Fonte: levantamentos do CC do ordens (v1.34, 911caf424346) e do CC do Kalman (bac1119) em 16/11.

| Item | Onde se faz hoje |
| --- | --- |
| Habilitar um uid no REAL pela 1ª vez | CLI (`usuario habilitar`) + uid no `REAL_UIDS` do env, com restart |
| Aprovar a conta | `REAL_CONTAS` no env, com restart |
| Limites por uid (`qtd_max`, `posicao_max`) | só CLI |
| Bloquear/desbloquear uid já habilitado | tela do admin |
| Zerar | tela do admin (Parada zerar tudo) ou CLI |
| Encerrar uma operação | tela do usuário e do admin |
| Ligar/desligar o envio do dia | tela do usuário, com confirmação da conta |
| Stop da corretora (1014) | não aparece na tela do usuário |
| Lista de quem está no REAL | só CLI (`usuario listar`) |

O cadastro já existe: conta, corretora e conector de cada usuário vêm da `usutxtconfigprd` (grupo `Robo` do JSON), que o próprio usuário edita na tela "Perfil do usuário – Robô" do GQ (GX). O kalman-ordens lê esse JSON com cache de 30 s e já alerta `CONTA_ALTERADA_COM_POSICAO`.

## Decisões

Decidido com o George em 16/11.

| # | Decisão |
| --- | --- |
| D1 | O REAL opera pela .53. A .51 fica fora desta fase. |
| D2 | Tudo o que é por usuário se faz pela tela. No env ficam só o interruptor geral (`AMBIENTES_PERMITIDOS`) e os limites da instância (`REAL_LIMITE_*`). |
| D3 | A habilitação no REAL exige aprovação do admin, gravada por usuário com a conta, a corretora e o conector aprovados. |
| D4 | `REAL_UIDS` e `REAL_CONTAS` deixam de existir. Uma só fonte de verdade: a aprovação. |
| D5 | Nenhuma habilitação antiga vale. O 1450 é aprovado de novo pela tela; o 5725 (REAL Flexscan desde 03/11) deixa de ser candidato. |
| D6 | Se o cadastro do GQ deixar de bater com o aprovado, o REAL do usuário para de aceitar entradas até nova aprovação. |
| D7 | Limites por usuário na aprovação: contratos por ordem 1 e posição máxima 1 nesta fase. A perda diária por usuário segue a do perfil no Kalman. |
| D8 | A tela do usuário mostra a stop que está na corretora, além da stop do gestor. |
| D9 | O Kalman é day trade e zera no mesmo dia. Day Trade × Position é só o OMS (20859) e fica para a etapa dos beta testers. |

## Fluxo de habilitação

O usuário cadastra a conta no GQ, o admin aprova no kalman-admin e o usuário liga o envio do dia na tela dele.

1. O usuário preenche corretora, conta e conector no GQ, como hoje.
2. O admin abre a ficha do usuário no kalman-admin, vê o que está no GQ e clica em "Aprovar REAL", com motivo obrigatório.
3. A aprovação grava, por uid: conector, corretora, conta, limites, quem aprovou, quando e o motivo. Cada mudança entra num histórico; nada é apagado.
4. O usuário liga "Enviar ordens" na tela dele, com a confirmação da conta, como já funciona. A ativação continua valendo só para o dia.

Estados da aprovação no REAL:

| Estado | Novas entradas | Posição aberta | Como sai do estado |
| --- | --- | --- | --- |
| SEM\_APROVACAO | recusa | não se aplica | admin aprova |
| APROVADO | aceita, se o usuário ligou o envio | gerida normalmente | admin bloqueia ou revoga; troca de cadastro no GQ |
| BLOQUEADO | recusa | gerida até sair (1014 e saídas seguem) | admin desbloqueia |
| CONTA\_DIVERGENTE | recusa | gerida até sair, na conta da operação | admin aprova o novo cadastro, ou o usuário volta ao aprovado |
| REVOGADO | recusa | gerida até sair | só nova aprovação |

Regras:

- Bloquear, revogar e aprovar pedem motivo e ficam no histórico.
- Revogar ou bloquear não zera. Para zerar, o admin usa o encerrar da operação ou a Parada, como hoje.
- Só quem já tem perfil de admin no kalman-admin vê e usa esses botões.
- Uma recusa por estado aparece na tela do usuário com o motivo em português claro ("REAL não aprovado", "conta alterada, aguardando aprovação").

## Troca de conta no GQ

A conta que vale para mandar ordem é sempre a aprovada; o cadastro do GQ só é comparado com ela, nunca usado no lugar dela.

- O kalman-ordens compara conector, corretora e conta da `usutxtconfigprd` com os aprovados a cada entrada e a cada leitura do cache (30 s).
- Diferença em qualquer um dos três: o uid vai para CONTA\_DIVERGENTE, sai alerta crítico no Telegram (uid, aprovado × atual, se há posição) e a ficha do admin mostra os dois lados.
- Posição aberta continua na conta em que entrou: a 1014, os ajustes e a saída vão para a conta gravada na operação, nunca para a do cadastro novo.
- Se o usuário voltar o cadastro ao aprovado, o estado volta a APROVADO sozinho. Qualquer outro caminho pede nova aprovação do admin.
- O alerta `CONTA_ALTERADA_COM_POSICAO` atual é incorporado a este, sem duplicar mensagens.

Pergunta para o CC do ordens: hoje as ordens de uma operação aberta já usam a conta gravada nela, ou releem o cadastro? Se releem, isso precisa mudar nesta entrega.

## Limites e env

Por usuário, o admin define na aprovação contratos por ordem e posição máxima (1 e 1 nesta fase); o usuário não altera. O Kalman continua mandando 1 contrato fixo no REAL, então o limite é a segunda barreira.

Mudar um limite é uma nova versão da aprovação: pede motivo, entra no histórico e vale na próxima entrada, sem restart.

No env do kalman-ordens ficam só travas da máquina:

| Chave | Papel | Valor nesta fase |
| --- | --- | --- |
| `AMBIENTES_PERMITIDOS` | interruptor geral do REAL | REAL incluído durante o período; tirar é decisão de admin, fora do pregão |
| `REAL_LIMITE_QTD_ORDEM` | contratos por ordem, instância | 1 |
| `REAL_LIMITE_POSICAO_MAX` | posição máxima, instância | 1 por usuário aprovado (1 hoje) |
| `REAL_LIMITE_PERDA_DIA_PTS` | perda do dia, instância | 600 (proposta: acima dos 500 do perfil do 1450) |
| `REAL_LIMITE_ENTRADAS_DIA` | entradas no dia, instância | a definir com o histórico do 1450 |
| `REAL_LIMITE_ENTRADAS_MIN` | entradas por minuto, instância | a definir com o histórico do 1450 |

`REAL_UIDS` e `REAL_CONTAS` saem. Se ainda estiverem no env depois da entrega, o serviço não sobe e diz por quê, para não haver duas fontes de verdade.

Em aberto: com o 35 e os beta testers, os limites da instância passam a somar vários usuários. Revisar os valores a cada etapa.

## Telas

A ficha do usuário no kalman-admin ganha o bloco de aprovação do REAL, e a tela do usuário passa a mostrar a stop que está na corretora.

**Ficha do usuário (kalman-admin), lado REAL**

- Cadastro atual no GQ: conector, corretora, conta.
- Aprovado: conector, corretora, conta, limites, por quem, quando, motivo.
- Estado (tabela da seção Fluxo), com destaque quando for CONTA\_DIVERGENTE mostrando aprovado × atual.
- Botões conforme o estado: Aprovar, Bloquear, Desbloquear, Revogar, Alterar limites. Todos pedem motivo e confirmação.
- Histórico da aprovação, mais recente primeiro.
- O que já existe na ficha continua: posição, PnL do dia, operação viva, SUSPENSA com Confirmar posição, canal e destino.

**Tela do usuário (kalmanops)**

- Ao lado da stop do gestor, um cartão "Stop na corretora" com o preço da 1014 vigente e o estado: VIVA, AJUSTANDO (replace em curso) ou DESPROTEGIDA (posição sem 1014 viva).
- DESPROTEGIDA aparece em vermelho e também gera alerta.
- Se o usuário não está aprovado, está bloqueado ou com conta divergente, o "Enviar ordens" fica desabilitado com o motivo, como já acontece com `pode_enviar=false`.
- A confirmação ao ligar continua: "ordens REAIS na conta X da corretora Y".

Fonte da stop na corretora: o kalman-ordens já guarda o `preco_stop_ajustado` e o piso; a tela lê pelo mesmo caminho da ficha. O CC do Kalman confirma o campo exato.

## Divisão de trabalho e ordem

O backend vem primeiro, no CC do ordens; as telas vêm depois, no CC do Kalman, sobre rotas já instaladas e conferidas. Uma etapa por vez.

1. **CC do ordens: SPEC técnica.** A partir desta, com tabela, rotas, migração e testes. O George confere antes de codar.
2. **CC do ordens: implementação** em branch própria: aprovação, revogação, estados, regra da troca de conta, rota de listagem, fim do `REAL_UIDS`/`REAL_CONTAS`, dados da stop na corretora para a tela.
3. **Instalação do backend** fora do pregão, com roteiro lido antes; comandos com sudo pelo George. Conferir com o REAL fora do `AMBIENTES_PERMITIDOS`.
4. **CC do Kalman: telas** (ficha do admin e cartão da stop), pelo proxy já existente.
5. **Instalação das telas** fora do pregão, mesmo cuidado.
6. **Aceitação** (próxima seção).

Regras do período REAL na .53:

- Nada de deploy, restart ou mudança de parâmetro na principal, no kalman-ordens ou no md durante o pregão.
- A principal só reinicia sem operação REAL aberta e sempre pelo script, nunca com `systemctl restart` direto.
- As sombras V28/V29 seguem, desde que não reiniciem a principal.

## Aceitação e portões do 1º dia REAL

O 1º dia REAL do 1450 só acontece com todos os itens abaixo marcados.

**Pela tela, fora do pregão (sem ordem real)**

- [ ] 1450 sem aprovação: "Enviar ordens" desabilitado com o motivo.
- [ ] Admin aprova o 1450 com conta 4582351 e limites 1/1; histórico mostra quem, quando e motivo.
- [ ] Trocar a conta no GQ: estado vai para CONTA\_DIVERGENTE, alerta chega no Telegram, ficha mostra aprovado × atual. Voltar a conta: estado volta a APROVADO.
- [ ] Bloquear, desbloquear, revogar e reaprovar, cada um com motivo no histórico.
- [ ] 5725 aparece como SEM\_APROVACAO.
- [ ] `REAL_UIDS`/`REAL_CONTAS` no env impedem a subida com mensagem clara.

**No REAL, 1 contrato, com margem alocada**

- [ ] Entrada pela tela do usuário; o cartão "Stop na corretora" mostra a 1014 VIVA com o mesmo preço da BTG.
- [ ] Status após reconexão: com posição aberta, reiniciar o kalman-ordens; a resposta do PnT traz a entrada executada (39=2) e a operação segue certa.
- [ ] Sobrevivência da stop: com o kalman-ordens parado, a 1014 continua viva na BTG.
- [ ] Encerrar pela tela do usuário; posição 0 e nada vivo no /saude e na BTG.
- [ ] Conciliação manual do dia: execuções do fio do REAL × BTG (quantidade, preço, sentido, resultado em pontos).

Cartão de emergência (pela tela): Parada zerar tudo no admin → conferir posição 0 e nada vivo no /saude e na BTG → Revogar o usuário. Se o kalman-ordens estiver fora, cancelar a 1014 e zerar direto na BTG.

## Fora desta SPEC e pendências

**Etapa dos beta testers (SPEC própria depois)**

- Lista de todos os usuários no REAL no admin, com estado de cada um.
- Zerar um usuário pelo admin.
- Day Trade × Position: campo `Robo.TipoOperacao` (`DAYTRADE` ou `POSITION`, ausente = `DAYTRADE`) na tela do GX, feito pelo George; o kalman-ordens traduz para o 20859 (`BTGDAYTRADE` ou `BTGCEDRO`), com o 20858 `BTG_ALGO` igual; a escolha fica gravada na entrada e vale para toda a operação; a confirmação mostra o OMS. Flexscan ignora o campo até a conversa com eles.
- Mensagem clara quando a corretora cancelar a ordem sem motivo (provável falta de margem).

**Perguntas ao PnT/BTG**

- Latência do robô de stop (\~1,2–2 s): é esperada?
- O que habilitar por conta nova na sessão GAMMAQ2, e se a sessão aceita contas de clientes diferentes.
- `BTGCEDRO` com ordem simples de WIN nas estratégias 1031 e 1014 (hoje usado só no L&S multileg).

**Em aberto**

- `REAL_LIMITE_ENTRADAS_DIA` e `REAL_LIMITE_ENTRADAS_MIN`: valores a partir do histórico do 1450.
- O SIMULADOR continua com a habilitação por CLI; decidir depois se segue o mesmo fluxo da tela.
