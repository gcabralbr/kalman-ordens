-- SPEC-ORDENS v1.28 §3 (30/10): a operação cujo conector caiu com posição (ou ordem viva) fica
-- SUSPENSA: nada sai por ela até o admin confirmar a posição na corretora. A suspensão sobrevive a um
-- restart. NULL = não suspensa. O estado novo ENCERRADA_FORA cabe no VARCHAR(32) de estado.
-- IF NOT EXISTS: segura em qualquer banco.
ALTER TABLE kalmanordemoperacao
    ADD COLUMN IF NOT EXISTS suspensa_em DATETIME(3) NULL AFTER divergente,
    ADD COLUMN IF NOT EXISTS suspensa_motivo VARCHAR(255) NULL AFTER suspensa_em;
