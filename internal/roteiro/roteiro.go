// Package roteiro lê os roteiros do PnT falso (cenarios/*.fix; formato em cenarios/FORMATO.md).
//
// Ler resolve blocos (executa), parâmetros ($PARAM), modelos (@MODELO) e confere a sintaxe de
// cada passo, inclusive se toda captura ($NOME) foi definida antes de ser usada. Executar os
// passos é com o executor, que conhece o serviço.
package roteiro

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Cenario é um roteiro pronto para executar.
type Cenario struct {
	Arquivo string
	Numero  string
	Titulo  string
	Hora    time.Time         // hora inicial (America/Sao_Paulo)
	Config  map[string]string // _comum.fix + cabeçalho do cenário
	Passos  []Passo
}

// Passo é uma instrução expandida.
type Passo struct {
	Origem string // arquivo:linha (e o bloco, se veio de um)
	Verbo  string
	Args   []string // tokens já sem aspas; em http, Args[2] é o resto da linha (json)
	// Campos de recebe/envia/envia_offline/recebe_sessao/nada: tag=valor (ou tag=>NOME).
	Campos []Campo
	// Em em_qualquer_ordem: os recebe do bloco.
	Filhos []Passo
}

// Campo de mensagem num passo.
type Campo struct {
	Tag     int
	Valor   string // pode conter $NOME (resolvido na execução)
	Captura string // não vazio em tag=>NOME
}

// Verbos conhecidos e quantos argumentos posicionais exigem (-1 = livre).
var verbos = map[string]int{
	"preco": -1, "avanca": 1,
	"recebe": -1, "envia": -1, "envia_offline": -1, "recebe_sessao": -1, "nada": -1,
	"em_qualquer_ordem": 0,
	"entrada":           -1, "saida": -1, "http": -1, "resposta": -1, "saude": -1,
	"evento": -1, "sem_evento": -1, "conta_eventos": -1, "alerta": 1, "sem_alerta": 1,
	"aguarda_logon": 0, "derruba_sessao": 0, "reinicia_servico": 0,
}

var verbosDeMensagem = map[string]bool{"recebe": true, "envia": true, "envia_offline": true, "recebe_sessao": true, "nada": true}

var camposPreco = map[string]bool{"ticker": true, "preco": true, "bid": true, "ask": true, "status84": true, "idade": true, "http": true}

type bloco struct {
	params []string
	linhas []linha
}

type linha struct {
	origem string
	texto  string
}

type leitor struct {
	modelos map[string][]Campo
	blocos  map[string]bloco
	config  map[string]string
}

// LerDiretorio lê _comum.fix e todos os NN-*.fix de dir, em ordem.
func LerDiretorio(dir string) ([]Cenario, error) {
	comum := filepath.Join(dir, "_comum.fix")
	arquivos, err := filepath.Glob(filepath.Join(dir, "[0-9]*.fix"))
	if err != nil {
		return nil, err
	}
	var r []Cenario
	for _, a := range arquivos {
		c, err := Ler(comum, a)
		if err != nil {
			return nil, err
		}
		r = append(r, c)
	}
	return r, nil
}

