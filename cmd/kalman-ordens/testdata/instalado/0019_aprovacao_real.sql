-- SPEC-TECNICA-REAL-PELA-TELA-F1 §1 (16/11): no REAL, a linha da kalmanordemusuario é a aprovação do
-- admin pela tela (estado, conta, corretora e conector aprovados, versão, quem aprovou); o histórico
-- continua na kalmanordemusuariohist, só INSERT. As linhas do SIMULADOR ficam com estado ''.
-- D5: nenhuma habilitação antiga do REAL vale (1450, 5725 e qualquer outra): todas SEM_APROVACAO e
-- habilitado=0, com uma linha MIGRACAO no histórico. IF NOT EXISTS e WHERE estado='': aplicada de
-- novo, não muda nada nem duplica o histórico.

ALTER TABLE kalmanordemusuario
    ADD COLUMN IF NOT EXISTS estado VARCHAR(20) NOT NULL DEFAULT '' AFTER conectores,
    ADD COLUMN IF NOT EXISTS corretora INT NULL AFTER estado,
    ADD COLUMN IF NOT EXISTS conta VARCHAR(20) NULL AFTER corretora,
    ADD COLUMN IF NOT EXISTS versao INT NOT NULL DEFAULT 0 AFTER conta,
    ADD COLUMN IF NOT EXISTS aprovado_por VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS aprovado_em DATETIME(3) NULL,
    ADD COLUMN IF NOT EXISTS aprovado_motivo VARCHAR(200) NULL,
    ADD COLUMN IF NOT EXISTS gq_conector VARCHAR(10) NULL,
    ADD COLUMN IF NOT EXISTS gq_corretora INT NULL,
    ADD COLUMN IF NOT EXISTS gq_conta VARCHAR(20) NULL,
    ADD COLUMN IF NOT EXISTS divergente_desde DATETIME(3) NULL,
    ADD COLUMN IF NOT EXISTS origem VARCHAR(10) NOT NULL DEFAULT '';

ALTER TABLE kalmanordemusuariohist
    ADD COLUMN IF NOT EXISTS estado VARCHAR(20) NOT NULL DEFAULT '' AFTER conectores,
    ADD COLUMN IF NOT EXISTS corretora INT NULL AFTER estado,
    ADD COLUMN IF NOT EXISTS conta VARCHAR(20) NULL AFTER corretora,
    ADD COLUMN IF NOT EXISTS versao INT NOT NULL DEFAULT 0 AFTER conta,
    ADD COLUMN IF NOT EXISTS aprovado_por VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS aprovado_em DATETIME(3) NULL,
    ADD COLUMN IF NOT EXISTS aprovado_motivo VARCHAR(200) NULL,
    ADD COLUMN IF NOT EXISTS gq_conector VARCHAR(10) NULL,
    ADD COLUMN IF NOT EXISTS gq_corretora INT NULL,
    ADD COLUMN IF NOT EXISTS gq_conta VARCHAR(20) NULL,
    ADD COLUMN IF NOT EXISTS divergente_desde DATETIME(3) NULL,
    ADD COLUMN IF NOT EXISTS origem VARCHAR(10) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS evento VARCHAR(20) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS estado_antes VARCHAR(20) NULL,
    ADD INDEX IF NOT EXISTS ix_kouh_uid_amb (uid, ambiente, id);

INSERT INTO kalmanordemusuariohist (uid, ambiente, habilitado, posicao_max, qtd_max_ordem, conectores,
        estado, estado_antes, evento, origem, versao, alterado_por, motivo, alterado_em)
    SELECT uid, ambiente, 0, posicao_max, qtd_max_ordem, conectores, 'SEM_APROVACAO',
           IF(habilitado=1, 'HABILITADO_V134', 'DESABILITADO_V134'), 'MIGRACAO', 'MIGRACAO', versao+1,
           'migracao-0019', 'SPEC-REAL-PELA-TELA-F1 D5: nenhuma habilitacao antiga vale', UTC_TIMESTAMP(3)
    FROM kalmanordemusuario WHERE ambiente='REAL' AND estado='';

UPDATE kalmanordemusuario SET habilitado=0, estado='SEM_APROVACAO', origem='MIGRACAO', versao=versao+1,
       alterado_por='migracao-0019', motivo='SPEC-REAL-PELA-TELA-F1 D5: nenhuma habilitacao antiga vale', alterado_em=UTC_TIMESTAMP(3)
    WHERE ambiente='REAL' AND estado='';
