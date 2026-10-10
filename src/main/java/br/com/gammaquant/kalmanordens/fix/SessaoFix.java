package br.com.gammaquant.kalmanordens.fix;

import br.com.gammaquant.kalmanordens.Config;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import quickfix.Application;
import quickfix.DataDictionary;
import quickfix.DefaultMessageFactory;
import quickfix.DoNotSend;
import quickfix.Field;
import quickfix.FieldNotFound;
import quickfix.FileLogFactory;
import quickfix.FileStoreFactory;
import quickfix.Message;
import quickfix.Session;
import quickfix.SessionID;
import quickfix.SessionNotFound;
import quickfix.SessionSettings;
import quickfix.SocketInitiator;
import quickfix.field.MsgType;
import quickfix.field.PossDupFlag;

import java.util.Iterator;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Sessão FIX própria do Kalman (initiator, FIX.4.4). Nunca compartilhada com o PT.
 *
 * Store e log em arquivo (Parte 7.1). Sequência sem reset intradiário [PnT-11].
 */
public final class SessaoFix implements Application, FixGateway {

    private static final Logger log = LoggerFactory.getLogger(SessaoFix.class);
    public static final String DICIONARIO_EMBUTIDO = "fix/dsa_FIX44.xml";

    private final Config cfg;
    private final OuvinteFix ouvinte;
    private volatile SessionID sessionId;
    private SocketInitiator initiator;
    private final Set<Integer> tagsDesconhecidas = ConcurrentHashMap.newKeySet();

    public SessaoFix(Config cfg, OuvinteFix ouvinte) {
        this.cfg = cfg;
        this.ouvinte = ouvinte;
    }

    public static SessionSettings configuracao(Config cfg) {
        SessionSettings s = new SessionSettings();
        SessionID id = new SessionID("FIX.4.4", cfg.fixSenderCompId(), cfg.fixTargetCompId());
        s.setString(id, "ConnectionType", "initiator");
        s.setString(id, "SocketConnectHost", cfg.fixHost());
        s.setLong(id, "SocketConnectPort", cfg.fixPorta());
        s.setString(id, "StartTime", cfg.fixInicio().toString());
        s.setString(id, "EndTime", cfg.fixFim().toString());
        s.setString(id, "TimeZone", "America/Sao_Paulo");
        s.setLong(id, "HeartBtInt", cfg.fixHeartbeat());
        s.setLong(id, "ReconnectInterval", cfg.fixReconexaoS());
        s.setString(id, "FileStorePath", cfg.fixDiretorio() + "/store");
        s.setString(id, "FileLogPath", cfg.fixDiretorio() + "/log");
        s.setString(id, "FileLogHeartbeats", "N");
        s.setString(id, "UseDataDictionary", "Y");
        s.setString(id, "DataDictionary", cfg.fixDicionario().isBlank() ? DICIONARIO_EMBUTIDO : cfg.fixDicionario());
        // As respostas de 2025 trazem tags fora do dicionário (20828–20835, 911, 5600...). [PnT-2]
        s.setString(id, "ValidateUserDefinedFields", "N");
        s.setString(id, "AllowUnknownMsgFields", "Y");
        s.setString(id, "ValidateFieldsOutOfOrder", "N");
        s.setString(id, "ResetOnLogon", cfg.fixResetOnLogon() ? "Y" : "N");
        s.setString(id, "ResetOnLogout", "N");
        s.setString(id, "ResetOnDisconnect", "N");
        s.setString(id, "PersistMessages", "Y");
        // Diferença de relógio com o PnT não pode descartar fill.
        s.setString(id, "CheckLatency", "N");
        return s;
    }

    public void iniciar() throws Exception {
        SessionSettings s = configuracao(cfg);
        initiator = new SocketInitiator(this, new FileStoreFactory(s), s, new FileLogFactory(s), new DefaultMessageFactory());
        initiator.start();
    }

    public void parar() {
        if (initiator != null) initiator.stop();
    }

    // ---------------- FixGateway ----------------

    @Override
    public boolean logado() {
        Session s = sessao();
        return s != null && s.isLoggedOn();
    }

    @Override
    public boolean enviar(Message m) {
        SessionID id = sessionId;
        if (id == null || !logado()) return false;
        try {
            return Session.sendToTarget(m, id);
        } catch (SessionNotFound e) {
            log.error("Sessão não encontrada ao enviar", e);
            return false;
        }
    }

    @Override
    public int[] sequencias() {
        Session s = sessao();
        if (s == null) return new int[] {0, 0};
        try {
            return new int[] {s.getExpectedTargetNum(), s.getExpectedSenderNum()};
        } catch (Exception e) {
            return new int[] {0, 0};
        }
    }

    private Session sessao() {
        SessionID id = sessionId;
        return id == null ? null : Session.lookupSession(id);
    }

    // ---------------- Application ----------------

    @Override
    public void onCreate(SessionID id) {
        this.sessionId = id;
    }

    @Override
    public void onLogon(SessionID id) {
        log.info("FIX logado: {}", id);
        ouvinte.aoLogar();
    }

    @Override
    public void onLogout(SessionID id) {
        log.warn("FIX deslogado: {}", id);
        ouvinte.aoCair();
    }

    @Override
    public void toAdmin(Message m, SessionID id) {
        try {
            if (MsgType.LOGON.equals(m.getHeader().getString(MsgType.FIELD))) {
                if (!cfg.fixUsuario().isBlank()) m.setString(553, cfg.fixUsuario());
                if (!cfg.fixSenha().isBlank()) m.setString(554, cfg.fixSenha());
            }
        } catch (FieldNotFound e) {
            // toda mensagem tem MsgType
        }
    }

    @Override
    public void fromAdmin(Message m, SessionID id) {
        try {
            if (MsgType.REJECT.equals(m.getHeader().getString(MsgType.FIELD))) {
                ouvinte.aoReceber((Message) m.clone());
            }
        } catch (FieldNotFound e) {
            log.warn("Mensagem admin sem MsgType");
        }
    }

    /**
     * Uma ordem reenviada por ResendRequest seria uma ordem velha chegando de novo:
     * nunca reenviar mensagem de aplicação (o QuickFIX/J manda GapFill no lugar).
     */
    @Override
    public void toApp(Message m, SessionID id) throws DoNotSend {
        try {
            if (m.getHeader().isSetField(PossDupFlag.FIELD) && m.getHeader().getBoolean(PossDupFlag.FIELD)) {
                log.warn("Reenvio de mensagem de aplicação suprimido: {}", m);
                throw new DoNotSend();
            }
        } catch (FieldNotFound e) {
            // isSetField acabou de confirmar
        }
    }

    @Override
    public void fromApp(Message m, SessionID id) {
        registrarTagsDesconhecidas(m, id);
        ouvinte.aoReceber((Message) m.clone());
    }

    private void registrarTagsDesconhecidas(Message m, SessionID id) {
        Session s = Session.lookupSession(id);
        DataDictionary dd = s == null ? null : s.getDataDictionary();
        if (dd == null) return;
        Iterator<Field<?>> it = m.iterator();
        while (it.hasNext()) {
            int tag = it.next().getTag();
            if (!dd.isField(tag) && tagsDesconhecidas.add(tag)) {
                log.info("Tag fora do dicionário recebida: {} (registrada uma vez)", tag);
            }
        }
    }
}
