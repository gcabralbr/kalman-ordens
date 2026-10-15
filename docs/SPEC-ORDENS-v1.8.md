# Spec de Implementação — `kalman-ordens` — v1.8

Revisão da [SPEC-ORDENS-v1.7.md](SPEC-ORDENS-v1.7.md). Lista **só o que mudou**; onde a v1.8 não
diz nada, vale a v1.7.

Tema: **habilitação do usuário pelo admin** (contrato oficial do responsável, 15/10; o lado do
Kalman implementa depois, contra esta versão) e um defeito da CLI `usuario`.

Data: 2026-10-15.

---

## 1. Habilitação pelo admin (H)

| # | Regra | Teste |
|---|---|---|
| H1 | **`GET /usuarios/{uid}/habilitacao`** → `{uid, existe, habilitado, posicao_max, qtd_max_ordem, por, motivo, em}` (direto do banco; `por` = o `alterado_por` gravado: o usuário do sistema na CLI, `admin:<por>` pela tela; `em` em RFC3339 com -03:00). Sem linha: `existe: false`, `habilitado: false`, o resto `null`. uid inválido → 400. | roteiro 65 |
| H2 | **`POST /usuarios/{uid}/habilitacao`**, corpo estrito `{habilitado, motivo, por}` (os três obrigatórios; campo desconhecido → 400) → `{aceito, codigo, motivo}`. Grava **só** `habilitado`, `alterado_por = "admin:<por>"`, `motivo` e `alterado_em`, **preservando `posicao_max` e `qtd_max_ordem`**, numa transação com o histórico (`kalmanordemusuariohist`). Registro: o pedido (v1.6 P1, rota `HABILITACAO`, uid do caminho) e, quando muda, o evento global **`ADMIN_ACAO`** `{acao: BLOQUEAR_USUARIO \| DESBLOQUEAR_USUARIO, uid, motivo, por}`, sem `operacao_id`. Aceites: `BLOQUEADO`, `DESBLOQUEADO`, `JA_BLOQUEADO`, `JA_DESBLOQUEADO` (os `JA_` não gravam nada nem publicam evento). Recusas: `CAMPO_INVALIDO`, **`SEM_HABILITACAO_PREVIA`** (`habilitado: true` para uid sem linha: a 1ª habilitação continua só pela CLI); `CONFIG_USUARIO_INDISPONIVEL` se o banco falhar. `habilitado: false` para uid sem linha → `JA_BLOQUEADO` (ele já não opera), sem criar linha. | roteiro 65; `TestContratoAlterarHabilitado` (memória e MariaDB) |
| H3 | **Efeito — conferido no código**: a habilitação é lida **só na ENTRADA** (`verificarHabilitacao`, em `pedirEntrada`: `UID_NAO_HABILITADO`). Saída (`pedirSaida` só confere o uid da operação, v1.2 U4), proteção (1014, replace, recriação, invariante), ajuste do stop (v1.4 K), cancelamento e fills da entrada pendente não a consultam: bloquear não cancela nem envia nada. A outra leitura é a escolha das contas do mass status (logon e `GET /pnt/ordens`): entram as contas dos habilitados **e** as das operações do dia — a conta de um usuário bloqueado com operação no dia continua conciliada. | roteiro 65 (entrada pendente executa e protege, ajuste, saída; entrada nova recusada); mutação "saída confere a habilitação" reprovada |

## 2. CLI `usuario habilitar`/`desabilitar` (H4)

**Defeito corrigido**: sem `-posicao-max`/`-qtd-max`, a CLI gravava 0 (zerava os limites do uid).
Agora, sem a flag, mantém o valor atual; com a flag, troca (inclusive para 0). Linha nova sem a
flag: 0 (só o global), como antes. Teste: `cmd/kalman-ordens` `TestUsuarioHabilitarPreservaLimites`
(reprova sem a correção).

## 3. Cobertura

| Teste | Cobre |
|---|---|
| roteiro 65 | H1–H3 |
| `internal/repo` `TestContratoAlterarHabilitado` | H2 no banco: só habilitado/por/motivo/em, limites preservados, histórico, sem linha, já no valor |
| `cmd/kalman-ordens` `TestUsuarioHabilitarPreservaLimites` | H4 |
