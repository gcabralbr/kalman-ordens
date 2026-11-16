# v1.36 (a conta livre e o REAL sem limites de negócio) na .53: instalação com o REAL desligado

Preparado em 16/11, depois da E2 (`5c1e7b0`).

O roteiro é para o George ler antes de rodar. Os comandos com `sudo` estão separados e numerados,
de **S1** a **S16**, e a lista completa está no §10. Os comandos sem `sudo` (curl e `versao`) só
leem. Nada é gravado na `usutxtconfigprd`, e nada roda no `dbecotools` além das leituras de sempre do
serviço.

**Quando:** fora do pregão, **depois das 18:45** (recomendado), com a conferência do §1 zerada:
posição 0, nada vivo e nada suspenso. A outra janela possível é depois da zeragem das 14:30, como na
v1.35, com o §1 zerado. O motivo está no §0.2. Não pode rodar com o REAL ligado.

**Uma janela só, do §1 ao §7.** Os comandos usam as variáveis de shell `$V`, `$DB`, `$T` e `$C`,
que só existem na janela do terminal onde foram criadas. Rode tudo, do §1 ao §7, na mesma janela. Os
comandos da volta (§9, S15 e S16) não dependem delas: usam o nome explícito do binário e o caminho
da cópia anotado no S6.

**Quanto tempo:** de 20 a 30 minutos. O serviço fica parado de 2 a 3 minutos, do S5 ao S10.

## 0. O que entra, quem é afetado e a volta

### 0.1 O que entra

O binário vem do worktree `../kalman-ordens-v136`, commit `5c1e7b004e3d` (branch `v1.36`).

A suíte completa passou nesse commit, com os dois bancos e `-race`:
- quando: 16/11, das 19:24:32 às 19:43:08;
- log: `var/suite136e2-20261116-192432.log`;
- resultado: FIM rc=0, 21 pacotes.

O binário foi gerado com `scripts/build.sh` no `5c1e7b004e3d`. Depois dele só entra este roteiro (docs).
**Não gere de novo**, porque o hash mudaria.

```
sha256  ca1be87ce2a169633646be7f4fefb5bb85646ec24458495568c977f5754544e3  dist/kalman-ordens
versao  5c1e7b004e3d
```

O que muda, por cima da v1.35 instalada (`09872ead98f2`), conforme a SPEC funcional revista em 16/11
(D3, D6, D7, D10) e a `SPEC-TECNICA-v1.36-CONTA-LIVRE.md`:
- **A aprovação do REAL não tem mais conta nem limites.** Ela diz só "este uid pode usar o REAL", com
  quem, quando, motivo e histórico. A conta, a corretora e o conector vêm do cadastro do GQ a cada
  entrada. Não existe mais o estado `CONTA_DIVERGENTE`, a comparação a cada 30 s nem o alerta
  `REAL_CONTA_DIVERGENTE`.
- **O cadastro que o sistema não atende** recusa a entrada REAL com `REAL_CADASTRO_INUTILIZAVEL` e
  avisa **uma vez** no Telegram, com o `RESOLVIDO` quando volta a ser usável. Isso só roda com o REAL
  ligado: **nesta instalação, com o REAL desligado, o verificador não roda** e nenhum alerta `REAL_*`
  é esperado.
- **O REAL sem limites de negócio (D7).** Os cinco `REAL_LIMITE_*` do `service.env` passam a ser
  **ignorados**, com um aviso no journal na partida. **O `service.env` não muda nesta instalação**:
  as chaves ficam lá, e é por isso que a volta para a v1.35 (que as exige com o REAL ligado) é só a
  troca do binário. No REAL ficam só três travas: `LIMITE_MENSAGENS_MIN`, as 50 recusas e uma
  operação aberta por usuário. O SIMULADOR não muda.
- **Migração 0020:** uma linha REAL em `CONTA_DIVERGENTE` volta a `APROVADO`, com uma linha
  `MIGRACAO` no histórico. Sem mudança de esquema. Hoje não deve haver nenhuma (S2).
