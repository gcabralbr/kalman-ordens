# SPEC kalman-ordens v1.32 — o webhook só onde é esperado, o motivo da boleta e a consulta de outro dia (03/11/2026)

Base: v1.31, instalada na .53 em 01/11 (93245099bd13). São três pedidos de 03/11, depois do REAL de
teste pela boleta (pausado: "Conta nao contratada", SPEC-CADASTRO-v1 §0.1):

| # | o quê | estado |
|---|---|---|
| 1 | a ponte sem URL de webhook não espera webhook | feito |
| 2 | a CLI da boleta confere o motivo antes do "sim" | feito |
| 3 | o `GET /ordens/{id}` de uma op de outro dia lê o banco | feito (§3 aprovado em 03/11, com D1 e D2) |
| 4 | os alertas da abertura da Flexscan (12/11) | feito (§4, aprovado em 12/11) |

Os quatro vão juntos numa v1.32 só, instalada fora do pregão (o REAL está pausado).

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

## 3. O `GET /ordens/{id}` de uma op de outro dia lê o banco

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

### 3.3 Decisões (03/11)

- **D1 (decidido: com alerta):** uma op de outro dia que não está final no banco (por exemplo
  `PROTEGIDA` de ontem) é uma anomalia, como foi a 234449: o serviço não a acompanha mais. A consulta
  responde o retrato com `acompanhada: false` e o estado gravado, **e emite `OPERACAO_DE_OUTRO_DIA`**
  (não crítico), uma vez por operação em cada processo (o Kalman consulta a cada subida; a CLI, a cada
  `boleta estado`). A op de outro dia final (`ZERADA`, `ENCERRADA_*`) não alerta, e a de hoje fora da
  memória (a instância do outro ambiente não está no ar) também não.
- **D2 (decidido: o KalmanWIN não muda nesta versão).** Os dois defeitos do Kalman (a posição não
  gravada antes do restart e o `_fechar_real` sem posição em memória) estão com o responsável pelo
  Kalman; a 234449 foi fechada à mão no Kalman em 03/11, 08:47. O que segue é o registro da análise:
  - com `acompanhada: false` e o estado final (`ZERADA`, `ENCERRADA_*`), o Kalman fecha a operação
    dele pelo `_conciliar`;
  - com `acompanhada: false` e o estado não final, o Kalman deve alertar "conferir à mão", não esperar.

  **O conserto do serviço sozinho não fecha a 234449.** No Kalman, o `_fechar_real` sem a posição em
  memória (`u.entrada is None`, o caso dela desde o restart de 30/10 10:58) só faz o registro tardio e
  não grava a operação como fechada. Com o retrato `ZERADA` vindo do banco, ela cairia no mesmo
  caminho. É preciso também, no Kalman, fechar a linha da `kalmanoperacao` mesmo sem a posição em
  memória (fora das somas, como hoje), e entender por que a reidratação das 10:58 trouxe "0
  posição(ões)" com a 234449 aberta.

### 3.4 Implementação e testes

- **Onde:** `Servico.Consultar` pergunta à instância dona (a memória, como antes, agora com `dia` e
  `acompanhada: true`). Se ela não conhece a operação, lê o banco fora da fila do motor
  (`carregarOperacao`, a leitura do histórico) e descreve com o mesmo `descrever`. O alerta sai pela
  instância do ambiente gravado (ou a principal).
- **Roteiro 128** (memória e MariaDB):
  - a 2801 zerada pelo stop e a 2802 protegida no dia 03/11; a 2802 consultada no mesmo dia vem com
    `acompanhada: true`;
  - o serviço sobe no dia seguinte: `reinicia_servico salta=24h`, um verbo novo dos roteiros que pula
    o relógio com o serviço fora, sem ticks no meio;
  - a 2801 vem `ZERADA`, com `acompanhada: false`, `dia 2026-11-03`, o pnl e a origem, sem alerta;
  - a 2802 vem `PROTEGIDA` com `acompanhada: false` e o `OPERACAO_DE_OUTRO_DIA`; consultada de novo, sem
    um segundo alerta;
  - uma op que não existe em nenhum dia: `existe: false`.
