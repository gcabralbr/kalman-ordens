# v1.32 na .53 — instalação (sábado, 14/11, sem pregão)

O binário é o do worktree limpo `../kalman-ordens-v132b`, commit `d1bad4358664`. A suíte completa
passou nele em 12/11 (`var/suite132e-20261112-101736.log`).

```
sha256  f74b38746555394987382cbd3923d254b782e0f36017b337352e09ed9f2e269d  dist/kalman-ordens
```

O que entra (SPEC-ORDENS-v1.32):
1. a ponte sem `FLEXSCAN_WEBHOOK_URL` não espera webhook (o REAL/BTG da .53);
2. a CLI da boleta confere o motivo antes do "sim";
3. o `GET /ordens/{id}` de uma operação de outro dia lê o banco (só leitura, `acompanhada: false`);
4. os alertas da abertura da Flexscan: a linha `FLEXSCAN_ABERTURA`, `FLEXSCAN_TOLERANCIA_ABERTURA`
   (08:45) e `(continua desde HH:MM:SS)`.

**Banco:** a v1.32 não tem migração nova (continuam 17; nenhum arquivo de `migracoes/` mudou desde a
v1.31). Por isso não há cópia do banco a fazer antes. O §3 confere no journal: "migrações: nenhuma
pendente". Se aparecer "migração aplicada", pare e me mande o journal.

**`service.env`:** nada a mudar. A `FLEXSCAN_TOLERANCIA_ABERTURA` vale 08:45 sem estar no arquivo, e
a janela das 08:02 já foi trocada em 13/11 (`INSTALACAO-2026-11-12-janela.md`).

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
V=$(/opt/kalman-ordens/kalman-ordens versao); echo $V      # 93245099bd13
curl -s 127.0.0.1:9092/saude | jq '{posicao: .ambientes.SIMULADOR.posicao,
  vivas: .ambientes.SIMULADOR.flexscan.ordens_vivas, janela: .ambientes.SIMULADOR.flexscan.janela,
  suspensas: .ambientes.SIMULADOR.operacoes_suspensas}'
#   esperado: posicao 0, vivas 0, janela "08:02-18:30", suspensas []
sha256sum /home/gammaquant/kalman-ordens-v132b/dist/kalman-ordens
#   f74b38746555394987382cbd3923d254b782e0f36017b337352e09ed9f2e269d
/home/gammaquant/kalman-ordens-v132b/dist/kalman-ordens versao   # d1bad4358664
```

Se a posição, as vivas ou as suspensas não estiverem zeradas, ou se o sha256 for diferente, pare.
A `janela` já deve estar `"08:02-18:30"` (a troca de sexta, 13/11). Se não estiver, pare também.

## 2. Instalar o binário (guardando o atual)

```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.$V
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v132b/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                   # d1bad4358664
```

## 3. Restart e conferência

```bash
sudo systemctl restart kalman-ordens
sleep 8; systemctl is-active kalman-ordens                 # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|migra|no ar|conector ligado|login|ERROR' | cut -c1-220
#   bom: "kalman-ordens subindo" versao=d1bad4358664; "migrações: nenhuma pendente";
#        "flexscan: conector ligado" ... webhook_esperado=true (SIMULADOR); "flexscan: login
#        motivo=partida"; "kalman-ordens no ar"
#   ruim: o serviço não sobe, ou "migração aplicada" → §5 e me mande o journal
curl -s 127.0.0.1:9092/saude | jq '{versao, flex: .ambientes.SIMULADOR.flexscan | {janela, logado, ativa_por, webhook_esperado, ultimo_login}}'
#   versao "d1bad4358664", janela "08:02-18:30", logado true, ativa_por null (sábado: fora da
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

Este é o primeiro pregão sem restart de manhã. O token da partida de sábado venceu no próprio sábado,
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
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.93245099bd13 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                   # 93245099bd13
```

Sem migração nova, o banco serve às duas versões. A v1.31 não lê a `FLEXSCAN_TOLERANCIA_ABERTURA`
(nem está no arquivo).

## 6. Depois (eu)

- conferir a versão e o journal; `scripts/instalado-migracoes.sh` e o commit;
- merge do `v1.32` no `main` e push;
- remover o worktree `../kalman-ordens-v132b` só depois disso.
