# SPEC kalman-ordens v1.15 — repetição espaçada dos alertas e reconexão a +45 s (22/10/2026)

Base: v1.14 (`982207f`, instalada na .53 em 22/10 às 08:02). Origem: a abertura de 22/10
(`docs/ESTADO-2026-10-22.md` §5): o `FONTE_DEGRADADA` mandou 4 mensagens em 1m45 e a 1ª reconexão do
pnt-md-hml, a +16 s do Trading, também veio degenerada. Decisões do usuário em 22/10.

## 1. kalman-ordens

- **Repetição dos críticos:** a cada `ALERTA_REPETICAO_S` (30) até `ALERTA_REPETICAO_CURTA_S` (120)
  desde a emissão; depois, a cada `ALERTA_REPETICAO_LONGA_S` (300). Repetições em +30, +60, +90,
  +120 s e então +420, +720 s… Uma emissão nova depois do `RESOLVIDO` recomeça nos 30 s.
- **Texto do `RESOLVIDO`:** sem o código repetido quando a chave é o próprio código
  (`RESOLVIDO: FONTE_DEGRADADA`, não `FONTE_DEGRADADA: FONTE_DEGRADADA`); com chave própria, continua
  `<código>: <chave>`.

## 2. pnt-md-hml

- **1ª reconexão de uma sequência** só `PNT_MD_RECONECTAR_PRIMEIRA_S` (45) depois da entrada em
  negociação (passagem de status84 ≠ 0 para 0 vista pelo serviço; o relógio atravessa as reconexões).
  Trading já no 1º status visto (serviço subido com o pregão aberto): sem espera. Um leilão no meio do
  dia reinicia a espera. Continua valendo o book degenerado por `PNT_MD_RECONECTAR_S` (10).
- **Seguintes:** no máximo uma a cada `PNT_MD_RECONECTAR_INTERVALO_S` (**30**, era 60); alerta crítico
  na 5ª seguida, como antes.
- Nas gravações: 22/10 → 09:01:44 (+45 s) e 09:02:15; 21/10 → 09:01:18, 09:01:48, 09:02:19 (book normal
  na conexão das 09:02:27); 20/10 (amostra até 09:01:24 e o restart das 10:45) e 19/10 → nenhuma.

## 3. Instalação

Fora da janela 09:00–14:30: restart do kalman-ordens (binário novo) e do pnt-md-hml (código do `main`,
`tools/pnt-md-hml`). Sem chave nova obrigatória no `service.env` (os padrões são os decididos).
