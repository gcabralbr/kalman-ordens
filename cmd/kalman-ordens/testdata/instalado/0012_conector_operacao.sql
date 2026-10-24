-- SPEC-FLEXSCAN-v1 §14.1 (23/10): a operação grava o conector da entrada (PNT ou FLEXSCAN), congelado
-- como a conta e o destino (v1.2 U4); o destino (Robo.CORSeqId) é só a corretora. As operações
-- existentes ficam PNT (o único conector de antes).
-- Em 0012 (e não na 0011): a .53 aplicou a 0011 sem esta coluna (binário 77550906a194, 23/10), e as
-- migrações são controladas pelo número. IF NOT EXISTS: segura em qualquer banco.

ALTER TABLE kalmanordemoperacao
    ADD COLUMN IF NOT EXISTS conector VARCHAR(10) NOT NULL DEFAULT 'PNT' AFTER destino;
