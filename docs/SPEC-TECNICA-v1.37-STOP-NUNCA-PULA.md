# SPEC técnica v1.37 (proposta): a stop nunca pula

19/11/2026. **Proposta para o ok do George, não implementada.** Vale para os dois conectores (PnT e
Flexscan). O requisito, nas palavras dele: uma saída pior por mercado rápido é aceitável; uma posição
que continua aberta porque o preço atravessou o gatilho e o limite da stop-limite não é.

## 1. Como é hoje (v1.36.2, `91df575bcc22`)

A proteção é uma stop-limite nos dois conectores. O gatilho é o preço médio ∓ `stop_pts` (ou o
ajustado), e o limite é o gatilho ∓ `FOLGA_STOP_PTS`, padrão 100 (`internal/motor/protecao.go:198-212`,
`internal/config/config.go:202`):
- **no PnT**, a 1014 do robô DSA;
- **na Flexscan**, uma stop-limite nativa na B3 (`ordType 4`).

Quando o preço passa do gatilho e do limite de uma vez, a stop dispara e vira uma limitada no limite
que fica parada fora do preço. **A corretora não faz nada** (a ordem fica viva). **O Kalman não age**:
no PnT ele mostra o aviso do `PROTECAO_DISPARADA` (`KalmanWIN/ordens_reais.py:2763`). **Quem age é o
serviço**, em `verificarFuro`, chamada a cada tick (100 ms) com a operação em `PROTEGIDA`
(`internal/motor/tick.go:203`, `:260-289`). Ele tem duas portas:

| porta | conector | regra | tempo até agir |
|---|---|---|---|
| **o disparo explícito** | só no PnT | o 39=B com 20807>0 (`observarDisparo`, `internal/motor/recebe.go:309-333`, que publica o `PROTECAO_DISPARADA`) e nenhum fill em `ESPERA_STOP_S` | **5 s** depois do 39=B (`config.go:208`) |
| **a reserva por preço** | os dois (na Flexscan é a **única**: o retrato vai de 3 para 5 sem sinal de disparo, SPEC-FLEXSCAN §14.5) | o **último** além do limite por `ESPERA_STOP_S` | **5 s** com o último além do limite. Só conta com preço confiável: `status84` 0 e idade ≤ `PRECO_IDADE_MAX_S` 3 s (`internal/motor/entrada.go:572-577`). Em leilão, ou com preço velho, a reserva não conta |

**O que ele faz** (`stopFurado`, `tick.go:291-299`):
1. emite o `STOP_FURADO`, crítico e com Telegram (`internal/alerta/alerta.go:27`);
2. passa a `CANCELANDO_PROTECAO` e cancela a stop;
3. **só com o cancelamento confirmado** manda a saída (`tick.go:204-216`).

A saída é uma limitada no bid − `COLAR_SAIDA_PTS` (50) × k, com repreço a cada `TIMEOUT_SAIDA_S` (2 s)
até `MAX_REPRECO_SAIDA` (3); depois vem o `SAIDA_ESGOTOU`, crítico, e os repreços seguem mais
espaçados (`internal/motor/saida.go:86-95`, `:192-240`).

Na saída por replace do PnT (v1.11), a 1014 disparada sem fill em 5 s cai no mesmo caminho
(`internal/motor/saida_replace.go:175-179`).

**O pior caso de hoje, somado:**
- 5 s da detecção;
- o cancelamento da stop: ~0,5 s na Flexscan, e no PnT até 3,69 s (medido na HML em 19/10);
- uma limitada que, num mercado em queda livre, pode precisar de repreços de 2 s.

**Na prática, de 6 a 12 s com a posição aberta e sem proteção efetiva.**

## 2. Já aconteceu?

Varri as operações de 14/10 a 19/11 pelo `/operacoes?dia=` (223 operações) e o journal desde 20/10.
**No REAL, nunca** (as 5 stops do REAL, 4 PnT e 1 Flexscan, executaram normalmente). Houve 4 casos
fora do REAL, e **nenhum foi um furo de mercado**:

