# Implantação

Deploy = `git clone`/`git pull` no servidor + `scripts/build.sh` + instalar `dist/kalman-ordens`.
O binário é estático (linux/amd64, sem cgo) e leva o commit embutido: aparece em
`kalman-ordens versao`, no `/saude` (`versao`) e no log ao subir. Build de árvore com alteração não
commitada sai com o sufixo `-sujo` — não instalar um desses.

Ordem das fases (SPEC v1 Parte 12): F1 na **.53** (dev, HML do PnT) → F2 na **.51** (produção,
ordens que não executam) → F3 (1 contrato, 1 usuário).

---

## 1. F1 na .53 (HML do PnT)

A .53 já está sincronizada com a .51: banco `dbecotools` (cópia de hoje), marketdata no mesmo
commit da produção (segundo login da Cedro, ouvindo em `172.16.110.53:9090`) e Kalman no mesmo
commit da produção. MariaDB da .53 é o mesmo 10.11 (clone da .51). O Kalman **não** chama o serviço
na F1 (a integração do gestor, Parte 2.3, ainda não existe): as ordens de teste saem por `curl`.

### 1.1 Código e build

A .53 já tem chave SSH com acesso aos repositórios `gcabralbr` (é por ela que clona o marketdata
e o kalmanwin por `git@github.com`): sem chave de deploy, o clone é por SSH. Go 1.26 já instalado
(mínimo 1.24).

```
cd ~ && git clone git@github.com:gcabralbr/kalman-ordens.git && cd ~/kalman-ordens
scripts/build.sh                 # dist/kalman-ordens + sha256
dist/kalman-ordens versao        # tem de ser o commit, sem -sujo
```

Atualizar depois: `cd ~/kalman-ordens && git pull && scripts/build.sh`, reinstalar o binário e
rodar `kalman-ordens migrar` antes de reiniciar (1.4).

### 1.2 Banco (schema próprio, local na .53)

```sql
CREATE DATABASE kalmanordens CHARACTER SET utf8mb4;
CREATE USER 'kalman_ordens'@'127.0.0.1' IDENTIFIED BY '<senha da .53>';
GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, INDEX ON kalmanordens.* TO 'kalman_ordens'@'127.0.0.1';
-- conciliação diária (Parte 10): só leitura, só esta tabela
GRANT SELECT ON dbecotools.kalmanoperacao TO 'kalman_ordens'@'127.0.0.1';
-- conta e destino do usuário (SPEC v1.2 U1): só estas três colunas; nada mais no dbecotools
GRANT SELECT (usucodusuario, usutxtconfig, usutxtconfigprd) ON dbecotools.acusuario TO 'kalman_ordens'@'127.0.0.1';
```

Instalação que já existia antes da v1.2: rodar só o último `GRANT`.

Sem `DROP` e sem `DELETE`. As tabelas nascem com `kalman-ordens migrar` (1.4), sem subir o
serviço; `kalmanordem_migracao` guarda a versão (o serviço também aplica as pendentes ao subir, e
a CLI `usuario` recusa trabalhar com o schema atrás do binário). O `sql_mode` é fixado pelo serviço em cada conexão
(`STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION`, o
valor da .51); não depende da configuração global.

### 1.3 Usuário de sistema e diretórios

```
sudo useradd --system --home /var/lib/kalman-ordens --shell /usr/sbin/nologin kalman-ordens
sudo install -d -o kalman-ordens -g kalman-ordens -m 700 /var/lib/kalman-ordens /var/lib/kalman-ordens/fix
sudo install -d -o root -g root -m 755 /opt/kalman-ordens
sudo install -d -o root -g kalman-ordens -m 750 /etc/kalman-ordens
```

Permissões: binário `root:root 755`; `/etc/kalman-ordens/service.env` `root:kalman-ordens 640`
(o serviço lê, não escreve); `/var/lib/kalman-ordens` (store e log FIX) **só do usuário do
serviço: diretórios 700, arquivos 600, dono `kalman-ordens`**. O log FIX é fiel ao fio: a senha
sai mascarada (`554=***`), mas o 9 do logon revela o tamanho dela (SPEC v1.1 F15). O **store**
(`fix/store`) guarda o logon como foi ao fio, **com a senha**: é o que o reenvio relê (SPEC v1.3
A1) — mesma proteção do service.env. A unidade usa
`UMask=0077` e `StateDirectoryMode=0700`, e o serviço aperta as permissões ao subir. Conferir
depois da primeira subida:

```
sudo find /var/lib/kalman-ordens -printf '%m %u:%g %p\n'   # só 700 (diretórios) e 600 (arquivos), kalman-ordens
```

### 1.4 Binário, service.env e unidade — instalada e PARADA

```
sudo install -o root -g root -m 755 ~/kalman-ordens/dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
sudo install -o root -g root -m 644 ~/kalman-ordens/deploy/LEIA.md /opt/kalman-ordens/LEIA.md
sudo install -o root -g kalman-ordens -m 640 ~/kalman-ordens/deploy/service.env.example /etc/kalman-ordens/service.env
sudo install -o root -g root -m 644 ~/kalman-ordens/deploy/kalman-ordens.service /etc/systemd/system/kalman-ordens.service
sudo systemctl daemon-reload
# NÃO habilitar nem iniciar antes da terça:
systemctl is-enabled kalman-ordens   # disabled
systemctl is-active kalman-ordens    # inactive
```

Preencher `/etc/kalman-ordens/service.env` (`sudoedit`) para a .53 / HML:

| Chave | Valor na .53 (F1) |
|---|---|
| `DB_DSN` | `kalman_ordens:<senha>@tcp(127.0.0.1:3306)/kalmanordens` |
| `FIX_HOST`, `FIX_PORTA` | `54.94.189.36`, `45985` (HML; da config antiga do PT na .53, `quickfixj-client-pnt.cfg`) |
| `FIX_SENDER_COMP_ID` | `GAMMAQ2` |
| `FIX_TARGET_COMP_ID` | `PNT_DSA` (o `PNT_GATEWAY_HML` do RoE 2025 era de outro ambiente) |
| `FIX_HEARTBEAT` | `30` |
| `FIX_USUARIO`, `FIX_SENHA` | a senha da HML, preenchida pelo responsável direto no service.env (nunca no repositório); usuário como na config antiga (vazio se ela não tinha) |
| `FIX_INICIO`, `FIX_FIM` | horário da sessão da HML |
| `ORDENS_AMBIENTE` | `HML` — o serviço lê a `usutxtconfig` do usuário no GQ (simulação; no GQ o usuário escolhe "BTG_Simulador") |
| `ORDENS_CONTAS_HML` | `99999` — a conta de HML; em HML a conta do usuário **tem** de estar aqui (trava cruzada, v1.2 U3) |
| `ORDENS_SENDER_LOCATION` | `177.124.234.134` (o 142 das ordens do PT no log de HML) |
| `PRECOS_URL` | rodada 1: `http://172.16.110.53:9090` (marketdata ao vivo); rodada 2: a instância em replay (`http://172.16.110.53:9095`, 1.7). É a única chave de preço (não há `ORDENS_PRECO_URL`) |
| `TELEGRAM_TOKEN`, `TELEGRAM_CHAT` | chat de **teste**, não o de produção |
| `HTTP_ENDERECO` | `127.0.0.1:9092` (o Kalman da .53 está no 9091, o marketdata no 9090) |

