# v1.31.1 na .53: o robô e o OMS nas ordens do PnT (sexta, 13/11, durante o pregão)

**Para que serve.** A boleta do REAL pelo PnT (1450, conta 4582351, sessão `GAMMAQ2`) foi recusada
às 09:35:58 com `[DSA-3] Robot not found`. As ordens do Kalman precisam levar `20858=BTG_ALGO` e
`20859=BTGDAYTRADE`, o OMS de day trade (SPEC-ORDENS-v1.31.1). A v1.31.1 é a v1.31 instalada mais só
isso.

**O que muda:**
- **Binário:** o do worktree limpo `../kalman-ordens-v1311`, commit `117b8bfefcd4`. A suíte completa
  passou nele em 13/11 (`var/suite1311-20261113-095901.log`, no df50964: tudo verde menos a contagem de roteiros, 137 → 138, corrigida no 117b8bf; a suíte do 117b8bf em `var/suite1311b-*.log`).
  ```
  sha256  76714e554186b3e11801be04d244883fc74018047259d18bcbc6792ce3d3fd59  dist/kalman-ordens
  ```
- **Banco:** nenhuma migração nova (continuam 17). Não há cópia a fazer.
- **`service.env`:** duas linhas novas, `REAL_PNT_ROBOT_ID=BTG_ALGO` e
  `REAL_PNT_ENTERING_FIRM=BTGDAYTRADE`. A v1.31 ignora chaves que não conhece, então a volta do
  binário (§5) não precisa mexer no env.

**Quando:** hoje, sem ordem viva em nenhum ambiente e com o lado REAL do 1450 **desligado** no Kalman.
O restart leva uns segundos.
- A sessão `GAMMAQ2` do kalman-ordens faz logout e loga de novo, continuando a sequência do store
  (`/var/lib/kalman-ordens/fix-real`), sem reset.
- O Kalman perde o `GET /eventos` e volta sozinho.
- A Flexscan do SIMULADOR loga de novo (`partida`).

```bash
K=/opt/kalman-ordens/kalman-ordens
N=/home/gammaquant/kalman-ordens-v1311/dist/kalman-ordens
F=/etc/kalman-ordens/service.env
B=$F.antes-v1311
```

## 1. Antes

```bash
V=$($K versao); echo $V                                      # 93245099bd13
curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos,
  sim: .ambientes.SIMULADOR | {posicao, vivas: .flexscan.ordens_vivas, suspensas: .operacoes_suspensas},
  real: .ambientes.REAL | {posicao, ordens_vivas, suspensas: .operacoes_suspensas, pnt: .canais.PNT.estado}}'
#   posições 0, vivas 0/null, suspensas []; REAL com PNT "NO_AR"
sha256sum $N                                                 # 76714e554186b3e11801be04d244883fc74018047259d18bcbc6792ce3d3fd59
$N versao                                                    # 117b8bfefcd4
journalctl -u kalman --since '-10min' --no-pager | grep -E 'ordens:.*REAL' | tail -3 | cut -c1-160
#   o lado REAL do 1450 desligado (ou nunca ligado hoje)
```

Pare aqui se:
- alguma posição, ordem viva ou operação suspensa não estiver zerada;
- o sha256 ou a versão forem diferentes;
- o lado REAL do 1450 estiver ligado no Kalman.

## 2. As duas linhas no `service.env`

O arquivo novo é montado ao lado, a partir de uma cópia. Se as chaves já existirem, só elas mudam; se
não, entram no fim. Depois de conferir a diferença com `diff`, o conteúdo é copiado por cima do
original. Sem `sed -i`.

```bash
sudo cp -a $F $B
sudo awk -v OFS='' '
BEGIN { v["REAL_PNT_ROBOT_ID"]="BTG_ALGO"; v["REAL_PNT_ENTERING_FIRM"]="BTGDAYTRADE" }
{ k=$0; sub(/=.*/, "", k) }
(k in v) && /^[A-Z_]+=/ { print k, "=", v[k]; feito[k]=1; next }
{ print }
END {
  if (!("REAL_PNT_ROBOT_ID" in feito) || !("REAL_PNT_ENTERING_FIRM" in feito)) {
    print ""; print "# 13/11 (v1.31.1): o robo e o OMS de day trade nas ordens do PnT do REAL ([DSA-3] sem eles)" }
  if (!("REAL_PNT_ROBOT_ID" in feito)) print "REAL_PNT_ROBOT_ID=BTG_ALGO"
  if (!("REAL_PNT_ENTERING_FIRM" in feito)) print "REAL_PNT_ENTERING_FIRM=BTGDAYTRADE"
}' $B | sudo tee $F.novo >/dev/null
sudo chmod 600 $F.novo
sudo diff $B $F.novo | grep -vE 'SENHA|_TOKEN|_SEGREDO'
#   esperado: só o comentário e as duas linhas acrescentadas no fim
sudo cp $F.novo $F && sudo rm $F.novo
sudo grep -n '^REAL_PNT_' $F                                 # as duas
```

