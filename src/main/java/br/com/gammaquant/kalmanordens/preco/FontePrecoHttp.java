package br.com.gammaquant.kalmanordens.preco;

import br.com.gammaquant.kalmanordens.dominio.Cotacao;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.math.BigDecimal;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.Optional;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * Lê /precos/atual do marketdata Go (9090) numa thread própria, a cada 200 ms.
 * O motor só lê o último valor (nunca espera HTTP). A idade devolvida soma a
 * idade informada pelo marketdata e o tempo desde a leitura.
 *
 * PENDENTE: o formato do JSON não está no repositório local do marketdata. Este
 * leitor assume um objeto com bid, ask, ultimo, status84 e idade_ms (ou um mapa
 * por símbolo com esses campos). Ajustar ao contrato real antes da F1.
 */
public final class FontePrecoHttp implements FontePreco {

    private static final Logger log = LoggerFactory.getLogger(FontePrecoHttp.class);

    private final HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofMillis(300)).build();
    private final ObjectMapper json = new ObjectMapper();
    private final ScheduledExecutorService exec = Executors.newSingleThreadScheduledExecutor(r -> {
        Thread t = new Thread(r, "precos");
        t.setDaemon(true);
        return t;
    });
    private final String url;
    private final String simbolo;
    private final Clock relogio;
    private volatile Cotacao ultima;
    private volatile Instant lidaEm;

    public FontePrecoHttp(String url, String simbolo, Clock relogio) {
        this.url = url;
        this.simbolo = simbolo;
        this.relogio = relogio;
    }

    public void iniciar() {
        exec.scheduleWithFixedDelay(this::ler, 0, 200, TimeUnit.MILLISECONDS);
    }

    public void parar() {
        exec.shutdownNow();
    }

    @Override
    public Optional<Cotacao> atual() {
        Cotacao c = ultima;
        Instant em = lidaEm;
        if (c == null || em == null) return Optional.empty();
        Duration idade = c.idade().plus(Duration.between(em, relogio.instant()));
        return Optional.of(new Cotacao(c.bid(), c.ask(), c.ultimo(), c.status84(), idade));
    }

    private void ler() {
        try {
            HttpRequest req = HttpRequest.newBuilder(URI.create(url)).timeout(Duration.ofMillis(500)).GET().build();
            HttpResponse<String> r = http.send(req, HttpResponse.BodyHandlers.ofString());
            if (r.statusCode() != 200) return;
            JsonNode n = json.readTree(r.body());
            if (n.has(simbolo)) n = n.get(simbolo);
            ultima = new Cotacao(dec(n, "bid"), dec(n, "ask"), dec(n, "ultimo"),
                    n.path("status84").asText(null), Duration.ofMillis(n.path("idade_ms").asLong(0)));
            lidaEm = relogio.instant();
        } catch (Exception e) {
            log.debug("Falha ao ler preço: {}", e.toString());
        }
    }

    private static BigDecimal dec(JsonNode n, String campo) {
        JsonNode v = n.get(campo);
        return v == null || v.isNull() ? null : new BigDecimal(v.asText());
    }
}
