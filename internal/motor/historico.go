package motor

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// Histórico para a tela de admin do Kalman e a CLI "historico" (SPEC v1.5 R). Só leitura, direto
// do banco (qualquer dia; não passa pela fila do motor). Todo "em" em RFC3339 com o fuso do pregão
// (-03:00) e milissegundos.

// ErrNaoEncontrada: operação inexistente no módulo. ErrDia: dia ausente ou fora de AAAA-MM-DD.
var (
	ErrNaoEncontrada = errors.New("operação não encontrada")
	ErrDia           = errors.New("dia obrigatório, AAAA-MM-DD")
)

const formatoEm = "2006-01-02T15:04:05.000-07:00"

func emBR(t time.Time) string { return t.In(relogio.Fuso).Format(formatoEm) }

func emBROuNulo(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return emBR(t)
}

// Dia: o intervalo [de, ate) do dia do pregão AAAA-MM-DD.
func Dia(dia string) (de, ate time.Time, err error) {
	de, err = time.ParseInLocation("2006-01-02", dia, relogio.Fuso)
	if err != nil {
		return de, ate, fmt.Errorf("%w (veio %q)", ErrDia, dia)
	}
	return de, de.AddDate(0, 0, 1), nil
}

// operacaoGravada: a operação e as ordens como estão no banco, no formato do motor.
func operacaoGravada(op dominio.Operacao, ords []dominio.Ordem) *operacao {
	o := &operacao{Operacao: op}
	for i := range ords {
		if ords[i].Modulo == op.Modulo && ords[i].OperacaoID == op.OperacaoID {
			o.ordens = append(o.ordens, &ordem{Ordem: ords[i]})
		}
	}
	return o
}

func carregarOperacao(r repo.Repositorio, modulo string, id int64) (*operacao, error) {
	if modulo == "" {
		modulo = dominio.ModuloPadrao
	}
	dia, ok, err := r.DiaDaOperacao(modulo, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNaoEncontrada
	}
	ops, ords, err := r.CarregarDia(dia)
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		if op.Modulo == modulo && op.OperacaoID == id {
			return operacaoGravada(op, ords), nil
		}
	}
	return nil, ErrNaoEncontrada
}

// HistoricoOperacao: GET /operacoes/{id}/historico. operacao = o mesmo do GET /ordens/{id};
// eventos = o publicado ao gestor; mensagens = cada mensagem FIX de aplicação das ordens da
// operação. cru: inclui fix_raw (senha mascarada, SOH como "|").
func HistoricoOperacao(r repo.Repositorio, modulo string, id int64, cru bool) (Resposta, error) {
	op, err := carregarOperacao(r, modulo, id)
	if err != nil {
		return nil, err
	}
	pubs, err := r.PublicadosDaOperacao(op.Modulo, op.OperacaoID)
	if err != nil {
		return nil, err
	}
	eventos := []map[string]any{}
	for _, p := range pubs {
		eventos = append(eventos, map[string]any{"seq": p.Seq, "tipo": p.Tipo, "dados": p.Dados, "em": emBR(p.Em)})
	}
	var ids []int64
	for _, o := range op.ordens {
		ids = append(ids, o.ID)
	}
	evs, err := r.EventosFIXDasOrdens(ids)
	if err != nil {
		return nil, err
	}
	mensagens := []map[string]any{}
	for _, e := range evs {
		mensagens = append(mensagens, mensagem(e, cru))
	}
	ps, err := r.PedidosDaOperacao(op.Modulo, op.OperacaoID)
	if err != nil {
		return nil, err
	}
	pedidos := []map[string]any{}
	for _, p := range ps {
		pedidos = append(pedidos, pedidoJSON(p))
	}
	return Resposta{"operacao": (&Motor{}).descrever(op), "eventos": eventos, "mensagens": mensagens, "pedidos": pedidos}, nil
}

// pedidoJSON (v1.6 P1): corpo e resposta como JSON quando são JSON (senão o texto cru).
func pedidoJSON(p repo.PedidoHTTP) map[string]any {
	return map[string]any{"em": emBR(p.Em), "rota": p.Rota, "papel": textoOuNulo(p.Papel), "http_status": p.HTTPStatus,
		"aceito": p.Aceito, "codigo": textoOuNulo(p.Codigo), "corpo": jsonOuTexto(p.Corpo), "resposta": jsonOuTexto(p.Resposta)}
}

