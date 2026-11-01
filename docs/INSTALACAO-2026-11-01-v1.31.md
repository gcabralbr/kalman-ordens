# v1.31 na .53 — instalação (domingo, 01/11) e o REAL de teste (terça, 03/11)

O binário vem do worktree limpo `../kalman-ordens-v131`. A v1.31 não tem migração nova (continuam 17).

## 0. Instalar o binário (hoje)

```bash
V=$(/opt/kalman-ordens/kalman-ordens versao)          # 208091723f3a (para voltar)
sudo cp /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.$V
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v131/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
```

Faça o restart só depois do §1, assim um restart só cobre o binário e as chaves.

## 1. As chaves REAL_* no `service.env`, com o REAL ainda desligado (hoje)

Com `AMBIENTES_PERMITIDOS=SIM`, as chaves `REAL_*` são **ignoradas**. O journal mostra um aviso
"ignorada: REAL fora de AMBIENTES_PERMITIDOS" por chave. Nada loga na Flexscan de produção até
terça.

**1.1 Antes**, guarde uma cópia e confira que ainda não há chave `REAL_` (se houver, edite à mão em
vez de acrescentar):

```bash
sudo cp -a /etc/kalman-ordens/service.env /etc/kalman-ordens/service.env.antes-v131
sudo grep -n '^AMBIENTES_PERMITIDOS\|^ORDENS_AMBIENTE\|^REAL_' /etc/kalman-ordens/service.env
#   esperado: AMBIENTES_PERMITIDOS=SIM, sem ORDENS_AMBIENTE e sem REAL_
```

**1.2 Acrescentar.** A senha é digitada no terminal, sem aparecer na tela nem ficar no histórico.
O segredo da rota é gerado na hora.

A URL (`https://apidma.flexscan.com.br/v1`) e a bolsa (`XBMF`, herdada do SIMULADOR) foram
confirmadas pelo teste de sexta: o login do `btg_gammaquant` e o `POST /orders` com 202.

```bash
read -rsp 'senha do btg_gammaquant: ' S; echo
SEG=$(openssl rand -base64 33 | tr '+/' '-_' | tr -d '=')
sudo tee -a /etc/kalman-ordens/service.env >/dev/null <<EOF

# ---- v1.31 (01/11): o REAL de teste da .53 — BTG, btg_gammaquant (só do kalman-ordens), conta 4582351.
# Ligado só com AMBIENTES_PERMITIDOS=SIM,REAL (terça); sem cadastrar webhook (D5): fills pela consulta.
REAL_UIDS=5725
REAL_CONTAS=4582351
REAL_LIMITE_QTD_ORDEM=1
REAL_LIMITE_POSICAO_MAX=1
REAL_LIMITE_PERDA_DIA_PTS=300
REAL_LIMITE_ENTRADAS_DIA=10
REAL_FLEXSCAN_CORRETORAS=85:BTG
REAL_FLEXSCAN_BTG_URL=https://apidma.flexscan.com.br/v1
REAL_FLEXSCAN_BTG_USUARIO=btg_gammaquant
REAL_FLEXSCAN_BTG_SENHA=$S
REAL_FLEXSCAN_BTG_CONTAS=4582351
REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO=$SEG
REAL_FLEXSCAN_BTG_WEBHOOK_URL=
EOF
unset S SEG
```

