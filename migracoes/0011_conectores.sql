-- SPEC-FLEXSCAN-v1 §14 (decisão de 23/10): o conector de execução (PNT ou FLEXSCAN) de cada uid vem
-- da habilitação, não do Robo.CORSeqId — a corretora (85 BTG, 88 Capital...) e o conector são
-- independentes. Um conector ativo por uid. As linhas existentes ficam com o PnT (o de antes).
-- A operação grava o conector da entrada (congelado como a conta e o destino, v1.2 U4).

ALTER TABLE kalmanordemusuario
    ADD COLUMN conectores VARCHAR(40) NOT NULL DEFAULT 'PNT' AFTER qtd_max_ordem;

ALTER TABLE kalmanordemusuariohist
    ADD COLUMN conectores VARCHAR(40) NOT NULL DEFAULT 'PNT' AFTER qtd_max_ordem;

ALTER TABLE kalmanordemoperacao
    ADD COLUMN conector VARCHAR(10) NOT NULL DEFAULT 'PNT' AFTER destino;
