package repo

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// linhasPorComando: linhas por INSERT de várias linhas (o pacote fica bem abaixo do max_allowed_packet).
const linhasPorComando = 200

// inserir: INSERT de várias linhas, em blocos; sufixo = "ON DUPLICATE KEY UPDATE ..." ou vazio.
func inserir(tx *sql.Tx, cabeca string, ncol int, linhas [][]any, sufixo string) error {
	marca := "(" + strings.TrimSuffix(strings.Repeat("?,", ncol), ",") + ")"
	for ini := 0; ini < len(linhas); ini += linhasPorComando {
		fim := min(ini+linhasPorComando, len(linhas))
		var b strings.Builder
		b.WriteString(cabeca)
		b.WriteString(" VALUES ")
		args := make([]any, 0, (fim-ini)*ncol)
		for i := ini; i < fim; i++ {
			if i > ini {
				b.WriteByte(',')
			}
			b.WriteString(marca)
			args = append(args, linhas[i]...)
		}
		if sufixo != "" {
			b.WriteString(" " + sufixo)
		}
		if _, err := tx.Exec(b.String(), args...); err != nil {
			return fmt.Errorf("%s: %w", strings.Fields(cabeca)[2], err)
		}
	}
	return nil
}

const colunasOp = `id, data, modulo, operacao_id, uid, conta, destino, conector, ambiente, simbolo, lado, qtd_pedida, stop_pts, stop_ajustado, stop_origem, stop_ajustado_origem, estado,
	motivo_entrada, ts_decisao, motivo_saida, origem_saida, saida_pedida_em, divergente, pnl_pts, encerrou_entrada, contador_n,
	pedido_entrada, resposta_entrada, pedido_saida, resposta_saida, sinal_entrada, sinal_saida, criado_em, atualizado_em`

func linhaOp(o dominio.Operacao, agora time.Time) []any {
	cn, _ := json.Marshal(o.ContadorN)
	// v1.13: textos cortados ao tamanho da coluna (o 1406 não trava mais o lote; HML 19/10 10:41)
	return []any{o.ID, o.Data, modulo(o.Modulo), o.OperacaoID, o.UID, nulo(cortar(o.Conta, 20)), o.Destino, dominio.ConectorDe(o.Conector), dominio.AmbienteDe(o.Ambiente), nulo(cortar(o.Simbolo, 20)), o.Lado.Letra(), o.QtdPedida,
		o.StopPts, nuloInt(o.StopAjustado), nulo(cortar(o.StopOrigem, 20)), nulo(cortar(o.StopAjustadoOrigem, 20)), string(o.Estado), nulo(cortar(o.MotivoEntrada, 20)), nulo(cortar(o.TsDecisao, 40)), nulo(cortar(o.MotivoSaida, 60)),
		nulo(cortar(o.OrigemSaida, 30)),
		utc(o.SaidaPedidaEm), o.Divergente, o.PnL.String(), o.EncerrouEntrada, string(cn), nulo(o.PedidoEntrada), nulo(o.RespostaEntrada),
		nulo(o.PedidoSaida), nulo(o.RespostaSaida), rajadaSQL(o.SinalEntrada), rajadaSQL(o.SinalSaida), utc(o.CriadaEm), agora}
}

const colunasOrdem = `id, data, modulo, uid, operacao_id, papel, conta, simbolo, clordid_base, clordid_atual, orderid37, secid198,
	estrategia, lado, qtd, preco, preco_stop, preco_stop_lim, estado, qtd_exec, preco_medio, motivo_gestor, motivo_rejeicao, criado_em, atualizado_em`

func linhaOrdem(o dominio.Ordem, agora time.Time) []any {
	return []any{o.ID, o.Data, modulo(o.Modulo), o.UID, o.OperacaoID, o.Papel.String(), nulo(cortar(o.Conta, 20)), nulo(cortar(o.Simbolo, 20)),
		cortar(o.ClOrdBase, 40), cortar(o.ClOrdAtual, 40), nulo(cortar(o.OrderID, 40)), nulo(cortar(o.SecID, 40)), o.Estrategia, o.Lado.Letra(), o.Qtd,
		o.Preco, o.Stop, o.StopLim, string(o.Estado), o.QtdExec, o.PrecoMedio.String(), nulo(cortar(o.MotivoGestor, 60)),
		nulo(cortar(o.MotivoRejeicao, 500)), utc(o.CriadaEm), agora}
}

