package br.com.gammaquant.kalmanordens.http;

import br.com.gammaquant.kalmanordens.dominio.PedidoOrdem;
import br.com.gammaquant.kalmanordens.motor.Motor;
import br.com.gammaquant.kalmanordens.persistencia.Repositorio;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.javalin.Javalin;
import io.javalin.http.Context;
import io.javalin.json.JavalinJackson;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;

/**
 * HTTP local (127.0.0.1:9092). Único cliente: o backend do Kalman, e o CLI por SSH.
 *
 * Javalin em vez de Spring Boot: são cinco rotas, sem injeção nem auto-configuração;
 * menos coisa subindo junto com uma sessão FIX de dinheiro real.
 */
public final class Api {

    private static final Logger log = LoggerFactory.getLogger(Api.class);
    private static final long ESPERA_MS = 5_000;

    private final Motor motor;
    private final ObjectMapper json = new ObjectMapper();
    private Javalin app;

    public Api(Motor motor) {
        this.motor = motor;
    }

    public void iniciar(String host, int porta) {
        app = Javalin.create(c -> {
            c.jsonMapper(new JavalinJackson(json, false));
            c.showJavalinBanner = false;
        });
        app.post("/ordens", this::ordens);
        app.get("/ordens/{operacao_id}", ctx -> responder(ctx, motor.consultar(Long.parseLong(ctx.pathParam("operacao_id")))));
        app.get("/eventos", this::eventos);
        app.post("/parada", ctx -> {
            JsonNode b = json.readTree(ctx.body());
            responder(ctx, motor.parar(b.path("modo").asText(null), b.path("origem").asText(null), b.path("motivo").asText(null)));
        });
        app.post("/retomar", ctx -> {
            JsonNode b = json.readTree(ctx.body());
            responder(ctx, motor.retomar(b.path("origem").asText("?"), b.path("motivo").asText("")));
        });
        app.get("/saude", ctx -> responder(ctx, motor.saude()));
        app.exception(Exception.class, (e, ctx) -> {
            log.warn("Requisição inválida {} {}: {}", ctx.method(), ctx.path(), e.toString());
            ctx.status(400).json(Map.of("aceito", false, "codigo", "REQUISICAO_INVALIDA", "motivo", String.valueOf(e.getMessage())));
        });
        app.start(host, porta);
    }

    public void parar() {
        if (app != null) app.stop();
    }

    private void ordens(Context ctx) throws Exception {
        PedidoOrdem p = json.readValue(ctx.body(), PedidoOrdem.class);
        if (!p.isEntrada() && !p.isSaida()) {
            ctx.status(400).json(Map.of("aceito", false, "codigo", "CAMPO_INVALIDO", "motivo", "papel deve ser ENTRADA ou SAIDA"));
            return;
        }
        responder(ctx, motor.pedir(p));
    }

    private void eventos(Context ctx) throws Exception {
        long desde = Long.parseLong(ctx.queryParamAsClass("desde", String.class).getOrDefault("0"));
        int limite = Math.min(Integer.parseInt(ctx.queryParamAsClass("limite", String.class).getOrDefault("500")), 1000);
        List<Map<String, Object>> r = new ArrayList<>();
        for (Repositorio.Publicado p : motor.eventos(desde, limite)) {
            Map<String, Object> e = new LinkedHashMap<>();
            e.put("seq", p.seq());
            e.put("tipo", p.tipo());
            e.put("operacao_id", p.operacaoId());
            e.put("em", p.em().toString());
            e.put("dados", json.readTree(p.dados()));
            r.add(e);
        }
        ctx.json(r);
    }

    /**
     * Espera o motor. Se estourar o tempo, o resultado é DESCONHECIDO (a ordem pode ter saído):
     * o gestor consulta GET /ordens/{id} ou os eventos antes de decidir.
     */
    private void responder(Context ctx, CompletableFuture<Map<String, Object>> f) throws Exception {
        try {
            ctx.json(f.get(ESPERA_MS, TimeUnit.MILLISECONDS));
        } catch (TimeoutException e) {
            ctx.status(503).json(Map.of("aceito", false, "codigo", "RESULTADO_DESCONHECIDO",
                    "motivo", "motor não respondeu em " + ESPERA_MS + " ms; consultar GET /ordens/{operacao_id}"));
        }
    }
}
