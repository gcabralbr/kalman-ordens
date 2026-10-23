-- v1.10 §6 (POST /sinais): a posição da operação na rajada do sinal — da entrada e da saída —
-- como JSON {sinal_id, k, posicao, tamanho, deslocamento, criterio}. NULL = pedido avulso
-- (POST /ordens). O contador k de cada módulo fica em kalmanordemestado (sinal_k:<modulo>).
-- Só ALTER (o usuário do banco não tem DROP).

ALTER TABLE kalmanordemoperacao
    ADD COLUMN sinal_entrada VARCHAR(255) NULL AFTER resposta_saida,
    ADD COLUMN sinal_saida   VARCHAR(255) NULL AFTER sinal_entrada;
