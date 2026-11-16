# v1.35 (REAL pela tela, só o backend) na .53: instalação com o REAL desligado

O roteiro é para o George ler antes de rodar. Os comandos com `sudo` estão separados e numerados,
de **S1** a **S17**, e a lista completa está no §10. Os comandos sem `sudo` (curl e `versao`) só
leem.

**Quando:** hoje à tarde, 16/11, **depois da zeragem das 14:30**, com a conferência do §1 zerada:
posição 0, nada vivo e nada suspenso. O George confirmou que depois das 14:00 nada roda no SIMULADOR
e não há operação aberta. Não pode rodar com o REAL ligado. O motivo está no §0.2.

**Uma janela só, do §1 ao §7.** Os comandos usam as variáveis de shell `$V`, `$DB`, `$T`, `$C` e
`$F`, que só existem na janela do terminal onde foram criadas. Rode tudo, do §1 ao §7, na mesma
janela, sem fechá-la e sem trocar de usuário. Os comandos da volta (§9, S16 e S17) não dependem
dessas variáveis: usam o nome explícito do binário e o caminho da cópia anotado no S7.

**Quanto tempo:** de 30 a 40 minutos ao todo. O serviço fica parado de 2 a 3 minutos, do S6 ao S11
(cópia do banco, binário, 0019 e partida).

## 0. O que entra, quem é afetado e a volta

### 0.1 O que entra

O binário vem do worktree `../kalman-ordens-v135`, commit `09872ead98f2` (branch `v1.35`).

A suíte completa passou nesse commit, com os dois bancos e `-race`:
- quando: 16/11, das 11:22:34 às 11:40:27;
- log: `var/suite135e3b-20261116-112234.log`;
- resultado: FIM rc=0, 21 pacotes.

Depois do `09872ea` só entraram docs: este roteiro e o §7.3 da SPEC técnica. O binário foi gerado
com `scripts/build.sh` no `09872ea`, antes desses commits de docs. **Não gere de novo**, porque
o hash mudaria.

```
sha256  f5e916590d79499b3aa244a9bea50512a704280f08914ed1c8e5eda84b7131a1  dist/kalman-ordens
versao  09872ead98f2
```

O que muda, por cima da v1.34 instalada (`911caf424346`), conforme a SPEC-REAL-PELA-TELA-F1 e a
SPEC técnica:
- **Migração 0019:** a aprovação do REAL passa a ficar na linha REAL da `kalmanordemusuario`
  (estado, conta aprovada, versão), e o histórico ganha `evento` e `estado_antes`. **Nenhuma
  habilitação antiga do REAL vale (D5):** todas as linhas REAL, inclusive as do 1450 e do 5725,
  vão para `SEM_APROVACAO` com `habilitado=0`, e cada uma ganha uma linha `MIGRACAO` no histórico.
  As linhas do SIMULADOR não mudam.
- **`REAL_UIDS`/`REAL_CONTAS` deixam de existir:** se tiverem valor, a v1.35 **não sobe**. Elas saem
  do `service.env` no §2, antes do restart.
- **Rotas novas:**
  - `GET /usuarios/real`;
  - `GET /usuarios/{uid}/real`;
  - `POST /usuarios/{uid}/real/{acao}`;
  - `GET /usuarios/{uid}/stop`;
  - o `stop_corretora` no `GET /ordens/{id}`.

  A rota antiga `POST /usuarios/{uid}/habilitacao?ambiente=REAL` passa a dar
  `409 USE_APROVACAO_REAL`.
- **CLI:**
  - `usuario habilitar/desabilitar -ambiente REAL` são recusados;
  - `usuario revogar` serve só para emergência;
  - o `usuario listar` mostra o estado do REAL.
- **O verificador do cadastro do GQ:** roda na principal a cada 30 s, mas só olha os uids
  `APROVADO`, `BLOQUEADO`, `CONTA_DIVERGENTE` e `REVOGADO`. Com todos em `SEM_APROVACAO`, **não faz
  nada nesta instalação**: nenhum alerta `REAL_*` é esperado.

