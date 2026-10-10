package br.com.gammaquant.kalmanordens.motor;

import com.fasterxml.jackson.annotation.JsonProperty;

/** Estado da parada (Parte 6). Persistido em kalmanordemestado, chave "parada". */
public record Parada(
        @JsonProperty("modo") String modo,
        @JsonProperty("origem") String origem,
        @JsonProperty("motivo") String motivo,
        @JsonProperty("desde") String desde) {

    public static final String BLOQUEAR = "BLOQUEAR";
    public static final String ZERAR = "ZERAR";
}
