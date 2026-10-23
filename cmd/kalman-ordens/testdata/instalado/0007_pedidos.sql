-- v1.6 P1 (suporte a reclamação de cliente): todo POST /ordens e POST /operacoes/{id}/stop fica
-- gravado como chegou e como foi respondido — aceito, recusado (que não cria operação), repetido,
-- corpo ilegível (400) ou sem resposta do motor a tempo (503). É o que mostra ao suporte o que o
-- gestor pediu e o que ouviu de volta. Mesma retenção dos eventos (v1.6 E1).
-- Só CREATE (o usuário do banco não tem DROP).

CREATE TABLE kalmanordempedido (
    id               BIGINT        NOT NULL AUTO_INCREMENT,
    data             DATE          NOT NULL,
    modulo           VARCHAR(8)    NOT NULL DEFAULT 'KALMAN',
    operacao_id      BIGINT        NULL,
    uid              INT           NULL,
    rota             VARCHAR(16)   NOT NULL,
    papel            VARCHAR(8)    NULL,
    http_status      SMALLINT      NOT NULL,
    aceito           TINYINT(1)    NOT NULL,
    codigo           VARCHAR(40)   NULL,
    corpo            TEXT          NOT NULL,
    resposta         TEXT          NOT NULL,
    em               DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    KEY ix_kped_dia_uid (data, uid),
    KEY ix_kped_operacao (modulo, operacao_id),
    KEY ix_kped_em (em)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
