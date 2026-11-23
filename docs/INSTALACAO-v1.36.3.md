# v1.36.3 na .53: o ExecID das recusas da Flexscan e a corrida da stop

Preparado em 23/11. **Não instalado.** O George lê antes de rodar. Os comandos com `sudo` são do
George e estão numerados de **U1** a **U4**; a lista está no §8.

- **Branch:** `v1.36.3` (worktree `../kalman-ordens-v1363`), a partir do `main` `3b0407b`.
- **Binário:** `947b81422b04`, sha256 `ebea45432ca97d7d0950c425f80e75b5de755d21b2f239fe3666ecf59f8c7247`
  (`dist/kalman-ordens` da worktree). Suíte completa em 23/11, 14:04:29–14:22:45, commit `947b814`: `FIM rc=0` (`var/suite1363-20261123-103100.log`); antes dela, os roteiros 138 e 139 passaram. As verificações ao contrário da SPEC §5 reprovaram como deviam (14:24): sem a marca de partida, o 139; sem o §3.2, o 138 pelo `RECUSA_PNT`; sem o §4, o 138 com a operação em `CANCELANDO_PROTECAO` (o cancel guardado).
- **Pré-requisito:** a v1.36.2 instalada (`91df575bcc22`), que é a de hoje.
- **Quando:** hoje, 23/11, **depois da zeragem** (14:30), com o REAL sem posição e o Kalman sem
  abrir entradas. Um restart do serviço. Dentro da janela da GAMMAQ2 (até 17:55), ela cai por ~2 s e
  loga de novo; sem operação aberta, sem efeito.
- **Duração:** de 5 a 10 minutos.

## 0. O que entra

SPEC técnica v1.36.3 (`docs/SPEC-TECNICA-v1.36.3-EXECID-E-CORRIDA-DA-STOP.md`):
- **§2, o ExecID das recusas da Flexscan (op 1123094048).** O relatório que a ponte gera para um POST
  recusado leva uma marca da partida. Depois de um restart, a recusa não repete mais o ExecID de uma
  recusa de antes: vira `REJEITADA` na hora, em vez de deixar a entrada em `ENTRANDO` com a E em
  `ENVIANDO`.
- **§3, item A (op 234820).** O 35=9 "could not be cancelled/replaced in time" com a stop executando
  sai como **um** `CANCEL_STOP_REJEITADO` (aviso), sem `RECUSA_PNT` e sem nova tentativa. As
  repetições do 35=9 (inclusive as de 69 s depois do fill) só ficam no registro. O mesmo vale para a
  saída por replace (v1.11) recusada.
- **§4, item B.1.** A saída pedida com o replace da 1014 ainda pendente (a stop manual acabou de
  mudar) espera a resposta dele. No 150=5, se o preço já passou do gatilho novo, a stop é a saída
  (`AGUARDANDO_STOP`, nenhum 35=F); senão, a saída por replace de sempre.

Sem migração, sem chave nova no `service.env`, sem mudança de contrato, na unidade ou em `deploy/`.

### 0.1 Quem é afetado

| quem | durante | depois |
|---|---|---|
| **kalman-ordens** (SIMULADOR e REAL) | um restart (U3), de segundos. O relogin das duas credenciais da Flexscan e da GAMMAQ2 | o descrito no §0 |
| **O Kalman** | nada (as ordens dele param ~2 s; sem posição, sem efeito) | menos `RECUSA_PNT` no Telegram; nenhum evento novo |
| **Principal e telas** | não reiniciam | nada muda |

### 0.2 A volta

Só o binário: `kalman-ordens.91df575bcc22`, guardado no U1, e restart (§7). Sem banco.

## 1. Antes (sem mudar nada)

```bash
date; /opt/kalman-ordens/kalman-ordens versao               # 91df575bcc22 (a v1.36.2)
sha256sum /home/gammaquant/kalman-ordens-v1363/dist/kalman-ordens   # ebea45432ca97d7d0950c425f80e75b5de755d21b2f239fe3666ecf59f8c7247
/home/gammaquant/kalman-ordens-v1363/dist/kalman-ordens versao        # 947b81422b04
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos,
  pos: [.ambientes[] | .posicao], div: [.ambientes[] | .divergente], vivas: [.ambientes[] | .ordens_vivas]}'
#   posições 0; divergente false; criticos {}. Nas vivas do REAL só pode estar a
#   KM-BOLETA-E-1123094048-1 DESCONHECIDA (a de 09:40, já conferida: o restart das 10:28 deu
#   "0 ordem(ns) KM ... 0 divergência(s)" com ela na lista)
```
Se houver posição, outra ordem viva ou crítico, pare e me chame.

