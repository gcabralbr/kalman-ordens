package br.com.gammaquant.kalmanordens.dominio;

import java.math.BigDecimal;
import java.math.RoundingMode;

/** Arredondamento ao tick do WIN (5 pts). */
public final class Tick {

    private final BigDecimal tick;

    public Tick(BigDecimal tick) {
        this.tick = tick;
    }

    public BigDecimal acima(BigDecimal p) {
        return p.divide(tick, 0, RoundingMode.CEILING).multiply(tick);
    }

    public BigDecimal abaixo(BigDecimal p) {
        return p.divide(tick, 0, RoundingMode.FLOOR).multiply(tick);
    }

    /** Preço agressivo: compra arredonda para cima, venda para baixo. */
    public BigDecimal agressivo(Lado lado, BigDecimal p) {
        return lado == Lado.C ? acima(p) : abaixo(p);
    }
}
