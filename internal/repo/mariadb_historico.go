package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

func (r *MariaDB) RegistrarSessao(e EventoSessao) error {
	_, err := r.db.Exec("INSERT INTO kalmanordemsessao (tipo, texto, em) VALUES (?,?,?)", e.Tipo, cortar(e.Texto, 1000), utc(e.Em))
	return err
}

func (r *MariaDB) SessaoEntre(de, ate time.Time) ([]EventoSessao, error) {
	rows, err := r.db.Query("SELECT tipo, texto, em FROM kalmanordemsessao WHERE em >= ? AND em < ? ORDER BY em, id", de.UTC(), ate.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var es []EventoSessao
	for rows.Next() {
		var e EventoSessao
		if err := rows.Scan(&e.Tipo, &e.Texto, &e.Em); err != nil {
			return nil, err
		}
		es = append(es, e)
	}
	return es, rows.Err()
}

func (r *MariaDB) DiaDaOperacao(mod string, op int64) (string, bool, error) {
	var d time.Time
	err := r.db.QueryRow("SELECT data FROM kalmanordemoperacao WHERE modulo = ? AND operacao_id = ?", modulo(mod), op).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return d.Format("2006-01-02"), true, nil
}

const colunasEvento = `id, ordem_id, modulo, direcao, msgtype, exectype, ordstatus, clordid, orderid37, execid, lastqty, lastpx,
	cumqty, avgpx, texto, codigo, fix_raw, em`

func (r *MariaDB) eventosFIX(where string, args ...any) ([]EventoFIX, error) {
	rows, err := r.db.Query("SELECT "+colunasEvento+" FROM kalmanordemevento WHERE "+where+" ORDER BY em, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var es []EventoFIX
	for rows.Next() {
		var e EventoFIX
		var ordem, lq, cq sql.NullInt64
		var et, st, cl, id37, ex, lp, ap, tx, cod, raw sql.NullString
		if err := rows.Scan(&e.ID, &ordem, &e.Modulo, &e.Direcao, &e.MsgType, &et, &st, &cl, &id37, &ex, &lq, &lp, &cq, &ap, &tx, &cod, &raw, &e.Em); err != nil {
			return nil, err
		}
		e.OrdemID, e.ExecType, e.OrdStatus, e.ClOrdID, e.OrderID, e.ExecID = ordem.Int64, et.String, st.String, cl.String, id37.String, ex.String
		e.Texto, e.Codigo, e.Bruta = tx.String, cod.String, raw.String
		if lq.Valid {
			v := int(lq.Int64)
			e.LastQty = &v
		}
		if cq.Valid {
			v := int(cq.Int64)
			e.CumQty = &v
		}
		if lp.Valid {
			d, _ := decimal.NewFromString(lp.String)
			e.LastPx = &d
		}
		if ap.Valid {
			d, _ := decimal.NewFromString(ap.String)
			e.AvgPx = &d
		}
		es = append(es, e)
	}
	return es, rows.Err()
}

func (r *MariaDB) EventosFIXDasOrdens(ids []int64) ([]EventoFIX, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return r.eventosFIX("ordem_id IN (?"+strings.Repeat(",?", len(ids)-1)+")", args...)
}

func (r *MariaDB) publicadosOnde(where string, args ...any) ([]Publicado, error) {
	rows, err := r.db.Query("SELECT seq, tipo, modulo, operacao_id, dados, em FROM kalmanordempub WHERE "+where+" ORDER BY seq", args...)
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

func (r *MariaDB) PublicadosDaOperacao(mod string, op int64) ([]Publicado, error) {
	return r.publicadosOnde("modulo = ? AND operacao_id = ?", modulo(mod), op)
}

func (r *MariaDB) PublicadosEntre(de, ate time.Time, tipo string) ([]Publicado, error) {
	return r.publicadosOnde("em >= ? AND em < ? AND (? = '' OR tipo = ?)", de.UTC(), ate.UTC(), tipo, tipo)
}