**O que esta instalação não confere:**
- **A troca de conta do 1450.** Sem as telas, o 1450 fica em `SEM_APROVACAO`, a CLI não aprova e
  o roteiro não grava na `usutxtconfigprd`. Esse teste fica para a instalação das telas do Kalman,
  pelo caminho real (SPEC técnica §7.3, como foi refeita hoje).
- **O aprovar, bloquear e revogar pela rota.** Também ficam para a instalação das telas.

### 0.2 Quem é afetado, e por que depois da zeragem das 14:30

| quem | durante a instalação | depois |
|---|---|---|
| **kalman-ordens, instância SIMULADOR** (a única ligada; Flexscan) | **parada de 2 a 3 min** (S6–S11). As entradas e as ações do Kalman ficam sem resposta, e o cliente (`ordens_reais.py`) reenvia o mesmo corpo em timeout ou 503. Ao subir, há um novo login da Flexscan (`partida`) | igual: as linhas do SIMULADOR e o caminho da Flexscan não mudam (§7) |
| **Principal** (`kalman`) | **não reinicia.** Perde o `GET /eventos` por alguns segundos e volta sozinha, como em 12/11. A tela de admin mostra o serviço fora nesse intervalo | a tela atual continua igual (Registradas, Fio do dia, Operações). **Não use** "bloquear/desbloquear usuário" no REAL: dá `409 USE_APROVACAO_REAL`. Até as telas novas, bloquear o REAL é pela Parada BLOQUEAR |
| **Paper do Ferro** (35) e os demais em papel | não passam pelo kalman-ordens: o papel roda dentro da principal | nada muda. A comparação dos gêmeos 35/1450 (principal em papel × WIN-V29) também não |
| **Sombras WIN-V28/V29** | `KALMAN_ORDENS_URL` vazio: não falam com o kalman-ordens | nada muda |
| **REAL** | já está desligado (`AMBIENTES_PERMITIDOS=SIM`) | 1450, 5725 e qualquer outra linha REAL ficam em `SEM_APROVACAO`. Para religar o REAL, é preciso aprovar pela tela (D5). É o desejado |
| fix-integration do GQ, md (marketdata) | não são tocados | — |

**Por que depois da zeragem das 14:30.** O SIMULADOR é a instância que reinicia. Com operação
aberta, uma entrada, uma stop ou uma saída da Flexscan poderia cair justo nos 2 a 3 minutos parados,
com posição aberta e sem o serviço olhando.

O George confirmou que, depois das 14:00, nada roda no SIMULADOR e não há operação aberta. Depois da
zeragem das 14:30, com o §1 zerado (posição 0, nada vivo e nada suspenso), esse risco não existe,
mesmo com o pregão da B3 ainda aberto. O REAL está desligado, então não há conta real envolvida.
A regra "instalação do backend fora do pregão" da SPEC funcional (passo 3) fica dispensada só nesta
instalação, por decisão do George em 16/11, pelo mesmo motivo.

O único efeito de estar dentro da janela da Flexscan (08:02–18:30) é o relogin da partida, descrito
no §6. Se o §1 não vier zerado, **não instale**: espere zerar ou deixe para depois das 18:45.

### 0.3 A volta para a v1.34: até onde é simples

| até | voltar é | como |
|---|---|---|
| antes do S10 (`migrar`) | trivial | o binário antigo de volta (§9.1); o banco não mudou |
| **depois da 0019** | **ainda simples**: só o binário, sem restaurar a cópia | §9.1. Foi testado hoje, 16/11, num schema de teste já com a 0019, com o binário da v1.34: `migrar` → "nenhuma migração pendente"; `usuario habilitar`/`desabilitar`/`listar` funcionam, porque ela lê e grava pelos nomes das colunas e as colunas novas têm padrão. A v1.34 não exige `REAL_UIDS`/`REAL_CONTAS` com o REAL desligado (`config/ambientes.go:101` da v1.34): o `service.env` do §2 serve às duas versões |
| depois das telas e da 1ª aprovação pela tela | **deixa de ser simples** | a v1.34 não tem as rotas (as telas quebram: 404) e só enxerga o `habilitado`. A aprovação, a conta aprovada e os estados viram letra morta. Religar o REAL na v1.34 exigiria `REAL_UIDS`/`REAL_CONTAS` de volta no env, sem a conferência do cadastro |

