# v1.36.4 na .53: a stop do PnT que não dispara (op 234852)

Preparado em 24/11. **Instalado em 25/11 às 20:00:51** (resultado no §9). O George lê antes de rodar. Os comandos com `sudo` são do
George e estão numerados de **U1** a **U4**; a lista está no §8.

- **Branch:** `v1.36.4` (worktree `../kalman-ordens-v1364`), a partir do `main` `1225a6f`.
- **Binário:** `ef0500954c33`, sha256 `f12902a15079f3b83e471bd12c2de70e8c5820060987c7113209ef71c6cbbfb9` (`dist/kalman-ordens` da worktree).
- **Testes:** suíte completa (memória e MariaDB) em 24/11, 16:02:50–16:21:38, commit `ef05009`, árvore limpa: `FIM rc=0` (`var/suite1364-20261124-160250.log`). Os roteiros 140–142 passam; as 5 verificações ao contrário (SPEC v1.37 §7.7) reprovaram como deviam.
- **Pré-requisito:** a v1.36.3 instalada (`947b81422b04`), que é a de hoje.
- **Quando:** depois da zeragem (14:30), com o REAL sem posição e o Kalman sem abrir entradas. É um
  restart. Dentro da janela da GAMMAQ2 (até 17:55), ela cai por ~2 s e loga de novo; depois das 17:55,
  ela só loga às 08:30. Sem operação aberta, nenhum dos dois tem efeito.
- **Duração:** de 5 a 10 minutos.

## 0. O que entra

SPEC técnica v1.37 §7 (o adendo de 24/11, `docs/SPEC-TECNICA-v1.37-STOP-NUNCA-PULA.md`; decisões e
implementação no §7.7):
- **A 3ª porta, só no PnT.** Quando a 1014 está aceita, o último passou do gatilho por 1 tick e nada
  veio do PnT (nem 150=I, nem 39=B, nem fill) em **2 s**, o serviço segue o caminho do stop furado:
  1. o `STOP_FURADO` crítico, com o texto **"stop furado (sem disparo)"**;
  2. o cancelamento da 1014;
  3. **só com o cancelamento confirmado**, a 1031 (o bid − 50, com os repreços de sempre).
  - O relógio não volta a zero se o preço voltar; volta com um gatilho novo.
  - Na 234852 isso teria agido às ~11:32:31, contra os ~33 s sem proteção efetiva daquele dia.
- **A saída por replace com 1 degrau (`SAIDA_REPLACE_DEGRAUS=1`).** Se o 1º degrau não disparar em 2 s,
  vem o mesmo `STOP_FURADO` "(sem disparo)" e o cancelamento com a 1031, sem os degraus 2–4 (eles
  custaram ~7 s na 234852).
- **O cancelamento da 1014 sem disparo recusado:**
  - recusado "in time" (a stop disparou por fim): `AGUARDANDO_STOP`, como na v1.36.3, **sem** a 1031;
  - recusado por outro motivo: o cancelamento é repetido, sem 1031 enquanto ela estiver viva.

**Contrato com o Kalman: sem mudança.** A saída da porta nova chega como `SAIDA_EXECUTADA` e `ZERADA`,
com `origem`/`motivo` `STOP_FURADO` (valores que já existem).

**Chaves novas, com padrão** (nada a mudar no `service.env`):
- `STOP_SEM_DISPARO_S` (2; 0 desliga) e `STOP_SEM_DISPARO_TICKS` (1);
- `SAIDA_REPLACE_DEGRAUS` (1).

Sem migração, sem mudança na unidade ou em `deploy/` além da documentação.

### 0.1 Quem é afetado

| quem | durante | depois |
|---|---|---|
| **kalman-ordens** (SIMULADOR e REAL) | um restart (U3), de segundos; o relogin das duas credenciais da Flexscan (e da GAMMAQ2, até 17:55) | o descrito no §0, só nas stops do PnT |
| **O Kalman** | nada (sem posição, sem efeito) | pode receber `ZERADA` com `STOP_FURADO` também nesse caso novo; o texto do Telegram traz "(sem disparo)" |
| **Principal e telas** | não reiniciam | nada muda |

### 0.2 A volta

Só o binário: `kalman-ordens.947b81422b04`, guardado no U1, e restart (§7). Sem banco.
Sem trocar o binário, `STOP_SEM_DISPARO_S=0` no `service.env` desliga a porta, e
`SAIDA_REPLACE_DEGRAUS=4` volta aos 4 degraus. As duas pedem restart.

## 1. Antes (sem mudar nada)

```bash
date; /opt/kalman-ordens/kalman-ordens versao               # 947b81422b04 (a v1.36.3)
sha256sum /home/gammaquant/kalman-ordens-v1364/dist/kalman-ordens   # f12902a15079f3b83e471bd12c2de70e8c5820060987c7113209ef71c6cbbfb9
/home/gammaquant/kalman-ordens-v1364/dist/kalman-ordens versao        # ef0500954c33
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos,
  pos: [.ambientes[] | .posicao], div: [.ambientes[] | .divergente], vivas: [.ambientes[] | .ordens_vivas]}'
#   posições 0; divergente false; criticos {}; vivas null
```
Se houver posição, ordem viva ou crítico, pare e me chame.

