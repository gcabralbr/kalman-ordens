# v1.36.5 na .53: D1 (saída parcial parada) e D2 (venda a mais com a stop já executada)

Preparado em 25/11. **Não instalado.** O George lê antes de rodar. Os comandos com `sudo` são do
George e estão numerados de **U1** a **U4**; a lista está no §8.

- **Branch:** `v1.36.5` (worktree `../kalman-ordens-v1365`), a partir da `teste-2c` (`7a8be66`, os
  roteiros 143–147), que parte da `v1.36.4`.
- **Binário:** `7a0e7f419bf2`, sha256 `08e141f36cbda1d902b781011a20ee8e91bfccbc008d3a414fe83028016e5209` (`dist/kalman-ordens` da worktree).
- **Testes:** suíte completa (-race, memória e MariaDB) em 25/11, 19:59–~20:15, commit `7a0e7f4`, árvore
  limpa: `FIM rc=0` (`var/suite1365-20261125-195917.log`). As 3 verificações ao contrário (SPEC v1.37
  §7.8) reprovaram como deviam.
- **Pré-requisito:** a v1.36.4 instalada (`ef0500954c33`, 25/11 20:00:51) e **um pregão limpo com ela**
  (decisão do George: 26/11).
- **Quando:** 26/11 **depois da zeragem (14:30)**, com o REAL sem posição e o Kalman sem abrir entradas.
  É um restart. Dentro da janela da GAMMAQ2 (até 17:55), ela cai por ~2 s e loga de novo; depois das
  17:55, ela só loga às 08:30. Sem operação aberta, nenhum dos dois tem efeito.
- **Duração:** de 5 a 10 minutos.

## 0. O que entra

SPEC técnica v1.37 §7.8 (`docs/SPEC-TECNICA-v1.37-STOP-NUNCA-PULA.md`):
- **D1, só com mais de 1 contrato, no PnT.** Na saída por replace, um fill parcial da 1014 disparada
  recomeça o relógio de `ESPERA_STOP_S` (5 s). Sem fill novo nesse tempo: `STOP_FURADO` ("o resto (1
  de 2) não executou"), o cancel do resto e, com o 150=4, a 1031 **só do que falta**. Antes, a operação
  ficava em `SAINDO` com o resto aberto e nada enviado.
- **D2, com 1 contrato (Flexscan e PnT).**
  - Operação furada e o cancel da stop recusado com **39=2** (a stop já executou; na Flexscan, o 400
    "Finalizado"): `CANCEL_STOP_REJEITADO` e `AGUARDANDO_STOP`, **sem a 1031**. Antes, saía a venda e a
    posição ficava −1, DIVERGENTE, quando o fill da stop aparecia.
  - **Aprovado além do combinado (George, 25/11):** em `AGUARDANDO_STOP` vindo de uma recusa com 39=2
    (furada **ou não**), sem o fill em `ESPERA_STOP_S` o serviço pede **um mass status** e espera mais um
    `ESPERA_STOP_S`; só sem o fill depois disso sai a 1031. Motivo: sem webhook, o fill da Flexscan só
    aparece na consulta seguinte (até 5 s).
  - Com outro 39 (a stop viva, cancelada ou rejeitada sem execução), a 1031 de antes. O furo do PnT
    (39=B) e os ramos da v1.36.4 (stop sem disparo) não mudam.

**Contrato com o Kalman: sem mudança.** Nenhum código de alerta novo; os textos do `STOP_FURADO` e do
`CANCEL_STOP_REJEITADO` ganham o detalhe (`docs/ALERTAS.md`).

**Sem chaves novas** (nada a mudar no `service.env`), sem migração, sem mudança na unidade ou em
`deploy/`.

### 0.1 Quem é afetado

| quem | durante | depois |
|---|---|---|
| **kalman-ordens** (SIMULADOR e REAL) | um restart (U3), de segundos; o relogin das duas credenciais da Flexscan (e da GAMMAQ2, até 17:55) | o descrito no §0: só a saída com fill parcial e o cancel da stop recusado com 39=2 |
| **O Kalman** | nada (sem posição, sem efeito) | nada muda no contrato; o `AGUARDANDO_STOP` com 39=2 pode levar até ~2×`ESPERA_STOP_S` (~10 s) antes de uma 1031, em vez de ~5 s |
| **Principal, telas e o vigia-stop** | não reiniciam (o vigia avisa por segundos que o serviço não responde) | nada muda |

### 0.2 A volta

Só o binário: `kalman-ordens.ef0500954c33`, guardado no U1, e restart (§7). Sem banco, sem env.

## 1. Antes (sem mudar nada)

