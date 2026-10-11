# Spec de Implementação — `kalman-ordens` — v1.2

Revisão da [SPEC-ORDENS-v1.1.md](SPEC-ORDENS-v1.1.md) (que revisa a v1). Lista **só o que
mudou**; onde a v1.2 não diz nada, vale a v1.1.

Tema único: **conta, destino e símbolo vêm da operação, não do service.env**. Feito antes da
rodada 1 da F1, para a HML testar o caminho de produção.

Data: 2026-10-11.

---

## 1. Usuário: conta e destino (U)

| # | Regra | Substitui | Roteiro |
|---|---|---|---|
| U1 | **Origem:** `dbecotools.acusuario`, `WHERE usucodusuario = '<uid>'` (`char(15)`; o uid do Kalman é o mesmo do GQ). No GQ o usuário escolhe no login onde opera; no serviço não há login, quem escolhe é o **ambiente da instância**, `ORDENS_AMBIENTE=HML\|PRODUCAO` (obrigatório): HML → `usutxtconfig` (simulação: para testar na HML do PnT o usuário escolhe "BTG_Simulador" no GQ); PRODUCAO → `usutxtconfigprd` (conta real). Do JSON só o grupo `Robo`; `AmbienteDef` e o resto são ignorados. JSON inválido, sem `Robo.CORSeqId`, ou uid sem linha → `CONFIG_USUARIO_INVALIDA` + alerta `USUARIO_CONFIG`; banco ilegível → `CONFIG_USUARIO_INDISPONIVEL` + alerta. Grant **por coluna**: `SELECT (usucodusuario, usutxtconfig, usutxtconfigprd) ON dbecotools.acusuario` (`deploy/LEIA.md` 1.2). Exemplo real (HML, uid 1450): `{"Robo":{"CORSeqId":85,"CCONroConta":"99999",...}}`. | `ORDENS_CONTA` | 41, 39 |
| U2 | **Destino** `Robo.CORSeqId`: só **85** (BTG → PnT), nos dois ambientes. **88** (Capital → Flexscan) → `DESTINO_NAO_SUPORTADO`, sem alerta. **9999** (simulador do GQ) ou qualquer outro → `DESTINO_INVALIDO` + alerta `USUARIO_CONFIG`. **Conta** `Robo.CCONroConta`: só dígitos (o simulador usa `QUANT`) → senão `CONTA_INVALIDA` + alerta. A conta vai na tag **1 e na 116** (OnBehalfOfSubID, cabeçalho) de toda ordem — como o PT faz (`pntfixrepo`, `PNTLongAndShortOrderMessageFactory`: `Account` + `OnBehalfOfSubID` nas single-leg; o 671 `LegAllocAccount` e o PartyID `448="CPF"` constante só existem no multileg, não se aplicam à 1031/1014; o cancel do PT não leva conta, o nosso leva o 1 e o 116). O **142** (SenderLocationID) continua da instância: `ORDENS_SENDER_LOCATION` (no log de HML, o IP `177.124.234.134`). | `ORDENS_ONBEHALF` | 37, 38, 42 |
| U3 | **Trava cruzada por conta:** `ORDENS_CONTAS_HML` (lista, obrigatória; hoje `99999`). Em HML a conta **tem** de estar na lista; em PRODUCAO **não** pode estar. Violação → `CONTA_AMBIENTE` + alerta crítico `CONTA_AMBIENTE` (por uid; resolvido na próxima entrada válida do uid). | — | 39, 40 |
| U4 | **Congelamento:** destino e conta são lidos **só na entrada** e gravados na operação e em cada ordem (`kalmanordemoperacao.conta/destino`, `kalmanordem.conta`). Proteção, saída, cancel e replace usam o gravado, **nunca releem**. A SAIDA não depende da habilitação atual nem da conta atual do GQ (só confere que o uid é o da operação). Conta/destino do usuário mudou no GQ com posição aberta → alerta crítico `CONTA_ALTERADA_COM_POSICAO` (conferência a cada `CONFERE_CONTA_S` = 60 s e no pedido de saída; resolve ao zerar ou se a configuração voltar); a posição segue pela conta original. | — | 42, 43 |
| U5 | **Habilitação por usuário:** tabela própria `kalmanordemusuario` (uid, habilitado, `posicao_max`, `qtd_max_ordem`, quem, quando, motivo) + histórico `kalmanordemusuariohist` (só INSERT). Alterada **só pela CLI** (`kalman-ordens usuario habilitar\|desabilitar -uid N -motivo X [-posicao-max N] [-qtd-max N]`, direto no banco; não há endpoint HTTP que altere); `quem` = usuário do sistema (`SUDO_USER`). Lida a cada entrada: sem habilitação ativa → `UID_NAO_HABILITADO` (o código de antes). **Escolher conta no GQ não habilita ordem real.** Limites do uid (0 = só o global) valem **junto** com os globais (o mais restritivo ganha). `usuario conferir -uid N` mostra o que uma entrada usaria. Substitui `ORDENS_UID_REAL`. | `ORDENS_UID_REAL` | 43 |
| U6 | **Mass status por conta:** um `35=AF 585=9` por conta — as das operações do dia e as dos usuários habilitados, resolvidas no ambiente (com a trava cruzada; a que falha não entra). Sem conta nenhuma, nada é pedido e a conciliação fecha no silêncio. As respostas de todos os `584` contam. | 7.2 | 39 (PRODUCAO com conta de HML: nenhum AF), todos (INICIO) |

