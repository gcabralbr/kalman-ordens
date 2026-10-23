// Package usuario resolve conta e destino de um usuário a partir da configuração do GQ
// (dbecotools.acusuario), SPEC v1.2 U1–U3. Função pura: quem lê a tabela é o repositório.
//
// No GQ o usuário escolhe no login onde opera; no serviço não há login, quem escolhe é o
// ambiente da instância (ORDENS_AMBIENTE): HML lê usutxtconfig (simulação), PRODUCAO lê
// usutxtconfigprd (conta real). Do JSON só importa o grupo "Robo".
package usuario

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// Ambientes da instância.
const (
	HML      = "HML"
	Producao = "PRODUCAO"
)

// Destinos (Robo.CORSeqId): a corretora. Não decide o caminho das ordens (SPEC-FLEXSCAN-v1 §14):
// o conector vem da habilitação. A mesma corretora pode sair pelo PnT ou pela Flexscan.
const (
	DestinoBTG     = dominio.DestinoBTG     // 85
	DestinoCapital = dominio.DestinoCapital // 88
	DestinoGQ      = 9999                   // simulador do GQ: nunca ordem real
	// DestinoPnT: o nome antigo do 85 (testes e textos de antes)
	DestinoPnT = DestinoBTG
)

// corretoras conhecidas; outra entra aqui quando for usada (um CORSeqId errado no GQ não vira ordem)
var corretoras = map[int]string{DestinoBTG: "BTG", DestinoCapital: "Capital"}

// Resolvido: conta, destino e conector de uma entrada.
type Resolvido struct {
	Conta       string
	Destino     int
	Conector    string
	OrigemConta string // Futuros.CCONroContaFut (WIN) ou Robo.CCONroConta (sem o grupo Futuros)
}

// Regras da instância para resolver um usuário: o ambiente, as contas de HML de cada conector
// (trava cruzada) e se o conector Flexscan está configurado.
type Regras struct {
	Ambiente     string
	ContasHML    []string // PnT (ORDENS_CONTAS_HML)
	ContasHMLFlx []string // Flexscan (FLEXSCAN_CONTAS_HML)
	Flexscan     bool     // FLEXSCAN_URL preenchida
}

// Falha: por que não dá para mandar ordem real. Codigo é a recusa devolvida ao gestor; Alerta
// diz se também vai alerta (código do catálogo; CONTA_AMBIENTE é crítico).
type Falha struct {
	Codigo string
	Motivo string
	Alerta string // "" = sem alerta
}

func (f *Falha) Error() string { return f.Codigo + ": " + f.Motivo }

var soDigitos = regexp.MustCompile(`^[0-9]+$`)

// contaFlexscan: a conta pela Flexscan tem letra e hífen no simulador (S-1117150) e é numérica na
// Capital em produção (715124).
var contaFlexscan = regexp.MustCompile(`^[A-Za-z0-9-]{1,20}$`)

// Campo devolve o nome da coluna lida no ambiente.
func Campo(ambiente string) string {
	if ambiente == Producao {
		return "usutxtconfigprd"
	}
	return "usutxtconfig"
}

// Resolver: pelo PnT (as regras de antes).
func Resolver(ambiente, texto string, contasHML []string) (Resolvido, *Falha) {
	return ResolverCom(Regras{Ambiente: ambiente, ContasHML: contasHML}, texto, dominio.ConectorPnT)
}