## 2. Guardar o que está instalado

**U1**: o binário da v1.36.3.
```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.947b81422b04
```

## 3. Instalar

**U2**: o binário.
```bash
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v1364/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # ef0500954c33
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
#   "subindo" e "no ar" com ef0500954c33; "migrações: nenhuma pendente"; os logins de partida OK
#   (flx_gammaquant e btg_gammaquant, nenhum "login recusado"); as conciliações com 0 divergência e
#   posição 0; nenhum DIVERGENTE, PARADA, chmod ou ERROR
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos}'
#   versao ef0500954c33, os mesmos ambientes de antes, criticos {}
curl -s 127.0.0.1:9092/saude | jq -c '.ambientes.REAL | {sessao_logada, posicao, divergente, parada, canais: (.canais|map_values(.estado))}'
#   sessao_logada true (só até 17:55; depois, false até 08:30); posicao 0; divergente false; parada null
for u in 1450 35 5725; do curl -s "127.0.0.1:9092/usuarios/$u/destino?ambiente=REAL" | jq -c --arg u $u '{uid: $u} + (.ambientes.REAL | {conector, conta, pode_enviar, codigo})'; done
#   1450 PNT 4582351, 35 PNT 5094181, 5725 FLEXSCAN D-4582351 (pode_enviar só com os canais no ar)
```
**No Telegram:** só as `CONCILIACAO` da partida. Nenhum AVISO (o restart foi pedido) e nenhum CRITICO.

**Atenção ao login REAL da Flexscan:** se vier "login recusado" ou "User password blocked", **não
reinicie de novo**: siga o ESTADO-2026-11-23 §7.1 e me chame. O PnT não depende dele.

## 4. Conferir (o caso de verdade)

Os roteiros 140–142 provam a v1.36.4 com o PnT e a Flexscan falsos. No REAL:
- **a stop normal** não muda nada: o "Delaying final state" (150=I) chega em ~0,6 s, que é o sinal de
  disparo;
- **uma stop que não dispara** dá, 2 s depois do preço passar do gatilho, o `STOP_FURADO "stop furado
  (sem disparo)..."` no Telegram, o 35=F da 1014 e, com o 150=4, a 1031.

```bash
journalctl -u kalman-ordens --since today --no-pager | grep -E 'sem disparo|STOP_FURADO|CANCEL_STOP_REJEITADO' | cut -c1-260
```

## 5. Depois (eu)

- o `instalado-migracoes.sh ef0500954c33` (sem migração nova);
- o merge da `v1.36.4` no `main`, a tag `instalado/v1.36.4` e o push;
- a memória e o ESTADO.

## 6. O vigia

O `scripts/vigia-stop.py` continua valendo como **observador independente**: um processo fora do
serviço, que vê a mesma regra pelo preço da 9090 e avisa também se o serviço não responder. Com a
v1.36.4, o alerta dele chega junto com o `STOP_FURADO` do serviço. Mantê-lo ou não é decisão sua.

## 7. Como voltar

**U4**: só o binário.
```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.947b81422b04 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 947b81422b04
```
Também é um login REAL da Flexscan (o aviso do §3 vale).

## 8. Os comandos com sudo, em ordem

| nº | § | o que faz | muda algo? |
|---|---|---|---|
| U1 | 2 | guarda o binário da v1.36.3 (`kalman-ordens.947b81422b04`) | grava a cópia |
| U2 | 3 | instala o binário | **troca o binário** |
| U3 | 3 | restart | **reinicia o serviço** |
| U4 | 7 | volta ao binário da v1.36.3 e restart | só se precisar |

## 9. Resultado (25/11)

Instalada pelo George às **20:00:51** (U1–U3), depois das 17:55 (a GAMMAQ2 fechada; loga às 08:30 de
26/11). Conferido por ele: `ef0500954c33` `active`, `NRestarts=0`, migrações nenhuma pendente, os logins
de partida da Flexscan (`btg_gammaquant` e `flx_gammaquant`) OK, as conciliações com 0 divergência e
posição 0, `criticos {}`. REAL: `sessao_logada false` (fora da janela), PNT `FORA`, FLEXSCAN:BTG
`NO_AR`, posição 0, sem parada. Destinos: 1450 PNT 4582351, 35 PNT 5094181, 5725 FLEXSCAN D-4582351.
Telegram: só as conciliações e o aviso do vigia (o serviço fora do ar por segundos). O U4 não foi
usado; a cópia da v1.36.3 fica em `/opt/kalman-ordens/kalman-ordens.947b81422b04`.

Depois: `instalado-migracoes.sh` (`ef0500954c33`, 20 migrações, sem nova), merge da `v1.36.4` no `main`
e a tag `instalado/v1.36.4` no `ef05009`. O caso de verdade (§4) fica para quando acontecer.
