# A janela da Flexscan às 08:02 na .53 (12/11)

Só o `service.env`, com o binário instalado hoje (v1.31, `93245099bd13`). O binário lê
`FLEXSCAN_JANELA` desde a v1.28. O motivo está na SPEC-ORDENS-v1.32 §4.1: o login das 08:00:00 fica
sem resposta em 3 s, e o das 08:00:30 passa em 180 ms.

**Quando:** sem ordem viva, com o seu ok. O plano era sexta, 13/11, antes das 08:00. Foi antecipado
para a quinta, 12/11, às 15:37 (no pregão, sem ordem viva nem operação aberta em nenhum módulo; veja
o §6). Instalado assim, o token da partida vence de madrugada e o login das 08:02 da sexta já é o
teste real (§4).

**O que muda:** a ponte passa a acordar e logar às 08:02, não às 08:00. A renovação da abertura
(08:15), a conferência da abertura (08:30) e o fim da janela (18:30) ficam como estão. A chave
também vale para o REAL, que fica pausado, se ele não tiver `REAL_FLEXSCAN_JANELA` própria (o recuo
das chaves da agenda, v1.28).

## 1. Antes

```bash
F=/etc/kalman-ordens/service.env
B=$F.antes-janela-0802
/opt/kalman-ordens/kalman-ordens versao        # 93245099bd13
curl -s 127.0.0.1:9092/saude | jq '{posicao: .ambientes.SIMULADOR.posicao,
  vivas: .ambientes.SIMULADOR.flexscan.ordens_vivas, janela: .ambientes.SIMULADOR.flexscan.janela,
  ativa_por: .ambientes.SIMULADOR.flexscan.ativa_por}'
#   esperado: posicao 0, vivas 0, janela "08:00-18:30", ativa_por null (fora da janela)
sudo cp -a $F $B                               # a cópia, com dono, modo e data
sudo grep -n 'FLEXSCAN_JANELA' $F
#   o que aparecer: nenhuma linha (vale o padrão 08:00-18:30), uma FLEXSCAN_JANELA=...,
#   e talvez uma REAL_FLEXSCAN_JANELA (essa fica como está)
```

Se a posição ou as vivas não forem 0, pare aqui.

## 2. A troca, só da linha `FLEXSCAN_JANELA` (sem `sed -i`)

O arquivo novo é montado ao lado, a partir da cópia. Se a linha existe, só ela muda. Se não existe,
ela entra no fim com um comentário. Depois de conferir a diferença, o conteúdo é copiado por cima
do original com `cp`, que mantém o dono, o modo e o contexto do SELinux do arquivo.

```bash
sudo awk '/^FLEXSCAN_JANELA=/ {print "FLEXSCAN_JANELA=08:02-18:30"; achou=1; next} {print}
  END {if (!achou) {print ""; print "# 12/11: a ponte acorda as 08:02 (o login das 08:00:00 ficava sem resposta em 3 s)"; print "FLEXSCAN_JANELA=08:02-18:30"}}' \
  $B | sudo tee $F.novo >/dev/null
sudo diff $B $F.novo
#   esperado: só a linha da janela (trocada, ou acrescentada com o comentário)
sudo cp $F.novo $F && sudo rm $F.novo
sudo grep -n '^FLEXSCAN_JANELA\|^REAL_FLEXSCAN_JANELA' $F
#   NN:FLEXSCAN_JANELA=08:02-18:30
```

Se o `diff` mostrar mais do que essa linha, não copie: `sudo rm $F.novo` e me mande a saída.

## 3. Restart e conferência

```bash
sudo systemctl restart kalman-ordens
sleep 5
systemctl is-active kalman-ordens               # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'agenda|login|ERROR' | head
#   "flexscan: login motivo=partida" (a partida loga uma vez, mesmo fora da janela)
curl -s 127.0.0.1:9092/saude | jq '.ambientes.SIMULADOR.flexscan | {janela, logado, ativa_por, ultimo_login}'
#   janela "08:02-18:30", logado true, ativa_por null, ultimo_login.motivo "partida" e resultado "OK"
```

Se o serviço não subir, o formato está errado (a agenda é conferida na partida). Volte pelo §5.

## 4. No pregão seguinte

**Atenção ao horário do restart.** A partida sempre loga, mesmo fora da janela, e o token dela vale
10 h. Com o restart **antes das 08:00**, a ponte já está logada às 08:02 e não faz o login das 08:02:
o próximo login é a renovação da abertura, às 08:15. Assim, o primeiro login às 08:02 só aparece no
primeiro pregão que **começa sem restart de manhã**. Um restart a qualquer hora da véspera faz o token
vencer de madrugada: até uma renovação às 18:29 dá um token que vence por volta das 04:30. Aí a ponte
dorme e loga às 08:02. O mesmo vale para um restart num dia sem pregão, porque o token vence no
mesmo dia. Com a janela trocada na quinta, 12/11, às 15:37, o primeiro login às 08:02 é na sexta,
13/11 (§6). Com a v1.32 no sábado, 14/11, o seguinte é na segunda, 16/11.