func jsonOuTexto(s string) any {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return s
}

func mensagem(e repo.EventoFIX, cru bool) map[string]any {
	texto := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	var lq, cq, lp, ap any
	if e.LastQty != nil {
		lq = *e.LastQty
	}
	if e.CumQty != nil {
		cq = *e.CumQty
	}
	if e.LastPx != nil {
		lp = *e.LastPx
	}
	if e.AvgPx != nil {
		ap = *e.AvgPx
	}
	m := map[string]any{"em": emBR(e.Em), "direcao": e.Direcao, "msgtype": e.MsgType, "exectype": texto(e.ExecType),
		"ordstatus": texto(e.OrdStatus), "clordid": texto(e.ClOrdID), "orderid": texto(e.OrderID), "lastqty": lq, "lastpx": lp,
		"cumqty": cq, "avgpx": ap, "texto": texto(e.Texto), "codigo": texto(e.Codigo)}
	if cru {
		m["fix_raw"] = texto(FixLegivel(e.Bruta))
	}
	return m
}

// FixLegivel: a mensagem FIX como gravada, com a senha mascarada (554/925) e o SOH como "|".
func FixLegivel(bruta string) string {
	return strings.ReplaceAll(pnt.MascararTexto(bruta), "\x01", "|")
}

// OperacoesDoDia: GET /operacoes?dia=&uid=&modulo= (uid 0 e modulo vazio = todos), em ordem de
// criação. preco_medio_* e zerada_em nulos quando não houve.
func OperacoesDoDia(r repo.Repositorio, dia string, uid int, modulo string) ([]map[string]any, error) {
	de, ate, err := Dia(dia)
	if err != nil {
		return nil, err
	}
	ops, ords, err := r.CarregarDia(dia)
	if err != nil {
		return nil, err
	}
	pubs, err := r.PublicadosEntre(de, ate, "")
	if err != nil {
		return nil, err
	}
	type chave struct {
		mod string
		id  int64
	}
	alertas, zerada := map[chave]int{}, map[chave]time.Time{}
	for _, p := range pubs {
		k := chave{p.Modulo, p.OperacaoID}
		switch p.Tipo {
		case "ALERTA":
			alertas[k]++
		case "ZERADA":
			zerada[k] = p.Em
		}
	}
	lista := []map[string]any{}
	existe := map[chave]bool{}
	for _, d := range ops {
		existe[chave{d.Modulo, d.OperacaoID}] = true
		if (uid != 0 && d.UID != uid) || (modulo != "" && d.Modulo != modulo) {
			continue
		}
		op := operacaoGravada(d, ords)
		k := chave{d.Modulo, d.OperacaoID}
		lista = append(lista, map[string]any{
			"operacao_id": d.OperacaoID, "uid": d.UID, "modulo": d.Modulo, "estado": d.Estado, "lado": d.Lado.Letra(),
			"qtd": d.QtdPedida, "preco_medio_entrada": medioOuNulo(op, true), "preco_medio_saida": medioOuNulo(op, false),
			"pnl_pts": d.PnL, "motivo_saida": textoOuNulo(d.MotivoSaida), "origem_saida": textoOuNulo(d.OrigemSaida),
			"stop_origem": op.stopOrigem(), "conector": dominio.ConectorDe(d.Conector), "criada_em": emBR(d.CriadaEm), "zerada_em": emBROuNulo(zerada[k]), "alertas_n": alertas[k],
			"sinal": sinalDe(op), "em": d.CriadaEm,
		})
	}
	// v1.6 P1: pedido recusado que não virou operação (limite, preço, habilitação, corpo ilegível...)
	// entra na lista com estado RECUSADA, o código, o motivo e o corpo
	ps, err := r.PedidosDoDia(dia, uid, modulo)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.Aceito || existe[chave{p.Modulo, p.OperacaoID}] || (p.Rota != "ORDENS" && p.Rota != "STOP" && p.Rota != RotaSinais) ||
			(p.Rota == RotaSinais && p.HTTPStatus == 503) {
			// do POST /sinais (v1.10 §6): o item recusado e a recusa do lote inteiro; o 503 não (o
			// motor tratou a parte, os itens têm linha própria)
			continue
		}
		var c struct {
			Lado     string            `json:"lado"`
			Qtd      *int              `json:"qtd"`
			Entradas []json.RawMessage `json:"entradas"`
			Saidas   []json.RawMessage `json:"saidas"`
		}
		_ = json.Unmarshal([]byte(p.Corpo), &c)
		if p.Rota == RotaSinais && p.OperacaoID != 0 && len(c.Entradas)+len(c.Saidas) == 1 {
			// a linha de um item: lado e qtd do item
			_ = json.Unmarshal(append(c.Entradas, c.Saidas...)[0], &c)
		}
		var resp struct {
			Motivo string `json:"motivo"`
		}
		_ = json.Unmarshal([]byte(p.Resposta), &resp)
		var opID, u, qtd any
		if p.OperacaoID != 0 {
			opID = p.OperacaoID
		}
		if p.UID != 0 {
			u = p.UID
		}
		if c.Qtd != nil {
			qtd = *c.Qtd
		}
		lista = append(lista, map[string]any{
			"operacao_id": opID, "uid": u, "modulo": p.Modulo, "estado": "RECUSADA", "lado": textoOuNulo(c.Lado), "qtd": qtd,
			"preco_medio_entrada": nil, "preco_medio_saida": nil, "pnl_pts": nil, "motivo_saida": nil, "origem_saida": nil, "stop_origem": nil, "conector": nil,
			"criada_em": emBR(p.Em), "zerada_em": nil, "alertas_n": 0,
			"rota": p.Rota, "papel": textoOuNulo(p.Papel), "http_status": p.HTTPStatus, "codigo": textoOuNulo(p.Codigo),
			"motivo": textoOuNulo(resp.Motivo), "corpo": jsonOuTexto(p.Corpo),
			"em": p.Em,
		})
	}
	sort.SliceStable(lista, func(i, j int) bool { return lista[i]["em"].(time.Time).Before(lista[j]["em"].(time.Time)) })
	for _, x := range lista {
		delete(x, "em")
	}
	return lista, nil
}

