package repo

import (
	"errors"

	"sort"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Quarentena (v1.13): uma linha do lote que o banco recusou por erro de dado.
type Quarentena struct {
	ID           int64     `json:"id"`
	Tabela       string    `json:"tabela"`
	Linha        string    `json:"linha"` // a linha em JSON (o que o motor tentou gravar)
	Erro         string    `json:"erro"`
	Em           time.Time `json:"em"`
	ResolvidaEm  time.Time `json:"resolvida_em,omitzero"`
	ResolvidaPor string    `json:"resolvida_por,omitempty"`
	Resolucao    string    `json:"resolucao,omitempty"`
}

// ErrDadoInvalido: erro de dado sem o MySQL (o repositório em memória e os testes usam).
var ErrDadoInvalido = errors.New("repo: dado inválido para a coluna")

// errosDeDado: os códigos do MySQL/MariaDB em que repetir a mesma linha não adianta (o dado é que
// não cabe): 1406 too long, 1366 incorrect string/integer, 1264 out of range, 1292 incorrect
// datetime, 1048 null, 1452 chave estrangeira (a linha-mãe foi para a quarentena).
var errosDeDado = map[uint16]bool{1406: true, 1366: true, 1264: true, 1292: true, 1048: true, 1452: true}

// ErroDeDado: o erro é de dado (quarentena), não de conexão ou de concorrência (repetir).
func ErroDeDado(err error) bool {
	if errors.Is(err, ErrDadoInvalido) {
		return true
	}
	var me *mysql.MySQLError
	return errors.As(err, &me) && errosDeDado[me.Number]
}

func (r *MariaDB) Quarentenar(q Quarentena) (int64, error) {
	res, err := r.db.Exec("INSERT INTO kalmanordemquarentena (tabela, linha, erro, em) VALUES (?,?,?,?)",
		cortar(q.Tabela, 40), q.Linha, cortar(q.Erro, 1000), utc(q.Em))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *MariaDB) QuarentenaPendente() ([]Quarentena, error) {
	rows, err := r.db.Query("SELECT id, tabela, linha, erro, em FROM kalmanordemquarentena WHERE resolvida_em IS NULL ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var qs []Quarentena
	for rows.Next() {
		var q Quarentena
		if err := rows.Scan(&q.ID, &q.Tabela, &q.Linha, &q.Erro, &q.Em); err != nil {
			return nil, err
		}
		qs = append(qs, q)
	}
	return qs, rows.Err()
}

func (r *MariaDB) ResolverQuarentena(id int64, por, resolucao string, em time.Time) (bool, error) {
	res, err := r.db.Exec("UPDATE kalmanordemquarentena SET resolvida_em=?, resolvida_por=?, resolucao=? WHERE id=? AND resolvida_em IS NULL",
		utc(em), cortar(por, 64), cortar(resolucao, 200), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (m *Memoria) Quarentenar(q Quarentena) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q.ID = int64(len(m.quarentena) + 1)
	m.quarentena = append(m.quarentena, q)
	return q.ID, nil
}

func (m *Memoria) QuarentenaPendente() ([]Quarentena, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var qs []Quarentena
	for _, q := range m.quarentena {
		if q.ResolvidaEm.IsZero() {
			qs = append(qs, q)
		}
	}
	sort.Slice(qs, func(i, j int) bool { return qs[i].ID < qs[j].ID })
	return qs, nil
}

func (m *Memoria) ResolverQuarentena(id int64, por, resolucao string, em time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.quarentena {
		if m.quarentena[i].ID == id && m.quarentena[i].ResolvidaEm.IsZero() {
			m.quarentena[i].ResolvidaEm, m.quarentena[i].ResolvidaPor, m.quarentena[i].Resolucao = em, por, resolucao
			return true, nil
		}
	}
	return false, nil
}

func (m *Medido) Quarentenar(q Quarentena) (int64, error) {
	return medir("Quarentenar", func() (int64, error) { return m.Base.Quarentenar(q) })
}
func (m *Medido) QuarentenaPendente() ([]Quarentena, error) {
	return medir("QuarentenaPendente", func() ([]Quarentena, error) { return m.Base.QuarentenaPendente() })
}
func (m *Medido) ResolverQuarentena(id int64, por, resolucao string, em time.Time) (bool, error) {
	return medir("ResolverQuarentena", func() (bool, error) { return m.Base.ResolverQuarentena(id, por, resolucao, em) })
}
