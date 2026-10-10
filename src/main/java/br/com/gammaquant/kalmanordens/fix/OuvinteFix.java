package br.com.gammaquant.kalmanordens.fix;

import quickfix.Message;

/** Callbacks da sessão, chamados na thread do QuickFIX/J. Quem implementa só enfileira. */
public interface OuvinteFix {

    void aoLogar();

    void aoCair();

    /** 35=8, 35=9, 35=j e 35=3. A mensagem já é uma cópia. */
    void aoReceber(Message m);
}
