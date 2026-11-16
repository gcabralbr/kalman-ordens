package repo

import (
	"errors"
	"testing"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// v1.35 (SPEC-TECNICA-REAL-PELA-TELA-F1 §1 e §5): a TransicaoReal nos dois backends — sem linha, a
// decisão vê existia=false; gravar=false e erro não gravam nada; cada gravação é uma linha nova
// no histórico com o evento e o estado de antes, e a linha guarda todos os campos da aprovação.
// O SIMULADOR do mesmo uid não é tocado.
func TestTransicaoReal(t *testing.T) {
	for nome, novo := range backends(t) {
		t.Run(nome, func(t *testing.T) {
			r := novo(t)
			em := time.Date(2026, 11, 17, 21, 20, 1, 123e6, time.UTC)
			if err := r.GravarHabilitacao(dominio.Habilitacao{UID: 1450, Ambiente: dominio.AmbienteSimulador, Habilitado: true,
				Conectores: []string{dominio.ConectorFlexscan}, Por: "roteiro", Motivo: "sim", Em: em}); err != nil {
				t.Fatal(err)
			}

			// sem linha, sem gravar
			res, err := r.TransicaoReal(1450, func(antes dominio.Habilitacao, existia bool) (dominio.Habilitacao, string, bool, error) {
				if existia || antes.UID != 1450 || antes.Ambiente != dominio.AmbienteReal || antes.EstadoReal() != dominio.EstadoSemAprovacao {
					t.Fatalf("sem linha: %+v %v", antes, existia)
				}
				return antes, "", false, nil
			})
			if err != nil || res.Existia || res.Gravou {
				t.Fatalf("sem gravar: %+v %v", res, err)
			}
			if _, ok, _ := r.Habilitacao(1450, dominio.AmbienteReal); ok {
				t.Fatal("gravou sem pedir")
			}

			// a aprovação
			aprovado := func(antes dominio.Habilitacao, _ bool) (dominio.Habilitacao, string, bool, error) {
				h := antes
				h.Habilitado, h.PosicaoMax, h.QtdMaxOrdem, h.Conectores = true, 1, 1, []string{dominio.ConectorPnT}
				h.Por, h.Motivo, h.Em = "admin:1450", "fase 1", em
				h.Estado, h.Corretora, h.Conta, h.Versao, h.Origem = dominio.EstadoAprovado, 85, "4582351", antes.Versao+1, dominio.OrigemTela
				h.AprovadoPor, h.AprovadoEm, h.AprovadoMotivo = "admin:1450", em, "fase 1"
				return h, dominio.EventoAprovar, true, nil
			}
			res, err = r.TransicaoReal(1450, aprovado)
			if err != nil || res.Existia || !res.Gravou || res.Depois.Versao != 1 {
				t.Fatalf("aprovar: %+v %v", res, err)
			}
			h, ok, err := r.Habilitacao(1450, dominio.AmbienteReal)
			if err != nil || !ok || !h.Habilitado || h.Estado != dominio.EstadoAprovado || h.Corretora != 85 || h.Conta != "4582351" ||
				h.Versao != 1 || h.ConectorAprovado() != dominio.ConectorPnT || h.AprovadoPor != "admin:1450" || !h.AprovadoEm.Equal(em) ||
				h.AprovadoMotivo != "fase 1" || h.Origem != dominio.OrigemTela || h.GQConta != "" || !h.DivergenteDesde.IsZero() {
				t.Fatalf("lida: %+v %v %v", h, ok, err)
			}

			// a divergência (o serviço) e um erro que não grava
			diverge := func(antes dominio.Habilitacao, existia bool) (dominio.Habilitacao, string, bool, error) {
				if !existia || antes.Estado != dominio.EstadoAprovado {
					t.Fatalf("divergência: %+v %v", antes, existia)
				}
				h := antes
				h.Habilitado, h.Estado, h.Versao, h.Origem = false, dominio.EstadoContaDivergente, antes.Versao+1, dominio.OrigemServico
				h.GQConector, h.GQCorretora, h.GQConta, h.DivergenteDesde = dominio.ConectorPnT, 85, "1234567", em.Add(time.Minute)
				h.Por, h.Motivo, h.Em = "servico", "cadastro do GQ diferente do aprovado", em.Add(time.Minute)
				return h, dominio.EventoDivergencia, true, nil
			}
			falha := errors.New("versao mudou")
			if _, err := r.TransicaoReal(1450, func(dominio.Habilitacao, bool) (dominio.Habilitacao, string, bool, error) {
				return dominio.Habilitacao{AprovacaoReal: dominio.AprovacaoReal{Estado: dominio.EstadoRevogado}}, dominio.EventoRevogar, true, falha
			}); !errors.Is(err, falha) {
				t.Fatalf("erro da decisão: %v", err)
			}
			if res, err = r.TransicaoReal(1450, diverge); err != nil || !res.Gravou || res.Antes.Versao != 1 || res.Depois.Versao != 2 {
				t.Fatalf("divergir: %+v %v", res, err)
			}
			h, _, _ = r.Habilitacao(1450, dominio.AmbienteReal)
			if h.Habilitado || h.Estado != dominio.EstadoContaDivergente || h.Conta != "4582351" || h.GQConta != "1234567" || h.GQCorretora != 85 ||
				!h.DivergenteDesde.Equal(em.Add(time.Minute)) || h.AprovadoMotivo != "fase 1" {
				t.Fatalf("divergente: %+v", h)
			}

			hist, err := r.HistoricoHabilitacao(1450, dominio.AmbienteReal, 10)
			if err != nil || len(hist) != 2 || hist[0].Evento != dominio.EventoDivergencia || hist[0].EstadoAntes != dominio.EstadoAprovado ||
				hist[0].GQConta != "1234567" || hist[1].Evento != dominio.EventoAprovar || hist[1].EstadoAntes != dominio.EstadoSemAprovacao ||
				hist[1].Conta != "4582351" || hist[0].ID <= hist[1].ID {
				t.Fatalf("histórico: %+v %v", hist, err)
			}
			if hist, _ := r.HistoricoHabilitacao(1450, dominio.AmbienteReal, 1); len(hist) != 1 || hist[0].Evento != dominio.EventoDivergencia {
				t.Fatalf("histórico com limite: %+v", hist)
			}
			s, ok, _ := r.Habilitacao(1450, dominio.AmbienteSimulador)
			if !ok || !s.Habilitado || s.Estado != "" || s.ConectorAprovado() != dominio.ConectorFlexscan {
				t.Fatalf("SIMULADOR tocado: %+v", s)
			}
			if hs, _ := r.HistoricoHabilitacao(1450, dominio.AmbienteSimulador, 10); len(hs) != 1 || hs[0].Evento != "" {
				t.Fatalf("histórico do SIMULADOR: %+v", hs)
			}
		})
	}
}
