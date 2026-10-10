# Dicionário FIX do PnT DSA

- `dsa_FIX44.original.xml` — cópia intacta do dicionário de 2020 (`pntfixrepo/docs/pnt/dsa_FIX44.xml`), byte a byte. Não editar.
- `dsa_FIX44.xml` — a cópia usada pelo serviço. Diferenças em relação ao original (e só estas):
  1. a raiz ganhou `type="FIX"`, sem o qual o QuickFIX/Go recusa o arquivo
     (`type attribute must be FIX or FIXT`);
  2. no corpo de **ExecutionReport (8)** e **OrderCancelReject (9)**, todo `required="Y"` virou
     `required="N"` (8: OrderID, ExecID, ExecType, OrdStatus, Instrument, Side, LeavesQty, CumQty,
     AvgPx; 9: OrderID, ClOrdID, OrigClOrdID, OrdStatus, CxlRejResponseTo). Mesmo com
     `RejectInvalidMessage=N`, o QuickFIX/Go rejeita com 35=3 a mensagem sem campo obrigatório, e
     ela não chega à aplicação: um fill incompleto seria descartado. O motor valida o conteúdo e
     decide (SPEC v1.1 F8). Cabeçalho, trailer e MsgType ficam como estão.

  Conferir com `diff --strip-trailing-cr dsa_FIX44.original.xml dsa_FIX44.xml`. A prova está em
  `dicionario_test.go` (aceito na cópia, rejeitado no original).

Defasado em relação ao RoE 2025 (faltam 20828–20835, 20844–20883, 35508, 35509; os grupos de
pernas não declaram tags que o PnT ecoa). Substituir pelo dicionário novo quando chegar [PnT-2].
Com ele, as mensagens multileg do PT recebidas no mass status falham na validação nas duas
bibliotecas (QuickFIX/J e QuickFIX/Go); por isso RejectInvalidMessage=N na v1.
