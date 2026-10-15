// Package dominio tem os tipos persistidos do serviço: operação real e ordem.
package dominio

import (
	"regexp"
	"time"

	"github.com/shopspring/decimal"
)

// Papel da ordem na operação.
type Papel byte

const (
	Entrada  Papel = 'E'
	Protecao Papel = 'P'
	Saida    Papel = 'S'
)

func (p Papel) String() string { return string(p) }

// Lado: +1 compra, -1 venda.
type Lado int8

const (
	Compra Lado = 1
	Venda  Lado = -1
)

// FIX devolve o 54.
func (l Lado) FIX() byte {
	if l == Compra {
		return '1'
	}
	return '2'
}

// Letra devolve C ou V.
func (l Lado) Letra() string {
	if l == Compra {
		return "C"
	}
	return "V"
}

// Oposto.
func (l Lado) Oposto() Lado { return -l }

// LadoDaLetra lê C/V.
func LadoDaLetra(s string) (Lado, bool) {
	switch s {
	case "C":
		return Compra, true
	case "V":
		return Venda, true
	}
	return 0, false
}

// ParaZerar devolve o lado que reduz uma posição com esse sinal.
func ParaZerar(posicao int) Lado {
	if posicao > 0 {
		return Venda
	}
	return Compra
}

// EstadoOrdem.
type EstadoOrdem string

const (
	Enviando       EstadoOrdem = "ENVIANDO"
	SemConfirmacao EstadoOrdem = "SEM_CONFIRMACAO"
	Pendente       EstadoOrdem = "PENDENTE" // 39=A
	Ativa          EstadoOrdem = "ATIVA"
	Parcial        EstadoOrdem = "PARCIAL"
	Executada      EstadoOrdem = "EXECUTADA"
	Cancelada      EstadoOrdem = "CANCELADA"
	Rejeitada      EstadoOrdem = "REJEITADA"
	Expirada       EstadoOrdem = "EXPIRADA"
	FalhaEnvio     EstadoOrdem = "FALHA_ENVIO"
	Desconhecida   EstadoOrdem = "DESCONHECIDA"
)

// Final: o PnT não vai mais mexer nela (salvo fill tardio).
func (e EstadoOrdem) Final() bool {
	switch e {
	case Executada, Cancelada, Rejeitada, Expirada, FalhaEnvio:
		return true
	}
	return false
}

// EstadoOperacao (Parte 4).
type EstadoOperacao string

const (
	Entrando               EstadoOperacao = "ENTRANDO"
	PosicionadaSemProtecao EstadoOperacao = "POSICIONADA_SEM_PROTECAO"
	Protegida              EstadoOperacao = "PROTEGIDA"
	CancelandoProtecao     EstadoOperacao = "CANCELANDO_PROTECAO"
	AguardandoStop         EstadoOperacao = "AGUARDANDO_STOP"
	Saindo                 EstadoOperacao = "SAINDO"
	Zerada                 EstadoOperacao = "ZERADA"
	EncerradaSemEntrada    EstadoOperacao = "ENCERRADA_SEM_ENTRADA"
)

func (e EstadoOperacao) Final() bool { return e == Zerada || e == EncerradaSemEntrada }
func (e EstadoOperacao) Saindo() bool {
	return e == CancelandoProtecao || e == AguardandoStop || e == Saindo
}

// Ordem é uma ordem no PnT (um NewOrderSingle; replace e cancel mudam a mesma).
type Ordem struct {
	ID             int64
	Data           string
	Modulo         string // o da operação
	UID            int
	OperacaoID     int64
	Papel          Papel
	Conta          string // tag 1 e 116: a da operação, congelada na entrada (v1.2 U4)
	Simbolo        string // 55: o da operação
	ClOrdBase      string
	ClOrdAtual     string
	OrderID        string // 37
	SecID          string // 198
	Estrategia     int
	Lado           Lado
	Qtd            int
	Preco          int64
	Stop           int64
	StopLim        int64
	Estado         EstadoOrdem
	QtdExec        int // maior CumQty aplicado
	PrecoMedio     decimal.Decimal
	MotivoGestor   string
	MotivoRejeicao string
	CriadaEm       time.Time
}

// Operacao é a operação real (uma por operacao_id do gestor).
type Operacao struct {
	ID         int64
	Data       string
	Modulo     string // módulo do GQ dono da operação (v1.3 M1); a chave é (Modulo, OperacaoID)
	OperacaoID int64
	UID        int
	// Congelados na entrada (v1.2 U4, S1): proteção e saída usam estes, nunca releem.
	Conta     string // Robo.CCONroConta do ambiente
	Destino   int    // Robo.CORSeqId (85)
	Simbolo   string // contrato vigente na entrada
	Lado      Lado   // da entrada
	QtdPedida int
	StopPts   int64
	// StopAjustado: o gatilho da 1014 pedido pelo gestor (v1.4 K1; 0 = sem ajuste, vale StopPts
	// sobre o médio). O último pedido vale; o que está no PnT é o Stop da 1014.
	StopAjustado    int64
	Estado          EstadoOperacao
	MotivoEntrada   string
	TsDecisao       string
	MotivoSaida     string
	OrigemSaida     string
	SaidaPedidaEm   time.Time
	Divergente      bool
	PnL             decimal.Decimal
	EncerrouEntrada bool
	CriadaEm        time.Time
	ContadorN       map[string]int // último n do ClOrdID, por papel
	// Idempotência do POST /ordens: pedido e resposta originais (JSON).
	PedidoEntrada, RespostaEntrada, PedidoSaida, RespostaSaida string
}

// Copia devolve uma cópia profunda.
func (o Operacao) Copia() Operacao {
	c := o
	c.ContadorN = map[string]int{}
	for k, v := range o.ContadorN {
		c.ContadorN[k] = v
	}
	return c
}

// Habilitacao: quem pode mandar ordem real (v1.2 U5). Alterada só pela CLI.
// PosicaoMax e QtdMaxOrdem: limites do uid (0 = só o global); valem junto com o global.
type Habilitacao struct {
	UID         int
	Habilitado  bool
	PosicaoMax  int
	QtdMaxOrdem int
	Por         string // quem alterou (usuário do sistema que rodou a CLI)
	Motivo      string
	Em          time.Time
}

// ModuloPadrao: o módulo de quem não diz qual é (o Kalman, o primeiro cliente do serviço).
const ModuloPadrao = "KALMAN"

var reModulo = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,7}$`)

// ModuloValido: 2 a 8 letras maiúsculas e dígitos, começando por letra. O nome entra no ClOrdID
// dos módulos que não são o KALMAN (KM-<MODULO>-E-<operacao_id>-<n>), que tem de caber em 28
// caracteres [PnT-4].
func ModuloValido(m string) bool { return reModulo.MatchString(m) }
