-- kalman-ordens — esquema inicial (MariaDB dbecotools).
-- Horários em America/Sao_Paulo (hora local do pregão), com milissegundos.

-- Operação real: uma linha por operacao_id do gestor. Guarda o estado da
-- máquina da Parte 4 e serve de chave de idempotência do POST /ordens.
CREATE TABLE kalmanordemoperacao (
    id               BIGINT       NOT NULL AUTO_INCREMENT,
    data             DATE         NOT NULL,
    operacao_id      BIGINT       NOT NULL,
    uid              INT          NOT NULL,
    lado             CHAR(1)      NOT NULL,          -- C | V (lado da entrada)
    qtd_pedida       INT          NOT NULL,
    stop_pts         DECIMAL(12,2) NOT NULL,
    estado           VARCHAR(32)  NOT NULL,
    motivo_entrada   VARCHAR(20)  NULL,
    ts_decisao       VARCHAR(40)  NULL,
    motivo_saida     VARCHAR(20)  NULL,
    origem_saida     VARCHAR(16)  NULL,              -- GESTOR | STOP_SERVIDOR | PARADA | SERVICO
    saida_pedida_em  DATETIME(3)  NULL,
    divergente       TINYINT      NOT NULL DEFAULT 0,
    pnl_pts          DECIMAL(14,2) NULL,
    criado_em        DATETIME(3)  NOT NULL,
    atualizado_em    DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_kop_operacao (operacao_id),
    KEY ix_kop_data (data)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Ordem: uma linha por ordem no PnT (cada NewOrderSingle). Replace e cancel
-- mudam a mesma linha (clordid_atual). Uma operação pode ter mais de uma
-- ordem do mesmo papel (ex.: nova 1014 depois de rejeição, nova saída depois
-- de uma saída rejeitada).
CREATE TABLE kalmanordem (
    id               BIGINT       NOT NULL AUTO_INCREMENT,
    data             DATE         NOT NULL,
    uid              INT          NOT NULL,
    operacao_id      BIGINT       NOT NULL,
    papel            CHAR(1)      NOT NULL,          -- E | P | S
    clordid_base     VARCHAR(40)  NOT NULL,
    clordid_atual    VARCHAR(40)  NOT NULL,
    orderid37        VARCHAR(40)  NULL,
    secid198         VARCHAR(40)  NULL,
    estrategia       INT          NOT NULL,          -- 847
    lado             CHAR(1)      NOT NULL,          -- C | V
    qtd              INT          NOT NULL,
    preco            DECIMAL(14,2) NULL,
    preco_stop       DECIMAL(14,2) NULL,             -- 20828
    preco_stop_lim   DECIMAL(14,2) NULL,             -- 20829
    estado           VARCHAR(24)  NOT NULL,
    qtd_exec         INT          NOT NULL DEFAULT 0,
    preco_medio      DECIMAL(14,4) NULL,
    motivo_gestor    VARCHAR(20)  NULL,
    motivo_rejeicao  VARCHAR(500) NULL,
    criado_em        DATETIME(3)  NOT NULL,
    atualizado_em    DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_kor_clordid (clordid_base),
    KEY ix_kor_operacao (data, operacao_id, papel),
    KEY ix_kor_orderid (orderid37)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Toda mensagem FIX enviada ou recebida que muda estado.
-- execid único: o mesmo fill nunca é aplicado duas vezes.
CREATE TABLE kalmanordemevento (
    id               BIGINT       NOT NULL AUTO_INCREMENT,
    ordem_id         BIGINT       NULL,
    direcao          CHAR(3)      NOT NULL,          -- ENV | REC
    msgtype          VARCHAR(4)   NOT NULL,
    exectype         CHAR(1)      NULL,              -- 150
    ordstatus        CHAR(1)      NULL,              -- 39
    clordid          VARCHAR(40)  NULL,              -- 11
    orderid37        VARCHAR(40)  NULL,
    execid           VARCHAR(80)  NULL,              -- 17
    lastqty          INT          NULL,              -- 32
    lastpx           DECIMAL(14,4) NULL,             -- 31
    cumqty           INT          NULL,              -- 14
    avgpx            DECIMAL(14,4) NULL,             -- 6
    texto            VARCHAR(1000) NULL,             -- 58
    codigo           VARCHAR(20)  NULL,              -- 102 / 103 / 380
    fix_raw          TEXT         NULL,
    em               DATETIME(3)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_koe_execid (execid),
    KEY ix_koe_ordem (ordem_id),
    KEY ix_koe_em (em)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Eventos publicados ao Kalman (GET /eventos?desde=seq). seq = id.
CREATE TABLE kalmanordempub (
    seq              BIGINT       NOT NULL AUTO_INCREMENT,
    tipo             VARCHAR(24)  NOT NULL,
    operacao_id      BIGINT       NULL,
    dados            TEXT         NOT NULL,          -- JSON
    em               DATETIME(3)  NOT NULL,
    PRIMARY KEY (seq),
    KEY ix_kop_em (em)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Chave/valor: parada, símbolo em uso etc.
CREATE TABLE kalmanordemestado (
    chave            VARCHAR(64)  NOT NULL,
    valor            TEXT         NOT NULL,
    atualizado_em    DATETIME(3)  NOT NULL,
    PRIMARY KEY (chave)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
