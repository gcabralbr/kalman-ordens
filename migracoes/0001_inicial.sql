-- kalman-ordens — esquema inicial. MariaDB 10.11 (produção: 10.11.15 na .51).
-- Schema próprio do serviço (em teste: kalmanordens_teste). Horários em UTC, DATETIME(3);
-- a coluna `data` é o dia do pregão (America/Sao_Paulo).

-- Operação real: uma linha por operacao_id do gestor. Estado da Parte 4, chave de idempotência
-- do POST /ordens e o pedido/resposta originais (reenvio idêntico devolve a resposta original).
CREATE TABLE kalmanordemoperacao (
    id               BIGINT        NOT NULL AUTO_INCREMENT,
    data             DATE          NOT NULL,
    operacao_id      BIGINT        NOT NULL,
    uid              INT           NOT NULL,
    lado             CHAR(1)       NOT NULL,
    qtd_pedida       INT           NOT NULL,
    stop_pts         BIGINT        NOT NULL,
    estado           VARCHAR(32)   NOT NULL,
    motivo_entrada   VARCHAR(20)   NULL,
    ts_decisao       VARCHAR(40)   NULL,
    motivo_saida     VARCHAR(30)   NULL,
    origem_saida     VARCHAR(30)   NULL,
    saida_pedida_em  DATETIME(3)   NULL,
    divergente       TINYINT       NOT NULL DEFAULT 0,
    pnl_pts          DECIMAL(16,4) NULL,
    encerrou_entrada TINYINT       NOT NULL DEFAULT 0,
    contador_n       VARCHAR(200)  NOT NULL DEFAULT '{}',
    pedido_entrada   TEXT          NULL,
    resposta_entrada TEXT          NULL,
    pedido_saida     TEXT          NULL,
    resposta_saida   TEXT          NULL,
    criado_em        DATETIME(3)   NOT NULL,
    atualizado_em    DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_kop_operacao (operacao_id),
    KEY ix_kop_data (data)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Ordem: uma linha por NewOrderSingle. Replace e cancel mudam a mesma linha.
CREATE TABLE kalmanordem (
    id               BIGINT        NOT NULL AUTO_INCREMENT,
    data             DATE          NOT NULL,
    uid              INT           NOT NULL,
    operacao_id      BIGINT        NOT NULL,
    papel            CHAR(1)       NOT NULL,
    clordid_base     VARCHAR(40)   NOT NULL,
    clordid_atual    VARCHAR(40)   NOT NULL,
    orderid37        VARCHAR(40)   NULL,
    secid198         VARCHAR(40)   NULL,
    estrategia       INT           NOT NULL,
    lado             CHAR(1)       NOT NULL,
    qtd              INT           NOT NULL,
    preco            BIGINT        NOT NULL DEFAULT 0,
    preco_stop       BIGINT        NOT NULL DEFAULT 0,
    preco_stop_lim   BIGINT        NOT NULL DEFAULT 0,
    estado           VARCHAR(24)   NOT NULL,
    qtd_exec         INT           NOT NULL DEFAULT 0,
    preco_medio      DECIMAL(16,4) NOT NULL DEFAULT 0,
    motivo_gestor    VARCHAR(30)   NULL,
    motivo_rejeicao  VARCHAR(500)  NULL,
    criado_em        DATETIME(3)   NOT NULL,
    atualizado_em    DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_kor_clordid (clordid_base),
    KEY ix_kor_operacao (data, operacao_id, papel),
    KEY ix_kor_orderid (orderid37)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Toda mensagem FIX enviada ou recebida que muda estado. execid único: o mesmo fill nunca é
-- aplicado duas vezes (NULL não conflita).
CREATE TABLE kalmanordemevento (
    id               BIGINT        NOT NULL AUTO_INCREMENT,
    ordem_id         BIGINT        NULL,
    direcao          CHAR(3)       NOT NULL,
    msgtype          VARCHAR(4)    NOT NULL,
    exectype         CHAR(1)       NULL,
    ordstatus        CHAR(1)       NULL,
    clordid          VARCHAR(60)   NULL,
    orderid37        VARCHAR(40)   NULL,
    execid           VARCHAR(80)   NULL,
    lastqty          INT           NULL,
    lastpx           DECIMAL(16,4) NULL,
    cumqty           INT           NULL,
    avgpx            DECIMAL(16,4) NULL,
    texto            VARCHAR(1000) NULL,
    codigo           VARCHAR(20)   NULL,
    fix_raw          TEXT          NULL,
    em               DATETIME(3)   NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_koe_execid (execid),
    KEY ix_koe_ordem (ordem_id),
    KEY ix_koe_em (em)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Eventos publicados ao gestor (GET /eventos?desde=seq). seq = id.
CREATE TABLE kalmanordempub (
    seq              BIGINT        NOT NULL AUTO_INCREMENT,
    tipo             VARCHAR(24)   NOT NULL,
    operacao_id      BIGINT        NULL,
    dados            TEXT          NOT NULL,
    em               DATETIME(3)   NOT NULL,
    PRIMARY KEY (seq),
    KEY ix_kpub_em (em)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Chave/valor: parada, zeragem do dia.
CREATE TABLE kalmanordemestado (
    chave            VARCHAR(64)   NOT NULL,
    valor            TEXT          NOT NULL,
    atualizado_em    DATETIME(3)   NOT NULL,
    PRIMARY KEY (chave)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
