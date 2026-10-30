-- SPEC-ORDENS v1.30 (SPEC-AMBIENTES-v1 D4 revisada): no REAL, uma ponte Flexscan por corretora; o
-- evento da ponte leva o canal (o NOME de REAL_FLEXSCAN_CORRETORAS: CMC, BTG...), para o tráfego de
-- cada corretora voltar só com as trocas dela. NULL = a ponte única (antes e fora do modo por
-- corretora). IF NOT EXISTS: segura em qualquer banco.
ALTER TABLE kalmanordemevento ADD COLUMN IF NOT EXISTS canal VARCHAR(10) NULL AFTER ambiente;
