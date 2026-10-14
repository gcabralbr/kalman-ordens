-- v1.3 H1: o ExecID (17) é único por ordem (37), não no banco inteiro. Na HML de 14/10 o fill
-- trouxe 17 numérico (cara de TradeID da bolsa), que pode se repetir entre ordens e entre dias:
-- com a chave só no 17, um fill novo com o 17 de outro seria ignorado em silêncio.
-- Só ALTER (o usuário do banco não tem DROP); NULL em qualquer das duas não conflita.

ALTER TABLE kalmanordemevento
    DROP INDEX uk_koe_execid,
    ADD UNIQUE KEY uk_koe_execid (orderid37, execid);
