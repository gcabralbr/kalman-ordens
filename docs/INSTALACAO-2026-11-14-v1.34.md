# v1.34 (com a v1.31.1, a v1.32 e a v1.33) na .53: instalação (sábado, 14/11, sem pregão)

Este roteiro **substitui** o `INSTALACAO-2026-11-14-v1.32.md`. A v1.32 não é mais instalada sozinha:
a v1.34 a contém (decidido em 13/11).

O binário vem do worktree limpo `../kalman-ordens-v134`, commit `911caf424346`. A suíte completa
passou nesse commit em 13/11 (`var/suite134c-20261113-142219.log`: FIM rc=0, 20 pacotes, os dois
bancos, `-race`). Depois dele só entraram docs (o §3 da SPEC-ORDENS-v1.34 e este roteiro).

```
sha256  03da077c148b1665529524b5bbae23d45d275b58a969d6682c7eb1885468d2cd  dist/kalman-ordens
```

**O que entra**, por cima da v1.31.1 instalada (`117b8bfefcd4`):
1. **v1.32** (SPEC-ORDENS-v1.32):
   - a ponte sem `FLEXSCAN_WEBHOOK_URL` não espera webhook;
   - a CLI da boleta confere o motivo antes do "sim";
   - o `GET /ordens/{id}` de outro dia lê o banco;
   - os alertas da abertura da Flexscan: a linha única `FLEXSCAN_ABERTURA`,
     `FLEXSCAN_TOLERANCIA_ABERTURA` (08:45) e "(continua desde)". É o teste de segunda (§7).
2. **v1.33** (SPEC-ORDENS-v1.33):
   - §1 só muda os testes;
   - §2: o `LIMITE_ENTRADAS_DIA` passa a valer **por módulo**. BOLETA e KALMAN têm cada um o seu
     teto, e o resto continua global. O `/saude` ganha `entradas_hoje_por_modulo`.
3. **v1.34** (SPEC-ADMIN-CONECTORES-v1, SPEC-ORDENS-v1.34): a tela de admin por conector e ambiente,
   que funciona também com o REAL desligado.
   - Rotas novas: `/conectores`, `/historico/ordens`, `/historico/fio`, e `/sessao/eventos` com
     `ambiente` e `conector`.
   - Os 409 `INSTANCIA_DESLIGADA` e `CONECTOR_AUSENTE` trazem o `historico` da rota.
   - A CLI `fio` mostra o fio de um dia.
   - Migração 0018.

**Banco: a migração 0018.** É a primeira desde a v1.31 e leva o banco de 17 para 18 migrações.
- Ela acrescenta três colunas anuláveis (`ambiente`, `conector`, `canal`) e um índice na
  `kalmanordemsessao`. Nenhuma outra tabela muda.
- As linhas antigas ficam com NULL. A API infere o conector delas e marca "(inferido)".
- O `IF NOT EXISTS` deixa rodar de novo sem erro.
- A atualização em cima do banco da versão instalada está coberta pelo `TestServirAtualizaDoInstalado`
  (o retrato das 17 migrações da v1.31, as mesmas da v1.31.1).

Aqui a migração é aplicada **à mão, com o serviço parado e depois da cópia** (§2–§4), não pela
partida. Assim a partida só confirma "migrações: nenhuma pendente".

**`service.env`:** nada a mudar.
- A v1.32, a v1.33 e a v1.34 não têm variável nova obrigatória. A `FLEXSCAN_TOLERANCIA_ABERTURA`
  vale 08:45 sem estar no arquivo.
- A janela das 08:02 está no arquivo desde 12/11.
- O REAL está desligado (`AMBIENTES_PERMITIDOS=SIM`) desde o fechamento do teste de 13/11, às
  11:42. É assim que a tela nova tem de mostrá-lo (§6).

**A tela (KalmanWIN):** já está instalada desde 13/11, às 15:19 (`ROTEIRO-53-ADMIN-CONECTORES-1311` do
KalmanWIN, deploy `20261113-151853`). Ela sonda o `/conectores` na carga. Com a v1.31.1 (404), fica
no modo antigo. Com a v1.34, passa ao modo novo sem nada a instalar do lado do Kalman: basta
recarregar a página (§6).