Histórico (SPEC v1.5): `kalman-ordens historico operacao -id N [-cru]`, `historico dia [-dia D]
[-uid N]`, `historico sessao [-dia D]` (JSON, direto no banco; `sudo` pelo service.env). As mesmas
consultas em `GET /operacoes/{id}/historico`, `GET /operacoes?dia=` e `GET /sessao/eventos?dia=`.

Saíram na v1.2 e **o serviço não sobe se estiverem no service.env**: `ORDENS_CONTA`,
`ORDENS_ONBEHALF`, `ORDENS_SIMBOLO`, `ORDENS_VENCIMENTO`, `ORDENS_UID_REAL`. Conta e destino vêm
do GQ (`acusuario` do uid, no ambiente), o símbolo é o ticker do marketdata (`WINV26` até quarta,
o mesmo da HML de terça) e a habilitação é pela CLI (1.5).

**Migrar logo em seguida** (com o binário e o service.env instalados; só banco, sem FIX nem HTTP):

```
sudo /opt/kalman-ordens/kalman-ordens migrar   # aplicada: 0001_inicial.sql, 0002_conta_simbolo_habilitacao.sql, 0003_execid_por_ordem.sql, 0004_modulo.sql, 0005_stop_ajustado.sql, 0006_historico.sql, 0007_pedidos.sql, 0008_sinais.sql, 0009_motivo_60.sql, 0010_quarentena.sql
sudo /opt/kalman-ordens/kalman-ordens migrar   # de novo: nenhuma migração pendente
```

O binário **sem comando não sobe o serviço**: mostra a ajuda e sai com erro. O serviço só sobe
com `servir` (o `ExecStart` da unidade).

`operacao_id` dos testes: usar a faixa `900000000+`, que nunca colide com a `kalmanoperacao`.

### 1.5 Comandos da F1

Habilitação (v1.2 U5): só pela CLI, direto no banco (lê o `DB_DSN` do service.env; por isso
`sudo`). No GQ, o usuário de teste (ex.: 1450) escolhe "BTG_Simulador": destino 85, conta 99999 na
`usutxtconfig`. `usuario conferir` mostra o que uma entrada dele usaria agora.

```
sudo /opt/kalman-ordens/kalman-ordens usuario conferir    -uid 1450   # destino=85 conta=99999
sudo /opt/kalman-ordens/kalman-ordens usuario habilitar   -uid 1450 -motivo "F1 rodada 1" -posicao-max 1 -qtd-max 1
sudo /opt/kalman-ordens/kalman-ordens usuario listar
sudo /opt/kalman-ordens/kalman-ordens usuario desabilitar -uid 1450 -motivo "fim da rodada"   # sem -posicao-max/-qtd-max, os limites ficam os atuais (v1.8 H4)
# v1.10: o serviço lê a habilitação de um cache relido a cada 30 s (CACHE_USUARIOS_S): o que a CLI
# grava vale em até 30 s; a tela de admin (POST /usuarios/{uid}/habilitacao) vale na hora
```

```
sudo systemctl start kalman-ordens
journalctl -u kalman-ordens -f
/opt/kalman-ordens/kalman-ordens saude

# entrada de teste (o uid é o habilitado acima)
curl -s -XPOST 127.0.0.1:9092/ordens -d '{"operacao_id":900000001,"uid":<uid>,"papel":"ENTRADA","lado":"C","qtd":1,"stop_pts":500,"motivo":"SINAL","ts_decisao":"F1-1"}'
# saída
curl -s -XPOST 127.0.0.1:9092/ordens -d '{"operacao_id":900000001,"uid":<uid>,"papel":"SAIDA","lado":"V","qtd":1,"motivo":"SINAL_OPOSTO","ts_decisao":"F1-1s"}'
curl -s 127.0.0.1:9092/ordens/900000001
curl -s '127.0.0.1:9092/eventos?desde=0'

/opt/kalman-ordens/kalman-ordens parar   -motivo "..."
/opt/kalman-ordens/kalman-ordens zerar   -motivo "..."
/opt/kalman-ordens/kalman-ordens retomar -motivo "..."
```

O log FIX completo do dia fica em `/var/lib/kalman-ordens/fix/log`; guardar com o journal ao fim
de cada sessão de teste (é a evidência das respostas do PnT).

### 1.6 Atenção na HML

- **Uma sessão `GAMMAQ2` por vez.** O processo antigo do PT na .53 usava a mesma sessão: ele não
  pode estar conectado na HML enquanto o serviço estiver no ar (o PnT derruba um dos dois, e a
  sequência fica trocada entre eles). Não há IP a liberar: a .53 já conectava nessa HML.
- A HML de terça reproduz a **sexta**; o marketdata ao vivo da .53 é o de terça. Na rodada 1 o
  preço não importa (só ordens que não executam); o fill fica para a rodada 2, com o marketdata em
  replay da sexta (1.7).
- Símbolo: o ticker do marketdata da .53 (`WINV26` até quarta), gravado na operação; é o mesmo
  da HML de terça. Não há símbolo no service.env (v1.2 S1).
- **BodyLength (9) e CheckSum (10).** A config antiga do PT tinha `ValidateLengthAndChecksum=N`
  e os exemplos do RoE têm 9/10 inconsistentes. O QuickFIX/Go não tem essa opção: o 10 errado é
  aceito (não é conferido); a mensagem com 9 errado é **descartada pela biblioteca** e recuperada
  por ResendRequest — o serviço avisa na hora (`MENSAGEM_ILEGIVEL`, crítico) e pede mass status
  (SPEC v1.1 F11, F12). Na terça, procurar `MENSAGEM_ILEGIVEL` / `Msg Parse Error` no log FIX.
- Parar o serviço não cancela nada no PnT. Se o PnT tiver Cancel-on-Disconnect ligado por padrão
  [PnT-12], a 1014 morre a cada restart — é um dos testes (2.6).
- **Queda abrupta (SIGKILL, travamento, rede): até ~4 min sem sessão.** O PnT segura a sessão
  antiga e recusa os logons novos com "already logged on" (14/10: ~3,5 min). O serviço tenta de
  novo com espera crescente (5, 10, 20, 40, 60 s…; `FIX_RECONEXAO_MAX_S`) e, depois de 60 s
  recusando, alerta `SESSAO_PRESA_NO_PNT` (crítico). A 1014 continua no PnT protegendo a posição.
  Para parar, sempre `systemctl stop` (logout limpo: a sessão volta na hora).

### 1.7 F1 em duas rodadas

**Rodada 1 (terça): só protocolo.** Nada depende de preço: as ordens ficam longe do livro e não
executam. Itens marcados "1" no checklist.