**O que a volta depois da 0019 não desfaz:**
- as linhas REAL ficam `habilitado=0`;
- para a v1.34, o REAL do 1450 e do 5725 está desabilitado.

Isso não pesa, porque o REAL já está desligado e religá-lo não faz parte desta janela. Restaurar a
cópia do §3 só seria preciso para desfazer o D5 exatamente, e ela apaga o que foi gravado depois
(§9.2). Só faça isso falando comigo.

Uma garantia de quem volta para a v1.35 depois de uma volta: uma linha REAL habilitada pela CLI da
v1.34 nesse meio-tempo continua `SEM_APROVACAO` na v1.35. A 0019 não roda de novo, e o estado vazio
lê como `SEM_APROVACAO`. Também foi testado hoje.

## 1. Antes (serviço no ar, v1.34)

Sem sudo:

```bash
V=$(/opt/kalman-ordens/kalman-ordens versao); echo $V      # 911caf424346 (a v1.34)
curl -s 127.0.0.1:9092/saude | jq '{versao, ambientes_permitidos, posicao: .ambientes.SIMULADOR.posicao,
  vivas: .ambientes.SIMULADOR.flexscan.ordens_vivas, janela: .ambientes.SIMULADOR.flexscan.janela,
  suspensas: .ambientes.SIMULADOR.operacoes_suspensas}'
#   esperado: ["SIMULADOR"], posicao 0, vivas 0, janela "08:02-18:30", suspensas []
sha256sum /home/gammaquant/kalman-ordens-v135/dist/kalman-ordens
#   f5e916590d79499b3aa244a9bea50512a704280f08914ed1c8e5eda84b7131a1
/home/gammaquant/kalman-ordens-v135/dist/kalman-ordens versao   # 09872ead98f2

# o retrato do SIMULADOR antes, para comparar no §7
for u in 1450 5725 35; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=SIMULADOR" \
  | jq -c '.ambientes.SIMULADOR | {destino, conector, corretora_codigo, conta, habilitado, pode_enviar, codigo}'; done \
  > /tmp/destino-sim-antes-v135.txt; cat /tmp/destino-sim-antes-v135.txt
```

**S1**: o nome do banco. Só o nome aparece, a senha não.
```bash
DB=$(sudo sed -n 's#^DB_DSN=.*)/\([^?"]*\).*#\1#p' /etc/kalman-ordens/service.env | tail -1); echo $DB
```

**S2**: o banco antes. Anote os números.
```bash
sudo mysql "$DB" -e "SELECT COUNT(*), MAX(versao) FROM kalmanordem_migracao;
  SELECT ambiente, COUNT(*), SUM(habilitado) FROM kalmanordemusuario GROUP BY ambiente;
  SELECT uid, habilitado FROM kalmanordemusuario WHERE ambiente='REAL' ORDER BY uid;
  SELECT COUNT(*) FROM kalmanordemusuariohist"
#   18 | 18;  as contagens por ambiente;  as linhas REAL (1450 e 5725, pelo menos);  o total do histórico
```

**S3**: a lista dos usuários pela CLI, guardada para comparar no §7.
```bash
sudo sh -c '/opt/kalman-ordens/kalman-ordens usuario listar > /var/backups/kalman-ordens/usuarios-antes-v135.txt'
sudo grep -c . /var/backups/kalman-ordens/usuarios-antes-v135.txt
```

Pare aqui se:
- a posição, as vivas ou as suspensas não estiverem zeradas;
- o sha256 ou a versão não baterem;
- o REAL estiver em `ambientes_permitidos`;
- o banco não estiver em 18;
- ainda não tiver passado a zeragem das 14:30.

## 2. O `service.env` (com o serviço ainda no ar; vale na partida do §6)

