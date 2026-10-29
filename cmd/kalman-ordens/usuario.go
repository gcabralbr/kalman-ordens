package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
	"gammaquant.com.br/kalman-ordens/internal/dominio"
	"gammaquant.com.br/kalman-ordens/internal/repo"
	"gammaquant.com.br/kalman-ordens/internal/usuario"
)

// cliUsuario: habilitação por uid (v1.2 U5), só por aqui. Fala direto com o banco (DB_DSN), não
// com o HTTP: não há endpoint que altere habilitação. Sem DB_DSN no ambiente, lê o service.env
// (-env), que só root e o grupo kalman-ordens leem.
//
// A habilitação é por (uid, ambiente) (SPEC-AMBIENTES-v1 §4.2): -ambiente REAL mexe só na do REAL;
// sem a flag, o SIMULADOR. Habilitar o SIMULADOR nunca habilita o REAL.
//
//	kalman-ordens usuario listar
//	kalman-ordens usuario conferir    -uid N
//	kalman-ordens usuario habilitar   -uid N -motivo X [-ambiente SIMULADOR|REAL] [-posicao-max N] [-qtd-max N] [-conectores pnt,flexscan]
//	kalman-ordens usuario desabilitar -uid N -motivo X [-ambiente SIMULADOR|REAL]
func cliUsuario(cfg config.Config, args []string, saida io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usuario listar | conferir | habilitar | desabilitar")
	}
	acao, args := args[0], args[1:]
	fs := flag.NewFlagSet("usuario "+acao, flag.ContinueOnError)
	uid := fs.Int("uid", 0, "uid do Kalman (o mesmo do GQ)")
	motivo := fs.String("motivo", "", "motivo (obrigatório em habilitar e desabilitar)")
	posMax := fs.Int("posicao-max", 0, "limite de posição do uid (0 = só o global; sem a flag, mantém o atual)")
	qtdMax := fs.Int("qtd-max", 0, "limite de quantidade por ordem do uid (0 = só o global; sem a flag, mantém o atual)")
	conectores := fs.String("conectores", "", "conectores permitidos ao uid: pnt, flexscan ou pnt,flexscan (com mais de um, o GQ escolhe pelo Robo.Conector; sem a flag, mantém o atual; obrigatória na linha nova)")
	ambFlag := fs.String("ambiente", dominio.AmbienteSimulador, "ambiente da habilitação: SIMULADOR ou REAL")
	arqEnv := fs.String("env", "/etc/kalman-ordens/service.env", "service.env, se DB_DSN não estiver no ambiente")
	if err := fs.Parse(args); err != nil {
		return err
	}
	amb, err := dominio.LerAmbiente(*ambFlag)
	if err != nil {
		return fmt.Errorf("-ambiente: %w", err)
	}
	r, cfg, err := abrirBancoCLI(cfg, *arqEnv)
	if err != nil {
		return err
	}
	defer r.Fechar()
	// o schema tem de estar na versão do binário: a CLI não migra sozinha
	if err := schemaEmDia(r); err != nil {
		return err
	}
	precisaUID := func() error {
		if *uid <= 0 {
			return fmt.Errorf("usuario %s exige -uid", acao)
		}
		return nil
	}
	switch acao {
	case "listar":
		hs, err := r.Habilitacoes()
		if err != nil {
			return err
		}
		for _, h := range hs {
			fmt.Fprintf(saida, "uid=%d ambiente=%s habilitado=%v posicao_max=%d qtd_max=%d conectores=%s por=%s em=%s motivo=%q\n",
				h.UID, dominio.AmbienteDe(h.Ambiente), h.Habilitado, h.PosicaoMax, h.QtdMaxOrdem, h.ConectoresTexto(), h.Por, h.Em.Format(time.RFC3339), h.Motivo)
		}
		return nil
	case "conferir":
		if err := precisaUID(); err != nil {
			return err
		}
		return conferirUsuario(r, cfg, *uid, saida)
	case "habilitar", "desabilitar":
		if err := precisaUID(); err != nil {
			return err
		}
		if *motivo == "" {
			return fmt.Errorf("usuario %s exige -motivo", acao)
		}
		// sem a flag, o limite fica o atual (v1.8 H3; antes zerava); linha nova: 0 = só o global
		atual, existe, err := r.Habilitacao(*uid, amb)
		if err != nil {
			return err
		}
		pm, qm, cs := atual.PosicaoMax, atual.QtdMaxOrdem, atual.Conectores
		var errCs error
		comConectores := false
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "posicao-max":
				pm = *posMax
			case "qtd-max":
				qm = *qtdMax
			case "conectores":
				cs, errCs = dominio.LerConectores(*conectores)
				comConectores = true
			}
		})
		if errCs != nil {
			return fmt.Errorf("-conectores: %w", errCs)
		}
		if !existe && !comConectores && acao == "habilitar" {
			// v1.26 (29/10): a linha nova nascia PNT sem ninguém pedir (a do REAL do 5725, que é
			// Flexscan); o conector é escolha explícita
			return fmt.Errorf("uid %d sem habilitação no %s: a linha nova exige -conectores (pnt, flexscan ou pnt,flexscan; o do %s do uid)", *uid, amb, usuario.Campo(amb))
		}
		h := dominio.Habilitacao{UID: *uid, Ambiente: amb, Habilitado: acao == "habilitar", PosicaoMax: pm, QtdMaxOrdem: qm, Conectores: cs,
			Por: quem(), Motivo: *motivo, Em: time.Now()}
		if err := r.GravarHabilitacao(h); err != nil {
			return err
		}
		fmt.Fprintf(saida, "uid=%d ambiente=%s habilitado=%v conectores=%s por=%s\n", h.UID, amb, h.Habilitado, h.ConectoresTexto(), h.Por)
		if h.Habilitado {
			return conferirUsuario(r, cfg, *uid, saida)
		}
		return nil
	}
	return fmt.Errorf("usuario %s: ação desconhecida (listar | conferir | habilitar | desabilitar)", acao)
}