**Rodada 2: fill e disparo da 1014 — a detalhar.** Uma segunda instância do marketdata na .53,
em modo replay, numa porta própria (ex. `9095`), toca a captura de sexta; o serviço passa a ler
dela trocando só o `PRECOS_URL` no service.env (`http://172.16.110.53:9095`) e reiniciando. Itens
marcados "2". Procedimento do replay, horários e critérios: a detalhar antes da rodada.

A bancada da SwingTrade exploratória [PnT-9, PnT-10] fica para depois da rodada 2.

**Atenção (2.5 e 2.6):** o serviço só envia a 1014 depois de um fill de entrada. Na rodada 1 a
venda limite executou (a HML é o pregão de ontem, ~3400 pts acima), e a 1014 saiu pelo caminho
normal.

### 1.8 Depois da rodada 1 (14/10): antes de qualquer novo start na .53

O binário `03c7e05` **não pode subir de novo** na .53: o store FIX dele tem os logons de 14/10
gravados com a senha mascarada e o reenvio entra em laço (SPEC v1.3 A1). A versão nova repara o
store sozinha na subida.

```
cd ~/kalman-ordens && git pull && scripts/build.sh
dist/kalman-ordens versao                      # o commit novo, sem -sujo
sudo install -o root -g root -m 755 dist/kalman-ordens /opt/kalman-ordens/kalman-ordens
sudo /opt/kalman-ordens/kalman-ordens migrar   # aplicadas: 0003_execid_por_ordem.sql (ExecID por ordem, v1.3 H1) e 0004_modulo.sql (módulo, v1.3 M1)
sudo /opt/kalman-ordens/kalman-ordens migrar   # de novo: nenhuma migração pendente
sudo systemctl start kalman-ordens
journalctl -u kalman-ordens | grep -E 'store reparado|logon recusado|sincronia|ALERTA'
```

Esperado na primeira subida: `pnt: store reparado reparo="N mensagem(ns), K com 9/10
recalculados, 0 trocada(s) por gap fill"` (K = os logons de 14/10), logon aceito e, se o PnT pedir
reenvio, gap fill — nada de `Resend Msg Parse Error` no log de eventos FIX. Não há chave nova
obrigatória no service.env (`FIX_RECONEXAO_MAX_S`, `FIX_SINCRONIA_MS` e `SESSAO_PRESA_ALERTA_S`
têm padrão; `ESPERA_ENCERRAMENTO_S` passou a 8). A cada logon o journal registra a janela de
sincronia (`recebidas_na_janela`, `reenvios_na_janela`; v1.3 A2): anotar em todo logon da HML — um
ResendRequest do PnT fora da janela quer dizer que 500 ms não bastam (`FIX_SINCRONIA_MS`). Depois:
o teste 2.6b.

### 1.9 Preço da HML pelo tick file do Kalman (`precos-hml`, só na .53)

A HML do PnT reproduz o pregão do **dia útil anterior**; o marketdata da .53 é o de hoje. O
`precos-hml` serve o `/precos/atual?ativo=WINFUT` no **mesmo contrato do marketdata**
([docs/referencia/precos-atual.md](../docs/referencia/precos-atual.md)) a partir do tick file do
Kalman daquele dia (`/var/lib/kalman/tick/winfut-AAAA-MM-DD.csv.gz` ou `.csv`), em
`127.0.0.1:9095`. Sem banco e sem Cedro. Substitui o replay do marketdata da 1.7.

- A cada pedido devolve a **última linha com horário <= a hora do dia de agora** (`preco_obs`,
  `bid`, `ask`, `status84`; campo vazio no arquivo é omitido). `ticker` = o contrato vigente
  (parâmetro). `ts` = hora da linha − `idade_seg` da linha, na data de hoje; `idade_seg` = agora − `ts`.
- Antes da primeira linha ou depois da última (o arquivo do Kalman vai de ~09:00 a ~14:01): `{}`,
  como o marketdata sem dado — o serviço recusa entradas com `SEM_PRECO`.
- Dia do arquivo: `PRECOS_HML_DATA` (`AAAA-MM-DD`) ou, vazio, o dia útil anterior pelo
  `/var/lib/kalman/feriados.json` (gerado pelo Kalman da `anferiado` a cada subida), recalculado
  na virada do dia. Sem o arquivo do dia, não sobe (e responde 503 se faltar depois).
- `PRECOS_HML_ADIANTO_MS` (padrão 0; `-adianto-ms`): a leitura do arquivo vai N ms à frente da
  hora de agora (negativo: atrás), para alinhar com a fita que a HML toca; o `ts` acompanha. Medição
  de 16/10 (15 fills de 09:07 a 10:03 contra a fita de 15/10): o melhor encaixe é com a HML **atrás**
  da precos-hml, de 1,4 a 2,0 s (Δ ≈ −1.400 a −2.000 ms) — evidência fraca (erro médio de 25 pts no
  melhor Δ). O relógio do PnT (o 52) está ~0,45 s atrás do nosso (chrony).

Instalar (o `scripts/build.sh` gera `dist/precos-hml` junto):

```
sudo install -o root -g root -m 755 ~/kalman-ordens/dist/precos-hml /opt/kalman-ordens/precos-hml
sudo install -o root -g root -m 644 ~/kalman-ordens/deploy/kalman-precos-hml.service /etc/systemd/system/kalman-precos-hml.service
sudo tee /etc/kalman-ordens/precos-hml.env >/dev/null <<'EOF'
PRECOS_HML_TICKER=WINZ26
PRECOS_HML_DATA=
PRECOS_HML_PORTA=9095
PRECOS_HML_ADIANTO_MS=0
EOF
sudo systemctl daemon-reload
sudo systemctl start kalman-precos-hml          # não habilitar: ferramenta de teste
journalctl -u kalman-precos-hml                 # "winfut-2026-10-14.csv.gz, N linhas, 09:00:38.915 a 14:00:59.976"
curl -s '127.0.0.1:9095/precos/atual?ativo=WINFUT'
```

A unidade roda como `gammaquant` (dono de `/var/lib/kalman`, 750), só leitura. Rodar à mão:
`dist/precos-hml -ticker WINZ26 [-data 2026-10-14] [-porta 9095] [-adianto-ms N]`.

No kalman-ordens em HML (`sudoedit /etc/kalman-ordens/service.env`, depois `systemctl restart
kalman-ordens`):

- `PRECOS_URL=http://127.0.0.1:9095`
- **colares de volta ao padrão**: `COLAR_ENTRADA_PTS=15` e `COLAR_SAIDA_PTS=50` (ou as duas linhas
  comentadas) — com o preço da HML, as ordens voltam a ser as de produção.

Ao fim do teste: `PRECOS_URL` de volta ao marketdata (`http://172.16.110.53:9090`), restart do
kalman-ordens e `sudo systemctl stop kalman-precos-hml`.

### 1.10 Preço da HML pelo feed da própria HML (`pnt-md-hml`, só na .53)

