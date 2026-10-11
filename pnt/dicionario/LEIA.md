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
     decide (SPEC v1.1 F8). Cabeçalho, trailer e MsgType ficam como estão;
  3. o enum de `MassStatusReqType (585)` ganhou o valor `9` (orders for account), que o RoE 2025
     manda usar no mass status por conta e o dicionário de 2020 não tem (o FIX 4.4 padrão vai até
     8). Sem ele, o próprio mass status do serviço falha na validação estrita (SPEC v1.1 F10).
  4. o campo `ActivateErrorStatusStrategy (20856)`, BOOLEAN, declarado e incluído (opcional) no
     **NewOrderSingle (D)**. O serviço manda `20856=Y` em toda ordem nova (SPEC v1.1 F14); o RoE
     2025 só lista a tag na Sniper e no multileg, e o log real de HML do PT a mostra no 35=AB.

  Conferir com `diff --strip-trailing-cr dsa_FIX44.original.xml dsa_FIX44.xml`. A prova está em
  `dicionario_test.go` (aceito na cópia, rejeitado no original).

Defasado em relação ao RoE 2025 (faltam 20828–20835, 20844–20883, 35508, 35509; os grupos de
pernas não declaram tags que o PnT ecoa). Substituir pelo dicionário novo quando chegar [PnT-2].
Com ele, as mensagens multileg do PT recebidas no mass status falham na validação nas duas
bibliotecas (QuickFIX/J e QuickFIX/Go); por isso RejectInvalidMessage=N na v1.