**Quando:** sábado, 14/11, a qualquer hora. O que o sábado muda é o mesmo do roteiro da v1.32:
- o login da partida tem de dar OK;
- o token vence no próprio sábado, e a ponte dorme até segunda às 08:02;
- confira mesmo assim que não há posição nem ordem viva.

O serviço fica parado por uns 2 minutos (§2–§5). O Kalman pode avisar que o canal de ordens está
fora nesse intervalo, e isso é esperado.

## 1. Antes

```bash
V=$(/opt/kalman-ordens/kalman-ordens versao); echo $V      # 117b8bfefcd4 (a v1.31.1)
curl -s 127.0.0.1:9092/saude | jq '{ambientes_permitidos, posicao: .ambientes.SIMULADOR.posicao,
  vivas: .ambientes.SIMULADOR.flexscan.ordens_vivas, janela: .ambientes.SIMULADOR.flexscan.janela,
  suspensas: .ambientes.SIMULADOR.operacoes_suspensas}'
#   esperado: ambientes_permitidos ["SIMULADOR"], posicao 0, vivas 0, janela "08:02-18:30", suspensas []
sha256sum /home/gammaquant/kalman-ordens-v134/dist/kalman-ordens
#   03da077c148b1665529524b5bbae23d45d275b58a969d6682c7eb1885468d2cd
/home/gammaquant/kalman-ordens-v134/dist/kalman-ordens versao   # 911caf424346

DB=$(sudo sed -n 's#^DB_DSN=.*)/\([^?"]*\).*#\1#p' /etc/kalman-ordens/service.env | tail -1); echo $DB
#   só o nome do banco (a senha não aparece)
sudo mysql "$DB" -e "SELECT COUNT(*), MAX(versao) FROM kalmanordem_migracao;
  SELECT COUNT(*) FROM kalmanordemsessao; SHOW COLUMNS FROM kalmanordemsessao"
#   17 | 17;  o número de linhas da sessão (anote);  colunas sem ambiente/conector/canal
```

Pare aqui se:
- a posição, as vivas ou as suspensas não estiverem zeradas;
- o sha256 for diferente;
- o REAL ainda estiver permitido;
- o banco não estiver em 17.

## 2. Parar o serviço e copiar o banco

Com o serviço parado, a cópia sai consistente. Ela leva todas as tabelas do serviço
(`kalmanordem*`), não só a que muda.

```bash
sudo systemctl stop kalman-ordens; systemctl is-active kalman-ordens     # inactive
T=$(sudo mysql "$DB" -N -e "SHOW TABLES LIKE 'kalmanordem%'"); echo $T
C=/var/backups/kalman-ordens/antes-0018-$(date +%Y%m%d-%H%M%S).sql.gz
sudo install -d -m 700 /var/backups/kalman-ordens
sudo sh -c "mysqldump --single-transaction '$DB' $(echo $T) | gzip > $C"; echo $C
sudo ls -l $C
sudo sh -c "zcat $C | tail -1"                                           # -- Dump completed on ...
sudo sh -c "zcat $C | grep -c 'INSERT INTO .kalmanordemsessao.'"         # > 0
```

Sem "Dump completed": **não siga**. Volte com `sudo systemctl start kalman-ordens` (a v1.31.1 está
intacta) e me mande a saída.

## 3. Instalar o binário (guardando o atual)

```bash
sudo cp -a /opt/kalman-ordens/kalman-ordens /opt/kalman-ordens/kalman-ordens.$V
sudo install -o root -g root -m 755 /home/gammaquant/kalman-ordens-v134/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
/opt/kalman-ordens/kalman-ordens versao                   # 911caf424346
```

## 4. A migração 0018

