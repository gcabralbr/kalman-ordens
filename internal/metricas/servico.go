package metricas

import (
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Métricas do serviço (SPEC v1.9 M). Durações em tempo real (não no relógio do motor).
var (
	FilaEspera = NovoHistograma("fila_espera_segundos",
		"Espera na fila do motor: de entrar na fila a começar a ser tratado, por origem (http, fix, tick, outro).", "origem")
	PedidoAteEnvio = NovoHistograma("pedido_ate_envio_segundos",
		"Do pedido HTTP entrar na fila do motor até a primeira mensagem FIX que ele causou (ENTRADA: a 1031; SAIDA: o cancel da 1014 ou a 1031).", "pedido")
	EnvioAte39A     = NovoHistograma("ordem_envio_ate_39a_segundos", "NewOrderSingle enviada até o 39=A (pendente) do PnT.", "papel")
	De39AAte390     = NovoHistograma("ordem_39a_ate_39_0_segundos", "39=A até o 39=0 (aceita) do PnT.", "papel")
	EnvioAteAceite  = NovoHistograma("ordem_envio_ate_aceite_segundos", "NewOrderSingle enviada até o 39=0 (aceita), com ou sem 39=A.", "papel")
	EnvioAteFill    = NovoHistograma("ordem_envio_ate_fill_segundos", "NewOrderSingle enviada até o primeiro fill tratado pelo motor.", "papel")
	FillAteProtecao = NovoHistograma("protecao_fill_ate_envio_segundos",
		"Chegada (à sessão) do fill de entrada até a 1014 enviada: quanto a posição ficou sem proteção pedida.")
	FixMensagens = NovoContador("fix_mensagens_total", "Mensagens FIX de aplicação enviadas e recebidas, por tipo (35).", "direcao", "tipo")
	Banco        = NovoHistograma("banco_segundos", "Latência de cada operação no banco, por operação do repositório.", "operacao")
	BancoErros   = NovoContador("banco_erros_total", "Operações no banco que falharam.", "operacao")
	Recusas      = NovoContador("recusas_total", "Pedidos recusados, por pedido (ENTRADA, SAIDA, STOP...) e código.", "pedido", "codigo")
	Alertas      = NovoContador("alertas_total", "Alertas emitidos, por código.", "codigo")
	HTTP         = NovoHistograma("http_segundos", "Duração das requisições HTTP, por rota e status.", "rota", "status")
	// v1.10
	Lote        = NovoHistograma("lote_segundos", "Gravação do lote de uma volta do motor (uma transação).")
	LoteLinhas  = NovoHistogramaBaldes("lote_linhas", "Linhas gravadas por lote.", []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000})
	EsperaEnvio = NovoHistograma("envio_espera_segundos",
		"Da decisão do motor ao envio ao PnT (commit do lote e, na entrada, o limitador de taxa), por prioridade.", "prioridade")
	EntradasExpiradasFila = NovoContador("entradas_expiradas_fila_total", "Entradas descartadas por passar de ENTRADA_TTL_FILA_S esperando a taxa.")
	// v1.10 §6: POST /sinais
	Disjuntor      = NovoContador("disjuntor_bolsa_total", "Disjuntor de bolsa indisponível: abriu, fechou, sonda.", "evento")
	SinaisPartes   = NovoContador("sinais_partes_total", "Partes de POST /sinais tratadas pelo motor, por código (LOTE_ACEITO, repetida, recusas do lote).", "codigo")
	SinaisItens    = NovoContador("sinais_itens_total", "Itens de POST /sinais, por papel e resultado (aceito, recusado, repetido).", "papel", "resultado")
	SinaisFechados = NovoContador("sinais_fechados_total", "Sinais fechados, por como fecharam (TODAS_AS_PARTES, PRAZO).", "como")
	SinalTamanho   = NovoHistogramaBaldes("sinal_itens_aceitos", "Itens aceitos por sinal fechado (o tamanho da rajada).", []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000})
)