// Ler lê um cenário, carregando antes o arquivo comum.
func Ler(comum, arquivo string) (Cenario, error) {
	l := &leitor{modelos: map[string][]Campo{}, blocos: map[string]bloco{}, config: map[string]string{}}
	lc, err := lerLinhas(comum)
	if err != nil {
		return Cenario{}, err
	}
	if err := l.definicoes(lc); err != nil {
		return Cenario{}, err
	}
	la, err := lerLinhas(arquivo)
	if err != nil {
		return Cenario{}, err
	}
	c := Cenario{Arquivo: filepath.Base(arquivo), Config: map[string]string{}}
	for k, v := range l.config {
		c.Config[k] = v
	}
	var corpo []linha
	cabecalho := true
	for _, ln := range la {
		toks, err := tokens(ln.texto)
		if err != nil {
			return c, fmt.Errorf("%s: %w", ln.origem, err)
		}
		switch {
		case cabecalho && toks[0] == "cenario":
			if len(toks) < 3 {
				return c, fmt.Errorf("%s: cenario NN Título", ln.origem)
			}
			c.Numero = toks[1]
			c.Titulo = strings.TrimSpace(strings.SplitN(ln.texto, toks[1], 2)[1])
		case cabecalho && toks[0] == "hora":
			loc, err := time.LoadLocation("America/Sao_Paulo")
			if err != nil {
				return c, err
			}
			if len(toks) != 3 {
				return c, fmt.Errorf("%s: hora AAAA-MM-DD HH:MM:SS", ln.origem)
			}
			h, err := time.ParseInLocation("2006-01-02 15:04:05", toks[1]+" "+toks[2], loc)
			if err != nil {
				return c, fmt.Errorf("%s: %w", ln.origem, err)
			}
			c.Hora = h
		case cabecalho && toks[0] == "config":
			k, v, ok := strings.Cut(strings.Join(toks[1:], " "), "=")
			if !ok {
				return c, fmt.Errorf("%s: config CHAVE=valor", ln.origem)
			}
			c.Config[k] = v
		default:
			cabecalho = false
			corpo = append(corpo, ln)
		}
	}
	if c.Numero == "" || c.Hora.IsZero() {
		return c, fmt.Errorf("%s: falta 'cenario' ou 'hora' no cabeçalho", arquivo)
	}
	capturas := map[string]bool{}
	passos, err := l.expandir(corpo, nil, capturas, 0)
	if err != nil {
		return c, err
	}
	c.Passos = passos
	return c, nil
}

// definicoes lê config, modelo e bloco do arquivo comum.
func (l *leitor) definicoes(ls []linha) error {
	for i := 0; i < len(ls); i++ {
		toks, err := tokens(ls[i].texto)
		if err != nil {
			return fmt.Errorf("%s: %w", ls[i].origem, err)
		}
		switch toks[0] {
		case "config":
			k, v, ok := strings.Cut(strings.Join(toks[1:], " "), "=")
			if !ok {
				return fmt.Errorf("%s: config CHAVE=valor", ls[i].origem)
			}
			l.config[k] = v
		case "modelo":
			if len(toks) < 3 {
				return fmt.Errorf("%s: modelo NOME tag=valor ...", ls[i].origem)
			}
			cs, err := l.campos(toks[2:], nil, ls[i].origem)
			if err != nil {
				return err
			}
			l.modelos[toks[1]] = cs
		case "bloco":
			if len(toks) < 2 {
				return fmt.Errorf("%s: bloco NOME [PARAM...]", ls[i].origem)
			}
			b := bloco{params: toks[2:]}
			j := i + 1
			for ; j < len(ls); j++ {
				if strings.TrimSpace(ls[j].texto) == "fim" {
					break
				}
				b.linhas = append(b.linhas, ls[j])
			}
			if j == len(ls) {
				return fmt.Errorf("%s: bloco %s sem fim", ls[i].origem, toks[1])
			}
			l.blocos[toks[1]] = b
			i = j
		default:
			return fmt.Errorf("%s: só config, modelo e bloco no arquivo comum", ls[i].origem)
		}
	}
	return nil
}

