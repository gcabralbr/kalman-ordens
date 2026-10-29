-- SPEC-AMBIENTES-v1 §1.2 e §4.2 (29/10): o ambiente do login do usuário no GQ (SIMULADOR × REAL)
-- escolhe a coluna do GQ por ordem. A operação grava o ambiente da entrada (fixo enquanto houver
-- posição) e a habilitação passa a ser por (uid, ambiente). As linhas existentes ficam SIMULADOR: a
-- .53 só teve contas de simulador. IF NOT EXISTS: segura em qualquer banco.

ALTER TABLE kalmanordemoperacao
    ADD COLUMN IF NOT EXISTS ambiente VARCHAR(10) NOT NULL DEFAULT 'SIMULADOR' AFTER conector;

ALTER TABLE kalmanordemusuario
    ADD COLUMN IF NOT EXISTS ambiente VARCHAR(10) NOT NULL DEFAULT 'SIMULADOR' AFTER uid,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (uid, ambiente);

ALTER TABLE kalmanordemusuariohist
    ADD COLUMN IF NOT EXISTS ambiente VARCHAR(10) NOT NULL DEFAULT 'SIMULADOR' AFTER uid;