**S4**: a cópia e o que está lá hoje. Os valores de `REAL_UIDS`/`REAL_CONTAS` não aparecem.
```bash
sudo cp -a /etc/kalman-ordens/service.env /etc/kalman-ordens/service.env.antes-v135
sudo stat -c '%a %U:%G' /etc/kalman-ordens/service.env
sudo awk -F= '/^[[:space:]]*(REAL_UIDS|REAL_CONTAS)=/ {print NR": "$1"=(valor oculto)"}
              /^[[:space:]]*(AMBIENTES_PERMITIDOS|REAL_LIMITE_[A-Z_]+)=/ {print NR": "$0}' /etc/kalman-ordens/service.env
```

**S5**: comentar `REAL_UIDS`/`REAL_CONTAS` e deixar os cinco limites do REAL exatamente nos
valores da SPEC funcional e da decisão 6 do §9 da SPEC técnica: `QTD_ORDEM=1`, `POSICAO_MAX=1`,
`PERDA_DIA_PTS=600`, `ENTRADAS_DIA=30` e `ENTRADAS_MIN=2`. Cada um é trocado se existir e
acrescentado se faltar.
```bash
F=/etc/kalman-ordens/service.env
sudo sed -i -E 's/^[[:space:]]*(REAL_UIDS|REAL_CONTAS)=/# v1.35 D4, nao existe mais: &/' $F
for kv in REAL_LIMITE_QTD_ORDEM=1 REAL_LIMITE_POSICAO_MAX=1 REAL_LIMITE_PERDA_DIA_PTS=600 \
          REAL_LIMITE_ENTRADAS_DIA=30 REAL_LIMITE_ENTRADAS_MIN=2; do
  k=${kv%%=*}
  if sudo grep -qE "^[[:space:]]*$k=" $F; then
    sudo sed -i -E "s/^[[:space:]]*$k=.*/$kv/" $F
  else
    echo "$kv" | sudo tee -a $F >/dev/null
  fi
done
sudo stat -c '%a %U:%G' $F                                   # o mesmo do S4
sudo grep -cE '^[[:space:]]*(REAL_UIDS|REAL_CONTAS)=' $F     # 0
sudo grep -E '^(AMBIENTES_PERMITIDOS|REAL_LIMITE_[A-Z_]+)=' $F
#   AMBIENTES_PERMITIDOS=SIM
#   REAL_LIMITE_QTD_ORDEM=1
#   REAL_LIMITE_POSICAO_MAX=1
#   REAL_LIMITE_PERDA_DIA_PTS=600
#   REAL_LIMITE_ENTRADAS_DIA=30
#   REAL_LIMITE_ENTRADAS_MIN=2
#   (cada chave uma vez só; a ordem pode ser outra)
```

Com o REAL desligado, os `REAL_LIMITE_*` só aparecem na ficha, como os limites da instância (§7.1),
e servem de teto para a aprovação futura.

## 3. Parar o serviço e copiar o banco

**S6**: parar.
```bash
sudo systemctl stop kalman-ordens; systemctl is-active kalman-ordens     # inactive
```

**S7**: a cópia de todas as tabelas `kalmanordem*`, com o serviço parado.
```bash
T=$(sudo mysql "$DB" -N -e "SHOW TABLES LIKE 'kalmanordem%'"); echo $T
C=/var/backups/kalman-ordens/antes-0019-$(date +%Y%m%d-%H%M%S).sql.gz
sudo sh -c "mysqldump --single-transaction '$DB' $(echo $T) | gzip > $C"; echo $C
sudo ls -l $C
sudo sh -c "zcat $C | tail -1"                                            # -- Dump completed on ...
sudo sh -c "zcat $C | grep -c 'INSERT INTO .kalmanordemusuario.'"         # > 0
```

**Anote o caminho completo da cópia**, o que o `echo $C` mostrou (por exemplo,
`/var/backups/kalman-ordens/antes-0019-20261116-184712.sql.gz`). O S17 usa esse caminho, e o `$C`
não existe mais numa janela nova.

Sem "Dump completed", **não siga**: volte com `sudo systemctl start kalman-ordens` (a v1.34 está
intacta, e o env do §2 serve a ela) e me mande a saída.

