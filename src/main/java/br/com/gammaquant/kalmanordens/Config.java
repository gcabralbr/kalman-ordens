package br.com.gammaquant.kalmanordens;

import java.math.BigDecimal;
import java.time.Duration;
import java.time.LocalDate;
import java.time.LocalTime;
import java.util.HashMap;
import java.util.Map;
import java.util.Optional;

/**
 * Configuração do serviço. Em produção vem do ambiente (systemd carrega
 * /etc/kalman-ordens/service.env). Nos testes, de um Map.
 *
 * Tempos com sufixo _S aceitam fração (ex.: TIMEOUT_ENTRADA_S=0.3 nos testes).
 */
public final class Config {

    private final Map<String, String> v;

    public Config(Map<String, String> valores) {
        this.v = new HashMap<>(valores);
    }

    public static Config doAmbiente() {
        return new Config(System.getenv());
    }

    public Config com(String chave, String valor) {
        Map<String, String> m = new HashMap<>(v);
        m.put(chave, valor);
        return new Config(m);
    }

    // ---------- FIX ----------
    public String fixSenderCompId() { return obrig("FIX_SENDER_COMP_ID"); }
    public String fixTargetCompId() { return str("FIX_TARGET_COMP_ID", "PNT_DSA"); }
    public String fixHost() { return obrig("FIX_HOST"); }
    public int fixPorta() { return Integer.parseInt(obrig("FIX_PORTA")); }
    public String fixUsuario() { return str("FIX_USUARIO", ""); }
    public String fixSenha() { return str("FIX_SENHA", ""); }
    public String fixDiretorio() { return str("FIX_DIR", "/var/lib/kalman-ordens/fix"); }
    /** Caminho do dicionário. Vazio = o dsa_FIX44.xml embutido no jar. [PnT-2] */
    public String fixDicionario() { return str("FIX_DICIONARIO", ""); }
    public LocalTime fixInicio() { return hora("FIX_INICIO", "08:30:00"); }
    public LocalTime fixFim() { return hora("FIX_FIM", "17:55:00"); }
    /** [PnT-11]: se o PnT exigir reset a cada logon, liga-se aqui. */
    public boolean fixResetOnLogon() { return bool("FIX_RESET_ON_LOGON", false); }
    public int fixHeartbeat() { return inteiro("FIX_HEARTBEAT", 30); }
    public int fixReconexaoS() { return inteiro("FIX_RECONEXAO_S", 5); }

    // ---------- Identificação da ordem ----------
    /** Conta com dígito, sem separador. */
    public String conta() { return obrig("ORDENS_CONTA"); }
    /** 116 OnBehalfOfSubID. [PnT-3] */
    public String onBehalfOfSubId() { return str("ORDENS_ONBEHALF", ""); }
    /** 142 SenderLocationID. [PnT-3] */
    public String senderLocationId() { return str("ORDENS_SENDER_LOCATION", ""); }
    public String bolsa() { return str("ORDENS_BOLSA", "BVMF"); }

    /** Único uid habilitado. Vazio = ninguém (padrão seguro). */
    public Optional<Integer> uidReal() {
        String s = str("ORDENS_UID_REAL", "").trim();
        return s.isEmpty() ? Optional.empty() : Optional.of(Integer.parseInt(s));
    }

    public String simbolo() { return obrig("ORDENS_SIMBOLO"); }
    /** Data de vencimento do símbolo em uso (yyyy-MM-dd). */
    public Optional<LocalDate> vencimento() {
        String s = str("ORDENS_VENCIMENTO", "").trim();
        return s.isEmpty() ? Optional.empty() : Optional.of(LocalDate.parse(s));
    }

    // ---------- HTTP ----------
    public String httpHost() { return str("HTTP_HOST", "127.0.0.1"); }
    public int httpPorta() { return inteiro("HTTP_PORTA", 9092); }

    // ---------- Preço ----------
    public String precosUrl() { return str("PRECOS_URL", "http://127.0.0.1:9090/precos/atual"); }
    /** Valor de status84 que indica negociação normal. */
    public String status84Negociacao() { return str("STATUS84_NEGOCIACAO", "0"); }

    // ---------- Banco ----------
    public String dbUrl() { return obrig("DB_URL"); }
    public String dbUsuario() { return str("DB_USUARIO", ""); }
    public String dbSenha() { return str("DB_SENHA", ""); }

