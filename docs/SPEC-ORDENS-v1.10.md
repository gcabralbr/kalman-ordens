# Spec de Implementação — `kalman-ordens` — v1.10

Revisão da [SPEC-ORDENS-v1.9.md](SPEC-ORDENS-v1.9.md). Lista **só o que mudou**; onde a v1.10 não
diz nada, vale a v1.9.

Tema: **escala** — decisões do responsável sobre a seção 6 do
[relatório de carga](CARGA-2026-10-15.md) (15/10): itens 1, 2, 3 e 5 implementados; o 4 (POST em
lote por sinal) é proposta de contrato (seção 6), a combinar antes do código. Resultado do teste
de carga depois da mudança: [CARGA-2026-10-15.md](CARGA-2026-10-15.md) seção 7.

Data: 2026-10-15.

---

## 1. Prioridade (P)

| # | Regra | Teste |
|---|---|---|
| P1 | **Entrada do motor em três filas, prioridade estrita**: **P0** = o que vem do PnT (ERs, 35=9, 35=j, 35=3, eventos de sessão), o tick, consultas e paradas; **P1** = pedidos de SAIDA, ajuste do stop, encerrar/cancelar-entrada, habilitação, consulta ao PnT; **P2** = pedidos de ENTRADA. O laço tira sempre da mais alta que tiver item. A P0 tem capacidade de ~1 milhão de itens: a goroutine da sessão FIX nunca espera o motor. | `internal/motor` `TestFilasPorPrioridade`; roteiro 67 |
| P2 | **Envio ao PnT por prioridade**, depois do commit da volta: **1014 (nova, recriada, replace) > cancel, 1031 de saída e o replace dela, mass status > 1031 de entrada**; dentro da classe, a ordem de decisão. | `TestEnvioPorPrioridade`; roteiro 67 |
| P3 | **Prazos de confirmação contados até a chegada à sessão**: a camada de sessão carimba a chegada de cada mensagem (pelos ClOrdID 11 e 41) antes de ela entrar na fila; `SEM_CONFIRMACAO` (`PRAZO_CONFIRMACAO_S`) e `CANCEL_SEM_RESPOSTA` só disparam se nenhuma resposta **chegou** no prazo, mesmo que ainda não tratada (no `CANCEL_SEM_RESPOSTA` depois do 39=6, só uma chegada posterior a ele conta). Fila cheia deixa de gerar alerta falso e mass status em massa. | roteiro 69 |
| P4 | **Limitador de taxa da sessão** (token bucket no relógio do motor): `FIX_TAXA_MSG_S` mensagens/s, rajada `FIX_RAJADA_MSG` — **conservador até [PnT-19]: 100/s, rajada 100**. **Só a 1031 de entrada espera**; 1014, cancel, saída e mass status saem na hora (consomem tokens; o saldo pode ficar negativo e as entradas seguintes esperam) — reserva para a proteção. A entrada que espera a taxa conta como viva (limites, posição pendente); o timeout da entrada (`TIMEOUT_ENTRADA_S`) conta do envio real. Cancelada enquanto espera (saída do gestor, parada, admin), **sai da fila e nunca vai ao PnT**: `ENTRADA_ENCERRADA motivo=CANCELADA` na hora. | roteiro 68 |

## 2. Banco em lote (B)

