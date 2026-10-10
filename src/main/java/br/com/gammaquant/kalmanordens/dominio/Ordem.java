package br.com.gammaquant.kalmanordens.dominio;

import java.math.BigDecimal;
import java.time.Instant;
import java.time.LocalDate;

/**
 * Uma ordem no PnT (um NewOrderSingle). Replace e cancel mudam a mesma ordem.
 * Mutável e confinada à thread do motor.
 */
public final class Ordem {

    public Long id;
    public LocalDate data;
    public int uid;
    public long operacaoId;
    public Papel papel;
    public String clordidBase;
    public String clordidAtual;
    public String orderId37;
    public String secId198;
    public int estrategia;
    public Lado lado;
    public int qtd;
    public BigDecimal preco;
    public BigDecimal precoStop;
    public BigDecimal precoStopLim;
    public EstadoOrdem estado = EstadoOrdem.ENVIANDO;
    /** CumQty (14) mais alto já aplicado. A posição é derivada daqui. */
    public int qtdExec;
    public BigDecimal precoMedio;
    public String motivoGestor;
    public String motivoRejeicao;

    // ---- estado volátil (não persistido; reconstruído ou irrelevante após restart) ----
    public Pendencia pendencia = Pendencia.NENHUMA;
    /** ClOrdID do cancel/replace em voo. */
    public String clordidPendente;
    public Instant pendenciaDesde;
    public Instant enviadaEm;
    /** Desde quando está em 39=A. */
    public Instant pendenteNovaDesde;
    /** Valores que o replace em voo vai aplicar quando confirmar (39=5). */
    public Integer qtdPendente;
    public BigDecimal precoPendente;
    public BigDecimal precoStopPendente;
    public BigDecimal precoStopLimPendente;
    /** Um cancel pedido enquanto havia replace em voo. */
    public boolean cancelarAposPendencia;

    /** Para o PnT, até prova em contrário. DESCONHECIDA (sumiu do mass status) não conta como viva. */
    public boolean isViva() {
        return !estado.isFinal() && estado != EstadoOrdem.DESCONHECIDA;
    }

    /** Cancelamentos enviados e o último envio (evita laço quando o PnT rejeita o cancel). */
    public int cancelamentos;
    public Instant ultimoCancelEm;

    /** Já alertou que ficou em 39=A tempo demais. */
    public boolean alertouPendente;

    public int restante() {
        return qtd - qtdExec;
    }

    /** Contribuição para a posição: + compra, - venda. */
    public int posicao() {
        return lado.sinal * qtdExec;
    }

    @Override
    public String toString() {
        return "Ordem[" + clordidAtual + " 37=" + orderId37 + " " + papel + " " + lado + " " + qtdExec + "/" + qtd + " " + estado + "]";
    }
}
