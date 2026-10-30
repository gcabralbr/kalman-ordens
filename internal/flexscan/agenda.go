package flexscan

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/relogio"
)

// Agenda da Flexscan (v1.28, 30/10): fora do pregão a conferência do webhook a cada 10 min e a
// renovação do login de madrugada eram só ruído no tráfego. Agora:
//   - a conferência do webhook e a renovação do token só dentro da janela (FLEXSCAN_JANELA, dias de
//     pregão), ou com ordem viva (por segurança, a qualquer hora);
//   - uma conferência obrigatória antes da abertura (FLEXSCAN_CONFERIR_ABERTURA), que pega a limpeza
//     noturna da Flexscan (~03:45) e recadastra a tempo;
//   - a renovação do token agendada para antes da abertura (FLEXSCAN_RENOVAR_ABERTURA) quando o
//     token não chega ao fim da janela; o token que vence fora da janela não é renovado: a ponte
//     "dorme" (sem login, sem alerta) e loga de novo na abertura;
//   - fora da janela, sem ordem viva, nenhuma chamada à Flexscan; um envio do motor (saída, ação do
//     admin) acorda a ponte na hora.

// Agenda: os horários em minutos do dia (fuso de Brasília); -1 = sem.
type Agenda struct {
	Inicio, Fim      int // a janela; Inicio -1 = sempre (sem janela, como antes da v1.28)
	RenovarAbertura  int
	ConferirAbertura int
}

// AgendaSempre: sem janela (a ponte sempre ativa, como antes da v1.28).
var AgendaSempre = Agenda{Inicio: -1, Fim: -1, RenovarAbertura: -1, ConferirAbertura: -1}

// LerAgenda: FLEXSCAN_JANELA ("08:00-18:30"; vazia ou "sempre" = sem janela) e os dois horários
// ("08:15"; vazio = sem).
func LerAgenda(janela, renovar, conferir string) (Agenda, error) {
	a := AgendaSempre
	j := strings.TrimSpace(janela)
	if j != "" && !strings.EqualFold(j, "sempre") {
		ini, fim, ok := strings.Cut(j, "-")
		if !ok {
			return a, fmt.Errorf("FLEXSCAN_JANELA %q: esperado HH:MM-HH:MM", janela)
		}
		var err error
		if a.Inicio, err = minutoDoDia(ini); err != nil {
			return a, fmt.Errorf("FLEXSCAN_JANELA %q: %w", janela, err)
		}
		if a.Fim, err = minutoDoDia(fim); err != nil {
			return a, fmt.Errorf("FLEXSCAN_JANELA %q: %w", janela, err)
		}
		if a.Fim <= a.Inicio {
			return a, fmt.Errorf("FLEXSCAN_JANELA %q: o fim tem de ser depois do início", janela)
		}
	}
	for _, x := range []struct {
		nome, v string
		dst     *int
	}{{"FLEXSCAN_RENOVAR_ABERTURA", renovar, &a.RenovarAbertura}, {"FLEXSCAN_CONFERIR_ABERTURA", conferir, &a.ConferirAbertura}} {
		if strings.TrimSpace(x.v) == "" {
			continue
		}
		m, err := minutoDoDia(x.v)
		if err != nil {
			return a, fmt.Errorf("%s %q: %w", x.nome, x.v, err)
		}
		*x.dst = m
	}
	return a, nil
}

func minutoDoDia(s string) (int, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if !ok || e1 != nil || e2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 || hh*60+mm > 24*60 {
		return 0, fmt.Errorf("horário %q inválido (HH:MM)", s)
	}
	return hh*60 + mm, nil
}

func hhmm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// Sempre: sem janela.
func (a Agenda) Sempre() bool { return a.Inicio < 0 }

// Texto: a janela para o /saude ("08:00-18:30" ou "sempre").
func (a Agenda) Texto() string {
	if a.Sempre() {
		return "sempre"
	}
	return hhmm(a.Inicio) + "-" + hhmm(a.Fim)
}

func minuto(t time.Time) int {
	t = t.In(relogio.Fuso)
	return t.Hour()*60 + t.Minute()
}

func diaDe(t time.Time) string { return t.In(relogio.Fuso).Format("2006-01-02") }

// fimDoDia: o fim da janela no dia de t (sem janela: a meia-noite seguinte).
func (a Agenda) fimDoDia(t time.Time) time.Time {
	t = t.In(relogio.Fuso)
	z := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, relogio.Fuso)
	if a.Sempre() {
		return z.AddDate(0, 0, 1)
	}
	return z.Add(time.Duration(a.Fim) * time.Minute)
}

// dentro: t na janela de um dia de pregão (sem janela: sempre).
func (a Agenda) dentro(t time.Time, pregao bool) bool {
	if a.Sempre() {
		return true
	}
	m := minuto(t)
	return pregao && m >= a.Inicio && m < a.Fim
}

// devida: o horário h (RenovarAbertura ou ConferirAbertura) já chegou hoje, num dia de pregão,
// antes do fim da janela (um restart à noite não faz a "conferência da abertura").
func (a Agenda) devida(h int, t time.Time, pregao bool) bool {
	if h < 0 || !pregao {
		return false
	}
	m := minuto(t)
	fim := 24 * 60
	if !a.Sempre() {
		fim = a.Fim
	}
	return m >= h && m < fim
}

// inicioDoDia: a partir de quando um login conta como o da abertura (o menor entre o início da
// janela e a renovação da abertura).
func (a Agenda) inicioDoDia() int {
	r := a.RenovarAbertura
	if !a.Sempre() && (r < 0 || a.Inicio < r) {
		r = a.Inicio
	}
	return r
}

// diaUtil: o calendário padrão, sem o arquivo de feriados (seg–sex).
func diaUtil(t time.Time) bool {
	switch t.In(relogio.Fuso).Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	return true
}
