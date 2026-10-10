package br.com.gammaquant.kalmanordens.dominio;

/** Máquina de estados da operação real (Parte 4 da spec). */
public enum EstadoOperacao {
    ENTRANDO,
    POSICIONADA_SEM_PROTECAO,
    PROTEGIDA,
    CANCELANDO_PROTECAO,
    AGUARDANDO_STOP,
    SAINDO,
    ZERADA,
    ENCERRADA_SEM_ENTRADA;

    public boolean isFinal() {
        return this == ZERADA || this == ENCERRADA_SEM_ENTRADA;
    }

    public boolean isSaindo() {
        return this == CANCELANDO_PROTECAO || this == AGUARDANDO_STOP || this == SAINDO;
    }
}
