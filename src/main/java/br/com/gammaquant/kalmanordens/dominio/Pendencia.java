package br.com.gammaquant.kalmanordens.dominio;

/** Pedido em voo sobre uma ordem. O PnT aceita só um por vez. */
public enum Pendencia {
    NENHUMA, CANCEL, REPLACE
}
