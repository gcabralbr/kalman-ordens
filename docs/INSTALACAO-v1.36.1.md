# v1.36.1 na .53: o alerta da unidade e a partida que caía por permissão

Preparado em 17/11, depois da instalação da v1.36 (`5c1e7b004e3d`). **Não instalado.** O George lê
antes de rodar. Os comandos com `sudo` são do George e estão numerados de **T1** a **T9**; a lista
está no §8.

- **Branch:** `v1.36.1` (worktree `../kalman-ordens-v1361`), a partir do `ea0cc03` da `v1.36`.
- **Binário:** `9f580660aab7`, sha256 `1f41fc69ca1f9d0ff55d1a87f5bc356a3056275aa7454920e089f656bac33b22`
  (`dist/kalman-ordens` da worktree). Suíte completa em 17/11, 17:37:01–17:55:57, commit `9f58066`:
  `FIM rc=0 teste-servico-falhou=0` (`var/suite1361-20261117-173701.log`).
- **Quando:** fora do pregão, com a posição em 0. A instalação tem um restart do serviço. Pode ser
  com o REAL ligado ou desligado; o roteiro serve aos dois.
- **Duração:** de 10 a 15 minutos, mais 1 minuto do teste opcional do §5.

## 0. O que entra e por quê

Em 17/11, às 09:40:55, o primeiro start do REAL morreu no `chmod` de um arquivo do store FIX:
`kalman-ordens: chmod /var/lib/kalman-ordens/fix-real/store/FIX.4.4-GAMMAQ2-PNT_DSA_BTG2.senderseqnums:
no such file or directory`. O systemd o reiniciou 10 s depois, e a partida foi limpa. Mesmo assim, o
Telegram recebeu na hora o CRITICO "a unidade desistiu de subir… voltar ao binario anterior (LEIA
1.15)". As duas coisas eram defeito:

1. **A partida que caía por permissão.** O serviço aperta as permissões do diretório FIX logo depois
   de pôr a sessão no ar. Com o store de outro dia, o QuickFIX/Go o zera no logon e recria os
   `*seqnums`. Um arquivo listado e apagado antes do `chmod` derrubava a partida.
   - **Agora:** o que sumiu é pulado. O arquivo novo já nasce 600 pelo `UMask=0077` da unidade.
   - **E:** qualquer outro erro de permissão vira `WARN` no journal ("pnt: permissões do diretório FIX
     não apertadas; a partida segue"). Permissão é endurecimento, não condição para operar.
2. **O CRITICO na primeira falha.** Com o `RestartMode` padrão, toda falha passa por *failed* e
   dispara o `OnFailure=`, mesmo com o reinício agendado.
   - **`RestartMode=direct`** (systemd 257 na .53; existe desde a 254): o reinício automático vai
     direto a *activating* e não dispara o `OnFailure`. **O CRITICO sai só quando o systemd desiste**:
     5 partidas em 5 min, ou um erro sem reinício.
   - **O AVISO a cada falha que vai ser reiniciada**, pelo `ExecStopPost=` da própria unidade:
     ```
     [.53 kalman-ordens] AVISO: falhou (exit-code, exited 1); ultimo erro: kalman-ordens: ...;
     o systemd reinicia em 10s (tentativa N de 5). Se ele desistir, sai o CRITICO
     ```
     O `ExecStopPost` roda em toda parada. O aviso fica calado na parada normal (`SERVICE_RESULT=success`)
     e na parada pedida (um `stop` ou `restart` em curso).
   - **O limite conta as partidas em 5 min, inclusive a primeira.** A última tentativa anunciada pode
     ser recusada pelo systemd; nesse caso o CRITICO vem logo depois.
   - **O texto do CRITICO:** "se caiu logo depois de instalar um binario novo, voltar ao anterior (LEIA
     1.16); senao, conferir o journal". A LEIA foi renumerada: a 1.15 virou o conector da Flexscan, e
     a instalação segura com a volta está na **1.16**.

### 0.1 Como o AVISO chega ao Telegram sem expor o token

| alerta | quem roda | de onde vem o destino |
|---|---|---|
| CRITICO (`OnFailure`) | a unidade `kalman-ordens-falhou@`, com `DynamicUser` | o `/etc/kalman-ordens/disco.env`, lido pelo systemd (`EnvironmentFile=`), **igual a hoje** |
| AVISO (`ExecStopPost`) | a própria `kalman-ordens`, como o usuário `kalman-ordens`, no mesmo sandbox (`ProtectSystem=strict`, `NoNewPrivileges`) | o `ALERTA_WEBHOOK_URL` (ou `TELEGRAM_TOKEN`/`TELEGRAM_CHAT`) do `service.env`, que o serviço **já tem** no ambiente para os alertas dele |

