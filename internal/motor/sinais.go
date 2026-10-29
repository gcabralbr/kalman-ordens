package motor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// POST /sinais (SPEC v1.10 §6): as entradas ou as saídas de um sinal do gestor em lote, em até
// SINAL_MAX_PARTES partes de até SINAL_MAX_ITENS_PARTE itens. Cada item é validado na chegada da
// parte como no POST /ordens; o envio espera o sinal fechar (todas as partes, ou
// SINAL_ESPERA_PARTES_MS depois da primeira) e sai em ordem rotativa: aceitos por uid (empate:
// operacao_id), começando no deslocamento ⌊k·φ·N⌋ mod N, k = o contador de sinais do módulo.

// RotaSinais: a rota no histórico de pedidos (pedidos[].rota), exatamente — o admin do Kalman
// compara com este texto.
const RotaSinais = "POST /sinais"

// CriterioRotativo: a regra de ordem da rajada.
const CriterioRotativo = "ROTATIVA_AUREA"

const razaoAurea = 0.6180339887

// guardaSinais: por quanto tempo um sinal fechado responde ao reenvio de uma parte (repetido).
const guardaSinais = time.Hour

// esperaMaxPartesNaFila: com parte do sinal ainda na fila do motor, o prazo das partes espera por
// ela até aqui (motor muito atrasado).
const esperaMaxPartesNaFila = 30 * time.Second

// PedidoSinal é o corpo do POST /sinais. Os itens são lidos um a um (campo desconhecido num item
// recusa só o item).
type PedidoSinal struct {
	SinalID   string            `json:"sinal_id"`
	Modulo    string            `json:"modulo,omitempty"`
	TsDecisao string            `json:"ts_decisao"`
	Parte     *int              `json:"parte"`
	Partes    *int              `json:"partes"`
	Entradas  []json.RawMessage `json:"entradas"`
	Saidas    []json.RawMessage `json:"saidas"`
	// Ambiente (SPEC-AMBIENTES-v1 §7.2): o de todos os itens; um POST por ambiente
	Ambiente string `json:"ambiente,omitempty"`
}

// ambienteChave: o ambiente da chave do sinal, (modulo, ambiente, sinal_id): o mesmo sinal_id pode
// ir nos dois ambientes. Sem o campo, o padrão da transição; inválido, como veio (os itens recusam).
func (p PedidoSinal) ambienteChave(padrao string) string {
	if p.Ambiente == "" {
		return padrao
	}
	if a, err := dominio.LerAmbiente(p.Ambiente); err == nil {
		return a
	}
	return p.Ambiente
}

func (p PedidoSinal) modulo() string {
	if p.Modulo == "" {
		return dominio.ModuloPadrao
	}
	return p.Modulo
}

// itemEntrada / itemSaida: os campos do POST /ordens do papel, sem papel, modulo e ts_decisao.
type itemEntrada struct {
	OperacaoID *int64 `json:"operacao_id"`
	UID        *int   `json:"uid"`
	Lado       string `json:"lado"`
	Qtd        *int   `json:"qtd"`
	StopPts    *int64 `json:"stop_pts"`
	Motivo     string `json:"motivo"`
}

type itemSaida struct {
	OperacaoID *int64 `json:"operacao_id"`
	UID        *int   `json:"uid"`
	Lado       string `json:"lado"`
	Qtd        *int   `json:"qtd"`
	Motivo     string `json:"motivo"`
}

var reSinalID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

type chaveSinal struct{ modulo, ambiente, id string }

// sinal: o estado de um sinal_id no motor (em memória; o que fica no banco é a rajada de cada
// operação e o contador k).
type sinal struct {
	chave     chaveSinal
	partes    int
	recebidas map[int]*parteSinal
	completas int // partes já tratadas por inteiro
	itens     int
	primeira  time.Time // relógio do motor
	fechado   bool
	fechadoEm time.Time
	leitura   [2]*preco.Leitura // uma leitura de preço para o sinal, por fonte (PnT, Flexscan própria)
	entradas  []*itemAdiado
	saidas    []*itemAdiado
	k         int64
	deslE     int
	deslS     int
	nE, nS    int // entradas e saídas na rajada (depois do fechamento)
}

