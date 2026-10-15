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
//	kalman-ordens usuario listar
//	kalman-ordens usuario conferir    -uid N
//	kalman-ordens usuario habilitar   -uid N -motivo X [-posicao-max N] [-qtd-max N]
//	kalman-ordens usuario desabilitar -uid N -motivo X
func cliUsuario(cfg config.Config, args []string, saida io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usuario listar | conferir | habilitar | desabilitar")
	}
	acao, args := args[0], args[1:]
	fs := flag.NewFlagSet("usuario "+acao, flag.ContinueOnError)
	uid := fs.Int("uid", 0, "uid do Kalman (o mesmo do GQ)")
	motivo := fs.String("motivo", "", "motivo (obrigatório em habilitar e desabilitar)")
	posMax := fs.Int("posicao-max", 0, "limite de posição do uid (0 = só o global)")
	qtdMax := fs.Int("qtd-max", 0, "limite de quantidade por ordem do uid (0 = só o global)")
	arqEnv := fs.String("env", "/etc/kalman-ordens/service.env", "service.env, se DB_DSN não estiver no ambiente")
	if err := fs.Parse(args); err != nil {
		return err
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
			fmt.Fprintf(saida, "uid=%d habilitado=%v posicao_max=%d qtd_max=%d por=%s em=%s motivo=%q\n",
				h.UID, h.Habilitado, h.PosicaoMax, h.QtdMaxOrdem, h.Por, h.Em.Format(time.RFC3339), h.Motivo)
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
		h := dominio.Habilitacao{UID: *uid, Habilitado: acao == "habilitar", PosicaoMax: *posMax, QtdMaxOrdem: *qtdMax,
			Por: quem(), Motivo: *motivo, Em: time.Now()}
		if err := r.GravarHabilitacao(h); err != nil {
			return err
		}
		fmt.Fprintf(saida, "uid=%d habilitado=%v por=%s\n", h.UID, h.Habilitado, h.Por)
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

// conferirUsuario mostra o que uma entrada desse uid usaria agora neste ambiente.
func conferirUsuario(r *repo.MariaDB, cfg config.Config, uid int, saida io.Writer) error {
	ambiente := cfg.Texto("ORDENS_AMBIENTE", "")
	h, ok, err := r.Habilitacao(uid)
	if err != nil {
		return err
	}
	fmt.Fprintf(saida, "uid=%d ambiente=%s habilitado=%v\n", uid, ambiente, ok && h.Habilitado)
	hml, prd, achou, err := r.ConfigUsuario(uid)
	if err != nil {
		return err
	}
	if !achou {
		fmt.Fprintf(saida, "acusuario: uid sem linha → entradas recusadas (CONFIG_USUARIO_INVALIDA)\n")
		return nil
	}
	texto := hml
	if ambiente == usuario.Producao {
		texto = prd
	}
	res, f := usuario.Resolver(ambiente, texto, usuario.Lista(cfg.Texto("ORDENS_CONTAS_HML", "")))
	if f != nil {
		fmt.Fprintf(saida, "%s: entradas recusadas (%s): %s\n", usuario.Campo(ambiente), f.Codigo, f.Motivo)
		return nil
	}
	fmt.Fprintf(saida, "%s: destino=%d conta=%s (vai no 1 e no 116)\n", usuario.Campo(ambiente), res.Destino, res.Conta)
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
