package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/relogio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
)

type semNotificar struct{}

func (semNotificar) Notificar(string, string) {}

// v1.26 (SPEC-AMBIENTES-v1 §10): com SIM,REAL, uma instância por ambiente, cada uma com a sua
// Flexscan; o webhook é um listener só, com a rota de cada ambiente pelo prefixo. As chaves do
// conector do REAL são as REAL_ (faltando, o erro diz o nome que o operador escreve).
func TestInstanciasDosDoisAmbientes(t *testing.T) {
	segSim, segReal := strings.Repeat("s", 40), strings.Repeat("r", 40)
	base := map[string]string{"AMBIENTES_PERMITIDOS": "SIM,REAL", "REAL_UIDS": "5725", "REAL_CONTAS": "715124",
		"REAL_LIMITE_QTD_ORDEM": "1", "REAL_LIMITE_PERDA_DIA_PTS": "300", "REAL_LIMITE_ENTRADAS_DIA": "10",
		"FLEXSCAN_URL": "http://127.0.0.1:1/sim", "FLEXSCAN_USUARIO": "u", "FLEXSCAN_SENHA": "p", "FLEXSCAN_WEBHOOK_SEGREDO": segSim,
		"REAL_FLEXSCAN_URL": "http://127.0.0.1:1/real", "REAL_FLEXSCAN_USUARIO": "ur", "REAL_FLEXSCAN_SENHA": "pr", "REAL_FLEXSCAN_WEBHOOK_SEGREDO": segReal,
		"FLEXSCAN_WEBHOOK_ENDERECO": "127.0.0.1:9098", "PRECOS_URL": "http://127.0.0.1:1",
		"FLEXSCAN_ESTADO_LOGIN": t.TempDir() + "/sim.json", "REAL_FLEXSCAN_ESTADO_LOGIN": t.TempDir() + "/real.json"}
	montar := func(v map[string]string) (*instancias, error) {
		cfg := config.Novo(v)
		amb, _, err := cfg.LerAmbientes()
		if err != nil {
			return nil, err
		}
		plano, err := planejarInstancias(cfg, amb)
		if err != nil {
			return nil, err
		}
		return montarInstancias(plano, cfg, amb, relogio.Real{}, repo.NovaMemoria(), semNotificar{})
	}
	in, err := montar(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, f := range in.fontes {
			f.Parar()
		}
	}()
	if len(in.lista) != 2 || in.servico.Do(dominio.AmbienteSimulador) == nil || in.servico.Do(dominio.AmbienteReal) == nil {
		t.Fatalf("instâncias: %d", len(in.lista))
	}
	if len(in.fontes) != 1 {
		t.Fatalf("a mesma URL de preço devia ser uma fonte só: %d", len(in.fontes))
	}
	real := in.lista[1]
	if real.amb != dominio.AmbienteReal || real.flx.URL != "http://127.0.0.1:1/real" || real.flx.Usuario != "ur" || real.sessao != nil {
		t.Fatalf("REAL: %+v", real.flx)
	}
	// o webhook: cada caminho na ponte do seu ambiente; fora dos prefixos, 404 sem corpo
	for _, c := range []struct {
		caminho string
		amb     int
	}{{"/flexscan/webhook-real/errado", 1}, {"/flexscan/webhook/errado", 0}} {
		w := httptest.NewRecorder()
		in.receptores.ServeHTTP(w, httptest.NewRequest(http.MethodPost, c.caminho, strings.NewReader("{}")))
		var n [2]int64
		for i, r := range in.receptores {
			for _, x := range r.h.(interface{ Recusas() map[string]int64 }).Recusas() {
				n[i] += x
			}
		}
		if n[c.amb] == 0 {
			t.Fatalf("%s não chegou ao receptor do ambiente %d: %v", c.caminho, c.amb, n)
		}
	}
	w := httptest.NewRecorder()
	in.receptores.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/outro", nil))
	if w.Code != http.StatusNotFound || w.Body.Len() != 0 {
		t.Fatalf("fora dos prefixos: %d %q", w.Code, w.Body.String())
	}

	erros := []struct {
		muda map[string]string
		quer string
	}{
		{map[string]string{"REAL_FLEXSCAN_SENHA": ""}, "REAL_FLEXSCAN_URL sem REAL_FLEXSCAN_USUARIO/REAL_FLEXSCAN_SENHA"},
		{map[string]string{"REAL_FIX_HOST": "h"}, "REAL_FIX_SENDER_COMP_ID"},
		{map[string]string{"REAL_FLEXSCAN_WEBHOOK_PREFIXO": "/flexscan/webhook/real/"}, "um prefixo por ambiente"},
		{map[string]string{"REAL_FLEXSCAN_WEBHOOK_SEGREDO": ""}, "webhook do REAL"},
	}
	for _, c := range erros {
		v := map[string]string{}
		for k, x := range base {
			v[k] = x
		}
		for k, x := range c.muda {
			v[k] = x
		}
		in, err := montar(v)
		if in != nil {
			for _, f := range in.fontes {
				f.Parar()
			}
		}
		if err == nil || !strings.Contains(err.Error(), c.quer) {
			t.Errorf("%v: esperava erro com %q, veio %v", c.muda, c.quer, err)
		}
	}
}
