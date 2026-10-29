// Package usuario resolve conta e destino de um usuário a partir da configuração do GQ
// (dbecotools.acusuario), SPEC v1.2 U1–U3. Função pura: quem lê a tabela é o repositório.
//
// SPEC-AMBIENTES-v1: quem escolhe a coluna é o ambiente do login do usuário no GQ, que vem em cada
// ordem: SIMULADOR lê usutxtconfig, REAL lê usutxtconfigprd. Do JSON só importa o grupo "Robo" (e a
// conta de futuros).
package usuario

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// Os valores de ORDENS_AMBIENTE (a equivalência da transição, SPEC-AMBIENTES-v1 §3.1): HML é o
// SIMULADOR com o PnT, PRODUCAO é o REAL para todas as contas (a trava cruzada de antes).
const (
	HML      = "HML"
	Producao = "PRODUCAO"
)

// Destinos (Robo.CORSeqId): a corretora. Não decide o caminho das ordens (SPEC-FLEXSCAN-v1 §14):
// o conector vem da habilitação. A mesma corretora pode sair pelo PnT ou pela Flexscan.
const (
	DestinoBTG     = dominio.DestinoBTG     // 85
	DestinoCapital = dominio.DestinoCapital // 88
	DestinoGQ      = 9999                   // simulador interno do GQ: no SIMULADOR com a conta QUANT, paper
	// DestinoPnT: o nome antigo do 85 (testes e textos de antes)
	DestinoPnT = DestinoBTG
	// ContaPaper: a conta do simulador interno do GQ (SPEC-AMBIENTES-v1 §0.1)
	ContaPaper = "QUANT"
)

// corretoras conhecidas; outra entra aqui quando for usada (um CORSeqId errado no GQ não vira ordem)
var corretoras = map[int]string{DestinoBTG: "BTG", DestinoCapital: "Capital"}

// Corretora: o nome da corretora (85 BTG, 88 Capital); "" se desconhecida.
func Corretora(destino int) string { return corretoras[destino] }

// Resolvido: conta, destino e conector de uma entrada.
type Resolvido struct {
	Conta       string
	Destino     int
	Conector    string
	OrigemConta string // Futuros.CCONroContaFut (WIN) ou Robo.CCONroConta (sem o grupo Futuros)
}

// Regras para resolver um usuário num ambiente: o ambiente da ordem, as contas de simulador de
// cada conector (a trava), se o conector Flexscan está configurado e o teto do REAL.
type Regras struct {
	Ambiente     string   // SIMULADOR ou REAL (HML e PRODUCAO: a equivalência da transição)
	ContasHML    []string // contas de simulador do PnT (ORDENS_CONTAS_HML)
	ContasHMLFlx []string // contas de simulador da Flexscan (FLEXSCAN_CONTAS_HML)
	Flexscan     bool     // FLEXSCAN_URL preenchida
	// o PnT no SIMULADOR: só na equivalência ORDENS_AMBIENTE=HML (a HML do PnT é D-1)
	PnTNoSimulador bool
	// REAL_CONTAS: lista ou todas (*)
	RealContas      []string
	RealContasTodas bool
}

// Falha: por que não dá para mandar ordem real. Codigo é a recusa devolvida ao gestor; Alerta
// diz se também vai alerta (código do catálogo; CONTA_AMBIENTE é crítico).
type Falha struct {
	Codigo string
	Motivo string
	Alerta string // "" = sem alerta
}

func (f *Falha) Error() string { return f.Codigo + ": " + f.Motivo }

// Paper: a falha é o destino paper (o simulador interno do GQ), não um erro de configuração.
func (f *Falha) Paper() bool { return f != nil && f.Codigo == "DESTINO_PAPER" }

var soDigitos = regexp.MustCompile(`^[0-9]+$`)

// contaFlexscan: a conta pela Flexscan tem letra e hífen no simulador (S-1117150) e é numérica na
// Capital em produção (715124).
var contaFlexscan = regexp.MustCompile(`^[A-Za-z0-9-]{1,20}$`)

