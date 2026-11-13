# Pergunta à PnT / BTG: latência do robô na DSA (13/11/2026)

Rascunho para enviar. Os números são do teste REAL de 13/11 (`INSTALACAO-2026-11-13-real-pnt-1450.md`
§8.4). O horário do gatilho é o do Profit.

---

**Assunto:** DSA BTG (GAMMAQ2 → PNT_DSA_BTG2): latência de execução do robô BTG_ALGO, 1014 e 1031

Olá,

Em 13/11 fizemos o primeiro teste REAL pela DSA da BTG:
- sessão `GAMMAQ2` → `PNT_DSA_BTG2`, conta 4582351, WINZ26, 1 contrato;
- `20858=BTG_ALGO` e `20859=BTGDAYTRADE`;
- estratégias 1031 (entrada e saída, limitada agressiva) e 1014 (stop loss, com ajuste por G).

As confirmações são rápidas, mas toda execução pelo robô leva de 1 a 2 s. Medimos do envio da
mensagem à chegada do ExecutionReport, no nosso lado:

| etapa | tempo |
|---|---|
| D da 1014 → 39=0 | 0,23 s; 0,33 s; 0,25 s |
| G da 1014 (ajuste do stop) → 39=5 | 0,09 s; 0,13 s |
| D da 1031 agressiva → execução (39=2) | 1,56 s; 0,99 s; 1,36 s |
| G da 1014 levando o gatilho ao preço (saída imediata) → execução | 1,40 s; 1,81 s |
| stop da 1014: o mercado tocou o gatilho → 150=I 39=B → execução | 2,0 s até o 39=B; 2,26 s até a execução |

No stop, o mercado tocou o gatilho de 173835 às 11:40:12.838. A primeira mensagem do robô
(`150=I 39=B`, "Delaying final state") chegou às 11:40:14.827, e a execução às 11:40:15.101, a 173790.
Os ClOrdIDs são `KM-P-234754-1` (o D) e `KM-P-234754-2` (o G do ajuste). Na homologação, em 19/10, o
mesmo stop levou 2,13 s.

Perguntas:

1. **Stop nativo da bolsa.** A DSA da BTG aceita alguma ordem stop executada pela própria B3 (OrdType
   4 com StopPx, por exemplo)? Ou a Simple Exchange Order (1033), que no RoE consta como "Not yet
   implemented on DSA"? Se sim, em que estratégia/OMS e com quais campos?
2. **A 1014 no BTG_ALGO.** Existe uma instância de robô ou um parâmetro de menor latência para o
   disparo da 1014? Por exemplo, o ciclo de verificação do gatilho, o tipo de dado que ele usa
   (negócio a negócio ou amostrado) ou outro RobotID para stops. Podem nos passar a linha do tempo
   interna do `KM-P-234754-2`: o negócio que disparou, o envio da ordem filha e a confirmação da bolsa?
3. **A 1031 agressiva.** Uma 1031 limitada agressiva leva cerca de 1,3 s entre a confirmação e a
   execução. O que acontece nesse intervalo: agendamento, risco pré-negociação do OMS BTGDAYTRADE,
   fila do robô? Há configuração que reduza esse tempo?

Obrigado,
George — GammaQuant
