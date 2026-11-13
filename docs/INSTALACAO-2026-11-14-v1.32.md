# v1.32 (com a v1.31.1) na .53 — instalação (sábado, 14/11, sem pregão)

> **Substituído em 13/11 pelo `INSTALACAO-2026-11-14-v1.34.md`:** no sábado entra direto a v1.34
> (`911caf424346`), que contém a v1.31.1, a v1.32 e a v1.33. Este roteiro não é mais seguido.

O binário é o do worktree limpo `../kalman-ordens-v132c`, commit `02ee0f348001`: a v1.32 com o
merge da v1.31.1 (13/11). A suíte completa passou nele em 13/11
(`var/suite132m-20261113-103322.log`).

```
sha256  c49d718ab48001e10d416912aa10c43c01e7e8365cd684d17a9f0e983a3cbdf1  dist/kalman-ordens
```

O binário anterior, `d1bad4358664` do `../kalman-ordens-v132b`, **não serve mais**. Ele não tem os
20858/20859, e com ele o REAL pelo PnT voltaria a dar `[DSA-3] Robot not found`.

O que entra (SPEC-ORDENS-v1.32):
1. a ponte sem `FLEXSCAN_WEBHOOK_URL` não espera webhook (o REAL/BTG da .53);
2. a CLI da boleta confere o motivo antes do "sim";
3. o `GET /ordens/{id}` de uma operação de outro dia lê o banco (só leitura, `acompanhada: false`);
4. os alertas da abertura da Flexscan: a linha `FLEXSCAN_ABERTURA`, `FLEXSCAN_TOLERANCIA_ABERTURA`
   (08:45) e `(continua desde HH:MM:SS)`;
5. a v1.31.1 (SPEC-ORDENS-v1.31.1, §6 da v1.32): o robô (20858) e o OMS (20859) da DSA nas ordens do
   PnT, pelas `REAL_PNT_ROBOT_ID` e `REAL_PNT_ENTERING_FIRM`.

**Banco:** a v1.32 não tem migração nova (continuam 17; nenhum arquivo de `migracoes/` mudou desde a
v1.31). Por isso não há cópia do banco a fazer antes. O §3 confere no journal: "migrações: nenhuma
pendente". Se aparecer "migração aplicada", pare e me mande o journal.

**`service.env`:** nada a mudar.
- A `FLEXSCAN_TOLERANCIA_ABERTURA` vale 08:45 sem estar no arquivo.
- A janela das 08:02 já foi trocada em 12/11, às 15:37 (`INSTALACAO-2026-11-12-janela.md`).
- O REAL está desligado (`AMBIENTES_PERMITIDOS=SIM`) desde o fechamento do teste de 13/11, às 11:42
  (§7 e §8 do `INSTALACAO-2026-11-13-real-pnt-1450.md`). As `REAL_PNT_*` saíram junto. O que volta ao
  env no próximo teste do REAL pelo PnT está no §9.3 daquele roteiro.

**Quando:** sábado, 14/11, a qualquer hora (decidido em 12/11). O sábado não é dia de pregão, e
isso muda o que dá para ver no dia:
- a partida loga mesmo assim (a instalação valida as credenciais na hora). A Flexscan responde no fim
  de semana: na instalação da v1.31, num domingo (01/11, 09:37), o login da partida deu OK;
- fora isso, a ponte não chama a Flexscan. Num dia sem pregão não há janela, renovação da abertura
  nem conferência do webhook. A conferência pedida pelo login da partida fica pendente;
- uns 10 h depois, o token da partida vence e a ponte dorme, sem alerta. No journal aparece
  "flexscan: sem sessão até a abertura";
- a abertura com a v1.32 (o login das 08:02 e a linha `FLEXSCAN_ABERTURA`) só aparece na segunda,
  16/11 (§4).

Mesmo sem pregão, confira que não há posição nem ordem viva.

## 1. Antes

```bash
V=$(/opt/kalman-ordens/kalman-ordens versao); echo $V      # 117b8bfefcd4 (a v1.31.1 de 13/11; 93245099bd13 se ela não entrou)
curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos, posicao: .ambientes.SIMULADOR.posicao,
  vivas: .ambientes.SIMULADOR.flexscan.ordens_vivas, janela: .ambientes.SIMULADOR.flexscan.janela,
  suspensas: .ambientes.SIMULADOR.operacoes_suspensas}'
#   esperado: ambientes_permitidos ["SIMULADOR"], posicao 0, vivas 0, janela "08:02-18:30", suspensas []
sha256sum /home/gammaquant/kalman-ordens-v132c/dist/kalman-ordens
#   c49d718ab48001e10d416912aa10c43c01e7e8365cd684d17a9f0e983a3cbdf1
/home/gammaquant/kalman-ordens-v132c/dist/kalman-ordens versao   # 02ee0f348001
ps -eo pid,cmd | grep '[f]ix-integration'                 # só para saber: em 13/11 ele ficou parado (só sobe à mão)
```

Pare aqui se:
- a posição, as vivas ou as suspensas não estiverem zeradas;
- o sha256 for diferente;
- o REAL ainda estiver permitido. Nesse caso, o restart subiria a sessão `GAMMAQ2` do kalman-ordens,
  que pode estar em uso pelo fix-integration do GQ: feche antes pelo §7 do roteiro do REAL.
A `janela` já deve estar `"08:02-18:30"` (trocada na quinta, 12/11, às 15:37). Se não estiver, pare também.

## 2. Instalar o binário (guardando o atual)

```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.$V
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v132c/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                   # 02ee0f348001
```

## 3. Restart e conferência

