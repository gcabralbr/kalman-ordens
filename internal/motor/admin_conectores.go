package motor

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/flexscan"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// A tela de admin por conector e ambiente (SPEC-ADMIN-CONECTORES-v1, v1.34):
//   - o histórico (eventos da sessão, ordens e fio do dia) é lido do banco, com a instância do
//     ambiente ligada ou não;
//   - o ao vivo e o "consultar agora" exigem a instância; sem ela, 409 com o código que diz o que
//     falta (§3.6).

// Códigos das respostas sem instância ou sem conector (§3.6).
const (
	InstanciaDesligada = "INSTANCIA_DESLIGADA"
	ConectorAusente    = "CONECTOR_AUSENTE"
	SemSessao          = "SEM_SESSAO"
	AmbienteInvalido   = "AMBIENTE_INVALIDO"
)

// Indisponivel: a resposta de uma rota que exige a instância ou o conector, com o HTTP dela.
type Indisponivel struct {
	HTTP int
	Resp Resposta
}

func (s *Servico) indisponivel(codigo, amb, conector, motivo string) *Indisponivel {
	r := Resposta{"erro": codigo, "codigo": codigo, "ambiente": amb, "motivo": motivo,
		"ambientes_permitidos": s.principal.p.Amb.Lista()}
	if conector != "" {
		r["conector"] = conector
		r["historico"] = "/historico/fio?dia=" + s.principal.hoje() + "&ambiente=" + amb + "&conector=" + conector
	}
	st := http.StatusConflict
	if codigo == SemSessao {
		st = http.StatusServiceUnavailable
	}
	return &Indisponivel{HTTP: st, Resp: r}
}

// instanciaDe: a instância do ambiente, ou a resposta INSTANCIA_DESLIGADA (o ambiente já validado).
func (s *Servico) instanciaDe(amb, conector string) (*Motor, *Indisponivel) {
	if m := s.motores[amb]; m != nil {
		return m, nil
	}
	return nil, s.indisponivel(InstanciaDesligada, amb, conector, amb+" fora de AMBIENTES_PERMITIDOS nesta maquina")
}

// Hoje: o dia corrente (o padrão das rotas de histórico).
func (s *Servico) Hoje() string { return s.principal.hoje() }

// Conectores: GET /conectores (§3.1), o mapa para a tela montar as abas.
func (s *Servico) Conectores() (Resposta, error) {
	var l []map[string]any
	for _, amb := range []string{dominio.AmbienteSimulador, dominio.AmbienteReal} {
		hist, err := s.Repo().AmbienteTemOperacao(amb)
		if err != nil {
			return nil, err
		}
		a := map[string]any{"ambiente": amb, "historico": hist}
		m := s.motores[amb]
		if m == nil {
			a["instancia"], a["motivo"], a["conectores"] = "DESLIGADA", amb+" fora de AMBIENTES_PERMITIDOS nesta maquina", []any{}
			l = append(l, a)
			continue
		}
		con := []map[string]any{}
		m.Fazer(func() { // o estado dos canais é do motor (a mesma leitura do /saude)
			for _, chave := range m.chavesCanais() {
				est, motivo, _ := m.canal(chave)
				nome, canal, _ := strings.Cut(chave, ":")
				con = append(con, map[string]any{"conector": nome, "canal": nuloSeVazio(canal), "estado": est, "motivo": nuloSeVazio(motivo)})
			}
		})
		a["instancia"], a["conectores"] = "LIGADA", con
		l = append(l, a)
	}
	return Resposta{"ambientes": l}, nil
}

// conectorDaQuery: PNT, FLEXSCAN, SERVICO ou vazio (todos); ok falso: outro valor.
func conectorDaQuery(v string, servico bool) (string, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	switch v {
	case "", dominio.ConectorPnT, dominio.ConectorFlexscan:
		return v, true
	case ConectorServico:
		return v, servico
	}
	return "", false
}

// ConectorDaQuery: o ?conector= das rotas de histórico (SERVICO só nos eventos da sessão).
func ConectorDaQuery(v string, servico bool) (string, bool) { return conectorDaQuery(v, servico) }

// conectorDaSessao (§3.2): o gravado; nas linhas de antes da 0018, inferido pelo tipo e pelo código.
func conectorDaSessao(e repo.EventoSessao) (string, bool) {
	if e.Conector != "" {
		return e.Conector, false
	}
	switch e.Tipo {
	case "LOGON", "LOGOUT", "QUEDA", "RESEND", "LOGON_RECUSADO", "BOLSA_FORA", "BOLSA_VOLTOU":
		return dominio.ConectorPnT, true
	}
	codigo, resto, _ := strings.Cut(e.Texto, ": ")
	if strings.Contains(e.Texto, "[flexscan") {
		return dominio.ConectorFlexscan, true
	}
	return ConectorDoAlerta(codigo, resto), true
}