```bash
sudo /opt/kalman-ordens/kalman-ordens migrar
#   aplicada: 0018_sessao_conector.sql
sudo mysql "$DB" -e "SELECT versao, nome, aplicada_em FROM kalmanordem_migracao WHERE versao >= 17;
  SHOW COLUMNS FROM kalmanordemsessao; SHOW INDEX FROM kalmanordemsessao WHERE Key_name='ix_kses_em_amb';
  SELECT COUNT(*) FROM kalmanordemsessao"
#   17 e 18 (0018_sessao_conector.sql); colunas ambiente, conector, canal (NULL);
#   o índice ix_kses_em_amb com 3 colunas; o mesmo número de linhas do §1
```

Se o `migrar` der erro, **não suba a v1.34**. Vá ao §8.1 e me mande a mensagem.

## 5. Subir e conferir o serviço

```bash
sudo systemctl start kalman-ordens
sleep 8; systemctl is-active kalman-ordens                 # active
journalctl -u kalman-ordens --since '-1min' --no-pager | grep -E 'subindo|migra|no ar|conector ligado|login|ERROR' | cut -c1-220
#   bom: "kalman-ordens subindo" versao=911caf424346; "migrações: nenhuma pendente";
#        "flexscan: conector ligado" ... webhook_esperado=true (SIMULADOR); "flexscan: login
#        motivo=partida"; "kalman-ordens no ar"
curl -s 127.0.0.1:9092/saude | jq '{versao, ambientes_permitidos,
  flex: .ambientes.SIMULADOR.flexscan | {janela, logado, ativa_por, webhook_esperado, ultimo_login},
  entradas_hoje_por_modulo: .ambientes.SIMULADOR.entradas_hoje_por_modulo}'
#   versao "911caf424346", ["SIMULADOR"], janela "08:02-18:30", logado true, ativa_por null,
#   ultimo_login partida/OK, entradas_hoje_por_modulo {} (a v1.33)
```

**O login da partida precisa dar OK.** Vale o que diz o §3 do roteiro da v1.32: se o
`ultimo_login` não vier `partida`/`OK`, espere um minuto e reinicie de novo. Se falhar outra vez, me
mande o journal. A falha é da Flexscan, e voltar de versão não resolve.

A boleta (item 2 da v1.32), sem enviar nada:

```bash
/opt/kalman-ordens/kalman-ordens boleta enviar -uid 5725 -ambiente REAL -lado C -qtd 1 -stop 300 -por 1 \
  -motivo "123456789012345678901" < /dev/null
#   "motivo com 21 caracteres (maximo 20): nada enviado"
```

## 6. A tela de admin e as rotas novas, com o REAL desligado

### 6.1 As rotas (só GET)

```bash
curl -s 127.0.0.1:9092/conectores | jq -c '.ambientes[] | {ambiente, instancia, historico, motivo,
  conectores: [.conectores[]? | {conector, estado}]}'
#   SIMULADOR: instancia LIGADA, conectores só FLEXSCAN (o PnT da HML está desligado; NO_AR ou DORMINDO)
#   REAL: instancia DESLIGADA, historico true, conectores [], motivo "... fora de AMBIENTES_PERMITIDOS ..."

# os 409, cada um com o histórico que o substitui (dia = hoje, 2026-11-14)
for r in 'pnt/ordens?ambiente=REAL' 'sessao/ao-vivo?ambiente=REAL' 'flexscan/trafego?ambiente=REAL' \
         'pnt/ordens?ambiente=SIMULADOR'; do
  printf '%-32s %s ' "$r" "$(curl -s -o /dev/null -w '%{http_code}' "127.0.0.1:9092/$r")"
  curl -s "127.0.0.1:9092/$r" | jq -r '"\(.codigo) \(.historico)"'
done
#   pnt/ordens?ambiente=REAL         409 INSTANCIA_DESLIGADA /historico/ordens?dia=2026-11-14&ambiente=REAL&conector=PNT
#   sessao/ao-vivo?ambiente=REAL     409 INSTANCIA_DESLIGADA /historico/fio?dia=2026-11-14&ambiente=REAL&conector=PNT
#   flexscan/trafego?ambiente=REAL   409 INSTANCIA_DESLIGADA /historico/fio?dia=2026-11-14&ambiente=REAL&conector=FLEXSCAN
#   pnt/ordens?ambiente=SIMULADOR    409 CONECTOR_AUSENTE    /historico/ordens?dia=2026-11-14&ambiente=SIMULADOR&conector=PNT

# o histórico do REAL de 13/11, do banco, com o REAL desligado
curl -s '127.0.0.1:9092/historico/ordens?dia=2026-11-13&ambiente=REAL' | jq -r '.instancia,
  (.ordens[] | "\(.clordid) \(.conector) \(.estado) \(.ultima_mensagem.msgtype)/\(.ultima_mensagem.ordstatus)")'
#   DESLIGADA; as ordens do teste (KM-*-23475x ..., conector PNT), inclusive a da boleta recusada por margem
curl -s '127.0.0.1:9092/historico/fio?dia=2026-11-13&ambiente=REAL&conector=PNT&limite=500' | jq -r '.instancia, .mais,
  (.mensagens[:6][] | "\(.em) \(.direcao) \(.msgtype) \(.ordstatus // "")")'
#   DESLIGADA; false; as primeiras mensagens do teste (ENV D, REC 8 ...)
curl -s '127.0.0.1:9092/sessao/eventos?dia=2026-11-14&conector=FLEXSCAN' | jq -r '.[] | "\(.em) \(.ambiente) \(.conector) \(.tipo)"' | tail -5
#   as linhas de hoje, gravadas pela v1.34: ambiente SIMULADOR, conector FLEXSCAN
```

