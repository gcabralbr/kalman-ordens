package br.com.gammaquant.kalmanordens.dominio;

/** Recusa síncrona do serviço ao gestor (Parte 2.1). */
public final class Recusa extends RuntimeException {

    public final String codigo;
    /** Conta para a parada automática por "3 recusas por limite em 1 min". */
    public final boolean porLimite;

    public Recusa(String codigo, String motivo, boolean porLimite) {
        super(motivo);
        this.codigo = codigo;
        this.porLimite = porLimite;
    }

    public static Recusa simples(String codigo, String motivo) {
        return new Recusa(codigo, motivo, false);
    }

    public static Recusa limite(String codigo, String motivo) {
        return new Recusa(codigo, motivo, true);
    }
}
