package br.com.gammaquant.kalmanordens;

import quickfix.Application;
import quickfix.DefaultMessageFactory;
import quickfix.FieldNotFound;
import quickfix.MemoryStoreFactory;
import quickfix.Message;
import quickfix.ScreenLogFactory;
import quickfix.Session;
import quickfix.SessionID;
import quickfix.SessionSettings;
import quickfix.SocketAcceptor;
import quickfix.field.MsgType;

import java.math.BigDecimal;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * PnT falso (Parte 11.1): acceptor QuickFIX/J local, roteirizável por cenário.
 * Formato das respostas copiado dos exemplos do RoE 2025 (1014, VWAP, mass status).
 *
 * Comportamento padrão: D → 39=A → 39=0; F → 39=6 → 39=4; G → 39=E → 39=5; AF → 150=I por ordem viva.
 * Cada passo pode ser trocado por um roteiro no teste.
 */
public final class PntFalso implements Application {

    /** Uma ordem no "robô". */
    public static final class OrdemPnt {
        public String id37;
        public String id198;
        public String clordid;
        public String clordidOriginal;
        public char lado;
        public int qtd;
        public BigDecimal preco;
        public int estrategia;
        public String stop;
        public String stopLim;
        public int cum;
        public BigDecimal somaPx = BigDecimal.ZERO;
        public char status = 'A';
        public String conta;
        public String simbolo;

        public boolean viva() {
            return status != '2' && status != '4' && status != '8' && status != 'C';
        }

        public BigDecimal medio() {
            return cum == 0 ? BigDecimal.ZERO : somaPx.divide(BigDecimal.valueOf(cum), 4, java.math.RoundingMode.HALF_EVEN);
        }
    }

    /** Ganchos do roteiro. Devolve true se tratou (pula o padrão). */
    public interface Roteiro {
        default boolean aoNovo(PntFalso pnt, OrdemPnt o, Message m) { return false; }
        default boolean aoCancel(PntFalso pnt, OrdemPnt o, Message m) { return false; }
        default boolean aoReplace(PntFalso pnt, OrdemPnt o, Message m) { return false; }
        default boolean aoMassStatus(PntFalso pnt, Message m) { return false; }
    }

    public final List<Message> recebidas = new CopyOnWriteArrayList<>();
    public final Map<String, OrdemPnt> porClordid = Collections.synchronizedMap(new LinkedHashMap<>());
    public final List<OrdemPnt> ordens = new CopyOnWriteArrayList<>();
    /** Ordens "do PT" na mesma conta, devolvidas no mass status. */
    public final List<OrdemPnt> ordensDoPt = new CopyOnWriteArrayList<>();
    public volatile Roteiro roteiro = new Roteiro() { };
    private final AtomicInteger seq37 = new AtomicInteger(100);
    private final AtomicInteger seqExec = new AtomicInteger(1);
    private volatile SessionID sessao;
    private SocketAcceptor acceptor;
    public final int porta;

    public PntFalso(int porta) {
        this.porta = porta;
    }

    public void iniciar() throws Exception {
        SessionSettings s = new SessionSettings();
        SessionID id = new SessionID("FIX.4.4", "PNT_DSA", "KALMAN");
        s.setString(id, "ConnectionType", "acceptor");
        s.setLong(id, "SocketAcceptPort", porta);
        s.setString(id, "StartTime", "00:00:00");
        s.setString(id, "EndTime", "00:00:00");
        s.setString(id, "UseDataDictionary", "Y");
        s.setString(id, "DataDictionary", "fix/dsa_FIX44.xml");
        s.setString(id, "ValidateUserDefinedFields", "N");
        s.setString(id, "AllowUnknownMsgFields", "Y");
        s.setString(id, "ValidateFieldsOutOfOrder", "N");
        s.setString(id, "CheckLatency", "N");
        acceptor = new SocketAcceptor(this, new MemoryStoreFactory(), s, new ScreenLogFactory(false, false, false), new DefaultMessageFactory());
        acceptor.start();
    }

    public void parar() {
        if (acceptor != null) acceptor.stop(true);
    }

    /** Derruba a conexão (simula queda). O store em memória mantém as sequências. */
    public void derrubar() throws java.io.IOException {
        Session s = Session.lookupSession(sessao);
        if (s != null) s.disconnect("teste: queda", false);
    }