```bash
date; /opt/kalman-ordens/kalman-ordens versao               # ef0500954c33 (a v1.36.4)
sha256sum /home/gammaquant/kalman-ordens-v1365/dist/kalman-ordens   # 08e141f36cbda1d902b781011a20ee8e91bfccbc008d3a414fe83028016e5209
/home/gammaquant/kalman-ordens-v1365/dist/kalman-ordens versao        # 7a0e7f419bf2
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos,
  pos: [.ambientes[] | .posicao], div: [.ambientes[] | .divergente], vivas: [.ambientes[] | .ordens_vivas]}'
#   posições 0; divergente false; criticos {}; vivas null
journalctl -u kalman-ordens --since today --no-pager | grep -E 'sem disparo|STOP_FURADO|CANCEL_STOP_REJEITADO|DIVERGENTE|PARADA' | cut -c1-220
#   o "pregão limpo" da v1.36.4: nenhum DIVERGENTE nem PARADA; um STOP_FURADO "(sem disparo)", se houver, me mande antes
```
Se houver posição, ordem viva, crítico ou DIVERGENTE, pare e me chame.

## 2. Guardar o que está instalado

**U1**: o binário da v1.36.4.
```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.ef0500954c33
```

## 3. Instalar

**U2**: o binário.
```bash
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v1365/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 7a0e7f419bf2
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
#   "subindo" e "no ar" com 7a0e7f419bf2; "migrações: nenhuma pendente"; os logins de partida OK
#   (flx_gammaquant e btg_gammaquant, nenhum "login recusado"); as conciliações com 0 divergência e
#   posição 0; nenhum DIVERGENTE, PARADA, chmod ou ERROR
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos}'
#   versao 7a0e7f419bf2, os mesmos ambientes de antes, criticos {}
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL | {sessao_logada, posicao, divergente, parada, canais: (.canais|map_values(.estado))}'
#   sessao_logada true (só até 17:55; depois, false até 08:30); posicao 0; divergente false; parada null
for u in 1450 35 5725; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=REAL" | jq -c --arg u $u '{uid: $u} + (.ambientes.REAL | {conector, conta, pode_enviar, codigo})'; done
#   1450 PNT 4582351, 35 PNT 5094181, 5725 FLEXSCAN D-4582351 (pode_enviar só com os canais no ar)
```
**No Telegram:** só as `CONCILIACAO` da partida e o aviso do vigia (o serviço fora do ar por segundos).
Nenhum AVISO do serviço (o restart foi pedido) e nenhum CRITICO.

**Atenção ao login REAL da Flexscan:** se vier "login recusado" ou "User password blocked", **não
reinicie de novo**: siga o ESTADO-2026-11-23 §7.1 e me chame. O PnT não depende dele.

## 4. Conferir (o caso de verdade)

Os roteiros 143–147 provam a v1.36.5 com o PnT e a Flexscan falsos. No REAL, com 1 contrato, o D1 não
aparece; o D2 só no caso raro (furo + stop executada + consulta atrasada). Quando acontecer:
- um `CANCEL_STOP_REJEITADO` seguido do `SAIDA_EXECUTADA` da **stop** (origem `STOP_SERVIDOR`), sem
  nenhuma `KM-S` e sem DIVERGENTE;
- se o fill demorar, um mass status pedido (`mass status` no journal) antes de qualquer 1031.

```bash
journalctl -u kalman-ordens --since today --no-pager | grep -E 'CANCEL_STOP_REJEITADO|mass status|STOP_FURADO|DIVERGENTE' | cut -c1-260
```

## 5. Depois (eu)

- o `instalado-migracoes.sh 7a0e7f419bf2` (sem migração nova);
- com o ok do George: o merge da `v1.36.5` no `main` (traz junto os roteiros 143–147 da `teste-2c`), a
  tag `instalado/v1.36.5` e o push;
- a memória e o ESTADO;
- os 2 contratos (4.2 e 4.3) voltam à decisão do George.

## 6. O vigia

Sem mudança: o `vigia-stop` (unidade de sistema transitória) continua observando as stops do PnT.

## 7. Como voltar

**U4**: só o binário.
```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.ef0500954c33 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # ef0500954c33
```
Também é um login REAL da Flexscan (o aviso do §3 vale). Para voltar à v1.36.3, a cópia
`kalman-ordens.947b81422b04` continua lá.

## 8. Os comandos com sudo, em ordem

| nº | § | o que faz | muda algo? |
|---|---|---|---|
| U1 | 2 | guarda o binário da v1.36.4 (`kalman-ordens.ef0500954c33`) | grava a cópia |
| U2 | 3 | instala o binário | **troca o binário** |
| U3 | 3 | restart | **reinicia o serviço** |
| U4 | 7 | volta ao binário da v1.36.4 e restart | só se precisar |
