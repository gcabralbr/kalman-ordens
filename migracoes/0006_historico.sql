-- v1.5 R (suporte a reclamação de cliente; tela de admin do Kalman): eventos da sessão FIX que
-- até aqui só existiam no log em arquivo e no journal — logon, logout, queda, logon recusado,
-- ResendRequest — mais as conciliações e os alertas sem operação, para GET /sessao/eventos.
-- Índice por operação nos eventos publicados (GET /operacoes/{id}/historico).
-- Só CREATE/ALTER (o usuário do banco não tem DROP).

CREATE TABLE kalmanordemsessao (
    id               BIGINT        NOT NULL AUTO_INCREMENT,
    tipo             VARCHAR(16)   NOT NULL,
    texto            VARCHAR(1000) NOT NULL,
    em               DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    KEY ix_kses_em (em)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE kalmanordempub
    ADD KEY ix_kpub_operacao (modulo, operacao_id);