// ResolverCom interpreta o JSON do ambiente para o conector do uid (o da habilitação) e aplica as
// travas: corretora conhecida (85 BTG, 88 Capital; 9999 é o simulador do GQ); a Flexscan só com
// FLEXSCAN_URL; conta pelo conector — PnT só dígitos, Flexscan até 20 letras, dígitos e hífen;
// trava cruzada — em HML a conta tem de estar na lista do conector, em PRODUCAO não pode estar.
func ResolverCom(rg Regras, texto, conector string) (Resolvido, *Falha) {
	ambiente := rg.Ambiente
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
	case destino == DestinoGQ:
		return Resolvido{}, &Falha{"DESTINO_INVALIDO", fmt.Sprintf("%s: Robo.CORSeqId %d é o simulador do GQ; ordem real só com uma corretora (85 BTG, 88 Capital)", campo, destino), "USUARIO_CONFIG"}
	case corretoras[destino] == "":
		return Resolvido{}, &Falha{"DESTINO_INVALIDO", fmt.Sprintf("%s: Robo.CORSeqId %d não é uma corretora conhecida (85 BTG, 88 Capital)", campo, destino), "USUARIO_CONFIG"}
	}
	// Robo.Conector (o GX gera desde 23/10): conferência — a habilitação decide, e os dois têm de bater
	if len(cfg.Robo.Conector) > 0 && string(cfg.Robo.Conector) != "null" {
		var gq string
		if err := json.Unmarshal(cfg.Robo.Conector, &gq); err != nil {
			return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": Robo.Conector não é texto", "USUARIO_CONFIG"}
		}
		if gq = strings.ToUpper(strings.TrimSpace(gq)); gq != "" && gq != conector {
			return Resolvido{}, &Falha{"CONECTOR_DIVERGENTE", fmt.Sprintf("%s: Robo.Conector %s no GQ e conector %s na habilitação", campo, gq, conector), "USUARIO_CONFIG"}
		}
	}
	contasHML, listaHML := rg.ContasHML, "ORDENS_CONTAS_HML"
	switch conector {
	case dominio.ConectorPnT:
	case dominio.ConectorFlexscan:
		if !rg.Flexscan {
			return Resolvido{}, &Falha{"CONECTOR_NAO_CONFIGURADO", "conector FLEXSCAN na habilitação, mas esta instância não tem FLEXSCAN_URL", ""}
		}
		contasHML, listaHML = rg.ContasHMLFlx, "FLEXSCAN_CONTAS_HML"
	default:
		return Resolvido{}, &Falha{"CONECTOR_NAO_CONFIGURADO", fmt.Sprintf("conector %q desconhecido", conector), ""}
	}
	// a conta de WIN (ativo de futuros) é a Futuros.CCONroContaFut; sem o grupo Futuros (ou com ela
	// vazia), a Robo.CCONroConta, como antes
	var conta string
	origem := "Futuros.CCONroContaFut"
	if cfg.Futuros != nil && len(cfg.Futuros.CCONroContaFut) > 0 && string(cfg.Futuros.CCONroContaFut) != "null" {
		if err := json.Unmarshal(cfg.Futuros.CCONroContaFut, &conta); err != nil {
			return Resolvido{}, &Falha{"CONTA_INVALIDA", campo + ": Futuros.CCONroContaFut não é texto", "USUARIO_CONFIG"}
		}
		conta = strings.TrimSpace(conta)
	}
	if conta == "" {
		origem = "Robo.CCONroConta"
		if err := json.Unmarshal(cfg.Robo.CCONroConta, &conta); err != nil {
			return Resolvido{}, &Falha{"CONTA_INVALIDA", campo + ": sem Futuros.CCONroContaFut e Robo.CCONroConta ausente ou não é texto", "USUARIO_CONFIG"}
		}
		conta = strings.TrimSpace(conta)
	}
	switch {
	case conector == dominio.ConectorFlexscan && !contaFlexscan.MatchString(conta):
		return Resolvido{}, &Falha{"CONTA_INVALIDA", fmt.Sprintf("%s: conector FLEXSCAN: conta %q (%s) inválida (até 20 letras, dígitos e hífen)", campo, conta, origem), "USUARIO_CONFIG"}
	case conector == dominio.ConectorPnT && !soDigitos.MatchString(conta):
		return Resolvido{}, &Falha{"CONTA_INVALIDA", fmt.Sprintf("%s: conector PNT: conta %q (%s) não é só dígitos", campo, conta, origem), "USUARIO_CONFIG"}
	}
	naLista := false
	for _, c := range contasHML {
		if c == conta {
			naLista = true
		}
	}
	switch {
	case ambiente == HML && !naLista:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta %s fora de %s numa instância HML", conta, listaHML), "CONTA_AMBIENTE"}
	case ambiente == Producao && naLista:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta de HML %s numa instância PRODUCAO", conta), "CONTA_AMBIENTE"}
	case ambiente != HML && ambiente != Producao:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("ORDENS_AMBIENTE %q inválido (HML ou PRODUCAO)", ambiente), "CONTA_AMBIENTE"}
	}
	return Resolvido{Conta: conta, Destino: destino, Conector: conector, OrigemConta: origem}, nil
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