// Estado: medidores escritos pelo motor (tick) e lidos na coleta.
var (
	filaTamanho, filaCapacidade   atomic.Int64
	aguardandoTaxa                atomic.Int64
	lerFilas                      atomic.Pointer[func() [3]int]
	ordensVivas, operacoesAbertas atomic.Int64
	posicaoTotal                  atomic.Int64
	sessaoLogada                  atomic.Int64
	lerFila                       atomic.Pointer[func() int]
)

// DefinirFilas (v1.10): as três filas de entrada do motor (P0, P1, P2); o último motor vale.
func DefinirFilas(tamanhos func() [3]int, capacidade int) {
	lerFilas.Store(&tamanhos)
	filaCapacidade.Store(int64(capacidade))
}

// DefinirAguardandoTaxa: entradas decididas esperando o limitador de taxa.
func DefinirAguardandoTaxa(n int) { aguardandoTaxa.Store(int64(n)) }

// DefinirFila: o motor em execução informa como ler o tamanho da fila (o último vale).
func DefinirFila(tamanho func() int, capacidade int) {
	lerFila.Store(&tamanho)
	filaCapacidade.Store(int64(capacidade))
}

// Estado do motor, atualizado a cada tick.
func DefinirEstado(vivas, abertas, posicao int, logada bool) {
	ordensVivas.Store(int64(vivas))
	operacoesAbertas.Store(int64(abertas))
	posicaoTotal.Store(int64(posicao))
	if logada {
		sessaoLogada.Store(1)
	} else {
		sessaoLogada.Store(0)
	}
}

var inicio = time.Now()

func init() {
	NovoMedidor("fila_tamanho", "Itens nas filas do motor agora (as três).", func() float64 {
		if f := lerFilas.Load(); f != nil {
			t := (*f)()
			return float64(t[0] + t[1] + t[2])
		}
		if f := lerFila.Load(); f != nil {
			return float64((*f)())
		}
		return 0
	})
	for i, nome := range []string{"p0", "p1", "p2"} {
		i := i
		NovoMedidor("fila_tamanho_"+nome, "Itens na fila "+strings.ToUpper(nome)+" do motor (P0: PnT e tick; P1: saídas, ajustes, admin; P2: entradas).", func() float64 {
			if f := lerFilas.Load(); f != nil {
				return float64((*f)()[i])
			}
			return 0
		})
	}
	NovoMedidor("envios_aguardando_taxa", "Entradas decididas esperando o limitador de taxa da sessão.", func() float64 { return float64(aguardandoTaxa.Load()) })
	NovoMedidor("fila_capacidade", "Capacidade da fila do motor (cheia = quem enfileira espera).", func() float64 { return float64(filaCapacidade.Load()) })
	NovoMedidor("ordens_vivas", "Ordens vivas no motor.", func() float64 { return float64(ordensVivas.Load()) })
	NovoMedidor("operacoes_abertas", "Operações do dia não encerradas.", func() float64 { return float64(operacoesAbertas.Load()) })
	NovoMedidor("posicao_total", "Posição somada de todas as operações.", func() float64 { return float64(posicaoTotal.Load()) })
	NovoMedidor("sessao_logada", "1 com a sessão FIX logada.", func() float64 { return float64(sessaoLogada.Load()) })
	NovoMedidor("go_goroutines", "Goroutines do processo.", func() float64 { return float64(runtime.NumGoroutine()) })
	NovoMedidor("memoria_heap_bytes", "Heap em uso (Go).", func() float64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return float64(ms.HeapAlloc)
	})
	NovoMedidor("processo_cpu_segundos", "CPU do processo (usuário + sistema) desde que subiu.", func() float64 {
		var r syscall.Rusage
		if syscall.Getrusage(syscall.RUSAGE_SELF, &r) != nil {
			return 0
		}
		return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
	})
	NovoMedidor("processo_rss_maximo_bytes", "Maior memória residente do processo desde que subiu.", func() float64 {
		var r syscall.Rusage
		if syscall.Getrusage(syscall.RUSAGE_SELF, &r) != nil {
			return 0
		}
		return float64(r.Maxrss) * 1024
	})
	NovoMedidor("processo_no_ar_segundos", "Segundos desde que o processo subiu.", func() float64 { return time.Since(inicio).Seconds() })
}