// expandir transforma linhas em passos, resolvendo executa e @MODELO.
func (l *leitor) expandir(ls []linha, params map[string]string, capturas map[string]bool, prof int) ([]Passo, error) {
	if prof > 5 {
		return nil, fmt.Errorf("blocos aninhados demais")
	}
	var r []Passo
	for i := 0; i < len(ls); i++ {
		ln := ls[i]
		texto := substituirParams(ln.texto, params)
		toks, err := tokens(texto)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ln.origem, err)
		}
		verbo := toks[0]
		switch verbo {
		case "executa":
			if len(toks) < 2 {
				return nil, fmt.Errorf("%s: executa NOME [PARAM=valor...]", ln.origem)
			}
			b, ok := l.blocos[toks[1]]
			if !ok {
				return nil, fmt.Errorf("%s: bloco %q não existe", ln.origem, toks[1])
			}
			ps := map[string]string{}
			for _, kv := range toks[2:] {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return nil, fmt.Errorf("%s: parâmetro %q", ln.origem, kv)
				}
				ps[k] = v
			}
			for _, p := range b.params {
				if _, ok := ps[p]; !ok {
					return nil, fmt.Errorf("%s: bloco %s exige %s", ln.origem, toks[1], p)
				}
			}
			var internas []linha
			for _, bl := range b.linhas {
				internas = append(internas, linha{origem: ln.origem + " → " + bl.origem, texto: bl.texto})
			}
			sub, err := l.expandir(internas, ps, capturas, prof+1)
			if err != nil {
				return nil, err
			}
			r = append(r, sub...)
			continue
		case "em_qualquer_ordem":
			j := i + 1
			var dentro []linha
			for ; j < len(ls); j++ {
				if strings.TrimSpace(ls[j].texto) == "fim" {
					break
				}
				dentro = append(dentro, ls[j])
			}
			if j == len(ls) {
				return nil, fmt.Errorf("%s: em_qualquer_ordem sem fim", ln.origem)
			}
			filhos, err := l.expandir(dentro, params, capturas, prof+1)
			if err != nil {
				return nil, err
			}
			for _, f := range filhos {
				if f.Verbo != "recebe" {
					return nil, fmt.Errorf("%s: só recebe dentro de em_qualquer_ordem", f.Origem)
				}
			}
			r = append(r, Passo{Origem: ln.origem, Verbo: verbo, Filhos: filhos})
			i = j
			continue
		}
		n, ok := verbos[verbo]
		if !ok {
			return nil, fmt.Errorf("%s: verbo desconhecido %q", ln.origem, verbo)
		}
		p := Passo{Origem: ln.origem, Verbo: verbo}
		if verbo == "http" {
			partes := strings.SplitN(strings.TrimSpace(texto), " ", 4)
			if len(partes) < 3 {
				return nil, fmt.Errorf("%s: http METODO /caminho [json]", ln.origem)
			}
			p.Args = partes[1:]
			r = append(r, p)
			continue
		}
		p.Args = toks[1:]
		if n >= 0 && len(p.Args) != n {
			return nil, fmt.Errorf("%s: %s espera %d argumento(s)", ln.origem, verbo, n)
		}
		if verbosDeMensagem[verbo] {
			cs, err := l.campos(p.Args, capturas, ln.origem)
			if err != nil {
				return nil, err
			}
			if verbo != "nada" && verbo != "recebe_sessao" && !tem35(cs) {
				return nil, fmt.Errorf("%s: %s sem 35", ln.origem, verbo)
			}
			if verbo != "recebe" {
				for _, c := range cs {
					if c.Captura != "" {
						return nil, fmt.Errorf("%s: captura só em recebe", ln.origem)
					}
				}
			}
			p.Campos = cs
			p.Args = nil
		}
		if err := conferir(p, capturas); err != nil {
			return nil, err
		}
		r = append(r, p)
	}
	return r, nil
}

var reCampo = regexp.MustCompile(`^(\d+)=(>?)(.*)$`)

// RefRe casa $NOME.
var RefRe = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
var reRef = RefRe

// campos interpreta tag=valor, tag=>NOME e @MODELO (que se expande e é sobrescrito pelo que vem depois).
func (l *leitor) campos(toks []string, capturas map[string]bool, origem string) ([]Campo, error) {
	var r []Campo
	for _, t := range toks {
		if strings.HasPrefix(t, "@") {
			m, ok := l.modelos[t[1:]]
			if !ok {
				return nil, fmt.Errorf("%s: modelo %q não existe", origem, t[1:])
			}
			r = append(r, m...)
			continue
		}
		g := reCampo.FindStringSubmatch(t)
		if g == nil {
			return nil, fmt.Errorf("%s: campo inválido %q (esperado tag=valor)", origem, t)
		}
		tag, _ := strconv.Atoi(g[1])
		c := Campo{Tag: tag}
		if g[2] == ">" {
			c.Captura = g[3]
			if capturas != nil {
				capturas[c.Captura] = true
			}
		} else {
			c.Valor = g[3]
			if capturas != nil {
				for _, ref := range reRef.FindAllStringSubmatch(c.Valor, -1) {
					if !capturas[ref[1]] {
						return nil, fmt.Errorf("%s: $%s usado antes de ser capturado", origem, ref[1])
					}
				}
			}
		}
		// valor posterior sobrescreve o do modelo (mesma tag), mantendo a posição do modelo
		sobrescreveu := false
		for i := range r {
			if r[i].Tag == tag && c.Captura == "" && r[i].Captura == "" {
				r[i] = c
				sobrescreveu = true
				break
			}
		}
		if !sobrescreveu {
			r = append(r, c)
		}
	}
	return r, nil
}

