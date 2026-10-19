// Package config lê a configuração do serviço (service.env em produção; um mapa nos testes).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Config é um mapa chave → valor com padrões.
type Config struct{ v map[string]string }

// Do mapa.
func Novo(v map[string]string) Config {
	c := Config{v: map[string]string{}}
	for k, x := range v {
		c.v[k] = x
	}
	return c
}

// DoAmbiente lê as variáveis de ambiente.
func DoAmbiente() Config {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return Novo(m)
}

func (c Config) Texto(k, padrao string) string {
	if s, ok := c.v[k]; ok && s != "" {
		return s
	}
	return padrao
}

func (c Config) Inteiro(k string, padrao int) int {
	s := c.Texto(k, "")
	if s == "" {
		return padrao
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		panic(fmt.Sprintf("config %s=%q: %v", k, s, err))
	}
	return n
}

func (c Config) Pontos(k string, padrao int64) int64 { return int64(c.Inteiro(k, int(padrao))) }

// Segundos aceita fração ("0.5").
func (c Config) Segundos(k string, padrao float64) time.Duration {
	s := c.Texto(k, "")
	if s == "" {
		return time.Duration(padrao * float64(time.Second))
	}
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		panic(fmt.Sprintf("config %s=%q: %v", k, s, err))
	}
	return time.Duration(d.Mul(decimal.NewFromInt(int64(time.Second))).IntPart())
}

// Hora HH:MM ou HH:MM:SS, como duração desde meia-noite.
func (c Config) Hora(k, padrao string) time.Duration {
	s := c.Texto(k, padrao)
	p := strings.Split(s, ":")
	if len(p) < 2 {
		panic(fmt.Sprintf("config %s=%q: hora", k, s))
	}
	var d time.Duration
	for i, x := range p {
		n, err := strconv.Atoi(x)
		if err != nil {
			panic(fmt.Sprintf("config %s=%q: hora", k, s))
		}
		d += time.Duration(n) * []time.Duration{time.Hour, time.Minute, time.Second}[i]
	}
	return d
}

func (c Config) Bool(k string, padrao bool) bool {
	switch strings.ToUpper(c.Texto(k, "")) {
	case "Y", "S", "TRUE", "1":
		return true
	case "N", "FALSE", "0":
		return false
	}
	return padrao
}

// Parametros são os valores que o motor usa, já lidos.
type Parametros struct {
	Bolsa, SenderLocationID string
	// v1.2: conta, destino e símbolo vêm da operação. O ambiente escolhe a coluna do GQ
	// (HML: usutxtconfig; PRODUCAO: usutxtconfigprd); ContasHML é a trava cruzada.
	Ambiente     string
	ContasHML    []string
	ConfereConta time.Duration

	ColarEntrada, FolgaStop, ColarSaida, Tick int64
	AjusteDegrauMin                           int64
	TimeoutEntrada, TimeoutSaida, EsperaStop  time.Duration
	EsperaErro, EsperaFillPosRecusa           time.Duration
	PrazoConfirmacao, EsperaEncerramento      time.Duration
	RetentativaProtecao, AlertaSemProtecao    time.Duration
	AlertaPendente, RepeticaoAlerta           time.Duration
	SilencioMassStatus, AlertaSessaoPresa     time.Duration
	IdadeMaxPreco, IdadeMaxPrecoDivergente    time.Duration
	MaxRepreco                                int
	ConsultaPnTEspera, ConsultaPnTIntervalo   time.Duration
	// v1.10: limitador de taxa da sessão (só as entradas esperam) e cache de usuários
	TaxaMsgS  float64
	RajadaMsg int
	// v1.10 P4: reserva da taxa para 1014, cancel e saída (a entrada nunca a esgota) e validade da
	// entrada na fila da taxa
	ReservaSaidaMsgS float64
	TTLFilaEntrada   time.Duration
	// v1.10 C6: espera máxima da conciliação com 911/912 em uso
	EsperaMaxMassStatus time.Duration
	CacheUsuarios       time.Duration
	// v1.10 §6: POST /sinais — fechamento do sinal em partes e limites do lote
	SinalEsperaPartes                  time.Duration
	SinalMaxItensParte, SinalMaxPartes int

	// Limites GLOBAIS (soma de todos os usuários; v1.10: padrões para rajadas de 190 usuários)
	PosicaoMax, QtdMaxOrdem, EntradasPorMinuto, MensagensPorMinuto, EntradasPorDia, RecusasParaParada int
	// Limites POR USUÁRIO (v1.10 L): o laço de reentrada de um uid; a virada de mão (entrada oposta
	// até LIMITE_VIRADA_S depois de um ZERADA por SINAL_OPOSTO) não passa por eles
	EntradasPorMinutoUID       int
	IntervaloUID, JanelaVirada time.Duration
	ReprecoEsgotado            time.Duration // repreço da saída depois de SAIDA_ESGOTOU
	// v1.11: saída por replace da 1014 (gatilho além do preço) em vez de cancel + nova ordem
	SaidaPorReplace     bool
	SaidaReplaceAlemPts int64
	SaidaReplaceTimeout time.Duration
	// v1.13: 20864=Y (IgnoreTunnel) também nas entradas (a 1014 e a saída sempre levam)
	IgnoreTunnelEntrada bool
	// v1.13: MaxReplaces da 1014 (RoE §4.3.7, padrão 100) e a reserva para a saída por replace
	Replaces1014Max, Replaces1014Reserva int
	// v1.10 §9: disjuntor de bolsa indisponível
	DisjuntorTextos                                   []string
	DisjuntorRejeicoes                                int
	DisjuntorJanela, DisjuntorSonda, DisjuntorEstavel time.Duration
	IntervaloEntradas                                 time.Duration
	PerdaDiaPts, BookDescoladoPts                     int64
	JanelaInicio, JanelaFim, HoraZeragem              time.Duration
}

