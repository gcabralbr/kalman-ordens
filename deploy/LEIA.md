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

Pré-requisito: acesso de leitura ao `git@github.com:gcabralbr/kalman-ordens.git` a partir da .53
(chave de deploy, só leitura). Go 1.26 já instalado (mínimo 1.24).

```
cd ~ && git clone git@github.com:gcabralbr/kalman-ordens.git && cd ~/kalman-ordens
scripts/build.sh                 # dist/kalman-ordens + sha256
dist/kalman-ordens versao        # tem de ser o commit, sem -sujo
```

Atualizar depois: `cd ~/kalman-ordens && git pull && scripts/build.sh` e reinstalar o binário (1.4).

### 1.2 Banco (schema próprio, local na .53)

```sql
CREATE DATABASE kalmanordens CHARACTER SET utf8mb4;
CREATE USER 'kalman_ordens'@'127.0.0.1' IDENTIFIED BY '<senha da .53>';
GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, INDEX ON kalmanordens.* TO 'kalman_ordens'@'127.0.0.1';
-- conciliação diária (Parte 10): só leitura, só esta tabela; nada mais no dbecotools
GRANT SELECT ON dbecotools.kalmanoperacao TO 'kalman_ordens'@'127.0.0.1';
```

Sem `DROP` e sem `DELETE`. As tabelas o serviço cria sozinho na primeira subida
(`kalmanordem_migracao` guarda a versão). O `sql_mode` é fixado pelo serviço em cada conexão
(`STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION`, o
valor da .51); não depende da configuração global.

### 1.3 Usuário de sistema e diretórios

```
sudo useradd --system --home /var/lib/kalman-ordens --shell /usr/sbin/nologin kalman-ordens
sudo install -d -o kalman-ordens -g kalman-ordens -m 750 /var/lib/kalman-ordens /var/lib/kalman-ordens/fix
sudo install -d -o root -g root -m 755 /opt/kalman-ordens
sudo install -d -o root -g kalman-ordens -m 750 /etc/kalman-ordens
```

Permissões: binário `root:root 755`; `/etc/kalman-ordens/service.env` `root:kalman-ordens 640`
(o serviço lê, não escreve); `/var/lib/kalman-ordens` do usuário do serviço (store e log FIX).

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
| `FIX_HOST`, `FIX_PORTA` | endereço da **HML** do PnT [PnT-1] |
| `FIX_SENDER_COMP_ID`, `FIX_USUARIO`, `FIX_SENHA` | usuário de **homologação** (não o do PT, não o de produção) |
| `FIX_TARGET_COMP_ID` | o da HML — confirmar: o mass status de HML do RoE 2025 vem com `49=PNT_GATEWAY_HML`, não `PNT_DSA` |
| `FIX_INICIO`, `FIX_FIM` | horário da sessão da HML (confirmar com o PnT) |
| `ORDENS_CONTA` | conta de HML, com dígito |
| `ORDENS_SIMBOLO`, `ORDENS_VENCIMENTO` | o símbolo que a HML aceita — **tem de ser igual ao ticker do marketdata da .53**, senão `SIMBOLO_DIVERGENTE` bloqueia as entradas (ver 1.6) |
| `PRECOS_URL` | `http://172.16.110.53:9090` |
| `ORDENS_UID_REAL` | um uid **de teste**, usado só nos `curl` da F1 (o Kalman não chama o serviço) |
| `TELEGRAM_TOKEN`, `TELEGRAM_CHAT` | chat de **teste**, não o de produção |
| `HTTP_ENDERECO` | `127.0.0.1:9092` (o Kalman da .53 está no 9091, o marketdata no 9090) |

`operacao_id` dos testes: usar a faixa `900000000+`, que nunca colide com a `kalmanoperacao`.

### 1.5 Comandos da F1

