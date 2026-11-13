# SPEC kalman-ordens v1.34 — a tela de admin por conector e ambiente (13/11/2026)

**Base:** v1.33 (branch `v1.33`, ainda não instalada).

**Pedido e aprovação:** 13/11, com as três decisões abaixo. O desenho está em
`SPEC-ADMIN-CONECTORES-v1.md`; aqui fica o que foi feito e onde a implementação precisou decidir.

**Decisões aprovadas:**
- entra na v1.34;
- o log do QuickFIX fica fora da API, com um comando da CLI no lugar;
- a consulta de ordens na API da Flexscan fica fora desta versão.

**Ordem das instalações:** v1.32 no sábado 14/11; v1.33 e v1.34 depois, cada uma na sua janela.

| # | o quê | estado |
|---|---|---|
| 1 | migração 0018: `ambiente`, `conector`, `canal` em `kalmanordemsessao` | feito |
| 2 | o alerta sem operação grava o conector de quem o emitiu; a sessão FIX grava `PNT` | feito |
| 3 | `GET /sessao/eventos?dia&ambiente&conector` | feito |
| 4 | `GET /conectores`, `GET /historico/ordens`, `GET /historico/fio` | feito |
| 5 | 409 `INSTANCIA_DESLIGADA` / `CONECTOR_AUSENTE` em `/pnt/ordens`, `/sessao/ao-vivo` e `/flexscan/trafego`; `?dia=` do tráfego lido do banco sem a instância | feito |
| 6 | CLI `kalman-ordens fio` (o log de arquivo do QuickFIX) e os filtros novos em `historico sessao` | feito |

## 1. O que muda no serviço

### 1.1 Banco

A **0018** acrescenta `ambiente`, `conector` e `canal` (NULL nas linhas antigas) e o índice
`(em, ambiente, conector)` em `kalmanordemsessao`. Só `ADD ... IF NOT EXISTS`. Ficam 18 migrações.

### 1.2 A origem do alerta

`alerta.Origem{Conector, Canal}` vai com o alerta:
- o `OuvinteFlexscan` emite e resolve dentro de `ComOrigem(FLEXSCAN, corretora)`;
- o crítico guarda a origem, e o `RESOLVIDO` sai com ela;
- sem origem, vale o código: `ConectorDoAlerta`, com a lista dos alertas do PnT, os `FLEXSCAN_*` e
  `RECUSA_FLEXSCAN`; o resto é `SERVICO`. O `RESOLVIDO` sem origem usa o código que resolve.

O `registrarSessao` recebe a origem, e o `montarLote` põe o ambiente da instância, como já fazia com
os eventos.

### 1.3 Rotas

O contrato está na SPEC-ADMIN-CONECTORES-v1 §3. Pontos decididos na implementação:

- **`/conectores`:** `historico` significa que há alguma operação gravada no ambiente
  (`AmbienteTemOperacao`, uma consulta EXISTS em `kalmanordemoperacao`). Procurar em
  `kalmanordemevento`, que não tem índice por ambiente, varreria a tabela.
- **`/sessao/eventos` com `ambiente`:** devolve só as linhas gravadas nesse ambiente. As de antes da
  0018 (ambiente NULL) só aparecem sem o filtro. O conector delas é inferido e vem com
  `inferido: true`. Sem filtro nenhum, a resposta é a de antes, com campos a mais.
- **Alertas da fonte de preço** (`FONTE_DEGRADADA`, `MERCADO_TRAVADO`) ficam `SERVICO`. A fonte é
  uma só por instância e serve aos dois conectores quando a Flexscan não tem fonte própria.
- **`/historico/ordens` e `/historico/fio`:** sem `?dia=` valem para hoje, e sem `?ambiente=` para o
  ambiente principal. A resposta traz `instancia` (`LIGADA`/`DESLIGADA`).
- **`/historico/fio`:**
  - cada mensagem do PnT é `{seq, conector: PNT, em, direcao, msgtype, ..., fix_raw}`, com o
    554/925 mascarado;
  - cada linha da Flexscan é `{seq, conector: FLEXSCAN, canal, linha}`, em que `linha` tem o
    formato de `/flexscan/trafego`;
  - `seq` é o id do evento, e `ultimo_seq` é o `desde` da próxima página;
  - a linha de POST/PUT/DELETE junta o pedido e a resposta. Por isso a página termina antes de um
    pedido ainda sem resposta, e ele volta inteiro na próxima;
  - `clordid=` é prefixo: `KM-P-234754` pega todas as versões da ordem.
- **`/sessao/ao-vivo` sem `?ambiente=`:** vai à instância com o PnT. Se nenhuma tiver, responde 409
  `CONECTOR_AUSENTE`. Antes ele dava 200 com o monitor vazio (o SIMULADOR da .53, com o PnT
  desligado).
- **`/flexscan/trafego` (ao vivo):** sem Flexscan na instância, responde 409 `CONECTOR_AUSENTE`
  (antes, 404). Com `?dia=`, lê do banco em qualquer caso. A `corretora` é o nome gravado no canal.
- **`/pnt/ordens`:**
  - 409 `INSTANCIA_DESLIGADA` (antes, 503 `AMBIENTE_NAO_PERMITIDO`);
  - 409 `CONECTOR_AUSENTE` (antes, 503 `SEM_SESSAO`);
  - a sessão caída continua 503 `SEM_SESSAO`;
  - ambiente inválido: 400.