// Parametros lê tudo com os padrões da SPEC (v1 + v1.1).
func (c Config) Parametros() Parametros {
	return Parametros{
		Bolsa:            c.Texto("ORDENS_BOLSA", "BVMF"),
		SenderLocationID: c.Texto("ORDENS_SENDER_LOCATION", ""),
		Ambiente:         c.Texto("ORDENS_AMBIENTE", ""),
		ContasHML:        listaContas(c.Texto("ORDENS_CONTAS_HML", "")),
		ConfereConta:     c.Segundos("CONFERE_CONTA_S", 60),

		ColarEntrada:            c.Pontos("COLAR_ENTRADA_PTS", 15),
		FolgaStop:               c.Pontos("FOLGA_STOP_PTS", 100),
		ColarSaida:              c.Pontos("COLAR_SAIDA_PTS", 50),
		Tick:                    c.Pontos("TICK_PTS", 5),
		AjusteDegrauMin:         c.Pontos("AJUSTE_DEGRAU_MIN_PTS", 25),
		TimeoutEntrada:          c.Segundos("TIMEOUT_ENTRADA_S", 3),
		TimeoutSaida:            c.Segundos("TIMEOUT_SAIDA_S", 2),
		EsperaStop:              c.Segundos("ESPERA_STOP_S", 5),
		EsperaErro:              c.Segundos("ESPERA_ERRO_S", 2),
		EsperaFillPosRecusa:     c.Segundos("ESPERA_FILL_POS_RECUSA_S", 3),
		PrazoConfirmacao:        c.Segundos("PRAZO_CONFIRMACAO_S", 3),
		EsperaEncerramento:      c.Segundos("ESPERA_ENCERRAMENTO_S", 8),
		RetentativaProtecao:     c.Segundos("RETENTATIVA_PROTECAO_S", 1),
		AlertaSemProtecao:       c.Segundos("ALERTA_SEM_PROTECAO_S", 2),
		AlertaPendente:          c.Segundos("ALERTA_PENDENTE_S", 3),
		RepeticaoAlerta:         c.Segundos("ALERTA_REPETICAO_S", 30),
		SilencioMassStatus:      c.Segundos("MASS_STATUS_SILENCIO_S", 3),
		AlertaSessaoPresa:       c.Segundos("SESSAO_PRESA_ALERTA_S", 60),
		IdadeMaxPreco:           c.Segundos("PRECO_IDADE_MAX_S", 3),
		IdadeMaxPrecoDivergente: c.Segundos("IDADE_MAX_PRECO_DIVERGENTE_S", 60),
		MaxRepreco:              c.Inteiro("MAX_REPRECO_SAIDA", 3),
		SaidaPorReplace:         c.Bool("SAIDA_POR_REPLACE", false),
		SaidaReplaceAlemPts:     c.Pontos("SAIDA_REPLACE_ALEM_PTS", 100),
		SaidaReplaceTimeout:     c.Segundos("SAIDA_REPLACE_TIMEOUT_S", 2),
		IgnoreTunnelEntrada:     c.Bool("IGNORE_TUNNEL_ENTRADA", false),
		Replaces1014Max:         c.Inteiro("REPLACES_1014_MAX", 100),
		Replaces1014Reserva:     c.Inteiro("REPLACES_1014_RESERVA", 5),
		ConsultaPnTEspera:       c.Segundos("CONSULTA_PNT_ESPERA_S", 5),
		ConsultaPnTIntervalo:    c.Segundos("CONSULTA_PNT_INTERVALO_S", 10),
		TaxaMsgS:                float64(c.Segundos("FIX_TAXA_MSG_S", 100)) / float64(time.Second),
		RajadaMsg:               c.Inteiro("FIX_RAJADA_MSG", 100),
		ReservaSaidaMsgS:        float64(c.Segundos("FIX_RESERVA_SAIDA_MSG_S", 30)) / float64(time.Second),
		TTLFilaEntrada:          c.Segundos("ENTRADA_TTL_FILA_S", 5),
		EsperaMaxMassStatus:     c.Segundos("MASS_STATUS_ESPERA_MAX_S", 30),
		CacheUsuarios:           c.Segundos("CACHE_USUARIOS_S", 30),
		SinalEsperaPartes:       time.Duration(c.Inteiro("SINAL_ESPERA_PARTES_MS", 500)) * time.Millisecond,
		SinalMaxItensParte:      c.Inteiro("SINAL_MAX_ITENS_PARTE", 1000),
		SinalMaxPartes:          c.Inteiro("SINAL_MAX_PARTES", 10),

		PosicaoMax:         c.Inteiro("LIMITE_POSICAO_MAX", 200),
		QtdMaxOrdem:        c.Inteiro("LIMITE_QTD_ORDEM", 1),
		IntervaloEntradas:  c.Segundos("LIMITE_INTERVALO_ENTRADAS_S", 0),
		EntradasPorMinuto:  c.Inteiro("LIMITE_ENTRADAS_MIN", 400),
		MensagensPorMinuto: c.Inteiro("LIMITE_MENSAGENS_MIN", 3000),
		EntradasPorDia:     c.Inteiro("LIMITE_ENTRADAS_DIA", 3000),
		PerdaDiaPts:        c.Pontos("LIMITE_PERDA_DIA_PTS", 1500),
		BookDescoladoPts:   c.Pontos("LIMITE_BOOK_DESCOLADO_PTS", 150),
		RecusasParaParada:  c.Inteiro("LIMITE_RECUSAS_PARADA", 50),

		EntradasPorMinutoUID: c.Inteiro("LIMITE_ENTRADAS_MIN_UID", 3),
		IntervaloUID:         c.Segundos("LIMITE_INTERVALO_UID_S", 30),
		JanelaVirada:         c.Segundos("LIMITE_VIRADA_S", 10),
		ReprecoEsgotado:      c.Segundos("REPRECO_ESGOTADO_S", 10),
		DisjuntorTextos:      lista(c.Texto("DISJUNTOR_TEXTOS", "Exchange is offline")),
		DisjuntorRejeicoes:   c.Inteiro("DISJUNTOR_REJEICOES", 5),
		DisjuntorJanela:      c.Segundos("DISJUNTOR_JANELA_S", 30),
		DisjuntorSonda:       c.Segundos("DISJUNTOR_SONDA_S", 30),
		DisjuntorEstavel:     c.Segundos("DISJUNTOR_ESTAVEL_S", 60),
		JanelaInicio:         c.Hora("JANELA_INICIO", "09:00:00"),
		JanelaFim:            c.Hora("JANELA_FIM", "14:00:00"),
		HoraZeragem:          c.Hora("HORA_ZERAGEM", "14:30:00"),
	}
}

func listaContas(s string) []string {
	var r []string
	for _, x := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' || c == ';' }) {
		r = append(r, x)
	}
	return r
}

// lista: textos separados por "|" (vazios fora).
func lista(s string) []string {
	var r []string
	for _, x := range strings.Split(s, "|") {
		if x = strings.TrimSpace(x); x != "" {
			r = append(r, x)
		}
	}
	return r
}
