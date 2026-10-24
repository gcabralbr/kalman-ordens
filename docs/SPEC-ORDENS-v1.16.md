# SPEC kalman-ordens v1.16 — FONTE_DEGRADADA só em dia de pregão (24/10/2026)

Base: v1.15 + conector Flexscan (SPEC-FLEXSCAN-v1 §14). Origem: sábado 24/10, o `FONTE_DEGRADADA`
alertou a cada 5 min a partir das 09:00, para as duas fontes (pnt-md-hml parado no fim de semana;
9090 sem preço): a `FONTE_JANELA` (09:00-14:30) não olhava o dia.

- A janela vale só em **dia de pregão**: seg–sex, fora dos feriados de `FERIADOS_ARQ`
  (`/var/lib/kalman/feriados.json`, gerado pelo Kalman da `anferiado`) — a regra do horário do
  pnt-md-hml. Fora dele, como fora da janela: nada alerta e o aberto se resolve. Sem o arquivo, ou
  ilegível, vale seg–sex (journal na partida: `feriados dias=N` ou "feriados ilegíveis").
- `/saude`: `fonte.dia_de_pregao` e, fora, `fonte.sem_pregao` (sábado, domingo, `feriado: ...`); o
  mesmo no bloco `flexscan.fonte`.
- Outros alertas por horário: nenhum outro é de agenda. `ZERAGEM`, `VIRADA_COM_OPERACAO` e
  `FIM_DIA_POSICAO` dependem de posição ou operação aberta (valem em qualquer dia); a janela de
  entradas é recusa (`FORA_JANELA`), não alerta; o monitor de disco alerta todo dia, como deve.
- Na .53: o diretório `/var/lib/kalman` é 750 do gammaquant; `sudo chmod o+x /var/lib/kalman` para o
  usuário `kalman-ordens` ler o arquivo (644).
- Roteiros: 107 (sábado), 108 (feriado), 109 (segunda com a fonte ruim: alerta); `pregao.TestDiaDePregao`.
