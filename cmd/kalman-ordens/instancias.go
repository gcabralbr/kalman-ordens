package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/alerta"
	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/flexscan"
	"gammaquant.com.br/kalman-ordens/internal/motor"
	"gammaquant.com.br/kalman-ordens/internal/preco"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/pnt"
)

// SPEC-AMBIENTES-v1 §2 (v1.26): uma instância do motor por ambiente permitido, cada uma com os seus
// conectores (a visão da configuração: no REAL, as chaves REAL_). Na equivalência ORDENS_AMBIENTE,
// uma instância só, com a configuração inteira (como antes).

// instancia: o motor de um ambiente e os conectores dele.
type instancia struct {
	amb     string
	cfg     config.Config // a visão do ambiente
	m       *motor.Motor
	ponte   *flexscan.Ponte
	flx     flexscan.Config
	webhook bool
	sessao  *pnt.Sessao
	fix     string // host:porta, ou "desligado"
}

func (in *instancia) resumo() string {
	flx := "sem"
	if in.ponte != nil {
		flx = in.flx.URL
	}
	return fmt.Sprintf("%s(flexscan=%s fix=%s)", in.amb, flx, in.fix)
}

type instancias struct {
	lista      []*instancia
	servico    *motor.Servico
	receptores rotasWebhook
	fontes     []*preco.HTTP
}

// rotasWebhook: o listener único do webhook, uma rota por ambiente (pelo prefixo do caminho; o
// segredo dentro dele identifica o ambiente).
type rotasWebhook []rotaWebhook

type rotaWebhook struct {
	prefixo string
	h       http.Handler
}