## 2. Símbolo (S)

| # | Regra | Substitui | Roteiro |
|---|---|---|---|
| S1 | **Entrada = contrato vigente:** o ticker do `/precos/atual` (que vem da tabela de contratos do GQ), gravado na operação e nas ordens (`simbolo`). Ticker que não é contrato de WIN (`^WIN[FGHJKMNQUVXZ]\d\d$`) → `SIMBOLO_INVALIDO` + alerta. Virada do vigente → aviso `CONTRATO_VIGENTE`. **Saída e proteção = símbolo gravado na posição.** | `ORDENS_SIMBOLO`, B3 da v1.1 | 16f, 44 |
| S2 | **Ticker divergente compara posição × vigente** (por operação), não serviço × marketdata. A entrada nunca diverge (vai no vigente). B5.b–B5.d valem para a posição num contrato que não é mais o vigente: alerta crítico `SIMBOLO_DIVERGENTE_COM_POSICAO` por operação (resolve ao zerar ou se o ticker voltar); saída com o último preço **do símbolo da posição** e colar máximo; mais de 60 s → saída recusada (`SIMBOLO_DIVERGENTE`) e `SAIDA_BLOQUEADA_PRECO_ANTIGO`. O último preço é guardado por ticker. Perda do dia e stop furado ficam suspensos enquanto houver posição em contrato não vigente (pendência 2 da v1.1). | B3, B5 da v1.1 | 16g–16j, 44 |
| S3 | **Vencimento:** `ORDENS_VENCIMENTO` e o alerta `VENCIMENTO` saem — a virada vem do ticker do marketdata (tabela de contratos do GQ). | B3 (aviso) | — |

## 3. Configuração

Saem do service.env: `ORDENS_CONTA`, `ORDENS_ONBEHALF`, `ORDENS_SIMBOLO`, `ORDENS_VENCIMENTO`,
`ORDENS_UID_REAL` (e `AVISO_VENCIMENTO_PREGOES`). **O serviço não sobe se alguma delas estiver no
service.env** (um arquivo antigo não fica valendo em silêncio).

