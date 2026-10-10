package br.com.gammaquant.kalmanordens.persistencia;

import com.zaxxer.hikari.HikariConfig;
import com.zaxxer.hikari.HikariDataSource;
import org.flywaydb.core.Flyway;

import javax.sql.DataSource;

public final class Db {

    private Db() {
    }

    public static HikariDataSource abrir(String url, String usuario, String senha) {
        HikariConfig h = new HikariConfig();
        h.setJdbcUrl(url);
        h.setUsername(usuario);
        h.setPassword(senha);
        h.setMaximumPoolSize(4);
        h.setPoolName("kalman-ordens");
        h.setConnectionTimeout(5_000);
        return new HikariDataSource(h);
    }

    /** Aplica as migrações. As tabelas do serviço ficam no histórico próprio (não mexe no resto do dbecotools). */
    public static void migrar(DataSource ds) {
        Flyway.configure()
                .dataSource(ds)
                .table("kalmanordem_flyway")
                .baselineOnMigrate(true)
                .baselineVersion("0")
                .locations("classpath:db/migration")
                .load()
                .migrate();
    }
}
