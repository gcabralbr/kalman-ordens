# v1.36.2 na .53: a recusa da corretora na entrada (REJEITADA_CORRETORA)

Preparado em 17/11. **Não instalado.** O George lê antes de rodar. Os comandos com `sudo` são do
George e estão numerados de **U1** a **U4**; a lista está no §8.

- **Branch:** `v1.36.2` (worktree `../kalman-ordens-v1362`), a partir do `8a8fff8` da `v1.36.1`.
- **Binário:** `91df575bcc22`, sha256 `0a9567bac3b6debf64bb45d7ef8861f473efc59e8bf66875e3e3196f3da2584e`
  (`dist/kalman-ordens` da worktree). Suíte completa em 17/11, 17:55:57–18:14:29, commit `91df575`:
  `FIM rc=0` (`var/suite1362-20261117-175557.log`).
- **Pré-requisito: a v1.36.1 instalada e conferida** (`INSTALACAO-v1.36.1.md` até o T6, com o
  binário `9f580660aab7`). A v1.36.2 contém a v1.36.1, mas a unidade e o `servico-falhou.sh` novos
  só entram pelo roteiro dela. Os dois podem ir na mesma noite, um depois do outro.
- **Quando:** à noite, depois das suítes, ou no dia seguinte depois da zeragem das 14:30. Fora do
  pregão, com a posição em 0. Um restart do serviço. Serve com o REAL ligado ou desligado.
- **Duração:** de 5 a 10 minutos.

## 0. O que entra

SPEC técnica v1.36.2 (`docs/SPEC-TECNICA-v1.36.2-RECUSA-CORRETORA.md`), do teste da margem de day
trade de 17/11 (ops 234777–234779):
- **O 39=4 da entrada com "changed to Error state ... is Rejected"** (a corretora recusou por dentro
  do robô DSA) encerra a entrada como `ENTRADA_ENCERRADA{motivo: "REJEITADA_CORRETORA", texto}`,
  8 s depois, em vez de `CANCELADA`. O 58 vai para o `motivo_rejeicao` da ordem. **Sem alerta e sem
  Telegram** (D4).
- **O 35=9 "Order cannot be canceled. It has ended already"** do nosso cancel do prazo deixa de ser
  `RECUSA_PNT`. Se o relatório final não chegar em 3 s, o `RECUSA_PNT` de antes sai, com um mass
  status (P3). Nessa espera, o prazo da entrada não manda outro cancel.
- **O 39=8 continua com o `RECUSA_PNT`** (P2), e o `ENTRADA_ENCERRADA` ganha o `texto` "[103] 58".
- **O campo `texto`** em todo `ENTRADA_ENCERRADA` (`null` fora das recusas).
- **A ficha e a lista do admin** ganham `recusas_corretora_hoje` (as entradas REAL do dia com
  `motivo_rejeicao`, do banco).

Sem migração, sem chave nova no `service.env`, sem mudança na unidade nem nos scripts de `deploy/`.

### 0.1 Quem é afetado

| quem | durante | depois |
|---|---|---|
| **kalman-ordens** (as instâncias ligadas) | um restart (U3): de segundos a 2–3 min. Dentro da janela da Flexscan (08:02–18:30), o relogin da partida | o motivo novo, o `texto` e a contagem na ficha |
| **O Kalman** (`feat/recusa-corretora`, `880b449`, instalado em 17/11 à tarde) | nada | passa a receber o `REJEITADA_CORRETORA`. Antes da v1.36.2 ele já roda sem problema: lê o `texto` com `.get` e não mostra a linha das recusas na ficha quando o campo não vem |
| **Principal e telas** | não reiniciam | nada muda |

### 0.2 A volta

Só o binário: `kalman-ordens.9f580660aab7`, guardado no U1, e restart (§7). Sem banco. Os
`motivo_rejeicao` que a v1.36.2 gravar ficam no banco; a v1.36.1 os mostra no histórico como texto,
sem efeito.

## 1. Antes (sem mudar nada)

```bash
/opt/kalman-ordens/kalman-ordens versao                      # 9f580660aab7 (a v1.36.1)
systemctl show kalman-ordens -p RestartMode | cat            # RestartMode=direct (a v1.36.1 está)
sha256sum /home/gammaquant/kalman-ordens-v1362/dist/kalman-ordens   # 0a9567bac3b6debf64bb45d7ef8861f473efc59e8bf66875e3e3196f3da2584e
/home/gammaquant/kalman-ordens-v1362/dist/kalman-ordens versao        # 91df575bcc22
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos,
  pos: [.ambientes[] | .posicao], vivas: [.ambientes[] | .ordens_vivas]}'
#   posições 0, nada vivo, criticos {}
```
Se a versão instalada não for a `9f580660aab7` ou o `RestartMode` não for `direct`, pare: falta a
v1.36.1.