type parteSinal struct {
	impressao string
	resposta  Resposta         // nil: em curso
	esperando []func(Resposta) // reenvios chegados durante o tratamento
}

// itemAdiado: entrada ou saída aceita, esperando o sinal fechar para sair na ordem da rajada.
type itemAdiado struct {
	op     *operacao
	enviar func()
	vivo   func() bool // false: cancelada antes do fechamento (fica fora da rajada)
}

// SinalAssincrono enfileira a parte (com saída: P1; só entradas: P2); a resposta chega no canal
// depois do commit e do envio. Até ser respondida, a parte conta como "na fila" do sinal: ele não
// fecha pelo prazo com parte dele esperando o motor.
func (m *Motor) SinalAssincrono(p PedidoSinal) <-chan Resposta {
	ch := make(chan Resposta, 1)
	origem, rotulo := "http_entrada", "SINAL_ENTRADA"
	if len(p.Saidas) > 0 {
		origem, rotulo = "http_saida", "SINAL_SAIDA"
	}
	k := chaveSinal{p.modulo(), p.ambienteChave(m.p.Amb.Padrao), p.SinalID}
	m.partesNaFila(k, 1)
	t0 := time.Now()
	m.entrar(origem, func() {
		m.pedidoEm, m.pedidoRotulo = t0, rotulo
		defer func() { m.pedidoEm = timeZero }()
		m.pedirSinal(p, origem, func(r Resposta) {
			m.partesNaFila(k, -1)
			m.responder(func() { ch <- r })
		})
	})
	return ch
}

// Sinal: SinalAssincrono e espera (testes).
func (m *Motor) Sinal(p PedidoSinal) Resposta { return <-m.SinalAssincrono(p) }

func (m *Motor) partesNaFila(k chaveSinal, d int) {
	m.muSinaisFila.Lock()
	defer m.muSinaisFila.Unlock()
	if m.sinaisFila[k] += d; m.sinaisFila[k] <= 0 {
		delete(m.sinaisFila, k)
	}
}

func (m *Motor) temParteNaFila(k chaveSinal) bool {
	m.muSinaisFila.Lock()
	defer m.muSinaisFila.Unlock()
	return m.sinaisFila[k] > 0
}

func recusaLote(p PedidoSinal, codigo, motivo string) Resposta {
	metricas.SinaisPartes.Inc(codigo)
	return Resposta{"sinal_id": p.SinalID, "parte": p.Parte, "partes": p.Partes, "aceito": false, "codigo": codigo, "motivo": motivo}
}

