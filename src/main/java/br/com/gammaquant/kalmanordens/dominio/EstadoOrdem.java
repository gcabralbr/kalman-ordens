package br.com.gammaquant.kalmanordens.dominio;

public enum EstadoOrdem {
    /** Gravada, ainda não confirmada como enviada. */
    ENVIANDO,
    /** 39=A */
    PENDENTE,
    /** 39=0 */
    ATIVA,
    /** 39=1 */
    PARCIAL,
    /** 39=2 */
    EXECUTADA,
    /** 39=4 */
    CANCELADA,
    /** 39=8 / 35=3 / 35=j */
    REJEITADA,
    /** 39=C */
    EXPIRADA,
    /** sendToTarget falhou: a ordem não saiu. */
    FALHA_ENVIO,
    /** Viva na tabela e ausente no mass status. */
    DESCONHECIDA;

    public boolean isFinal() {
        return this == EXECUTADA || this == CANCELADA || this == REJEITADA || this == EXPIRADA || this == FALHA_ENVIO;
    }

    public static EstadoOrdem do39(char c) {
        return switch (c) {
            case 'A' -> PENDENTE;
            case '0', 'E', '5', '6' -> ATIVA;
            case '1' -> PARCIAL;
            case '2' -> EXECUTADA;
            case '4' -> CANCELADA;
            case '8' -> REJEITADA;
            case 'C' -> EXPIRADA;
            default -> null;
        };
    }
}
