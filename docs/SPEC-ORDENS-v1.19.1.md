# SPEC kalman-ordens v1.19.1 — `PNT_DESLIGADO` (27/10/2026, para 28/10)

Base: v1.19 (instalada na .53, `6b9a6434796f`). Origem: a partir de 28/10 a HML do PnT fica
desligada (pnt-md-hml e kalman-hml parados, timers desabilitados): o trabalho segue só na Flexscan,
depois PnT de produção. Sem esta chave, o serviço não sobe sem as `FIX_*` e, com elas, mantém a
sessão FIX e a fonte do PnT (o `FONTE_DEGRADADA` crítico todo dia às 09:00, com a 9096 parada).

## `PNT_DESLIGADO=1` (padrão 0)

1. **Sem sessão FIX.** O `servir` não cria a sessão. `FIX_*` e `ORDENS_CONTAS_HML` deixam de ser
   obrigatórias e são ignoradas. Passa a exigir `FLEXSCAN_URL`: sem ela, não sobe. O motor ignora
   qualquer sessão FIX (`Usar` e os retornos da sessão), e nada sai para o PnT.
2. **Sem fonte do PnT.** A fonte única é a da Flexscan: `PRECOS_URL_FLEXSCAN` (ou `PRECOS_URL` sem
   ela), com `PRECOS_SAUDE_URL_FLEXSCAN`. O `PRECOS_URL`/`PRECOS_SAUDE_URL` do PnT (9096) não é lido.
   O `FONTE_DEGRADADA` e o `MERCADO_TRAVADO` que saírem são da fonte da Flexscan. O `/saude` mostra
   essa fonte no `fonte` do topo, e `flexscan.fonte` não aparece (fonte compartilhada, como antes da
   SPEC-FLEXSCAN §14).
3. **Entrada de uid PNT: `CONECTOR_DESLIGADO`** (recusa, sem alerta):
   - uid habilitado só no PnT: logo depois da validação dos campos, antes da conta (sem o
     `CONTA_AMBIENTE` crítico de uma conta do PnT fora da lista);
   - uid habilitado nos dois conectores: quando o GQ escolhe o PnT (depois da conta).
4. **A Flexscan segue igual:** entrada, proteção, ajuste, saída, conciliação pelo login, limites.
5. **`/saude`:** `pnt_desligado: true` e `sessao_logada: false`. O `sessao_logada` continua sendo o
   do PnT: o Kalman mostra "Sessão com a corretora caída" no topo enquanto não ler o
   `pnt_desligado` (pedido ao CC do Kalman: com `pnt_desligado` true, não mostrar essa faixa).

Operação do PnT aberta com a chave ligada: fica sem conector (sem saída, sem conciliação).
**Ligar só com a posição do PnT zerada** (`posicao_por_conector.PNT` 0).

## Testes

- Roteiro **114** (`114-pnt-desligado.fix`): o PnT falso loga e o motor não usa a sessão
  (`sessao_logada` false, nenhuma mensagem de aplicação, sem `SESSAO_*`). O uid 7 recebe
  `CONECTOR_DESLIGADO`. A Flexscan faz entrada, proteção, ajuste TRSP e saída normalmente.
  Mutação conferida: o motor usando a sessão reprova o roteiro.
- `TestServirPnTDesligado` (MariaDB): sobe sem `FIX_*` nem `ORDENS_CONTAS_HML`, não lê o
  `PRECOS_URL` do PnT (servidor que conta as leituras: 0), loga uma vez na Flexscan e recusa o uid
  PNT com `CONECTOR_DESLIGADO`. Sem `FLEXSCAN_URL`, não sobe.
