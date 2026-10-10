package br.com.gammaquant.kalmanordens;

import br.com.gammaquant.kalmanordens.alerta.Notificador;
import br.com.gammaquant.kalmanordens.dominio.Cotacao;
import br.com.gammaquant.kalmanordens.dominio.PedidoOrdem;
import br.com.gammaquant.kalmanordens.fix.SessaoFix;
import br.com.gammaquant.kalmanordens.motor.Motor;
import br.com.gammaquant.kalmanordens.persistencia.Db;
import br.com.gammaquant.kalmanordens.persistencia.Repositorio;
import br.com.gammaquant.kalmanordens.preco.FontePreco;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.zaxxer.hikari.HikariDataSource;

import java.io.IOException;
import java.math.BigDecimal;
import java.net.ServerSocket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.function.BooleanSupplier;

/**
 * Serviço completo (motor + sessão FIX real + H2) contra o PnT falso.
 * Tempos encurtados; o resto é a configuração de produção.
 */
public final class Bancada implements AutoCloseable {

    public static final String UID = "7";

    public final PntFalso pnt;
    public final Path dirFix;
    public final String dbUrl;
    public final HikariDataSource ds;
    public final Repositorio repo;
    public final PrecoFalso preco = new PrecoFalso();
    public final List<String> alertas = new CopyOnWriteArrayList<>();
    public Config cfg;
    public Motor motor;
    public SessaoFix fix;
    private final ObjectMapper json = new ObjectMapper();
    private long operacao = 9045770;

    public static final class PrecoFalso implements FontePreco {
        public volatile BigDecimal bid = new BigDecimal("130000");
        public volatile BigDecimal ask = new BigDecimal("130005");
        public volatile BigDecimal ultimo = new BigDecimal("130000");
        public volatile String status84 = "0";
        public volatile Duration idade = Duration.ofMillis(100);

        @Override
        public Optional<Cotacao> atual() {
            return Optional.of(new Cotacao(bid, ask, ultimo, status84, idade));
        }

        public void em(String b, String a, String u) {
            bid = new BigDecimal(b);
            ask = new BigDecimal(a);
            ultimo = new BigDecimal(u);
        }
    }

    public Bancada() throws Exception {
        this(Map.of());
    }

    public Bancada(Map<String, String> extra) throws Exception {
        this(new PntFalso(portaLivre()), Files.createTempDirectory("kalman-fix"), "jdbc:h2:mem:k" + System.nanoTime()
                + ";MODE=MariaDB;DATABASE_TO_LOWER=TRUE;DB_CLOSE_DELAY=-1", extra);
        pnt.iniciar();
        subir();
    }

    private Bancada(PntFalso pnt, Path dirFix, String dbUrl, Map<String, String> extra) {
        this.pnt = pnt;
        this.dirFix = dirFix;
        this.dbUrl = dbUrl;
        this.ds = Db.abrir(dbUrl, "sa", "");
        Db.migrar(ds);
        this.repo = new Repositorio(ds, Clock.systemUTC());
        Map<String, String> v = new HashMap<>();
        v.put("FIX_SENDER_COMP_ID", "KALMAN");
        v.put("FIX_TARGET_COMP_ID", "PNT_DSA");
        v.put("FIX_HOST", "127.0.0.1");
        v.put("FIX_PORTA", String.valueOf(pnt.porta));
        v.put("FIX_DIR", dirFix.toString());
        v.put("FIX_INICIO", "00:00:00");
        v.put("FIX_FIM", "23:59:59");
        v.put("FIX_RECONEXAO_S", "1");
        v.put("FIX_HEARTBEAT", "5");
        v.put("ORDENS_CONTA", "12345");
        v.put("ORDENS_SIMBOLO", "WINZ26");
        v.put("ORDENS_UID_REAL", UID);
        v.put("DB_URL", dbUrl);
        v.put("JANELA_INICIO", "00:00:00");
        v.put("JANELA_FIM", "23:59:59");
        v.put("HORA_ZERAGEM", "23:59:59");
        v.put("TIMEOUT_ENTRADA_S", "0.5");
        v.put("TIMEOUT_SAIDA_S", "0.4");
        v.put("ESPERA_STOP_S", "0.6");
        v.put("RETENTATIVA_PROTECAO_S", "0.2");
        v.put("ALERTA_SEM_PROTECAO_S", "1");
        v.put("ALERTA_PENDENTE_S", "0.5");
        v.put("MASS_STATUS_SILENCIO_S", "0.3");
        v.put("TICK_MOTOR_S", "0.02");
        v.put("LIMITE_INTERVALO_ENTRADAS_S", "0");
        v.put("LIMITE_ENTRADAS_MIN", "100");
        v.put("LIMITE_MENSAGENS_MIN", "1000");
        v.putAll(extra);
        this.cfg = new Config(v);
    }

