package br.com.gammaquant.kalmanordens.alerta;

/** Canal de alerta externo. Não pode bloquear quem chama. */
public interface Notificador {

    void enviar(String texto);
}