// conferir valida os argumentos de cada verbo.
func conferir(p Passo, capturas map[string]bool) error {
	switch p.Verbo {
	case "avanca":
		if _, err := Duracao(p.Args[0]); err != nil {
			return fmt.Errorf("%s: %w", p.Origem, err)
		}
	case "preco":
		if len(p.Args) == 0 {
			return fmt.Errorf("%s: preco sem campos", p.Origem)
		}
		if len(p.Args) == 1 && (p.Args[0] == "vazio" || p.Args[0] == "normal") {
			return nil
		}
		for _, a := range p.Args {
			k, v, ok := strings.Cut(a, "=")
			if !ok || !camposPreco[k] {
				return fmt.Errorf("%s: campo de preco inválido %q", p.Origem, a)
			}
			if k == "idade" {
				if _, err := Duracao(v); err != nil {
					return fmt.Errorf("%s: %w", p.Origem, err)
				}
			}
		}
	case "entrada":
		if len(p.Args) < 2 || (p.Args[1] != "C" && p.Args[1] != "V") {
			return fmt.Errorf("%s: entrada OP C|V [qtd=] [stop=] [motivo=]", p.Origem)
		}
	case "saida":
		if len(p.Args) < 3 || (p.Args[1] != "C" && p.Args[1] != "V") {
			return fmt.Errorf("%s: saida OP C|V MOTIVO [qtd=]", p.Origem)
		}
	case "resposta", "saude", "evento", "sem_evento":
		inicio := 0
		if p.Verbo == "evento" || p.Verbo == "sem_evento" {
			if len(p.Args) == 0 {
				return fmt.Errorf("%s: %s TIPO [campo=valor...]", p.Origem, p.Verbo)
			}
			inicio = 1
		}
		for _, a := range p.Args[inicio:] {
			if !strings.Contains(a, "=") {
				return fmt.Errorf("%s: %q devia ser campo=valor", p.Origem, a)
			}
		}
	case "conta_eventos":
		n := len(p.Args)
		if n < 3 || p.Args[n-2] != "=" {
			return fmt.Errorf("%s: conta_eventos TIPO [campo=valor...] = N", p.Origem)
		}
		if _, err := strconv.Atoi(p.Args[n-1]); err != nil {
			return fmt.Errorf("%s: contagem %q", p.Origem, p.Args[n-1])
		}
	}
	_ = capturas
	return nil
}

// Duracao aceita 500ms, 3s, 3.1s, 1m.
func Duracao(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("duração inválida %q", s)
	}
	return d, nil
}

func tem35(cs []Campo) bool {
	for _, c := range cs {
		if c.Tag == 35 {
			return true
		}
	}
	return false
}

func substituirParams(s string, ps map[string]string) string {
	if len(ps) == 0 {
		return s
	}
	return reRef.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := ps[m[1:]]; ok {
			return v
		}
		return m
	})
}

// tokens separa por espaço, respeitando aspas: 58="a b" vira um token 58=a b.
func tokens(s string) ([]string, error) {
	var r []string
	var cur strings.Builder
	dentro := false
	tem := false
	for _, ch := range s {
		switch {
		case ch == '"':
			dentro = !dentro
			tem = true
		case (ch == ' ' || ch == '\t') && !dentro:
			if tem {
				r = append(r, cur.String())
				cur.Reset()
				tem = false
			}
		default:
			cur.WriteRune(ch)
			tem = true
		}
	}
	if dentro {
		return nil, fmt.Errorf("aspas não fechadas")
	}
	if tem {
		r = append(r, cur.String())
	}
	if len(r) == 0 {
		return nil, fmt.Errorf("linha vazia")
	}
	return r, nil
}

func lerLinhas(arquivo string) ([]linha, error) {
	f, err := os.Open(arquivo)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r []linha
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	base := filepath.Base(arquivo)
	for s.Scan() {
		n++
		t := strings.TrimSpace(s.Text())
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		r = append(r, linha{origem: fmt.Sprintf("%s:%d", base, n), texto: t})
	}
	return r, s.Err()
}
