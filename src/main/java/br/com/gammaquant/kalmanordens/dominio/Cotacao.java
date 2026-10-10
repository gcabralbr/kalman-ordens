package br.com.gammaquant.kalmanordens.dominio;

import java.math.BigDecimal;
import java.time.Duration;

/** Leitura de /precos/atual do marketdata. */
public record Cotacao(BigDecimal bid, BigDecimal ask, BigDecimal ultimo, String status84, Duration idade) {

    public BigDecimal meio() {
        return bid.add(ask).divide(BigDecimal.valueOf(2));
    }

    public boolean completa() {
        return bid != null && ask != null && ultimo != null && idade != null;
    }
}
