package br.com.gammaquant.kalmanordens.persistencia;

import br.com.gammaquant.kalmanordens.dominio.EstadoOperacao;
import br.com.gammaquant.kalmanordens.dominio.EstadoOrdem;
import br.com.gammaquant.kalmanordens.dominio.Lado;
import br.com.gammaquant.kalmanordens.dominio.Operacao;
import br.com.gammaquant.kalmanordens.dominio.Ordem;
import br.com.gammaquant.kalmanordens.dominio.Papel;

import javax.sql.DataSource;
import java.math.BigDecimal;
import java.sql.Connection;
import java.sql.Date;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.SQLIntegrityConstraintViolationException;
import java.sql.Statement;
import java.sql.Timestamp;
import java.sql.Types;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * JDBC puro sobre as tabelas da Parte 8. Chamado só pela thread do motor
 * (e por leituras do HTTP, que não alteram nada).
 */
public final class Repositorio {

    public static final ZoneId FUSO = ZoneId.of("America/Sao_Paulo");

    private final DataSource ds;
    private final Clock relogio;

    public Repositorio(DataSource ds, Clock relogio) {
        this.ds = ds;
        this.relogio = relogio;
    }

    private Timestamp agora() {
        return Timestamp.valueOf(LocalDateTime.now(relogio.withZone(FUSO)));
    }

    private static Timestamp ts(Instant i) {
        return i == null ? null : Timestamp.valueOf(LocalDateTime.ofInstant(i, FUSO));
    }

    // ------------------------------------------------------------ operação