## 4. Instalar o binário (guardando o atual)

**S8**: guardar a v1.34.
```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.911caf424346
/opt/kalman-ordens/kalman-ordens.911caf424346 versao      # 911caf424346
```

**S9**: instalar a v1.35.
```bash
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v135/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                   # 09872ead98f2
```

## 5. A migração 0019

**S10**: migrar e conferir.
```bash
sudo /opt/kalman-ordens/kalman-ordens migrar
#   aplicada: 0019_aprovacao_real.sql
sudo mysql "$DB" -e "SELECT versao, nome FROM kalmanordem_migracao WHERE versao >= 18;
  SELECT ambiente, estado, COUNT(*), SUM(habilitado) FROM kalmanordemusuario GROUP BY ambiente, estado;
  SELECT uid, estado, habilitado, versao, origem FROM kalmanordemusuario WHERE ambiente='REAL' ORDER BY uid;
  SELECT uid, evento, estado_antes, estado, alterado_por FROM kalmanordemusuariohist WHERE evento='MIGRACAO' ORDER BY uid;
  SELECT COUNT(*) FROM kalmanordemusuariohist"
#   18 e 19 (0019_aprovacao_real.sql)
#   SIMULADOR com estado '' e as mesmas contagens do S2; REAL só SEM_APROVACAO, SUM(habilitado)=0
#   cada linha REAL do S2 (1450 e 5725 inclusos): SEM_APROVACAO, habilitado 0, origem MIGRACAO
#   uma linha MIGRACAO por uid REAL, estado_antes HABILITADO_V134 ou DESABILITADO_V134 (o do S2)
#   o total do histórico = o do S2 + o número de linhas REAL
```

Se o `migrar` der erro, **não suba a v1.35**. Vá ao §9.1 e me mande a mensagem.

## 6. Subir e conferir o serviço

**S11**: subir.
```bash
sudo systemctl start kalman-ordens
```

Sem sudo:
```bash
sleep 8; systemctl is-active kalman-ordens                 # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|migra|no ar|conector ligado|login|nao existem|avaliador|ERROR' | cut -c1-220
#   bom: "kalman-ordens subindo" versao=09872ead98f2; "migrações: nenhuma pendente";
#        "flexscan: conector ligado"; "flexscan: login motivo=partida"; "kalman-ordens no ar"
#   ruim: "nao existem mais" (sobrou REAL_UIDS/REAL_CONTAS: o S5 não pegou; a v1.35 não sobe)
#         "avaliador do REAL: ..." (não deve aparecer com todos em SEM_APROVACAO)
curl -s 127.0.0.1:9092/saude | jq '{versao, ambientes_permitidos,
  posicao: .ambientes.SIMULADOR.posicao, suspensas: .ambientes.SIMULADOR.operacoes_suspensas,
  flex: .ambientes.SIMULADOR.flexscan | {janela, logado, ativa_por, webhook_esperado, ultimo_login,
  webhook_conferido_em, webhook_recadastrado_em, webhook_faltas_seguidas, ordens_vivas}}'
#   versao "09872ead98f2", ["SIMULADOR"], posicao 0, suspensas [], janela "08:02-18:30",
#   logado true, ativa_por "janela", ultimo_login partida/OK, ordens_vivas 0,
#   webhook_conferido_em = agora (a conferência pedida pelo login), webhook_faltas_seguidas 0,
#   webhook_recadastrado_em o de hoje de manhã (08:02), não o de agora
```

**O relogin da Flexscan dentro da janela (08:02–18:30).** É o mesmo da instalação da janela, em 12/11
às 15:37: agenda `ativa por=janela`, `LOGIN partida OK` e a conferência do webhook `presente` logo
depois. O cuidado a mais está em dois pontos:
- **Depois das 08:45 não há tolerância.** A `FLEXSCAN_TOLERANCIA_ABERTURA` vale só para a abertura.
  Às 14:30, um login da partida que falhe dá o **crítico `FLEXSCAN_LOGIN` na hora** no Telegram, e
  a ponte tenta de novo sozinha. Sem operação e sem ordem viva, nada fica exposto, mas o SIMULADOR
  fica sem Flexscan até um login dar certo.