    // ---------- Telegram ----------
    public String telegramToken() { return str("TELEGRAM_TOKEN", ""); }
    public String telegramChat() { return str("TELEGRAM_CHAT", ""); }

    // ---------- Execução ----------
    public BigDecimal tick() { return dec("TICK_PTS", "5"); }
    public BigDecimal colarEntrada() { return dec("COLAR_ENTRADA_PTS", "15"); }
    public Duration timeoutEntrada() { return seg("TIMEOUT_ENTRADA_S", "3"); }
    public BigDecimal folgaStop() { return dec("FOLGA_STOP_PTS", "100"); }
    public BigDecimal colarSaida() { return dec("COLAR_SAIDA_PTS", "50"); }
    public Duration timeoutSaida() { return seg("TIMEOUT_SAIDA_S", "2"); }
    public int maxReprecoSaida() { return inteiro("MAX_REPRECO_SAIDA", 3); }
    public Duration esperaStop() { return seg("ESPERA_STOP_S", "5"); }
    public Duration esperaRetentativaProtecao() { return seg("RETENTATIVA_PROTECAO_S", "1"); }
    public Duration alertaSemProtecao() { return seg("ALERTA_SEM_PROTECAO_S", "2"); }
    public Duration alertaPendenteNova() { return seg("ALERTA_PENDENTE_S", "3"); }
    public Duration repeticaoAlertaCritico() { return seg("ALERTA_REPETICAO_S", "30"); }
    public Duration silencioMassStatus() { return seg("MASS_STATUS_SILENCIO_S", "3"); }
    public Duration intervaloTick() { return seg("TICK_MOTOR_S", "0.1"); }

    // ---------- Limites (Parte 5) ----------
    public int posicaoMaxima() { return inteiro("LIMITE_POSICAO_MAX", 1); }
    public int qtdMaximaOrdem() { return inteiro("LIMITE_QTD_ORDEM", 1); }
    public Duration intervaloMinimoEntradas() { return seg("LIMITE_INTERVALO_ENTRADAS_S", "60"); }
    public int entradasPorMinuto() { return inteiro("LIMITE_ENTRADAS_MIN", 1); }
    public int mensagensPorMinuto() { return inteiro("LIMITE_MENSAGENS_MIN", 10); }
    public int entradasPorDia() { return inteiro("LIMITE_ENTRADAS_DIA", 15); }
    public BigDecimal perdaDiariaPts() { return dec("LIMITE_PERDA_DIA_PTS", "1500"); }
    public LocalTime janelaInicio() { return hora("JANELA_INICIO", "09:00:00"); }
    public LocalTime janelaFim() { return hora("JANELA_FIM", "14:00:00"); }
    public Duration idadeMaximaPreco() { return seg("PRECO_IDADE_MAX_S", "3"); }
    public BigDecimal bookDescoladoPts() { return dec("LIMITE_BOOK_DESCOLADO_PTS", "150"); }
    public int recusasParaParada() { return inteiro("LIMITE_RECUSAS_PARADA", 3); }
    public LocalTime horaZeragem() { return hora("HORA_ZERAGEM", "14:30:00"); }
    public int pregoesAvisoVencimento() { return inteiro("AVISO_VENCIMENTO_PREGOES", 3); }

    // ---------- utilitários ----------
    private String str(String k, String padrao) {
        String s = v.get(k);
        return s == null ? padrao : s;
    }

    private String obrig(String k) {
        String s = v.get(k);
        if (s == null || s.isBlank()) throw new IllegalStateException("Configuração obrigatória ausente: " + k);
        return s;
    }

    private int inteiro(String k, int padrao) {
        String s = v.get(k);
        return s == null || s.isBlank() ? padrao : Integer.parseInt(s.trim());
    }

    private boolean bool(String k, boolean padrao) {
        String s = v.get(k);
        if (s == null || s.isBlank()) return padrao;
        return s.trim().equalsIgnoreCase("Y") || s.trim().equalsIgnoreCase("true") || s.trim().equals("1");
    }

    private BigDecimal dec(String k, String padrao) {
        return new BigDecimal(str(k, padrao).trim());
    }

    private Duration seg(String k, String padrao) {
        BigDecimal s = new BigDecimal(str(k, padrao).trim());
        return Duration.ofMillis(s.multiply(BigDecimal.valueOf(1000)).longValueExact());
    }

    private LocalTime hora(String k, String padrao) {
        return LocalTime.parse(str(k, padrao).trim());
    }
}
