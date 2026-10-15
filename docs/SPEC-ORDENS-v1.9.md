# Spec de Implementação — `kalman-ordens` — v1.9

Revisão da [SPEC-ORDENS-v1.8.md](SPEC-ORDENS-v1.8.md). Lista **só o que mudou**; onde a v1.9 não
diz nada, vale a v1.8. **O envio não muda** nesta versão.

Tema: **escala (190 → 4.000 usuários no mesmo sinal)** — métricas contínuas e teste de carga.
Relatório: [CARGA-2026-10-15.md](CARGA-2026-10-15.md) (resultados, ponto de saturação e o desenho
proposto da fila com prioridade, do limitador de taxa e da ordem justa — a combinar).

Data: 2026-10-15.

---

## 1. Métricas (M)

| # | Regra | Teste |
|---|---|---|
| M1 | **`GET /metrics`** no formato de texto do Prometheus, no HTTP local (só 127.0.0.1, como todo o HTTP). Sem dependência nova (`internal/metricas`). Lista no relatório, seção 5. Durações em tempo real (não no relógio do motor); histogramas de 0,5 ms a 60 s. | roteiro 66; `internal/metricas` `TestFormato` |
| M2 | **Fila do motor**: toda entrada na fila registra a origem (`http`, `fix`, `tick`, `outro`) e a espera até ser tratada (`fila_espera_segundos`); tamanho e capacidade. | roteiro 66 |
| M3 | **Por pedido e por ordem**: do pedido HTTP à primeira mensagem FIX que ele causou (`pedido_ate_envio_segundos{pedido}`); envio → 39=A, 39=A → 39=0, envio → 39=0, envio → 1º fill (`ordem_*{papel}`, só das ordens enviadas pelo processo); **chegada do fill de entrada → 1014 enviada** (`protecao_fill_ate_envio_segundos`). | roteiro 66 |
| M4 | **Sessão, banco, recusas, alertas, HTTP**: mensagens de aplicação enviadas/recebidas por tipo; latência de cada operação do repositório (decorador `repo.Medido`, também em memória) e erros; recusas por pedido e código; alertas por código; duração por rota e status. Estado (atualizado no tick): ordens vivas, operações abertas, posição, sessão; processo: goroutines, heap, CPU, RSS máximo. | roteiro 66 |

Executor de roteiros: verbo `metrica NOME [rotulo=valor...] OP N` (cenarios/FORMATO.md).

## 2. Teste de carga (C)

`teste/carga` (`KALMAN_CARGA=190,300,500,1000,4000 KALMAN_TESTE_MARIADB=... go test -run TestCarga
-timeout 90m ./teste/carga`; pulado sem as duas variáveis): o binário real como processo próprio,
MariaDB de teste, PnT falso com respondedor automático (inclusive mass status com as ordens da
conta), rajadas de N entradas e N saídas no mesmo instante; mede POST, POST → PnT, fill → 1014,
39=4 → 1031, msg/s, CPU e memória do serviço e do MariaDB, e lê o `/metrics`. Resultado em JSON
(`KALMAN_CARGA_SAIDA`).

## 3. Pendência nova ao PnT

**[PnT-19]** Limite de mensagens por segundo da sessão DSA (e por conta); o que acontece acima dele
(rejeição, desconexão); throttle por conta. Necessária para o limitador de taxa (relatório 6.3).