- **A conferência do webhook roda logo depois do login.** O webhook foi cadastrado às 08:02 e deve
  estar `presente`, sem recadastro. Se a Flexscan o tiver limpado no meio do dia, sai o crítico
  `FLEXSCAN_WEBHOOK_AUSENTE` com "recadastrado" e, em seguida, o `RESOLVIDO`. Isso não é defeito da
  v1.35, mas me mande o texto.

**O login da partida precisa dar OK.** Se o `ultimo_login` não vier `partida`/`OK`, ou se vier o
`FLEXSCAN_LOGIN`:
1. olhe primeiro se a ponte já se recuperou sozinha: `logado true` no `/saude` um minuto depois, e o
   `RESOLVIDO` do `FLEXSCAN_LOGIN`;
2. se não, reinicie de novo com o **S11b**:
```bash
sudo systemctl restart kalman-ordens
```
Se falhar outra vez, me mande o journal. A falha é da Flexscan, e voltar de versão não resolve: a
v1.34 faria o mesmo login.

**No Telegram:** só a `CONCILIACAO` da partida (0 ordens, posição 0), como em 12/11. Nenhum crítico e
nenhum `REAL_*`.

## 7. A conferência desta instalação (REAL desligado)

### 7.1 A 0019 e as linhas REAL em `SEM_APROVACAO`, pelas rotas (sem sudo, só GET)

```bash
curl -s 127.0.0.1:9092/usuarios/real | jq -c '.usuarios[] | {uid, estado, conta, versao, origem}'
#   uma linha por uid REAL do S2, todas estado SEM_APROVACAO, conta null, origem MIGRACAO
#   (1450 e 5725 inclusos)
for u in 1450 5725; do curl -s "127.0.0.1:9092/usuarios/$u/real" | jq -c '{uid, estado, versao, acoes,
  gq: (.gq | {conector, corretora_codigo, valido}), limites_instancia, posicao: .posicao.instancia,
  hist: [.historico[] | {evento, estado_antes, origem}]}'; done
#   estado SEM_APROVACAO, acoes ["aprovar"], gq.valido true (o cadastro atual do GQ, só leitura),
#   limites_instancia {qtd_ordem 1, posicao_max 1, perda_dia_pts 600, entradas_dia 30,
#   entradas_min 2} (os do S5), posicao DESLIGADA,
#   hist[0] = {MIGRACAO, HABILITADO_V134 ou DESABILITADO_V134, MIGRACAO}
```

### 7.2 A recusa do REAL

```bash
curl -s '127.0.0.1:9092/usuarios/1450/destino?ambiente=REAL' | jq -c '.ambientes.REAL | {pode_enviar, habilitado, codigo, motivo}'
#   pode_enviar false, habilitado false, codigo AMBIENTE_NAO_PERMITIDO
curl -s -o /dev/null -w '%{http_code}\n' 127.0.0.1:9092/usuarios/1450/stop     # 409
curl -s 127.0.0.1:9092/usuarios/1450/stop | jq -c '{codigo, ambiente}'
#   {"codigo":"INSTANCIA_DESLIGADA","ambiente":"REAL"}
```

**Sobre o código `REAL_SEM_APROVACAO`.** Com o REAL fora do `AMBIENTES_PERMITIDOS`, ele **não
aparece** nem no `/destino` nem numa entrada. A verificação 3 (o teto da máquina) vem antes da
habilitação, e o primeiro código ganha: sai `AMBIENTE_NAO_PERMITIDO`. Isso não muda desde a
SPEC-AMBIENTES-v1.

O que esta instalação prova é o estado que gera esse código: `SEM_APROVACAO` em todas as linhas REAL
(§7.1).
- A ligação entre o estado e o código está em `internal/motor/aprovacao.go:58`.
- Ela é provada pelos roteiros 119 e 127 (entrada REAL com o REAL permitido →
  `aceito=false codigo=REAL_SEM_APROVACAO`), na suíte de hoje.