```bash
sudo systemctl restart kalman-ordens
sleep 8; systemctl is-active kalman-ordens                 # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|migra|no ar|conector ligado|login|ERROR' | cut -c1-220
#   bom: "kalman-ordens subindo" versao=02ee0f348001; "migrações: nenhuma pendente";
#        "flexscan: conector ligado" ... webhook_esperado=true (SIMULADOR); "flexscan: login
#        motivo=partida"; "kalman-ordens no ar"
#   ruim: o serviço não sobe, ou "migração aplicada" → §5 e me mande o journal
curl -s 127.0.0.1:9092/saude | jq '{versao, flex: .ambientes.SIMULADOR.flexscan | {janela, logado, ativa_por, webhook_esperado, ultimo_login}}'
#   versao "02ee0f348001", janela "08:02-18:30", logado true, ativa_por null (sábado: fora da
#   janela), webhook_esperado true, ultimo_login.motivo "partida" e resultado "OK"
/opt/kalman-ordens/kalman-ordens boleta enviar -uid 5725 -ambiente REAL -lado C -qtd 1 -stop 300 -por 1 \
  -motivo "123456789012345678901" < /dev/null
#   item 2: "motivo com 21 caracteres (maximo 20): nada enviado" (conferido antes do destino e do
#   "sim"; o REAL, fora de AMBIENTES_PERMITIDOS, recusaria de qualquer jeito)
```

**O login da partida precisa dar OK.** A tolerância da v1.32 só vale num dia de pregão. Se o login
falhar no sábado, sai o `FLEXSCAN_LOGIN` crítico, e a ponte não tenta de novo fora da janela. A
próxima tentativa seria segunda às 08:02, e até lá o crítico se repete no Telegram a cada 5 min. Se
o `ultimo_login` não for `partida`/`OK`, espere um minuto e reinicie de novo
(`sudo systemctl restart kalman-ordens`). Se falhar outra vez, me mande o journal. Voltar à v1.31
não resolve, porque a falha é da Flexscan e não da versão.

No sábado, o tráfego só tem esse login, sem `WEBHOOK_CADASTRO`:

```bash
curl -s '127.0.0.1:9092/flexscan/trafego?limite=10' | jq -r '.mensagens[]
  | select(.tipo=="LOGIN" or .tipo=="WEBHOOK_CADASTRO") | "\(.em) \(.tipo) \(.status_http) \(.duracao_ms) \(.resumo)"'
#   2026-11-14T..:.. LOGIN 200 <ms> partida OK      (e nada mais)
```

## 4. Segunda, 16/11, das 08:02 às 08:45: o teste real

O primeiro teste real foi na sexta, 13/11, ainda com a v1.31 (janela trocada na véspera à tarde; o
resultado está no §6 do roteiro da janela). A segunda é o primeiro pregão sem restart de manhã com a v1.32. O token da partida de sábado venceu no próprio sábado,
então a ponte passa o fim de semana dormindo. Às 08:02:00 ela acorda e faz o login `reconexao`. É o
teste da janela nova: a Flexscan responde rápido às 08:02?

Esse login cai a partir do início da janela e por isso conta como o da abertura. Às 08:15 não há
renovação: o token das 08:02 é renovado perto do vencimento, no fim da tarde. A conferência pendente
desde sábado roda logo depois do login, já com o motivo "depois do login das 08:02:0x (reconexao)".

O esperado no Telegram: **uma linha só**, `FLEXSCAN_ABERTURA`, e nenhum crítico:

```
[flexscan] abertura: webhook recadastrado (ausente na conferencia depois do login das 08:02:0x
(reconexao); a Flexscan limpa os webhooks de madrugada)
```

E no tráfego:

```bash
curl -s '127.0.0.1:9092/flexscan/trafego?limite=30' | jq -r '.mensagens[]
  | select(.tipo=="LOGIN" or .tipo=="WEBHOOK_CADASTRO") | "\(.em) \(.tipo) \(.status_http) \(.duracao_ms) \(.resumo)"'
#   08:02:00 LOGIN reconexao OK, em menos de 1 s; 08:02 conferencia ausente, recadastro,
#   conferencia presente; 08:30 conferencia presente (a da abertura)
```

Se o login das 08:02:00 ficar sem resposta, a tolerância cuida dele (sem ordem viva, até as 08:45).
Ele não alerta, a ponte tenta de novo a cada `FLEXSCAN_ESPERA_LOGIN_S`, e a mesma linha única passa a
contar o que houve:

```
[flexscan] abertura: logou 08:02:3x na 2a tentativa (a 1a, 08:02:00: ... Client.Timeout ...);
webhook recadastrado (...)
```

Nesse caso, a janela das 08:02 não resolveu a lentidão, e quem a esconde é a tolerância. Me mande a
linha. Se vier `FLEXSCAN_LOGIN` ou `FLEXSCAN_WEBHOOK_AUSENTE` antes das 08:45, me mande o texto: ele diz
qual conferência foi e quantas tentativas houve.

## 5. Como voltar

```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.$V /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                   # o $V do §1 (117b8bfefcd4)
```

A volta é para o binário que estava instalado (o `$V` do §1), de preferência a v1.31.1, porque a
v1.31 (`93245099bd13`) não tem os 20858/20859. Sem migração nova, o banco serve às três versões. A
v1.31 e a v1.31.1 não leem a `FLEXSCAN_TOLERANCIA_ABERTURA` (nem está no arquivo).

## 6. Depois (eu)

- conferir a versão e o journal; `scripts/instalado-migracoes.sh` e o commit;
- merge do `v1.32` (que já tem o `v1.31.1`) no `main` e push;
- remover os worktrees `../kalman-ordens-v132b`, `../kalman-ordens-v132c` e `../kalman-ordens-v1311`
  só depois disso.