O AVISO **não lê o `disco.env`**. Foram descartadas duas outras formas:
- **o `+` no `ExecStopPost`:** rodaria como root, fora do sandbox, só para ler um arquivo;
- **abrir o `disco.env` ao grupo do serviço:** daria ao serviço uma credencial que hoje ele não tem.

Do jeito escolhido, nenhum processo ganha acesso a um segredo que já não tivesse.

Mais três cuidados:
- **O token e a URL não vão mais na linha de comando do `curl`**, que qualquer usuário da máquina vê
  no `ps`. Eles entram pela configuração na entrada padrão (`curl -K -`). O CRITICO também passou a
  enviar assim.
- **O AVISO não lê o journal:** o usuário do serviço não tem o grupo `systemd-journal`. O último erro
  vem de `/var/lib/kalman-ordens/ultimo-erro`, que o `servir` grava ao cair (600, do usuário do
  serviço) e apaga na partida seguinte. Numa morte por sinal não há arquivo, e o aviso diz "ver o
  journal".
- **Um envio só, com 5 s de prazo,** porque o `ExecStopPost` atrasa o reinício. O `-` no comando
  faz uma falha do aviso não contar como falha da unidade.

### 0.2 Quem é afetado

| quem | durante | depois |
|---|---|---|
| **kalman-ordens** (as instâncias ligadas) | um restart (T6): de segundos a 2–3 min. Dentro da janela da Flexscan (08:02–18:30), o relogin da partida, sem tolerância | igual. Muda só o alerta da unidade e a partida, que não cai mais por permissão |
| **Principal e telas** | não reiniciam | nada muda |
| **A unidade de alerta `kalman-ordens-falhou@`** | não muda: o mesmo arquivo de unidade e o mesmo `disco.env` | o texto do CRITICO e o envio por `-K -` (pelo `servico-falhou.sh` novo) |

### 0.3 A volta

Só arquivos, sem banco:
- o binário `kalman-ordens.5c1e7b004e3d`;
- a unidade e o script guardados no T3.

Depois, `daemon-reload` e restart (§7). Não há migração na v1.36.1.

## 1. Antes (sem mudar nada)

```bash
/opt/kalman-ordens/kalman-ordens versao                      # 5c1e7b004e3d (a v1.36)
sha256sum /home/gammaquant/kalman-ordens-v1361/dist/kalman-ordens   # 1f41fc69ca1f9d0ff55d1a87f5bc356a3056275aa7454920e089f656bac33b22
/home/gammaquant/kalman-ordens-v1361/dist/kalman-ordens versao        # 9f580660aab7
systemctl show kalman-ordens -p RestartMode -p ExecStopPost -p OnFailure -p StartLimitBurst
#   RestartMode=normal, ExecStopPost= vazio, OnFailure=kalman-ordens-falhou@kalman-ordens.service.service, 5
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos,
  pos: [.ambientes[] | .posicao], vivas: [.ambientes[] | .ordens_vivas]}'
#   posições 0, nada vivo, criticos {}
```

**T1**: o AVISO e o CRITICO vão para o mesmo lugar? A comparação é feita sem mostrar os valores.
```bash
sudo bash -c 'for k in ALERTA_WEBHOOK_URL TELEGRAM_TOKEN TELEGRAM_CHAT ALERTA_SERVIDOR; do
  a=$(grep -E "^$k=" /etc/kalman-ordens/service.env | tail -1 | cut -d= -f2-)
  b=$(grep -E "^$k=" /etc/kalman-ordens/disco.env | tail -1 | cut -d= -f2-)
  printf "%-20s service.env:%-9s disco.env:%-9s %s\n" $k "${a:+definida}" "${b:+definida}" "$([ "$a" = "$b" ] && echo iguais || echo DIFERENTES)"
done'
#   o esperado: ALERTA_WEBHOOK_URL definida nos dois, iguais
```
Se vier DIFERENTES, o AVISO vai para o destino dos alertas do serviço e o CRITICO para o do disco,
que são os dois canais de hoje. Não é defeito, mas me diga qual dos dois é o certo para o AVISO.

## 2. Guardar o que está instalado