// Campo devolve o nome da coluna lida no ambiente.
func Campo(ambiente string) string {
	if ambiente == Producao || ambiente == dominio.AmbienteReal {
		return "usutxtconfigprd"
	}
	return "usutxtconfig"
}

// Resolver: pelo PnT (as regras de antes).
func Resolver(ambiente, texto string, contasHML []string) (Resolvido, *Falha) {
	return ResolverCom(Regras{Ambiente: ambiente, ContasHML: contasHML}, texto, []string{dominio.ConectorPnT})
}

// normalizar: HML e PRODUCAO (a equivalência da transição) viram SIMULADOR com o PnT e REAL para
// todas as contas.
func (rg Regras) normalizar() Regras {
	switch rg.Ambiente {
	case HML:
		rg.Ambiente, rg.PnTNoSimulador = dominio.AmbienteSimulador, true
	case Producao:
		rg.Ambiente, rg.RealContasTodas = dominio.AmbienteReal, true
	}
	return rg
}

// ResolverCom interpreta o JSON da coluna do ambiente, escolhe o conector e aplica as travas.
//   - Paper (SPEC-AMBIENTES-v1 §0.1): no SIMULADOR, CORSeqId 9999 com a conta QUANT é o simulador
//     interno do GQ → DESTINO_PAPER (nenhuma ordem); 9999 com outra conta → CONFIG_USUARIO_INVALIDA;
//     no REAL, 9999 → DESTINO_INVALIDO.
//   - Conector (SPEC-FLEXSCAN-v1 §14.3): o GQ com Robo.Conector → tem de estar entre os da
//     habilitação (CONECTOR_NAO_HABILITADO); sem → o da habilitação, se for um só (vazia = PnT),
//     senão CONECTOR_INDEFINIDO. A Flexscan só com FLEXSCAN_URL; o PnT só no REAL
//     (CONECTOR_AMBIENTE), salvo na equivalência HML.
//   - Conta pelo conector: PnT só dígitos, Flexscan até 20 letras, dígitos e hífen.
//   - Trava (§4.1 item 8): no SIMULADOR a conta tem de estar na lista de simulador do conector; no
//     REAL não pode estar em nenhuma lista de simulador (CONTA_AMBIENTE) e tem de estar em
//     REAL_CONTAS (REAL_CONTA_NAO_PERMITIDA).
func ResolverCom(rg Regras, texto string, habilitados []string) (Resolvido, *Falha) {
	rg = rg.normalizar()
	ambiente := rg.Ambiente
	if ambiente != dominio.AmbienteSimulador && ambiente != dominio.AmbienteReal {
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("ambiente %q inválido (SIMULADOR ou REAL)", ambiente), "CONTA_AMBIENTE"}
	}
	campo := Campo(ambiente)
	if strings.TrimSpace(texto) == "" {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + " vazio", "USUARIO_CONFIG"}
	}
	// só os campos usados: o JSON tem credenciais (Robo.StrSenhaRobo, Robo.CCOTxtUser) e nunca vai
	// inteiro para log, recusa ou banco
	var cfg configGQ
	if err := json.Unmarshal([]byte(texto), &cfg); err != nil {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": JSON inválido (" + erroJSON(err) + ")", "USUARIO_CONFIG"}
	}
	if cfg.Robo == nil || len(cfg.Robo.CORSeqId) == 0 {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": sem Robo.CORSeqId", "USUARIO_CONFIG"}
	}
	var destino int
	if err := json.Unmarshal(cfg.Robo.CORSeqId, &destino); err != nil {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": Robo.CORSeqId não é inteiro: " + string(cfg.Robo.CORSeqId), "USUARIO_CONFIG"}
	}
	switch {
	case destino == DestinoGQ && ambiente == dominio.AmbienteReal:
		return Resolvido{}, &Falha{"DESTINO_INVALIDO", fmt.Sprintf("%s: Robo.CORSeqId %d é o simulador do GQ; no REAL, só uma corretora (85 BTG, 88 Capital)", campo, destino), "USUARIO_CONFIG"}
	case destino == DestinoGQ:
		conta, _, f := cfg.conta(campo)
		switch {
		case f != nil:
			return Resolvido{}, f
		case conta == ContaPaper:
			return Resolvido{Conta: conta, Destino: destino}, &Falha{"DESTINO_PAPER", fmt.Sprintf("%s: simulador interno do GQ (9999, conta %s): paper, nenhuma ordem vai ao serviço", campo, ContaPaper), ""}
		}
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", fmt.Sprintf("%s: Robo.CORSeqId 9999 (simulador do GQ) sem a conta %s (conta %q)", campo, ContaPaper, conta), "USUARIO_CONFIG"}
	case corretoras[destino] == "":
		return Resolvido{}, &Falha{"DESTINO_INVALIDO", fmt.Sprintf("%s: Robo.CORSeqId %d não é uma corretora conhecida (85 BTG, 88 Capital)", campo, destino), "USUARIO_CONFIG"}
	}
	// o conector: o GQ escolhe (Robo.Conector, gerado pelo GX desde 23/10) entre os da habilitação
	if len(habilitados) == 0 {
		habilitados = []string{dominio.ConectorPnT}
	}
	lista := strings.ToLower(strings.Join(habilitados, ","))
	var gq string
	if len(cfg.Robo.Conector) > 0 && string(cfg.Robo.Conector) != "null" {
		if err := json.Unmarshal(cfg.Robo.Conector, &gq); err != nil {
			return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": Robo.Conector não é texto", "USUARIO_CONFIG"}
		}
		gq = strings.ToUpper(strings.TrimSpace(gq))
	}
	var conector string
	switch {
	case gq != "":
		for _, h := range habilitados {
			if h == gq {
				conector = gq
			}
		}
		if conector == "" {
			return Resolvido{}, &Falha{"CONECTOR_NAO_HABILITADO", fmt.Sprintf("%s: Robo.Conector %s fora da habilitação do uid (%s)", campo, gq, lista), ""}
		}
	case len(habilitados) == 1:
		conector = habilitados[0]
	default:
		return Resolvido{}, &Falha{"CONECTOR_INDEFINIDO", fmt.Sprintf("%s: sem Robo.Conector no GQ e a habilitação tem mais de um conector (%s)", campo, lista), ""}
	}
	contasSim, listaSim := rg.ContasHML, "ORDENS_CONTAS_HML"
	switch conector {
	case dominio.ConectorPnT:
		if ambiente == dominio.AmbienteSimulador && !rg.PnTNoSimulador {
			return Resolvido{}, &Falha{"CONECTOR_AMBIENTE", "o PnT só opera no REAL (a HML do PnT é D-1)", ""}
		}
	case dominio.ConectorFlexscan:
		if !rg.Flexscan {
			return Resolvido{}, &Falha{"CONECTOR_NAO_CONFIGURADO", "conector FLEXSCAN na habilitação, mas esta instância não tem FLEXSCAN_URL", ""}
		}
		contasSim, listaSim = rg.ContasHMLFlx, "FLEXSCAN_CONTAS_HML"
	default:
		return Resolvido{}, &Falha{"CONECTOR_NAO_CONFIGURADO", fmt.Sprintf("conector %q desconhecido", conector), ""}
	}
	conta, origem, f := cfg.conta(campo)
	if f != nil {
		return Resolvido{}, f
	}
	switch {
	case conector == dominio.ConectorFlexscan && !contaFlexscan.MatchString(conta):
		return Resolvido{}, &Falha{"CONTA_INVALIDA", fmt.Sprintf("%s: conector FLEXSCAN: conta %q (%s) inválida (até 20 letras, dígitos e hífen)", campo, conta, origem), "USUARIO_CONFIG"}
	case conector == dominio.ConectorPnT && !soDigitos.MatchString(conta):
		return Resolvido{}, &Falha{"CONTA_INVALIDA", fmt.Sprintf("%s: conector PNT: conta %q (%s) não é só dígitos", campo, conta, origem), "USUARIO_CONFIG"}
	}
	if ambiente == dominio.AmbienteSimulador {
		if !contem(contasSim, conta) {
			return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta %s fora de %s no SIMULADOR", conta, listaSim), "CONTA_AMBIENTE"}
		}
	} else {
		// REAL: nenhuma conta de simulador, de nenhum conector
		if contem(rg.ContasHML, conta) || contem(rg.ContasHMLFlx, conta) {
			return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta de simulador %s no REAL", conta), "CONTA_AMBIENTE"}
		}
		if !rg.RealContasTodas && !contem(rg.RealContas, conta) {
			return Resolvido{}, &Falha{"REAL_CONTA_NAO_PERMITIDA", fmt.Sprintf("conta %s fora de REAL_CONTAS nesta máquina", conta), "REAL_CONTA_NAO_PERMITIDA"}
		}
	}
	return Resolvido{Conta: conta, Destino: destino, Conector: conector, OrigemConta: origem}, nil
}

