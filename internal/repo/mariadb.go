package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/migracoes"
)

// SQLMode é fixado em toda conexão (não depende da configuração global do servidor). É o valor
// da .51 e o padrão do mariadb:10.11.15.
const SQLMode = "STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION"

// MariaDB implementa Repositorio sobre MariaDB 10.11. Horários gravados em UTC.
type MariaDB struct {
	db            *sql.DB
	tabelaUsuario string // dbecotools.acusuario (em teste, acusuario_teste no schema de teste)
}

// TabelaUsuarioPadrao: a configuração dos usuários do GQ. Só SELECT de usucodusuario,
// usutxtconfig e usutxtconfigprd (grant por coluna, deploy/LEIA.md 1.2).
const TabelaUsuarioPadrao = "dbecotools.acusuario"

var reTabela = regexp.MustCompile(`^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+)?$`)

// UsarTabelaUsuario troca a tabela de usuários do GQ (testes: acusuario_teste).
func (r *MariaDB) UsarTabelaUsuario(nome string) error {
	if !reTabela.MatchString(nome) {
		return fmt.Errorf("repo: nome de tabela inválido %q", nome)
	}
	r.tabelaUsuario = nome
	return nil
}

// AbrirMariaDB conecta (DSN no formato do go-sql-driver; parseTime e loc=UTC são forçados).
func AbrirMariaDB(dsn string) (*MariaDB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("repo: DSN: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 10 * time.Second
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["sql_mode"] = "'" + SQLMode + "'" // SET sql_mode a cada conexão nova
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("repo: conectar: %w", err)
	}
	return &MariaDB{db: db, tabelaUsuario: TabelaUsuarioPadrao}, nil
}

func (r *MariaDB) Fechar() error { return r.db.Close() }

// Migrar aplica as migrações pendentes (tabela kalmanordem_migracao).
func (r *MariaDB) Migrar() error {
	_, err := r.MigrarRelatando()
	return err
}

// MigrarRelatando aplica as migrações pendentes e devolve os nomes das que aplicou (nenhuma = o
// schema já estava na versão do binário).
func (r *MariaDB) MigrarRelatando() ([]string, error) {
	if _, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS kalmanordem_migracao (
		versao INT NOT NULL PRIMARY KEY, nome VARCHAR(100) NOT NULL, aplicada_em DATETIME(3) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return nil, err
	}
	pend, err := r.MigracoesPendentes()
	if err != nil {
		return nil, err
	}
	var aplicadas []string
	for _, m := range pend {
		for _, inst := range m.Instrucoes {
			if _, err := r.db.Exec(inst); err != nil {
				return aplicadas, fmt.Errorf("migração %s: %w", m.Nome, err)
			}
		}
		if _, err := r.db.Exec("INSERT INTO kalmanordem_migracao (versao, nome, aplicada_em) VALUES (?,?,?)", m.Versao, m.Nome, time.Now().UTC()); err != nil {
			return aplicadas, err
		}
		aplicadas = append(aplicadas, m.Nome)
	}
	return aplicadas, nil
}

