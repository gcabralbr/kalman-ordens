# SPEC-ORDENS v1.31.1: o robô e o OMS da DSA nas ordens do PnT (13/11)

Correção a partir da v1.31 instalada (`93245099bd13`), para o REAL pelo PnT na .53.

## O caso

**13/11, 09:35:58.** Primeiro teste do REAL pelo PnT: o uid 1450, a conta 4582351 e a sessão `GAMMAQ2` → `PNT_DSA_BTG2`.
A boleta mandou a entrada:
`35=D ... 847=1031 20856=Y`.

O PnT recusou na hora:
`39=8 58=[DSA-3] Robot not found. Check the Robot settings on InterfaceServer`.

**A causa** (o PnT/BTG): a DSA tem dois OMS.
- O de **day trade**, `BTGDAYTRADE`, é o do Kalman e do PTF.
- O de **posição**, `BTGCEDRO`, é o do L&S do GQ.

A ordem tem de dizer qual robô e qual OMS usar. O L&S do GQ já mandava os dois: `20858=BTG_ALGO` e
`20859=BTGCEDRO`. O kalman-ordens não mandava nenhum.

## A regra

- Duas chaves, com os nomes do dicionário da DSA:
  - `PNT_ROBOT_ID` vai na tag 20858 (`RobotID`);
  - `PNT_ENTERING_FIRM` vai na tag 20859 (`EnteringFirmName`).
- No REAL, valem só as `REAL_PNT_ROBOT_ID` e `REAL_PNT_ENTERING_FIRM`. Elas fazem parte da sessão do
  ambiente, como as `REAL_FIX_*`: a sem prefixo nunca cai para o REAL, e a REAL_ nunca aparece no
  SIMULADOR. Na equivalência HML (`ORDENS_AMBIENTE`), valem as sem prefixo.
- Vazias, não vão. A HML e o SIMULADOR ficam como estavam.
- **Em que mensagens vão.** O dicionário da DSA prevê as duas no D, no G, no AB e no AC. Por isso
  elas entram em toda ordem que o serviço monta pelo `corpoOrdem`:
  - o D da entrada (1031);
  - o D da proteção (1014);
  - o D da saída (1031);
  - o G (o replace do ajuste da 1014, e o repreço).
- **O F (cancelamento) não leva.** O dicionário não tem as duas tags no F, e o cancelamento do GQ
  também sai sem elas. O PnT acha a ordem pelo 37 e pelo 41.
- O AF (mass status) e o AN (posições) não são ordens e ficam como estão.
- **No journal**, ao subir com a sessão, sai a linha "pnt: sessão" com `robot_id_20858` e
  `entering_firm_20859`.

## Testes

- `TestRoboEOMS` (pnt):
  - as duas tags no D 1031, no D 1014 e no G, inclusive na mensagem montada para o fio;
  - o F sem elas;
  - sem as duas na `Identificacao`, nenhuma delas vai.
- `TestVisaoRoboPnT` (config): o REAL lê as REAL_; o SIMULADOR não as vê; a sem prefixo não vaza
  para o REAL.
- **Roteiro 128**, com o PnT falso validando cada mensagem contra o dicionário:
  - as duas tags na entrada, na 1014, no replace do ajuste e na saída;
  - o cancelamento da 1014 passa sem elas.
- **Conferido:** sem a correção, o `TestRoboEOMS` e o roteiro 128 falham ("tag 20858 ausente").