- **As rotas** (o contrato do §5 da SPEC técnica da v1.36): a ficha vem com `aprovado` sem conta e
  sem limites, `limites_instancia` e `divergencia` `null`; a ação `limites` saiu (400); o `aprovar`
  aceita e ignora o `visto` e os limites que as telas F1 mandam. O `/destino` ganhou o campo `causa`.

### 0.2 Quem é afetado, e por que fora do pregão

| quem | durante a instalação | depois |
|---|---|---|
| **kalman-ordens, instância SIMULADOR** (a única ligada) | **parada de 2 a 3 min** (S5–S10). Depois das 18:45 a Flexscan está fora da janela (08:02–18:30): a partida não faz login, e o de amanhã às 08:02 é o de sempre. Depois das 14:30, há o relogin da partida, como na v1.35 (§6 daquele roteiro) | igual: as linhas do SIMULADOR e o caminho da Flexscan não mudam (§7.3) |
| **Principal** (`kalman`) e as **telas F1** (KalmanWIN `06faeef`) | **não reinicia.** Perde o `GET /eventos` por alguns segundos e volta sozinha | as telas funcionam (§7.2): o quadro "Aprovado" mostra conta e limites como "—", e o botão "Alterar limites" some. A atualização das telas é do CC do Kalman, depois |
| **Paper do Ferro** (35) e os demais em papel | não passam pelo kalman-ordens | nada muda |
| **Sombras WIN-V28/V29** | não falam com o kalman-ordens | nada muda |
| **REAL** | desligado (`AMBIENTES_PERMITIDOS=SIM`) | o 1450 continua `APROVADO` (a versão não muda) e o 5725 em `SEM_APROVACAO`. A conta aprovada na v1.35 fica gravada, mas não vale mais |
| fix-integration do GQ, md | não são tocados | — |

**Por que fora do pregão.** O SIMULADOR é a instância que reinicia: com operação aberta, uma
entrada, uma stop ou uma saída da Flexscan poderia cair nos 2 a 3 minutos parados. Depois das 18:45
não há pregão, nem janela da Flexscan, nem relogin. Se o §1 não vier zerado, **não instale**.

### 0.3 A volta para a v1.35: até onde é simples

| até | voltar é | como |
|---|---|---|
| antes do S9 (`migrar`) | trivial | o binário antigo de volta (§9.1); o banco não mudou |
| **depois da 0020**, sem nenhuma ação na tela | **simples**: só o binário | §9.1. A 0020 não muda o esquema e a v1.35 ignora a linha 20 da `kalmanordem_migracao`. O `service.env` não mudou. O 1450 volta como estava (aprovado com a conta 4582351 gravada) |
| depois de um **aprovar** feito na v1.36 | **simples, com um efeito visível** | a linha aprovada na v1.36 fica sem conta. Na v1.35, o avaliador (que roda mesmo com o REAL desligado) põe esse uid em `CONTA_DIVERGENTE` e dispara o crítico `REAL_CONTA_DIVERGENTE`, repetindo. É o lado seguro (entradas recusadas). Sai com um aprovar pela tela F1, que grava a conta |

O mesmo vale se o cadastro do 1450 no GQ mudar enquanto a v1.36 estiver instalada: na volta, a v1.35
compara e põe em `CONTA_DIVERGENTE`, como faria hoje.

## 1. Antes (serviço no ar, v1.35)

Sem sudo:

```bash
V=$(/opt/kalman-ordens/kalman-ordens versao); echo $V      # 09872ead98f2 (a v1.35)
curl -s 127.0.0.1:9092/saude | jq '{versao, ambientes_permitidos, posicao: .ambientes.SIMULADOR.posicao,
  vivas: .ambientes.SIMULADOR.flexscan.ordens_vivas, suspensas: .ambientes.SIMULADOR.operacoes_suspensas,
  criticos: .alertas_criticos}'
#   esperado: ["SIMULADOR"], posicao 0, vivas 0, suspensas [], criticos {}
sha256sum /home/gammaquant/kalman-ordens-v136/dist/kalman-ordens
#   ca1be87ce2a169633646be7f4fefb5bb85646ec24458495568c977f5754544e3
/home/gammaquant/kalman-ordens-v136/dist/kalman-ordens versao   # 5c1e7b004e3d

# o retrato do SIMULADOR e do REAL antes, para comparar no §7
for u in 1450 5725 35; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=SIMULADOR" \
  | jq -c '.ambientes.SIMULADOR | {destino, conector, corretora_codigo, conta, habilitado, pode_enviar, codigo}'; done \
  > /tmp/destino-sim-antes-v136.txt; cat /tmp/destino-sim-antes-v136.txt
curl -s 127.0.0.1:9092/usuarios/real | jq -c '.usuarios[] | {uid, estado, versao}'
#   1450 APROVADO (versão 5, ou a que a tela deixou), 5725 SEM_APROVACAO; nenhum CONTA_DIVERGENTE
```