func contem(l []string, x string) bool {
	for _, c := range l {
		if c == x {
			return true
		}
	}
	return false
}

// conta: a de WIN (ativo de futuros) é a Futuros.CCONroContaFut; sem o grupo Futuros (ou com ela
// vazia), a Robo.CCONroConta, como antes.
func (cfg configGQ) conta(campo string) (conta, origem string, f *Falha) {
	origem = "Futuros.CCONroContaFut"
	if cfg.Futuros != nil && len(cfg.Futuros.CCONroContaFut) > 0 && string(cfg.Futuros.CCONroContaFut) != "null" {
		if err := json.Unmarshal(cfg.Futuros.CCONroContaFut, &conta); err != nil {
			return "", "", &Falha{"CONTA_INVALIDA", campo + ": Futuros.CCONroContaFut não é texto", "USUARIO_CONFIG"}
		}
		conta = strings.TrimSpace(conta)
	}
	if conta == "" {
		origem = "Robo.CCONroConta"
		if err := json.Unmarshal(cfg.Robo.CCONroConta, &conta); err != nil {
			return "", "", &Falha{"CONTA_INVALIDA", campo + ": sem Futuros.CCONroContaFut e Robo.CCONroConta ausente ou não é texto", "USUARIO_CONFIG"}
		}
		conta = strings.TrimSpace(conta)
	}
	return conta, origem, nil
}