| quando | onde | o que foi |
|---|---|---|
| 15/10, 09:35, 10:00 e 10:13 (ops 234011, 234013, 234015) | SIMULADOR/HML do PnT | **falso positivo da reserva.** A fonte de preço (9090, do mercado real) estava longe do livro da HML: "último 208410 além do limite 208775" com a saída executando a 209345. Detectou 5,1 s depois do `PROTEGIDA` e zerou ~6 s depois, com −20/−30 pts |
| 21/10, 11:24 (op 900000129, KHML) | HML do PnT | a saída por replace disparou a 1014 e não houve fill em 5 s. O `STOP_FURADO` saiu, e a saída executou 4,2 s depois |

**Lição:** a reserva só pelo **último** se engana quando a fonte não é o livro em que a ordem está.
A regra nova precisa de uma confirmação pelo livro (D1).

## 3. A proposta

### D1. A detecção: mais rápida e por duas evidências

A stop está furada quando, com a posição ≠ 0 e a stop disparada ou não:
- **(a) no PnT**, depois do 39=B de disparo, nenhum fill em `FURO_DISPARO_MS`. Proposta: **1.000 ms**
  (hoje são 5 s). A E1 mede, no fio das stops executadas (REAL e SIMULADOR), o tempo do 39=B ao fill,
  para confirmar que 1 s fica bem acima do normal;
- **(b) nos dois conectores**, o lado do livro que executaria a stop está além do limite **e** o
  último também (a venda: bid < limite e último < limite; a compra, espelhado), por `FURO_CONFIRMA_MS`.
  Proposta: **500 ms**, com o preço fresco (idade ≤ 1 s) e `status84` 0.

As duas evidências juntas evitam o falso positivo de 15/10. Com a fonte de preço longe do livro de
verdade, o bid e o último não concordam por 500 ms com a stop parada.

### D2. A ação: saída agressiva imediata, sem a janela sem proteção

**A ordem de preferência:**
1. **Trocar a própria stop pela saída** (um replace/PUT do preço para o agressivo). A ordem que estava
   na bolsa vira a saída: não existe instante sem ordem nem risco de duas vendas.
   - **No PnT:** o replace da 1014 disparada, que é o caminho da v1.11 e já está em produção.
   - **Na Flexscan:** o `PUT /orders/{id}` com `price` na stop disparada, que nessa hora é uma limitada
     em repouso. A razão 4 de 23/10 era sobre o `stopPrice`, não sobre o `price`; **isso precisa da
     pergunta FX-11 e de uma sondagem** (§5).
2. **Se o replace for recusado** (a ordem já terminal, ou "Replacing quantity must be bigger…", como em
   15/10): cancelar e, **com o cancelamento confirmado**, a nova saída. É o caminho de hoje, sem os 5 s.

**O preço:** uma limitada **bem dentro do livro**: bid − `SAIDA_FURO_PTS` na venda (o ask + na compra).
Proposta: **300 pts**, dentro do túnel da B3. Uma ordem a mercado tem o mesmo efeito, mas a limitada
larga põe um teto e respeita o túnel. O repreço vem a cada 1 s, com k crescente e **sem esgotar**:
enquanto a posição for ≠ 0, a saída continua sendo repreçada e o crítico segue aberto.

**A stop na bolsa continua sendo a proteção para quando o serviço cair.** Nada muda antes do furo. No
furo, a própria ordem da bolsa vira a saída agressiva (D2.1). Se o serviço cair no meio, a ordem na
bolsa já é agressiva.

**Leilão e mercado travado ficam como estão:** com `status84` ≠ 0, vale o `SAIDA_EM_LEILAO`; travado no
limite, o `MERCADO_TRAVADO`. É a SPEC-FLEXSCAN §14.5, que segue aberta. Nenhuma saída executa sem o
outro lado.