```bash
curl -s '127.0.0.1:9092/flexscan/trafego?limite=20' | jq -r '.mensagens[]
  | select(.tipo=="LOGIN" or .tipo=="WEBHOOK_CADASTRO") | "\(.em) \(.tipo) \(.status_http) \(.duracao_ms) \(.resumo)"'
```

O esperado com o restart antes das 08:00: o login `partida OK` na hora do restart. Às 08:02, a
conferência pedida por esse login acha o webhook `ausente`, recadastra e confere `presente`. Com a
v1.31 isso ainda manda `FLEXSCAN_WEBHOOK_AUSENTE` e `RESOLVIDO`; com a v1.32 vira a linha
`FLEXSCAN_ABERTURA`. Às 08:15 vem o login `renovacao-abertura OK`. A limpeza é da Flexscan e
continua todo dia.

O esperado num pregão sem restart de manhã: às 08:02:00 o login `reconexao OK`, em menos de 1 s, sem
`FLEXSCAN_LOGIN`; depois, a mesma conferência.

Se o login das 08:02 também falhar, a lentidão não é só no primeiro instante. Nesse caso a janela
pode voltar ao que era, e quem resolve passa a ser a tolerância da v1.32.

## 5. Como voltar

```bash
sudo cp /etc/kalman-ordens/service.env.antes-janela-0802 /etc/kalman-ordens/service.env
sudo systemctl restart kalman-ordens
curl -s 127.0.0.1:9092/saude | jq '.ambientes.SIMULADOR.flexscan.janela'   # "08:00-18:30"
```

Também fora do pregão e sem ordem viva. A cópia `service.env.antes-janela-0802` pode ser apagada
depois de alguns pregões sem problema.

## 6. O que foi feito (12/11)

**A conferência antes, às 15:33:**
- kalman-ordens (SIMULADOR, o único permitido): posição 0, vivas 0, suspensas vazias e nenhuma
  operação no dia;
- Kalman: 0 posição e 0 operação não final, com os lados SIMULADOR desligados desde 14:52;
- sombras: `KALMAN_ORDENS_URL` vazio, então não mandam ordens.

**A instalação, às 15:37:50, ainda com a v1.31:**
- `FLEXSCAN_JANELA=08:02-18:30` entrou na linha 114, acrescentada porque não havia linha antes;
- não havia `REAL_FLEXSCAN_JANELA`;
- a cópia ficou em `service.env.antes-janela-0802`.

**Depois do restart:**
- agenda `ativa por=janela`;
- `LOGIN partida OK` às 15:37:51, com 500 ms;
- a conferência logo em seguida deu `presente`, em 12 ms;
- o token vence à 01:37 de 13/11;
- o Kalman perdeu o `GET /eventos` por uns segundos e voltou sozinho;
- a única linha no Telegram foi a `CONCILIACAO` da partida (0 ordens e posição 0).

**O esperado para sexta, 13/11, ainda com a v1.31:**
- à 01:37, a ponte dorme sem alerta ("flexscan: sem sessão até a abertura");
- às 08:02:00, `LOGIN reconexao OK` em menos de 1 s, **sem** `FLEXSCAN_LOGIN`;
- logo depois, a conferência acha o webhook `ausente` (a limpeza das ~03:45), recadastra e confere
  `presente`. A v1.31 ainda manda `FLEXSCAN_WEBHOOK_AUSENTE` + `RESOLVIDO`;
- às 08:15 não há renovação, porque o login das 08:02 conta como o da abertura;
- às 08:30, a conferência da abertura dá `presente`.

**Depois:**
- a v1.32 entra no sábado, 14/11 (`INSTALACAO-2026-11-14-v1.32.md`);
- segunda, 16/11, é a segunda vez do teste, já com a linha única `FLEXSCAN_ABERTURA`.

**Resultado de 13/11: passou.** Conferido às 08:05.

| hora | o que houve |
|---|---|
| 01:37:51 | "flexscan: sem sessão até a abertura" (token vencido fora da janela), sem alerta |
| 08:02:00.031 | agenda `ativa por=janela` |
| 08:02:00.800 | `LOGIN reconexao OK`, status 200, em 769 ms; token até 18:02 |
| 08:02:00.863 | conferência: `ausente`, em 29 ms (a limpeza da madrugada) |
| 08:02:01.000 | recadastro (`order.all,algo.changed`), em 136 ms |
| 08:02:01.010 | conferência: `presente`, em 10 ms |

No Telegram:
- `FLEXSCAN_WEBHOOK_AUSENTE` às 08:02:01.000 e `RESOLVIDO` às 08:02:01.011, o esperado com a v1.31;
- a `CONCILIACAO` (0 ordens, posição 0) às 08:02:03;
- **nenhum `FLEXSCAN_LOGIN`.**

Nos dias anteriores, o login das 08:00:00 ficava sem resposta em 3 s. O das 08:02:00 passou na
primeira tentativa. O tempo dele, 769 ms, ainda é alto perto dos ~180 ms de um login comum, mas fica
bem dentro dos 3 s.

08:15/08:30: às 08:15 não houve login (o das 08:02 contou como o da abertura). Às 08:30:00 a
conferência da abertura deu `presente`, em 39 ms. Até as 08:33, nenhum alerta depois das 08:03.