**S1**: o nome do banco. Só o nome aparece.
```bash
DB=$(sudo sed -n 's#^DB_DSN=.*)/\([^?]*\).*#\1#p' /etc/kalman-ordens/service.env | tail -1 | tr -d "\"'"); echo $DB
```

**S2**: o banco antes. Anote os números.
```bash
sudo mysql "$DB" -e "SELECT COUNT(*), MAX(versao) FROM kalmanordem_migracao;
  SELECT uid, estado, habilitado, versao FROM kalmanordemusuario WHERE ambiente='REAL' ORDER BY uid;
  SELECT COUNT(*) AS conta_divergente FROM kalmanordemusuario WHERE ambiente='REAL' AND estado='CONTA_DIVERGENTE';
  SELECT COUNT(*) FROM kalmanordemusuariohist"
#   19 | 19;  1450 APROVADO 1 (versão), 5725 SEM_APROVACAO 0;  conta_divergente 0;  o total do histórico
```

**S3**: a lista dos usuários pela CLI, guardada para comparar no §7.
```bash
sudo sh -c '/opt/kalman-ordens/kalman-ordens usuario listar > /var/backups/kalman-ordens/usuarios-antes-v136.txt'
sudo grep -c . /var/backups/kalman-ordens/usuarios-antes-v136.txt
```

**S4**: o que o `service.env` tem do REAL. Nada muda nele; isto só diz o que a ficha vai mostrar no
§7.1 (o PnT do REAL configurado ou não). Os valores não aparecem.
```bash
sudo awk -F= '/^[[:space:]]*(AMBIENTES_PERMITIDOS|REAL_LIMITE_[A-Z_]+|REAL_FIX_HOST|REAL_PNT_DESLIGADO|REAL_FLEXSCAN_URL|REAL_FLEXSCAN_CORRETORAS)=/ \
  {print NR": "$1"="(($1=="AMBIENTES_PERMITIDOS")?$2:"(definida)")}' /etc/kalman-ordens/service.env
#   AMBIENTES_PERMITIDOS=SIM; os cinco REAL_LIMITE_* (definida); anote se aparecem REAL_FIX_HOST e
#   REAL_PNT_DESLIGADO
```

Pare aqui se:
- a posição, as vivas ou as suspensas não estiverem zeradas, ou houver crítico aberto;
- o sha256 ou a versão não baterem;
- o REAL estiver em `ambientes_permitidos`;
- o banco não estiver em 19;
- `conta_divergente` não for 0 (não é erro: a 0020 resolve; me mande o S2 antes de seguir).

## 2. O `service.env`: não muda

Os cinco `REAL_LIMITE_*` ficam onde estão (a v1.36 os ignora com um aviso; a v1.35 os exige numa
volta com o REAL ligado). `AMBIENTES_PERMITIDOS=SIM` continua. Nenhum comando aqui.

## 3. Parar o serviço e copiar o banco

**S5**: parar.
```bash
sudo systemctl stop kalman-ordens; systemctl is-active kalman-ordens     # inactive
```

**S6**: a cópia de todas as tabelas `kalmanordem*`, com o serviço parado.
```bash
T=$(sudo mysql "$DB" -N -e "SHOW TABLES LIKE 'kalmanordem%'"); echo $T
C=/var/backups/kalman-ordens/antes-0020-$(date +%Y%m%d-%H%M%S).sql.gz
sudo sh -c "mysqldump --single-transaction '$DB' $(echo $T) | gzip > $C"; echo $C
sudo chmod 600 $C
sudo ls -l $C                         # -rw------- root root
sudo sh -c "zcat $C | tail -1"                                            # -- Dump completed on ...
sudo sh -c "zcat $C | grep -c 'INSERT INTO .kalmanordemusuario.'"         # > 0
```

