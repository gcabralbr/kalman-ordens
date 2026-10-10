package br.com.gammaquant.kalmanordens.dominio;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

import java.math.BigDecimal;

/** Corpo do POST /ordens (Parte 2.1). */
@JsonIgnoreProperties(ignoreUnknown = false)
public record PedidoOrdem(
        @JsonProperty("operacao_id") Long operacaoId,
        @JsonProperty("uid") Integer uid,
        @JsonProperty("papel") String papel,
        @JsonProperty("lado") String lado,
        @JsonProperty("qtd") Integer qtd,
        @JsonProperty("stop_pts") BigDecimal stopPts,
        @JsonProperty("motivo") String motivo,
        @JsonProperty("ts_decisao") String tsDecisao) {

    public boolean isEntrada() {
        return "ENTRADA".equals(papel);
    }

    public boolean isSaida() {
        return "SAIDA".equals(papel);
    }
}
