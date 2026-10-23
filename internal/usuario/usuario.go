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

	"gammaquant.com.br/kalman-ordens/internal/dominio"
)

// Ambientes da instância.
const (
	HML      = "HML"
	Producao = "PRODUCAO"
)

// Destinos (Robo.CORSeqId): 85 (PnT) sempre; 88 (Flexscan) só com o conector configurado
// (SPEC-FLEXSCAN-v1).
const (
	DestinoPnT      = dominio.DestinoPnT      // BTG → PnT
	DestinoFlexscan = dominio.DestinoFlexscan // Capital → Flexscan
	DestinoGQ       = 9999                    // simulador do GQ
)

// Resolvido: conta, destino e conector de uma entrada.
type Resolvido struct {
	Conta    string
	Destino  int
	Conector string
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

// contaFlexscan: a conta da Flexscan tem letra e hífen (apisim: S-1117150).
var contaFlexscan = regexp.MustCompile(`^[A-Za-z0-9-]{1,20}$`)

// Campo devolve o nome da coluna lida no ambiente.
func Campo(ambiente string) string {
	if ambiente == Producao {
		return "usutxtconfigprd"
	}
	return "usutxtconfig"
}

// Resolver: as regras de antes (só o PnT).
func Resolver(ambiente, texto string, contasHML []string) (Resolvido, *Falha) {
	return ResolverCom(Regras{Ambiente: ambiente, ContasHML: contasHML}, texto)
}

// ResolverCom interpreta o JSON do ambiente e aplica as travas: destino 85, ou 88 com a Flexscan
// configurada; conta só dígitos (PnT) ou letras, dígitos e hífen (Flexscan); trava cruzada — em
// HML a conta tem de estar na lista do conector, em PRODUCAO não pode estar.
func ResolverCom(rg Regras, texto string) (Resolvido, *Falha) {
	ambiente := rg.Ambiente
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
	contasHML, listaHML := rg.ContasHML, "ORDENS_CONTAS_HML"
	switch destino {
	case DestinoPnT:
	case DestinoFlexscan:
		if !rg.Flexscan {
			return Resolvido{}, &Falha{"DESTINO_NAO_SUPORTADO", fmt.Sprintf("%s: destino %d (Capital → Flexscan) não é suportado nesta instância (FLEXSCAN_URL vazio)", campo, destino), ""}
		}
		contasHML, listaHML = rg.ContasHMLFlx, "FLEXSCAN_CONTAS_HML"
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
	switch {
	case destino == DestinoFlexscan && !contaFlexscan.MatchString(conta):
		return Resolvido{}, &Falha{"CONTA_INVALIDA", fmt.Sprintf("%s: conta Flexscan %q inválida (letras, dígitos e hífen)", campo, conta), "USUARIO_CONFIG"}
	case destino != DestinoFlexscan && !soDigitos.MatchString(conta):
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
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta %s fora de %s numa instância HML", conta, listaHML), "CONTA_AMBIENTE"}
	case ambiente == Producao && naLista:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("conta de HML %s numa instância PRODUCAO", conta), "CONTA_AMBIENTE"}
	case ambiente != HML && ambiente != Producao:
		return Resolvido{}, &Falha{"CONTA_AMBIENTE", fmt.Sprintf("ORDENS_AMBIENTE %q inválido (HML ou PRODUCAO)", ambiente), "CONTA_AMBIENTE"}
	}
	return Resolvido{Conta: conta, Destino: destino, Conector: dominio.ConectorDoDestino(destino)}, nil
}

// Lista lê ORDENS_CONTAS_HML ("99999" ou "99999,88888").
func Lista(s string) []string {
	var r []string
	for _, x := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' || c == ';' }) {
		r = append(r, strings.TrimSpace(x))
	}
	return r
}
