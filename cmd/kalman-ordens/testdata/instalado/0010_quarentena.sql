-- v1.13: quarentena do lote. A linha que o banco recusa por erro de dado (1406, 1366, 1264, 1292,
-- 1048, 1452) não trava mais o gravador: vai para cá (a linha em JSON e o erro), com alerta crítico
-- BANCO_QUARENTENA; o resto do lote grava. Com linha não resolvida, o servir sobe em PARADA até a
-- resolução pela CLI (kalman-ordens quarentena resolver) e o POST /retomar.

CREATE TABLE IF NOT EXISTS kalmanordemquarentena (
    id               BIGINT        NOT NULL AUTO_INCREMENT PRIMARY KEY,
    tabela           VARCHAR(40)   NOT NULL,
    linha            MEDIUMTEXT    NOT NULL,
    erro             VARCHAR(1000) NOT NULL,
    em               DATETIME(3)   NOT NULL,
    resolvida_em     DATETIME(3)   NULL,
    resolvida_por    VARCHAR(64)   NULL,
    resolucao        VARCHAR(200)  NULL,
    KEY ix_kq_pendente (resolvida_em, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- kalmanordempub.tipo de VARCHAR(24) para VARCHAR(40): PROTECAO_RESERVA_REPLACES tem 25 caracteres
-- (utf8mb4: 160 bytes, o mesmo prefixo de 1 byte; sem cópia da tabela).
ALTER TABLE kalmanordempub MODIFY tipo VARCHAR(40) NOT NULL, ALGORITHM=INPLACE, LOCK=NONE;
