# v1.32 na .53 — instalação (sexta, 14/11, antes das 08:00)

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

**Quando:** antes das 08:00, sem posição nem ordem viva. A ponte fica fora uns segundos.

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
curl -s 127.0.0.1:9092/saude | jq '{versao, flex: .ambientes.SIMULADOR.flexscan | {janela, logado, webhook_esperado, ultimo_login}}'
#   versao "d1bad4358664", janela "08:02-18:30", logado true, webhook_esperado true,
#   ultimo_login.motivo "partida" e resultado "OK"
/opt/kalman-ordens/kalman-ordens boleta enviar -uid 5725 -ambiente REAL -lado C -qtd 1 -stop 300 -por 1 \
  -motivo "123456789012345678901" < /dev/null
#   item 2: "motivo com 21 caracteres (maximo 20): nada enviado" (conferido antes do destino e do
#   "sim"; o REAL, fora de AMBIENTES_PERMITIDOS, recusaria de qualquer jeito)
```

## 4. Na abertura (08:02 a 08:45)

Com o restart antes das 08:00, o login da partida já vale para o dia: às 08:02 a ponte não loga de
novo (às 08:15 vem a renovação da abertura, porque o token da partida não chega às 18:30). Às 08:02,
a conferência pedida pelo login da partida roda e acha o cadastro limpo de madrugada.

O esperado no Telegram: **uma linha só**, `FLEXSCAN_ABERTURA`, e nenhum crítico:

```
[flexscan] abertura: webhook recadastrado (ausente na conferencia depois do login das 07:4x:xx
(partida); a Flexscan limpa os webhooks de madrugada)
```

E no tráfego:

```bash
curl -s '127.0.0.1:9092/flexscan/trafego?limite=30' | jq -r '.mensagens[]
  | select(.tipo=="LOGIN" or .tipo=="WEBHOOK_CADASTRO") | "\(.em) \(.tipo) \(.status_http) \(.duracao_ms) \(.resumo)"'
#   07:4x LOGIN partida OK; 08:02 conferencia ausente, recadastro, conferencia presente;
#   08:15 LOGIN renovacao-abertura OK; 08:30 conferencia presente
```

Se vier `FLEXSCAN_LOGIN` ou `FLEXSCAN_WEBHOOK_AUSENTE` antes das 08:45, me mande o texto: ele diz
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