// MigracoesPendentes: as migrações embutidas no binário que o banco ainda não tem. Só lê (sem
// kalmanordem_migracao, todas estão pendentes).
func (r *MariaDB) MigracoesPendentes() ([]migracoes.Migracao, error) {
	ms, err := migracoes.Todas()
	if err != nil {
		return nil, err
	}
	var existe int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'kalmanordem_migracao'`).Scan(&existe); err != nil {
		return nil, err
	}
	if existe == 0 {
		return ms, nil
	}
	var pend []migracoes.Migracao
	for _, m := range ms {
		var n int
		if err := r.db.QueryRow("SELECT COUNT(*) FROM kalmanordem_migracao WHERE versao=?", m.Versao).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			pend = append(pend, m)
		}
	}
	return pend, nil
}

// LimparTeste apaga as tabelas do serviço — só num schema cujo nome termina em _teste.
func (r *MariaDB) LimparTeste() error {
	var schema string
	if err := r.db.QueryRow("SELECT DATABASE()").Scan(&schema); err != nil {
		return err
	}
	if !strings.HasSuffix(schema, "_teste") {
		return fmt.Errorf("repo: LimparTeste recusado no schema %q (só *_teste)", schema)
	}
	for _, t := range []string{"kalmanordemevento", "kalmanordempub", "kalmanordemestado", "kalmanordem", "kalmanordemoperacao",
		"kalmanordemusuario", "kalmanordemusuariohist", "kalmanordemsessao", "acusuario_teste", "kalmanordem_migracao"} {
		if _, err := r.db.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return err
		}
	}
	return nil
}

func utc(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func nuloInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nulo(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *MariaDB) SalvarOperacao(o *dominio.Operacao) error {
	cn, _ := json.Marshal(o.ContadorN)
	agora := time.Now().UTC()
	pnl := o.PnL.String()
	if o.ID == 0 {
		o.Modulo = modulo(o.Modulo) // o padrão da coluna
		res, err := r.db.Exec(`INSERT INTO kalmanordemoperacao (data, modulo, operacao_id, uid, conta, destino, simbolo, lado, qtd_pedida,
			stop_pts, stop_ajustado, estado, motivo_entrada, ts_decisao, motivo_saida, origem_saida, saida_pedida_em, divergente, pnl_pts,
			encerrou_entrada, contador_n, pedido_entrada, resposta_entrada, pedido_saida, resposta_saida, criado_em, atualizado_em)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			o.Data, modulo(o.Modulo), o.OperacaoID, o.UID, nulo(o.Conta), o.Destino, nulo(o.Simbolo), o.Lado.Letra(), o.QtdPedida, o.StopPts, nuloInt(o.StopAjustado), string(o.Estado),
			nulo(o.MotivoEntrada), nulo(o.TsDecisao), nulo(o.MotivoSaida), nulo(o.OrigemSaida), utc(o.SaidaPedidaEm),
			o.Divergente, pnl, o.EncerrouEntrada, string(cn), nulo(o.PedidoEntrada), nulo(o.RespostaEntrada),
			nulo(o.PedidoSaida), nulo(o.RespostaSaida), utc(o.CriadaEm), agora)
		if err != nil {
			var me *mysql.MySQLError
			if errors.As(err, &me) && me.Number == 1062 {
				return ErrDuplicada
			}
			return err
		}
		o.ID, err = res.LastInsertId()
		return err
	}
	_, err := r.db.Exec(`UPDATE kalmanordemoperacao SET estado=?, stop_ajustado=?, motivo_saida=?, origem_saida=?, saida_pedida_em=?,
		divergente=?, pnl_pts=?, encerrou_entrada=?, contador_n=?, pedido_entrada=?, resposta_entrada=?, pedido_saida=?,
		resposta_saida=?, atualizado_em=? WHERE id=?`,
		string(o.Estado), nuloInt(o.StopAjustado), nulo(o.MotivoSaida), nulo(o.OrigemSaida), utc(o.SaidaPedidaEm), o.Divergente, pnl,
		o.EncerrouEntrada, string(cn), nulo(o.PedidoEntrada), nulo(o.RespostaEntrada), nulo(o.PedidoSaida),
		nulo(o.RespostaSaida), agora, o.ID)
	return err
}

func (r *MariaDB) OperacaoExiste(mod string, id int64) (bool, error) {
	var n int
	err := r.db.QueryRow("SELECT COUNT(*) FROM kalmanordemoperacao WHERE modulo=? AND operacao_id=?", modulo(mod), id).Scan(&n)
	return n > 0, err
}

