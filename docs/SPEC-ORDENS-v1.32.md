# SPEC kalman-ordens v1.32 — o webhook só onde é esperado, o motivo da boleta e a consulta de outro dia (03/11/2026)

Base: v1.31, instalada na .53 em 01/11 (93245099bd13). São três pedidos de 03/11, depois do REAL de
teste pela boleta (pausado: "Conta nao contratada", SPEC-CADASTRO-v1 §0.1):

| # | o quê | estado |
|---|---|---|
| 1 | a ponte sem URL de webhook não espera webhook | **feito** no branch `v1.32` |
| 2 | a CLI da boleta confere o motivo antes do "sim" | **feito** no branch `v1.32` |
| 3 | o `GET /ordens/{id}` de uma op de outro dia lê o banco | **aguardando aprovação** (§3) |

## 1. A ponte sem `FLEXSCAN_WEBHOOK_URL` não espera webhook

**O caso (03/11, 09:04):** a ponte REAL/BTG da .53 roda sem webhook de propósito (D5:
`REAL_FLEXSCAN_BTG_WEBHOOK_URL` vazia, os fills pela consulta). Mesmo assim, 5 s depois do 202 da
boleta saiu `FLEXSCAN_WEBHOOK_ATRASADO` ("POST KM-BOLETA-E-1103090415-1 respondido 202 e nenhum
webhook da ordem em 5s"). O motivo: a espera olhava só se o receptor estava ligado
(`FLEXSCAN_WEBHOOK_ENDERECO`, comum a todas as pontes), não se a ponte cadastra webhook.

**A regra nova:** uma ponte só espera webhook com as três coisas:
- o receptor ligado;
- a URL do cadastro dela (`FLEXSCAN_WEBHOOK_URL`, `REAL_FLEXSCAN_WEBHOOK_URL` ou
  `REAL_FLEXSCAN_<NOME>_WEBHOOK_URL`);
- `FLEXSCAN_WEBHOOK_ESPERA_S` > 0.

**Sem a URL**, a ponte fica só com a consulta:
- não abre a espera depois do 202, nem depois de uma mudança vista pela consulta;
- não dá `FLEXSCAN_WEBHOOK_ATRASADO`, não conta falta e não dá `FLEXSCAN_WEBHOOK_SEM_CHEGAR`;
- não confere o cadastro. Isso já era assim desde a v1.29 e continua.

A rota do receptor continua de pé. Um webhook que chegue nela (de um cadastro feito à mão) é aceito
como antes.

**Consequência:** um webhook cadastrado à mão, com a URL vazia no `service.env`, deixa de ter o alarme
de atraso. Onde se espera webhook, a URL tem de estar no `service.env`. É o caso do SIMULADOR da .53
(`FLEXSCAN_WEBHOOK_URL` preenchida: a conferência da abertura roda).

**Onde se vê:**
- `/saude`, no bloco de cada ponte: `webhook_esperado` (true ou false);
- no journal, na linha "flexscan: conector ligado": `webhook_esperado=`.

**Testes:**
- `TestEsperaWebhook`: o receptor ligado e sem a URL não abre a espera;
- roteiro 127 (REAL/BTG sem a URL): 30 s depois do 202 da limitada, `sem_alerta
  FLEXSCAN_WEBHOOK_ATRASADO` e `sem_alerta FLEXSCAN_WEBHOOK_SEM_CHEGAR`. Conferido: sem a correção, o
  roteiro falha;
- roteiro 101 (com a URL): o webhook mudo continua dando o atraso, as faltas e o crítico.

## 2. A CLI da boleta confere o motivo antes de perguntar

**O caso (03/11, 09:03):** `-motivo "teste REAL 03/11: limitada longe do preco"` tem 41 caracteres. O
serviço recusou com `CAMPO_INVALIDO` ("motivo com 41 caracteres (maximo 20)"), mas a CLI só soube
depois do "sim".

**A regra nova:** a CLI confere o tamanho do motivo **antes** do `/destino` e da pergunta, com os
mesmos limites e o mesmo texto do serviço. Nada é enviado:
- `boleta enviar`: até 20 caracteres (`MaxMotivoEntrada`, a coluna `motivo_entrada`);
- `boleta cancelar` e `boleta encerrar`: até 60 caracteres (`MaxMotivoSaida`).

O serviço continua conferindo; a CLI só antecipa. O uso (`kalman-ordens boleta`) mostra os limites.

**O exemplo corrigido:** o §3.3 do `INSTALACAO-2026-11-01-v1.31.md` passou a usar `-motivo "teste
REAL limitada"` (19 caracteres), com uma nota sobre o limite.

**Teste:** `TestBoletaCLI` cobre o motivo de 41 caracteres (para antes de ler o "sim", nenhum
`POST`) e o `cancelar` com 61 caracteres.

## 3. O `GET /ordens/{id}` de uma op de outro dia lê o banco (para aprovar)

### 3.1 O caso (234449)

O Kalman, ao subir, consulta `GET /ordens/{id}?modulo=KALMAN` (sem `ambiente`) para cada operação
que ele ainda tem como não final. O serviço responde da memória, e a memória só tem as operações do
dia (`CarregarDia(hoje)`). Em 02/11 a 234449, de 30/10 e `ZERADA` no banco, voltou
`{"existe": false}`, e o Kalman alertou "DESCONHECIDA no serviço".

Isso é assim desde as primeiras versões. Os filtros de instância e de ponte da v1.25–v1.31 não
entram no caminho.

### 3.2 A proposta

1. **Primeiro a memória, como hoje.** A operação do dia, acompanhada pela instância, responde como
   sempre.
2. **Fora da memória, o banco.** A chave é `(modulo, operacao_id)`, única em todos os dias
   (`uk_kop_operacao`, 0004), então não há ambiguidade. A leitura é a mesma do
   `GET /operacoes/{id}/historico` (`carregarOperacao`), e a resposta tem a mesma forma do
   `GET /ordens/{id}` (o `descrever`), com dois campos novos:

   | campo | o quê |
   |---|---|
   | `dia` | o dia da operação (`2026-10-30`) |
   | `acompanhada` | `false`: o serviço não acompanha mais essa operação (só o retrato gravado) |

   Na resposta da memória, `acompanhada` é `true` e `dia` é o de hoje. Os dois campos vão sempre, para
   o cliente não ter de adivinhar.
3. **Só leitura.** Nada é carregado na instância, e nenhuma ação passa a valer para a op de outro
   dia: `encerrar`, `cancelar-entrada`, `confirmar-posicao` e a SAIDA do `POST /ordens` continuam como
   hoje.
4. **Banco indisponível:** `{"existe": false, "motivo": "banco indisponivel: ..."}` com HTTP 200, como
   a consulta já responde hoje para o que não acha. Um 503 faria o `recuperar()` do Kalman parar com
   `ErroRede`. Com o `motivo`, o alerta do Kalman pode dizer o porquê.
5. **O ambiente:** é o gravado na operação. A consulta não depende de instância (o banco é comum), e o
   `?ambiente=` continua ignorado como hoje.

### 3.3 Para decidir

- **D1: uma op de outro dia que não está final no banco** (por exemplo `PROTEGIDA` de ontem) é uma
  anomalia, porque o serviço não a acompanha mais. A proposta responde o retrato com
  `acompanhada: false` e o estado gravado, sem alerta do lado do serviço. Alternativa: emitir também
  um alerta `OPERACAO_DE_OUTRO_DIA` (não crítico) na consulta.
- **D2: o lado do Kalman** (`KalmanWIN`, que eu só mexo avisando antes):
  - com `acompanhada: false` e o estado final (`ZERADA`, `ENCERRADA_*`), o Kalman fecha a operação
    dele pelo `_conciliar`;
  - com `acompanhada: false` e o estado não final, o Kalman deve alertar "conferir à mão", não esperar.

  **O conserto do serviço sozinho não fecha a 234449.** No Kalman, o `_fechar_real` sem a posição em
  memória (`u.entrada is None`, o caso dela desde o restart de 30/10 10:58) só faz o registro tardio e
  não grava a operação como fechada. Com o retrato `ZERADA` vindo do banco, ela cairia no mesmo
  caminho. É preciso também, no Kalman, fechar a linha da `kalmanoperacao` mesmo sem a posição em
  memória (fora das somas, como hoje), e entender por que a reidratação das 10:58 trouxe "0
  posição(ões)" com a 234449 aberta.

### 3.4 Testes (depois da aprovação)

- **No motor:** a op de ontem (gravada, fora do `CarregarDia`) responde com `existe: true`,
  `acompanhada: false`, `dia` e o estado gravado. A op de hoje responde com `acompanhada: true`. Uma op
  que não existe responde `existe: false`. O banco com erro responde `existe: false` com o `motivo`.
- **Nos dois backends** (memória e MariaDB), na suíte.
- **Um roteiro** com uma op zerada num dia e a consulta no dia seguinte, depois do restart.

## 4. O que não muda

- Nenhuma migração (continuam 17).
- O contrato do Kalman: os campos do §3 são novos, nenhum muda.
- O `service.env` da .53: nada a mudar para os itens 1 e 2. Depois da v1.32, a ponte REAL/BTG mostra
  `webhook_esperado=false`.
