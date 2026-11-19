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

**Respostas do George ao §5 (19/11, SPEC aprovada):**
1. Os tempos: 1 s para o disparo sem fill e 500 ms de confirmação pelo livro.
2. A saída: limitada larga (bid − 300), com repreço a cada 1 s enquanto houver posição.
3. A trava fica.
4. `FOLGA_STOP_PTS=200` já, como medida provisória. Entrou em 19/11, às 14:28:56, pelo F7 do
   `REAL-2026-11-19-flexscan-5725.md`.
5. A FX-11 vai junto com a FX-9 ampliada.

## 6. E1: a medição (19/11, 14:37–14:52, só leitura)

Fontes:
- o HTTP local: `/operacoes?dia=` de 14/10 a 19/11 e `/operacoes/{id}/historico?cru=1` de cada stop
  executada;
- os ticks gravados pelo Kalman (`/var/lib/kalman/tick/winfut-*.csv*`, 68 dias de pregão, de 11/08 a
  19/11; último a cada ~100 ms; bid/ask só desde 01/10);
- a gravação da 9090 de 26/10 (`var/precos-9090-20261026.jsonl`, a única).

Os scripts são de análise, ficaram fora do repositório e não mudaram nada.

### 6.1 O PnT: do disparo da 1014 ao fill

61 stops executadas (`origem_saida STOP_SERVIDOR`): 18 do PnT (4 no REAL, 14 na HML) e 43 da Flexscan
(1 no REAL, 42 no apisim).

| onde | o 39=B de disparo (20807 > 0, "[DSA-122]") | do 39=B ao fill |
|---|---|---|
| **REAL (4: 13, 17, 18 e 19/11)** | **nunca veio.** Só o 39=B "Delaying final state 'Finished'", sem o 20807, que já é o robô fechando a ordem | 254, 274, 497 e 531 ms do "Delaying" ao ER de fill |
| HML (14, mercado D-1) | 9 vezes | 170, 246, 486, 492, 497, 516, 730, 973 e **1.501 ms** (a saída por replace de 19/10). E os 9.346 ms do furo de 21/10 |

**Consequência para o D1(a):** com 1 s, a regra só pega na HML, onde daria 1 falso positivo em 9 (o
de 1.501 ms, e o de 973 ms passaria raspando). **No REAL ela não arma**, porque o robô BTG_ALGO não
manda o disparo explícito. No REAL, o PnT depende do D1(b), o do livro, como a Flexscan. O código fica
como aprovado (1 s, quando o 39=B com 20807 vier). Entra nas perguntas ao PnT: por que o robô do REAL
não manda o 39=B "[DSA-122]" que a HML manda?

### 6.2 O deslizamento das stops executadas (preço da execução × gatilho vigente)

Sinal: positivo = pior que o gatilho.

| onde | n | deslizamento | além do limite? |
|---|---|---|---|
| **REAL, PnT** | 4 | +20, −5, −10 e −45 | nenhuma |
| **REAL, Flexscan** (19/11, op 1119124417) | 1 | −5 | não |
| apisim, Flexscan | 42 | de +5 a +20, quase sempre +5 (o simulador executa 1 tick pior) | nenhuma |
| HML, PnT | 14 | de −325 a +20. Os três extremos são saídas por replace ou o livro da HML | nenhuma |

**Nenhuma stop executada passou do limite**, e nenhuma pulou: as 3 "STOP_FURADO" de 15/10 foram falso
positivo da fonte (§2). No REAL, o pior deslizamento foi de 20 pts.

### 6.3 Quanto o preço anda além de um gatilho no 1º segundo

Método:
- **O gatilho:** para cada tick em que o último cruza um nível, o gatilho hipotético é o nível logo
  depois do último anterior (G = P_ant ∓ 5).
- **A medida:** quanto o último vai além de G dentro da janela.
- **O que entra:** só o pregão contínuo (`status84` 0).
- **Os episódios:** eventos a menos de 30 s um do outro contam como um.
- **Uma cota de cima:** um episódio conta se *algum* nível foi atravessado. Uma stop só pula se estiver
  naquele nível.

| janela | medida | > 100 | > 150 | > 200 | > 300 |
|---|---|---|---|---|---|
| 1 s | % dos 7,7 milhões de cruzamentos | 0,30% | 0,098% | 0,043% | 0,012% |
| **0,3 s** (a reação de uma stop na bolsa ou do robô) | episódios em 66 dias (sem 05/10 e 26/10) | 338 (5,1/dia) | 139 (2,1/dia) | **79 (1,2/dia)** | 25 (0,4/dia) |
| 1 s | episódios em 66 dias | 1.107 (16,8/dia) | 281 (4,3/dia) | 139 (2,1/dia) | 58 (0,9/dia) |

**Fora da conta, dois dias:**
- **26/10, a ampliação do limite:** 10.420 pts de uma vez. Nenhuma folga resolve, e o caso é da
  §14.5.
- **05/10:** 1.520 pts num segundo.

O livro (bid/ask) dá números maiores e mais ruidosos (p99,9 de 920 pts, com bid/ask velho ou cruzado
na fonte). Por isso a medida principal é o último. O D1(b) usa o livro **e** o último, com o livro
validado (bid ≤ ask, idade ≤ 1 s).

### 6.4 A folga: 200 se confirma

- **De 100 para 200:**
  - os cruzamentos de 1 s em que o preço passa da folga caem de 0,30% para 0,043% (7 vezes menos);
  - os episódios da janela de 0,3 s caem de 5,1 para 1,2 por dia (4 vezes menos).
- **De 200 para 300:** cai mais 3 vezes, a 0,4 por dia.
- **O custo da folga maior** só aparece quando o preço de fato atravessa o gatilho com gap: o fill
  pode sair até 200 pts além do gatilho, contra 100 (com 1 WIN, R$ 40 contra R$ 20). No movimento
  normal, o fill não muda (o deslizamento real medido ficou em ±45 pts).
- **Recomendação:** manter **200**. O que sobra é coberto pelo D1/D2: a saída agressiva em ~1 s. O
  300 só se, depois da E2, os alertas de `STOP_FURADO` aparecerem mais de uma vez por semana.

### 6.5 O que muda na E2 por causa da E1

- **O D1(b) é o caminho principal nos dois conectores**, porque no REAL o PnT não manda o disparo
  explícito.
- **Um roteiro novo:** o PnT do REAL sem o 39=B de disparo, só com o "Delaying final state", e o
  preço além do limite. O furo tem de vir pelo livro, em 500 ms.
- **A pergunta ao PnT/BTG** sobre o 39=B "[DSA-122]" do robô do REAL entra no próximo ESTADO.
- **A FX-11** tem roteiro próprio: `docs/SONDAGEM-FX11-flexscan.md`.