// cliMigrar: aplica as migrações pendentes, registra em kalmanordem_migracao e sai — sem FIX nem
// HTTP. Rodar de novo não faz nada. Instalação: logo depois do binário e do service.env.
func cliMigrar(cfg config.Config, args []string, saida io.Writer) error {
	fs := flag.NewFlagSet("migrar", flag.ContinueOnError)
	arqEnv := fs.String("env", "/etc/kalman-ordens/service.env", "service.env, se DB_DSN não estiver no ambiente")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, _, err := abrirBancoCLI(cfg, *arqEnv)
	if err != nil {
		return err
	}
	defer r.Fechar()
	aplicadas, err := r.MigrarRelatando()
	for _, n := range aplicadas {
		fmt.Fprintf(saida, "aplicada: %s\n", n)
	}
	if err != nil {
		return err
	}
	if len(aplicadas) == 0 {
		fmt.Fprintln(saida, "nenhuma migração pendente: o schema já está na versão do binário")
	}
	return nil
}

// abrirBancoCLI: DB_DSN do ambiente ou do service.env; ACUSUARIO_TABELA só em teste.
func abrirBancoCLI(cfg config.Config, arqEnv string) (*repo.MariaDB, config.Config, error) {
	if cfg.Texto("DB_DSN", "") == "" {
		c, err := lerServiceEnv(arqEnv, cfg)
		if err != nil {
			return nil, cfg, err
		}
		cfg = c
	}
	r, err := repo.AbrirMariaDB(cfg.Texto("DB_DSN", ""))
	if err != nil {
		return nil, cfg, err
	}
	if t := cfg.Texto("ACUSUARIO_TABELA", ""); t != "" {
		if err := r.UsarTabelaUsuario(t); err != nil {
			r.Fechar()
			return nil, cfg, err
		}
	}
	return r, cfg, nil
}

