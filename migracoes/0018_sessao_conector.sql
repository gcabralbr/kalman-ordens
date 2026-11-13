-- SPEC-ADMIN-CONECTORES-v1 §3.2 (v1.34): o evento da sessão (logon, queda, alerta sem operação,
-- conciliação) leva o ambiente da instância, o conector de quem o emitiu (PNT, FLEXSCAN, SERVICO) e
-- o canal (a corretora da ponte Flexscan no REAL). NULL nas linhas antigas: a API infere o conector
-- pelo tipo e pelo código e marca "inferido". IF NOT EXISTS: segura em qualquer banco.
ALTER TABLE kalmanordemsessao
    ADD COLUMN IF NOT EXISTS ambiente VARCHAR(10) NULL AFTER em,
    ADD COLUMN IF NOT EXISTS conector VARCHAR(10) NULL AFTER ambiente,
    ADD COLUMN IF NOT EXISTS canal VARCHAR(10) NULL AFTER conector,
    ADD KEY IF NOT EXISTS ix_kses_em_amb (em, ambiente, conector);
