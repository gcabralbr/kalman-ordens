# Dicionário FIX do PnT DSA

- `dsa_FIX44.pnt-2026-10-13.xml` — o dicionário recebido do PnT em 13/10/2026 (`dsa_FIX44.xml`),
  byte a byte (`-text` no git; sha256 `5b78faa3e2297060c22873ad50457cfea3024748cfaaa7f9189f15fd0fc5fcce`).
  Não editar. Substitui o de 2020 (que continua em `pntfixrepo/docs/pnt/dsa_FIX44.xml` e no
  histórico deste repositório).
- `dsa_FIX44.xml` — a cópia usada pelo serviço, **gerada**: `go generate ./pnt/dicionario`
  (`Derivar` em `derivar.go`). Não editar à mão: `TestCopiaDerivadaDoOriginal` exige que ela seja
  exatamente `Derivar(original)`. Diferenças em relação ao original (e só estas; SPEC v1.2 seção 7):
  1. a raiz ganhou `type="FIX"`, sem o qual o QuickFIX/Go recusa o arquivo
     (`type attribute must be FIX or FIXT`);
  2. no corpo de **ExecutionReport (8)** e **OrderCancelReject (9)**, todo `required="Y"` virou
     `required="N"` (8: OrderID, ExecID, ExecType, OrdStatus, Instrument, Side, LeavesQty, CumQty,
     AvgPx; 9: OrderID, ClOrdID, OrigClOrdID, OrdStatus, CxlRejResponseTo). Mesmo com
     `RejectInvalidMessage=N`, o QuickFIX/Go rejeita com 35=3 a mensagem sem campo obrigatório, e
     ela não chega à aplicação: um fill incompleto seria descartado. O motor valida o conteúdo e
     decide (SPEC v1.1 F8). Cabeçalho, trailer, grupos e MsgType ficam como estão;
  3. o enum de `MassStatusReqType (585)` ganhou o valor `9` (orders for account), que o RoE 2025
     manda usar no mass status por conta e o dicionário do PnT continua sem (vai até 8). Sem ele, o
     próprio mass status do serviço falha na validação estrita (SPEC v1.1 F10);
  4. as tags da **1014** (RoE 2025), que o dicionário do PnT não tem: 20828 PriceStopLoss,
     20829 PriceSendOrderLoss, 20830 PriceStartGain, 20831 PriceSendOrderGain,
     20832 PriceInitOrderLossInc, 20833 OrderLossIncMoney (PRICE), 20834 OrderLossIncPerc (FLOAT),
     20835 CountSameBrokerBuySellTrades (BOOLEAN) — declaradas e incluídas (opcionais) no corpo de
     **NewOrderSingle (D)** e **OrderCancelReplaceRequest (G)**. No 35=8 não entram (o PnT também
     não põe lá as tags de estratégia dele): o eco passa por ser tag de usuário (≥ 5000) com
     `ValidateUserDefinedFields=N`;
  5. no componente `InstrumentLeg`, a 20798 é referida como `LegLastShares` (INT), o nome declarado
     em `<fields>`. O PnT a refere como `LegLastQty` (QTY), nome que não existe em `<fields>`: assim
     o QuickFIX/Go não carrega o arquivo (`unknown field LegLastQty`;
     `TestDicionarioDoPnTNaoCarregaSemADiferenca5`).

  `ActivateErrorStatusStrategy (20856)` não é mais diferença: o PnT já o declara no 35=D.

  Conferir com `diff dsa_FIX44.pnt-2026-10-13.xml dsa_FIX44.xml`. Provas em `dicionario_test.go`
  (aceito na cópia, rejeitado no dicionário do PnT). `Derivar` confere cada premissa e falha se um
  dicionário novo já trouxer a diferença ou mudar o trecho: ao receber outro, guardar o original
  com a data no nome, trocar `Original` em `derivar.go`, rodar `go generate` e a suíte.

O dicionário do PnT ainda não descreve tudo o que o PnT ecoa: a mensagem multileg com pernas sem o
delimitador 600 (cenário 20, mensagem 2) falha na validação, por isso `RejectInvalidMessage=N` na
v1 (SPEC v1.1 F2). O mass status da SwingTrade 2028 (cenário 20, mensagem 1) passa a ser válido.