- A senha é gravada como foi digitada. Se ela tiver espaço, aspas ou `\`, ponha a linha entre aspas
  simples à mão (`sudo nano`), porque o `EnvironmentFile` do systemd interpreta esses caracteres.
- `REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO`: a .53 tem o receptor ligado, então a rota da BTG
  (`/flexscan/webhook-real-btg/<segredo>`) precisa dele. Sem `_WEBHOOK_URL`, nada é cadastrado na
  Flexscan, e a rota fica sem uso.

**1.3 Restart e conferência:**

```bash
sudo systemctl restart kalman-ordens
sleep 8; journalctl -u kalman-ordens -n 40 --no-pager
#   bom: "subindo" versao=93245099bd13, "kalman-ordens no ar", os avisos "REAL_... ignorada: REAL fora
#        de AMBIENTES_PERMITIDOS" e nenhum "flexscan: login" do btg_gammaquant
systemctl is-active kalman-ordens
curl -s 127.0.0.1:9092/saude | jq '{versao, ambientes_permitidos}'     # ["SIMULADOR"]
/opt/kalman-ordens/kalman-ordens boleta                                 # mostra os subcomandos
```

Para voltar: o `install` do `kalman-ordens.$V` e o `service.env.antes-v131`, com o restart.

## 2. Habilitar o REAL do 5725 (hoje, com o REAL ainda desligado)

```bash
sudo /opt/kalman-ordens/kalman-ordens usuario habilitar -uid 5725 -ambiente REAL -conectores flexscan -qtd-max 1 -posicao-max 1 -motivo "teste REAL BTG 03/11"
sudo /opt/kalman-ordens/kalman-ordens usuario conferir -uid 5725
#   a habilitação REAL: habilitado, qtd_max 1, posicao_max 1, conectores flexscan
#   a coluna REAL (usutxtconfigprd) tem de resolver para FLEXSCAN / 85 / 4582351; se não resolver,
#   grave no GQ: {"Robo":{"Conector":"FLEXSCAN","CORSeqId":85,"CCONroConta":"4582351"}}
```

A habilitação nasce aqui, mas nada vai para o REAL enquanto `AMBIENTES_PERMITIDOS` for `SIM`.

## 3. Terça, 03/11: o roteiro

Comece depois das 09:15, com o mercado aberto e o preço da Flexscan OK. `K=/opt/kalman-ordens/kalman-ordens`.

**3.1 Ligar o REAL:**

```bash
sudo sed -i 's/^AMBIENTES_PERMITIDOS=.*/AMBIENTES_PERMITIDOS=SIM,REAL/' /etc/kalman-ordens/service.env
sudo grep -n '^AMBIENTES_PERMITIDOS' /etc/kalman-ordens/service.env
sudo systemctl restart kalman-ordens
sleep 8; journalctl -u kalman-ordens -n 40 --no-pager
#   bom: "flexscan: conector ligado ... corretora=BTG", "flexscan: login motivo=partida usuario=btg_gammaquant"
#   ruim: o serviço não sobe (o erro diz a chave) → voltar a SIM (3.6) e me mandar o journal
```

**3.2 Conferências** (nada é enviado):

```bash
curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos, real: .ambientes.REAL | {canais, conciliando, divergente, posicao}}'
curl -s 127.0.0.1:9092/saude | jq '.ambientes.REAL.flexscan_corretoras.BTG | {logado, contas, webhook_cadastrado}'
#   BTG logado true, contas ["4582351"], webhook_cadastrado false (D5); canal FLEXSCAN:BTG NO_AR
curl -s "127.0.0.1:9092/flexscan/trafego?ambiente=REAL&corretora=BTG&limite=10" | jq '.saude.logado, .mensagens[-3:]'
$K boleta destino -uid 5725 -ambiente REAL -conta 4582351 -corretora BTG
#   pode_enviar true, corretora BTG, conta 4582351, rótulo "REAL · Flexscan · BTG 4582351"
curl -s '172.16.110.53:9090/precos/atual?ativo=WINFUT' | jq '.WINFUT | {ticker, bid, ask, status84, idade_seg}'   # a fonte de preço (PRECOS_URL_FLEXSCAN)
```

**3.3 A boleta longe do preço** (uma compra 1.000 pontos abaixo do bid; múltiplo de 5):

```bash
BID=<o bid de agora>; P=$(( (BID - 1000) / 5 * 5 ))
$K boleta enviar -uid 5725 -ambiente REAL -conta 4582351 -corretora BTG -lado C -qtd 1 -stop 300 -preco $P -por 5725 -motivo "teste REAL 03/11: limitada longe do preco"
#   mostra o destino e pede "sim"; a resposta traz aceito true, operacao_id (MMDDHHMMSS) e preco
OP=<operacao_id>
```

**3.4 Estado** (repita algumas vezes durante ~1 min):

```bash
$K boleta estado -op $OP
#   estado ENTRANDO; a ordem E com orderid37 (o id da Flexscan) e preco $P, qtd_exec 0
#   "Conta nao contratada": estado ENCERRADA_SEM_ENTRADA, a ordem REJEITADA e o alerta
#   RECUSA_FLEXSCAN com o texto (a conta ainda não liberada: pare aqui e me avise)
curl -s "127.0.0.1:9092/flexscan/trafego?ambiente=REAL&corretora=BTG&limite=20" | jq '.mensagens[-5:]'
```

Confira também na tela da Flexscan ou do BTG: a ordem na conta 4582351, com o name `KM-BOLETA-E-<OP>-1`.

**3.5 Cancelar:**

```bash
$K boleta cancelar -op $OP -por 5725 -motivo "fim do teste"
#   aceito true, codigo CANCELANDO
$K boleta estado -op $OP
#   em ~10 s: ENCERRADA_SEM_ENTRADA, a ordem CANCELADA (evento ENTRADA_ENCERRADA motivo=CANCELADA)
curl -s 127.0.0.1:9092/saude | jq '.ambientes.REAL.posicao'          # 0
```

Se a ordem executar antes do cancelar (só com o mercado despencando 1.000 pontos), a stop de 300 é
posta sozinha, e o encerramento é `$K boleta encerrar -op $OP -por 5725 -motivo "fim do teste"`.

**3.6 Voltar ao SIMULADOR** (sempre, mesmo se algo falhar no meio):

```bash
curl -s 127.0.0.1:9092/saude | jq '.ambientes.REAL.posicao'          # 0 antes de desligar
sudo sed -i 's/^AMBIENTES_PERMITIDOS=.*/AMBIENTES_PERMITIDOS=SIM/' /etc/kalman-ordens/service.env
sudo systemctl restart kalman-ordens
sleep 8; curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos}'   # ["SIMULADOR"]
```

As chaves `REAL_*` ficam no arquivo, ignoradas, para o próximo teste.
