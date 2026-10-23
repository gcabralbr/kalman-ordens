-- v1.2: conta, destino e símbolo vêm da operação (congelados na entrada), não do service.env;
-- habilitação por usuário, alterada só pela CLI (substitui ORDENS_UID_REAL).

ALTER TABLE kalmanordemoperacao
    ADD COLUMN conta   VARCHAR(20) NULL AFTER uid,
    ADD COLUMN destino INT         NULL AFTER conta,
    ADD COLUMN simbolo VARCHAR(20) NULL AFTER destino;

ALTER TABLE kalmanordem
    ADD COLUMN conta   VARCHAR(20) NULL AFTER papel,
    ADD COLUMN simbolo VARCHAR(20) NULL AFTER conta;

-- Habilitação atual por uid. posicao_max/qtd_max_ordem = 0: vale só o limite global.
CREATE TABLE kalmanordemusuario (
    uid              INT           NOT NULL,
    habilitado       TINYINT       NOT NULL,
    posicao_max      INT           NOT NULL DEFAULT 0,
    qtd_max_ordem    INT           NOT NULL DEFAULT 0,
    alterado_por     VARCHAR(64)   NOT NULL,
    motivo           VARCHAR(200)  NOT NULL,
    alterado_em      DATETIME(3)   NOT NULL,
    PRIMARY KEY (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Histórico de toda alteração (só INSERT).
CREATE TABLE kalmanordemusuariohist (
    id               BIGINT        NOT NULL AUTO_INCREMENT,
    uid              INT           NOT NULL,
    habilitado       TINYINT       NOT NULL,
    posicao_max      INT           NOT NULL,
    qtd_max_ordem    INT           NOT NULL,
    alterado_por     VARCHAR(64)   NOT NULL,
    motivo           VARCHAR(200)  NOT NULL,
    alterado_em      DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    KEY ix_kouh_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