// impressaoSinal: a parte canônica (itens compactados, "modulo":"KALMAN" = sem modulo), para
// comparar reenvios.
func impressaoSinal(p PedidoSinal) string {
	if p.Modulo == dominio.ModuloPadrao {
		p.Modulo = ""
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// tamanhoFatia: itens tratados por vez; entre uma fatia e a seguinte o motor atende o resto das
// filas (fills e 1014 da P0 não esperam uma parte de 1.000 inteira).
const tamanhoFatia = 250

// parteEmCurso: a parte sendo tratada, fatia a fatia.
type parteEmCurso struct {
	p      PedidoSinal
	s      *sinal
	origem string
	raws   []json.RawMessage
	peds   []Pedido
	errs   []error
	i      int
	lista  []resultadoItem
	fim    func(Resposta)
}

type resultadoItem struct {
	itemRegistro
	op *operacao
}

func copiaRepetida(r Resposta) Resposta {
	c := Resposta{}
	for x, v := range r {
		c[x] = v
	}
	c["repetido"] = true
	return c
}

func (m *Motor) pedirSinal(p PedidoSinal, origem string, fim func(Resposta)) {
	switch {
	case !reSinalID.MatchString(p.SinalID):
		fim(recusaLote(p, "CAMPO_INVALIDO", "sinal_id ausente ou invalido (1 a 64 de A-Z a-z 0-9 . _ : -)"))
		return
	case p.Modulo != "" && !dominio.ModuloValido(p.Modulo):
		fim(recusaLote(p, "CAMPO_INVALIDO", fmt.Sprintf("modulo %q invalido (2 a 8 letras maiusculas e digitos)", p.Modulo)))
		return
	case p.Parte == nil || p.Partes == nil:
		fim(recusaLote(p, "CAMPO_INVALIDO", "parte e partes obrigatorios"))
		return
	case *p.Partes < 1:
		fim(recusaLote(p, "CAMPO_INVALIDO", "partes deve ser de 1 a "+strconv.Itoa(m.p.SinalMaxPartes)))
		return
	case *p.Partes > m.p.SinalMaxPartes:
		fim(recusaLote(p, "LIMITE_LOTE", fmt.Sprintf("%d partes; maximo %d", *p.Partes, m.p.SinalMaxPartes)))
		return
	case *p.Parte < 1 || *p.Parte > *p.Partes:
		fim(recusaLote(p, "CAMPO_INVALIDO", fmt.Sprintf("parte %d fora de 1..%d", *p.Parte, *p.Partes)))
		return
	case len(p.Entradas)+len(p.Saidas) == 0:
		fim(recusaLote(p, "CAMPO_INVALIDO", "entradas e saidas vazias"))
		return
	case len(p.Entradas)+len(p.Saidas) > m.p.SinalMaxItensParte:
		fim(recusaLote(p, "LIMITE_LOTE", fmt.Sprintf("%d itens na parte; maximo %d", len(p.Entradas)+len(p.Saidas), m.p.SinalMaxItensParte)))
		return
	}
	k := chaveSinal{p.modulo(), p.ambienteChave(m.p.Amb.Padrao), p.SinalID}
	imp := impressaoSinal(p)
	s := m.sinais[k]
	if s != nil {
		if r := s.recebidas[*p.Parte]; r != nil {
			switch {
			case r.impressao != imp:
				m.al.Emitir("DUPLICADA_DIFERENTE", "", fmt.Sprintf("parte %d do sinal %s repetida com conteudo diferente", *p.Parte, p.SinalID), 0)
				fim(recusaLote(p, "DUPLICADA", "parte ja recebida com outro conteudo"))
			case r.resposta == nil:
				// a original ainda está sendo tratada: o reenvio recebe a resposta dela
				r.esperando = append(r.esperando, fim)
			default:
				metricas.SinaisPartes.Inc("REPETIDA")
				fim(copiaRepetida(r.resposta))
			}
			return
		}
		if s.fechado {
			fim(recusaLote(p, "SINAL_FECHADO", "o sinal ja fechou (todas as partes, ou o prazo depois da primeira); parte nao aceita"))
			return
		}
		if s.partes != *p.Partes {
			fim(recusaLote(p, "CAMPO_INVALIDO", fmt.Sprintf("partes = %d, mas a primeira parte do sinal disse %d", *p.Partes, s.partes)))
			return
		}
		if s.itens+len(p.Entradas)+len(p.Saidas) > m.p.SinalMaxItensParte*m.p.SinalMaxPartes {
			fim(recusaLote(p, "LIMITE_LOTE", fmt.Sprintf("mais de %d itens no sinal", m.p.SinalMaxItensParte*m.p.SinalMaxPartes)))
			return
		}
	} else {
		s = &sinal{chave: k, partes: *p.Partes, recebidas: map[int]*parteSinal{}, primeira: m.agora()}
		m.sinais[k] = s
	}
	s.itens += len(p.Entradas) + len(p.Saidas)
	s.recebidas[*p.Parte] = &parteSinal{impressao: imp} // em curso (resposta nil)

	pc := &parteEmCurso{p: p, s: s, origem: origem, raws: append(append([]json.RawMessage{}, p.Entradas...), p.Saidas...), fim: fim}
	for i, raw := range pc.raws {
		papel := "ENTRADA"
		if i >= len(p.Entradas) {
			papel = "SAIDA"
		}
		ped, err := pedidoDoItem(papel, raw, p)
		pc.peds, pc.errs = append(pc.peds, ped), append(pc.errs, err)
	}
	m.continuarParte(pc)
}

// continuarParte: uma fatia de itens, como o POST /ordens (a entrada e a saída aceitas esperam o
// fechamento); a próxima fatia volta para a fila do motor.
func (m *Motor) continuarParte(pc *parteEmCurso) {
	p, s := pc.p, pc.s
	ate := min(pc.i+tamanhoFatia, len(pc.raws))
	var ids []int64
	for j := pc.i; j < ate; j++ {
		if pc.errs[j] == nil && pc.peds[j].Papel == "ENTRADA" && pc.peds[j].OperacaoID != nil {
			ids = append(ids, *pc.peds[j].OperacaoID)
		}
	}
	m.cache = m.novoCacheFatia(p.modulo(), ids)
	if !s.fechado {
		m.sinalAtual = s
	}
	rp := m.respostaAtual
	m.respostaAtual = nil
	for j := pc.i; j < ate; j++ {
		ped, papel := pc.peds[j], pc.peds[j].Papel
		var r Resposta
		if pc.errs[j] != nil {
			r = m.recusar(ped, simples("CAMPO_INVALIDO", pc.errs[j].Error()))
		} else {
			r = m.pedir(ped)
		}
		item := Resposta{"operacao_id": nil, "papel": papel, "aceito": r["aceito"] == true, "codigo": r["codigo"], "motivo": r["motivo"],
			"posicao": nil, "tamanho": nil}
		if ped.OperacaoID != nil {
			item["operacao_id"] = *ped.OperacaoID
		}
		for _, c := range []string{"clordid", "preco", "ja_em_saida", "origem", "repetido"} {
			if v, ok := r[c]; ok {
				item[c] = v
			}
		}
		resultado := "recusado"
		switch {
		case r["repetido"] == true:
			resultado = "repetido"
		case r["aceito"] == true:
			resultado = "aceito"
		}
		metricas.SinaisItens.Inc(papel, resultado)
		pc.lista = append(pc.lista, resultadoItem{itemRegistro{raw: pc.raws[j], res: item, uid: valorPtr(ped.UID), opid: valorPtr(ped.OperacaoID)}, m.operacaoDo(ped)})
	}
	m.sinalAtual, m.cache = nil, nil
	m.respostaAtual = rp
	pc.i = ate
	if pc.i < len(pc.raws) {
		m.entrar(pc.origem, func() { m.continuarParte(pc) })
		return
	}
	m.concluirParte(pc)
}

// concluirParte: a parte inteira tratada — fecha o sinal se era a última e responde (também aos
// reenvios que chegaram durante o tratamento).
func (m *Motor) concluirParte(pc *parteEmCurso) {
	p, s := pc.p, pc.s
	s.completas++
	fechouAgora := false
	if s.completas == s.partes && !s.fechado {
		m.fecharSinal(s, "TODAS_AS_PARTES")
		fechouAgora = true
	}
	aceitos := 0
	resultados := make([]Resposta, 0, len(pc.lista))
	regs := make([]itemRegistro, 0, len(pc.lista))
	for _, x := range pc.lista {
		if x.res["aceito"] == true {
			aceitos++
		}
		if fechouAgora && x.op != nil && x.res["aceito"] == true && x.res["repetido"] != true {
			raj := x.op.SinalEntrada
			if x.res["papel"] == "SAIDA" {
				raj = x.op.SinalSaida
			}
			if raj.SinalID == p.SinalID && raj.Posicao > 0 {
				x.res["posicao"], x.res["tamanho"] = raj.Posicao, raj.Tamanho
			}
		}
		resultados = append(resultados, x.res)
		regs = append(regs, x.itemRegistro)
	}
	resp := Resposta{"sinal_id": p.SinalID, "parte": *p.Parte, "partes": *p.Partes, "aceito": true, "codigo": "LOTE_ACEITO",
		"motivo": fmt.Sprintf("%d de %d itens aceitos", aceitos, len(pc.lista)), "fechado": fechouAgora,
		"criterio": CriterioRotativo, "k": nil, "deslocamento": nil, "resultados": resultados}
	if fechouAgora && s.k > 0 {
		resp["k"] = s.k
		if s.nE > 0 {
			resp["deslocamento"] = s.deslE
			if s.nS > 0 {
				resp["deslocamento_saidas"] = s.deslS
			}
		} else {
			resp["deslocamento"] = s.deslS
		}
	}
	ps := s.recebidas[*p.Parte]
	ps.resposta = resp
	metricas.SinaisPartes.Inc("LOTE_ACEITO")
	m.registrarItensSinal(p, regs)
	slog.Info("sinal: parte tratada", "sinal", p.SinalID, "modulo", s.chave.modulo, "parte", *p.Parte, "partes", *p.Partes,
		"itens", len(pc.lista), "aceitos", aceitos, "fechou", fechouAgora)
	pc.fim(resp)
	for _, f := range ps.esperando {
		metricas.SinaisPartes.Inc("REPETIDA")
		f(copiaRepetida(resp))
	}
	ps.esperando = nil
}

// cacheFatia: o que a entrada confere varrendo as operações do dia (posição e entradas pendentes,
// ordem sem confirmação, o preço do resultado do dia) e o DUPLICADA de outro dia, calculados uma
// vez por fatia (dentro dela só as entradas aceitas mudam algo, e atualizam o cache).
type cacheFatia struct {
	modulo         string
	existentes     map[int64]bool // nil: consulta item a item (a consulta da fatia falhou)
	semConfirmacao bool
	comprometido   int
	porUID         map[int]int // calculado na primeira entrada com limite do uid
	preco          precoDia
	precoOK        bool
	precoLido      bool
}

func (m *Motor) novoCacheFatia(modulo string, ids []int64) *cacheFatia {
	c := &cacheFatia{modulo: modulo, semConfirmacao: m.haSemConfirmacao(), comprometido: m.comprometidoTotal()}
	if len(ids) > 0 {
		if ex, err := m.repo.OperacoesExistentes(modulo, ids); err == nil {
			c.existentes = ex
		} else {
			slog.Warn("sinal: consulta das operações existentes falhou; uma a uma", "erro", err)
		}
	} else {
		c.existentes = map[int64]bool{}
	}
	return c
}

type itemRegistro struct {
	raw  json.RawMessage
	res  Resposta
	uid  int
	opid int64
}

func valorPtr[T int | int64](p *T) T {
	if p == nil {
		return 0
	}
	return *p
}

// pedidoDoItem: o item como um Pedido do POST /ordens (papel da lista, modulo e ts_decisao do
// sinal). Campo desconhecido ou de tipo errado → erro (CAMPO_INVALIDO só no item); o Pedido volta
// com o que deu para ler (operacao_id e uid para a resposta e o registro).
func pedidoDoItem(papel string, raw json.RawMessage, s PedidoSinal) (Pedido, error) {
	p := Pedido{Modulo: s.Modulo, Papel: papel, TsDecisao: s.TsDecisao, Ambiente: s.Ambiente}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if papel == "ENTRADA" {
		var it itemEntrada
		err := dec.Decode(&it)
		p.OperacaoID, p.UID, p.Lado, p.Qtd, p.StopPts, p.Motivo = it.OperacaoID, it.UID, it.Lado, it.Qtd, it.StopPts, it.Motivo
		if err != nil {
			var meio itemEntrada
			_ = json.Unmarshal(raw, &meio)
			p.OperacaoID, p.UID = meio.OperacaoID, meio.UID
			return p, fmt.Errorf("item de entrada: %v", err)
		}
		return p, nil
	}
	var it itemSaida
	err := dec.Decode(&it)
	p.OperacaoID, p.UID, p.Lado, p.Qtd, p.Motivo = it.OperacaoID, it.UID, it.Lado, it.Qtd, it.Motivo
	if err != nil {
		var meio itemSaida
		_ = json.Unmarshal(raw, &meio)
		p.OperacaoID, p.UID = meio.OperacaoID, meio.UID
		return p, fmt.Errorf("item de saida: %v", err)
	}
	return p, nil
}

// adiarEntrada / adiarSaida: chamados por pedirEntrada / pedirSaida quando o pedido veio de um
// sinal — a ordem (ou o início da saída) espera o fechamento.
func (m *Motor) adiarEntrada(op *operacao, o *ordem, enviar func()) {
	s := m.sinalAtual
	op.SinalEntrada = dominio.Rajada{SinalID: s.chave.id, Criterio: CriterioRotativo}
	o.adiada = true
	s.entradas = append(s.entradas, &itemAdiado{op: op, vivo: func() bool { return o.adiada }, enviar: func() {
		o.adiada = false
		enviar()
	}})
	m.salvarOp(op)
}

func (m *Motor) adiarSaida(op *operacao, iniciar func()) {
	s := m.sinalAtual
	op.SinalSaida = dominio.Rajada{SinalID: s.chave.id, Criterio: CriterioRotativo}
	s.saidas = append(s.saidas, &itemAdiado{op: op, vivo: func() bool { return true }, enviar: func() {
		antes := len(m.v.envios)
		iniciar()
		// SAIDA_ENVIADA: quando a primeira mensagem da saída sai (o cancel da 1014, ou a 1031)
		for i := antes; i < len(m.v.envios); i++ {
			e := &m.v.envios[i]
			if e.o == nil || m.donoDa[e.o] != op {
				continue
			}
			antigo, cl := e.aoEnviar, e.clordid
			e.aoEnviar = func() {
				if antigo != nil {
					antigo()
				}
				m.publicarEnviada("SAIDA_ENVIADA", op, op.SinalSaida, cl)
			}
			break
		}
	}})
	m.salvarOp(op)
}

// publicarEnviada: ENTRADA_ENVIADA / SAIDA_ENVIADA (v1.10 §6.4), com a posição na rajada (nula
// no pedido avulso).
func (m *Motor) publicarEnviada(tipo string, op *operacao, r dominio.Rajada, clordid string) {
	d := map[string]any{"sinal_id": nil, "posicao": nil, "tamanho": nil, "clordid": clordid, "enviada_em": emBR(m.agora())}
	if !r.Vazia() {
		d["sinal_id"] = r.SinalID
		if r.Posicao > 0 {
			d["posicao"], d["tamanho"] = r.Posicao, r.Tamanho
		}
	}
	m.publicar(tipo, op, d)
}

// fecharSinal: ordena as entradas e as saídas aceitas, gira pelo deslocamento áureo e manda
// (entradas pelo balde da entrada; saídas na fila de saída). A validade na fila da entrada conta
// daqui.
func (m *Motor) fecharSinal(s *sinal, como string) {
	if s.fechado {
		return
	}
	s.fechado, s.fechadoEm = true, m.agora()
	metricas.SinaisFechados.Inc(como)
	// a entrada cancelada enquanto o sinal estava aberto (saída, parada, admin) já encerrou: fora
	vivos := func(l []*itemAdiado) []*itemAdiado {
		var r []*itemAdiado
		for _, it := range l {
			if it.vivo() {
				r = append(r, it)
			}
		}
		return r
	}
	s.entradas, s.saidas = vivos(s.entradas), vivos(s.saidas)
	if len(s.entradas)+len(s.saidas) == 0 {
		slog.Info("sinal fechado sem itens aceitos", "sinal", s.chave.id, "como", como)
		return
	}
	s.k = m.proximoK(s.chave.modulo)
	metricas.SinalTamanho.Observar(float64(len(s.entradas) + len(s.saidas)))
	s.deslE = m.rajada(s, s.entradas, func(op *operacao) *dominio.Rajada { return &op.SinalEntrada })
	s.deslS = m.rajada(s, s.saidas, func(op *operacao) *dominio.Rajada { return &op.SinalSaida })
	s.nE, s.nS = len(s.entradas), len(s.saidas)
	s.entradas, s.saidas = nil, nil // o sinal fica em memória só para o reenvio (respostas)
	slog.Info("sinal fechado", "sinal", s.chave.id, "modulo", s.chave.modulo, "como", como, "k", s.k,
		"entradas", s.nE, "deslocamento", s.deslE, "saidas", s.nS, "deslocamento_saidas", s.deslS)
}

// rajada: ordem por uid (empate: operacao_id), começando no deslocamento; posição 1..N.
func (m *Motor) rajada(s *sinal, itens []*itemAdiado, campo func(*operacao) *dominio.Rajada) int {
	n := len(itens)
	if n == 0 {
		return 0
	}
	sort.SliceStable(itens, func(i, j int) bool {
		a, b := itens[i].op, itens[j].op
		if a.UID != b.UID {
			return a.UID < b.UID
		}
		return a.OperacaoID < b.OperacaoID
	})
	desl := Deslocamento(s.k, n)
	for i := 0; i < n; i++ {
		it := itens[(desl+i)%n]
		r := campo(it.op)
		*r = dominio.Rajada{SinalID: s.chave.id, K: s.k, Posicao: i + 1, Tamanho: n, Deslocamento: desl, Criterio: CriterioRotativo}
		m.salvarOp(it.op)
		it.enviar()
	}
	return desl
}

// Deslocamento: ⌊k·φ·N⌋ mod N (φ = 0,6180339887).
func Deslocamento(k int64, n int) int {
	if n <= 0 {
		return 0
	}
	return int(int64(math.Floor(float64(k)*razaoAurea*float64(n))) % int64(n))
}

// proximoK: o contador de sinais do módulo, persistente entre dias (kalmanordemestado
// sinal_k:<modulo>); sobe a cada sinal fechado com algum item aceito.
func (m *Motor) proximoK(modulo string) int64 {
	chave := "sinal_k:" + modulo
	k, ok := m.sinalK[modulo]
	if !ok {
		if v, achou, err := m.repo.LerEstado(chave); err == nil && achou {
			k, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	k++
	m.sinalK[modulo] = k
	m.gravarEstado(chave, strconv.FormatInt(k, 10))
	return k
}

// tickSinais: fecha o sinal cujas partes não chegaram todas em SINAL_ESPERA_PARTES_MS; esquece os
// fechados há mais de uma hora.
func (m *Motor) tickSinais() {
	agora := m.agora()
	for k, s := range m.sinais {
		switch {
		case !s.fechado && agora.Sub(s.primeira) >= m.p.SinalEsperaPartes &&
			(!m.temParteNaFila(k) || agora.Sub(s.primeira) >= esperaMaxPartesNaFila):
			slog.Warn("sinal fechado pelo prazo, sem todas as partes", "sinal", s.chave.id, "partes", s.partes, "recebidas", len(s.recebidas))
			m.fecharSinal(s, "PRAZO")
		case s.fechado && agora.Sub(s.fechadoEm) > guardaSinais:
			delete(m.sinais, k)
		}
	}
}

// registrarItensSinal: cada item da parte no histórico de pedidos da operação (rota "POST /sinais"),
// com o corpo reduzido ao item (sinal_id, parte, partes, ts_decisao e a lista com ele só) e a
// resposta do item.
func (m *Motor) registrarItensSinal(p PedidoSinal, itens []itemRegistro) {
	agora := m.agora()
	dia := agora.In(relogio.Fuso).Format("2006-01-02")
	var ps []repo.PedidoHTTP
	for _, it := range itens {
		papel, _ := it.res["papel"].(string)
		corpo := map[string]any{"sinal_id": p.SinalID, "ts_decisao": p.TsDecisao, "parte": p.Parte, "partes": p.Partes,
			"entradas": []json.RawMessage{}, "saidas": []json.RawMessage{}}
		if p.Modulo != "" {
			corpo["modulo"] = p.Modulo
		}
		if papel == "SAIDA" {
			corpo["saidas"] = []json.RawMessage{it.raw}
		} else {
			corpo["entradas"] = []json.RawMessage{it.raw}
		}
		resp := Resposta{"sinal_id": p.SinalID, "parte": p.Parte, "partes": p.Partes}
		for k, v := range it.res {
			resp[k] = v
		}
		bc, _ := json.Marshal(corpo)
		br, _ := json.Marshal(resp)
		codigo, _ := it.res["codigo"].(string)
		ps = append(ps, repo.PedidoHTTP{Data: dia, Modulo: p.Modulo, OperacaoID: it.opid, UID: it.uid, Rota: RotaSinais, Papel: papel,
			HTTPStatus: http.StatusOK, Aceito: it.res["aceito"] == true, Codigo: codigo, Corpo: string(bc), Resposta: string(br), Em: agora})
	}
	m.v.muPedidos.Lock()
	m.v.pedidos = append(m.v.pedidos, ps...)
	m.v.muPedidos.Unlock()
}