- O código aparece na .53 da primeira vez que o REAL estiver no teto, na instalação das telas ou no
  1º dia.

**S12**: a CLI não aprova. A recusa vem antes de qualquer gravação.
```bash
sudo /opt/kalman-ordens/kalman-ordens usuario habilitar -uid 1450 -ambiente REAL -motivo "teste da instalacao"
#   erro: "usuario habilitar -ambiente REAL: use a tela (kalman-admin); a aprovacao do REAL e por
#   /usuarios/1450/real (...)"; nada gravado
```

**S13**: a lista pela CLI, com o estado do REAL.
```bash
sudo /opt/kalman-ordens/kalman-ordens usuario listar | grep 'ambiente=REAL'
#   cada linha REAL: habilitado=false ... estado=SEM_APROVACAO ... origem=MIGRACAO por=migracao-0019
```

**S14** (opcional, o item 1 do §7.3): a v1.35 recusa `REAL_UIDS`/`REAL_CONTAS`, sem restart. A cópia
do S4 ainda tem as chaves.
```bash
sudo /opt/kalman-ordens/kalman-ordens usuario conferir -uid 1450 -env /etc/kalman-ordens/service.env.antes-v135
#   erro: "configuração dos ambientes: REAL_UIDS/REAL_CONTAS nao existem mais (v1.35,
#   SPEC-REAL-PELA-TELA-F1 D4): ... tire do service.env: REAL_UIDS, REAL_CONTAS"
#   (só as chaves que estavam lá; se o S4 não mostrou nenhuma, pule)
```

### 7.3 O SIMULADOR intacto

```bash
for u in 1450 5725 35; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=SIMULADOR" \
  | jq -c '.ambientes.SIMULADOR | {destino, conector, corretora_codigo, conta, habilitado, pode_enviar, codigo}'; done \
  | diff /tmp/destino-sim-antes-v135.txt - && echo "SIMULADOR igual"
#   SIMULADOR igual
curl -s "127.0.0.1:9092/historico/ordens?dia=$(date +%F)&ambiente=SIMULADOR" | jq -r '.instancia, (.ordens | length)'
#   LIGADA; as ordens de hoje (as mesmas que a tela mostra em Registradas)
curl -s "127.0.0.1:9092/sessao/eventos?dia=$(date +%F)&conector=FLEXSCAN" | jq -r '.[-3:][] | "\(.em) \(.ambiente) \(.tipo)"'
#   as últimas linhas, a mais nova com o login da partida, SIMULADOR
```

**S15**: as linhas do SIMULADOR, iguais às do S3.
```bash
diff <(sudo grep 'ambiente=SIMULADOR' /var/backups/kalman-ordens/usuarios-antes-v135.txt) \
     <(sudo /opt/kalman-ordens/kalman-ordens usuario listar | grep 'ambiente=SIMULADOR') && echo "SIMULADOR igual"
```

**Na tela** (`kalman-admin.html`, como admin, recarregue a página; nada a gravar): Operações do dia,
Registradas, Fio do dia e Sessão como antes, e o seletor com "REAL (desligado)". **Não clique** em
"bloquear/desbloquear usuário" no REAL: dá `409 USE_APROVACAO_REAL`, o esperado até as telas novas.

## 8. Amanhã, 08:02–08:45

A abertura da Flexscan deve ser igual à de hoje: **uma linha só**, `FLEXSCAN_ABERTURA`, sem nenhum
crítico e sem nenhum `REAL_*`. Me mande a linha, ou o texto de qualquer `FLEXSCAN_LOGIN` ou
`FLEXSCAN_WEBHOOK_AUSENTE` antes das 08:45.

## 9. Como voltar

### 9.1 Para a v1.34, só o binário (antes ou depois da 0019)

**S16**:
```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.911caf424346 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens       # (start, se estiver parado)
/opt/kalman-ordens/kalman-ordens versao   # 911caf424346
```

- **O env:** não precisa voltar. A v1.34 sobe sem `REAL_UIDS`/`REAL_CONTAS` com o REAL desligado.
  Se quiser o arquivo exatamente como antes, use
  `sudo cp -a /etc/kalman-ordens/service.env.antes-v135 /etc/kalman-ordens/service.env`.
