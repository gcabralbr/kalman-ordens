-- SPEC-AMBIENTES-v1 §2 (v1.26): os dois ambientes no mesmo serviço, cada um com a sua sessão. O
-- evento (FIX e Flexscan) grava o ambiente da instância que o trocou: a recarga do tráfego, a
-- contagem de envios e a deduplicação de fill são por ambiente. A chave única do fill passa a
-- (orderid37, execid, ambiente): duas Flexscan (simulador e produção) numeram as ordens cada uma a
-- seu modo, e o mesmo par nas duas é outro evento. As linhas existentes ficam SIMULADOR (a .53 só
-- teve o simulador). Só ALTER (o usuário do banco não tem DROP).

ALTER TABLE kalmanordemevento
    ADD COLUMN IF NOT EXISTS ambiente VARCHAR(10) NOT NULL DEFAULT 'SIMULADOR',
    DROP INDEX IF EXISTS uk_koe_execid,
    ADD UNIQUE KEY uk_koe_execid (orderid37, execid, ambiente);