// EventosDaSessaoFiltrados: GET /sessao/eventos?dia=&ambiente=&conector= (§3.2). Ambiente vazio =
// todos (inclusive as linhas antigas, sem ambiente); com ambiente, só as gravadas nele.
func EventosDaSessaoFiltrados(r repo.Repositorio, dia, amb, conector string) ([]map[string]any, error) {
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
		con, inferido := conectorDaSessao(e)
		if (amb != "" && e.Ambiente != amb) || (conector != "" && con != conector) {
			continue
		}
		x := map[string]any{"em": emBR(e.Em), "tipo": e.Tipo, "texto": e.Texto, "ambiente": nuloSeVazio(e.Ambiente),
			"conector": con, "canal": nuloSeVazio(e.Canal)}
		if inferido {
			x["inferido"] = true
		}
		lista = append(lista, x)
	}
	return lista, nil
}

// HistoricoOrdens: GET /historico/ordens (§3.3), as ordens do dia como gravadas, com a última
// mensagem de cada uma. uid 0 e módulo/conector vazios = todos.
func HistoricoOrdens(r repo.Repositorio, dia, amb, conector string, uid int, modulo string) ([]map[string]any, error) {
	if _, _, err := Dia(dia); err != nil {
		return nil, err
	}
	ops, ords, err := r.CarregarDia(dia)
	if err != nil {
		return nil, err
	}
	type chave struct {
		mod string
		op  int64
	}
	donas := map[chave]dominio.Operacao{}
	for _, o := range ops {
		if dominio.AmbienteDe(o.Ambiente) != amb || (conector != "" && o.Conector != conector) || (uid != 0 && o.UID != uid) ||
			(modulo != "" && o.Modulo != modulo) {
			continue
		}
		donas[chave{o.Modulo, o.OperacaoID}] = o
	}
	var sel []dominio.Ordem
	var ids []int64
	for _, o := range ords {
		if _, ok := donas[chave{o.Modulo, o.OperacaoID}]; ok {
			sel = append(sel, o)
			ids = append(ids, o.ID)
		}
	}
	evs, err := r.EventosFIXDasOrdens(ids)
	if err != nil {
		return nil, err
	}
	ultima := map[int64]repo.EventoFIX{}
	for _, e := range evs {
		if u, ok := ultima[e.OrdemID]; !ok || e.Em.After(u.Em) || (e.Em.Equal(u.Em) && e.ID > u.ID) {
			ultima[e.OrdemID] = e
		}
	}
	sort.SliceStable(sel, func(i, j int) bool { return sel[i].CriadaEm.Before(sel[j].CriadaEm) })
	lista := []map[string]any{}
	for _, o := range sel {
		op := donas[chave{o.Modulo, o.OperacaoID}]
		x := map[string]any{"operacao_id": o.OperacaoID, "modulo": o.Modulo, "uid": o.UID, "conta": o.Conta, "conector": op.Conector,
			"papel": o.Papel.String(), "clordid": o.ClOrdAtual, "clordid_base": o.ClOrdBase, "orderid37": nuloSeVazio(o.OrderID),
			"estrategia": o.Estrategia, "lado": o.Lado.Letra(), "qtd": o.Qtd, "preco": o.Preco, "preco_stop": o.Stop,
			"preco_stop_limite": o.StopLim, "estado": string(o.Estado), "qtd_exec": o.QtdExec, "preco_medio": o.PrecoMedio,
			"motivo_rejeicao": nuloSeVazio(o.MotivoRejeicao), "criada_em": emBR(o.CriadaEm), "ultima_mensagem": nil}
		if e, ok := ultima[o.ID]; ok {
			x["ultima_mensagem"] = map[string]any{"em": emBR(e.Em), "direcao": e.Direcao, "msgtype": e.MsgType,
				"exectype": nuloSeVazio(e.ExecType), "ordstatus": nuloSeVazio(e.OrdStatus), "texto": nuloSeVazio(e.Texto)}
		}
		lista = append(lista, x)
	}
	return lista, nil
}