- O caso do banco com erro (`existe: false` com o `motivo`) é o mesmo caminho de erro do histórico.

## 4. Os alertas da abertura da Flexscan (12/11)

### 4.1 O caso

Todo dia de pregão a .53 manda ao Telegram, às 08:00:
`FLEXSCAN_LOGIN` ("login sem resposta: ... Client.Timeout ..."), o mesmo com "(continua)",
`RESOLVIDO`, `FLEXSCAN_WEBHOOK_AUSENTE` ("... recadastrado") e `RESOLVIDO`. No celular, parece que o
login falha, resolve e falha de novo. O levantamento de 12/11 (journal desde 20/10 e o tráfego
gravado):

- **O login.** A ponte acorda às 08:00:00, o início de `FLEXSCAN_JANELA`. O login fica sem resposta em
  `FLEXSCAN_TIMEOUT_MS` (3 s) em 03, 04, 05, 09, 11 e 12/11. Em 06 e 10/11 passou em 2,6 e 2,8 s. A
  segunda tentativa, 30 s depois (`FLEXSCAN_ESPERA_LOGIN_S`), leva cerca de 180 ms, e a renovação
  das 17:30 cerca de 1 s. Sempre uma falha só, e nunca no pregão. A Flexscan fica lenta no primeiro
  instante depois das 08:00 (em 28/10 uma conferência às 08:00:14 também deu timeout).
- **O "(continua)"** é a repetição do mesmo crítico a cada `ALERTA_REPETICAO_S` (30 s). Ela cai no
  mesmo segundo da segunda tentativa e sai uns 0,3 s antes do `RESOLVIDO`. Não é uma segunda falha.
- **O webhook.** A Flexscan apaga o cadastro todo dia entre 03:45:34 e 03:50:13 (com a conferência
  de 10 em 10 min de 28 a 30/10). O `GET /webhooks` volta 200 sem a nossa URL e sem nenhum outro
  cadastro. Não depende do token, e nós não apagamos: a ponte só faz GET e POST, e a .53 não tem
  nenhum timer nesse horário. A referência da API não fala de expiração. A conferência depois do
  login das 08:00 acha o cadastro ausente e recadastra. É a limpeza noturna que a v1.28 §1 já tinha
  visto.

### 4.2 A regra nova

**A tolerância da abertura:** num dia de pregão, do início do dia da agenda (o menor entre o início
da janela e `FLEXSCAN_RENOVAR_ABERTURA`) até antes de `FLEXSCAN_TOLERANCIA_ABERTURA` (nova, padrão
`08:45`; `sem` = sem tolerância, como antes), **sem ordem viva da Flexscan** (do motor ou acompanhada
pela ponte). No REAL, `REAL_FLEXSCAN_TOLERANCIA_ABERTURA` se diferir (o recuo das chaves da agenda).

