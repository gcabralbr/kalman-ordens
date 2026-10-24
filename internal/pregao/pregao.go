// Package pregao diz se um dia é de pregão: seg–sex, fora dos feriados do arquivo que o Kalman gera
// da tabela anferiado (/var/lib/kalman/feriados.json, {"AAAA-MM-DD": "descrição"}). A mesma regra do
// horário do pnt-md-hml (deploy/pnt-md-hml-horario.sh): sem o arquivo, ou ilegível, o dia útil vale
// (seg–sex) — melhor um alerta a mais num feriado do que nenhum num dia de pregão.
package pregao

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Calendario lê o arquivo de feriados no máximo uma vez por hora (e na troca de dia).
type Calendario struct {
	arquivo string
	fuso    *time.Location

	mu       sync.Mutex
	feriados map[string]string
	lidoEm   time.Time
	diaLido  string
	erro     string
}

// Novo: arquivo "" = só seg–sex.
func Novo(arquivo string, fuso *time.Location) *Calendario {
	return &Calendario{arquivo: arquivo, fuso: fuso}
}

// DiaDePregao: o dia de t (no fuso) é de pregão; motivo diz por que não ("sabado", "feriado: X").
func (c *Calendario) DiaDePregao(t time.Time) (bool, string) {
	t = t.In(c.fuso)
	switch t.Weekday() {
	case time.Saturday:
		return false, "sabado"
	case time.Sunday:
		return false, "domingo"
	}
	dia := t.Format("2006-01-02")
	c.mu.Lock()
	defer c.mu.Unlock()
	c.carregar(t, dia)
	if d, ok := c.feriados[dia]; ok {
		return false, "feriado: " + d
	}
	return true, ""
}

func (c *Calendario) carregar(t time.Time, dia string) {
	if c.arquivo == "" || (dia == c.diaLido && t.Sub(c.lidoEm) < time.Hour && t.Sub(c.lidoEm) >= 0) {
		return
	}
	c.diaLido, c.lidoEm = dia, t
	f, err := ler(c.arquivo)
	if err != nil {
		if msg := err.Error(); msg != c.erro {
			slog.Warn("feriados ilegíveis: vale seg–sex", "arquivo", c.arquivo, "erro", err)
			c.erro = msg
		}
		c.feriados = nil
		return
	}
	c.erro = ""
	c.feriados = f
}

func ler(arquivo string) (map[string]string, error) {
	b, err := os.ReadFile(arquivo)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", arquivo, err)
	}
	return m, nil
}

// Conferir: lê o arquivo agora (a partida registra no journal quantos feriados vieram, ou por que não).
func (c *Calendario) Conferir() (int, error) {
	if c.arquivo == "" {
		return 0, fmt.Errorf("FERIADOS_ARQ vazio")
	}
	f, err := ler(c.arquivo)
	return len(f), err
}