- **Corpo do 409:** traz `erro` e `codigo` (o mesmo valor; o `erro` é o campo que a tela já lê),
  `ambiente`, `motivo`, `ambientes_permitidos`, `conector` e `historico`. `historico` é a rota que
  substitui a que falhou (13/11, depois da suíte do `95d87dc`):
  - `/pnt/ordens` → `/historico/ordens?dia=<hoje>&ambiente=<A>&conector=PNT` (as ordens gravadas no
    lugar do "consultar agora");
  - `/sessao/ao-vivo` → `/historico/fio?dia=<hoje>&ambiente=<A>&conector=PNT`;
  - `/flexscan/trafego` → `/historico/fio?dia=<hoje>&ambiente=<A>&conector=FLEXSCAN`.
- **Ambiente inválido nas rotas de admin:** 400 com `codigo: AMBIENTE_INVALIDO`.

### 1.4 CLI

- **`kalman-ordens fio [-dia D] [-ambiente A] [-dir DIR] [-clordid P] [-sem-heartbeat] [-eventos]`:**
  - lê `<FIX_DIR>/log/*.messages.current.log` (no REAL, `REAL_FIX_DIR`) do service.env, ou o `-dir`;
  - o log é UTC; o comando filtra o dia de Brasília e imprime com hora de Brasília;
  - a direção sai do 49 (ENV quando é o nosso SenderCompID); senha mascarada; SOH como `|`;
  - `-eventos` junta o log de eventos da sessão;
  - pede sudo, porque o diretório é do usuário do serviço.
- **`historico sessao`:** ganha `-ambiente` e `-conector`.

### 1.5 Roteiros

O `reinicia_servico` aceita `config=CHAVE=VALOR`, que sobe o serviço com a chave trocada. É o
fechamento do REAL de 13/11: o env de volta e o restart.

## 2. Testes

- **Roteiro 131** (`cenarios/131-admin-conectores.fix`):
  - o dia com o REAL pelo PnT (entrada recusada) e o SIMULADOR pela Flexscan;
  - os eventos da sessão por conector e ambiente, inclusive o `RESOLVIDO` da ponte como FLEXSCAN;
  - `/conectores` com as duas instâncias ligadas;
  - `reinicia_servico config=AMBIENTES_PERMITIDOS=SIM`, e daí:
    - `/conectores` com o REAL `DESLIGADA` e `historico: true`;
    - `/historico/ordens` e `/historico/fio` do REAL, do banco;
    - o 409 de `/sessao/ao-vivo`, `/pnt/ordens` e `/flexscan/trafego`, com o `historico` de cada
      rota conferido por inteiro (nos dois códigos);
    - o tráfego do dia do REAL com 200;
    - os 400 de ambiente, conector e dia inválidos.
  A contagem de roteiros vai a 141.
- **`TestContratoFio`** (memória e MariaDB): os filtros por ambiente, conector, canal e prefixo de
  ClOrdID, o limite e o `desde`.
- **`TestContratoHistorico`:** o evento da sessão com ambiente, conector e canal, ida e volta.
- **`TestMigracao0018SessaoConector`:** a linha antiga fica NULL, a nova grava os três, e a 0018
  aplicada de novo não falha.
- **`TestHistoricoFioPaginaSemPartirPedido`:** a página não parte o POST da Flexscan, e o `fix_raw`
  sai mascarado.
- **`TestConectorDaSessao`:** o conector gravado e o inferido.
- **`TestCliFio`:**
  - o dia de Brasília num log UTC;
  - a máscara do 554 e a direção;
  - `-sem-heartbeat`, `-clordid` e `-eventos`;
  - o erro sem log.
- A suíte completa nos dois bancos (§3).

## 3. Resultado

A suíte completa rodou nos dois bancos, com `-race`, no worktree limpo `../kalman-ordens-v134`:

- **`a4b0339`**: falhou só o `TestCoerencia` (`var/suite134-20261113-131032.log`). O roteiro 131
  usava `INSTANCIA_DESLIGADA` e `CONECTOR_AUSENTE`, que ainda não estavam no `ALERTAS.md`. Os outros
  19 pacotes passaram, inclusive os roteiros.
- **`95d87dc`**, com os códigos catalogados no `ALERTAS.md`: passou inteira, `FIM rc=0`, 20 pacotes
  (`var/suite134b-20261113-132918.log`).
- **O commit seguinte** (o `historico` do 409 por rota): ver §3.1.

### 3.1 O `historico` por rota (13/11)

Pedido depois da suíte do `95d87dc`: no 409 do `/pnt/ordens`, o `historico` passa a apontar para
`/historico/ordens`. Antes, as três rotas apontavam para o fio do dia.
- **Código:** `indisponivel` e `instanciaDe` recebem a rota de histórico, e o `pntDe` recebe a do
  chamador.
- **Roteiro 131:** confere o `historico` inteiro nas três rotas e nos dois códigos.
- **Mutação:** com o `/pnt/ordens` apontando para o fio, o 131 falha na linha 59.
- Antes da suíte, rodaram todos os pacotes rápidos, inclusive `internal/roteiro` (o `TestCoerencia`).