1. **O login que falha na tolerância** (não definitivo) não alerta: fica no journal ("login da
   abertura falhou (sem alerta até a tolerância)") e no tráfego, como hoje. A ponte tenta a cada
   `FLEXSCAN_ESPERA_LOGIN_S`.
2. **O cadastro do webhook ausente** (sem nenhum dos dois gatilhos) na tolerância, se o dia ainda não
   teve o cadastro conferido completo: a ponte recadastra e confere de novo. Completo, não alerta.
3. **A linha da abertura** (`FLEXSCAN_ABERTURA`, novo, não crítico) sai uma vez, quando há o que
   contar (um login tolerado que deu certo, ou o recadastro tolerado), depois da conferência que
   segue o login (ponte sem URL de webhook: no login). Por exemplo:
   `[flexscan] abertura: logou 08:02:30 na 2a tentativa (a 1a, 08:02:00: login sem resposta: ...);
   webhook recadastrado (ausente na conferencia depois do login das 08:02:30 (reconexao); a Flexscan
   limpa os webhooks de madrugada)`.
4. **Continua crítico:**
   - o erro definitivo (senha bloqueada, MFA...), na hora;
   - o login ainda falhando a partir de `FLEXSCAN_TOLERANCIA_ABERTURA`: o `FLEXSCAN_LOGIN` sai na
     primeira falha depois dela, já com as tentativas;
   - o login que falha no pregão ou com ordem viva;
   - o webhook que some depois de conferido completo no mesmo dia, o cadastro incompleto (só um
     gatilho), o recadastro que falha e o recadastro fora da tolerância.

   Um login que deu crítico não entra na linha da abertura (já teve o `RESOLVIDO`).

**Os textos:**
- `FLEXSCAN_LOGIN`, a partir da segunda tentativa: `... (N tentativas desde HH:MM:SS)`. O texto é
  atualizado a cada tentativa, e as repetições do crítico saem com o último.
- Todo crítico repetido: `(continua desde HH:MM:SS)`, a hora da emissão, no lugar de `(continua)`.
- `FLEXSCAN_WEBHOOK_AUSENTE` diz qual conferência foi: `(conferencia depois do login das 08:46:30
  (reconexao))`, `(conferencia da abertura)`, `(conferencia da abertura, depois do login das ...)`,
  `(conferencia por 3 esperas sem webhook)`, `(conferencia periodica)`.

### 4.3 Junto: a janela de 08:02 (só `service.env`)

Aprovado em 12/11 e instalado à parte, antes da v1.32: `FLEXSCAN_JANELA=08:02-18:30` na .53
(`docs/INSTALACAO-2026-11-12-janela.md`).
Às 08:02 a Flexscan já responde rápido. Com a janela nova a falha das 08:00 deve sumir, e a
tolerância fica para o dia em que ela não sumir. O recadastro diário continua (a limpeza é da
Flexscan) e passa a sair como linha da abertura.

### 4.4 Implementação e testes

- `internal/flexscan/abertura.go`: a série de falhas (quantas, desde quando, se já deu crítico), o
  resumo do dia e a linha. `tentarLogin` chama `loginFalhou`/`loginDeuCerto`, e `conferirWebhook`
  decide o recadastro tolerado e diz o motivo da conferência.
- `Agenda.ToleranciaAbertura` (`AgendaDe`; formato errado: o serviço não sobe, como as outras chaves
  da agenda).
- `alerta.Alertas.Atualizar` (o texto de um crítico aberto) e o `Tick` com o "desde". O motor
  (`OuvinteFlexscan.AtualizarAlerta`) é chamado pela ponte por uma interface opcional, como o
  `Adormeceu`.
- `TestAberturaTolerada`, no relógio controlado:
  - quinta: a limpeza de madrugada e a Flexscan fora do ar às 08:00:00 (503). Nenhum crítico e uma
    linha, "logou 08:00:30 na 2a tentativa" e "webhook recadastrado (ausente na conferencia depois do
    login das 08:00:30 (reconexao)". O cadastro apagado às 08:20 dá crítico na conferência da
    abertura (já conferido no dia);
  - sexta: fora do ar das 08:00 às 08:46. Nada até 08:44:50; às 08:45:00 o crítico com "(91
    tentativas desde 08:00:00)", e o texto vivo chega a 93. O recadastro depois do login das 08:46:30
    é crítico e diz qual conferência foi;
  - segunda: com ordem viva, a falha das 08:09 é crítica na hora.
- `TestRepeticaoEspaca`: o "(continua desde 06:00:30)" e o `Atualizar`.
- Os roteiros não mudam: os da Flexscan começam depois de 08:59.

## 5. O que não muda

- Nenhuma migração (continuam 17).
- O contrato do Kalman: os campos do §3 (`dia`, `acompanhada`) são novos, nenhum muda. O Kalman de
  hoje passa a receber o retrato de uma op de outro dia em vez de `existe: false`, e o trata pelo
  `_conciliar` como uma op do dia.
- O `service.env` da .53: nada a mudar para os itens 1, 2 e 4 (`FLEXSCAN_TOLERANCIA_ABERTURA` vale
  08:45 sem estar no arquivo). Depois da v1.32, a ponte REAL/BTG mostra
  `webhook_esperado=false`.