**Anote o caminho completo da cópia**, o que o `echo $C` mostrou. O S16 usa esse caminho.

Sem "Dump completed", **não siga**: volte com `sudo systemctl start kalman-ordens` (a v1.35 está
intacta) e me mande a saída.

## 4. Instalar o binário (guardando o atual)

**S7**: guardar a v1.35.
```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.09872ead98f2
/opt/kalman-ordens/kalman-ordens.09872ead98f2 versao      # 09872ead98f2
```

**S8**: instalar a v1.36.
```bash
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v136/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                   # 5c1e7b004e3d
```

## 5. A migração 0020

**S9**: migrar e conferir.
```bash
sudo /opt/kalman-ordens/kalman-ordens migrar
#   aplicada: 0020_conta_livre.sql
sudo mysql "$DB" -e "SELECT versao, nome FROM kalmanordem_migracao WHERE versao >= 19;
  SELECT uid, estado, habilitado, versao FROM kalmanordemusuario WHERE ambiente='REAL' ORDER BY uid;
  SELECT COUNT(*) FROM kalmanordemusuariohist"
#   19 e 20 (0020_conta_livre.sql)
#   as linhas REAL iguais às do S2 (com conta_divergente 0, a 0020 não muda nada)
#   o total do histórico igual ao do S2
```

Se o `migrar` der erro, **não suba a v1.36**. Vá ao §9.1 e me mande a mensagem.

## 6. Subir e conferir o serviço

**S10**: subir.
```bash
sudo systemctl start kalman-ordens
```

Sem sudo:
```bash
sleep 8; systemctl is-active kalman-ordens                 # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|migra|no ar|ignorada|verificador|ERROR' | cut -c1-220
#   bom: "kalman-ordens subindo" versao=5c1e7b004e3d; "migrações: nenhuma pendente"; "kalman-ordens no ar";
#        cinco linhas "configuração" aviso="REAL_LIMITE_... ignorada: sem limites de negocio no REAL (v1.36, D7)"
#   ruim: ERROR; "verificador do REAL: ..." (não deve aparecer com o REAL desligado)
curl -s 127.0.0.1:9092/saude | jq '{versao, ambientes_permitidos, posicao: .ambientes.SIMULADOR.posicao,
  suspensas: .ambientes.SIMULADOR.operacoes_suspensas, criticos: .alertas_criticos,
  flex: .ambientes.SIMULADOR.flexscan | {janela, ativa, ativa_por, logado, ultimo_login, ordens_vivas}}'
#   versao "5c1e7b004e3d", ["SIMULADOR"], posicao 0, suspensas [], criticos {}, janela "08:02-18:30",
#   ordens_vivas 0;
#   depois das 18:45: ativa false, ativa_por null (fora da janela: sem login até as 08:02 de amanhã);
#   depois das 14:30: ativa true, ativa_por "janela", logado true, ultimo_login partida/OK, como no §6
#   do roteiro da v1.35
```

**No Telegram:** só a `CONCILIACAO` da partida (0 ordens, posição 0). Nenhum crítico e nenhum `REAL_*`.

## 7. A conferência desta instalação (REAL desligado)

### 7.1 A aprovação sem conta e sem limites, pelas rotas (sem sudo, só GET)

