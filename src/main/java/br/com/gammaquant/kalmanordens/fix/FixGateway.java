package br.com.gammaquant.kalmanordens.fix;

import quickfix.Message;

/** O que o motor precisa da sessão FIX. */
public interface FixGateway {

    boolean logado();

    /**
     * Envia. Devolve false se a mensagem não saiu (sem logon, sessão inexistente,
     * sendToTarget falso). Mensagem que não saiu nunca é "OK" para o gestor.
     */
    boolean enviar(Message m);

    /** Próximas sequências esperadas (entrada, saída), para o /saude. */
    default int[] sequencias() {
        return new int[] {0, 0};
    }
}