| # | Regra | Teste |
|---|---|---|
| B1 | **Uma transação por volta**: o motor trata até 256 itens ou 20 ms das filas, acumula tudo o que mudou e grava num `GravarLote` — INSERT de várias linhas por tabela (operação, ordem, evento FIX, publicado, estado, sessão, pedido HTTP), em blocos de 200 linhas. **As mensagens ao PnT só saem depois do commit** ("gravar antes de enviar", P1 da v1.1). As respostas HTTP e os `Fazer` síncronos também só saem depois do commit e do envio. | `TestEnvioDepoisDoCommit`; `TestContratoLote` (memória e MariaDB) |
| B2 | **IDs de operação e ordem atribuídos pelo motor** (a partir do maior ID do banco na subida): o evento FIX já aponta para a ordem antes do commit. Operação/ordem **nova** = INSERT (colisão na chave única é erro, nunca a atualização de outra linha); **já gravada** = upsert pelo ID, **só se mudou** desde a última gravação (cópia comparada) — sem gravações redundantes. | `TestContratoLote` |
| B3 | **Deduplicação de fill em memória**: os pares (37, 17) do dia são carregados na subida (`ChavesFillDoDia`); o fill repetido é descartado no motor (antes, pela chave única do banco, uma ida por mensagem). No banco, `INSERT IGNORE` com a chave única de rede. | roteiros 09, 50 |
| B4 | **Habilitação e `acusuario` em memória** (`cacheUsuarios`): carregados na subida (todas as habilitações e a configuração do GQ dos habilitados, numa consulta só), **relidos a cada `CACHE_USUARIOS_S` = 30 s** em segundo plano e **invalidados pela rota do serviço** que muda a habilitação (`POST /usuarios/{uid}/habilitacao`). Uma recarga que começou antes de uma invalidação é descartada. Uid fora do cache é lido na hora e guardado. **A CLI grava direto no banco: o efeito dela chega em até 30 s.** | `TestCacheUsuarios`; roteiros 37–43 e 65 |
| B5 | **Pedido HTTP gravado no lote** (não mais pela goroutine HTTP, que disputava as 4 conexões do pool com o motor). | roteiro 62 |
| B6 | **Falha ao gravar o lote**: nova tentativa na hora; falhou de novo → alerta crítico `ERRO_INTERNO` ("gravação no banco falhou"), **o envio segue** (a proteção não espera o banco, como antes da v1.10) e o que não gravou vai na volta seguinte; resolve na primeira gravação boa. | — |

Consequência para o contrato (rara): a falha de envio da entrada (sessão caiu entre a decisão e o
envio) ainda vira a recusa `ENVIO_FALHOU` quando acontece na mesma volta; quando a entrada já
tinha sido aceita e esperava a taxa, o gestor recebe `ENTRADA_ENCERRADA motivo=REJEITADA` (e
`RECUSA_PNT` com o texto).

| B7 | **Perda do dia incremental**: o resultado do dia (limite `PERDA_DIARIA`, conferido a cada entrada) era recalculado sobre todas as operações a cada entrada — O(N²) numa rajada, 40% da CPU do motor com 1.000 entradas (perfil). Agora: aberto = último × Σposição − Σ(médio × posição), com os agregados ajustados a cada fill (a parte da operação sai antes e volta depois). Mesma conta. | `TestResultadoDoDiaAgregado`; roteiro 15 |

## 3. Mass status do logon (C5)

O mass status da conciliação (logon, `SEM_CONFIRMACAO`, mensagem ilegível, 35=3) passa a ser
pedido **só para as contas com ordem viva ou posição** (pelo estado carregado do banco), não para
todos os habilitados. Sem nenhuma, nada é pedido e a conciliação fecha no silêncio. **O silêncio
(`MASS_STATUS_SILENCIO_S`) conta do envio real do último AF e da última resposta de mass status
chegada à sessão** (antes: da decisão de pedir e da última resposta tratada — com o despacho ou a
fila lentos, a conciliação fechava antes de as respostas serem vistas e marcava ordens vivas como
`DESCONHECIDA`). A consulta do
admin (`GET /pnt/ordens`, v1.7 C1) continua com as contas das operações do dia e dos habilitados.
Roteiros: `_comum.fix` (INICIO sem AF), 45 (logon sem ordens: nenhum AF), 12, 18, 20 (posição
aberta antes do restart).

## 4. Configuração

| Chave | Inicial | Uso |
|---|---|---|
| `FIX_TAXA_MSG_S` | 100 | P4 (conservador até [PnT-19]) |
| `FIX_RAJADA_MSG` | 100 | P4 |
| `CACHE_USUARIOS_S` | 30 | B4 |
| `FIX_STORE_SYNC` | `Y` | fsync do store FIX a cada mensagem (o padrão do QuickFIX/Go, como até aqui). Com `N`, sem fsync: ver o relatório, seção 7.2 — **decisão pendente** |
| `DEBUG_PPROF` | — | `1` expõe `/debug/pprof/` no HTTP local (diagnóstico; o teste de carga usa) |