func (rs rotasWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, x := range rs {
		if strings.HasPrefix(r.URL.Path, x.prefixo) {
			x.h.ServeHTTP(w, r)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

// nomeChave: o nome da chave como o operador a escreve no service.env (REAL_ no REAL fora da
// equivalência), para as mensagens de erro.
func nomeChave(amb string, legado bool, k string) string {
	if amb == dominio.AmbienteReal && !legado {
		return "REAL_" + k
	}
	return k
}

// planejarInstancias: as instâncias e a visão de cada uma, com a configuração dos conectores
// conferida (antes de abrir o banco: o serviço mal configurado não sobe).
func planejarInstancias(cfg config.Config, amb config.Ambientes) ([]*instancia, error) {
	legado := amb.Legado != ""
	var lista []*instancia
	if legado {
		a := dominio.AmbienteSimulador
		if amb.Legado == config.LegadoProducao {
			a = dominio.AmbienteReal
		}
		lista = []*instancia{{amb: a, cfg: cfg}}
	} else {
		for _, a := range amb.Lista() {
			v, avisos := cfg.Visao(amb, a)
			for _, x := range avisos {
				slog.Warn("configuração", "ambiente", a, "aviso", x)
			}
			lista = append(lista, &instancia{amb: a, cfg: v})
		}
	}
	for _, in := range lista {
		c := in.cfg
		k := func(x string) string { return nomeChave(in.amb, legado, x) }
		pntDesligado := c.Bool("PNT_DESLIGADO", false)
		flxURL := c.Texto("FLEXSCAN_URL", "")
		if pntDesligado && flxURL == "" {
			if legado {
				// PNT_DESLIGADO (28/10) na equivalência: só a Flexscan
				return nil, fmt.Errorf("PNT_DESLIGADO: configuração ausente: [FLEXSCAN_URL]")
			}
			slog.Warn("ambiente sem conector: as entradas são recusadas (só o paper do GQ vale nele)", "ambiente", in.amb)
		}
		if !pntDesligado {
			quer := []string{"FIX_SENDER_COMP_ID", "FIX_HOST", "FIX_PORTA"}
			if legado {
				quer = append(quer, "ORDENS_CONTAS_HML") // a trava de antes (v1.2)
			}
			var faltam []string
			for _, x := range quer {
				if c.Texto(x, "") == "" {
					faltam = append(faltam, k(x))
				}
			}
			if len(faltam) > 0 {
				return nil, fmt.Errorf("configuração ausente: %v", faltam)
			}
		}
		if flxURL != "" {
			if c.Texto("FLEXSCAN_USUARIO", "") == "" || c.Texto("FLEXSCAN_SENHA", "") == "" {
				return nil, fmt.Errorf("%s sem %s/%s", k("FLEXSCAN_URL"), k("FLEXSCAN_USUARIO"), k("FLEXSCAN_SENHA"))
			}
			if _, err := flexscan.AgendaDe(c); err != nil {
				return nil, err // v1.28: a janela da Flexscan com formato errado não sobe
			}
		}
	}
	return lista, nil
}

// montarInstancias: os motores, as pontes, as sessões FIX e as fontes de preço das instâncias.
func montarInstancias(lista []*instancia, cfg config.Config, amb config.Ambientes, rel relogio.Relogio, r repo.Repositorio, tg alerta.Notificador) (*instancias, error) {
	out := &instancias{lista: lista}
	legado := amb.Legado != ""
	// as fontes de preço com a mesma URL são uma só (o mercado é um só, §2.1)
	fontes := map[string]*preco.HTTP{}
	fonte := func(url, saude string) *preco.HTTP {
		k := url + "|" + saude
		if f := fontes[k]; f != nil {
			return f
		}
		f := preco.NovaHTTP(url, cfg.Texto("PRECOS_ATIVO", "WINFUT"), rel)
		f.ComSaude(saude)
		f.Iniciar(200 * time.Millisecond)
		fontes[k] = f
		out.fontes = append(out.fontes, f)
		return f
	}
	prefixos := map[string]string{}
	var ms []*motor.Motor
	for _, in := range out.lista {
		c := in.cfg
		k := func(x string) string { return nomeChave(in.amb, legado, x) }
		pntDesligado := c.Bool("PNT_DESLIGADO", false)
		flxURL := c.Texto("FLEXSCAN_URL", "")
		// fonte de preço por conector (SPEC-FLEXSCAN-v1): PRECOS_URL é o padrão; PRECOS_URL_<CONECTOR>
		// só onde difere (HML: PnT → 9096, o book D-1 da HML do PnT; Flexscan → 9090, o D0 do simulador)
		urlPnT := c.Texto("PRECOS_URL_PNT", c.Texto("PRECOS_URL", "http://172.16.110.51:9090"))
		saudePnT := c.Texto("PRECOS_SAUDE_URL_PNT", c.Texto("PRECOS_SAUDE_URL", "")) // HML: o /saude do pnt-md-hml (motivo do FONTE_DEGRADADA)
		if pntDesligado {
			// a fonte única é a da Flexscan (o FONTE_DEGRADADA e o MERCADO_TRAVADO são dela)
			urlPnT = c.Texto("PRECOS_URL_FLEXSCAN", c.Texto("PRECOS_URL", "http://172.16.110.51:9090"))
			saudePnT = c.Texto("PRECOS_SAUDE_URL_FLEXSCAN", "")
		}
		in.m = motor.NovoNoAmbiente(in.amb, c, rel, r, fonte(urlPnT, saudePnT), tg)
		in.m.DefinirURLsPrecos(urlPnT, "")
		if flxURL != "" {
			in.flx = flexscan.ConfigDe(c)
			if u := c.Texto("PRECOS_URL_FLEXSCAN", c.Texto("PRECOS_URL", "http://172.16.110.51:9090")); u != urlPnT {
				in.m.UsarPrecosFlexscan(fonte(u, c.Texto("PRECOS_SAUDE_URL_FLEXSCAN", "")))
				in.m.DefinirURLsPrecos(urlPnT, u)
			}
			in.ponte = flexscan.Nova(in.flx, in.m.OuvinteFlexscan(), rel)
			in.m.UsarFlexscan(in.ponte)
			if c.Texto("FLEXSCAN_WEBHOOK_ENDERECO", "") != "" {
				prefixo := c.Texto("FLEXSCAN_WEBHOOK_PREFIXO", "/flexscan/webhook/")
				for outro, p := range prefixos {
					if strings.HasPrefix(prefixo, p) || strings.HasPrefix(p, prefixo) {
						return nil, fmt.Errorf("%s %q e o webhook do %s (%q): um prefixo por ambiente, sem um conter o outro", k("FLEXSCAN_WEBHOOK_PREFIXO"), prefixo, outro, p)
					}
				}
				rec, err := flexscan.NovoReceptor(in.ponte, prefixo, c.Texto("FLEXSCAN_WEBHOOK_SEGREDO", ""))
				if err != nil {
					return nil, fmt.Errorf("webhook do %s: %w", in.amb, err)
				}
				prefixos[in.amb] = prefixo
				out.receptores = append(out.receptores, rotaWebhook{prefixo, rec})
				in.webhook = true
			} else {
				slog.Warn("flexscan sem receptor de webhook (FLEXSCAN_WEBHOOK_ENDERECO vazio): só a consulta", "ambiente", in.amb)
			}
		}
		in.fix = "desligado"
		if !pntDesligado {
			s, err := pnt.NovaSessao(pnt.ConfigSessao{
				SenderCompID: c.Texto("FIX_SENDER_COMP_ID", ""),
				TargetCompID: c.Texto("FIX_TARGET_COMP_ID", "PNT_DSA"),
				Host:         c.Texto("FIX_HOST", ""),
				Porta:        c.Inteiro("FIX_PORTA", 0),
				Usuario:      c.Texto("FIX_USUARIO", ""),
				Senha:        c.Texto("FIX_SENHA", ""),
				Dir:          c.Texto("FIX_DIR", "/var/lib/kalman-ordens/fix"),
				Inicio:       c.Texto("FIX_INICIO", "08:30:00"),
				Fim:          c.Texto("FIX_FIM", "17:55:00"),
				HeartBtInt:   c.Inteiro("FIX_HEARTBEAT", 30),
				ReconexaoS:   c.Inteiro("FIX_RECONEXAO_S", 5),
				// espera crescente depois de logon recusado ou store ilegível (SPEC v1.3 B1, D1)
				ReconexaoMaxS: c.Inteiro("FIX_RECONEXAO_MAX_S", 60),
				// envio liberado só depois do reenvio que o PnT pede logo após o logon (v1.3 A2)
				SincroniaMs:  c.Inteiro("FIX_SINCRONIA_MS", 500),
				ResetOnLogon: c.Bool("FIX_RESET_ON_LOGON", false),
				// fsync do store FIX a cada mensagem (padrão: sim); ver docs/CARGA-2026-10-15.md seção 7
				SemSyncStore: !c.Bool("FIX_STORE_SYNC", true),
				// monitor ao vivo (GET /sessao/ao-vivo): cada mensagem do fio, em memória
				Espelho: in.m.AoVivo().Registrar,
			}, in.m)
			if err != nil {
				return nil, err
			}
			in.m.Usar(s)
			in.sessao, in.fix = s, c.Texto("FIX_HOST", "")+":"+c.Texto("FIX_PORTA", "")
		} else if legado || in.amb == dominio.AmbienteReal {
			slog.Warn("PNT_DESLIGADO: sem sessão FIX nem fonte do PnT; entradas de uid com conector PNT recusadas (CONECTOR_DESLIGADO)", "ambiente", in.amb, "fonte", urlPnT)
		}
		ms = append(ms, in.m)
	}
	if len(out.receptores) > 0 {
		if err := ipExplicito(cfg.Texto("FLEXSCAN_WEBHOOK_ENDERECO", "")); err != nil {
			return nil, err
		}
	}
	out.servico = motor.NovoServico(ms...)
	return out, nil
}