**O tempo, do preço passar do limite até a saída estar no livro:** ~0,5 s de confirmação, mais um
replace (~0,4–0,5 s). Fica **em torno de 1 s**, contra os 6–12 s de hoje. No PnT com o disparo
explícito: 1 s, mais o replace.

### D3. A folga do limite (hoje 100 pts): aumentar, sim, mas não é a garantia

- **Mudar a folga não pede binário.** O `FOLGA_STOP_PTS` já é uma chave do `service.env`
  (`config.go:202`) e vale para as stops criadas e recriadas depois do restart.
- **Uma folga maior** diminui a chance de pular. Ela só piora o preço quando o mercado de fato atravessa
  o gatilho com gap (no movimento normal, a stop-limite executa no melhor preço disponível, não no
  limite). Com 1 WIN, 100 pts são R$ 20.
- **Proposta:** a E1 mede, nas gravações da 9090 (`var/precos-9090-*.jsonl`) e no fio das stops
  executadas, quanto o preço andou além do gatilho no primeiro segundo depois do disparo. A folga vem
  dessa distribuição, com uma sugestão inicial de **200 pts**, a decidir com os números.
- **A garantia do requisito é D1 + D2. A folga só reduz a frequência.**

### D4. O alerta no Telegram

O `STOP_FURADO` continua **crítico** (Telegram), **um por operação**, com o texto que o George precisa
para agir:
```
[.53 kalman-ordens] CRITICO STOP_FURADO: uid 1450 op 234xxx (PnT, conta 4582351): stop de venda
gatilho 171200 limite 171100 furou (bid 170950, último 170940, há 0,5 s; disparo do PnT há 1,0 s);
saindo agressivo (limite 170650, replace da stop)
```
No fill, o `RESOLVIDO` traz o resultado: "saída a 170900: 200 pts além do limite, resultado −330 pts".
Se a saída não executar em 10 s, vem o `SAIDA_ESGOTOU`, que já existe e também é crítico. É defeito de
mercado, mas é posição real com o preço pior que o combinado, por isso vai ao Telegram.

**Contrato com o Kalman: sem mudança.** A saída continua chegando como `SAIDA_EXECUTADA` e `ZERADA`
com `origem`/`motivo` `STOP_FURADO`, valores que já existem (15/10).

## 4. Etapas

| etapa | o quê |
|---|---|
| **E1** | só medição: o tempo do 39=B ao fill (fio das stops do PnT); o andar do preço além do gatilho (gravações da 9090); a sondagem FX-11 na Flexscan (PUT do `price` numa stop-limite disparada, 1 contrato, no fim de um teste) |
| **E2** | o código de D1 e D2 nos dois conectores. Chaves `FURO_DISPARO_MS`, `FURO_CONFIRMA_MS`, `SAIDA_FURO_PTS` e o repreço de 1 s. Roteiros: o furo do PnT pelo disparo; o furo da Flexscan pelo livro; a fonte quebrada (último além, bid não: **sem** furo, o caso de 15/10); o replace recusado → cancelamento e nova ordem; o leilão; a stop que executa no meio (fill parcial) |
| **E3** | o `ALERTAS.md`, o roteiro de instalação e a folga nova no `service.env`, se aprovada |

## 5. Decisões para o George

1. Os tempos: **1 s** do disparo sem fill (PnT) e **500 ms** de confirmação pelo livro.
2. A saída: **limitada larga** (300 pts dentro do livro) ou a mercado.
3. **Nunca mandar a saída antes de a stop ser trocada ou ter o cancelamento confirmado** (evita a
   posição ao contrário, se a limitada da stop executar depois). Recomendo manter essa trava.
4. A folga do limite: decidir depois da medição da E1 (sugestão inicial: 200).
5. **FX-11 à Flexscan:** o `PUT /orders/{id}` do `price` vale numa stop-limite já disparada? Ela
   aparece como limitada? Vai junto com a FX-9 ampliada.