Sem migração nova.

## 5. Métricas novas

`lote_segundos`, `lote_linhas`, `envio_espera_segundos{prioridade}` (da decisão ao envio: commit e
taxa), `fila_tamanho_p0|p1|p2`, `envios_aguardando_taxa`; `fila_espera_segundos{origem}` com
`http_entrada` e `http_saida`; `banco_segundos{operacao="GravarLote"}`. `alertas_total` passa a
contar só os alertas que saem (um crítico já ativo não conta de novo).

## 6. Proposta de contrato — POST em lote por sinal (item 4, a combinar)

O Kalman manda **uma requisição por sinal** com todas as entradas; o serviço valida cada uma como
hoje, ordena as aceitas de forma rotativa e registra a posição de cada operação na rajada.

```
POST /sinais
{
  "sinal_id": "<texto, único por módulo>",
  "modulo": "KALMAN",                          (opcional)
  "ts_decisao": "<o do sinal>",
  "entradas": [
    {"operacao_id": 900000001, "uid": 1450, "lado": "C", "qtd": 1, "stop_pts": 500, "motivo": "SINAL"},
    ...
  ]
}
→ 200
{
  "sinal_id": "...", "aceito": true, "codigo": "LOTE_ACEITO", "motivo": "...",
  "tamanho": 4000, "aceitas": 3990, "criterio": "ROTATIVA", "deslocamento": 1237,
  "resultados": [
    {"operacao_id": 900000001, "aceito": true, "posicao": 2764, "clordid": "KM-E-900000001-1", "preco": 130020},
    {"operacao_id": 900000002, "aceito": false, "codigo": "UID_NAO_HABILITADO", "motivo": "..."},
    ...
  ]
}
```

- **Validação por entrada**, a mesma do `POST /ordens` (habilitação, campos, duplicada, parada,
  sessão, conta, limites do uid, preço); **um só preço** (a mesma leitura) para o lote inteiro.
  Lote inválido (JSON, sem `sinal_id`, `entradas` vazia ou com mais de 10.000) → 400
  `CAMPO_INVALIDO` para o lote todo.
- **Ordem rotativa**: as aceitas, por uid crescente, deslocadas de `deslocamento` posições. Proposta:
  `deslocamento = ⌊k·φ·N⌋ mod N` (k = contador de sinais do módulo, persistente entre dias; φ =
  0,618…; N = aceitas) — cada usuário passa por todas as faixas de posição depressa e sem padrão
  fixo, e continua determinística e explicável ("no sinal 37 você foi o 2.764º de 3.990").
  Alternativa mais simples: `deslocamento = k mod N` (desliza de 1 em 1 — justo no longo prazo, mas
  quem está na frente fica na frente por muitos sinais).
- O envio segue a ordem rotativa, pela taxa (P4); a proteção de cada uma continua imediata.
- **Registro por operação** (migração nova): `sinal_id`, `posicao_rajada`, `tamanho_rajada`,
  `deslocamento`, `criterio`; no `GET /ordens/{id}`, no histórico e na lista do dia.
- **Idempotência**: mesmo `sinal_id` com o mesmo corpo → o resultado original com `repetido: true`;
  corpo diferente → `DUPLICADA`; uma entrada que repete o `(modulo, operacao_id)` de outra já
  recebida → `DUPLICADA` só nela. 503 `RESULTADO_DESCONHECIDO` como no `POST /ordens` (o lote segue;
  reenviar o mesmo corpo).
- **Resposta**: depois do commit do lote (milissegundos); `aceito` na entrada = decidida e na fila de
  envio (não necessariamente já no PnT). Proposta junto: evento `ENTRADA_ENVIADA` (`posicao`,
  `enviada_em`) quando a 1031 sai, para o gestor e a tela de admin.
- `POST /ordens` continua para entrada avulsa (sem rajada: ordem de chegada) e para a SAIDA.
- Para combinar: (a) `φ` ou `+1`; (b) o evento `ENTRADA_ENVIADA`; (c) saídas em lote por sinal
  (o "sinal oposto" fecha todos — mesma justiça de preço na saída?); (d) limite de entradas por lote.