    public boolean logado() {
        Session s = sessao == null ? null : Session.lookupSession(sessao);
        return s != null && s.isLoggedOn();
    }

    public List<Message> recebidas(String msgType) {
        List<Message> r = new ArrayList<>();
        for (Message m : recebidas) if (msgType.equals(tipo(m))) r.add(m);
        return r;
    }

    // ------------------------------------------------------------ respostas

    public void enviar(Message m) {
        try {
            Session.sendToTarget(m, sessao);
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    /** Mensagem montada mas guardada no store sem ir ao fio (para testar ResendRequest). */
    public Message er(OrdemPnt o, char execType, char status, String clordid, String origClordid) {
        Message m = new Message();
        m.getHeader().setString(MsgType.FIELD, MsgType.EXECUTION_REPORT);
        m.setString(37, o.id37);
        if (o.id198 != null) m.setString(198, o.id198);
        m.setString(11, clordid);
        if (origClordid != null) m.setString(41, origClordid);
        m.setString(17, "Exec_" + seqExec.getAndIncrement() + ":" + o.id37);
        m.setChar(150, execType);
        m.setChar(39, status);
        m.setString(1, o.conta == null ? "12345" : o.conta);
        m.setString(55, o.simbolo == null ? "WINZ26" : o.simbolo);
        m.setString(207, "XBMF");
        m.setChar(54, o.lado);
        m.setInt(38, o.qtd);
        m.setChar(40, '2');
        if (o.preco != null) m.setString(44, o.preco.toPlainString());
        m.setInt(847, o.estrategia);
        m.setChar(59, '0');
        m.setInt(151, Math.max(0, o.qtd - o.cum));
        m.setInt(14, o.cum);
        m.setString(6, o.medio().toPlainString());
        if (o.estrategia == 1014 && o.stop != null) {
            // eco com ruído de float, como no RoE 2025
            m.setString(20828, o.stop + ".0000000001");
            m.setString(20829, o.stopLim + ".0000000001");
        }
        m.setUtcTimeStamp(60, LocalDateTime.now(ZoneOffset.UTC), true);
        return m;
    }

    public void ack(OrdemPnt o) {
        o.status = 'A';
        enviar(er(o, 'A', 'A', o.clordid, null));
        o.status = '0';
        if (o.id198 == null) o.id198 = "PNT-" + o.id37.substring(4);
        enviar(er(o, '0', '0', o.clordid, null));
    }

    public void rejeitar(OrdemPnt o, String texto, String codigo) {
        o.status = '8';
        Message m = er(o, '8', '8', o.clordid, null);
        m.setString(58, texto);
        if (codigo != null) m.setString(103, codigo);
        enviar(m);
    }

    public void fill(OrdemPnt o, int qtd, BigDecimal px) {
        enviar(mensagemFill(o, qtd, px));
    }

    public Message mensagemFill(OrdemPnt o, int qtd, BigDecimal px) {
        o.cum += qtd;
        o.somaPx = o.somaPx.add(px.multiply(BigDecimal.valueOf(qtd)));
        o.status = o.cum >= o.qtd ? '2' : '1';
        Message m = er(o, 'F', o.status, o.clordid, null);
        m.setInt(32, qtd);
        m.setString(31, px.toPlainString());
        m.setString(1057, "N");
        return m;
    }

    public void cancelar(OrdemPnt o, String clCancel) {
        String antigo = o.clordid;
        enviar(er(o, '6', '6', clCancel, antigo));
        o.status = '4';
        o.clordid = clCancel;
        porClordid.put(clCancel, o);
        Message m = er(o, '4', '4', clCancel, antigo);
        m.setInt(151, 0);
        enviar(m);
    }

    public void rejeitarCancel(OrdemPnt o, String clPedido, char resposta, String texto) {
        Message m = new Message();
        m.getHeader().setString(MsgType.FIELD, MsgType.ORDER_CANCEL_REJECT);
        m.setString(37, o.id37);
        m.setString(11, clPedido);
        m.setString(41, o.clordid);
        m.setChar(39, o.status);
        m.setChar(434, resposta);
        m.setInt(102, 0);
        m.setString(58, texto);
        enviar(m);
    }

    public void substituir(OrdemPnt o, Message g) throws FieldNotFound {
        String cl = g.getString(11);
        String antigo = o.clordid;
        enviar(er(o, 'E', 'E', cl, antigo));
        o.qtd = g.getInt(38);
        if (g.isSetField(44)) o.preco = new BigDecimal(g.getString(44));
        if (g.isSetField(20828)) o.stop = g.getString(20828);
        if (g.isSetField(20829)) o.stopLim = g.getString(20829);
        o.clordid = cl;
        porClordid.put(cl, o);
        o.status = o.cum > 0 ? '1' : '0';
        enviar(er(o, '5', '5', cl, antigo));
    }

    public void massStatus(Message af, List<OrdemPnt> quais) throws FieldNotFound {
        String req = af.getString(584);
        for (OrdemPnt o : quais) {
            Message m = er(o, 'I', o.status, o.clordid, o.clordidOriginal);
            m.setString(584, req);
            enviar(m);
        }
    }

    public void businessReject(Message ref, int razao, String texto) throws FieldNotFound {
        Message m = new Message();
        m.getHeader().setString(MsgType.FIELD, MsgType.BUSINESS_MESSAGE_REJECT);
        m.setInt(45, ref.getHeader().getInt(34));
        m.setString(372, ref.getHeader().getString(MsgType.FIELD));
        if (ref.isSetField(584)) m.setString(379, ref.getString(584));
        m.setInt(380, razao);
        m.setString(58, texto);
        enviar(m);
    }

    // ------------------------------------------------------------ Application

    @Override
    public void onCreate(SessionID id) {
        sessao = id;
    }

    @Override
    public void onLogon(SessionID id) {
    }

    @Override
    public void onLogout(SessionID id) {
    }

    @Override
    public void toAdmin(Message m, SessionID id) {
    }

    @Override
    public void fromAdmin(Message m, SessionID id) {
    }

    @Override
    public void toApp(Message m, SessionID id) {
    }

    @Override
    public void fromApp(Message m, SessionID id) throws FieldNotFound {
        recebidas.add(m);
        String t = tipo(m);
        switch (t) {
            case "D" -> {
                OrdemPnt o = new OrdemPnt();
                o.id37 = "dsa." + seq37.incrementAndGet();
                o.clordid = m.getString(11);
                o.clordidOriginal = o.clordid;
                o.lado = m.getChar(54);
                o.qtd = m.getInt(38);
                o.preco = m.isSetField(44) ? new BigDecimal(m.getString(44)) : null;
                o.estrategia = m.getInt(847);
                o.stop = m.isSetField(20828) ? m.getString(20828) : null;
                o.stopLim = m.isSetField(20829) ? m.getString(20829) : null;
                o.conta = m.getString(1);
                o.simbolo = m.getString(55);
                ordens.add(o);
                porClordid.put(o.clordid, o);
                if (!roteiro.aoNovo(this, o, m)) ack(o);
            }
            case "F" -> {
                OrdemPnt o = acharPor(m);
                if (o == null) return;
                if (!roteiro.aoCancel(this, o, m)) {
                    if (o.viva()) cancelar(o, m.getString(11));
                    else rejeitarCancel(o, m.getString(11), '1', "[DSA-41] Order already finished");
                }
            }
            case "G" -> {
                OrdemPnt o = acharPor(m);
                if (o == null) return;
                if (!roteiro.aoReplace(this, o, m)) {
                    if (o.viva()) substituir(o, m);
                    else rejeitarCancel(o, m.getString(11), '2', "[DSA-41] Order already finished");
                }
            }
            case "AF" -> {
                if (!roteiro.aoMassStatus(this, m)) {
                    List<OrdemPnt> todas = new ArrayList<>();
                    for (OrdemPnt o : ordens) if (o.viva()) todas.add(o);
                    todas.addAll(ordensDoPt);
                    massStatus(m, todas);
                }
            }
            default -> {
            }
        }
    }

    public OrdemPnt acharPor(Message m) throws FieldNotFound {
        if (m.isSetField(37)) {
            String id = m.getString(37);
            for (OrdemPnt o : ordens) if (o.id37.equals(id)) return o;
        }
        return m.isSetField(41) ? porClordid.get(m.getString(41)) : null;
    }

    public OrdemPnt ultima(int estrategia) {
        OrdemPnt r = null;
        for (OrdemPnt o : ordens) if (o.estrategia == estrategia) r = o;
        return r;
    }

    public static String tipo(Message m) {
        try {
            return m.getHeader().getString(MsgType.FIELD);
        } catch (FieldNotFound e) {
            return "?";
        }
    }
}
