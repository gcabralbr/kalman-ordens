package br.com.gammaquant.kalmanordens.preco;

import br.com.gammaquant.kalmanordens.dominio.Cotacao;

import java.util.Optional;

/** Cotação atual do símbolo operado. Vazio = sem leitura (marketdata fora, erro, símbolo ausente). */
public interface FontePreco {

    Optional<Cotacao> atual();
}
