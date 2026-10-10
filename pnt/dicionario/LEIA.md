# Dicionário FIX do PnT DSA

- `dsa_FIX44.original.xml` — cópia intacta do dicionário de 2020 (`pntfixrepo/docs/pnt/dsa_FIX44.xml`), byte a byte. Não editar.
- `dsa_FIX44.xml` — a cópia usada pelo serviço. Única diferença: a raiz ganhou `type="FIX"`,
  sem o qual o QuickFIX/Go recusa o arquivo (`type attribute must be FIX or FIXT`).

Defasado em relação ao RoE 2025 (faltam 20828–20835, 20844–20883, 35508, 35509; os grupos de
pernas não declaram tags que o PnT ecoa). Substituir pelo dicionário novo quando chegar [PnT-2].
Com ele, as mensagens multileg do PT recebidas no mass status falham na validação nas duas
bibliotecas (QuickFIX/J e QuickFIX/Go); por isso RejectInvalidMessage=N na v1.
