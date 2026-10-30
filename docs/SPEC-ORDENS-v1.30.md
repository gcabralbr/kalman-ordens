# SPEC kalman-ordens v1.30 — as credenciais Flexscan do REAL por corretora (30/10/2026)

Base: v1.29. É o passo 2b da SPEC-AMBIENTES-v1 (§8), da D4 revisada (§7.3), aprovado em 30/10 com dois
pontos:
- a ponte é escolhida pelo `CORSeqId` **gravado** na operação;
- cada guarda-chuva tem a sua lista de contas permitidas.

## 1. Configuração

Sem `REAL_FLEXSCAN_CORRETORAS`, nada muda: o REAL tem a ponte única de sempre (`REAL_FLEXSCAN_*`), que
é o caso da .53 no passo 4 (D5). Com ela, o REAL tem **uma ponte por corretora**:

| chave | o quê |
|---|---|
| `REAL_FLEXSCAN_CORRETORAS` | `CORSeqId:NOME`, separados por vírgula (ex.: `88:CMC,85:BTG`). NOME de 2 a 10 letras e dígitos, maiúsculas |
| `REAL_FLEXSCAN_<NOME>_USUARIO` / `_SENHA` | o guarda-chuva **próprio** do kalman-ordens nessa corretora (D4: nunca o do GX) |
| `REAL_FLEXSCAN_<NOME>_CONTAS` | as contas permitidas nesse guarda-chuva (lista explícita, sem `*`; cada conta num guarda-chuva só) |
| `REAL_FLEXSCAN_<NOME>_URL` | a API; sem ela, a `REAL_FLEXSCAN_URL` comum |
| `REAL_FLEXSCAN_<NOME>_WEBHOOK_SEGREDO` / `_WEBHOOK_PREFIXO` / `_WEBHOOK_URL` | o webhook dessa corretora; prefixo padrão `/flexscan/webhook-real-<nome>/` |
| `REAL_FLEXSCAN_<NOME>_ESTADO_LOGIN` | padrão `/var/lib/kalman-ordens/flexscan-login-real-<nome>.json` |

- Com `REAL_FLEXSCAN_CORRETORAS`, as chaves `REAL_FLEXSCAN_USUARIO` e `REAL_FLEXSCAN_SENHA` têm de
  estar vazias.
- Tempos, janela (`FLEXSCAN_JANELA`...), timeout e limites de taxa são comuns a todas as pontes.
- O listener do webhook (`FLEXSCAN_WEBHOOK_ENDERECO`) é um só, com uma rota por corretora; um
  prefixo não pode conter o outro.
- O serviço não sobe com a configuração errada. O erro diz a chave que falta ou que conflita.
- **Nenhuma corretora fixa no código para o REAL por corretora** (ajuste de 30/10):
  - qualquer `CORSeqId` declarado em `REAL_FLEXSCAN_CORRETORAS` vale como corretora conhecida, e o
    nome dela é o NOME declarado (no `/destino`, no rótulo "REAL · Flexscan · XP 7001" e nos alertas);
  - corretora nova é uma linha no `service.env` mais as chaves dela, sem binário novo;
  - 85 (BTG) e 88 (Capital) continuam conhecidas sem declaração (o SIMULADOR da .53 e a ponte única);
  - um `CORSeqId` nem declarado nem desses dois dá `DESTINO_INVALIDO`, com a lista das conhecidas e a
    indicação de `REAL_FLEXSCAN_CORRETORAS`.

## 2. Comportamento

- **Na entrada (REAL):** depois da coluna do GQ resolvida, vêm duas verificações, também no
  `/destino`, com os mesmos códigos:
  - `CORRETORA_SEM_CREDENCIAL`: a corretora (`Robo.CORSeqId`) não tem guarda-chuva nesta máquina;
  - `CONTA_FORA_DA_CORRETORA`: a conta não está em `REAL_FLEXSCAN_<NOME>_CONTAS`, e o motivo diz a
    chave.

  Depois, a sessão da ponte dessa corretora (`SEM_SESSAO`).
- **A operação usa a ponte da corretora gravada na entrada** (`destino`), em tudo o que manda: a
  proteção, o ajuste do stop, a saída, a zeragem, a conciliação (o mass status pergunta a conta pela
  ponte da corretora gravada) e o restart. Se o usuário mudar de corretora no GQ, a operação aberta
  continua pela original (roteiro 125).
- **Cada ponte tem a sua sessão:**
  - seu login e token, e a agenda da v1.28 por ponte;
  - seu cadastro de webhook, com a conferência por evento da v1.29;
  - seu tráfego e sua queda: o canal `FLEXSCAN:<NOME>` da v1.28 §3.
  - Uma corretora fora do ar suspende só as operações dela.
- **Restart:** o tráfego do dia volta por ponte. O evento grava o `canal` (migração 0017), e os
  eventos anteriores, sem canal, são da ponte única.

## 3. Contrato

- **`GET /saude`, bloco da instância REAL:**
  - `flexscan_corretoras`: um bloco por corretora (`{"CMC": {...}, "BTG": {...}}`), com os campos do
    bloco `flexscan` de sempre mais `corretora_codigo` e `contas`;
  - sem o bloco `flexscan` único;
  - `canais` com `FLEXSCAN:CMC`, `FLEXSCAN:BTG`.
- **`GET /flexscan/trafego?ambiente=REAL&corretora=CMC`** (e com `&dia=`):
  - no REAL por corretora, o `corretora` é obrigatório; ausente ou desconhecido dá 400, com a lista;
  - a resposta traz `"corretora": "CMC"`;
  - na ponte única, `corretora` dá 400.
- **`GET /usuarios/{uid}/destino`:** os dois códigos novos, e o `canal` da ponte da corretora.
- **Alertas de sessão:** `SESSAO_CAIU` / `SESSAO_VOLTOU` com a chave `SESSAO_CAIU:FLEXSCAN:CMC` e o
  texto `[flexscan CMC] ...`.

## 4. Testes

- **Roteiro 125:** duas Flexscan falsas (`config REAL_FLEXSCAN_CMC_URL=falso`, `flx_ambiente
  REAL:CMC`). Cobre:
  - a conta fora da lista;
  - uma operação em cada corretora;
  - a troca de corretora no GQ com a operação aberta;
  - o restart;
  - o tráfego por corretora;
  - a BTG fora do ar sem afetar a CMC.
- **Roteiro 125** também cobre uma corretora 77:XP, só declarada no `service.env`.
- **Roteiro 126:** a corretora sem guarda-chuva e um `CORSeqId` não declarado.
- **`TestInstanciasPorCorretora`:** a montagem e os erros de configuração.
