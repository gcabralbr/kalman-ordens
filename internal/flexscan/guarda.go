package flexscan

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// guardaLogin: o limite de logins na Flexscan que sobrevive aos restarts (23/10: um ciclo de restart
// pode virar uma rajada de logins, e a Flexscan bloqueia a senha). Antes de cada login:
//   - um erro definitivo gravado (senha bloqueada/inválida/expirada, MFA) para a mesma credencial →
//     não tenta; outra credencial (FLEXSCAN_SENHA trocada) libera;
//   - LoginMax tentativas em LoginJanela → não tenta até a janela passar.
//
// A tentativa é gravada antes de ir à rede. Sem conseguir gravar, não tenta (o limite não valeria).
// A credencial é guardada como sha256(sal ‖ usuário ‖ senha), nunca a senha.
type guardaLogin struct {
	mu      sync.Mutex
	arquivo string // "" = só em memória (testes)
	max     int
	janela  time.Duration
	impr    func(sal string) string
	estado  estadoLogin
	lido    bool
}

type estadoLogin struct {
	Sal        string      `json:"sal"`
	Tentativas []time.Time `json:"tentativas"`
	Bloqueio   *bloqueio   `json:"bloqueio,omitempty"`
}

type bloqueio struct {
	Credencial string    `json:"credencial"`
	Motivo     string    `json:"motivo"`
	Em         time.Time `json:"em"`
}

func novaGuarda(arquivo string, max int, janela time.Duration, usuario, senha string) *guardaLogin {
	if max <= 0 {
		max = 5
	}
	if janela <= 0 {
		janela = 10 * time.Minute
	}
	return &guardaLogin{arquivo: arquivo, max: max, janela: janela, impr: func(sal string) string {
		h := sha256.Sum256([]byte(sal + "\x00" + usuario + "\x00" + senha))
		return hex.EncodeToString(h[:])
	}}
}

func (g *guardaLogin) carregar() error {
	if g.lido {
		return nil
	}
	if g.arquivo != "" {
		b, err := os.ReadFile(g.arquivo)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return err
		default:
			if err := json.Unmarshal(b, &g.estado); err != nil {
				return fmt.Errorf("%s ilegível: %w", g.arquivo, err)
			}
		}
	}
	if g.estado.Sal == "" {
		s := make([]byte, 16)
		if _, err := rand.Read(s); err != nil {
			return err
		}
		g.estado.Sal = hex.EncodeToString(s)
	}
	g.lido = true
	return nil
}

func (g *guardaLogin) gravar() error {
	if g.arquivo == "" {
		return nil
	}
	b, _ := json.Marshal(g.estado)
	tmp := g.arquivo + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, g.arquivo); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(g.arquivo)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// pode: registra a tentativa e diz se o login pode ir à rede. O erro é o motivo de não ir.
func (g *guardaLogin) pode(agora time.Time) *ErroLogin {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.carregar(); err != nil {
		return &ErroLogin{Motivo: "estado dos logins ilegível (" + err.Error() + "): sem login até corrigir", Definitivo: true}
	}
	if b := g.estado.Bloqueio; b != nil && b.Credencial == g.impr(g.estado.Sal) {
		return &ErroLogin{Motivo: fmt.Sprintf("login bloqueado desde %s (%s): troque FLEXSCAN_SENHA (ou apague %s) para tentar de novo",
			b.Em.In(fuso).Format("02/01 15:04:05"), b.Motivo, g.arquivo), Definitivo: true}
	}
	var recentes []time.Time
	for _, t := range g.estado.Tentativas {
		if agora.Sub(t) < g.janela {
			recentes = append(recentes, t)
		}
	}
	if len(recentes) >= g.max {
		g.estado.Tentativas = recentes
		return &ErroLogin{Motivo: fmt.Sprintf("limite de %d logins em %s atingido (contando os restarts): o próximo depois de %s",
			g.max, g.janela, recentes[0].Add(g.janela).In(fuso).Format("15:04:05"))}
	}
	g.estado.Tentativas = append(recentes, agora)
	if err := g.gravar(); err != nil {
		g.estado.Tentativas = recentes
		return &ErroLogin{Motivo: "não foi possível gravar a tentativa de login (" + err.Error() + "): sem login (o limite entre restarts não valeria)", Definitivo: true}
	}
	return nil
}

// resultado: o desfecho do login — erro definitivo grava o bloqueio desta credencial; sucesso limpa.
func (g *guardaLogin) resultado(err error, agora time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var el *ErroLogin
	switch {
	case err == nil:
		if g.estado.Bloqueio == nil {
			return
		}
		g.estado.Bloqueio = nil
	case errors.As(err, &el) && el.Definitivo:
		g.estado.Bloqueio = &bloqueio{Credencial: g.impr(g.estado.Sal), Motivo: el.Motivo, Em: agora}
	default:
		return
	}
	_ = g.gravar()
}
