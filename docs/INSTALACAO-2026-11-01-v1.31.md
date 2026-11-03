# v1.31 na .53 — instalação (domingo, 01/11) e o REAL de teste (terça, 03/11)

> 03/11: §1 e §3 revistos. A .53 tinha `ORDENS_AMBIENTE=HML`, não `AMBIENTES_PERMITIDOS`.

O binário vem do worktree limpo `../kalman-ordens-v131`. A v1.31 não tem migração nova (continuam 17).

## 0. Instalar o binário (hoje)

```bash
V=$(/opt/kalman-ordens/kalman-ordens versao)          # 208091723f3a (para voltar)
sudo cp /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.$V
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v131/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
```

Faça o restart só depois do §1, assim um restart só cobre o binário e as chaves.

## 1. Sair da equivalência HML e pôr as chaves REAL_*, com o REAL ainda desligado

**Revisto em 03/11.** A .53 não tem `AMBIENTES_PERMITIDOS`: tem `ORDENS_AMBIENTE=HML` (linha 38) e
nenhuma `REAL_`. Este § troca o HML pelo equivalente explícito e acrescenta as chaves, tudo num
restart só, ainda só com o SIMULADOR.

**Com `ORDENS_AMBIENTE=HML` as chaves `REAL_*` também ficam ignoradas.** Na equivalência, o serviço
monta uma instância só, a do SIMULADOR, e nunca lê as `REAL_FLEXSCAN_*`
(`internal/config/ambientes.go`, `cmd/kalman-ordens/instancias.go`). Mesmo assim, saia do HML agora,
por três motivos:
- o REAL de hoje exige `AMBIENTES_PERMITIDOS`;
- as duas chaves juntas não sobem ("duas fontes para a mesma coisa");
- assim você confere a troca antes do pregão, sem o REAL junto.

**O equivalente do `ORDENS_AMBIENTE=HML`** (SPEC-AMBIENTES-v1 §3.1) são estas duas linhas, no lugar da
linha 38:

```
AMBIENTES_PERMITIDOS=SIM
AMBIENTE_PADRAO=SIMULADOR
```

O `AMBIENTE_PADRAO=SIMULADOR` mantém o padrão do HML: um pedido que chega sem `ambiente` continua indo
para o SIMULADOR, e não é recusado com `AMBIENTE_AUSENTE`.

**O que muda junto.** Nenhuma outra chave sai nem entra.

| | HML (hoje) | `SIM` + `AMBIENTE_PADRAO=SIMULADOR` |
|---|---|---|
| `ORDENS_CONTAS_HML`, `FLEXSCAN_CONTAS_HML` | a trava das contas de simulador | **iguais**: continuam a trava (não mexer) |
| `FLEXSCAN_*` do simulador, `LIMITE_*`, preços (9090, `PNT_DESLIGADO=1`) | | **iguais** |
| **5725 no SIMULADOR** (`usutxtconfig`, Flexscan, conta do `FLEXSCAN_CONTAS_HML`) | | **igual**: a coluna, o conector, a conta, os limites. É conferido no 1.4 |
| 1450 (`usutxtconfig` no PnT, 99999) | recusado: `CONECTOR_DESLIGADO` (o PnT desligado desde 28/10) | recusado com outro código: `CONECTOR_AMBIENTE` ("o PnT só opera no REAL"). Decisão de 30/10 (SPEC-AMBIENTES §0.1) |
| `FIX_*` sem prefixo | usadas só se o PnT ligasse | ignoradas, com um aviso no journal por chave ("o PnT só opera no REAL"). Pode deixar |
| `GET /saude`, campo `ambiente` | `"HML"` | `""`. O Kalman não usa (lê `ambientes`) |
| bancada da HML (`teste-posicao`) | disponível | `SO_HML` (sem uso com o PnT desligado) |

**1.1 Antes**, guarde uma cópia e o retrato do 5725 no SIMULADOR, para comparar depois:

```bash
sudo cp -a /etc/kalman-ordens/service.env /etc/kalman-ordens/service.env.antes-v131
sudo grep -n '^AMBIENTES_PERMITIDOS\|^AMBIENTE_PADRAO\|^ORDENS_AMBIENTE\|^REAL_' /etc/kalman-ordens/service.env
#   esperado: só  38:ORDENS_AMBIENTE=HML
/opt/kalman-ordens/kalman-ordens versao                                  # 93245099bd13 (o §0 feito)
curl -s 127.0.0.1:9092/usuarios/5725/destino | jq -S '.ambientes.SIMULADOR' > ~/5725-sim-antes.json
curl -s 127.0.0.1:9092/usuarios/1450/destino | jq -S '.ambientes.SIMULADOR' > ~/1450-sim-antes.json
curl -s 127.0.0.1:9092/saude | jq '{ambiente, ambientes_permitidos}'     # "HML", ["SIMULADOR"]
```

**1.2 Sair do HML** (a linha 38 vira as duas):

```bash
sudo sed -i 's/^ORDENS_AMBIENTE=HML$/AMBIENTES_PERMITIDOS=SIM\nAMBIENTE_PADRAO=SIMULADOR/' /etc/kalman-ordens/service.env
sudo grep -n '^AMBIENTES_PERMITIDOS\|^AMBIENTE_PADRAO\|^ORDENS_AMBIENTE' /etc/kalman-ordens/service.env
#   38:AMBIENTES_PERMITIDOS=SIM
#   39:AMBIENTE_PADRAO=SIMULADOR        (e nenhum ORDENS_AMBIENTE)
```

**1.3 Acrescentar as chaves REAL_*.** A senha é digitada no terminal, sem aparecer na tela nem ficar
no histórico. O segredo da rota é gerado na hora.

A URL (`https://apidma.flexscan.com.br/v1`) e a bolsa (`XBMF`, herdada do SIMULADOR) foram
confirmadas pelo teste de sexta: o login do `btg_gammaquant` e o `POST /orders` com 202.

```bash
read -rsp 'senha do btg_gammaquant: ' S; echo
SEG=$(openssl rand -base64 33 | tr '+/' '-_' | tr -d '=')
sudo tee -a /etc/kalman-ordens/service.env >/dev/null <<EOF

# ---- v1.31 (03/11): o REAL de teste da .53 — BTG, btg_gammaquant (só do kalman-ordens), conta 4582351.
# Ligado só com AMBIENTES_PERMITIDOS=SIM,REAL; sem cadastrar webhook (D5): fills pela consulta.
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
sudo grep -c '^REAL_' /etc/kalman-ordens/service.env                    # 13
```

