-- v1.3 M1 (diretriz da v2, 14/10): o serviço de ordens no PnT não é só do Kalman — qualquer
-- módulo do GQ vai usá-lo. Coluna `modulo` (padrão 'KALMAN', o que já existe) nas tabelas de
-- operação, ordem, evento, eventos publicados e habilitação (com o histórico). A operação passa a
-- ser única por (modulo, operacao_id): a idempotência do POST /ordens é (modulo, operacao_id,
-- papel). Habilitação e limites continuam por uid (por (modulo, uid) na v2).
-- Só ALTER (o usuário do banco não tem DROP).

ALTER TABLE kalmanordemoperacao
    ADD COLUMN modulo VARCHAR(8) NOT NULL DEFAULT 'KALMAN' AFTER data,
    DROP INDEX uk_kop_operacao,
    ADD UNIQUE KEY uk_kop_operacao (modulo, operacao_id);

ALTER TABLE kalmanordem
    ADD COLUMN modulo VARCHAR(8) NOT NULL DEFAULT 'KALMAN' AFTER data;

ALTER TABLE kalmanordemevento
    ADD COLUMN modulo VARCHAR(8) NOT NULL DEFAULT 'KALMAN' AFTER ordem_id;

ALTER TABLE kalmanordempub
    ADD COLUMN modulo VARCHAR(8) NOT NULL DEFAULT 'KALMAN' AFTER tipo,
    ADD KEY ix_kpub_modulo (modulo, seq);

ALTER TABLE kalmanordemusuario
    ADD COLUMN modulo VARCHAR(8) NOT NULL DEFAULT 'KALMAN' AFTER uid;

ALTER TABLE kalmanordemusuariohist
    ADD COLUMN modulo VARCHAR(8) NOT NULL DEFAULT 'KALMAN' AFTER uid;