| Chave | Inicial | Uso |
|---|---|---|
| `ORDENS_AMBIENTE` | — (obrigatória) | `HML` ou `PRODUCAO` (U1) |
| `ORDENS_CONTAS_HML` | — (obrigatória) | contas de HML, separadas por vírgula; hoje `99999` (U3) |
| `CONFERE_CONTA_S` | 60 | U4 |
| `ORDENS_SENDER_LOCATION` | — | 142 (fica; na HML `177.124.234.134`) |
| `ACUSUARIO_TABELA` | `dbecotools.acusuario` | só em teste (`acusuario_teste` no schema de teste) |

Migração `0002_conta_simbolo_habilitacao.sql`: colunas `conta`, `destino`, `simbolo` em
`kalmanordemoperacao`; `conta`, `simbolo` em `kalmanordem`; tabelas `kalmanordemusuario` e
`kalmanordemusuariohist`. Só `ALTER`/`CREATE` (o usuário do banco não tem `DROP`).

## 4. Contrato com o gestor — o que muda

Recusas novas no `POST /ordens` (nenhuma conta para a parada automática):
`CONFIG_USUARIO_INDISPONIVEL`, `CONFIG_USUARIO_INVALIDA`, `DESTINO_NAO_SUPORTADO`,
`DESTINO_INVALIDO`, `CONTA_INVALIDA`, `CONTA_AMBIENTE`, `SIMBOLO_INVALIDO`. `SIMBOLO_DIVERGENTE`
passa a existir só na SAIDA. `UID_NAO_HABILITADO` agora vem da tabela de habilitação. Na SAIDA, uid
diferente do da operação → `CAMPO_INVALIDO`. `GET /ordens/{id}` traz `uid`, `conta`, `destino`,
`simbolo`; `/saude` traz `ambiente` e `simbolo` = vigente. Precedência e tabela completas em
`docs/ALERTAS.md`.

## 5. Pendências

1. **Mass status por conta (U6)** é decisão desta implementação (o pedido não dizia como pedir o
   mass status sem conta fixa). Revisar se o PnT aceitar `585` por usuário em vez de por conta.
2. **PartyID 448:** o PT manda `453=1 448=CPF 447=D 452=5` (constante `"CPF"`) só no multileg; não
   replicado na 1031/1014. Perguntar ao PnT se a single-leg exige PartyID [PnT-3].
3. **Conta no cancel:** o PT não manda conta no 35=F; o serviço manda 1 e 116. Confirmar na
   rodada 1 que o PnT aceita (checklist 2.10e).

## 6. Cobertura dos roteiros

| Roteiro | Cobre |
|---|---|
| 16f | S1 (virada sem posição: entrada no vigente, `CONTRATO_VIGENTE`) |
| 16g–16j | S2 (posição em contrato não vigente: alerta, saída pelo último preço do símbolo da posição, 60 s) |
| 37 | U2 (destino 88 → `DESTINO_NAO_SUPORTADO`, sem alerta; volta a 85 → aceita) |
| 38 | U2 (simulador do GQ: 9999 → `DESTINO_INVALIDO`; conta `QUANT` → `CONTA_INVALIDA`) |
| 39 | U1, U3, U6 (PRODUCAO: conta de HML recusada, nenhum AF no logon; conta real no 1 e 116) |
| 40 | U3 (HML: conta fora da lista recusada) |
| 41 | U1 (JSON inválido, sem `Robo`, uid sem linha) |
| 42 | U4 (conta alterada no GQ com posição: alerta crítico; cancel e saída pela conta original) |
| 43 | U5 (não habilitado com conta escolhida no GQ; desabilitado pela CLI: entrada recusada, saída da posição aberta continua) |
| 44 | S1, S2 (virada com posição: cancel e saída em WINZ26; entrada nova em WINF27) |

Testes de unidade: `internal/usuario` (JSON, destinos, conta, trava cruzada),
`internal/repo` `TestContratoV12` (memória e MariaDB), `cmd/kalman-ordens`
`TestServirRecusaConfigAntiga` e a fumaça (CLI `usuario habilitar` + 1/116/55 no fio).
