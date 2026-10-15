package motor

import (
	"log/slog"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// cacheUsuarios (v1.10 B): habilitação (kalmanordemusuario) e configuração do GQ (acusuario) em
// memória — a entrada não vai ao banco. Carregado na subida, relido a cada CACHE_USUARIOS_S (30 s,
// em segundo plano) e invalidado pelas rotas do serviço que mudam a habilitação. A CLI grava direto
// no banco: o efeito dela chega em até 30 s. O que não está no cache (uid novo) é lido na hora e
// guardado, inclusive "sem linha".
type cacheUsuarios struct {
	hab          map[int]dominio.Habilitacao
	semHab       map[int]bool
	cfg          map[int][2]string // uid → {usutxtconfig, usutxtconfigprd}
	semCfg       map[int]bool
	carregadoEm  time.Time
	recarregando bool
	versao       int // cada invalidação sobe; uma recarga que começou antes é descartada
}

func novoCacheUsuarios() *cacheUsuarios {
	return &cacheUsuarios{hab: map[int]dominio.Habilitacao{}, semHab: map[int]bool{}, cfg: map[int][2]string{}, semCfg: map[int]bool{}}
}

// lerUsuarios: todas as habilitações e a configuração do GQ dos habilitados (fora da goroutine
// do motor na recarga).
func (m *Motor) lerUsuarios() (map[int]dominio.Habilitacao, map[int][2]string, error) {
	hs, err := m.repo.Habilitacoes()
	if err != nil {
		return nil, nil, err
	}
	hab := map[int]dominio.Habilitacao{}
	var uids []int
	for _, h := range hs {
		hab[h.UID] = h
		if h.Habilitado {
			uids = append(uids, h.UID)
		}
	}
	cfg, err := m.repo.ConfigUsuarios(uids)
	if err != nil {
		return nil, nil, err
	}
	return hab, cfg, nil
}

func (m *Motor) aplicarUsuarios(hab map[int]dominio.Habilitacao, cfg map[int][2]string) {
	c := m.usuarios
	c.hab, c.cfg = hab, cfg
	c.semHab, c.semCfg = map[int]bool{}, map[int]bool{}
	c.carregadoEm = m.agora()
}

// carregarUsuarios: na subida, antes da goroutine do motor.
func (m *Motor) carregarUsuarios() {
	hab, cfg, err := m.lerUsuarios()
	if err != nil {
		slog.Warn("cache de usuários: leitura falhou; cada entrada lê do banco até a próxima recarga", "erro", err)
		m.usuarios.carregadoEm = m.agora()
		return
	}
	m.aplicarUsuarios(hab, cfg)
	slog.Info("cache de usuários carregado", "habilitacoes", len(hab), "configs", len(cfg))
}

// recarregarUsuarios (tick): passado CACHE_USUARIOS_S, relê em segundo plano e aplica na fila P0.
func (m *Motor) recarregarUsuarios(agora time.Time) {
	c := m.usuarios
	if c.recarregando || agora.Sub(c.carregadoEm) < m.p.CacheUsuarios {
		return
	}
	c.recarregando = true
	versao := c.versao
	go func() {
		hab, cfg, err := m.lerUsuarios()
		m.entrar("outro", func() {
			c.recarregando = false
			switch {
			case err != nil:
				slog.Warn("cache de usuários: recarga falhou", "erro", err)
				c.carregadoEm = m.agora()
			case c.versao != versao:
				// invalidado durante a leitura: esta leitura pode ser anterior à mudança
				c.carregadoEm = time.Time{}
			default:
				m.aplicarUsuarios(hab, cfg)
			}
		})
	}()
}

// invalidarUsuario: a rota do serviço mudou a habilitação do uid (0 = todos).
func (m *Motor) invalidarUsuario(uid int) {
	c := m.usuarios
	c.versao++
	if uid == 0 {
		c.hab, c.semHab, c.cfg, c.semCfg = map[int]dominio.Habilitacao{}, map[int]bool{}, map[int][2]string{}, map[int]bool{}
		return
	}
	delete(c.hab, uid)
	delete(c.semHab, uid)
	delete(c.cfg, uid)
	delete(c.semCfg, uid)
}

// InvalidarUsuarios: o executor de roteiros muda o banco por fora (usuario, habilita) e avisa.
func (m *Motor) InvalidarUsuarios() { m.Fazer(func() { m.invalidarUsuario(0) }) }

// habilitacaoDo: do cache; sem entrada, lê do banco e guarda.
func (m *Motor) habilitacaoDo(uid int) (dominio.Habilitacao, bool, error) {
	c := m.usuarios
	if h, ok := c.hab[uid]; ok {
		return h, true, nil
	}
	if c.semHab[uid] {
		return dominio.Habilitacao{UID: uid}, false, nil
	}
	h, ok, err := m.repo.Habilitacao(uid)
	if err != nil {
		return h, ok, err
	}
	if ok {
		c.hab[uid] = h
	} else {
		c.semHab[uid] = true
	}
	return h, ok, nil
}

// configUsuarioDo: do cache; sem entrada, lê do banco e guarda.
func (m *Motor) configUsuarioDo(uid int) (string, string, bool, error) {
	c := m.usuarios
	if v, ok := c.cfg[uid]; ok {
		return v[0], v[1], true, nil
	}
	if c.semCfg[uid] {
		return "", "", false, nil
	}
	hml, prd, achou, err := m.repo.ConfigUsuario(uid)
	if err != nil {
		return "", "", false, err
	}
	if achou {
		c.cfg[uid] = [2]string{hml, prd}
	} else {
		c.semCfg[uid] = true
	}
	return hml, prd, achou, nil
}
