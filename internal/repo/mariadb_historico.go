package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

func (r *MariaDB) RegistrarPedido(p *PedidoHTTP) error {
	p.Modulo = modulo(cortar(p.Modulo, 8))
	var op, uid any
	if p.OperacaoID != 0 {
		op = p.OperacaoID
	}
	if p.UID != 0 {
		uid = p.UID
	}
	res, err := r.db.Exec(`INSERT INTO kalmanordempedido (data, modulo, operacao_id, uid, rota, papel, http_status, aceito, codigo,
		corpo, resposta, em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, p.Data, p.Modulo, op, uid, p.Rota, nulo(cortar(p.Papel, 8)), p.HTTPStatus,
		p.Aceito, nulo(cortar(p.Codigo, 40)), strings.ToValidUTF8(cortar(p.Corpo, 65000), ""), strings.ToValidUTF8(cortar(p.Resposta, 65000), ""), utc(p.Em))
	if err != nil {
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

func (r *MariaDB) pedidos(where string, args ...any) ([]PedidoHTTP, error) {
	rows, err := r.db.Query(`SELECT id, data, modulo, operacao_id, uid, rota, papel, http_status, aceito, codigo, corpo, resposta, em
		FROM kalmanordempedido WHERE `+where+` ORDER BY em, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []PedidoHTTP
	for rows.Next() {
		var p PedidoHTTP
		var d time.Time
		var op, uid sql.NullInt64
		var papel, cod sql.NullString
		if err := rows.Scan(&p.ID, &d, &p.Modulo, &op, &uid, &p.Rota, &papel, &p.HTTPStatus, &p.Aceito, &cod, &p.Corpo, &p.Resposta, &p.Em); err != nil {
			return nil, err
		}
		p.Data, p.OperacaoID, p.UID, p.Papel, p.Codigo = d.Format("2006-01-02"), op.Int64, int(uid.Int64), papel.String, cod.String
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

func (r *MariaDB) PedidosDoDia(data string, uid int, mod string) ([]PedidoHTTP, error) {
	return r.pedidos("data = ? AND (? = 0 OR uid = ?) AND (? = '' OR modulo = ?)", data, uid, uid, mod, mod)
}

func (r *MariaDB) PedidosDaOperacao(mod string, op int64) ([]PedidoHTTP, error) {
	return r.pedidos("modulo = ? AND operacao_id = ?", modulo(mod), op)
}

// TabelasExpurgo (v1.6 E1): as tabelas com retenção — mensagens FIX, eventos publicados, sessão e
// pedidos HTTP. O resumo (kalmanordemoperacao, kalmanordem) não tem expurgo.
var TabelasExpurgo = []string{"kalmanordemevento", "kalmanordempub", "kalmanordemsessao", "kalmanordempedido"}

// Expurgar apaga, de cada tabela de TabelasExpurgo, as linhas com em < antes, em lotes (sem
// segurar a tabela por muito tempo). simular: só conta. Usa um usuário de banco próprio (DELETE só
// nessas tabelas); o do serviço não tem DELETE.
func (r *MariaDB) Expurgar(antes time.Time, lote int, simular bool) (map[string]int64, error) {
	n := map[string]int64{}
	for _, t := range TabelasExpurgo {
		if simular {
			var c int64
			if err := r.db.QueryRow("SELECT COUNT(*) FROM "+t+" WHERE em < ?", antes.UTC()).Scan(&c); err != nil {
				return n, fmt.Errorf("%s: %w", t, err)
			}
			n[t] = c
			continue
		}
		for {
			res, err := r.db.Exec("DELETE FROM "+t+" WHERE em < ? LIMIT ?", antes.UTC(), lote)
			if err != nil {
				return n, fmt.Errorf("%s: %w", t, err)
			}
			k, _ := res.RowsAffected()
			n[t] += k
			if k < int64(lote) {
				break
			}
		}
	}
	return n, nil
}