A precos-hml serve o tick file do Kalman do pregão anterior; o `pnt-md-hml` serve **o que a HML
toca de fato**, lido do WebBroker Market Data (WB_MD) da PnT (SignalR `/mdhub`), no **mesmo contrato
do marketdata**, em `127.0.0.1:9096`: `preco` = último LastTrade, `bid`/`ask` = topo do AggrBook,
`status84` = 0 com o papel em negociação (`SecurityStatus` "Trading" / 17; senão ≠ 0), `ts` /
`idade_seg` pela chegada. Sem fonte — feed caído (`OnFeederUnavailable`), conexão fechada, token
inválido, sem dado de hoje — responde `{}` (o kalman-ordens recusa a entrada com `SEM_PRECO`). Cada
queda e volta vai ao journal. Medição de 16/10: a HML toca os negócios do pregão anterior ~0,8 s
atrás do horário original (SPEC v1.10, seção 8.1).

Token (uma vez, como `gammaquant`; a unidade renova sozinha pelo refresh token). Fica em
`/var/lib/kalman-pnt-md-hml/token.json` (fora do `/home`, escrita atômica; o `StateDirectory` da
unidade cria o diretório, dono `gammaquant`, 700). O md_spike e o pnt-md-hml usam o **mesmo** arquivo
(`PNT_MD_TOKEN` muda os dois): o refresh token gira a cada renovação.

```
sudo install -d -o gammaquant -g gammaquant -m 700 /var/lib/kalman-pnt-md-hml   # se a unidade ainda não subiu
cd ~/kalman-ordens && tools/pnt-md-spike/.venv/bin/python tools/pnt-md-spike/md_spike.py login-auto --usuario pnt_external_gammaq --corretora SIMULACAO
```

Migração de um token antigo (`~/.config/pnt-md-spike/token.json`, até 21/10), com o pnt-md-hml
parado: `install -m 600 ~/.config/pnt-md-spike/token.json /var/lib/kalman-pnt-md-hml/token.json`
(o `install` cria o arquivo com o contexto SELinux do diretório; um `mv` levaria o do `/home`) e,
depois de o serviço subir com `token_ok`, apagar o antigo. Se o `/etc/kalman-ordens/pnt-md-hml.env`
tiver `PNT_MD_TOKEN`, tirar ou apontar para o arquivo novo.

**Reconexão automática** (21/10): book degenerado com o papel em negociação (status84 0) por mais de
`PNT_MD_RECONECTAR_S` (10) → fecha o websocket com close frame, espera o close do servidor e abre um
cliente novo (negotiate e connectionId novos) depois de `PNT_MD_ESPERA_RECONEXAO_S` (5); no máximo uma
a cada `PNT_MD_RECONECTAR_INTERVALO_S` (60); depois de `PNT_MD_RECONECTAR_ALERTA_APOS` (5) seguidas
sem book normal, `CRITICAL` no journal a cada tentativa. No `/saude`: `reconexoes_book`,
`reconexoes_seguidas`, `reconexao_alerta`, `ultima_reconexao` (hora, motivo, `close_servidor`). Toda
queda (inclusive a que o servidor fecha) abre um cliente novo: a reconexão interna do pysignalr, que
reusa o `?id=` da conexão antiga sem negotiate, não é usada. Stop/restart (SIGTERM) também fecham
com close frame. Reconexão manual pelo mesmo caminho: `curl -s -X POST 127.0.0.1:9096/reconectar`
(o resultado sai em `ultima_reconexao` no `/saude`).

Instalar e subir (o código e o ambiente Python ficam no repositório: `tools/pnt-md-hml`):

```
cd ~/kalman-ordens && git pull
python3 -m venv tools/pnt-md-hml/.venv && tools/pnt-md-hml/.venv/bin/pip install -r tools/pnt-md-hml/requirements.txt
sudo install -o root -g root -m 644 deploy/kalman-pnt-md-hml.service /etc/systemd/system/kalman-pnt-md-hml.service
sudo systemctl daemon-reload
sudo systemctl start kalman-pnt-md-hml          # não habilitar: ferramenta de teste
journalctl -u kalman-pnt-md-hml -f              # "fonte de volta (conectado)"
curl -s '127.0.0.1:9096/precos/atual?ativo=WINFUT'
curl -s '127.0.0.1:9096/saude'                  # disponivel, conectado, feed_ok, token_ok, motivo, reconexoes_book
```

No kalman-ordens em HML: `PRECOS_URL=http://127.0.0.1:9096` e restart. Ao fim do teste: de volta
ao `PRECOS_URL` anterior, restart do kalman-ordens e `sudo systemctl stop kalman-pnt-md-hml`.

**O Kalman da .53 NÃO vai para a 9096 só por configuração** (verificado no código em 16/10). A
fonte dele é `KALMAN_GO_BASE` (`/etc/kalman/service.env`; padrão `http://172.16.110.53:9090`), mas:

1. ele pede `GET /precos/atual` **sem** `?ativo=` (`fonte_dados.py`). O pnt-md-hml aceita: sem
   `ativo` vale WINFUT; outro ativo continua 400 (a precos-hml segue exigindo `ativo=WINFUT`). Não
   há WDOFUT nem `/residuo/atual` na 9096: o Kalman tenta o `/residuo/atual` como reserva (404) e
   o `monitor.py` dele marca o WDOFUT como sem preço;
2. o histórico do motor (os candles de 5 min) vem da `precocandle` do mercado **real** de hoje;
   trocar só o preço ao vivo misturaria o pregão real com o D-1 que a HML toca;
3. o Kalman da .53 atende todos os usuários daquela instância: a troca muda os sinais e o paper de
   todos.

Enquanto isso não for decidido, o Kalman decide sobre o mercado real e o kalman-ordens executa
contra o book da HML (D-1): a comparação decisão → execução e o PnL do REAL **não** medem o mesmo
preço.

**Volta (sempre sem posição aberta e antes de ligar o REAL do 1450):** a precos-hml (9095)
continua rodando; voltar é `PRECOS_URL=http://127.0.0.1:9095` + `sudo systemctl restart
kalman-ordens` (e o Kalman, se um dia a fonte dele for trocada). O `PRECOS_HML_ADIANTO_MS` (ex.:
−900, para alinhar a precos-hml com a HML, que toca a fita ~0,8–1 s atrasada) só vale para a
precos-hml — e a instalada na .53 é a `bca4f5b`, anterior ao parâmetro (instalar o binário novo do
`scripts/build.sh` para usar).
Opcional em `/etc/kalman-ordens/pnt-md-hml.env`: `PNT_MD_ATIVO` (WINZ26), `PNT_MD_PORTA` (9096),
`PNT_MD_TOKEN`.

