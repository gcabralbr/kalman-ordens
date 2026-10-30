package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// v1.30 (SPEC-AMBIENTES-v1 D4 revisada, passo 2b): na produção, a Flexscan do REAL usa um usuário
// guarda-chuva por corretora, com a conta do cliente em cada ordem. REAL_FLEXSCAN_CORRETORAS
// ("88:CMC,85:BTG": CORSeqId:NOME) liga uma ponte por corretora; cada NOME tem as suas chaves
// REAL_FLEXSCAN_<NOME>_URL (ou a REAL_FLEXSCAN_URL comum), _USUARIO, _SENHA, _CONTAS (as contas
// permitidas nesse guarda-chuva), _WEBHOOK_SEGREDO, _WEBHOOK_PREFIXO, _WEBHOOK_URL, _ESTADO_LOGIN.
// Vazia: a ponte única de sempre (REAL_FLEXSCAN_*).

// CorretoraFlexscan: um guarda-chuva Flexscan do REAL.
type CorretoraFlexscan struct {
	Codigo int    // Robo.CORSeqId
	Nome   string // CMC, BTG...
	Contas []string
}

var reNomeCorretora = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// chavesCorretora: as chaves do conector que cada corretora tem (o sufixo depois de FLEXSCAN_).
var chavesCorretora = []string{"URL", "USUARIO", "SENHA", "WEBHOOK_SEGREDO", "WEBHOOK_PREFIXO", "WEBHOOK_URL", "ESTADO_LOGIN"}

// CorretorasFlexscan: REAL_FLEXSCAN_CORRETORAS conferida (nil = a ponte única). Lida da
// configuração inteira (as chaves REAL_).
func (c Config) CorretorasFlexscan() ([]CorretoraFlexscan, error) {
	bruto := strings.TrimSpace(c.Texto("REAL_FLEXSCAN_CORRETORAS", ""))
	if bruto == "" {
		return nil, nil
	}
	if c.Texto("REAL_FLEXSCAN_USUARIO", "") != "" || c.Texto("REAL_FLEXSCAN_SENHA", "") != "" {
		return nil, fmt.Errorf("REAL_FLEXSCAN_CORRETORAS com REAL_FLEXSCAN_USUARIO/REAL_FLEXSCAN_SENHA: as credenciais são por corretora (REAL_FLEXSCAN_<NOME>_USUARIO)")
	}
	var r []CorretoraFlexscan
	codigos, nomes, donoConta := map[int]bool{}, map[string]bool{}, map[string]string{}
	for _, item := range strings.Split(bruto, ",") {
		cod, nome, ok := strings.Cut(strings.TrimSpace(item), ":")
		n, err := strconv.Atoi(strings.TrimSpace(cod))
		nome = strings.ToUpper(strings.TrimSpace(nome))
		if !ok || err != nil || n <= 0 || !reNomeCorretora.MatchString(nome) {
			return nil, fmt.Errorf("REAL_FLEXSCAN_CORRETORAS %q: esperado CORSeqId:NOME (ex.: 88:CMC,85:BTG; NOME de 2 a 10 letras e dígitos)", bruto)
		}
		if codigos[n] || nomes[nome] {
			return nil, fmt.Errorf("REAL_FLEXSCAN_CORRETORAS %q: corretora %d ou nome %s repetido", bruto, n, nome)
		}
		codigos[n], nomes[nome] = true, true
		k := func(s string) string { return "REAL_FLEXSCAN_" + nome + "_" + s }
		var faltam []string
		for _, s := range []string{"USUARIO", "SENHA", "CONTAS"} {
			if c.Texto(k(s), "") == "" {
				faltam = append(faltam, k(s))
			}
		}
		if c.Texto(k("URL"), "") == "" && c.Texto("REAL_FLEXSCAN_URL", "") == "" {
			faltam = append(faltam, k("URL")+" (ou REAL_FLEXSCAN_URL)")
		}
		if len(faltam) > 0 {
			return nil, fmt.Errorf("corretora %s (%d): configuração ausente: %v", nome, n, faltam)
		}
		cr := CorretoraFlexscan{Codigo: n, Nome: nome}
		for _, conta := range strings.Split(c.Texto(k("CONTAS"), ""), ",") {
			if conta = strings.TrimSpace(conta); conta == "" {
				continue
			}
			if conta == "*" {
				return nil, fmt.Errorf("%s: a lista de contas permitidas é explícita (sem *)", k("CONTAS"))
			}
			if outra := donoConta[conta]; outra != "" {
				return nil, fmt.Errorf("conta %s em %s e em REAL_FLEXSCAN_%s_CONTAS: cada conta num guarda-chuva só", conta, k("CONTAS"), outra)
			}
			donoConta[conta] = nome
			cr.Contas = append(cr.Contas, conta)
		}
		r = append(r, cr)
	}
	return r, nil
}

// VisaoCorretora: a visão do REAL (c) com as chaves do conector Flexscan da corretora no lugar das
// REAL_FLEXSCAN_ (a URL comum vale quando a da corretora não vem). Os caminhos em disco e o prefixo
// do webhook têm padrões próprios por corretora.
func (c Config) VisaoCorretora(cr CorretoraFlexscan) Config {
	v := Novo(c.v)
	min := strings.ToLower(cr.Nome)
	padroes := map[string]string{
		"URL":             c.Texto("FLEXSCAN_URL", ""),
		"WEBHOOK_PREFIXO": "/flexscan/webhook-real-" + min + "/",
		"ESTADO_LOGIN":    "/var/lib/kalman-ordens/flexscan-login-real-" + min + ".json",
	}
	for _, s := range chavesCorretora {
		delete(v.v, "FLEXSCAN_"+s)
		if x := c.Texto("REAL_FLEXSCAN_"+cr.Nome+"_"+s, padroes[s]); x != "" {
			v.v["FLEXSCAN_"+s] = x
		}
	}
	return v
}

// Contem: a conta está entre as permitidas no guarda-chuva.
func (cr CorretoraFlexscan) Contem(conta string) bool {
	for _, x := range cr.Contas {
		if x == conta {
			return true
		}
	}
	return false
}
