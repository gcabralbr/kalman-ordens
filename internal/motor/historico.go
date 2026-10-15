package motor

import (
	"errors"
	"fmt"
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
	return Resposta{"operacao": (&Motor{}).descrever(op), "eventos": eventos, "mensagens": mensagens}, nil
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
	for _, d := range ops {
		if (uid != 0 && d.UID != uid) || (modulo != "" && d.Modulo != modulo) {
			continue
		}
		op := operacaoGravada(d, ords)
		k := chave{d.Modulo, d.OperacaoID}
		lista = append(lista, map[string]any{
			"operacao_id": d.OperacaoID, "uid": d.UID, "modulo": d.Modulo, "estado": d.Estado, "lado": d.Lado.Letra(),
			"qtd": d.QtdPedida, "preco_medio_entrada": medioOuNulo(op, true), "preco_medio_saida": medioOuNulo(op, false),
			"pnl_pts": d.PnL, "motivo_saida": textoOuNulo(d.MotivoSaida), "origem_saida": textoOuNulo(d.OrigemSaida),
			"criada_em": emBR(d.CriadaEm), "zerada_em": emBROuNulo(zerada[k]), "alertas_n": alertas[k],
		})
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