// conferirUsuario mostra, em cada ambiente, o que uma entrada desse uid usaria agora: a
// habilitação, a coluna do GQ resolvida (paper, ou conector, corretora e conta, com a trava) e se
// esta máquina permite o ambiente para ele (SPEC-AMBIENTES-v1 §4.2).
func conferirUsuario(r *repo.MariaDB, cfg config.Config, uid int, saida io.Writer) error {
	teto, _, err := cfg.LerAmbientes()
	if err != nil {
		return fmt.Errorf("configuração dos ambientes: %w", err)
	}
	hml, prd, achou, err := r.ConfigUsuario(uid)
	if err != nil {
		return err
	}
	if achou {
		// o que está no GQ, nas duas colunas
		fmt.Fprintf(saida, "acusuario: usutxtconfig %s; usutxtconfigprd %s\n", usuario.Lido(hml), usuario.Lido(prd))
	}
	for _, amb := range []string{dominio.AmbienteSimulador, dominio.AmbienteReal} {
		h, ok, err := r.Habilitacao(uid, amb)
		if err != nil {
			return err
		}
		permitido := teto.Permitidos[amb] && (amb != dominio.AmbienteReal || teto.UIDReal(uid))
		conectores := strings.ToLower(h.ConectoresTexto())
		if !ok {
			// sem linha não há conector habilitado (o PNT do ConectoresTexto é o padrão de linha vazia)
			conectores = "sem_linha"
		}
		fmt.Fprintf(saida, "uid=%d ambiente=%s habilitado=%v conectores=%s permitido_nesta_maquina=%v\n", uid, amb, ok && h.Habilitado, conectores, permitido)
		if !achou {
			fmt.Fprintf(saida, "%s: uid sem linha em acusuario → entradas recusadas (CONFIG_USUARIO_INVALIDA)\n", usuario.Campo(amb))
			continue
		}
		texto := hml
		if amb == dominio.AmbienteReal {
			texto = prd
		}
		// a visão do ambiente (v1.26: no REAL, o conector Flexscan é o das REAL_FLEXSCAN_*)
		v, _ := cfg.Visao(teto, amb)
		rg := usuario.Regras{Ambiente: amb, ContasHML: usuario.Lista(cfg.Texto("ORDENS_CONTAS_HML", "")),
			ContasHMLFlx: usuario.Lista(cfg.Texto("FLEXSCAN_CONTAS_HML", "")), Flexscan: v.Texto("FLEXSCAN_URL", "") != "",
			PnTNoSimulador: teto.PnTNoSimulador(), RealContas: teto.RealContas, RealContasTodas: teto.RealContasTodas}
		cs := h.Conectores
		if !ok {
			cs = []string{dominio.ConectorPnT, dominio.ConectorFlexscan} // sem linha: o que o GQ aponta
		}
		res, f := usuario.ResolverCom(rg, texto, cs)
		switch {
		case f.Paper():
			fmt.Fprintf(saida, "%s: paper (simulador interno do GQ): nenhuma ordem vai ao serviço\n", usuario.Campo(amb))
		case f != nil:
			fmt.Fprintf(saida, "%s, conectores habilitados %s: entradas recusadas (%s): %s\n", usuario.Campo(amb), conectores, f.Codigo, f.Motivo)
		case !ok:
			fmt.Fprintf(saida, "%s: o GQ aponta conector=%s corretora=%d conta=%s (%s); sem habilitação: habilitar com -ambiente %s -conectores %s\n",
				usuario.Campo(amb), res.Conector, res.Destino, res.Conta, res.OrigemConta, amb, strings.ToLower(res.Conector))
		default:
			fmt.Fprintf(saida, "%s: conector=%s corretora=%d conta=%s (%s)\n", usuario.Campo(amb), res.Conector, res.Destino, res.Conta, res.OrigemConta)
		}
	}
	return nil
}

// quem: o usuário do sistema que rodou a CLI (por sudo, o de origem).
func quem() string {
	for _, k := range []string{"SUDO_USER", "USER", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "?"
}

// lerServiceEnv lê CHAVE=valor (linhas # são comentário); o ambiente tem precedência.
func lerServiceEnv(caminho string, base config.Config) (config.Config, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return base, fmt.Errorf("DB_DSN fora do ambiente e %s ilegível (rodar com sudo?): %w", caminho, err)
	}
	defer f.Close()
	v := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		l := strings.TrimSpace(s.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if k, x, ok := strings.Cut(l, "="); ok {
			v[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(x), `"'`)
		}
	}
	for _, kv := range os.Environ() {
		if k, x, ok := strings.Cut(kv, "="); ok && x != "" {
			v[k] = x
		}
	}
	return config.Novo(v), s.Err()
}
