# Implantação na .51

## Compilar (na máquina de desenvolvimento)

```
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o kalman-ordens ./cmd/kalman-ordens
```

Binário estático, sem dependência de sistema.

## Instalar

```
sudo useradd --system --home /var/lib/kalman-ordens --shell /usr/sbin/nologin kalman-ordens
sudo install -d -o kalman-ordens -g kalman-ordens -m 750 /var/lib/kalman-ordens /var/lib/kalman-ordens/fix
sudo install -d -m 755 /opt/kalman-ordens
sudo install -m 755 kalman-ordens /opt/kalman-ordens/kalman-ordens
sudo install -d -m 750 /etc/kalman-ordens
sudo install -m 600 -o kalman-ordens deploy/service.env.example /etc/kalman-ordens/service.env   # e preencher
sudo install -m 644 deploy/kalman-ordens.service /etc/systemd/system/kalman-ordens.service
sudo systemctl daemon-reload
```

Banco (uma vez; o serviço cria as tabelas sozinho na subida, `kalmanordem_migracao` guarda a versão):

```sql
CREATE DATABASE kalmanordens CHARACTER SET utf8mb4;
CREATE USER 'kalman_ordens'@'127.0.0.1' IDENTIFIED BY '...';
GRANT SELECT, INSERT, UPDATE, CREATE, ALTER, INDEX ON kalmanordens.* TO 'kalman_ordens'@'127.0.0.1';
```

Sem `DROP`: a limpeza de tabelas existe só para o schema `*_teste` e é recusada em qualquer outro.

## Operar

```
sudo systemctl start kalman-ordens
journalctl -u kalman-ordens -f                       # log com ClOrdID e 37 em toda linha de ordem
/opt/kalman-ordens/kalman-ordens saude
/opt/kalman-ordens/kalman-ordens parar   -motivo "..."   # BLOQUEAR (origem cli)
/opt/kalman-ordens/kalman-ordens zerar   -motivo "..."   # ZERAR: BLOQUEAR + saída da posição do Kalman
/opt/kalman-ordens/kalman-ordens retomar -motivo "..."   # sempre manual
```

O CLI só fala com o HTTP local (`HTTP_ENDERECO`): funciona por SSH com o Kalman fora do ar.

`systemctl stop` faz logout FIX e **não cancela a 1014**: a posição continua protegida no PnT.

O log FIX completo (mensagens) fica em `/var/lib/kalman-ordens/fix/log`; o store de sequência em
`/var/lib/kalman-ordens/fix/store`. Não apagar o store no meio do dia (a sequência continua de onde
parou, [PnT-11]).

## Testes antes da HML (critério da F0)

Na máquina de desenvolvimento ou na .51, com Docker:

```
scripts/teste-completo.sh
```

Sobe `mariadb:10.11.15` (a versão da .51) num container, roda a suíte inteira nos dois backends
(memória e MariaDB) e, se houver cgo, com `-race`.