**Gravação bruta (19/10):** o serviço grava cada mensagem recebida do WB_MD (LastTrade com Time e
TradeID, AggrBook, SecurityStatus, quedas e voltas) em `var/pnt-md-hml-<ATIVO>-AAAAMMDD.jsonl`
(append, um por dia, formato do md_spike: o `tools/pnt-md-spike/medidas.py` lê). Escrita numa thread
com fila; erro de escrita vai ao journal e o serviço segue; `/saude` mostra `gravadas` e
`descartadas`. `PNT_MD_GRAVAR_DIR=` (vazio) desliga. A unidade precisa de `ReadWritePaths=... /home/gammaquant/kalman-ordens/var`
(reinstalar a unidade + `daemon-reload`). **O WB_MD aceita uma conexão por usuário** (409 "Já
existe uma conexão ativa para este usuário"): não rodar o md_spike junto com o serviço.

Testes: `tools/pnt-md-hml/.venv/bin/python -m unittest tools/pnt-md-hml/test_pnt_md_hml.py`
(unidade e a gravação de 16/10 como fonte) e `go test ./internal/preco -run PntMdHml` (o contrato pelo
parser do serviço).

### 1.12 Saída por replace da 1014 (`SAIDA_POR_REPLACE`, v1.11)

Com `SAIDA_POR_REPLACE=1`, a saída que não é por stop (SINAL_OPOSTO, TRSP, MANUAL, ADMIN, parada,
zeragem) troca o gatilho da 1014 para além do preço em vez de cancelar a 1014 e mandar a 1031 (HML
19/10: cancel em 3,69 s, replace em 468 ms). Venda: gatilho = max(bid, último) +
`SAIDA_REPLACE_ALEM_PTS` (100), limite = bid − `COLAR_SAIDA_PTS` × k; compra espelhada. Só com a 1014
ativa e aceita, sem pedido pendente, não disparada, cobrindo a posição, sem entrada viva, sem ticker
divergente e com o disjuntor fechado; senão, o caminho de antes. 150=5 sem disparo em
`SAIDA_REPLACE_TIMEOUT_S` (2): degrau seguinte, até `MAX_REPRECO_SAIDA`; esgotado, cancel + 1031.
Disparada sem fill em `ESPERA_STOP_S`: stop furado. Recusado: com a 1014 disparada/terminal ("in
time"), espera os fills (AGUARDANDO_STOP); outro motivo, cancel + 1031 na hora. Contrato com o
Kalman inalterado (estado SAINDO; fill com a origem e o motivo do pedido; sem PROTEGIDA,
PROTECAO_AJUSTADA nem PROTECAO_DISPARADA); eventos novos `SAIDA_POR_REPLACE` e
`SAIDA_REPLACE_FALHOU` (o Kalman ignora). O ajuste do stop pedido durante a saída continua
recusado com `SEM_PROTECAO`. Depois de um restart no meio, o caminho de antes. Teste manual:
`docs/TESTE-SAIDA-REPLACE.md`. Roteiros 87 e 88.

O disjuntor (v1.11): o 35=9 de cancel/replace da 1014 que perde a corrida para o disparo ("in
time", 39=B, 1014 disparada ou terminal) não conta para a regra genérica (roteiro 86).

### 1.13 Quarentena do banco, IgnoreTunnel e reserva de replaces (v1.13)

- **Quarentena:** a linha que o banco recusa por erro de dado vai para a `kalmanordemquarentena`
  (alerta crítico `BANCO_QUARENTENA`); o resto grava. **Com linha pendente, o serviço sobe em PARADA**
  (BLOQUEAR, origem QUARENTENA). Revisar e liberar:
  ```
  sudo /opt/kalman-ordens/kalman-ordens quarentena listar
  sudo /opt/kalman-ordens/kalman-ordens quarentena resolver -id N -por <uid> -motivo "o que foi conferido"
  sudo /opt/kalman-ordens/kalman-ordens retomar -motivo "quarentena revisada"   # POST /retomar
  ```
  O `/retomar` recusa com `QUARENTENA_PENDENTE` enquanto houver linha pendente. Resolver não regrava a
  linha: o estado real vem da conciliação com o PnT.
- **IgnoreTunnel:** `20864=Y` na 1014, nos replaces dela e na 1031 de saída; entradas no padrão
  (`IGNORE_TUNNEL_ENTRADA=1` liga). Mensagem com "tunnel" do PnT: evento `TUNNEL`, métrica
  `kalman_ordens_tunel_total{papel,tipo}`; recusa de entrada: alerta `TUNNEL_RECUSA_ENTRADA`.
- **Reserva de replaces:** a 1014 aceita `REPLACES_1014_MAX` (100) replaces; a partir de MAX −
  `REPLACES_1014_RESERVA` (95) o ajuste do stop é recusado com `PROTECAO_LIMITE_REPLACES` (evento e
  alerta `PROTECAO_RESERVA_REPLACES`) e o trailing segue pelo TRSP do gestor.
- **Migrações no journal:** o `servir` registra cada migração aplicada (`migração aplicada`).
- **Convenção de fuso (conferida em 19/10):** toda coluna `DATETIME(3)` (`em`, `criado_em`,
  `atualizado_em`, `saida_pedida_em`, `alterado_em`, `aplicada_em`, `resolvida_em`) é **UTC** — o
  serviço grava com `utc()`/`.UTC()`, a conexão força `loc=UTC` e nenhum SQL usa `NOW()`. As colunas
  `DATE` (`data` de operação, ordem e pedido) são o **dia do pregão em Brasília** (`relogio.Fuso`). Na
  API, o histórico sai em −03:00 (`emBR`) e o `/eventos` em UTC com `Z`; os dois com o fuso explícito.
  Consulta direta no banco: converter com `CONVERT_TZ(em, '+00:00', '-03:00')`.

### 1.14 Fonte degradada e monitor de disco (v1.14)

- **`FONTE_DEGRADADA`** (alerta crítico; `fonte` no `/saude`: `degradada`, `ruim_desde`, `motivo`,
  `janela`): a fonte de preço ruim por mais de `FONTE_DEGRADADA_S` (30) dentro de `FONTE_JANELA`
  (`09:00-18:25`). Ruim = sem resposta, `{}`, 404/400; em negociação, sem bid/ask, book cruzado ou preço
  mais velho que `PRECO_IDADE_MAX_S`; leilão com preço não conta. Resolve quando a fonte volta (ou a
  janela fecha). Na .53 com o pnt-md-hml, `PRECOS_SAUDE_URL=http://127.0.0.1:9096/saude` põe no texto
  o motivo dado pela fonte (book degenerado, atraso, reconexões seguidas). O topo da tela de admin é do
  lado do Kalman (lê `alertas_criticos` / `fonte` do `/saude`).
- **Monitor de disco** (independe dos serviços; `/` e `/home`, a cada 5 min): `DISCO_80` aviso,
  `DISCO_90` crítico, repetindo no máximo a cada 30 min enquanto durar, `RESOLVIDO` na volta. Telegram
  pelo `/etc/kalman-ordens/disco.env` (o chat da .53; vazio = só no journal):
  ```
  sudo install -o root -g root -m 755 deploy/disco-alerta.sh /opt/kalman-ordens/disco-alerta.sh
  sudo install -o root -g root -m 600 deploy/disco.env.example /etc/kalman-ordens/disco.env   # editar
  sudo install -o root -g root -m 644 deploy/kalman-disco-alerta.service deploy/kalman-disco-alerta.timer /etc/systemd/system/
  sudo systemctl daemon-reload && sudo systemctl enable --now kalman-disco-alerta.timer
  sudo systemctl start kalman-disco-alerta.service && journalctl -u kalman-disco-alerta -n 5   # "disco: / NN% (OK)"
  ```
  Teste: `scripts/teste-disco-alerta.sh`.

### 1.11 Emergência KHML (instância HML do Kalman, módulo `KHML`)

A tela de admin da principal fala com o módulo `KALMAN`; para uma operação da KHML
(`operacao_id` a partir de 900000000), direto no kalman-ordens, na .53. **O `?modulo=KHML` é
obrigatório**: sem ele a ação procura a operação no KALMAN e responde `OPERACAO_INEXISTENTE`.
`motivo` (texto) e `por` (uid do admin, inteiro > 0) são obrigatórios.

```
OP=900000123; POR=<uid do admin>; B=127.0.0.1:9092
# achar a operação (do dia, só leitura)
curl -s "$B/operacoes?dia=$(date +%F)&modulo=KHML&uid=1450"
curl -s "$B/ordens/$OP?modulo=KHML"
# encerrar: zera a posição pelo fluxo de saída (origem ADMIN); a operação fecha com ZERADA
curl -s -X POST "$B/operacoes/$OP/encerrar?modulo=KHML" -H 'Content-Type: application/json' \
     -d "{\"motivo\":\"emergencia KHML\",\"por\":$POR}"
# cancelar a entrada: só sem posição e com a entrada viva
curl -s -X POST "$B/operacoes/$OP/cancelar-entrada?modulo=KHML" -H 'Content-Type: application/json' \
     -d "{\"motivo\":\"emergencia KHML\",\"por\":$POR}"
```

Respostas: `ENCERRANDO`, `CANCELANDO` / `JA_CANCELANDO` (aceitas); `OPERACAO_INEXISTENTE` (id ou
módulo errado), `OPERACAO_ENCERRADA`, `OPERACAO_EM_SAIDA` (a saída já está em curso: acompanhar,
não repetir), `SEM_POSICAO`, `SEM_ENTRADA_PENDENTE`, `COM_POSICAO` (a entrada já executou: usar
encerrar), `SIMBOLO_DIVERGENTE`, `CAMPO_INVALIDO` (sem motivo/por); `503 RESULTADO_DESCONHECIDO` →
consultar `GET /ordens/$OP?modulo=KHML` antes de repetir. A ação vira `ADMIN_ACAO` na linha do tempo
da operação (`/eventos?modulo=KHML`).

Último recurso, **para todos os módulos** (a parada é global, e o evento dela só aparece em
`?modulo=KALMAN` ou sem filtro): `POST /parada` com `{"modo":"BLOQUEAR"|"ZERAR","origem":"admin",
"motivo":"...","por":N}`; volta com `POST /retomar`.

Testado em 19/10 07:57 (sem efeito) com um id inexistente: `encerrar` e `cancelar-entrada` com
`?modulo=KHML` → `OPERACAO_INEXISTENTE`; sem `por` → `CAMPO_INVALIDO`.

---

## 2. Checklist da F1 (SPEC v1 Parte 11.2)

Antes da rodada 1: processo antigo do PT **desconectado** da HML (sessão `GAMMAQ2`); senha da HML
no service.env; horário da sessão; `ORDENS_AMBIENTE=HML` e `ORDENS_CONTAS_HML=99999`; uid de
teste habilitado e `usuario conferir` mostrando `destino=85 conta=99999`; `systemctl start`. Para cada item,
anotar a resposta do PnT (mensagens do log FIX) e o ponto `[PnT-n]` da SPEC que ela fecha.

| # | Rodada | Item | Como | Registrar |
|---|---|---|---|---|
| 2.1 | 1 | Logon e mass status | subir; `/saude` com `sessao_logada=true`; no log, o `35=AF 585=9` e a resposta | se o `585=9` é aceito (F10); se volta `35=j` / `35=3` |
| 2.2 | 1 | Sequência e reconexão no meio do dia [PnT-11] | `systemctl restart kalman-ordens` com a sessão no ar; repetir mais tarde | o logon volta sem `141=Y`? Há `ResendRequest`/`GapFill`? O PnT exige reset? |
| 2.3 | 1 | 1031 limite longe: aceite e cancelamento | entrada por `curl` com limite longe do livro; o serviço cancela no timeout (3 s) | `39=A`→`39=0`; o `35=F` **só com as tags do dicionário** (F4) é aceito? `39=6`→`39=4` |
| 2.4 | 1 | 1031: replace | 1031 longe viva + replace (saída que não executa: reapregoa a cada 2 s) | `35=G` aceito (`39=E`→`39=5`); o 198 muda? **14/10: replace da saída aceito (`39=E` → `39=5`); o replace seguinte rejeitado com `35=9 39=2 [IS-72]` porque a ordem já tinha executado (o fill chegou 100 ms depois do 35=9) — o motor zerou sem saída duplicada (SPEC v1.3 seção 1).** |
| 2.5 | 1 | 1014 com gatilho longe: envio, replace e cancel | protocolo da 1014 sem fill: gatilho longe do livro, replace de preço, cancel | `44` = `20829` aceito; `40=2` devolvido [PnT-6]; ecos 20828/20829; `35=G` e `39=4` da 1014 |
| 2.6 | 1 | Cancel-on-Disconnect [PnT-12] | com uma 1014 viva (gatilho longe), `systemctl stop` e `start` | **14/10: logout limpo, a 1014 sobreviveu.** |
| 2.6b | 1 (repetir) | COD em queda abrupta, com a v1.3 (1.8) | com uma 1014 viva: `sudo systemctl kill -s SIGKILL kalman-ordens` e `sudo systemctl start kalman-ordens` logo em seguida | **14/10 (`6866df3`): a 1014 sobreviveu ao SIGKILL — sem COD para o usuário de HML, fecha [PnT-12] na HML.** Sessão presa no PnT por 18 s (na queda da manhã, ~3,5 min); store reparado na subida (74 logons recalculados); janela de sincronia de 500 ms com 1 ResendRequest dentro. |
| 2.7 | 1 | Mass status: o que volta [PnT-13] | restart com uma ordem viva e uma encerrada no dia | só as ativas? o `150=I` traz o `14` certo (a HML de 2025 trouxe `39=2` com `14=0`)? |
| 2.8 | 1 | Comprimento do ClOrdID [PnT-4] | os enviados: `KM-E-900000001-1` (16), `KM-MS-<ms>-<n>` (~22) | aceitos (o RoE diz máximo 28; ambos abaixo) |
| 2.9 | 1 | Códigos de erro [PnT-15] | qualquer rejeição que aparecer | texto 58 e 103/102 gravados |
| 2.10 | 1 e 2 | 9/10 errados vindos do PnT (F11, F12) | `grep 'Msg Parse Error'` no log de eventos FIX; alertas `MENSAGEM_ILEGIVEL` | esperado nenhum (log real de 2025: 100% corretos, F16) |
| 2.10b | 1 | **HML aceita `20856=Y` em 1031 e 1014?** (F14) | toda NewOrderSingle do serviço leva `20856=Y`; ver a resposta das ordens de 2.3 e 2.5 | aceito (`39=A`/`39=0`) ou rejeitado (`39=8`/`35=3`/`35=j`, texto 58). Rejeitado → tirar a tag e perguntar ao PnT como ativar a reativação automática fora da Sniper/multileg |
| 2.10c | 1 | Estado de erro (39=7) em single-leg | se aparecer algum `39=7`: o texto 58, se veio `150=D/39=0` e em quanto tempo | alertas `PROTECAO_EM_ERRO` / `ENTRADA_EM_ERRO` / `SAIDA_EM_ERRO` (só se não voltou em 2 s) |
| 2.10e | 1 | Conta e símbolo da operação (v1.2) | no log FIX, a 1031 e o cancel de 2.3 | `1=99999` e `116=99999` (cabeçalho) em D, F e G; `142=177.124.234.134`; `55=WINV26`. O cancel do PT não levava conta: o PnT aceita o nosso com 1/116? |
| 2.10d | 1 | Senha fora dos logs (F15; store: v1.3 A1) | depois do logon: `sudo grep -rhoa '554=[^[:cntrl:]]*' /var/lib/kalman-ordens/fix/log \| sort -u`, `journalctl -u kalman-ordens \| grep -c '<senha>'` e `sudo find /var/lib/kalman-ordens/fix/store -printf '%m %u %p\n'` | só `554=***` no log; nada no journal; store (com a senha) só `600 kalman-ordens` |
| 2.11 | 2 | 1014 depois de um fill de entrada | fill com o marketdata em replay | 1014 enviada na hora; `ENTRADA_EXECUTADA` → `PROTEGIDA` |
| 2.12 | 2 | 1014: replace de quantidade | com `LIMITE_POSICAO_MAX=2`/`LIMITE_QTD_ORDEM=2` e entrada de 2 com dois fills | `35=G` da 1014 aceito; um pedido por vez. **14/10: não testável na HML — a entrada de 5 (operação 900000004) executou num único fill (`32=5` a 211330) e a 1014 nasceu com 5; a HML não respeita a profundidade do livro. Coberto só pelo roteiro 02; teste real na F4.** Operação 900000004: venda 5 a 211330, `ZERADA`, +115 pts. |
| 2.13 | 2 | 1014: disparo | preço do replay atravessa o gatilho | fills da 1014 com o mesmo 37 [PnT-5]; disparo com folga. **14/10 (operação 900000003): disparo `150=I 39=B` com `20807=1`/`20806=211525` e `[DSA-122]`; fill 0,5 s depois a 211430 (gatilho 211425, limite 211525) no 37 e no ClOrdID da 1014 — fecha [PnT-5]; `39=B` "Delaying final state" e `39=2` final 3 s depois (SPEC v1.3 J1–J3).** |
| 2.14 | depois da 2 | SwingTrade 2028 exploratória [PnT-9, PnT-10] | ferramenta de bancada (fora do serviço) | fica para depois da rodada 2 |

Depois: ajustar na SPEC cada ponto `[PnT-n]` respondido; se mudar comportamento, roteiro novo antes
do código. Dicionário novo do PnT [PnT-2]: recebido em 13/10/2026, cópia regenerada (SPEC v1.2
seção 7). Rodada 1 (14/10): resultados e correções na [SPEC v1.3](../docs/SPEC-ORDENS-v1.3.md)
seção 1 — **rodada 1 concluída**: fechadas [PnT-11], [PnT-12] (na HML), [PnT-13] e [PnT-17] (a 1014
funciona no DSA). Rodada 2: 2.13 (disparo) concluído em 14/10, fecha [PnT-5]; 2.12 não testável na HML (fica para a
F4). **F1 concluída em 14/10/2026: checklist fechado.** Estado de cada `[PnT-n]`: tabela de
pendências da [SPEC v1](../docs/SPEC-ORDENS-v1.md). Próxima fase: F2 (seção 3), bloqueada.

---

## 3. O que muda na .51 (F2)

**Estado (14/10/2026): bloqueada** pelo usuário FIX de produção e pela liberação do IP da .51 no
PnT [PnT-1]. Na F2 (ordens que não executam, 3 pregões sem alerta inesperado, SPEC v1 Parte 12),
conferir com o usuário de produção o que a HML fechou: identificação (1, 116, 142) [PnT-3] e COD
[PnT-12].

Mesmo procedimento da seção 1, na .51, com:

| Item | .53 (F1) | .51 (F2) |
|---|---|---|
| IP liberado no PnT | nenhum (a .53 já conectava na HML) | **.51 na produção** [PnT-1] |
| Credenciais FIX | `GAMMAQ2` da HML | **usuário novo de produção** (nunca o do PT), conta com dígito |
| `FIX_HOST`/`FIX_PORTA`/`FIX_TARGET_COMP_ID` | HML | produção (`PNT_DSA`) |
| `FIX_INICIO`/`FIX_FIM` | da HML | `08:30:00` / `17:55:00` |
| `PRECOS_URL` | `:9090` (rodada 1) / `:9095` replay (rodada 2) na .53 | **`http://172.16.110.51:9090`** |
| Banco | `kalmanordens` na .53 | `kalmanordens` na .51 (mesmos grants da 1.2, inclusive as 3 colunas de `acusuario`) |
| `ORDENS_AMBIENTE` | `HML` (`usutxtconfig`) | **`PRODUCAO`** (`usutxtconfigprd`: a conta real que o usuário escolheu no GQ) |
| `ORDENS_CONTAS_HML` | `99999` (a conta tem de estar na lista) | `99999` (a conta **não** pode estar na lista) |
| Símbolo | ticker do marketdata da .53 | ticker do marketdata da .51 (contrato vigente) |
| Habilitação (CLI) | uid de teste | F2: uid de teste (ordens que não executam, por `curl`); F3: o usuário real |
| Telegram | chat de teste | chat de produção |
| Build | `~/kalman-ordens` na .53 | o mesmo commit verificado na F1 (`kalman-ordens versao` igual) |

---

## 4. Testes antes de subir (critério da F0)

Nativo na .53 (sem Docker): MariaDB local (10.11.15), schema `kalmanordens_teste`, usuário de
teste só com esse schema. Uma vez, como root do MariaDB:

```sql
CREATE DATABASE IF NOT EXISTS kalmanordens_teste CHARACTER SET utf8mb4;
CREATE USER IF NOT EXISTS 'kalman_ordens_teste'@'127.0.0.1' IDENTIFIED BY '<senha de teste>';
-- os testes criam, alteram e apagam as tabelas (LimparTeste: DROP TABLE, só em *_teste)
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, INDEX ON kalmanordens_teste.* TO 'kalman_ordens_teste'@'127.0.0.1';
```

O DSN fica fora do repositório, em `~/.config/kalman-ordens/teste-mariadb.dsn` (600):
`kalman_ordens_teste:<senha de teste>@tcp(127.0.0.1:3306)/kalmanordens_teste` (ou na variável
`KALMAN_TESTE_MARIADB`, que tem precedência). O script recusa schema que não termine em `_teste`.

```
scripts/teste-completo.sh                    # dois backends (memória e MariaDB local), com -race (gcc)
scripts/teste-completo.sh --sem-race         # o mesmo sem -race
scripts/longo.sh suite scripts/teste-completo.sh   # fora da sessão SSH (systemd --user): log em var/suite-*.log
```

Sem privilégio global, `TestLimparTesteRecusaProducao` (cria outro schema) e
`TestSQLModeFixoNaConexao` (muda o `sql_mode` global) se pulam no modo nativo — de propósito: o
MariaDB da .53 é o do Kalman e do dbecotools. Os dois rodam no modo container.

Com Docker (máquina de desenvolvimento):

```
scripts/teste-completo.sh --container          # dois backends (memória e mariadb:10.11.15)
scripts/teste-completo.sh --container --race   # o mesmo com -race, em golang:1.24-bookworm
```

---

## 5. Retenção e cópia do log FIX (SPEC v1.6 E)

Banco: 12 meses (eventos FIX, publicados, sessão, pedidos); resumo de operações e ordens sem
expurgo. Log FIX em arquivo: 90 dias compactado. Cópia do log para fora da máquina: só na .51
(para a .53), instalação na F2. 5.1 e 5.2 valem nas duas máquinas.

### 5.1 Expurgo do banco (timer mensal)

Usuário de banco só do expurgo (como root do MariaDB; na .51, o mesmo com o schema de lá):

```sql
CREATE USER 'kalman_ordens_expurgo'@'127.0.0.1' IDENTIFIED BY '<senha do expurgo>';
GRANT SELECT, DELETE ON kalmanordens.kalmanordemevento TO 'kalman_ordens_expurgo'@'127.0.0.1';
GRANT SELECT, DELETE ON kalmanordens.kalmanordempub    TO 'kalman_ordens_expurgo'@'127.0.0.1';
GRANT SELECT, DELETE ON kalmanordens.kalmanordemsessao TO 'kalman_ordens_expurgo'@'127.0.0.1';
GRANT SELECT, DELETE ON kalmanordens.kalmanordempedido TO 'kalman_ordens_expurgo'@'127.0.0.1';
GRANT SELECT ON kalmanordens.kalmanordem_migracao      TO 'kalman_ordens_expurgo'@'127.0.0.1';
```

```
sudo install -o root -g root -m 600 ~/kalman-ordens/deploy/expurgo.env.example /etc/kalman-ordens/expurgo.env
sudoedit /etc/kalman-ordens/expurgo.env          # a senha do expurgo; EXPURGO_MESES=12
sudo install -o root -g root -m 644 ~/kalman-ordens/deploy/kalman-ordens-expurgo.service ~/kalman-ordens/deploy/kalman-ordens-expurgo.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo /opt/kalman-ordens/kalman-ordens expurgar -simular   # só conta (lê /etc/kalman-ordens/expurgo.env)
sudo systemctl enable --now kalman-ordens-expurgo.timer
systemctl list-timers kalman-ordens-expurgo.timer
```

O prazo muda em `EXPURGO_MESES` (mínimo 3). `journalctl -u kalman-ordens-expurgo` mostra o que cada
execução apagou por tabela.

### 5.2 Log FIX: logrotate (diário, 90 dias, compactado)

O SELinux (Enforcing na .53) não deixa o logrotate mexer em `var_lib_t`: o diretório do log ganha o
rótulo de log (o serviço continua escrevendo nele).

```
sudo semanage fcontext -a -t var_log_t '/var/lib/kalman-ordens/fix/log(/.*)?'
sudo restorecon -Rv /var/lib/kalman-ordens/fix/log
sudo install -o root -g root -m 644 ~/kalman-ordens/deploy/logrotate-kalman-ordens /etc/logrotate.d/kalman-ordens
sudo logrotate -d /etc/logrotate.d/kalman-ordens    # simula: os dois *.current.log, rotate 90
```

Roda com o `logrotate.timer` do sistema (meia-noite, fora da sessão FIX). Arquivos rotacionados:
`FIX.4.4-<sender>-<target>.messages.current.log-AAAAMMDD.gz` (e o `.event.`), 600 do
`kalman-ordens`. O prazo muda em `rotate`/`maxage`. Depois da primeira noite:
`sudo ls -lZ /var/lib/kalman-ordens/fix/log` e `ausearch -m avc -ts today | grep logrotate` (nada).

### 5.3 Cópia do log FIX para a .53 (só na .51, F2)

Na **.53** (destino), uma conta só para receber, com a chave da .51 restrita ao `rrsync` num
diretório:

```
sudo useradd --system --create-home --home-dir /var/lib/kalmancopia --shell /bin/bash kalmancopia
sudo install -d -o kalmancopia -g kalmancopia -m 700 /var/lib/kalmancopia/fix-log-51 /var/lib/kalmancopia/.ssh
sudo install -o root -g root -m 755 /usr/share/doc/rsync/support/rrsync /usr/local/bin/rrsync
# authorized_keys (uma linha, com a chave pública gerada na .51 abaixo):
#   command="/usr/local/bin/rrsync /var/lib/kalmancopia/fix-log-51",restrict ssh-ed25519 AAAA... kalman-ordens@51
sudoedit /var/lib/kalmancopia/.ssh/authorized_keys
sudo chown kalmancopia: /var/lib/kalmancopia/.ssh/authorized_keys && sudo chmod 600 /var/lib/kalmancopia/.ssh/authorized_keys
```

Na **.51** (origem):

```
sudo ssh-keygen -t ed25519 -N '' -C kalman-ordens@51 -f /etc/kalman-ordens/copia-log_ed25519
sudo chown kalman-ordens:kalman-ordens /etc/kalman-ordens/copia-log_ed25519 && sudo chmod 600 /etc/kalman-ordens/copia-log_ed25519
sudo cat /etc/kalman-ordens/copia-log_ed25519.pub            # vai para a authorized_keys da .53
ssh-keyscan -t ed25519 172.16.110.53 | sudo tee /etc/kalman-ordens/copia-log_known_hosts   # conferir a impressão digital
sudo chmod 644 /etc/kalman-ordens/copia-log_known_hosts
sudo install -o root -g root -m 755 ~/kalman-ordens/deploy/copia-log-fix.sh /opt/kalman-ordens/copia-log-fix.sh
sudo install -o root -g root -m 600 ~/kalman-ordens/deploy/copia-log.env.example /etc/kalman-ordens/copia-log.env
sudo install -o root -g root -m 644 ~/kalman-ordens/deploy/kalman-ordens-copia-log.service ~/kalman-ordens/deploy/kalman-ordens-copia-log.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl start kalman-ordens-copia-log.service && journalctl -u kalman-ordens-copia-log -n 20   # "copia-log-fix: ok"
sudo systemctl enable --now kalman-ordens-copia-log.timer
```

O destino espelha os 90 dias da .51 (`--delete`): protege contra a perda do disco da .51, não
contra apagar na .51.