    public void inserirOperacao(Operacao op) {
        String sql = "INSERT INTO kalmanordemoperacao (data, operacao_id, uid, lado, qtd_pedida, stop_pts, estado, motivo_entrada, ts_decisao, divergente, criado_em, atualizado_em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)";
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql, Statement.RETURN_GENERATED_KEYS)) {
            Timestamp t = agora();
            ps.setDate(1, Date.valueOf(op.data));
            ps.setLong(2, op.operacaoId);
            ps.setInt(3, op.uid);
            ps.setString(4, op.lado.name());
            ps.setInt(5, op.qtdPedida);
            ps.setBigDecimal(6, op.stopPts);
            ps.setString(7, op.estado.name());
            ps.setString(8, op.motivoEntrada);
            ps.setString(9, op.tsDecisao);
            ps.setInt(10, op.divergente ? 1 : 0);
            ps.setTimestamp(11, t);
            ps.setTimestamp(12, t);
            ps.executeUpdate();
            op.id = chave(ps);
        } catch (SQLException e) {
            throw new FalhaBanco("inserirOperacao " + op.operacaoId, e);
        }
    }

    public boolean operacaoExiste(long operacaoId) {
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement("SELECT 1 FROM kalmanordemoperacao WHERE operacao_id = ?")) {
            ps.setLong(1, operacaoId);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next();
            }
        } catch (SQLException e) {
            throw new FalhaBanco("operacaoExiste", e);
        }
    }

    public void atualizarOperacao(Operacao op) {
        String sql = "UPDATE kalmanordemoperacao SET estado=?, motivo_saida=?, origem_saida=?, saida_pedida_em=?, divergente=?, pnl_pts=?, atualizado_em=? WHERE id=?";
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setString(1, op.estado.name());
            ps.setString(2, op.motivoSaida);
            ps.setString(3, op.origemSaida);
            ps.setTimestamp(4, ts(op.saidaPedidaEm));
            ps.setInt(5, op.divergente ? 1 : 0);
            ps.setBigDecimal(6, op.pnlPts);
            ps.setTimestamp(7, agora());
            ps.setLong(8, op.id);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new FalhaBanco("atualizarOperacao " + op.operacaoId, e);
        }
    }

    // ------------------------------------------------------------ ordem

    public void inserirOrdem(Ordem o) {
        String sql = "INSERT INTO kalmanordem (data, uid, operacao_id, papel, clordid_base, clordid_atual, orderid37, secid198, estrategia, lado, qtd, preco, preco_stop, preco_stop_lim, estado, qtd_exec, preco_medio, motivo_gestor, motivo_rejeicao, criado_em, atualizado_em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)";
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql, Statement.RETURN_GENERATED_KEYS)) {
            Timestamp t = agora();
            ps.setDate(1, Date.valueOf(o.data));
            ps.setInt(2, o.uid);
            ps.setLong(3, o.operacaoId);
            ps.setString(4, o.papel.name());
            ps.setString(5, o.clordidBase);
            ps.setString(6, o.clordidAtual);
            ps.setString(7, o.orderId37);
            ps.setString(8, o.secId198);
            ps.setInt(9, o.estrategia);
            ps.setString(10, o.lado.name());
            ps.setInt(11, o.qtd);
            ps.setBigDecimal(12, o.preco);
            ps.setBigDecimal(13, o.precoStop);
            ps.setBigDecimal(14, o.precoStopLim);
            ps.setString(15, o.estado.name());
            ps.setInt(16, o.qtdExec);
            ps.setBigDecimal(17, o.precoMedio);
            ps.setString(18, o.motivoGestor);
            ps.setString(19, o.motivoRejeicao);
            ps.setTimestamp(20, t);
            ps.setTimestamp(21, t);
            ps.executeUpdate();
            o.id = chave(ps);
        } catch (SQLException e) {
            throw new FalhaBanco("inserirOrdem " + o.clordidBase, e);
        }
    }

    public void atualizarOrdem(Ordem o) {
        String sql = "UPDATE kalmanordem SET clordid_atual=?, orderid37=?, secid198=?, qtd=?, preco=?, preco_stop=?, preco_stop_lim=?, estado=?, qtd_exec=?, preco_medio=?, motivo_rejeicao=?, atualizado_em=? WHERE id=?";
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setString(1, o.clordidAtual);
            ps.setString(2, o.orderId37);
            ps.setString(3, o.secId198);
            ps.setInt(4, o.qtd);
            ps.setBigDecimal(5, o.preco);
            ps.setBigDecimal(6, o.precoStop);
            ps.setBigDecimal(7, o.precoStopLim);
            ps.setString(8, o.estado.name());
            ps.setInt(9, o.qtdExec);
            ps.setBigDecimal(10, o.precoMedio);
            ps.setString(11, trunc(o.motivoRejeicao, 500));
            ps.setTimestamp(12, agora());
            ps.setLong(13, o.id);
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new FalhaBanco("atualizarOrdem " + o.clordidBase, e);
        }
    }

    // ------------------------------------------------------------ evento FIX

    /** Linha de kalmanordemevento. */
    public record RegistroEvento(Long ordemId, String direcao, String msgtype, String exectype, String ordstatus,
                                 String clordid, String orderId37, String execid, Integer lastqty, BigDecimal lastpx,
                                 Integer cumqty, BigDecimal avgpx, String texto, String codigo, String fixRaw) {
    }

    /** Grava. Devolve false se o execid já existe (fill repetido: não aplicar de novo). */
    public boolean inserirEvento(RegistroEvento r) {
        String sql = "INSERT INTO kalmanordemevento (ordem_id, direcao, msgtype, exectype, ordstatus, clordid, orderid37, execid, lastqty, lastpx, cumqty, avgpx, texto, codigo, fix_raw, em) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)";
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            if (r.ordemId() == null) ps.setNull(1, Types.BIGINT); else ps.setLong(1, r.ordemId());
            ps.setString(2, r.direcao());
            ps.setString(3, r.msgtype());
            ps.setString(4, r.exectype());
            ps.setString(5, r.ordstatus());
            ps.setString(6, r.clordid());
            ps.setString(7, r.orderId37());
            ps.setString(8, r.execid());
            if (r.lastqty() == null) ps.setNull(9, Types.INTEGER); else ps.setInt(9, r.lastqty());
            ps.setBigDecimal(10, r.lastpx());
            if (r.cumqty() == null) ps.setNull(11, Types.INTEGER); else ps.setInt(11, r.cumqty());
            ps.setBigDecimal(12, r.avgpx());
            ps.setString(13, trunc(r.texto(), 1000));
            ps.setString(14, r.codigo());
            ps.setString(15, r.fixRaw());
            ps.setTimestamp(16, agora());
            ps.executeUpdate();
            return true;
        } catch (SQLIntegrityConstraintViolationException e) {
            return false;
        } catch (SQLException e) {
            throw new FalhaBanco("inserirEvento", e);
        }
    }

    /** Mensagens de ordem (D/F/G) enviadas desde um instante: base do limite "ordens por minuto". */
    public List<Instant> enviosDesde(Instant desde) {
        String sql = "SELECT em FROM kalmanordemevento WHERE direcao='ENV' AND msgtype IN ('D','F','G') AND em >= ? ORDER BY em";
        List<Instant> r = new ArrayList<>();
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setTimestamp(1, ts(desde));
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) r.add(rs.getTimestamp(1).toLocalDateTime().atZone(FUSO).toInstant());
            }
            return r;
        } catch (SQLException e) {
            throw new FalhaBanco("enviosDesde", e);
        }
    }

    // ------------------------------------------------------------ publicação

    public long publicar(String tipo, Long operacaoId, String json) {
        String sql = "INSERT INTO kalmanordempub (tipo, operacao_id, dados, em) VALUES (?,?,?,?)";
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql, Statement.RETURN_GENERATED_KEYS)) {
            ps.setString(1, tipo);
            if (operacaoId == null) ps.setNull(2, Types.BIGINT); else ps.setLong(2, operacaoId);
            ps.setString(3, json);
            ps.setTimestamp(4, agora());
            ps.executeUpdate();
            return chave(ps);
        } catch (SQLException e) {
            throw new FalhaBanco("publicar " + tipo, e);
        }
    }

    public record Publicado(long seq, String tipo, Long operacaoId, String dados, LocalDateTime em) {
    }

    public List<Publicado> publicadosDesde(long seq, int limite) {
        String sql = "SELECT seq, tipo, operacao_id, dados, em FROM kalmanordempub WHERE seq > ? ORDER BY seq LIMIT " + limite;
        List<Publicado> r = new ArrayList<>();
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setLong(1, seq);
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    long op = rs.getLong(3);
                    r.add(new Publicado(rs.getLong(1), rs.getString(2), rs.wasNull() ? null : op, rs.getString(4),
                            rs.getTimestamp(5).toLocalDateTime()));
                }
            }
            return r;
        } catch (SQLException e) {
            throw new FalhaBanco("publicadosDesde", e);
        }
    }

    // ------------------------------------------------------------ estado chave/valor

    public Optional<String> lerEstado(String chave) {
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement("SELECT valor FROM kalmanordemestado WHERE chave=?")) {
            ps.setString(1, chave);
            try (ResultSet rs = ps.executeQuery()) {
                return rs.next() ? Optional.of(rs.getString(1)) : Optional.empty();
            }
        } catch (SQLException e) {
            throw new FalhaBanco("lerEstado " + chave, e);
        }
    }

    public void gravarEstado(String chave, String valor) {
        try (Connection c = ds.getConnection()) {
            Timestamp t = agora();
            try (PreparedStatement up = c.prepareStatement("UPDATE kalmanordemestado SET valor=?, atualizado_em=? WHERE chave=?")) {
                up.setString(1, valor);
                up.setTimestamp(2, t);
                up.setString(3, chave);
                if (up.executeUpdate() > 0) return;
            }
            try (PreparedStatement in = c.prepareStatement("INSERT INTO kalmanordemestado (chave, valor, atualizado_em) VALUES (?,?,?)")) {
                in.setString(1, chave);
                in.setString(2, valor);
                in.setTimestamp(3, t);
                in.executeUpdate();
            }
        } catch (SQLException e) {
            throw new FalhaBanco("gravarEstado " + chave, e);
        }
    }

    // ------------------------------------------------------------ carga no restart (Parte 7.3)

    /** Operações do dia com as ordens, na ordem de criação. */
    public List<Operacao> carregarDia(LocalDate data) {
        Map<Long, Operacao> ops = new LinkedHashMap<>();
        try (Connection c = ds.getConnection()) {
            try (PreparedStatement ps = c.prepareStatement("SELECT * FROM kalmanordemoperacao WHERE data=? ORDER BY id")) {
                ps.setDate(1, Date.valueOf(data));
                try (ResultSet rs = ps.executeQuery()) {
                    while (rs.next()) {
                        Operacao op = new Operacao();
                        op.id = rs.getLong("id");
                        op.data = rs.getDate("data").toLocalDate();
                        op.operacaoId = rs.getLong("operacao_id");
                        op.uid = rs.getInt("uid");
                        op.lado = Lado.valueOf(rs.getString("lado"));
                        op.qtdPedida = rs.getInt("qtd_pedida");
                        op.stopPts = rs.getBigDecimal("stop_pts");
                        op.estado = EstadoOperacao.valueOf(rs.getString("estado"));
                        op.motivoEntrada = rs.getString("motivo_entrada");
                        op.tsDecisao = rs.getString("ts_decisao");
                        op.motivoSaida = rs.getString("motivo_saida");
                        op.origemSaida = rs.getString("origem_saida");
                        Timestamp sp = rs.getTimestamp("saida_pedida_em");
                        op.saidaPedidaEm = sp == null ? null : sp.toLocalDateTime().atZone(FUSO).toInstant();
                        op.divergente = rs.getInt("divergente") != 0;
                        op.pnlPts = rs.getBigDecimal("pnl_pts");
                        op.criadoEm = rs.getTimestamp("criado_em").toLocalDateTime().atZone(FUSO).toInstant();
                        ops.put(op.operacaoId, op);
                    }
                }
            }
            try (PreparedStatement ps = c.prepareStatement("SELECT * FROM kalmanordem WHERE data=? ORDER BY id")) {
                ps.setDate(1, Date.valueOf(data));
                try (ResultSet rs = ps.executeQuery()) {
                    while (rs.next()) {
                        Ordem o = new Ordem();
                        o.id = rs.getLong("id");
                        o.data = rs.getDate("data").toLocalDate();
                        o.uid = rs.getInt("uid");
                        o.operacaoId = rs.getLong("operacao_id");
                        o.papel = Papel.valueOf(rs.getString("papel"));
                        o.clordidBase = rs.getString("clordid_base");
                        o.clordidAtual = rs.getString("clordid_atual");
                        o.orderId37 = rs.getString("orderid37");
                        o.secId198 = rs.getString("secid198");
                        o.estrategia = rs.getInt("estrategia");
                        o.lado = Lado.valueOf(rs.getString("lado"));
                        o.qtd = rs.getInt("qtd");
                        o.preco = rs.getBigDecimal("preco");
                        o.precoStop = rs.getBigDecimal("preco_stop");
                        o.precoStopLim = rs.getBigDecimal("preco_stop_lim");
                        o.estado = EstadoOrdem.valueOf(rs.getString("estado"));
                        o.qtdExec = rs.getInt("qtd_exec");
                        o.precoMedio = rs.getBigDecimal("preco_medio");
                        o.motivoGestor = rs.getString("motivo_gestor");
                        o.motivoRejeicao = rs.getString("motivo_rejeicao");
                        Operacao op = ops.get(o.operacaoId);
                        if (op != null) op.ordens.add(o);
                    }
                }
            }
        } catch (SQLException e) {
            throw new FalhaBanco("carregarDia " + data, e);
        }
        // O contador n do ClOrdID continua de onde parou: maior n já usado por papel.
        Map<String, Integer> maiorN = maioresN(data);
        for (Operacao op : ops.values()) {
            for (Papel p : Papel.values()) {
                Integer n = maiorN.get(op.operacaoId + "/" + p.name());
                if (n != null) op.contadorN.put(p, n);
            }
        }
        return new ArrayList<>(ops.values());
    }

    /** Maior n de ClOrdID já enviado (inclui cancel/replace, que só aparecem nos eventos). */
    private Map<String, Integer> maioresN(LocalDate data) {
        Map<String, Integer> r = new HashMap<>();
        String sql = "SELECT e.clordid FROM kalmanordemevento e WHERE e.direcao='ENV' AND e.em >= ? AND e.clordid LIKE 'KM-%'";
        try (Connection c = ds.getConnection(); PreparedStatement ps = c.prepareStatement(sql)) {
            ps.setTimestamp(1, Timestamp.valueOf(data.atStartOfDay()));
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    String[] p = rs.getString(1).split("-");
                    if (p.length != 4) continue;
                    String k = p[2] + "/" + p[1];
                    r.merge(k, Integer.parseInt(p[3]), Math::max);
                }
            }
            return r;
        } catch (SQLException | NumberFormatException e) {
            throw new FalhaBanco("maioresN", e);
        }
    }

    private static long chave(PreparedStatement ps) throws SQLException {
        try (ResultSet rs = ps.getGeneratedKeys()) {
            if (!rs.next()) throw new SQLException("sem chave gerada");
            return rs.getLong(1);
        }
    }

    private static String trunc(String s, int n) {
        return s == null || s.length() <= n ? s : s.substring(0, n);
    }

    public static final class FalhaBanco extends RuntimeException {
        public FalhaBanco(String msg, Throwable causa) {
            super(msg, causa);
        }
    }
}