// configGQ: os campos do usutxtconfig que o serviço usa (o resto, inclusive as credenciais do Robo,
// não é lido).
type configGQ struct {
	Robo *struct {
		CORSeqId    json.RawMessage `json:"CORSeqId"`
		CCONroConta json.RawMessage `json:"CCONroConta"`
		Conector    json.RawMessage `json:"Conector"`
	} `json:"Robo"`
	Futuros *struct {
		CCONroContaFut json.RawMessage `json:"CCONroContaFut"`
	} `json:"Futuros"`
}

// erroJSON: o erro do json sem trecho do conteúdo (só o tipo e a posição).
func erroJSON(err error) string {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Sprintf("sintaxe na posição %d", se.Offset)
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return fmt.Sprintf("tipo errado em %s", te.Field)
	}
	return "ilegível"
}

// Lido: os campos usados de uma coluna, crus (para a CLI mostrar o que está no GQ) — nunca o JSON
// inteiro.
func Lido(texto string) string {
	if strings.TrimSpace(texto) == "" {
		return "vazio"
	}
	var cfg configGQ
	if err := json.Unmarshal([]byte(texto), &cfg); err != nil {
		return "JSON inválido (" + erroJSON(err) + ")"
	}
	if cfg.Robo == nil {
		return "sem Robo"
	}
	r := fmt.Sprintf("CORSeqId=%s Conector=%s CCONroConta=%s", cru(cfg.Robo.CORSeqId), cru(cfg.Robo.Conector), cru(cfg.Robo.CCONroConta))
	if cfg.Futuros != nil {
		r += " CCONroContaFut=" + cru(cfg.Futuros.CCONroContaFut)
	}
	return r
}

func cru(b json.RawMessage) string {
	if len(b) == 0 {
		return "-"
	}
	return string(b)
}

// Lista lê ORDENS_CONTAS_HML ("99999" ou "99999,88888").
func Lista(s string) []string {
	var r []string
	for _, x := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' || c == ';' }) {
		r = append(r, strings.TrimSpace(x))
	}
	return r
}
