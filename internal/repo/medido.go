package repo

import (
	"time"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/metricas"
)

// Medido mede cada operação do repositório (SPEC v1.9 M: latência de gravação e leitura no banco,
// métrica banco_segundos{operacao}). Delega tudo ao repositório de baixo.
type Medido struct{ Base Repositorio }

// Medir embrulha o repositório (o motor usa o embrulhado; quem precisa do concreto usa Base).
func Medir(r Repositorio) Repositorio {
	if m, ok := r.(*Medido); ok {
		return m
	}
	return &Medido{Base: r}
}

func medir[T any](op string, f func() (T, error)) (T, error) {
	t0 := time.Now()
	v, err := f()
	metricas.Banco.Observar(time.Since(t0).Seconds(), op)
	if err != nil {
		metricas.BancoErros.Inc(op)
	}
	return v, err
}

func medirErr(op string, f func() error) error {
	_, err := medir(op, func() (struct{}, error) { return struct{}{}, f() })
	return err
}

func (m *Medido) SalvarOperacao(o *dominio.Operacao) error {
	return medirErr("SalvarOperacao", func() error { return m.Base.SalvarOperacao(o) })
}
func (m *Medido) OperacaoExiste(mod string, id int64) (bool, error) {
	return medir("OperacaoExiste", func() (bool, error) { return m.Base.OperacaoExiste(mod, id) })
}
func (m *Medido) SalvarOrdem(o *dominio.Ordem) error {
	return medirErr("SalvarOrdem", func() error { return m.Base.SalvarOrdem(o) })
}
func (m *Medido) RegistrarEvento(e EventoFIX) (bool, error) {
	return medir("RegistrarEvento", func() (bool, error) { return m.Base.RegistrarEvento(e) })
}
func (m *Medido) EnviosDesde(t time.Time) (int, error) {
	return medir("EnviosDesde", func() (int, error) { return m.Base.EnviosDesde(t) })
}
func (m *Medido) Publicar(tipo, mod string, op int64, dados []byte, em time.Time) (int64, error) {
	return medir("Publicar", func() (int64, error) { return m.Base.Publicar(tipo, mod, op, dados, em) })
}
func (m *Medido) Publicados(desde int64, limite int, mod string) ([]Publicado, error) {
	return medir("Publicados", func() ([]Publicado, error) { return m.Base.Publicados(desde, limite, mod) })
}
func (m *Medido) LerEstado(k string) (string, bool, error) {
	var ok bool
	v, err := medir("LerEstado", func() (string, error) {
		v, o, err := m.Base.LerEstado(k)
		ok = o
		return v, err
	})
	return v, ok, err
}
func (m *Medido) GravarEstado(k, v string, em time.Time) error {
	return medirErr("GravarEstado", func() error { return m.Base.GravarEstado(k, v, em) })
}
func (m *Medido) CarregarDia(d string) ([]dominio.Operacao, []dominio.Ordem, error) {
	var ords []dominio.Ordem
	ops, err := medir("CarregarDia", func() ([]dominio.Operacao, error) {
		ops, o, err := m.Base.CarregarDia(d)
		ords = o
		return ops, err
	})
	return ops, ords, err
}
func (m *Medido) Habilitacao(uid int) (dominio.Habilitacao, bool, error) {
	var ok bool
	h, err := medir("Habilitacao", func() (dominio.Habilitacao, error) {
		h, o, err := m.Base.Habilitacao(uid)
		ok = o
		return h, err
	})
	return h, ok, err
}
func (m *Medido) Habilitacoes() ([]dominio.Habilitacao, error) {
	return medir("Habilitacoes", m.Base.Habilitacoes)
}
func (m *Medido) GravarHabilitacao(h dominio.Habilitacao) error {
	return medirErr("GravarHabilitacao", func() error { return m.Base.GravarHabilitacao(h) })
}
func (m *Medido) AlterarHabilitado(uid int, hab bool, por, motivo string, em time.Time) (dominio.Habilitacao, bool, error) {
	var ok bool
	h, err := medir("AlterarHabilitado", func() (dominio.Habilitacao, error) {
		h, o, err := m.Base.AlterarHabilitado(uid, hab, por, motivo, em)
		ok = o
		return h, err
	})
	return h, ok, err
}
func (m *Medido) DiaDaOperacao(mod string, id int64) (string, bool, error) {
	var ok bool
	d, err := medir("DiaDaOperacao", func() (string, error) {
		d, o, err := m.Base.DiaDaOperacao(mod, id)
		ok = o
		return d, err
	})
	return d, ok, err
}
func (m *Medido) EventosFIXDasOrdens(ids []int64) ([]EventoFIX, error) {
	return medir("EventosFIXDasOrdens", func() ([]EventoFIX, error) { return m.Base.EventosFIXDasOrdens(ids) })
}
func (m *Medido) PublicadosDaOperacao(mod string, id int64) ([]Publicado, error) {
	return medir("PublicadosDaOperacao", func() ([]Publicado, error) { return m.Base.PublicadosDaOperacao(mod, id) })
}
func (m *Medido) PublicadosEntre(de, ate time.Time, tipo string) ([]Publicado, error) {
	return medir("PublicadosEntre", func() ([]Publicado, error) { return m.Base.PublicadosEntre(de, ate, tipo) })
}
func (m *Medido) RegistrarSessao(e EventoSessao) error {
	return medirErr("RegistrarSessao", func() error { return m.Base.RegistrarSessao(e) })
}
func (m *Medido) SessaoEntre(de, ate time.Time) ([]EventoSessao, error) {
	return medir("SessaoEntre", func() ([]EventoSessao, error) { return m.Base.SessaoEntre(de, ate) })
}
func (m *Medido) RegistrarPedido(p *PedidoHTTP) error {
	return medirErr("RegistrarPedido", func() error { return m.Base.RegistrarPedido(p) })
}
func (m *Medido) PedidosDoDia(d string, uid int, mod string) ([]PedidoHTTP, error) {
	return medir("PedidosDoDia", func() ([]PedidoHTTP, error) { return m.Base.PedidosDoDia(d, uid, mod) })
}
func (m *Medido) PedidosDaOperacao(mod string, id int64) ([]PedidoHTTP, error) {
	return medir("PedidosDaOperacao", func() ([]PedidoHTTP, error) { return m.Base.PedidosDaOperacao(mod, id) })
}
func (m *Medido) ConfigUsuario(uid int) (string, string, bool, error) {
	var prd string
	var ok bool
	hml, err := medir("ConfigUsuario", func() (string, error) {
		h, p, o, err := m.Base.ConfigUsuario(uid)
		prd, ok = p, o
		return h, err
	})
	return hml, prd, ok, err
}

func (m *Medido) GravarLote(l *Lote) error {
	return medirErr("GravarLote", func() error { return m.Base.GravarLote(l) })
}
func (m *Medido) MaioresIDs() (int64, int64, error) {
	var b int64
	a, err := medir("MaioresIDs", func() (int64, error) {
		a, x, err := m.Base.MaioresIDs()
		b = x
		return a, err
	})
	return a, b, err
}
func (m *Medido) ChavesFillDoDia(de, ate time.Time) (map[string]bool, error) {
	return medir("ChavesFillDoDia", func() (map[string]bool, error) { return m.Base.ChavesFillDoDia(de, ate) })
}
func (m *Medido) OperacoesExistentes(mod string, ids []int64) (map[int64]bool, error) {
	return medir("OperacoesExistentes", func() (map[int64]bool, error) { return m.Base.OperacoesExistentes(mod, ids) })
}

func (m *Medido) ConfigUsuarios(uids []int) (map[int][2]string, error) {
	return medir("ConfigUsuarios", func() (map[int][2]string, error) { return m.Base.ConfigUsuarios(uids) })
}

var _ Repositorio = (*Medido)(nil)
