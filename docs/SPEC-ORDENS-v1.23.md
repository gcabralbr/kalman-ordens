# SPEC kalman-ordens v1.23 — o tráfego da Flexscan vira à meia-noite e consulta dias passados (29/10/2026)

Base: v1.22 (`9dab71f`). Origem: a aba "Flexscan ao vivo" de 29/10 mostrava o 28/10 junto. O buffer
do `/flexscan/trafego` não virava à meia-noite: depois de um restart, ele guardava o dia recarregado e
acumulava os dias seguintes.

## 1. A virada do dia no buffer

- O buffer guarda só o dia corrente, à meia-noite de Brasília.
- A conferência roda a cada linha nova, a cada consulta e na recarga da partida. Ela não depende de
  haver tráfego.
- No dia novo:
  - as linhas de antes da meia-noite saem do buffer (o `kalmanordemevento` guarda tudo, e o dia
    passado sai pelo `?dia=`, §2);
  - `saude.recarregadas` volta a 0, porque as recarregadas eram todas do dia da partida;
  - a latência dos POSTs (`latencia_post_*`) passa a contar só os do dia.
- **O `seq` não volta a 1:** segue a numeração, e um `desde` antigo continua valendo.
- **A recarga da partida** só aceita as linhas do dia corrente.
- **`saude.dia` (novo):** `"AAAA-MM-DD"`, o dia do buffer.

**Para o Kalman:** a aba guarda as linhas que já recebeu. Quando `saude.dia` mudar, ela deve limpar a
lista. O `boot` e o `ultimo_seq` não mudam na virada, então não servem de sinal.

## 2. `GET /flexscan/trafego?dia=AAAA-MM-DD`

- **O dia inteiro, do `kalmanordemevento`:** mesma conversão da recarga da partida (v1.21 §1b),
  então as consultas que não mudaram o estado não aparecem.
- **Teto:** até 50.000 eventos. Acima disso vêm os mais recentes, com `"cortado": true`.
- `desde` e `limite` não valem com `dia`.
- `seq` vai de 1 a N, na ordem do dia.
- `operacao_id` e `modulo` vêm do ClOrdID. O `uid` vem das operações gravadas daquele dia.
- **Sem bloco `saude`:** o estado da ponte só existe para o dia corrente.
- `dia` também pode ser hoje: aí a resposta vem do banco, não do buffer.

```json
{"dia": "2026-10-28", "mensagens": [ ... as linhas, no formato da v1.22 §5.1 ... ],
 "ultimo_seq": 1432, "eventos": 2911, "cortado": false}
```

- **Erros:** `dia` fora do formato dá 400 `{"erro":"dia invalido (AAAA-MM-DD)"}`. Sem a Flexscan, 404,
  como no ao vivo.

## 3. Testes

- `TestTrafegoViradaDoDia`:
  - a recarga descarta a linha da véspera;
  - a consulta sozinha vira o dia;
  - `recarregadas` volta a 0, o `seq` segue e `saude.dia` muda;
  - o buffer cheio esvazia inteiro na virada pela linha nova.
- `TestTrafegoFlexscanDoDia`:
  - só os eventos do dia pedido, numerados de 1 a N;
  - a operação e o dono de cada linha;
  - `ErrDia` para dia inválido, e `ok=false` sem a Flexscan.
