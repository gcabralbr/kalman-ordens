package pnt

import (
	"strings"
	"testing"
	"time"

	"github.com/quickfixgo/quickfix"
)

// v1.31.1: o robô (20858) e o OMS (20859) da DSA vão no D (1031 e 1014) e no G, montados a partir da
// Identificacao; o F não os tem no dicionário e não leva. Sem os dois na Identificacao, nada muda.
func TestRoboEOMS(t *testing.T) {
	agora := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	com := Identificacao{Conta: "4582351", Bolsa: "BVMF", RobotID: "BTG_ALGO", EnteringFirm: "BTGDAYTRADE"}
	sem := Identificacao{Conta: "4582351", Bolsa: "BVMF"}
	entrada := Ordem{ClOrdID: "KM-E-1-1", Simbolo: "WINZ26", Lado: Compra, Qtd: 1, Preco: 171690, Estrategia: EstrategiaAgendada}
	stop := Ordem{ClOrdID: "KM-P-1-1", Simbolo: "WINZ26", Lado: Venda, Qtd: 1, Estrategia: EstrategiaStop, Stop: 171390, StopLim: 171290}
	tag := func(m *quickfix.Message, n int) (string, bool) {
		v, err := m.Body.GetString(quickfix.Tag(n))
		return v, err == nil
	}
	for nome, m := range map[string]*quickfix.Message{
		"D 1031": NovaOrdem(com, entrada, agora),
		"D 1014": NovaOrdem(com, stop, agora),
		"G 1014": Replace(com, stop, "KM-P-1-0", "dsa.1", agora),
	} {
		if v, _ := tag(m, 20858); v != "BTG_ALGO" {
			t.Errorf("%s: 20858 = %q, quer BTG_ALGO", nome, v)
		}
		if v, _ := tag(m, 20859); v != "BTGDAYTRADE" {
			t.Errorf("%s: 20859 = %q, quer BTGDAYTRADE", nome, v)
		}
		if v, _ := tag(m, 847); v == "" {
			t.Errorf("%s: sem 847", nome)
		}
	}
	f := Cancelamento(com, "KM-P-1-2", "KM-P-1-1", "dsa.1", "WINZ26", Venda, 1, agora)
	for _, n := range []int{20858, 20859} {
		if v, ok := tag(f, n); ok {
			t.Errorf("F com %d=%q (o dicionário não tem no F)", n, v)
		}
	}
	for nome, m := range map[string]*quickfix.Message{"D": NovaOrdem(sem, entrada, agora), "G": Replace(sem, stop, "KM-P-1-0", "dsa.1", agora)} {
		for _, n := range []int{20858, 20859} {
			if v, ok := tag(m, n); ok {
				t.Errorf("%s sem robô na Identificacao com %d=%q", nome, n, v)
			}
		}
	}
	// o fio: as duas tags no corpo da mensagem montada
	if s := NovaOrdem(com, entrada, agora).String(); !strings.Contains(s, "\x0120858=BTG_ALGO\x01") || !strings.Contains(s, "\x0120859=BTGDAYTRADE\x01") {
		t.Errorf("D no fio sem as duas: %q", s)
	}
}