// GravarLote: uma transação, um INSERT de várias linhas por tabela (v1.10 B).
func (r *MariaDB) GravarLote(l *Lote) error {
	if l.Vazio() {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	agora := time.Now().UTC()
	ncolOp, ncolOrdem := len(strings.Split(colunasOp, ",")), len(strings.Split(colunasOrdem, ","))

	var novas, velhas [][]any
	for _, o := range l.NovasOperacoes {
		novas = append(novas, linhaOp(o, agora))
	}
	for _, o := range l.Operacoes {
		velhas = append(velhas, linhaOp(o, agora))
	}
	if err := inserir(tx, "INSERT INTO kalmanordemoperacao ("+colunasOp+")", ncolOp, novas, ""); err != nil {
		return err
	}
	if err := inserir(tx, "INSERT INTO kalmanordemoperacao ("+colunasOp+")", ncolOp, velhas,
		`ON DUPLICATE KEY UPDATE estado=VALUES(estado), stop_ajustado=VALUES(stop_ajustado), stop_origem=VALUES(stop_origem),
		stop_ajustado_origem=VALUES(stop_ajustado_origem), motivo_saida=VALUES(motivo_saida),
		origem_saida=VALUES(origem_saida), saida_pedida_em=VALUES(saida_pedida_em), divergente=VALUES(divergente), pnl_pts=VALUES(pnl_pts),
		encerrou_entrada=VALUES(encerrou_entrada), contador_n=VALUES(contador_n), pedido_entrada=VALUES(pedido_entrada),
		resposta_entrada=VALUES(resposta_entrada), pedido_saida=VALUES(pedido_saida), resposta_saida=VALUES(resposta_saida),
		sinal_entrada=VALUES(sinal_entrada), sinal_saida=VALUES(sinal_saida), atualizado_em=VALUES(atualizado_em)`); err != nil {
		return err
	}
	novas, velhas = nil, nil
	for _, o := range l.NovasOrdens {
		novas = append(novas, linhaOrdem(o, agora))
	}
	for _, o := range l.Ordens {
		velhas = append(velhas, linhaOrdem(o, agora))
	}
	if err := inserir(tx, "INSERT INTO kalmanordem ("+colunasOrdem+")", ncolOrdem, novas, ""); err != nil {
		return err
	}
	if err := inserir(tx, "INSERT INTO kalmanordem ("+colunasOrdem+")", ncolOrdem, velhas,
		`ON DUPLICATE KEY UPDATE clordid_atual=VALUES(clordid_atual), orderid37=VALUES(orderid37), secid198=VALUES(secid198), qtd=VALUES(qtd),
		preco=VALUES(preco), preco_stop=VALUES(preco_stop), preco_stop_lim=VALUES(preco_stop_lim), estado=VALUES(estado), qtd_exec=VALUES(qtd_exec),
		preco_medio=VALUES(preco_medio), motivo_rejeicao=VALUES(motivo_rejeicao), atualizado_em=VALUES(atualizado_em)`); err != nil {
		return err
	}

	var ev [][]any
	for _, e := range l.Eventos {
		var lq, cq, lp, ap, ordem any
		if e.LastQty != nil {
			lq = *e.LastQty
		}
		if e.CumQty != nil {
			cq = *e.CumQty
		}
		if e.LastPx != nil {
			lp = e.LastPx.String()
		}
		if e.AvgPx != nil {
			ap = e.AvgPx.String()
		}
		if e.OrdemID != 0 {
			ordem = e.OrdemID
		}
		ev = append(ev, []any{ordem, modulo(e.Modulo), e.Direcao, cortar(e.MsgType, 20), nulo(cortar(e.ExecType, 1)), nulo(cortar(e.OrdStatus, 1)),
			nulo(cortar(e.ClOrdID, 60)), nulo(cortar(e.OrderID, 40)), nulo(cortar(e.ExecID, 80)), lq, lp, cq, ap, nulo(cortar(e.Texto, 1000)), nulo(cortar(e.Codigo, 20)), nulo(e.Bruta), utc(e.Em)})
	}
	// INSERT IGNORE: o fill repetido (37, 17) já foi descartado pelo motor; a chave única fica de rede
	if err := inserir(tx, `INSERT IGNORE INTO kalmanordemevento (ordem_id, modulo, direcao, msgtype, exectype, ordstatus, clordid, orderid37,
		execid, lastqty, lastpx, cumqty, avgpx, texto, codigo, fix_raw, em)`, 17, ev, ""); err != nil {
		return err
	}

	var pubs [][]any
	for _, p := range l.Publicados {
		var op any
		if p.OperacaoID != 0 {
			op = p.OperacaoID
		}
		pubs = append(pubs, []any{cortar(p.Tipo, 40), modulo(p.Modulo), op, string(p.Dados), utc(p.Em)})
	}
	if err := inserir(tx, "INSERT INTO kalmanordempub (tipo, modulo, operacao_id, dados, em)", 5, pubs, ""); err != nil {
		return err
	}
	var est [][]any
	for _, e := range l.Estados {
		est = append(est, []any{cortar(e.Chave, 64), e.Valor, e.Em.UTC()})
	}
	if err := inserir(tx, "INSERT INTO kalmanordemestado (chave, valor, atualizado_em)", 3, est,
		"ON DUPLICATE KEY UPDATE valor=VALUES(valor), atualizado_em=VALUES(atualizado_em)"); err != nil {
		return err
	}
	var ses [][]any
	for _, e := range l.Sessao {
		ses = append(ses, []any{cortar(e.Tipo, 16), cortar(e.Texto, 1000), utc(e.Em)})
	}
	if err := inserir(tx, "INSERT INTO kalmanordemsessao (tipo, texto, em)", 3, ses, ""); err != nil {
		return err
	}
	var ped [][]any
	for _, p := range l.Pedidos {
		var op, uid any
		if p.OperacaoID != 0 {
			op = p.OperacaoID
		}
		if p.UID != 0 {
			uid = p.UID
		}
		ped = append(ped, []any{p.Data, modulo(cortar(p.Modulo, 8)), op, uid, cortar(p.Rota, 16), nulo(cortar(p.Papel, 8)), p.HTTPStatus, p.Aceito,
			nulo(cortar(p.Codigo, 40)), strings.ToValidUTF8(cortar(p.Corpo, 65000), ""), strings.ToValidUTF8(cortar(p.Resposta, 65000), ""), utc(p.Em)})
	}
	if err := inserir(tx, `INSERT INTO kalmanordempedido (data, modulo, operacao_id, uid, rota, papel, http_status, aceito, codigo, corpo,
		resposta, em)`, 12, ped, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// MaioresIDs: o maior ID de operação e de ordem (o motor continua dali).
func (r *MariaDB) MaioresIDs() (int64, int64, error) {
	var op, ordem sql.NullInt64
	if err := r.db.QueryRow("SELECT MAX(id) FROM kalmanordemoperacao").Scan(&op); err != nil {
		return 0, 0, err
	}
	if err := r.db.QueryRow("SELECT MAX(id) FROM kalmanordem").Scan(&ordem); err != nil {
		return 0, 0, err
	}
	return op.Int64, ordem.Int64, nil
}

// ChavesFillDoDia: os pares (37, 17) gravados em [de, ate) — a deduplicação de fill do motor.
func (r *MariaDB) ChavesFillDoDia(de, ate time.Time) (map[string]bool, error) {
	rows, err := r.db.Query(`SELECT orderid37, execid FROM kalmanordemevento WHERE em >= ? AND em < ?
		AND orderid37 IS NOT NULL AND execid IS NOT NULL`, de.UTC(), ate.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		m[ChaveFill(a, b)] = true
	}
	return m, rows.Err()
}

// ConfigUsuarios: usutxtconfig e usutxtconfigprd de vários uids (cache do motor, v1.10 B).
func (r *MariaDB) OperacoesExistentes(mod string, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for ini := 0; ini < len(ids); ini += 500 {
		fim := min(ini+500, len(ids))
		args := []any{modulo(mod)}
		for _, id := range ids[ini:fim] {
			args = append(args, id)
		}
		rows, err := r.db.Query("SELECT operacao_id FROM kalmanordemoperacao WHERE modulo = ? AND operacao_id IN ("+
			strings.TrimSuffix(strings.Repeat("?,", fim-ini), ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		rows.Close()
	}
	return out, nil
}

func (r *MariaDB) ConfigUsuarios(uids []int) (map[int][2]string, error) {
	out := map[int][2]string{}
	for ini := 0; ini < len(uids); ini += 500 {
		fim := min(ini+500, len(uids))
		args := make([]any, 0, fim-ini)
		for _, u := range uids[ini:fim] {
			args = append(args, fmt.Sprint(u))
		}
		rows, err := r.db.Query("SELECT usucodusuario, usutxtconfig, usutxtconfigprd FROM "+r.tabelaUsuario+
			" WHERE usucodusuario IN ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+")", args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cod string
			var hml, prd sql.NullString
			if err := rows.Scan(&cod, &hml, &prd); err != nil {
				rows.Close()
				return nil, err
			}
			var uid int
			fmt.Sscan(strings.TrimSpace(cod), &uid)
			out[uid] = [2]string{hml.String, prd.String}
		}
		rows.Close()
	}
	return out, nil
}