- **A 0019:** fica aplicada. A v1.34 a ignora (§0.3).
- **O REAL:** as linhas REAL seguem `habilitado=0`, sem efeito com o REAL desligado.

### 9.2 Restaurar a cópia: só falando comigo

Só se a `kalmanordemusuario` ou a `kalmanordemusuariohist` ficarem danificadas, ou se for preciso
desfazer o D5 exatamente.
- O dump tem `DROP TABLE`/`CREATE TABLE` das `kalmanordem*`: **perde tudo o que foi gravado depois do
  S7** (operações, sessão, eventos).
- Antes, volte a v1.34 (S16) com o serviço parado.

**S17**: troque `<CAMINHO-ANOTADO-NO-S7>` pelo caminho completo da cópia. O nome do banco é lido
de novo, como no S1, e só o nome aparece.
```bash
sudo systemctl stop kalman-ordens
sudo ls -l <CAMINHO-ANOTADO-NO-S7>
DB=$(sudo sed -n 's#^DB_DSN=.*)/\([^?"]*\).*#\1#p' /etc/kalman-ordens/service.env | tail -1); echo $DB
sudo sh -c "zcat <CAMINHO-ANOTADO-NO-S7> | mysql '$DB'"
sudo mysql "$DB" -e "SELECT COUNT(*), MAX(versao) FROM kalmanordem_migracao"   # 18 | 18
sudo systemctl start kalman-ordens
```

## 10. Os comandos com sudo, em ordem

| nº | § | o que faz | muda algo? |
|---|---|---|---|
| S1 | 1 | lê o nome do banco no `service.env` | não |
| S2 | 1 | o banco antes (migrações, usuários, histórico) | não |
| S3 | 1 | guarda o `usuario listar` em `/var/backups/kalman-ordens/` | grava um arquivo |
| S4 | 2 | cópia do `service.env` e o que tem de REAL nele (valores das listas ocultos) | grava a cópia |
| S5 | 2 | comenta `REAL_UIDS`/`REAL_CONTAS`; os cinco `REAL_LIMITE_*` em 1, 1, 600, 30 e 2 (troca ou acrescenta) | **muda o env** |
| S6 | 3 | para o serviço | **para o SIMULADOR** |
| S7 | 3 | dump das `kalmanordem*` | grava a cópia |
| S8 | 4 | guarda o binário da v1.34 em `/opt/kalman-ordens/kalman-ordens.911caf424346` | grava a cópia |
| S9 | 4 | instala a v1.35 | **troca o binário** |
| S10 | 5 | `migrar` (0019) e a conferência no banco | **muda o banco** |
| S11 | 6 | sobe o serviço (S11b: restart, só se o login falhar) | sobe o SIMULADOR |
| S12 | 7.2 | `usuario habilitar -ambiente REAL` (tem de ser recusado) | não (recusado) |
| S13 | 7.2 | `usuario listar` do REAL | não |
| S14 | 7.2 | opcional: a recusa das chaves extintas, com a cópia do env | não |
| S15 | 7.3 | compara as linhas do SIMULADOR com o S3 | não |
| S16 | 9.1 | volta: o `kalman-ordens.911caf424346` de volta e restart | só se precisar |
| S17 | 9.2 | volta: restaurar o dump pelo caminho anotado no S7 | só falando comigo |

## 11. Depois (eu)

- conferir a versão e o journal;
- rodar o `scripts/instalado-migracoes.sh` (o retrato passa a ser o `09872ead98f2`, com 19
  migrações) e fazer o commit;
- o ESTADO novo;
- marcar o roteiro de 13/11 §9 (CLI `habilitar -ambiente REAL` + `REAL_UIDS`) como obsoleto;
- o merge da `v1.35` no `main`, junto com a `v1.34` pendente (o §4 do ESTADO-2026-11-16);
- a cópia `antes-0018` pode sair depois de alguns pregões com a v1.35;
- a `antes-0019` fica até a instalação das telas.
