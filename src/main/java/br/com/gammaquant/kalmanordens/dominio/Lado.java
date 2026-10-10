package br.com.gammaquant.kalmanordens.dominio;

/** Lado da ordem. C = compra (54=1), V = venda (54=2). */
public enum Lado {
    C('1', 1),
    V('2', -1);

    public final char fix;
    public final int sinal;

    Lado(char fix, int sinal) {
        this.fix = fix;
        this.sinal = sinal;
    }

    public Lado oposto() {
        return this == C ? V : C;
    }

    public static Lado doFix(char c) {
        return switch (c) {
            case '1' -> C;
            case '2' -> V;
            default -> throw new IllegalArgumentException("Side inválido: " + c);
        };
    }

    /** Lado que reduz uma posição com este sinal. */
    public static Lado paraZerar(int posicao) {
        return posicao > 0 ? V : C;
    }
}