// HistoricoFio: GET /historico/fio (§3.5), o fio do dia gravado: o PnT com o fix_raw (senha
// mascarada), a Flexscan no formato das linhas do tráfego. seq = o id do evento; ultimo_seq é o
// desde da próxima página. A linha do POST/PUT/DELETE da Flexscan junta o pedido e a resposta (dois
// eventos): a página termina antes de um pedido ainda sem resposta, que volta na próxima.
func HistoricoFio(r repo.Repositorio, dia string, f repo.FiltroFio) (Resposta, error) {
	de, ate, err := Dia(dia)
	if err != nil {
		return nil, err
	}
	evs, err := r.EventosFio(de, ate, f)
	if err != nil {
		return nil, err
	}
	lim := f.Limite
	if lim <= 0 || lim > repo.LimiteFioMax {
		lim = repo.LimiteFioPadrao
	}
	mais := len(evs) >= lim
	type chave struct{ metodo, cl string }
	pedidoFlx := func(e repo.EventoFIX) (chave, bool) {
		t := strings.TrimPrefix(e.MsgType, "FLX-")
		return chave{t, e.ClOrdID}, e.Direcao == "ENV" && (t == "POST" || t == "PUT" || t == "DELETE")
	}
	respostaFlx := func(e repo.EventoFIX) (chave, bool) {
		t := strings.TrimPrefix(e.MsgType, "FLX-")
		metodo, _, _ := strings.Cut(e.Texto, " ")
		ok := e.Direcao == "REC" && t != "WH" && t != "PRECO" && t != "GET" && (metodo == "POST" || metodo == "PUT" || metodo == "DELETE")
		return chave{metodo, e.ClOrdID}, ok
	}
	// o corte: o primeiro pedido sem resposta nesta página (se não for o primeiro evento de uma
	// página cheia, que nunca andaria: esse fica de fora, sem linha)
	corte := int64(0)
	abertos := map[chave]repo.EventoFIX{}
	for _, e := range evs {
		if k, ok := pedidoFlx(e); ok && strings.HasPrefix(e.MsgType, "FLX-") {
			abertos[k] = e
		} else if k, ok := respostaFlx(e); ok && strings.HasPrefix(e.MsgType, "FLX-") {
			delete(abertos, k)
		}
	}
	for _, e := range abertos {
		if corte == 0 || e.ID < corte {
			corte = e.ID
		}
	}
	if corte != 0 && len(evs) > 0 && corte == evs[0].ID && mais {
		corte = 0
	}
	l := []any{}
	ultimo := f.DesdeID
	pend := map[chave]repo.EventoFIX{}
	for _, e := range evs {
		if corte != 0 && e.ID >= corte {
			break
		}
		ultimo = e.ID
		if !strings.HasPrefix(e.MsgType, "FLX-") {
			m := mensagem(e, true)
			m["seq"], m["conector"] = e.ID, dominio.ConectorPnT
			l = append(l, m)
			continue
		}
		lote := []repo.EventoFIX{e}
		if k, ok := pedidoFlx(e); ok {
			pend[k] = e
			continue
		}
		if k, ok := respostaFlx(e); ok {
			if p, achou := pend[k]; achou {
				lote = []repo.EventoFIX{p, e}
				delete(pend, k)
			}
		}
		for _, x := range flexscan.LinhasDoRegistro(lote) {
			x.Seq = e.ID
			l = append(l, map[string]any{"seq": e.ID, "conector": dominio.ConectorFlexscan, "canal": nuloSeVazio(e.Canal), "linha": x})
		}
	}
	return Resposta{"dia": dia, "ambiente": f.Ambiente, "conector": nuloSeVazio(f.Conector), "mensagens": l,
		"ultimo_seq": ultimo, "mais": mais || corte != 0}, nil
}

// TrafegoFlexscanDoDiaSemInstancia (§3.5): o tráfego do dia do registro, com a instância desligada
// (a corretora é o nome gravado no canal; vazio = a ponte única).
func TrafegoFlexscanDoDiaSemInstancia(r repo.Repositorio, amb, corretora, dia string) (map[string]any, error) {
	return trafegoDoDia(r, amb, strings.ToUpper(strings.TrimSpace(corretora)), dia)
}

func trafegoDoDia(r repo.Repositorio, amb, canal, dia string) (map[string]any, error) {
	de, ate, err := Dia(dia)
	if err != nil {
		return nil, err
	}
	evs, err := r.EventosFlexscanEntre(de, ate, LimiteEventosDiaFlexscan, amb, canal)
	if err != nil {
		return nil, err
	}
	ops, _, err := r.CarregarDia(dia)
	if err != nil {
		return nil, err
	}
	donos := map[string]int{}
	for _, o := range ops {
		donos[o.Modulo+"/"+strconv.FormatInt(o.OperacaoID, 10)] = o.UID
	}
	ls := flexscan.LinhasDoRegistro(evs)
	for i := range ls {
		ls[i].Seq = int64(i + 1)
	}
	preencherOperacao(ls, func(mod string, op int64) (int, bool) {
		uid, ok := donos[mod+"/"+strconv.FormatInt(op, 10)]
		return uid, ok
	})
	if ls == nil {
		ls = []flexscan.LinhaTrafego{}
	}
	return map[string]any{"dia": dia, "mensagens": ls, "ultimo_seq": len(ls), "eventos": len(evs),
		"cortado": len(evs) >= LimiteEventosDiaFlexscan}, nil
}

// EstadoInstancia: LIGADA ou DESLIGADA (o ambiente nesta máquina).
func (s *Servico) EstadoInstancia(amb string) string {
	if s.motores[amb] != nil {
		return "LIGADA"
	}
	return "DESLIGADA"
}

// PadraoHistorico: o ambiente das rotas de histórico sem ?ambiente= (o principal).
func (s *Servico) PadraoHistorico() string { return s.principal.amb }
