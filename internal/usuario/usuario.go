// Package usuario resolve conta e destino de um usuário a partir da configuração do GQ
// (dbecotools.acusuario), SPEC v1.2 U1–U3. Função pura: quem lê a tabela é o repositório.
//
// No GQ o usuário escolhe no login onde opera; no serviço não há login, quem escolhe é o
// ambiente da instância (ORDENS_AMBIENTE): HML lê usutxtconfig (simulação), PRODUCAO lê
// usutxtconfigprd (conta real). Do JSON só importa o grupo "Robo".
package usuario

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Ambientes da instância.
const (
	HML      = "HML"
	Producao = "PRODUCAO"
)

// Destinos (Robo.CORSeqId). Só o PnT é suportado.
const (
	DestinoPnT      = 85   // BTG → PnT
	DestinoFlexscan = 88   // Capital → Flexscan
	DestinoGQ       = 9999 // simulador do GQ
)

// Resolvido: conta e destino de uma entrada.
type Resolvido struct {
	Conta   string
	Destino int
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

// Campo devolve o nome da coluna lida no ambiente.
func Campo(ambiente string) string {
	if ambiente == Producao {
		return "usutxtconfigprd"
	}
	return "usutxtconfig"
}

// Resolver interpreta o JSON do ambiente e aplica as travas: destino só 85; conta só dígitos;
// trava cruzada — em HML a conta tem de estar em contasHML, em PRODUCAO não pode estar.
func Resolver(ambiente, texto string, contasHML []string) (Resolvido, *Falha) {
	campo := Campo(ambiente)
	if strings.TrimSpace(texto) == "" {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + " vazio", "USUARIO_CONFIG"}
	}
	var cfg struct {
		Robo *struct {
			CORSeqId    json.RawMessage `json:"CORSeqId"`
			CCONroConta json.RawMessage `json:"CCONroConta"`
		} `json:"Robo"`
	}
	if err := json.Unmarshal([]byte(texto), &cfg); err != nil {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": JSON inválido (" + err.Error() + ")", "USUARIO_CONFIG"}
	}
	if cfg.Robo == nil || len(cfg.Robo.CORSeqId) == 0 {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": sem Robo.CORSeqId", "USUARIO_CONFIG"}
	}
	var destino int
	if err := json.Unmarshal(cfg.Robo.CORSeqId, &destino); err != nil {
		return Resolvido{}, &Falha{"CONFIG_USUARIO_INVALIDA", campo + ": Robo.CORSeqId não é inteiro: " + string(cfg.Robo.CORSeqId), "USUARIO_CONFIG"}
	}
	switch destino {
	case DestinoPnT:
	case DestinoFlexscan:
		return Resolvido{}, &Falha{"DESTINO_NAO_SUPORTADO", fmt.Sprintf("%s: destino %d (Capital → Flexscan) não é suportado; só 85 (BTG → PnT)", campo, destino), ""}
	case DestinoGQ:
		return Resolvido{}, &Falha{"DESTINO_INVALIDO", fmt.Sprintf("%s: destino %d é o simulador do GQ; ordem real só com 85 (BTG → PnT)", campo, destino), "USUARIO_CONFIG"}
	default:
		return Resolvido{}, &Falha{"DESTINO_INVALIDO", fmt.Sprintf("%s: destino %d desconhecido; só 85 (BTG → PnT)", campo, destino), "USUARIO_CONFIG"}
	}
	var conta string
	if err := json.Unmarshal(cfg.Robo.CCONroConta, &conta); err != nil {
		return Resolvido{}, &Falha{"CONTA_INVALIDA", campo + ": Robo.CCONroConta ausente ou não é texto", "USUARIO_CONFIG"}
	}
	conta = strings.TrimSpace(conta)
	if !soDigitos.MatchString(conta) {
		return Resolvido{}, &Falha{"CONTA_INVALIDA", fmt.Sprintf("%s: conta %q não é só dígitos", campo, conta), "USUARIO_CONFIG"}
	}
	naLista := false
	for _, c := range contasHML {
		if c == conta {
			naLista = true
		}
	}
	switch {
	case ambiente == HML && !naLista:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta %s fora de ORDENS_CONTAS_HML numa instância HML", conta), "CONTA_AMBIENTE"}
	case ambiente == Producao && naLista:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta de HML %s numa instância PRODUCAO", conta), "CONTA_AMBIENTE"}
	case ambiente != HML && ambiente != Producao:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("ORDENS_AMBIENTE %q inválido (HML ou PRODUCAO)", ambiente), "CONTA_AMBIENTE"}
	}
	return Resolvido{Conta: conta, Destino: destino}, nil
}

// Lista lê ORDENS_CONTAS_HML ("99999" ou "99999,88888").
func Lista(s string) []string {
	var r []string
	for _, x := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' || c == ';' }) {
		r = append(r, strings.TrimSpace(x))
	}
	return r
}
