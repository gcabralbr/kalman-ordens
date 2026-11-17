-- SPEC-TECNICA-v1.36-CONTA-LIVRE §1 (16/11): a conta é livre (D6) e o estado CONTA_DIVERGENTE da
-- v1.35 não existe mais. A linha REAL que estiver nele volta a APROVADO (habilitado=1): o admin
-- aprovou o uid, e o que o tirou do estado foi a conta, que deixou de importar. O GQ visto e o
-- divergente_desde ficam NULL; as colunas não saem (a v1.36 só deixa de usá-las). Uma linha
-- MIGRACAO no histórico por linha mudada. Sem mudança de esquema; WHERE estado='CONTA_DIVERGENTE':
-- aplicada de novo, não muda nada nem duplica o histórico.

INSERT INTO kalmanordemusuariohist (uid, ambiente, habilitado, posicao_max, qtd_max_ordem, conectores,
        estado, estado_antes, evento, origem, versao, corretora, conta, aprovado_por, aprovado_em, aprovado_motivo,
        alterado_por, motivo, alterado_em)
    SELECT uid, ambiente, 1, posicao_max, qtd_max_ordem, conectores, 'APROVADO', 'CONTA_DIVERGENTE', 'MIGRACAO', 'MIGRACAO', versao+1,
           corretora, conta, aprovado_por, aprovado_em, aprovado_motivo,
           'migracao-0020', 'v1.36: a conta e livre (D6); CONTA_DIVERGENTE extinto', UTC_TIMESTAMP(3)
    FROM kalmanordemusuario WHERE ambiente='REAL' AND estado='CONTA_DIVERGENTE';

UPDATE kalmanordemusuario SET habilitado=1, estado='APROVADO', origem='MIGRACAO', versao=versao+1,
       gq_conector=NULL, gq_corretora=NULL, gq_conta=NULL, divergente_desde=NULL,
       alterado_por='migracao-0020', motivo='v1.36: a conta e livre (D6); CONTA_DIVERGENTE extinto', alterado_em=UTC_TIMESTAMP(3)
    WHERE ambiente='REAL' AND estado='CONTA_DIVERGENTE';
