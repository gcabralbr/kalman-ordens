package br.com.gammaquant.kalmanordens.alerta;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;

/** Telegram direto da .51 (Parte 9). Envio em thread própria, fila limitada. */
public final class Telegram implements Notificador {

    private static final Logger log = LoggerFactory.getLogger(Telegram.class);

    private final HttpClient http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(3)).build();
    private final ObjectMapper json = new ObjectMapper();
    private final ExecutorService exec = new ThreadPoolExecutor(1, 1, 0, TimeUnit.SECONDS,
            new LinkedBlockingQueue<>(500), r -> {
                Thread t = new Thread(r, "telegram");
                t.setDaemon(true);
                return t;
            }, new ThreadPoolExecutor.DiscardOldestPolicy());
    private final String token;
    private final String chat;

    public Telegram(String token, String chat) {
        this.token = token;
        this.chat = chat;
    }

    @Override
    public void enviar(String texto) {
        if (token.isBlank() || chat.isBlank()) return;
        exec.execute(() -> postar(texto));
    }

    private void postar(String texto) {
        try {
            String corpo = json.writeValueAsString(Map.of("chat_id", chat, "text", "[kalman-ordens] " + texto));
            HttpRequest req = HttpRequest.newBuilder(URI.create("https://api.telegram.org/bot" + token + "/sendMessage"))
                    .timeout(Duration.ofSeconds(5))
                    .header("Content-Type", "application/json")
                    .POST(HttpRequest.BodyPublishers.ofString(corpo))
                    .build();
            HttpResponse<String> r = http.send(req, HttpResponse.BodyHandlers.ofString());
            if (r.statusCode() != 200) log.warn("Telegram respondeu {}", r.statusCode());
        } catch (Exception e) {
            log.warn("Falha no Telegram: {}", e.toString());
        }
    }
}