### 6.2 A CLI `fio` (o log do QuickFIX do REAL de 13/11, senha mascarada)

```bash
sudo /opt/kalman-ordens/kalman-ordens fio -dia 2026-11-13 -ambiente REAL -sem-heartbeat -eventos | head -5
#   2026-11-13 08:..:..  ENV 8=FIX.4.4|...|35=A|...|554=***|...   (hora de Brasília)
sudo /opt/kalman-ordens/kalman-ordens fio -dia 2026-11-13 -ambiente REAL | grep -cE '\|(554|925)=[^*|]'
#   0 (nenhuma senha aberta)
sudo /opt/kalman-ordens/kalman-ordens fio -dia 2026-11-13 -ambiente REAL -clordid KM-P-234754 | cut -c1-160
#   o stop do teste: o D, o 39=0, o G do ajuste, o 150=I 39=B e a execução
```

Se aparecer "nenhum log de mensagens em …/fix-real/log", o `log/` do REAL não está lá. Isso não
impede a instalação, mas me avise (os arquivos soltos de `fix-real/` eram só os de fora do `store/` e
do `log/`).

### 6.3 Na tela (`kalman-admin.html`, como admin; recarregar a página; nada a gravar)

O esperado é o do §6 do `ROTEIRO-53-ADMIN-CONECTORES-1311` do KalmanWIN:
- **As abas:** aparece "Fio do dia". A aba Ordens ganha "Registradas" (o padrão) e "Consultar
  agora (PnT)". A Sessão ganha os filtros de ambiente e de conector. O seletor do topo mostra
  "REAL (desligado)".
- **Ordens com o REAL:**
  - "Registradas", no dia 13/11, lista as ordens do teste.
  - "Consultar agora" mostra o **painel** "REAL desligado nesta máquina", com o motivo, "ambientes
    ligados: SIMULADOR" e os botões "Ver o fio do dia" e "Ver as ordens registradas". Não aparece
    mais o erro HTTP 503.
  - Os dois botões do painel abrem o histórico certo: o fio do dia e as ordens registradas.
- **PnT ao vivo com o REAL:** o mesmo painel, perguntando a cada 30 s, não a cada 1 s.
- **Fio do dia, 13/11, REAL:** as mensagens do teste, de 500 em 500, sem senha.
- **Sessão:** as linhas de hoje com o ambiente e o conector. As antigas aparecem em "Todos os
  ambientes", marcadas "(inferido)".
- **Operações do dia, o detalhe de uma operação, Parada e Retomar:** iguais a antes.

Se o painel não aparecer e a tela continuar no modo antigo, confira o `/conectores` pelo proxy do
Kalman (o passo 4 daquele roteiro, com `admin-conectores`). Ele deve vir com `"disponivel": true`.

