# SPEC kalman-ordens v1.29 — a conferência do webhook por evento e o /destino sem cache (30/10/2026)

Base: v1.28 (`718eefec6b7a`, instalada em 30/10 às 12:18). Pedidos de 30/10. As decisões D2, D4 e D5
de ambientes estão na SPEC-AMBIENTES-v1 §7.3 e §8.

## 1. A conferência do cadastro do webhook por evento

Na produção o cadastro é estável; a limpeza noturna é do simulador. A conferência periódica fica
**desligada por padrão**: `FLEXSCAN_WEBHOOK_CONFERIR_MIN=0`. Um valor maior que 0 liga a periódica,
como antes.

A conferência sai nestes eventos, sempre dentro da agenda da v1.28 §1 (fora da janela e sem ordem
viva, a ponte não chama a Flexscan):

| evento | quando |
|---|---|
| **abertura** | `FLEXSCAN_CONFERIR_ABERTURA` (08:30), uma por dia de pregão |
| **depois de cada login** | partida, reconexão, renovação, depois de um 401. O login da partida fora da janela fica só no login (v1.28): a conferência pendente sai quando a ponte ficar ativa |
| **mudança espontânea sem webhook** | a consulta viu uma mudança (fill, stop executado, cancelamento) e o webhook dela não veio em `FLEXSCAN_WEBHOOK_ESPERA_S` (5 s). No máximo uma a cada minuto |
| **N esperas seguidas sem webhook** | `FLEXSCAN_WEBHOOK_FALTAS_N` (3): as ações nossas (POST/PUT/DELETE com 202) e as mudanças sem webhook, seguidas. Junto vem o `FLEXSCAN_WEBHOOK_SEM_CHEGAR` (crítico, como na v1.22). Sai sempre, sem o espaçamento |

O motivo vai no log (`flexscan: conferência do webhook por=...`), e o tráfego continua com a linha
`WEBHOOK_CADASTRO` de cada conferência e recadastro.

## 2. O `/usuarios/{uid}/destino` lê a configuração atual

O `/destino` só é chamado ao abrir a tela e ao ligar "Enviar ordens".
- **Ele lê do banco, na hora, a habilitação** (os dois ambientes) **e a configuração do GQ**
  (`acusuario`), sem o cache (`CACHE_USUARIOS_S`).
- O que ele leu atualiza o cache da entrada: a ordem logo depois usa a mesma configuração que a tela
  mostrou.
- Com o banco ilegível, os ambientes pedidos voltam com `CONFIG_USUARIO_INDISPONIVEL`, e o cache
  fica como estava.

Roteiro 123: o GQ troca o destino sem avisar o serviço (o verbo novo `gq`, com a recarga do cache de
hora em hora). O `/destino` mostra a troca, e a entrada dá o mesmo código: paper, conta fora da
trava, e de volta.

## 3. A máquina sem o SIMULADOR (a .51, D2 revisada)

Ver a SPEC-AMBIENTES-v1 §7.3, D2. No `/destino` do SIMULADOR, numa máquina com
`AMBIENTES_PERMITIDOS=REAL`:
- **um usuário paper** dá `DESTINO_PAPER` ("Simulador · paper"), não `AMBIENTE_NAO_PERMITIDO`;
- **uma configuração que aponta para um conector** dá `AMBIENTE_NAO_PERMITIDO` com o motivo "no
  SIMULADOR todo usuario e paper: esta maquina nao roda o SIMULADOR e o simulador da Flexscan e so
  para os testes da .53; ponha o destino do usuario no paper do GQ".

Roteiro 124 (a .51: só a instância REAL).

## 4. Configuração

| chave | padrão | o quê |
|---|---|---|
| `FLEXSCAN_WEBHOOK_CONFERIR_MIN` | `0` (era 10) | a conferência periódica; 0 = só por evento |
| `FLEXSCAN_WEBHOOK_FALTAS_N` | `3` | esperas seguidas sem webhook: `FLEXSCAN_WEBHOOK_SEM_CHEGAR` e a conferência na hora |
