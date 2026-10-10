package br.com.gammaquant.kalmanordens.fix;

import br.com.gammaquant.kalmanordens.Config;
import br.com.gammaquant.kalmanordens.dominio.Ordem;
import quickfix.Message;
import quickfix.field.MsgType;

import java.math.BigDecimal;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneOffset;

/**
 * Montagem das mensagens enviadas ao PnT (Parte 3). Sem estado.
 *
 * Identificação copiada do createSO que funciona em produção: 1, 116, 142, 21=4,
 * 60 em UTC, 207. [PnT-3]: se o usuário novo exigir PartyIDs ou 35508/35509, é aqui.
 */
public final class Mensagens {

    public static final int TAG_PRICE_STOP_LOSS = 20828;
    public static final int TAG_PRICE_SEND_ORDER_LOSS = 20829;
    public static final int TAG_TARGET_STRATEGY = 847;
    public static final int ESTRATEGIA_AGENDADA = 1031;
    public static final int ESTRATEGIA_STOP = 1014;

    private final Config cfg;
    private final Clock relogio;

    public Mensagens(Config cfg, Clock relogio) {
        this.cfg = cfg;
        this.relogio = relogio;
    }

    /** NewOrderSingle (35=D) com os valores correntes da ordem. */
    public Message nova(Ordem o) {
        Message m = base(MsgType.ORDER_SINGLE);
        corpo(m, o.clordidAtual, o, o.qtd, o.preco, o.precoStop, o.precoStopLim);
        return m;
    }

    /**
     * OrderCancelReplaceRequest (35=G). Manda o 37 e o 41 verdadeiro (ClOrdID corrente),
     * nunca 41=0. Reenvia a ordem inteira, como nos exemplos do RoE 2025.
     */
    public Message replace(Ordem o, String novoClordid, int qtd, BigDecimal preco, BigDecimal stop, BigDecimal stopLim) {
        Message m = base(MsgType.ORDER_CANCEL_REPLACE_REQUEST);
        corpo(m, novoClordid, o, qtd, preco, stop, stopLim);
        m.setString(41, o.clordidAtual);
        if (o.orderId37 != null) m.setString(37, o.orderId37);
        return m;
    }

    /** OrderCancelRequest (35=F), com a ordem inteira + 41 + 37. */
    public Message cancel(Ordem o, String novoClordid) {
        Message m = base(MsgType.ORDER_CANCEL_REQUEST);
        corpo(m, novoClordid, o, o.qtd, o.preco, o.precoStop, o.precoStopLim);
        m.setString(41, o.clordidAtual);
        if (o.orderId37 != null) m.setString(37, o.orderId37);
        return m;
    }

    /** OrderMassStatusRequest (35=AF) por conta (585=9). */
    public Message massStatus(String reqId) {
        Message m = base(MsgType.ORDER_MASS_STATUS_REQUEST);
        m.setString(584, reqId);
        m.setInt(585, 9);
        m.setString(1, cfg.conta());
        return m;
    }

    private Message base(String msgType) {
        Message m = new Message();
        m.getHeader().setString(MsgType.FIELD, msgType);
        if (!cfg.onBehalfOfSubId().isBlank()) m.getHeader().setString(116, cfg.onBehalfOfSubId());
        if (!cfg.senderLocationId().isBlank()) m.getHeader().setString(142, cfg.senderLocationId());
        return m;
    }

    private void corpo(Message m, String clordid, Ordem o, int qtd, BigDecimal preco, BigDecimal stop, BigDecimal stopLim) {
        m.setString(1, cfg.conta());
        m.setString(11, clordid);
        m.setChar(21, '4');
        m.setInt(38, qtd);
        m.setChar(40, '2');
        m.setChar(54, o.lado.fix);
        m.setString(55, cfg.simbolo());
        m.setChar(59, '0');
        m.setUtcTimeStamp(60, LocalDateTime.now(relogio.withZone(ZoneOffset.UTC)), true);
        m.setString(207, cfg.bolsa());
        m.setInt(TAG_TARGET_STRATEGY, o.estrategia);
        if (o.estrategia == ESTRATEGIA_STOP) {
            // [PnT-6] sempre 40=2 com folga. O exemplo do RoE manda 44 igual ao 20829.
            m.setString(TAG_PRICE_STOP_LOSS, preco(stop));
            m.setString(TAG_PRICE_SEND_ORDER_LOSS, preco(stopLim));
            m.setString(44, preco(stopLim));
        } else {
            m.setString(44, preco(preco));
        }
    }

    static String preco(BigDecimal p) {
        if (p == null) throw new IllegalArgumentException("preço nulo");
        BigDecimal s = p.stripTrailingZeros();
        return (s.scale() < 0 ? s.setScale(0) : s).toPlainString();
    }
}
