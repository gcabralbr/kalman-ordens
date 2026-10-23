package flexscan

import (
	"time"

	"gammaquant.com.br/kalman-ordens/internal/config"
)

// ConfigDe lê a configuração da ponte do service.env (SPEC-FLEXSCAN-v1 §6).
func ConfigDe(c config.Config) Config {
	return Config{
		URL:           c.Texto("FLEXSCAN_URL", ""),
		Usuario:       c.Texto("FLEXSCAN_USUARIO", ""),
		Senha:         c.Texto("FLEXSCAN_SENHA", ""),
		Bolsa:         c.Texto("FLEXSCAN_BOLSA", "XBMF"),
		Timeout:       time.Duration(c.Inteiro("FLEXSCAN_TIMEOUT_MS", 3000)) * time.Millisecond,
		ReqPorS:       float64(c.Inteiro("FLEXSCAN_REQ_POR_S", 10)),
		Rajada:        c.Inteiro("FLEXSCAN_RAJADA", 5),
		Trabalhadores: c.Inteiro("FLEXSCAN_TRABALHADORES", 4),
		Confirma:      c.Segundos("FLEXSCAN_CONFIRMA_S", 5),
		Consulta:      c.Segundos("FLEXSCAN_CONSULTA_S", 5),
		AtrasoWebhook: c.Segundos("FLEXSCAN_WEBHOOK_ATRASO_S", 10),
		ConfirmarFill: c.Bool("FLEXSCAN_CONFIRMAR_FILL", true),
		Webhook:       c.Texto("FLEXSCAN_WEBHOOK_ENDERECO", "") != "",
		RenovarAntes:  time.Duration(c.Inteiro("FLEXSCAN_RENOVAR_ANTES_MIN", 30)) * time.Minute,
		VidaToken:     time.Duration(c.Inteiro("FLEXSCAN_TOKEN_VIDA_H", 9)) * time.Hour,
		EsperaLogin:   c.Segundos("FLEXSCAN_ESPERA_LOGIN_S", 30),
	}
}
