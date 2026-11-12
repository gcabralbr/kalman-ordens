# SPEC kalman-ordens v1.33 — roteiros sem prazo no relógio real (12/11/2026)

Base: v1.32 (branch `v1.32`, d1bad43 + bf66b1e; a instalar em 14/11). Pedido e aprovação de 12/11.
O serviço não muda de comportamento: só o executor dos roteiros, e dois contadores de observação no
motor.

| # | o quê | estado |
|---|---|---|
| 1 | os roteiros decidem pelo serviço parado, não por um prazo | feito |

## 1. Os roteiros decidem pelo serviço parado

### 1.1 O caso

Em 12/11 a suíte completa do d1bad43 falhou duas vezes, cada vez por um roteiro diferente, e passou
na terceira:
- **64** (`GET /pnt/ordens`): `recebe: nenhuma mensagem chegou em 2s`;
- **72** (rajada de sinais): `_status = 503 ... o motor nao respondeu a tempo`.

Rodados à parte com `-race`, os dois falharam 1 vez em 15 também no 883bdc8: a instabilidade já
existia e não vem da v1.32. Os dois decidem no relógio real:
- o `recebe` (e o `em_qualquer_ordem`) desiste se o PnT falso não recebe nada em 2 s
  (`esperaMensagem`);
- os roteiros 24, 62, 72 e 73 têm `config HTTP_ESPERA_S=0.2`: a API espera o motor 200 ms em
  **todos** os pedidos, não só no 503 de propósito. Com `-race` e a .53 carregada, o motor passa
  disso;
- o `ocioso()` (depois de `avanca`, `envia`, `motor_normal`...) desiste **calado** em 3 s e deixa o
  roteiro seguir com o serviço trabalhando, o que pode virar falha em outro passo.

### 1.2 A regra nova

**O serviço parado** (`quieto`, no executor), conferido duas vezes seguidas para nada ter entrado no
meio:
- cada motor sem nada na fila nem em curso: tudo o que entrou já teve a volta fechada (commit, envios
  e respostas). É o `Motor.Parado()`, novo;
- tudo o que o PnT falso mandou já foi tratado pelo motor, e tudo o que a sessão FIX mandou já chegou
  ao falso (as contas que o `ocioso` já fazia);
- as pontes Flexscan sem trabalho em curso.

1. **`recebe`**: sem mensagem, espera o serviço parar. Parado e sem mensagem, falha na hora com "o
   serviço parou sem mandar". Uma mensagem que só viria com o relógio andando não vem, e o roteiro
   tem de mandar `avanca`.
2. **`em_qualquer_ordem`**: o mesmo; falha quando o serviço parou e nada novo chegou.
3. **`ocioso`** (e com ele o `nada`): espera o serviço parar, sem desistir calado. Se não parar em
   30 s (`esperaServico`), o passo falha com "o serviço não parou em 30s". Esse teto é só contra
   travamento: num serviço que funciona, nunca é atingido.
4. **A espera da API nos roteiros**: o executor monta a API duas vezes:
   - a normal, com 30 s (`esperaHTTP`; `HTTP_ESPERA_S` ainda vale se um roteiro quiser): a resposta
     chega quando o motor responde;
   - a curta, com 0,2 s, usada só enquanto `motor_lento` está ativo. O motor está parado de
     propósito, então o 503 é certo.

   Sai o `config HTTP_ESPERA_S=0.2` dos roteiros 24, 62, 72 e 73.
5. **`http_async`** só devolve o controle quando o pedido já entrou na fila do motor
   (`Motor.PedidosHTTP()`, novo), ou quando já mandou mensagem direto pela sessão (a bancada, que
   não passa pelo motor), ou quando já respondeu. Sem isso, um "serviço parado" visto antes de o
   pedido entrar seria falso.
6. **Com `motor_lento`, como antes:** o `recebe` espera até 2 s, e o `ocioso` não espera (nada anda
   até `motor_normal`).

Ficam como estão os prazos que esperam o que não depende do serviço: o `recebe_sessao` (a sessão
FIX, 2 s), o `envia` com o motor travado e a bancada, que tem a `espera_s` real como contrato.

### 1.3 O que muda no código do serviço

Só observação, para os roteiros:
- `Motor.naFila` e `Motor.feitos` (atômicos): cada item posto numa fila (`entrar`, `Enfileirar`,
  `Parar`, a volta agendada) soma em `naFila`. Os itens de uma volta passam a `feitos` no fim do
  `fecharVolta`, antes do `aposVolta` (quem espera um `Fazer` já vê o motor parado), ou no `laco`,
  se o `fecharVolta` não chegou lá (pânico). `Parado()` lê `feitos` antes de `naFila`, para nunca dar
  um parado falso;
- `Motor.pedidosHTTP`: os pedidos de P1 e P2 que entraram.

Nenhuma decisão do motor lê esses contadores.

**Um limite do executor:** as latências do `/saude` (`http.*`) são de cada API montada. Um pedido
feito com `motor_lento` ativo conta na API curta e não aparece no `/saude`. Nenhum roteiro com
`motor_lento` confere essas latências.

### 1.4 Testes

- `TestRecebeSemMensagemFalhaNaHora`: o roteiro 72 até o primeiro `nada`, com um `recebe` a mais de
  uma ordem que não existe. Falha nesse passo com "o serviço parou sem mandar", e o roteiro inteiro
  leva bem menos que o teto.
- Os roteiros 24, 62, 72 e 73 continuam vendo o 503 com `motor_lento` (`resposta _status=503`, e a
  mutação "503 virou 200" do 24 continua reprovada).
- Estabilidade: os roteiros 64 e 72, 200 vezes cada, com `-race` e 6 processos em paralelo: nenhuma
  falha (§1.5).
- A suíte completa nos dois bancos.

### 1.5 Resultado (12/11)

- Antes (883bdc8 e d1bad43): os roteiros 64 e 72 falhavam cerca de 1 vez em 15 com `-race`; a suíte
  completa do d1bad43 falhou 2 vezes em 3.
- Depois (5e9fc2d): os roteiros 64 e 72, 204 vezes cada, com `-race` e 6 processos em paralelo
  (`var/estresse133-20261112-112003.log`): 204 + 204 passaram, nenhuma falha.
- `TestRecebeSemMensagemFalhaNaHora`: falha no passo inserido, "o serviço parou sem mandar"; o
  roteiro inteiro leva 0,8 s.
- A suíte completa nos dois bancos, com `-race`, no worktree limpo `../kalman-ordens-v133` (d0cd493):
  passou na primeira (`var/suite133-20261112-112550.log`).
