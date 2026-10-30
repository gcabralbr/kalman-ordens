package motor

import (
	"sync"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

// Servico (SPEC-AMBIENTES-v1 §2, v1.26): as instâncias do motor, uma por ambiente, atrás da mesma
// API. Cada instância tem os seus conectores, posição, parada, conciliação e limites; elas
// compartilham o processo, o banco (com o contador de IDs) e a fonte de preço.
//
// O roteamento:
//   - a entrada e o sinal vão à instância do ambiente do pedido (sem o campo, o AMBIENTE_PADRAO);
//     ambiente inválido, ausente ou sem instância vai à principal, que recusa com o código do §4.1;
//   - a entrada de uma operação já recebida vai à instância dona (AMBIENTE_DIVERGENTE, se o ambiente
//     for outro);
//   - a saída, o stop, a consulta e as ações do admin vão à instância dona da operação (vale o
//     ambiente gravado; um pedido com outro alerta AMBIENTE_PEDIDO_DIVERGENTE);
//   - a parada e a retomada sem ambiente valem para todas.
//
// Com uma instância só (a .53, os roteiros de sempre), tudo vai a ela, como antes.
type Servico struct {
	motores   map[string]*Motor
	lista     []*Motor // SIMULADOR antes do REAL
	principal *Motor   // o SIMULADOR, se houver: os campos de hoje do /saude, o banco, os recusados
	donos     *sync.Map
}

// NovoServico junta as instâncias (não inicia); no máximo uma por ambiente.
func NovoServico(ms ...*Motor) *Servico {
	s := &Servico{motores: map[string]*Motor{}, donos: &sync.Map{}}
	ids := &contadorIDs{}
	for _, amb := range []string{dominio.AmbienteSimulador, dominio.AmbienteReal} {
		for _, m := range ms {
			if m.amb != amb {
				continue
			}
			if s.motores[amb] != nil {
				panic("motor: duas instâncias do " + amb)
			}
			m.ids, m.donos = ids, s.donos
			s.motores[amb] = m
			s.lista = append(s.lista, m)
		}
	}
	s.principal = s.lista[0]
	if len(s.lista) > 1 {
		metricas.DefinirFilas(func() [3]int {
			var r [3]int
			for _, m := range s.lista {
				r[0], r[1], r[2] = r[0]+len(m.p0), r[1]+len(m.p1), r[2]+len(m.p2)
			}
			return r
		}, len(s.lista)*(capFilaP0+capFilaP1+capFilaP2))
	}
	return s
}

// Motores: as instâncias, SIMULADOR antes do REAL.
func (s *Servico) Motores() []*Motor { return s.lista }

// Do: a instância do ambiente (nil = sem instância).
func (s *Servico) Do(amb string) *Motor { return s.motores[amb] }

// Iniciar carrega o dia de cada instância e as põe a correr.
func (s *Servico) Iniciar() error {
	for _, m := range s.lista {
		if err := m.Iniciar(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Servico) Parar() {
	for _, m := range s.lista {
		m.Parar()
	}
}

func (s *Servico) Tick() {
	for _, m := range s.lista {
		m.Tick()
	}
}

// doAmbiente: a instância do ambiente pedido (o campo, ou o AMBIENTE_PADRAO); sem instância ou
// inválido, a principal (que recusa com o código e o alerta do §4.1).
func (s *Servico) doAmbiente(bruto string) *Motor {
	if len(s.lista) == 1 {
		return s.principal
	}
	if bruto == "" {
		bruto = s.principal.p.Amb.Padrao
	}
	if amb, err := dominio.LerAmbiente(bruto); err == nil && s.motores[amb] != nil {
		return s.motores[amb]
	}
	return s.principal
}

// dono: a instância da operação; desconhecida, a principal (que responde "não existe").
func (s *Servico) dono(modulo string, id int64) *Motor {
	if len(s.lista) == 1 {
		return s.principal
	}
	if modulo == "" {
		modulo = dominio.ModuloPadrao
	}
	if v, ok := s.donos.Load(chaveOperacao{modulo, id}); ok {
		if m := s.motores[v.(string)]; m != nil {
			return m
		}
	}
	return s.principal
}

// PedirAssincrono: POST /ordens.
func (s *Servico) PedirAssincrono(p Pedido) <-chan Resposta {
	if p.Papel != "ENTRADA" && p.OperacaoID != nil {
		return s.dono(p.Modulo, *p.OperacaoID).PedirAssincrono(p)
	}
	m := s.doAmbiente(p.Ambiente)
	if p.OperacaoID != nil && s.temDono(p.Modulo, *p.OperacaoID) {
		m = s.dono(p.Modulo, *p.OperacaoID) // a entrada repetida: a dona responde (AMBIENTE_DIVERGENTE)
	}
	return m.PedirAssincrono(p)
}

func (s *Servico) temDono(modulo string, id int64) bool {
	if modulo == "" {
		modulo = dominio.ModuloPadrao
	}
	_, ok := s.donos.Load(chaveOperacao{modulo, id})
	return ok
}

// SinalAssincrono: POST /sinais (um POST por ambiente, §7.2).
func (s *Servico) SinalAssincrono(p PedidoSinal) <-chan Resposta {
	return s.doAmbiente(p.Ambiente).SinalAssincrono(p)
}

// AjustarStopAssincrono: POST /operacoes/{id}/stop.
func (s *Servico) AjustarStopAssincrono(id int64, a AjusteStop) <-chan Resposta {
	return s.dono(a.Modulo, id).AjustarStopAssincrono(id, a)
}

// Consultar: GET /ordens/{id}.
func (s *Servico) Consultar(modulo string, id int64) Resposta {
	return s.dono(modulo, id).Consultar(modulo, id)
}

// AcaoOperacaoAssincrona: encerrar e cancelar-entrada do admin.
func (s *Servico) AcaoOperacaoAssincrona(acao, modulo string, id int64, a AcaoAdmin) <-chan Resposta {
	return s.dono(modulo, id).AcaoOperacaoAssincrona(acao, modulo, id, a)
}

// Eventos: GET /eventos (do banco: os dois ambientes, cada evento com o seu).
func (s *Servico) Eventos(desde int64, limite int, modulo string) ([]repo.Publicado, error) {
	return s.principal.Eventos(desde, limite, modulo)
}

// Repo: o banco (comum às instâncias).
func (s *Servico) Repo() repo.Repositorio { return s.principal.Repo() }

// RegistrarPedidoHTTP: o pedido como chegou (kalmanordempedido, comum às instâncias).
func (s *Servico) RegistrarPedidoHTTP(rota, modulo string, operacaoID int64, uid int, papel string, corpo []byte, status int, resposta Resposta) {
	m := s.principal
	if operacaoID != 0 {
		m = s.dono(modulo, operacaoID)
	}
	m.RegistrarPedidoHTTP(rota, modulo, operacaoID, uid, papel, corpo, status, resposta)
}

// alvos: as instâncias de um pedido com ambiente opcional (vazio = todas). ok falso: ambiente
// inválido ou sem instância.
func (s *Servico) alvos(bruto string) ([]*Motor, bool) {
	if bruto == "" {
		return s.lista, true
	}
	amb, err := dominio.LerAmbiente(bruto)
	if err != nil || s.motores[amb] == nil {
		return nil, false
	}
	return []*Motor{s.motores[amb]}, true
}

func semInstancia(bruto string) Resposta {
	return Resposta{"aceito": false, "codigo": "AMBIENTE_NAO_PERMITIDO", "motivo": "ambiente " + bruto + " sem instancia nesta maquina"}
}

// porAmbiente: com uma instância, a resposta dela (como antes); com mais, a da primeira com o bloco
// "ambientes" de todas, e aceito só se todas aceitaram.
func (s *Servico) porAmbiente(ms []*Motor, f func(*Motor) Resposta) Resposta {
	if len(ms) == 1 && len(s.lista) == 1 {
		return f(ms[0])
	}
	var r Resposta
	todas := map[string]any{}
	aceito := true
	for _, m := range ms {
		x := f(m)
		x["ambiente"] = m.amb
		todas[m.amb] = x
		if x["aceito"] != true {
			aceito = false
		}
		if r == nil {
			r = Resposta{}
			for k, v := range x {
				r[k] = v
			}
		}
	}
	r["aceito"], r["ambientes"] = aceito, todas
	return r
}

// AtivarParada: POST /parada; ambiente vazio = todas as instâncias (§2.2).
func (s *Servico) AtivarParada(amb, modo, origem, motivo string, por int) Resposta {
	ms, ok := s.alvos(amb)
	if !ok {
		return semInstancia(amb)
	}
	return s.porAmbiente(ms, func(m *Motor) Resposta { return m.AtivarParada(modo, origem, motivo, por) })
}

// Retomar: POST /retomar; ambiente vazio = todas.
func (s *Servico) Retomar(amb, origem, motivo string, por int) Resposta {
	ms, ok := s.alvos(amb)
	if !ok {
		return semInstancia(amb)
	}
	return s.porAmbiente(ms, func(m *Motor) Resposta { return m.Retomar(origem, motivo, por) })
}

// AlterarHabilitacaoAssincrono: a instância do ambiente (o cache dela); sem instância, a principal
// grava (a habilitação REAL pode ser preparada numa máquina sem o REAL).
func (s *Servico) AlterarHabilitacaoAssincrono(uid int, amb string, a HabilitacaoAdmin) <-chan Resposta {
	m := s.motores[dominio.AmbienteDe(amb)]
	if m == nil {
		m = s.principal
	}
	return m.AlterarHabilitacaoAssincrono(uid, amb, a)
}

// DestinoDoUsuario: GET /usuarios/{uid}/destino; cada ambiente pela sua instância (o conector
// configurado nela, §4.1 item 7), ou pela principal se não houver (o teto recusa).
func (s *Servico) DestinoDoUsuario(uid int, ambientes []string) (Resposta, error) {
	r := map[string]any{}
	for _, amb := range ambientes {
		m := s.motores[amb]
		if m == nil {
			m = s.principal
		}
		x, err := m.DestinoDoUsuario(uid, []string{amb})
		if err != nil {
			return nil, err
		}
		r[amb] = x["ambientes"].(map[string]any)[amb]
	}
	return Resposta{"uid": uid, "ambientes": r}, nil
}

// comPnT: a instância com a sessão FIX (o REAL; na equivalência HML, o SIMULADOR).
func (s *Servico) comPnT(amb string) (*Motor, bool) {
	if amb != "" {
		ms, ok := s.alvos(amb)
		if !ok {
			return nil, false
		}
		return ms[0], true
	}
	for _, m := range s.lista {
		if m.fix != nil {
			return m, true
		}
	}
	return s.principal, true
}

// ConsultarPnTAssincrono: GET /pnt/ordens[?ambiente=].
func (s *Servico) ConsultarPnTAssincrono(amb string) <-chan Resposta {
	m, ok := s.comPnT(amb)
	if !ok {
		ch := make(chan Resposta, 1)
		ch <- Resposta{"erro": "AMBIENTE_NAO_PERMITIDO", "motivo": "ambiente " + amb + " sem instancia nesta maquina"}
		return ch
	}
	return m.ConsultarPnTAssincrono()
}

// ConsultarAoVivo: GET /sessao/ao-vivo[?ambiente=]; sem o parâmetro, a instância com a sessão FIX.
func (s *Servico) ConsultarAoVivo(amb string, desde int64, limite int) (map[string]any, bool) {
	m, ok := s.comPnT(amb)
	if !ok {
		return nil, false
	}
	r := m.ConsultarAoVivo(desde, limite)
	r["ambiente"] = m.amb
	return r, true
}

// flexscanDo: GET /flexscan/trafego[?ambiente=]; sem o parâmetro, o SIMULADOR (como antes).
func (s *Servico) flexscanDo(amb string) (*Motor, bool) {
	if amb == "" {
		return s.principal, true
	}
	ms, ok := s.alvos(amb)
	if !ok {
		return nil, false
	}
	return ms[0], true
}

// TrafegoFlexscan: o buffer da ponte do ambiente. ok falso: sem instância ou sem Flexscan.
// v1.30: ?corretora= escolhe a ponte no modo por corretora (erro: corretora inválida, 400).
func (s *Servico) TrafegoFlexscan(amb, corretora string, desde int64, limite int) (map[string]any, bool, error) {
	m, ok := s.flexscanDo(amb)
	if !ok {
		return nil, false, nil
	}
	r, ok, err := m.TrafegoFlexscan(corretora, desde, limite)
	if ok && err == nil {
		r["ambiente"] = m.amb
	}
	return r, ok, err
}

// TrafegoFlexscanDoDia: o dia inteiro, do registro do ambiente.
func (s *Servico) TrafegoFlexscanDoDia(amb, corretora, dia string) (map[string]any, bool, error) {
	m, ok := s.flexscanDo(amb)
	if !ok {
		return nil, false, nil
	}
	r, ok, err := m.TrafegoFlexscanDoDia(corretora, dia)
	if ok && err == nil {
		r["ambiente"] = m.amb
	}
	return r, ok, err
}

// BancadaPosicoes: a bancada da HML (só na instância com o PnT; recusada com o REAL permitido).
func (s *Servico) BancadaPosicoes(conta string, espera time.Duration) Resposta {
	m, _ := s.comPnT("")
	return m.BancadaPosicoes(conta, espera)
}

// Saude: GET /saude. Os campos de hoje são os da principal (o SIMULADOR) até o Kalman migrar
// (§2.3); "ambientes" traz o bloco de cada instância.
func (s *Servico) Saude() Resposta {
	r := s.principal.Saude()
	amb := map[string]any{}
	for _, m := range s.lista {
		x := r
		if m != s.principal {
			x = m.Saude()
		}
		b := Resposta{}
		for k, v := range x {
			if k != "versao" && k != "ambientes_permitidos" && k != "ambiente" {
				b[k] = v
			}
		}
		amb[m.amb] = b
	}
	r["ambientes"] = amb
	return r
}