    /** Sobe motor + sessão, espera o logon e a conciliação. */
    public void subir() throws Exception {
        Notificador n = alertas::add;
        motor = new Motor(cfg, Clock.systemUTC(), repo, preco, n);
        fix = new SessaoFix(cfg, motor);
        motor.usar(fix);
        motor.iniciar();
        fix.iniciar();
        esperar("logon", () -> fix.logado(), 10_000);
        esperar("conciliação", () -> !Boolean.TRUE.equals(saude().get("conciliando")), 10_000);
    }

    /** Derruba o processo (sem cancelar nada no PnT) e sobe de novo com o mesmo banco e store. */
    public void reiniciar() throws Exception {
        fix.parar();
        motor.parar();
        esperar("PnT ver a queda", () -> !pnt.logado(), 5_000);
        subir();
    }

    public long novaOperacao() {
        return ++operacao;
    }

    public Map<String, Object> entrar(long op, String lado) throws Exception {
        return motor.pedir(new PedidoOrdem(op, Integer.parseInt(UID), "ENTRADA", lado, 1, new BigDecimal("500"), "SINAL",
                "2026-10-13T10:15:02.123-03:00")).get();
    }

    public Map<String, Object> sair(long op, String lado, String motivo) throws Exception {
        return motor.pedir(new PedidoOrdem(op, Integer.parseInt(UID), "SAIDA", lado, 1, null, motivo, null)).get();
    }

    public Map<String, Object> saude() {
        try {
            return motor.saude().get();
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    public Map<String, Object> operacao(long op) throws Exception {
        return motor.consultar(op).get();
    }

    /** Eventos publicados (tipo + dados). */
    public List<JsonNode> eventos() {
        List<JsonNode> r = new ArrayList<>();
        for (Repositorio.Publicado p : repo.publicadosDesde(0, 10_000)) {
            try {
                JsonNode d = json.readTree(p.dados());
                ((com.fasterxml.jackson.databind.node.ObjectNode) d).put("_tipo", p.tipo());
                r.add(d);
            } catch (IOException e) {
                throw new IllegalStateException(e);
            }
        }
        return r;
    }

    public List<JsonNode> eventos(String tipo) {
        return eventos().stream().filter(e -> tipo.equals(e.get("_tipo").asText())).toList();
    }

    public List<JsonNode> eventos(String tipo, long op) {
        return eventos(tipo).stream().filter(e -> e.path("operacao_id").asLong() == op).toList();
    }

    public boolean temAlerta(String trecho) {
        return alertas.stream().anyMatch(a -> a.contains(trecho));
    }

    public static void esperar(String oque, BooleanSupplier cond, long ms) throws InterruptedException {
        long fim = System.currentTimeMillis() + ms;
        while (System.currentTimeMillis() < fim) {
            if (cond.getAsBoolean()) return;
            Thread.sleep(20);
        }
        throw new AssertionError("Tempo esgotado esperando: " + oque);
    }

    public void esperar(String oque, BooleanSupplier cond) throws InterruptedException {
        esperar(oque, cond, 5_000);
    }

    @Override
    public void close() {
        try {
            fix.parar();
        } catch (Exception ignorado) {
        }
        motor.parar();
        pnt.parar();
        ds.close();
    }

    private static int portaLivre() throws IOException {
        try (ServerSocket s = new ServerSocket(0)) {
            return s.getLocalPort();
        }
    }
}