func (r *MariaDB) SalvarOrdem(o *dominio.Ordem) error {
	agora := time.Now().UTC()
	if o.ID == 0 {
		o.Modulo = modulo(o.Modulo)
		res, err := r.db.Exec(`INSERT INTO kalmanordem (data, modulo, uid, operacao_id, papel, conta, simbolo, clordid_base, clordid_atual,
			orderid37, secid198, estrategia, lado, qtd, preco, preco_stop, preco_stop_lim, estado, qtd_exec, preco_medio,
			motivo_gestor, motivo_rejeicao, criado_em, atualizado_em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			o.Data, modulo(o.Modulo), o.UID, o.OperacaoID, o.Papel.String(), nulo(o.Conta), nulo(o.Simbolo), o.ClOrdBase, o.ClOrdAtual, nulo(o.OrderID), nulo(o.SecID),
			o.Estrategia, o.Lado.Letra(), o.Qtd, o.Preco, o.Stop, o.StopLim, string(o.Estado), o.QtdExec, o.PrecoMedio.String(),
			nulo(o.MotivoGestor), nulo(cortar(o.MotivoRejeicao, 500)), utc(o.CriadaEm), agora)
		if err != nil {
			return err
		}
		o.ID, err = res.LastInsertId()
		return err
	}
	_, err := r.db.Exec(`UPDATE kalmanordem SET clordid_atual=?, orderid37=?, secid198=?, qtd=?, preco=?, preco_stop=?,
		preco_stop_lim=?, estado=?, qtd_exec=?, preco_medio=?, motivo_rejeicao=?, atualizado_em=? WHERE id=?`,
		o.ClOrdAtual, nulo(o.OrderID), nulo(o.SecID), o.Qtd, o.Preco, o.Stop, o.StopLim, string(o.Estado), o.QtdExec,
		o.PrecoMedio.String(), nulo(cortar(o.MotivoRejeicao, 500)), agora, o.ID)
	return err
}

func (r *MariaDB) RegistrarEvento(e EventoFIX) (bool, error) {
	var lq, cq any
	if e.LastQty != nil {
		lq = *e.LastQty
	}
	if e.CumQty != nil {
		cq = *e.CumQty
	}
	var lp, ap any
	if e.LastPx != nil {
		lp = e.LastPx.String()
	}
	if e.AvgPx != nil {
		ap = e.AvgPx.String()
	}
	var ordem any
	if e.OrdemID != 0 {
		ordem = e.OrdemID
	}
	_, err := r.db.Exec(`INSERT INTO kalmanordemevento (ordem_id, modulo, direcao, msgtype, exectype, ordstatus, clordid, orderid37,
		execid, lastqty, lastpx, cumqty, avgpx, texto, codigo, fix_raw, em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ordem, modulo(e.Modulo), e.Direcao, e.MsgType, nulo(e.ExecType), nulo(e.OrdStatus), nulo(cortar(e.ClOrdID, 60)), nulo(e.OrderID),
		nulo(e.ExecID), lq, lp, cq, ap, nulo(cortar(e.Texto, 1000)), nulo(cortar(e.Codigo, 20)), nulo(e.Bruta), utc(e.Em))
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && me.Number == 1062 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *MariaDB) EnviosDesde(t time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM kalmanordemevento WHERE direcao='ENV' AND msgtype IN ('D','F','G') AND em >= ?`, t.UTC()).Scan(&n)
	return n, err
}

func (r *MariaDB) Publicar(tipo, mod string, op int64, dados []byte, em time.Time) (int64, error) {
	var opv any
	if op != 0 {
		opv = op
	}
	res, err := r.db.Exec("INSERT INTO kalmanordempub (tipo, modulo, operacao_id, dados, em) VALUES (?,?,?,?,?)", tipo, modulo(mod), opv, string(dados), utc(em))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *MariaDB) Publicados(desde int64, limite int, mod string) ([]Publicado, error) {
	rows, err := r.db.Query("SELECT seq, tipo, modulo, operacao_id, dados, em FROM kalmanordempub WHERE seq > ? AND (? = '' OR modulo = ?) ORDER BY seq LIMIT ?",
		desde, mod, mod, limite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []Publicado
	for rows.Next() {
		var p Publicado
		var op sql.NullInt64
		var d string
		if err := rows.Scan(&p.Seq, &p.Tipo, &p.Modulo, &op, &d, &p.Em); err != nil {
			return nil, err
		}
		p.OperacaoID, p.Dados = op.Int64, json.RawMessage(d)
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

func (r *MariaDB) LerEstado(k string) (string, bool, error) {
	var v string
	err := r.db.QueryRow("SELECT valor FROM kalmanordemestado WHERE chave=?", k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (r *MariaDB) GravarEstado(k, v string, em time.Time) error {
	_, err := r.db.Exec(`INSERT INTO kalmanordemestado (chave, valor, atualizado_em) VALUES (?,?,?)
		ON DUPLICATE KEY UPDATE valor=VALUES(valor), atualizado_em=VALUES(atualizado_em)`, k, v, em.UTC())
	return err
}

func (r *MariaDB) CarregarDia(data string) ([]dominio.Operacao, []dominio.Ordem, error) {
	rows, err := r.db.Query(`SELECT id, data, modulo, operacao_id, uid, conta, destino, simbolo, lado, qtd_pedida, stop_pts, stop_ajustado, estado, motivo_entrada, ts_decisao,
		motivo_saida, origem_saida, saida_pedida_em, divergente, pnl_pts, encerrou_entrada, contador_n, pedido_entrada,
		resposta_entrada, pedido_saida, resposta_saida, criado_em FROM kalmanordemoperacao WHERE data=? ORDER BY id`, data)
	if err != nil {
		return nil, nil, err
	}
	var ops []dominio.Operacao
	for rows.Next() {
		var o dominio.Operacao
		var d time.Time
		var lado, estado, cn string
		var me, ts, ms, os, pe, re, ps, rs, pnl, conta, simb sql.NullString
		var dest sql.NullInt64
		var sp sql.NullTime
		var sa sql.NullInt64
		if err := rows.Scan(&o.ID, &d, &o.Modulo, &o.OperacaoID, &o.UID, &conta, &dest, &simb, &lado, &o.QtdPedida, &o.StopPts, &sa, &estado, &me, &ts, &ms, &os,
			&sp, &o.Divergente, &pnl, &o.EncerrouEntrada, &cn, &pe, &re, &ps, &rs, &o.CriadaEm); err != nil {
			rows.Close()
			return nil, nil, err
		}
		o.Data = d.Format("2006-01-02")
		o.Conta, o.Destino, o.Simbolo = conta.String, int(dest.Int64), simb.String
		o.StopAjustado = sa.Int64
		o.Lado, _ = dominio.LadoDaLetra(lado)
		o.Estado = dominio.EstadoOperacao(estado)
		o.MotivoEntrada, o.TsDecisao, o.MotivoSaida, o.OrigemSaida = me.String, ts.String, ms.String, os.String
		o.PedidoEntrada, o.RespostaEntrada, o.PedidoSaida, o.RespostaSaida = pe.String, re.String, ps.String, rs.String
		if sp.Valid {
			o.SaidaPedidaEm = sp.Time
		}
		if pnl.Valid {
			o.PnL, _ = decimal.NewFromString(pnl.String)
		}
		o.ContadorN = map[string]int{}
		_ = json.Unmarshal([]byte(cn), &o.ContadorN)
		ops = append(ops, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows, err = r.db.Query(`SELECT id, data, modulo, uid, operacao_id, papel, conta, simbolo, clordid_base, clordid_atual, orderid37, secid198,
		estrategia, lado, qtd, preco, preco_stop, preco_stop_lim, estado, qtd_exec, preco_medio, motivo_gestor,
		motivo_rejeicao, criado_em FROM kalmanordem WHERE data=? ORDER BY id`, data)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ords []dominio.Ordem
	for rows.Next() {
		var o dominio.Ordem
		var d time.Time
		var papel, lado, estado, pm string
		var oid, sid, mg, mr, conta, simb sql.NullString
		if err := rows.Scan(&o.ID, &d, &o.Modulo, &o.UID, &o.OperacaoID, &papel, &conta, &simb, &o.ClOrdBase, &o.ClOrdAtual, &oid, &sid, &o.Estrategia,
			&lado, &o.Qtd, &o.Preco, &o.Stop, &o.StopLim, &estado, &o.QtdExec, &pm, &mg, &mr, &o.CriadaEm); err != nil {
			return nil, nil, err
		}
		o.Data = d.Format("2006-01-02")
		o.Papel = dominio.Papel(papel[0])
		o.Conta, o.Simbolo = conta.String, simb.String
		o.Lado, _ = dominio.LadoDaLetra(lado)
		o.Estado = dominio.EstadoOrdem(estado)
		o.OrderID, o.SecID, o.MotivoGestor, o.MotivoRejeicao = oid.String, sid.String, mg.String, mr.String
		o.PrecoMedio, _ = decimal.NewFromString(pm)
		ords = append(ords, o)
	}
	return ops, ords, rows.Err()
}

// ---------------------------------------------------------------- habilitação e usuários do GQ

func (r *MariaDB) Habilitacao(uid int) (dominio.Habilitacao, bool, error) {
	h := dominio.Habilitacao{UID: uid}
	err := r.db.QueryRow(`SELECT habilitado, posicao_max, qtd_max_ordem, alterado_por, motivo, alterado_em
		FROM kalmanordemusuario WHERE uid=?`, uid).Scan(&h.Habilitado, &h.PosicaoMax, &h.QtdMaxOrdem, &h.Por, &h.Motivo, &h.Em)
	if errors.Is(err, sql.ErrNoRows) {
		return h, false, nil
	}
	return h, err == nil, err
}

func (r *MariaDB) Habilitacoes() ([]dominio.Habilitacao, error) {
	rows, err := r.db.Query(`SELECT uid, habilitado, posicao_max, qtd_max_ordem, alterado_por, motivo, alterado_em
		FROM kalmanordemusuario ORDER BY uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hs []dominio.Habilitacao
	for rows.Next() {
		var h dominio.Habilitacao
		if err := rows.Scan(&h.UID, &h.Habilitado, &h.PosicaoMax, &h.QtdMaxOrdem, &h.Por, &h.Motivo, &h.Em); err != nil {
			return nil, err
		}
		hs = append(hs, h)
	}
	return hs, rows.Err()
}

func (r *MariaDB) GravarHabilitacao(h dominio.Habilitacao) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO kalmanordemusuario (uid, habilitado, posicao_max, qtd_max_ordem, alterado_por, motivo, alterado_em)
		VALUES (?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE habilitado=VALUES(habilitado), posicao_max=VALUES(posicao_max),
		qtd_max_ordem=VALUES(qtd_max_ordem), alterado_por=VALUES(alterado_por), motivo=VALUES(motivo), alterado_em=VALUES(alterado_em)`,
		h.UID, h.Habilitado, h.PosicaoMax, h.QtdMaxOrdem, cortar(h.Por, 64), cortar(h.Motivo, 200), h.Em.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO kalmanordemusuariohist (uid, habilitado, posicao_max, qtd_max_ordem, alterado_por, motivo, alterado_em)
		VALUES (?,?,?,?,?,?,?)`, h.UID, h.Habilitado, h.PosicaoMax, h.QtdMaxOrdem, cortar(h.Por, 64), cortar(h.Motivo, 200), h.Em.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// ConfigUsuario: usucodusuario é char(15) com o uid do Kalman (o mesmo do GQ).
func (r *MariaDB) ConfigUsuario(uid int) (string, string, bool, error) {
	var hml, prd sql.NullString
	err := r.db.QueryRow("SELECT usutxtconfig, usutxtconfigprd FROM "+r.tabelaUsuario+" WHERE usucodusuario = ?",
		strconv.Itoa(uid)).Scan(&hml, &prd)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return hml.String, prd.String, true, nil
}

// PrepararUsuariosTeste cria acusuario_teste (mesmas colunas lidas do dbecotools.acusuario) no
// schema de teste e passa a ler dela. Recusado fora de *_teste.
func (r *MariaDB) PrepararUsuariosTeste() error {
	if err := r.soTeste(); err != nil {
		return err
	}
	if _, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS acusuario_teste (
		usucodusuario CHAR(15) NOT NULL PRIMARY KEY, usutxtconfig TEXT NULL, usutxtconfigprd TEXT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return err
	}
	return r.UsarTabelaUsuario("acusuario_teste")
}

// DefinirUsuario (testes): campo hml, prd ou ausente, na acusuario_teste.
func (r *MariaDB) DefinirUsuario(uid int, campo, valor string) error {
	if err := r.soTeste(); err != nil {
		return err
	}
	cod := strconv.Itoa(uid)
	var err error
	switch campo {
	case "ausente":
		_, err = r.db.Exec("DELETE FROM acusuario_teste WHERE usucodusuario=?", cod)
	case "hml":
		_, err = r.db.Exec(`INSERT INTO acusuario_teste (usucodusuario, usutxtconfig) VALUES (?,?)
			ON DUPLICATE KEY UPDATE usutxtconfig=VALUES(usutxtconfig)`, cod, valor)
	case "prd":
		_, err = r.db.Exec(`INSERT INTO acusuario_teste (usucodusuario, usutxtconfigprd) VALUES (?,?)
			ON DUPLICATE KEY UPDATE usutxtconfigprd=VALUES(usutxtconfigprd)`, cod, valor)
	default:
		err = errors.New("repo: campo do usuário deve ser hml, prd ou ausente")
	}
	return err
}

func (r *MariaDB) soTeste() error {
	var schema string
	if err := r.db.QueryRow("SELECT DATABASE()").Scan(&schema); err != nil {
		return err
	}
	if !strings.HasSuffix(schema, "_teste") {
		return fmt.Errorf("repo: só no schema de teste (*_teste), não em %q", schema)
	}
	return nil
}

func cortar(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// modulo: vazio = o padrão (KALMAN).
func modulo(m string) string {
	if m == "" {
		return dominio.ModuloPadrao
	}
	return m
}
