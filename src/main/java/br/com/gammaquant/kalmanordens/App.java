package br.com.gammaquant.kalmanordens;

import br.com.gammaquant.kalmanordens.alerta.Telegram;
import br.com.gammaquant.kalmanordens.fix.SessaoFix;
import br.com.gammaquant.kalmanordens.http.Api;
import br.com.gammaquant.kalmanordens.motor.Motor;
import br.com.gammaquant.kalmanordens.persistencia.Db;
import br.com.gammaquant.kalmanordens.persistencia.Repositorio;
import br.com.gammaquant.kalmanordens.preco.FontePrecoHttp;
import com.zaxxer.hikari.HikariDataSource;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Clock;

/**
 * Ordem de subida (Parte 7.3): banco → estado do dia → HTTP → sessão FIX.
 * Parar o processo não cancela a 1014: a posição continua protegida no PnT.
 */
public final class App {

    private static final Logger log = LoggerFactory.getLogger(App.class);

    public static void main(String[] args) throws Exception {
        Config cfg = Config.doAmbiente();
        Clock relogio = Clock.systemUTC();

        HikariDataSource ds = Db.abrir(cfg.dbUrl(), cfg.dbUsuario(), cfg.dbSenha());
        Db.migrar(ds);
        Repositorio repo = new Repositorio(ds, relogio);

        FontePrecoHttp precos = new FontePrecoHttp(cfg.precosUrl(), cfg.simbolo(), relogio);
        precos.iniciar();

        Motor motor = new Motor(cfg, relogio, repo, precos, new Telegram(cfg.telegramToken(), cfg.telegramChat()));
        SessaoFix fix = new SessaoFix(cfg, motor);
        motor.usar(fix);
        motor.iniciar();

        Api api = new Api(motor);
        api.iniciar(cfg.httpHost(), cfg.httpPorta());

        fix.iniciar();
        log.info("kalman-ordens no ar: HTTP {}:{}, FIX {} → {}:{}, símbolo {}, uid real {}", cfg.httpHost(), cfg.httpPorta(),
                cfg.fixSenderCompId(), cfg.fixHost(), cfg.fixPorta(), cfg.simbolo(), cfg.uidReal().map(String::valueOf).orElse("(nenhum)"));

        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            log.info("Desligando (a 1014 continua no PnT)");
            fix.parar();
            api.parar();
            motor.parar();
            precos.parar();
            ds.close();
        }, "desligamento"));
    }
}