**T2**: o binário da v1.36.
```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.5c1e7b004e3d
```

**T3**: a unidade e o script de hoje.
```bash
sudo cp -a /etc/systemd/system/kalman-ordens.service /var/backups/kalman-ordens/kalman-ordens.service.antes-v1361
sudo cp -a /opt/kalman-ordens/servico-falhou.sh /var/backups/kalman-ordens/servico-falhou.sh.antes-v1361
```

## 3. Instalar

**T4**: o script e a unidade nova. O serviço continua no ar com a configuração antiga até o T6.
```bash
cd /home/gammaquant/kalman-ordens-v1361
sudo install -o root -g root -m 755 deploy/servico-falhou.sh /opt/kalman-ordens/servico-falhou.sh
sudo install -o root -g root -m 644 deploy/kalman-ordens.service /etc/systemd/system/
sudo systemctl daemon-reload
systemctl show kalman-ordens -p RestartMode -p ExecStopPost -p OnFailure -p StartLimitBurst | cut -c1-200
#   RestartMode=direct; ExecStopPost={ path=/opt/kalman-ordens/servico-falhou.sh ; argv[]=... --reiniciando kalman-ordens.service ; ignore_errors=yes ...}
#   OnFailure=kalman-ordens-falhou@kalman-ordens.service.service; StartLimitBurst=5
```

**T5**: o binário.
```bash
sudo install -o root -g root -m 755 dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 9f580660aab7
```

**T6**: o restart.
```bash
sudo systemctl restart kalman-ordens
```

Sem sudo:
```bash
sleep 10; systemctl is-active kalman-ordens                  # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|no ar|permiss|servico-falhou|AVISO|ERROR' | cut -c1-220
#   bom: "kalman-ordens subindo" versao=9f580660aab7; "kalman-ordens no ar"
#   não deve aparecer: AVISO (o restart pedido é calado), "permissões ... não apertadas" (pode, se
#   algo sumiu no meio: é só WARN, a partida segue), ERROR
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos}'
#   versao 9f580660aab7, os mesmos ambientes de antes, criticos {}
```

**No Telegram:** só a `CONCILIACAO` da partida. Nenhum AVISO, porque o restart foi pedido, e nenhum
CRITICO.

## 4. O que fica valendo

- **O restart e o stop pedidos** não mandam nada, como antes.
- **Uma queda com reinício** manda um AVISO por tentativa. Com o serviço de volta, nada mais sai.
- **O CRITICO** sai só quando o systemd desiste. Aí a unidade fica em `failed`, e o restart exige
  antes `sudo systemctl reset-failed kalman-ordens` (LEIA 1.16).

## 5. Opcional: provar o comportamento numa unidade descartável (1 min)

Isto prova no systemd da .53 o que os testes só simulam:
- o `RestartMode=direct`;
- o AVISO do `ExecStopPost` com o usuário e o sandbox do serviço;
- o CRITICO só quando ele desiste.

A unidade fica em `/run`, que some no reboot, e roda `/bin/false`. **Ela não toca no kalman-ordens.**
Manda 4 mensagens ao Telegram, todas marcadas `kalman-teste-falhou`.

**T7**:
```bash
sudo tee /run/systemd/system/kalman-teste-falhou.service >/dev/null <<'U'
[Unit]
Description=teste do alerta da v1.36.1 (descartavel)
StartLimitIntervalSec=60
StartLimitBurst=3
OnFailure=kalman-ordens-falhou@%n.service
[Service]
User=kalman-ordens
EnvironmentFile=/etc/kalman-ordens/service.env
ExecStart=/bin/false
Restart=on-failure
RestartSec=2
RestartMode=direct
ExecStopPost=-/opt/kalman-ordens/servico-falhou.sh --reiniciando %n
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
U
sudo systemctl daemon-reload
sudo systemctl start kalman-teste-falhou; sleep 15
systemctl show kalman-teste-falhou -p Result -p NRestarts     # Result=exit-code, NRestarts=3
```
**Esperado no Telegram, nesta ordem:**
- três `[.53 kalman-teste-falhou] AVISO: falhou (exit-code, exited 1); ultimo erro: sem mensagem do
  servico, ver o journal; o systemd reinicia em 2s (tentativa 1 de 3)`, depois a 2 de 3 e a 3 de 3;
- um `[.53 kalman-teste-falhou] CRITICO: a unidade desistiu de subir (resultado exit-code, 3
  reinicio(s), ...)`, com "versao ?" e "sem erro no journal" (natural para o `/bin/false`).

