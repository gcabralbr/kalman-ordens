package br.com.gammaquant.kalmanordens.alerta;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.function.Consumer;

/**
 * Alertas da Parte 9. Os críticos repetem a cada 30 s até resolver().
 * Confinado à thread do motor.
 */
public final class Alertas {

    private static final Logger log = LoggerFactory.getLogger(Alertas.class);

    private record Critico(String texto, Instant proximo) {
    }

    private final Notificador notificador;
    private final Consumer<String> publicador;
    private final Clock relogio;
    private final Duration repeticao;
    private final Map<String, Critico> criticos = new LinkedHashMap<>();

    /** @param publicador publica o evento ALERTA para o Kalman */
    public Alertas(Notificador notificador, Consumer<String> publicador, Clock relogio, Duration repeticao) {
        this.notificador = notificador;
        this.publicador = publicador;
        this.relogio = relogio;
        this.repeticao = repeticao;
    }

    public void alerta(String texto) {
        log.warn("ALERTA {}", texto);
        notificador.enviar(texto);
        publicador.accept(texto);
    }

    /** Alerta crítico com chave. Repetido até resolver(chave). Uma segunda chamada com a mesma chave não duplica. */
    public void critico(String chave, String texto) {
        if (criticos.containsKey(chave)) return;
        String t = "CRÍTICO: " + texto;
        log.error("ALERTA {}", t);
        notificador.enviar(t);
        publicador.accept(t);
        criticos.put(chave, new Critico(t, relogio.instant().plus(repeticao)));
    }

    public void resolver(String chave) {
        if (criticos.remove(chave) != null) {
            alerta("resolvido: " + chave);
        }
    }

    public boolean ativo(String chave) {
        return criticos.containsKey(chave);
    }

    public Map<String, String> ativos() {
        Map<String, String> r = new LinkedHashMap<>();
        criticos.forEach((k, v) -> r.put(k, v.texto()));
        return r;
    }

    /** Chamado pelo tick do motor. */
    public void tick() {
        Instant agora = relogio.instant();
        criticos.replaceAll((k, c) -> {
            if (agora.isBefore(c.proximo())) return c;
            log.error("ALERTA (repetição) {}", c.texto());
            notificador.enviar(c.texto() + " (continua)");
            return new Critico(c.texto(), agora.plus(repeticao));
        });
    }
}
