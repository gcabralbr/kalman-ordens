# SPEC kalman-ordens v1.14 — proteção contra falha de fonte (21/10/2026)

Base: v1.13. Origem: 20/10 e 21/10, o AggrBook da HML degenerou (Aggregated:false, sem limite de
níveis) e o /home encheu (ESTADO 20/10 e 21/10). Spec do usuário em `docs/ESTADO-2026-10-21.md` §4b;
itens 1 e 4 (pnt-md-hml) já na leva 1. Esta versão: itens 2 e 3.

## 1. FONTE_DEGRADADA (item 2)

- **Alerta crítico `FONTE_DEGRADADA`** (repete a cada `ALERTA_REPETICAO_S`, `RESOLVIDO` na volta)
  quando a fonte de preço fica ruim por mais de `FONTE_DEGRADADA_S` (30), dentro de `FONTE_JANELA`
  (`09:00-18:25`, configurável; fim excluído). Fora da janela não alerta e o que estiver aberto se
  resolve.
- **Ruim**, do que o kalman-ordens vê no `/precos/atual`: sem resposta; `{}` (no pnt-md-hml: book
  degenerado ou atraso acima da base); 404/400; em negociação (status84 0): sem bid/ask, bid ≥ ask ou
  idade acima de `PRECO_IDADE_MAX_S`. **Leilão (status84 ≠ 0) com preço, bid ou ask não conta**; leilão
  sem nada conta.
- **Motivo da própria fonte (opcional):** `PRECOS_SAUDE_URL` (o `/saude` do pnt-md-hml, só na HML),
  lido a cada 5 s numa goroutine própria (o motor nunca espera HTTP); com a fonte indisponível, o
  `motivo`, o `book_motivo` e as reconexões seguidas entram no texto. O marketdata de produção não tem
  esse `/saude`: lá fica vazio.
- **/saude:** `fonte` = `{degradada, ruim_desde, motivo, janela}`; o alerta também em
  `alertas_criticos`. O topo da tela de admin é do lado do Kalman.
- **Entradas** continuam recusadas pelas regras de preço de sempre (`SEM_PRECO`, `PRECO_VELHO`,
  `BOOK_INVALIDO`, `LEILAO`); o alerta só avisa.
- **Envio para fora:** o Telegram que já existe (`TELEGRAM_TOKEN`/`TELEGRAM_CHAT`); na .53, quando o
  chat próprio for criado. E-mail fora.

## 2. Monitor de disco da .53 (item 3)

Fora do serviço (funciona com os serviços parados): `deploy/disco-alerta.sh` pelo
`kalman-disco-alerta.timer` (a cada 5 min, `DynamicUser`), `/` e `/home`: `DISCO_80` aviso,
`DISCO_90` crítico; manda na mudança de nível e, enquanto durar, no máximo a cada `DISCO_REPETIR_MIN`
(30); `RESOLVIDO` na volta abaixo de 80%. Telegram direto (`curl`), configuração em
`/etc/kalman-ordens/disco.env`; sem token/chat, só no journal; falha de envio sai com erro e tenta na
próxima execução. Os códigos `DISCO_*` não são do catálogo do serviço (não passam pelo kalman-ordens).

## 3. Testes

- Roteiro 94: `{}` por 30 s (29 s não alerta; 31 s alerta; repetição sem novo alerta; volta →
  RESOLVIDO); soluços de 20 s não somam; leilão com preço 61 s sem alerta; leilão sem nada, preço
  velho e book cruzado alertam; fora da janela (encurtada para 09:00-11:10 no roteiro) não alerta.
- `preco.TestMotivoFonte` (o `/saude` do pnt-md-hml), `config.TestIntervalo`.
- `scripts/teste-disco-alerta.sh`: níveis, repetição de 30 min, dois pontos, RESOLVIDO, Telegram fora
  do ar (não marca como enviado), sem Telegram, `df` real.
