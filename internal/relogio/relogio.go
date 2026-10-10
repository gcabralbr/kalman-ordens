// Package relogio dá a hora ao motor. Em produção, a hora do sistema; nos roteiros, um relógio
// que só anda quando o executor manda (verbo avanca).
package relogio

import (
	"sync"
	"time"
)

// Relogio.
type Relogio interface {
	Agora() time.Time
}

// Real usa time.Now.
type Real struct{}

func (Real) Agora() time.Time { return time.Now() }

// Controlado só anda com Avancar.
type Controlado struct {
	mu sync.Mutex
	t  time.Time
}

func NovoControlado(t time.Time) *Controlado { return &Controlado{t: t} }

func (c *Controlado) Agora() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *Controlado) Avancar(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Fuso do pregão.
var Fuso = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()
