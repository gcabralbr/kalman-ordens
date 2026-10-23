-- SPEC-FLEXSCAN-v1 §2 item 1: conectores de execução permitidos a cada uid. O destino continua vindo
-- do GQ (Robo.CORSeqId: 85 PnT, 88 Flexscan); a entrada só passa se o conector dele estiver aqui.
-- As linhas existentes ficam só com o PnT (o comportamento de antes).

ALTER TABLE kalmanordemusuario
    ADD COLUMN conectores VARCHAR(40) NOT NULL DEFAULT 'PNT' AFTER qtd_max_ordem;

ALTER TABLE kalmanordemusuariohist
    ADD COLUMN conectores VARCHAR(40) NOT NULL DEFAULT 'PNT' AFTER qtd_max_ordem;