## 2. Guardar o que está instalado

**U1**: o binário da v1.36.2.
```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.91df575bcc22
```

## 3. Instalar

**U2**: o binário.
```bash
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v1363/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 947b81422b04
```

**U3**: o restart.
```bash
sudo systemctl restart kalman-ordens
```

Sem sudo, ~20 s depois:
```bash
sleep 20; systemctl is-active kalman-ordens                  # active
systemctl show kalman-ordens -p NRestarts | cat              # NRestarts=0
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|no ar|migra|flexscan: login|login recusado|concilia|DIVERGENTE|PARADA|chmod|permiss|ERROR' | cut -c1-220
#   "subindo" e "no ar" com 947b81422b04; "migrações: nenhuma pendente"; os logins de partida OK
#   (flx_gammaquant e btg_gammaquant, nenhum "login recusado"); as conciliações com 0 divergência e
#   posição 0; nenhum DIVERGENTE, PARADA, chmod ou ERROR
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos}'
#   versao 947b81422b04, os mesmos ambientes de antes, criticos {}
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL | {sessao_logada, posicao, divergente, parada, canais: (.canais|map_values(.estado))}'
#   sessao_logada true (até 17:55); posicao 0; divergente false; parada null; PNT e FLEXSCAN:BTG NO_AR
for u in 1450 35 5725; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=REAL" | jq -c --arg u $u '{uid: $u} + (.ambientes.REAL | {conector, conta, pode_enviar, codigo})'; done
#   1450 PNT 4582351, 35 PNT 5094181, 5725 FLEXSCAN D-4582351; pode_enviar true nos três
```
**No Telegram:** só as `CONCILIACAO` da partida. Nenhum AVISO (o restart foi pedido) e nenhum CRITICO.

**Atenção ao login REAL da Flexscan:** é o 4º login da `btg_gammaquant` hoje (08:02, 08:28, 10:28 e
este). Se vier "login recusado" ou "User password blocked", **não reinicie de novo**: siga o
ESTADO-2026-11-23 §7.1 e me chame. O PnT (1450 e 35) não depende dele.

## 4. Conferir (não há como provar sem o caso de verdade)

Os roteiros 138 e 139 provam a v1.36.3 com o PnT e a Flexscan falsos. No REAL, o esperado:
- **uma recusa da Flexscan no POST** (como as de 09:11, 09:36 e 09:40): a entrada fecha
  `REJEITADA` na hora, também depois de um restart. Nada fica em `ENVIANDO`;
- **a saída com a stop executando** (como a 234820): no Telegram, **um** `CANCEL_STOP_REJEITADO`
  e nenhum `RECUSA_PNT`; no journal, as repetições como "35=9 repetido ou atrasado: só registro";
- **a saída logo depois de mover a stop manual:** "saída pedida com o replace da 1014 pendente:
  decide na resposta". Com o preço além do gatilho novo, nenhum 35=F.

Para o journal:
```bash
journalctl -u kalman-ordens --since today --no-pager | grep -E 'RECUSA_PNT|CANCEL_STOP_REJEITADO|repetido ou atrasado|decide na resposta|ExecID repetido' | cut -c1-260
```

## 5. Depois (eu)

- o `instalado-migracoes.sh 947b81422b04` (sem migração nova);
- o merge da `v1.36.3` no `main`, a tag `instalado/v1.36.3` e o push;
- a memória e o ESTADO.

## 6. Amanhã (24/11)

O `REAL-2026-11-24-listagem-conta-D.md` vale igual com a v1.36.3: no §1, a versão esperada é a
`947b81422b04`.

## 7. Como voltar

**U4**: só o binário.
```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.91df575bcc22 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 91df575bcc22
```
Também é um login REAL da Flexscan (o aviso do §3 vale).

## 8. Os comandos com sudo, em ordem

| nº | § | o que faz | muda algo? |
|---|---|---|---|
| U1 | 2 | guarda o binário da v1.36.2 (`kalman-ordens.91df575bcc22`) | grava a cópia |
| U2 | 3 | instala o binário | **troca o binário** |
| U3 | 3 | restart | **reinicia o serviço** |
| U4 | 7 | volta ao binário da v1.36.2 e restart | só se precisar |