## 2. Guardar o que está instalado

**U1**: o binário da v1.36.1.
```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.9f580660aab7
```

## 3. Instalar

**U2**: o binário.
```bash
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v1362/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 91df575bcc22
```

**U3**: o restart.
```bash
sudo systemctl restart kalman-ordens
```

Sem sudo:
```bash
sleep 10; systemctl is-active kalman-ordens                  # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|no ar|migra|ERROR' | cut -c1-220
#   "kalman-ordens subindo" versao=91df575bcc22; "migrações: nenhuma pendente"; "kalman-ordens no ar"; nenhum ERROR
curl -s 127.0.0.1:9092/saude | jq -c '{versao, ambientes_permitidos, criticos: .alertas_criticos}'
#   versao 91df575bcc22, os mesmos ambientes de antes, criticos {}
```
**No Telegram:** só a `CONCILIACAO` da partida. Nenhum AVISO (o restart foi pedido) e nenhum CRITICO.

## 4. Conferir o contrato novo (sem sudo)

A ficha e a lista do admin, com o 1450:
```bash
curl -s 127.0.0.1:9092/usuarios/1450/real | jq -c '.recusas_corretora_hoje'
#   {"quantidade":N,"ultima":...}. N conta as entradas REAL do dia com motivo_rejeicao. As três de
#   17/11 (234777–234779) NÃO contam: a v1.35 não gravou o texto do 39=4. Sem recusa: {"quantidade":0,"ultima":null}
curl -s 127.0.0.1:9092/usuarios/real | jq -c '[.usuarios[] | {uid, recusas_corretora_hoje}]'
#   o mesmo N do 1450
```
Com o REAL desligado, as duas rotas respondem igual (a ficha lê do banco).

**Na tela do admin (Kalman `880b449`):** a ficha do 1450 passa a mostrar a linha das recusas do dia.

## 5. O primeiro caso de verdade (não é passo da instalação)

Não há como provar a recusa da corretora sem a corretora recusar. O roteiro 137 a prova com o PnT
falso. Quando acontecer no REAL (ou se o George repetir o teste da margem, em outro dia), o esperado:
- **no journal:** nenhum `ALERTA codigo=RECUSA_PNT` da entrada; o evento
  `ENTRADA_ENCERRADA ... "motivo":"REJEITADA_CORRETORA" ... "texto":"This strategy was changed to Error state, ..."`
  uns 8 s depois do 39=4;
- **no Telegram:** nada;
- **na ficha:** `recusas_corretora_hoje.quantidade` sobe e a `ultima` é a operação;
- **no Kalman:** a mensagem ao usuário e, na 2ª seguida, o "Enviar ordens" desligado (regra dele, D5).

Para o journal:
```bash
journalctl -u kalman-ordens --since today --no-pager | grep -E 'REJEITADA_CORRETORA|ja terminou|já terminou|RECUSA_PNT' | cut -c1-260
```

## 6. Depois (eu)

- o `instalado-migracoes.sh 91df575bcc22`, que não terá migração nova;
- o merge da `v1.36.2` na `v1.36.1` e no `main`;
- a memória e o ESTADO.

## 7. Como voltar

**U4**: só o binário.
```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.9f580660aab7 /opt/kalman-ordens/kalman-ordens
sudo systemctl restart kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                      # 9f580660aab7
```
O Kalman segue funcionando: sem o campo, a ficha não mostra a linha das recusas, e a margem volta a
chegar como `CANCELADA`.

## 8. Os comandos com sudo, em ordem

| nº | § | o que faz | muda algo? |
|---|---|---|---|
| U1 | 2 | guarda o binário da v1.36.1 (`kalman-ordens.9f580660aab7`) | grava a cópia |
| U2 | 3 | instala o binário | **troca o binário** |
| U3 | 3 | restart | **reinicia o serviço** |
| U4 | 7 | volta ao binário da v1.36.1 e restart | só se precisar |

## 9. Resultado (17/11, pelo George)

- **19:32: v1.36.2 `91df575bcc22` instalada** por este roteiro (`39fcc51`), logo depois da v1.36.1
  (`9f580660aab7`, 19:25). Sem crítico e sem AVISO no restart pedido; `RestartMode=direct`,
  `NRestarts=0`. A `CONCILIACAO` do SIMULADOR deu 0/0.
- **§4:** `recusas_corretora_hoje` do 1450 = `{"quantidade":0,"ultima":null}`.
- **Binário anterior:** `/opt/kalman-ordens/kalman-ordens.9f580660aab7` (a v1.36.1), para a volta (§7).
- A sessão REAL ficou "sem sessão" desde as 19:25: fora da janela dela (08:30–17:55). Ela loga sozinha
  às 08:30 de 18/11; a conferência está no ESTADO-2026-11-18.
- **Retrato do instalado:** `91df575bcc22`, 20 migrações (sem migração nova).