O limite (3 partidas em 60 s) conta a primeira: a 3ª tentativa anunciada é a que o systemd recusa
("Start request repeated too quickly"), e aí vem o CRITICO. Com o `RestartMode=direct`, o `Result`
fica no da última falha (`exit-code`), não `start-limit-hit`, e o `NRestarts` conta a recusada (3).
Visto no T7 de 17/11, às 19:28 (deploy/LEIA.md, "a última tentativa anunciada pode ser recusada").

**Não ok:**
- **um CRITICO antes do último AVISO:** o `RestartMode=direct` não fez efeito;
- **nenhum AVISO:** o `ExecStopPost` não alcançou o destino. Me mande o
  `journalctl -u kalman-teste-falhou -n 30`.

**T8**: apagar a unidade de teste.
```bash
sudo systemctl reset-failed kalman-teste-falhou 'kalman-ordens-falhou@kalman-teste-falhou.service.service'
sudo rm /run/systemd/system/kalman-teste-falhou.service
sudo systemctl daemon-reload
```

## 6. Depois (eu)

- o `instalado-migracoes.sh 9f580660aab7`, que não terá migração nova;
- o merge da `v1.36.1` na `v1.36` e no `main`;
- a memória e o ESTADO.

## 7. Como voltar

**T9**: só arquivos.
```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.5c1e7b004e3d /opt/kalman-ordens/kalman-ordens
sudo install -o root -g root -m 644 /var/backups/kalman-ordens/kalman-ordens.service.antes-v1361 /etc/systemd/system/kalman-ordens.service
sudo install -o root -g root -m 755 /var/backups/kalman-ordens/servico-falhou.sh.antes-v1361 /opt/kalman-ordens/servico-falhou.sh
sudo systemctl daemon-reload
sudo systemctl reset-failed kalman-ordens 2>/dev/null; sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 5c1e7b004e3d
```
O arquivo `/var/lib/kalman-ordens/ultimo-erro`, se existir, pode ficar: a v1.36 não o lê.

## 8. Os comandos com sudo, em ordem

| nº | § | o que faz | muda algo? |
|---|---|---|---|
| T1 | 1 | compara o destino dos alertas no `service.env` e no `disco.env`, sem mostrar os valores | não |
| T2 | 2 | guarda o binário da v1.36 (`kalman-ordens.5c1e7b004e3d`) | grava a cópia |
| T3 | 2 | guarda a unidade e o script de hoje em `/var/backups/kalman-ordens/` | grava as cópias |
| T4 | 3 | instala o script e a unidade nova, `daemon-reload` | **muda a unidade** (vale no T6) |
| T5 | 3 | instala o binário | **troca o binário** |
| T6 | 3 | restart | **reinicia o serviço** |
| T7 | 5 | opcional: a unidade descartável que falha sempre | 4 mensagens de teste no Telegram |
| T8 | 5 | apaga a unidade descartável | não |
| T9 | 7 | volta: binário, unidade e script de antes, e restart | só se precisar |

## 9. Resultado (17/11, pelo George)

- **19:25: v1.36.1 `9f580660aab7` instalada** por este roteiro (`c3f0f91`), com o REAL ligado e a posição 0.
  - `RestartMode=direct` e o `ExecStopPost` com o `servico-falhou.sh --reiniciando` na unidade.
  - O restart pedido (T6) não mandou AVISO. Nenhum crítico. A `CONCILIACAO` do SIMULADOR deu 0/0.
  - A sessão REAL ficou "sem sessão": às 19:25 estava fora da janela dela (`REAL_FIX_INICIO`/`FIM`,
    08:30–17:55). Não é defeito; ela loga sozinha às 08:30.
- **19:28: T7.** Três AVISOs (tentativas 1, 2 e 3 de 3) e depois um CRITICO, nessa ordem. O resultado
  foi `Result=exit-code`, `NRestarts=3`: a 3ª tentativa anunciada foi a recusada ("Start request
  repeated too quickly"). O comportamento está certo; a expectativa do §5 foi corrigida. O CRITICO de
  teste saiu com "versao ?" e "sem erro no journal", natural para o `/bin/false`.
- **19:32: a v1.36.2 `91df575bcc22`** foi instalada em seguida (`INSTALACAO-v1.36.2.md`).
- **Retrato do instalado:** `9f580660aab7`, 20 migrações (sem migração nova).