```bash
curl -s 127.0.0.1:9092/usuarios/real | jq -c '.usuarios[] | {uid, estado, versao, conta, cadastro_utilizavel, qtd_max_ordem}'
#   1450: estado APROVADO, a mesma versão do S2, conta = a do GQ agora, qtd_max_ordem null
#   5725: SEM_APROVACAO
curl -s 127.0.0.1:9092/usuarios/1450/real | jq -c '{estado, versao, acoes, aprovado, divergencia, limites_instancia,
  gq: (.gq | {conector, corretora_codigo, conta, valido, erro}), hist: [.historico[:4][] | {evento, estado, conta}]}'
#   estado APROVADO, acoes ["bloquear","revogar"] (sem "limites"),
#   aprovado {conector null, corretora_codigo null, corretora null, conta null, qtd_max_ordem null,
#   posicao_max null, por, em, motivo} (o por/em/motivo da aprovação de 16/11),
#   divergencia null, limites_instancia null,
#   gq: o cadastro do GQ agora. valido true se o S4 mostrou REAL_FIX_HOST sem REAL_PNT_DESLIGADO;
#   senão valido false com erro.causa CONECTOR_NAO_ATENDIDO ("conector PNT nao atendido nesta
#   maquina"): a máquina não tem o PnT do REAL configurado. Não é defeito com o REAL desligado; me
#   mande a linha.
#   hist: as linhas de 16/11 como foram gravadas (com a conta da v1.35 e as DIVERGENCIA/VOLTOU do teste)
curl -s '127.0.0.1:9092/usuarios/1450/destino?ambiente=REAL' | jq -c '.ambientes.REAL | {pode_enviar, codigo, causa}'
#   pode_enviar false, codigo AMBIENTE_NAO_PERMITIDO, causa null
curl -s -o /dev/null -w '%{http_code}\n' 127.0.0.1:9092/usuarios/1450/stop     # 409
```

**S11**: a lista pela CLI, com o estado do REAL.
```bash
sudo /opt/kalman-ordens/kalman-ordens usuario listar | grep 'ambiente=REAL'
#   1450 estado=APROVADO; 5725 estado=SEM_APROVACAO
#   (a CLI ainda mostra corretora, conta, posicao_max e qtd_max gravados pela v1.35 na linha do 1450:
#   ficam no banco e não valem mais na v1.36)
```

### 7.2 Nas telas F1 (kalman-admin, como admin; só olhar, nada a gravar)

Recarregue a ficha do 1450 no bloco "Aprovação do REAL":
- o estado `APROVADO` e o histórico como antes;
- no quadro "Aprovado", conector, corretora e conta como "—", e a linha de limites em branco;
- "Limites da instância" com "—";
- os botões Bloquear e Revogar, **sem** "Alterar limites".

Não clique em nada nesta instalação. Se quiser ver o aprovar com o corpo das telas F1 funcionando,
é o roteiro 132 (passou na suíte). A aceitação pela tela da SPEC funcional (trocar a conta sem
bloqueio; corretora 107 com um alerta só) precisa do REAL ligado e fica para o roteiro do 1º dia.

### 7.3 O SIMULADOR intacto

```bash
for u in 1450 5725 35; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=SIMULADOR" \
  | jq -c '.ambientes.SIMULADOR | {destino, conector, corretora_codigo, conta, habilitado, pode_enviar, codigo}'; done \
  | diff /tmp/destino-sim-antes-v136.txt - && echo "SIMULADOR igual"
#   SIMULADOR igual
```

**S12**: as linhas do SIMULADOR, iguais às do S3.
```bash
diff <(sudo grep 'ambiente=SIMULADOR' /var/backups/kalman-ordens/usuarios-antes-v136.txt) \
     <(sudo /opt/kalman-ordens/kalman-ordens usuario listar | grep 'ambiente=SIMULADOR') && echo "SIMULADOR igual"
```

**S13** (opcional): a CLI ainda recusa habilitar o REAL (nada gravado).
```bash
sudo /opt/kalman-ordens/kalman-ordens usuario habilitar -uid 1450 -ambiente REAL -motivo "teste da instalacao"
#   erro: "... use a tela (kalman-admin) ..."; nada gravado
```

**S14** (opcional): o `service.env` sem nenhuma chave `REAL_LIMITE_*` também sobe (a v1.36 não as
exige). Só a conferência da configuração, sem restart:
```bash
sudo sh -c "grep -vE '^[[:space:]]*REAL_LIMITE_' /etc/kalman-ordens/service.env > /tmp/env-sem-limites" && \
  sudo /opt/kalman-ordens/kalman-ordens usuario conferir -uid 1450 -env /tmp/env-sem-limites; sudo rm -f /tmp/env-sem-limites
#   sem erro de configuração (a saída é a conferência do uid)
```

## 8. Amanhã, 08:02–08:45