Se o `diff` mostrar mais do que isso, não copie: apague com `sudo rm $F.novo` e me mande a saída.

**A cópia do fechamento de hoje continua valendo.** A `service.env.antes-real-pnt-1311` é a do §7.3
do roteiro do REAL e não tem estas duas linhas. Ao voltar o env por ela no fim do teste, as duas saem
junto, o que está certo.

## 3. O binário (guardando o atual)

```bash
sudo cp -a $K $K.$V                                          # kalman-ordens.93245099bd13
sudo install -o root -g root -m 755 $N $K
$K versao                                                    # 117b8bfefcd4
```

## 4. Restart e conferência

```bash
sudo systemctl restart kalman-ordens
sleep 10; systemctl is-active kalman-ordens                  # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|migra|pnt: sessão|logon|sequ|ambientes|no ar|ERROR' | cut -c1-240
#   bom: "kalman-ordens subindo" versao=117b8bfefcd4; "migrações: nenhuma pendente";
#        "pnt: sessão" ambiente=REAL ... robot_id_20858=BTG_ALGO entering_firm_20859=BTGDAYTRADE;
#        o logon da GAMMAQ2 (sem reset); "kalman-ordens no ar"
#   ruim: o serviço não sobe, "migração aplicada" ou logon recusado → §5 e me mande o journal
curl -s 127.0.0.1:9092/saude | jq '{versao, real: .ambientes.REAL | {canais, sessao_logada, posicao, divergente}}'
#   versao "117b8bfefcd4"; PNT "NO_AR"; sessao_logada true; posicao 0; divergente false
```

**De novo a ordem de comunicação.** Siga o §6 do `INSTALACAO-2026-11-13-real-pnt-1450.md`: a boleta
de 1 contrato, 1.000 pontos abaixo do bid, com stop de 300, e depois o cancelamento. Desta vez,
confira o D no fio:

```bash
curl -s '127.0.0.1:9092/sessao/ao-vivo' | jq -r '(.mensagens // .)[]? | select(.direcao=="ENV" and .msgtype=="D") | .fix_raw' | tail -1 \
  | grep -oE '\|(11|847|20856|20858|20859)=[^|]*'
#   |11=KM-BOLETA-E-...  |847=1031  |20856=Y  |20858=BTG_ALGO  |20859=BTGDAYTRADE
curl -s '127.0.0.1:9092/sessao/ao-vivo' | jq -r '(.mensagens // .)[]? | select(.direcao=="REC" and .msgtype=="8") | "\(.campos["11"]) 39=\(.campos["39"]) \(.campos["58"] // "")"' | tail -3
#   bom: 39=0 (aceita; ou A e depois 0); ruim: 39=8 com o 58 → me mande o texto
```

Na BTG: a ordem na conta 4582351, aceita e depois cancelada. Só depois disso, e com o seu ok, o
Kalman entra (§6.2 do roteiro do REAL).

## 5. Como voltar

```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.93245099bd13 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 93245099bd13
```

Sem migração nova, o banco serve às duas versões. As duas `REAL_PNT_*` podem ficar no arquivo, porque
a v1.31 as ignora. Para tirá-las, use `sudo cp $B $F` e reinicie de novo.

Também sem ordem viva e com o lado REAL do 1450 desligado no Kalman.

## 6. Depois (eu)

- conferir a versão e o journal; rodar `scripts/instalado-migracoes.sh` e fazer o commit;
- **a v1.32 de sábado não tem esta correção.** O binário `d1bad4358664` do roteiro de 14/11 sairia
  sem os 20858/20859, e o REAL pelo PnT voltaria ao `[DSA-3]`. Antes de sábado, faço o merge do
  `v1.31.1` no `v1.32` (e no `v1.33`), rodo a suíte de novo e gero um binário novo. O roteiro de
  14/11 passa a apontar para ele;
- o merge do `v1.31.1` no `main` vem junto com o da v1.32.
