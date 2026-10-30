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
		EstadoLogin:   c.Texto("FLEXSCAN_ESTADO_LOGIN", "/var/lib/kalman-ordens/flexscan-login.json"),
		LoginMax:      c.Inteiro("FLEXSCAN_LOGIN_MAX", 5),
		LoginJanela:   time.Duration(c.Inteiro("FLEXSCAN_LOGIN_JANELA_MIN", 10)) * time.Minute,
		// v1.21
		PrimeiraConsulta: time.Duration(c.Inteiro("FLEXSCAN_PRIMEIRA_CONSULTA_MS", 200)) * time.Millisecond,
		WebhookURL:       c.Texto("FLEXSCAN_WEBHOOK_URL", ""),
		// v1.29: 0 = sem a periódica (a conferência é por evento: abertura, login, webhook faltando)
		ConfereWebhook: time.Duration(c.Inteiro("FLEXSCAN_WEBHOOK_CONFERIR_MIN", 0)) * time.Minute,
		// v1.22
		EsperaWebhook: c.Segundos("FLEXSCAN_WEBHOOK_ESPERA_S", 5),
		// v1.28: a agenda (o erro de formato é conferido na partida, AgendaDe)
		Agenda:         agendaOuSempre(AgendaDe(c)),
		SemRespostaMax: c.Inteiro("FLEXSCAN_SEM_RESPOSTA_N", 3),
		FaltasWebhook:  c.Inteiro("FLEXSCAN_WEBHOOK_FALTAS_N", 3),
	}
}

// AgendaDe (v1.28): FLEXSCAN_JANELA, FLEXSCAN_RENOVAR_ABERTURA e FLEXSCAN_CONFERIR_ABERTURA.
func AgendaDe(c config.Config) (Agenda, error) {
	return LerAgenda(c.Texto("FLEXSCAN_JANELA", "08:00-18:30"), c.Texto("FLEXSCAN_RENOVAR_ABERTURA", "08:15"),
		c.Texto("FLEXSCAN_CONFERIR_ABERTURA", "08:30"))
}

func agendaOuSempre(a Agenda, err error) *Agenda {
	if err != nil {
		a = AgendaSempre
	}
	return &a
}
