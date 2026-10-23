-- v1.4 K1: ajuste da proteção pelo gestor (POST /operacoes/{id}/stop). O gatilho pedido fica na
-- operação: proteção nova, recriada ou ajustada pela quantidade usa ele, não o stop_pts da
-- entrada — o ajuste sobrevive a restart e conciliação. NULL = sem ajuste (vale o stop_pts).
-- Só ALTER (o usuário do banco não tem DROP).

ALTER TABLE kalmanordemoperacao
    ADD COLUMN stop_ajustado BIGINT NULL AFTER stop_pts;
