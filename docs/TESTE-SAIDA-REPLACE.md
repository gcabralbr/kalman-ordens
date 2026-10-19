# Teste manual da saída por replace (v1.11, HML, .53)

Pré-requisitos: kalman-ordens com o B instalado (`kalman-ordens versao` = o commit do B),
`SAIDA_POR_REPLACE=1` em `/etc/kalman-ordens/service.env`, a kalman-hml **desligada** (o 1450 tem
posição máxima 1 em todos os módulos), posição 0, pnt-md-hml no ar (9096). Módulo `TESTE`,
`operacao_id` na faixa 990000001+ (não colide com a KHML). `POR` = o uid do admin.

```
B=127.0.0.1:9092; POR=<uid do admin>; G=var/pnt-md-hml-WINZ26-$(date +%Y%m%d).jsonl
P=tools/pnt-md-spike/.venv/bin/python
entrada() { curl -s -X POST $B/ordens -H 'Content-Type: application/json' -d "{\"modulo\":\"TESTE\",\"operacao_id\":$1,\"uid\":1450,\"papel\":\"ENTRADA\",\"lado\":\"$2\",\"qtd\":1,\"stop_pts\":500,\"motivo\":\"TESTE\"}"; echo; }
saida()   { curl -s -X POST $B/ordens -H 'Content-Type: application/json' -d "{\"modulo\":\"TESTE\",\"operacao_id\":$1,\"uid\":1450,\"papel\":\"SAIDA\",\"lado\":\"$2\",\"qtd\":1,\"motivo\":\"MANUAL\"}"; echo; }
ver()     { curl -s "$B/ordens/$1?modulo=TESTE"; echo; }
fix()     { curl -s "$B/operacoes/$1/historico?modulo=TESTE&cru=1" | python3 -c "
import json,sys
d=json.load(sys.stdin)
for m in d['mensagens']:
    t=dict(x.split('=',1) for x in (m.get('fix_raw') or '').replace('\x01','|').split('|') if '=' in x)
    print(m['em'][11:23], m['direcao'], '35='+t.get('35',''), '11='+t.get('11',''), '150='+t.get('150',''), '39='+t.get('39',''), '44='+t.get('44',''), '20828='+t.get('20828',''), '31='+t.get('31',''), '52='+t.get('52','')[9:], t.get('58','')[:80])
for e in d['eventos']: print('EV', e['em'][11:23], e['tipo'], json.dumps(e.get('dados'))[:160])"; }
```

## T0. Antes

```
curl -s $B/saude    # versao = commit do B, sessao_logada, posicao 0, parada null
journalctl -u kalman-ordens -f | grep -E "saída por replace|SAIDA_REPLACE|REPLACE decidido|disparada|FILL|ZERADA|ALERTA"   # em outro terminal
```

## T1. Entrada (compra) e proteção

```
entrada 990000001 C
ver 990000001        # estado PROTEGIDA; a 1014 com preco_stop = médio − 500
```

Conferir: `aceito:true`; `ENTRADA_EXECUTADA` e `PROTEGIDA` no journal.

## T2. Saída MANUAL — **a pergunta principal: a 1014 aceita o gatilho além do preço?**

```
saida 990000001 V
ver 990000001        # logo depois: estado SAINDO, a 1014 com preco_stop ACIMA do mercado
fix 990000001
```

Conferir, nesta ordem:

1. journal: `saída por replace da 1014 ... gatilho=G limite=L degrau=1`, com G = max(bid, último) +
   100 e L = bid − 50 (o book da 9096 no instante);
2. FIX: `→ 35=G 11=KM-TESTE-P-990000001-2 ... 20828=G 44=L`;
3. **a resposta do PnT** (o dado de hoje):
   - **aceito:** `← 150=E` e `← 150=5` (anotar os dois tempos e o 52), depois `← 150=I 39=B` com
     [DSA-122] (o disparo) e `← 150=F` (fill). Eventos: `SAIDA_POR_REPLACE`, `SAIDA_EXECUTADA
     origem=GESTOR`, `ZERADA motivo=MANUAL origem=GESTOR`; **nenhum** `PROTECAO_DISPARADA`,
     `PROTEGIDA` ou `PROTECAO_AJUSTADA` depois do pedido;
   - **150=5 sem disparo em 2 s:** novos 35=G (degrau 2, 3...) a cada 2 s; esgotado,
     `SAIDA_REPLACE_FALHOU seguinte=CANCEL_E_NOVA` e o caminho de antes (35=F, 39=4, 35=D);
   - **recusado (35=9):** anotar o texto 58; `SAIDA_REPLACE_FALHOU seguinte=CANCEL_E_NOVA` e o caminho
     de antes, na hora;
4. `ver 990000001`: ZERADA, posição 0; `curl -s $B/saude`: posição 0, sem alerta crítico, sem
   `PNT_BOLSA_INDISPONIVEL`.

Tempos: `$P tools/pnt-md-spike/medidas.py saidas $G` (com `--modulo TESTE`), linha "POR REPLACE":
pedido → 35=G → 150=5 → 150=I → fill, e o custo contra o topo do book no pedido.

## T3. Encerrar pelo admin (origem ADMIN)

```
entrada 990000002 C
curl -s -X POST "$B/operacoes/990000002/encerrar?modulo=TESTE" -H 'Content-Type: application/json' -d "{\"motivo\":\"teste saida por replace\",\"por\":$POR}"; echo
fix 990000002
```

Conferir: resposta `ENCERRANDO`; o mesmo fluxo do T2; `ZERADA origem=ADMIN`.

## T4. Venda (o espelho) e repetições

```
entrada 990000003 V
saida 990000003 C    # gatilho = min(ask, último) − 100; limite = ask + 50
```

Repetir T2/T4 algumas vezes (ids seguintes) para os tempos. No fim:
`$P tools/pnt-md-spike/medidas.py saidas $G --modulo TESTE` e `... ajustes --modulo TESTE`.

## Se algo sair errado

- posição presa: `curl -s -X POST "$B/operacoes/<id>/encerrar?modulo=TESTE" ...` (LEIA 1.11, com
  `modulo=TESTE`);
- desligar o caminho novo: `SAIDA_POR_REPLACE=0` no service.env + restart (sem posição): volta ao
  cancel + 1031.