- A senha é gravada como foi digitada. Se ela tiver espaço, aspas ou `\`, ponha a linha entre aspas
  simples à mão (`sudo nano`), porque o `EnvironmentFile` do systemd interpreta esses caracteres.
- `REAL_FLEXSCAN_BTG_WEBHOOK_SEGREDO`: a .53 tem o receptor ligado, então a rota da BTG
  (`/flexscan/webhook-real-btg/<segredo>`) precisa dele. Sem `_WEBHOOK_URL`, nada é cadastrado na
  Flexscan, e a rota fica sem uso.

**1.4 Restart e conferência** (antes das 09:00; o SIMULADOR fica fora uns segundos):

```bash
sudo systemctl restart kalman-ordens
sleep 8; journalctl -u kalman-ordens -n 60 --no-pager
#   bom: "subindo" versao=93245099bd13; "ambientes" permitidos=[SIMULADOR] padrao=SIMULADOR
#        ordens_ambiente=""; 6 avisos "REAL_... ignorada: REAL fora de AMBIENTES_PERMITIDOS"
#        (UIDS, CONTAS e os 4 limites; as REAL_FLEXSCAN_* não avisam); os avisos "FIX_... ignorada:
#        o PnT só opera no REAL"; "kalman-ordens no ar"; nenhum "flexscan: login" do btg_gammaquant
#   ruim: o serviço não sobe → 1.5 (voltar) e me mandar o journal
systemctl is-active kalman-ordens
curl -s 127.0.0.1:9092/saude | jq '{versao, ambiente, ambientes_permitidos}'   # "", ["SIMULADOR"]
curl -s 127.0.0.1:9092/usuarios/5725/destino | jq -S '.ambientes.SIMULADOR' | diff ~/5725-sim-antes.json - && echo 5725 IGUAL
curl -s 127.0.0.1:9092/usuarios/1450/destino | jq -S '.ambientes.SIMULADOR' | diff ~/1450-sim-antes.json -
#   o 5725 tem de dar "5725 IGUAL" (se não der: 1.5 e me mande o diff)
#   o 1450 (hoje CONECTOR_DESLIGADO, pode_enviar false): pode mudar o codigo, o motivo, o destino e o
#   rotulo (CONECTOR_AMBIENTE), mas pode_enviar continua false
/opt/kalman-ordens/kalman-ordens boleta                                 # mostra os subcomandos
```

**1.5 Para voltar** (ao HML de antes, sem as REAL_):

```bash
sudo cp -a /etc/kalman-ordens/service.env.antes-v131 /etc/kalman-ordens/service.env
sudo systemctl restart kalman-ordens
```

Se o problema for o binário, reinstale também o `kalman-ordens.$V` do §0.

## 2. Habilitar o REAL do 5725 (hoje, com o REAL ainda desligado)

```bash
sudo /opt/kalman-ordens/kalman-ordens usuario habilitar -uid 5725 -ambiente REAL -conectores flexscan -qtd-max 1 -posicao-max 1 -motivo "teste REAL BTG 03/11"
sudo /opt/kalman-ordens/kalman-ordens usuario conferir -uid 5725
#   a habilitação REAL: habilitado, qtd_max 1, posicao_max 1, conectores flexscan
#   a coluna REAL (usutxtconfigprd) tem de resolver para FLEXSCAN / 85 / 4582351; se não resolver,
#   grave no GQ: {"Robo":{"Conector":"FLEXSCAN","CORSeqId":85,"CCONroConta":"4582351"}}
```

A habilitação nasce aqui, mas nada vai para o REAL enquanto `AMBIENTES_PERMITIDOS` for `SIM`. O §2 pode ser feito antes ou depois do §1.

## 3. Terça, 03/11: o roteiro

Comece depois das 09:00, com o mercado aberto e o preço da Flexscan OK (`idade_seg` de poucos
segundos no 3.2). Pré-requisitos: o §1 feito (`AMBIENTES_PERMITIDOS=SIM` e `AMBIENTE_PADRAO=SIMULADOR`
no lugar do HML) e o §2. `K=/opt/kalman-ordens/kalman-ordens`.

| | a linha |
|---|---|
| o teste (3.1) | `AMBIENTES_PERMITIDOS=SIM,REAL` |
| a volta (3.6) | `AMBIENTES_PERMITIDOS=SIM` |

O `AMBIENTE_PADRAO=SIMULADOR` fica nas duas: um pedido sem `ambiente` nunca vai para o REAL.

**3.1 Ligar o REAL:**

```bash
sudo sed -i 's/^AMBIENTES_PERMITIDOS=.*/AMBIENTES_PERMITIDOS=SIM,REAL/' /etc/kalman-ordens/service.env
sudo grep -n '^AMBIENTES_PERMITIDOS\|^AMBIENTE_PADRAO\|^ORDENS_AMBIENTE' /etc/kalman-ordens/service.env
#   AMBIENTES_PERMITIDOS=SIM,REAL e AMBIENTE_PADRAO=SIMULADOR (nenhum ORDENS_AMBIENTE)
sudo systemctl restart kalman-ordens
sleep 8; journalctl -u kalman-ordens -n 60 --no-pager
#   bom: "ambientes" permitidos=[SIMULADOR REAL] padrao=SIMULADOR real_uids=[5725] real_contas=[4582351];
#        "flexscan: conector ligado ... corretora=BTG", "flexscan: login motivo=partida usuario=btg_gammaquant"
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
#   RECUSA_FLEXSCAN com o texto. Vem do OMS do BTG: a conta sem o Flexscan contratado no BTG
#   (03/11; SPEC-CADASTRO-v1 §0.1). Pare aqui, vá ao 3.6 e repita o §3 quando o BTG liberar
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
sudo grep -n '^AMBIENTES_PERMITIDOS\|^AMBIENTE_PADRAO' /etc/kalman-ordens/service.env   # SIM e SIMULADOR
sleep 8; curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos}'   # ["SIMULADOR"]
curl -s 127.0.0.1:9092/usuarios/5725/destino | jq -S '.ambientes.SIMULADOR' | diff ~/5725-sim-antes.json - && echo 5725 IGUAL
```

A volta é para o `SIM` explícito, não para o `ORDENS_AMBIENTE=HML`: os dois são equivalentes (§1), e
o HML sai de vez no passo 5 da SPEC-AMBIENTES §8. As chaves `REAL_*` ficam no arquivo, ignoradas, para
o próximo teste. Para voltar ao arquivo de antes de tudo, use o 1.5.
