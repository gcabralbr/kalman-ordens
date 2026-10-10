package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/migracoes"
)

// MariaDB implementa Repositorio sobre MariaDB 10.11. Horários gravados em UTC.
type MariaDB struct {
	db *sql.DB
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
	return &MariaDB{db: db}, nil
}

func (r *MariaDB) Fechar() error { return r.db.Close() }

// Migrar aplica as migrações pendentes (tabela kalmanordem_migracao).
func (r *MariaDB) Migrar() error {
	if _, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS kalmanordem_migracao (
		versao INT NOT NULL PRIMARY KEY, nome VARCHAR(100) NOT NULL, aplicada_em DATETIME(3) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return err
	}
	ms, err := migracoes.Todas()
	if err != nil {
		return err
	}
	for _, m := range ms {
		var n int
		if err := r.db.QueryRow("SELECT COUNT(*) FROM kalmanordem_migracao WHERE versao=?", m.Versao).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		for _, inst := range m.Instrucoes {
			if _, err := r.db.Exec(inst); err != nil {
				return fmt.Errorf("migração %s: %w", m.Nome, err)
			}
		}
		if _, err := r.db.Exec("INSERT INTO kalmanordem_migracao (versao, nome, aplicada_em) VALUES (?,?,?)", m.Versao, m.Nome, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
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
	for _, t := range []string{"kalmanordemevento", "kalmanordempub", "kalmanordemestado", "kalmanordem", "kalmanordemoperacao", "kalmanordem_migracao"} {
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
		res, err := r.db.Exec(`INSERT INTO kalmanordemoperacao (data, operacao_id, uid, lado, qtd_pedida, stop_pts, estado,
			motivo_entrada, ts_decisao, motivo_saida, origem_saida, saida_pedida_em, divergente, pnl_pts, encerrou_entrada,
			contador_n, pedido_entrada, resposta_entrada, pedido_saida, resposta_saida, criado_em, atualizado_em)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			o.Data, o.OperacaoID, o.UID, o.Lado.Letra(), o.QtdPedida, o.StopPts, string(o.Estado),
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
	_, err := r.db.Exec(`UPDATE kalmanordemoperacao SET estado=?, motivo_saida=?, origem_saida=?, saida_pedida_em=?,
		divergente=?, pnl_pts=?, encerrou_entrada=?, contador_n=?, pedido_entrada=?, resposta_entrada=?, pedido_saida=?,
		resposta_saida=?, atualizado_em=? WHERE id=?`,
		string(o.Estado), nulo(o.MotivoSaida), nulo(o.OrigemSaida), utc(o.SaidaPedidaEm), o.Divergente, pnl,
		o.EncerrouEntrada, string(cn), nulo(o.PedidoEntrada), nulo(o.RespostaEntrada), nulo(o.PedidoSaida),
		nulo(o.RespostaSaida), agora, o.ID)
	return err
}

func (r *MariaDB) OperacaoExiste(id int64) (bool, error) {
	var n int
	err := r.db.QueryRow("SELECT COUNT(*) FROM kalmanordemoperacao WHERE operacao_id=?", id).Scan(&n)
	return n > 0, err
}

func (r *MariaDB) SalvarOrdem(o *dominio.Ordem) error {
	agora := time.Now().UTC()
	if o.ID == 0 {
		res, err := r.db.Exec(`INSERT INTO kalmanordem (data, uid, operacao_id, papel, clordid_base, clordid_atual, orderid37,
			secid198, estrategia, lado, qtd, preco, preco_stop, preco_stop_lim, estado, qtd_exec, preco_medio, motivo_gestor,
			motivo_rejeicao, criado_em, atualizado_em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			o.Data, o.UID, o.OperacaoID, o.Papel.String(), o.ClOrdBase, o.ClOrdAtual, nulo(o.OrderID), nulo(o.SecID),
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
	_, err := r.db.Exec(`INSERT INTO kalmanordemevento (ordem_id, direcao, msgtype, exectype, ordstatus, clordid, orderid37,
		execid, lastqty, lastpx, cumqty, avgpx, texto, codigo, fix_raw, em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ordem, e.Direcao, e.MsgType, nulo(e.ExecType), nulo(e.OrdStatus), nulo(cortar(e.ClOrdID, 60)), nulo(e.OrderID),
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

func (r *MariaDB) Publicar(tipo string, op int64, dados []byte, em time.Time) (int64, error) {
	var opv any
	if op != 0 {
		opv = op
	}
	res, err := r.db.Exec("INSERT INTO kalmanordempub (tipo, operacao_id, dados, em) VALUES (?,?,?,?)", tipo, opv, string(dados), utc(em))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *MariaDB) Publicados(desde int64, limite int) ([]Publicado, error) {
	rows, err := r.db.Query("SELECT seq, tipo, operacao_id, dados, em FROM kalmanordempub WHERE seq > ? ORDER BY seq LIMIT ?", desde, limite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []Publicado
	for rows.Next() {
		var p Publicado
		var op sql.NullInt64
		var d string
		if err := rows.Scan(&p.Seq, &p.Tipo, &op, &d, &p.Em); err != nil {
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
	rows, err := r.db.Query(`SELECT id, data, operacao_id, uid, lado, qtd_pedida, stop_pts, estado, motivo_entrada, ts_decisao,
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
		var me, ts, ms, os, pe, re, ps, rs, pnl sql.NullString
		var sp sql.NullTime
		if err := rows.Scan(&o.ID, &d, &o.OperacaoID, &o.UID, &lado, &o.QtdPedida, &o.StopPts, &estado, &me, &ts, &ms, &os,
			&sp, &o.Divergente, &pnl, &o.EncerrouEntrada, &cn, &pe, &re, &ps, &rs, &o.CriadaEm); err != nil {
			rows.Close()
			return nil, nil, err
		}
		o.Data = d.Format("2006-01-02")
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
	rows, err = r.db.Query(`SELECT id, data, uid, operacao_id, papel, clordid_base, clordid_atual, orderid37, secid198,
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
		var oid, sid, mg, mr sql.NullString
		if err := rows.Scan(&o.ID, &d, &o.UID, &o.OperacaoID, &papel, &o.ClOrdBase, &o.ClOrdAtual, &oid, &sid, &o.Estrategia,
			&lado, &o.Qtd, &o.Preco, &o.Stop, &o.StopLim, &estado, &o.QtdExec, &pm, &mg, &mr, &o.CriadaEm); err != nil {
			return nil, nil, err
		}
		o.Data = d.Format("2006-01-02")
		o.Papel = dominio.Papel(papel[0])
		o.Lado, _ = dominio.LadoDaLetra(lado)
		o.Estado = dominio.EstadoOrdem(estado)
		o.OrderID, o.SecID, o.MotivoGestor, o.MotivoRejeicao = oid.String, sid.String, mg.String, mr.String
		o.PrecoMedio, _ = decimal.NewFromString(pm)
		ords = append(ords, o)
	}
	return ops, ords, rows.Err()
}

func cortar(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