## 7. Segunda, 16/11, das 08:02 às 08:45: o teste da linha única `FLEXSCAN_ABERTURA`

É o mesmo do §4 do roteiro da v1.32, agora com a v1.34. A segunda é o primeiro pregão sem restart de
manhã. A ponte passa o fim de semana dormindo e, às 08:02:00, faz o login `reconexao`. A
conferência pendente desde sábado roda logo depois.

O esperado no Telegram é **uma linha só**, `FLEXSCAN_ABERTURA`, e nenhum crítico:

```
[flexscan] abertura: webhook recadastrado (ausente na conferencia depois do login das 08:02:0x
(reconexao); a Flexscan limpa os webhooks de madrugada)
```

```bash
curl -s '127.0.0.1:9092/flexscan/trafego?limite=30' | jq -r '.mensagens[]
  | select(.tipo=="LOGIN" or .tipo=="WEBHOOK_CADASTRO") | "\(.em) \(.tipo) \(.status_http) \(.duracao_ms) \(.resumo)"'
#   08:02:00 LOGIN reconexao OK, em menos de 1 s; 08:02 conferencia ausente, recadastro,
#   conferencia presente; 08:30 conferencia presente
curl -s '127.0.0.1:9092/sessao/eventos?dia=2026-11-16&conector=FLEXSCAN' | jq -r '.[] | "\(.em) \(.ambiente) \(.tipo) \(.texto)"' | cut -c1-200
#   (v1.34) a mesma linha da abertura gravada com SIMULADOR / FLEXSCAN
```

Se o login das 08:02:00 ficar sem resposta, a tolerância cuida dele até as 08:45:
- ela não alerta;
- ela tenta de novo;
- a mesma linha passa a contar a tentativa: "logou 08:02:3x na 2a tentativa (…)".

Me mande essa linha. Se vier `FLEXSCAN_LOGIN` ou `FLEXSCAN_WEBHOOK_AUSENTE` antes das 08:45, me mande
o texto.

## 8. Como voltar

### 8.1 Se o `migrar` falhou (§4), ou a v1.34 não sobe

```bash
sudo install -o root -g root -m 755 /opt/kalman-ordens/kalman-ordens.$V /opt/kalman-ordens/kalman-ordens
sudo systemctl start kalman-ordens      # (restart, se estiver de pé)
/opt/kalman-ordens/kalman-ordens versao                   # 117b8bfefcd4
```

A v1.31.1 sobe com o banco em 17 e também em 18. Pelo código da v1.31.1:
- ela só aplica as migrações dela e não estranha a 0018 registrada;
- ela grava na `kalmanordemsessao` pelos nomes das colunas (`tipo, texto, em`), e as colunas novas
  ficam NULL;
- ela lê só `tipo, texto, em`.

Por isso a volta **não precisa desfazer a migração nem restaurar a cópia**. A tela volta sozinha ao
modo antigo (o `/conectores` dá 404).

### 8.2 Restaurar a cópia: só se a `kalmanordemsessao` ficou danificada

Faça isso só depois de falar comigo. O dump tem `DROP TABLE` e `CREATE TABLE` das tabelas
`kalmanordem*`, então o que foi gravado depois da cópia se perde. Antes de restaurar, volte a
v1.31.1 (§8.1) com o serviço parado.

```bash
sudo systemctl stop kalman-ordens
sudo sh -c "zcat $C | mysql '$DB'"
sudo mysql "$DB" -e "SELECT COUNT(*), MAX(versao) FROM kalmanordem_migracao"   # 17 | 17
sudo systemctl start kalman-ordens
```

## 9. Depois (eu)

- conferir a versão e o journal;
- `scripts/instalado-migracoes.sh` (o retrato passa a ser o 911caf424346, com 18 migrações) e o
  commit;
- merge do `v1.34` no `main` (ele já tem o `v1.31.1`, o `v1.32` e o `v1.33`) e push;
- só depois disso, remover os worktrees `../kalman-ordens-v132`, `-v132b`, `-v132c`, `-v133` e
  `-v1311`. O `-v134` fica até a segunda.
- a cópia do §2 fica em `/var/backups/kalman-ordens/` até a v1.35.
