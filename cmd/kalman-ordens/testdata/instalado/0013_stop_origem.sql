-- SPEC-ORDENS v1.19 (27/10): a origem da stop confirmada na corretora (INICIAL, GESTOR, TRSP,
-- STOP_MANUAL...) e a do último ajuste pedido (para a 1014 recriada). NULL = INICIAL (operações
-- anteriores). IF NOT EXISTS: segura em qualquer banco.
ALTER TABLE kalmanordemoperacao
    ADD COLUMN IF NOT EXISTS stop_origem VARCHAR(20) NULL AFTER stop_ajustado,
    ADD COLUMN IF NOT EXISTS stop_ajustado_origem VARCHAR(20) NULL AFTER stop_origem;

-- O msgtype dos eventos da ponte Flexscan (FLX-POST, FLX-WH, FLX-PRECO, FLX-SEMRESPOSTA, FLX-200...)
-- era cortado em 4 ("FLX-"). Aumentar o VARCHAR dentro de 255 bytes não reescreve a tabela.
ALTER TABLE kalmanordemevento MODIFY msgtype VARCHAR(20) NOT NULL;
