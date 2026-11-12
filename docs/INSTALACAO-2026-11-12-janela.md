# A janela da Flexscan às 08:02 na .53 (12/11)

Só o `service.env`, com o binário instalado hoje (v1.31, `93245099bd13`). O binário lê
`FLEXSCAN_JANELA` desde a v1.28. O motivo está na SPEC-ORDENS-v1.32 §4.1: o login das 08:00:00 fica
sem resposta em 3 s, e o das 08:00:30 passa em 180 ms.

**Quando:** fora do pregão e sem ordem viva, com o seu ok: 13/11 antes das 08:00 (decidido em 12/11).
Veja no §4 o que isso muda na conferência do login das 08:02.

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
primeiro pregão que **começa sem restart de manhã**: com o restart depois das 18:30 da véspera, o token
da partida vence de madrugada, a ponte dorme e loga às 08:02. Com a janela em 13/11 de manhã e a v1.32
em 14/11 de manhã, isso é na segunda, 17/11.

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
