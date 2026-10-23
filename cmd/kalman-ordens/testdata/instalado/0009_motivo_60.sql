-- v1.12: motivo_saida (kalmanordemoperacao) e motivo_gestor (kalmanordem) de VARCHAR(30) para
-- VARCHAR(60). HML 19/10 10:41: o motivo de 41 caracteres de um encerrar do admin travou o gravador
-- por 5,5 min (Error 1406, sql_mode estrito); na .53 as duas colunas já foram alteradas à mão
-- (10:47:21) — o MODIFY é idempotente. utf8mb4: 60 × 4 = 240 bytes, o mesmo prefixo de 1 byte
-- (ALGORITHM=INPLACE, sem cópia da tabela). A API recusa motivo maior que a coluna (CAMPO_INVALIDO).

ALTER TABLE kalmanordemoperacao MODIFY motivo_saida VARCHAR(60) NULL, ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE kalmanordem MODIFY motivo_gestor VARCHAR(60) NULL, ALGORITHM=INPLACE, LOCK=NONE;