A abertura da Flexscan deve ser igual à de hoje: **uma linha só**, `FLEXSCAN_ABERTURA`, sem nenhum
crítico e sem nenhum `REAL_*`. Me mande a linha.

## 9. Como voltar

### 9.1 Para a v1.35, só o binário (antes ou depois da 0020)

**S15**:
```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.09872ead98f2 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens       # (start, se estiver parado)
/opt/kalman-ordens/kalman-ordens versao   # 09872ead98f2
```

- **O env:** não mudou; serve às duas versões.
- **A 0020:** fica aplicada. A v1.35 a ignora (§0.3).
- **Um aprovar feito na v1.36:** vira `CONTA_DIVERGENTE` na v1.35, com o crítico (§0.3). Reaprove pela
  tela F1.

### 9.2 Restaurar a cópia: só falando comigo

Só se a `kalmanordemusuario` ou a `kalmanordemusuariohist` ficarem danificadas. O dump tem
`DROP TABLE`/`CREATE TABLE` das `kalmanordem*`: **perde tudo o que foi gravado depois do S6**. Antes,
volte a v1.35 (S15) com o serviço parado.

**S16**: troque `<CAMINHO-ANOTADO-NO-S6>` pelo caminho completo da cópia.
```bash
sudo systemctl stop kalman-ordens
sudo ls -l <CAMINHO-ANOTADO-NO-S6>
DB=$(sudo sed -n 's#^DB_DSN=.*)/\([^?]*\).*#\1#p' /etc/kalman-ordens/service.env | tail -1 | tr -d "\"'"); echo $DB
sudo sh -c "zcat <CAMINHO-ANOTADO-NO-S6> | mysql '$DB'"
sudo mysql "$DB" -e "SELECT COUNT(*), MAX(versao) FROM kalmanordem_migracao"   # 19 | 19
sudo systemctl start kalman-ordens
```

## 10. Os comandos com sudo, em ordem

| nº | § | o que faz | muda algo? |
|---|---|---|---|
| S1 | 1 | lê o nome do banco no `service.env` | não |
| S2 | 1 | o banco antes (migrações, linhas REAL, CONTA_DIVERGENTE, histórico) | não |
| S3 | 1 | guarda o `usuario listar` em `/var/backups/kalman-ordens/` | grava um arquivo |
| S4 | 1 | as chaves do REAL no `service.env` (valores ocultos) | não |
| S5 | 3 | para o serviço | **para o SIMULADOR** |
| S6 | 3 | dump das `kalmanordem*` (chmod 600) | grava a cópia |
| S7 | 4 | guarda o binário da v1.35 em `/opt/kalman-ordens/kalman-ordens.09872ead98f2` | grava a cópia |
| S8 | 4 | instala a v1.36 | **troca o binário** |
| S9 | 5 | `migrar` (0020) e a conferência no banco | **muda o banco** (só se houver CONTA_DIVERGENTE) |
| S10 | 6 | sobe o serviço | sobe o SIMULADOR |
| S11 | 7.1 | `usuario listar` do REAL | não |
| S12 | 7.3 | compara as linhas do SIMULADOR com o S3 | não |
| S13 | 7.3 | opcional: `usuario habilitar -ambiente REAL` (tem de ser recusado) | não (recusado) |
| S14 | 7.3 | opcional: a configuração sem `REAL_LIMITE_*` confere | não (arquivo temporário apagado) |
| S15 | 9.1 | volta: o `kalman-ordens.09872ead98f2` de volta e restart | só se precisar |
| S16 | 9.2 | volta: restaurar o dump pelo caminho anotado no S6 | só falando comigo |

## 11. Depois (eu)

- conferir a versão e o journal;
- rodar o `scripts/instalado-migracoes.sh` (o retrato passa a ser o `5c1e7b004e3d`, com 20 migrações) e
  fazer o commit;
- o ESTADO novo;
- o merge da `v1.36` no `main`;
- avisar o CC do Kalman do contrato novo (§5 da SPEC técnica da v1.36) para a atualização das telas;
- a cópia `antes-0019` pode sair; a `antes-0020` fica até alguns pregões com a v1.36.