func medioOuNulo(op *operacao, entrada bool) any {
	if m, ok := op.medio(entrada); ok {
		return m
	}
	return nil
}

func textoOuNulo(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// EventosDaSessao: GET /sessao/eventos?dia= — logon, logout, queda, logon recusado, ResendRequest,
// conciliações e alertas sem operação.
func EventosDaSessao(r repo.Repositorio, dia string) ([]map[string]any, error) {
	de, ate, err := Dia(dia)
	if err != nil {
		return nil, err
	}
	es, err := r.SessaoEntre(de, ate)
	if err != nil {
		return nil, err
	}
	lista := []map[string]any{}
	for _, e := range es {
		lista = append(lista, map[string]any{"em": emBR(e.Em), "tipo": e.Tipo, "texto": e.Texto})
	}
	return lista, nil
}

// Repo: o repositório do motor (consultas de histórico pela API).
func (m *Motor) Repo() repo.Repositorio { return m.repo }

// RegistrarPedidoHTTP (v1.6 P1): o POST como chegou (corpo cru) e como foi respondido, inclusive
// 400 (corpo ilegível) e 503. Chamado pela API, fora da fila do motor; uma falha ao gravar só vai
// ao log (não muda a resposta já dada).
func (m *Motor) RegistrarPedidoHTTP(rota, modulo string, operacaoID int64, uid int, papel string, corpo []byte, status int, resposta Resposta) {
	agora := m.rel.Agora()
	b, _ := json.Marshal(resposta)
	codigo, _ := resposta["codigo"].(string)
	p := &repo.PedidoHTTP{Data: agora.In(relogio.Fuso).Format("2006-01-02"), Modulo: modulo, OperacaoID: operacaoID, UID: uid,
		Rota: rota, Papel: papel, HTTPStatus: status, Aceito: resposta["aceito"] == true, Codigo: codigo,
		Corpo: string(corpo), Resposta: string(b), Em: agora}
	// vai no lote da próxima volta (v1.10 B): a goroutine HTTP não disputa o banco com o motor
	m.v.muPedidos.Lock()
	m.v.pedidos = append(m.v.pedidos, *p)
	m.v.muPedidos.Unlock()
}
