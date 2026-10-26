# SPEC kalman-ordens v1.18 — leilão: o preco da fonte não é referência (26/10/2026)

Base: v1.17 (`MERCADO_TRAVADO`, não instalada; vai junto). Origem: 26/10 09:38, leilão de ampliação
do limite do WIN. A 9090 deu `status84` 3 com `preco` = último negócio (177.215), mas o preço teórico
do leilão era 166.790 (Profit). O teórico não existe no contrato do `/precos/atual`. Pela API direta,
a B3 aceitou alterar, registrar e cancelar durante o leilão (SPEC-FLEXSCAN-v1 §14.5.1). Decisão do
usuário: `SAIDA_LEILAO_PCT` = 10 sobre o último negócio, nunca a mercado.

## Regras, com `status84` ≠ 0 na fonte da operação

1. **`precoConfiavel` sem preço.** Isso tem duas consequências:
   - **Sem stop furado por comparação.** A reserva do `verificarFuro` não compara o limite da stop com
     um preço que não é o de reabertura. O gatilho explícito do PnT (39=B sem fill) continua, porque
     é um disparo real.
   - **Resultado do dia sem preço.** A posição não é marcada no último negócio. O `/saude` mostra só o
     realizado, e a `PERDA_DIARIA` não conta o não realizado, como já acontece com preço velho.
2. **Saída com limite largo.** O limite é `SAIDA_LEILAO_PCT` (10) % sobre o último negócio: venda
   abaixo, compra acima, arredondado ao tick no sentido agressivo. Nunca a mercado. Sem `preco` na
   leitura (leilão de abertura), usa o último guardado do símbolo; sem nenhum, `SAIDA_SEM_PRECO`. O
   leilão executa no preço de reabertura, não no limite.
3. **`SAIDA_EM_LEILAO`, crítico, uma vez por leilão.** O texto traz o `status84`, o limite e o
   percentual. Resolve quando o `status84` volta a 0 ou quando a operação zera. Substitui o
   `SAIDA_PRECO_DEGRADADO` nesse caso.
4. **Sem repreço durante o leilão** e sem contagem para o `SAIDA_ESGOTOU`. Uma saída que já estava no
   livro quando o leilão começou é trocada uma vez para o limite largo.
5. **Repreço normal quando o `status84` volta a 0.** O próximo repreço é o primeiro degrau (bid − 100
   na venda), sobre o book.
6. **Saída por replace da 1014 (PnT) não é usada em leilão.** Vale cancel + limite largo.

Parâmetro novo: `SAIDA_LEILAO_PCT` (inteiro, %, padrão 10; `deploy/service.env.example`).

## O que não muda

- A entrada em leilão continua recusada por `LEILAO`.
- O `FONTE_DEGRADADA` continua quieto em leilão com preço, bid ou ask.
- Mercado travado (`status84` 0, bid = ask ou um lado só) é a v1.17. Lá o motor ainda não deixa de
  cancelar a proteção nem de repreçar (SPEC-FLEXSCAN-v1 §14.5, item 1).

## Testes

Roteiro **111** (`111-leilao-de-ampliacao.fix`), três fases:
1. **Operação protegida no leilão.** `status84` 3, `preco` 129.300 abaixo do limite da 1014 (129.410)
   e bid = ask presos. Nenhum `STOP_FURADO` em 10 s e `resultado_dia_pts` 0. Na reabertura com gap
   (129.000), o furo pela reserva em 5 s, cancel e saída sobre o book.
2. **Saída pedida no leilão.** Limite 117.000 (130.000 − 10%), `SAIDA_EM_LEILAO`, 10 s sem repreço. Na
   reabertura abaixo do limite largo, `RESOLVIDO` e repreço normal (bid − 100).
3. **Saída já no livro** quando o leilão começa. Uma troca para 117.000, nenhuma outra; o fill sai no
   preço de reabertura (126.500).

Mutações conferidas: sem o `status84` no `precoConfiavel`, a fase 1 alerta `STOP_FURADO` no leilão;
sem a regra 4, a fase 2 repreça no leilão. O 16c e o 110 da v1.17 seguem.