```
sudo systemctl start kalman-ordens
journalctl -u kalman-ordens -f
/opt/kalman-ordens/kalman-ordens saude

# entrada de teste (o uid é o ORDENS_UID_REAL de teste)
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

- A HML roda com **dados de ontem**; o marketdata da .53 é o de hoje. O preço da entrada (ask + 15)
  vem do marketdata da .53 e pode estar longe do livro da HML: bom para testar cancelamento (não
  executa), ruim para testar fill. Para fill, combinar com o PnT um preço/instrumento que execute.
- Se o símbolo da HML não for o ticker do marketdata da .53, o serviço bloqueia entradas
  (`SIMBOLO_DIVERGENTE`) por desenho. Resolver antes da terça: símbolo que a HML aceita no dia.
- Parar o serviço não cancela nada no PnT. Se o PnT tiver Cancel-on-Disconnect ligado por padrão
  [PnT-12], a 1014 morre a cada restart — é um dos testes (2.8).

---

## 2. Checklist da terça (SPEC v1 Parte 11.2)

Antes: IP da .53 liberado na HML; credenciais de HML; `FIX_TARGET_COMP_ID`, horário da sessão e
símbolo do dia confirmados; `systemctl start`. Para cada item, anotar a resposta do PnT (mensagens
do log FIX) e o ponto `[PnT-n]` da SPEC que ela fecha.

| # | Item | Como | Registrar |
|---|---|---|---|
| 2.1 | Logon e mass status | subir; `/saude` com `sessao_logada=true`; no log, o `35=AF 585=9` e a resposta | se o `585=9` é aceito (F10); se volta `35=j` / `35=3` |
| 2.2 | Sequência e reconexão no meio do dia [PnT-11] | `systemctl restart kalman-ordens` com a sessão no ar; repetir mais tarde | o logon volta sem `141=Y`? Há `ResendRequest`/`GapFill`? O PnT exige reset? |
| 2.3 | 1031 limite: aceite e cancelamento | entrada por `curl` com preço que não executa; o serviço cancela no timeout (3 s) | `39=A`→`39=0`; o `35=F` **só com as tags do dicionário** (F4) é aceito? `39=6`→`39=4` |
| 2.4 | 1031: replace | saída de uma posição com preço que não executa: o serviço reapregoa a cada 2 s | `35=G` aceito (`39=E`→`39=5`); o 198 muda? |
| 2.5 | 1014 só com stop: envio | depois de um fill de entrada | `44` = `20829` aceito; `40=2` devolvido [PnT-6]; ecos 20828/20829 |
| 2.6 | 1014: replace de quantidade e preço | com `LIMITE_POSICAO_MAX=2`/`LIMITE_QTD_ORDEM=2` e entrada de 2 com dois fills | `35=G` da 1014 aceito; um pedido por vez |
| 2.7 | 1014: cancelamento e disparo | saída do gestor (cancel); disparo se a HML mexer o preço | `39=4`; fills da 1014 com o mesmo 37 [PnT-5]; disparo com folga |
| 2.8 | Cancel-on-Disconnect [PnT-12] | com uma 1014 viva, `systemctl stop` e `start` | a 1014 continua viva no mass status? (se morrer: pedir exceção) |
| 2.9 | Mass status: o que volta [PnT-13] | restart com uma ordem viva e uma encerrada no dia | só as ativas? o `150=I` traz o `14` certo (a HML de 2025 trouxe `39=2` com `14=0`)? |
| 2.10 | Comprimento do ClOrdID [PnT-4] | os enviados: `KM-E-900000001-1` (16), `KM-MS-<ms>-<n>` (~22) | aceitos (o RoE diz máximo 28; ambos abaixo) |
| 2.11 | Códigos de erro [PnT-15] | qualquer rejeição que aparecer | texto 58 e 103/102 gravados |
| 2.12 | SwingTrade 2028 exploratória [PnT-9, PnT-10] | **ferramenta de bancada ainda não existe** (fora do serviço, por desenho) | pendente: decidir se faz antes da terça |

Depois: ajustar na SPEC cada ponto `[PnT-n]` respondido; se mudar comportamento, roteiro novo antes
do código. Dicionário novo do PnT [PnT-2], se vier: substituir a cópia e rodar a suíte.

---

## 3. O que muda na .51 (F2)

Mesmo procedimento da seção 1, na .51, com:

| Item | .53 (F1) | .51 (F2) |
|---|---|---|
| IP liberado no PnT | .53 na HML | **.51 na produção** [PnT-1] |
| Credenciais FIX | homologação | **usuário novo de produção** (nunca o do PT), conta com dígito |
| `FIX_HOST`/`FIX_PORTA`/`FIX_TARGET_COMP_ID` | HML | produção (`PNT_DSA`) |
| `FIX_INICIO`/`FIX_FIM` | da HML | `08:30:00` / `17:55:00` |
| `PRECOS_URL` | `http://172.16.110.53:9090` | **`http://172.16.110.51:9090`** |
| Banco | `kalmanordens` na .53 | `kalmanordens` na .51 (mesmos grants da 1.2) |
| `ORDENS_SIMBOLO` | o da HML | vencimento vigente (WIN) |
| `ORDENS_UID_REAL` | uid de teste | F2: uid de teste (ordens que não executam, por `curl`); F3: o usuário real |
| Telegram | chat de teste | chat de produção |
| Build | `~/kalman-ordens` na .53 | o mesmo commit verificado na F1 (`kalman-ordens versao` igual) |

---

## 4. Testes antes de subir (critério da F0)

Na máquina de desenvolvimento, com Docker:

```
scripts/teste-completo.sh          # dois backends (memória e mariadb:10.11.15)
scripts/teste-completo.sh --race   # o mesmo com -race, em golang:1.24-bookworm
```
