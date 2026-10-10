package br.com.gammaquant.kalmanordens.dominio;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.time.Instant;
import java.time.LocalDate;
import java.util.ArrayList;
import java.util.EnumMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

/**
 * Operação real de um usuário: entrada, proteção e saída. Confinada à thread do motor.
 */
public final class Operacao {

    public Long id;
    public LocalDate data;
    public long operacaoId;
    public int uid;
    /** Lado da entrada. */
    public Lado lado;
    public int qtdPedida;
    public BigDecimal stopPts;
    public EstadoOperacao estado = EstadoOperacao.ENTRANDO;
    public String motivoEntrada;
    public String tsDecisao;
    public String motivoSaida;
    public String origemSaida;
    public Instant saidaPedidaEm;
    public boolean divergente;
    public BigDecimal pnlPts;
    public Instant criadoEm;

    public final List<Ordem> ordens = new ArrayList<>();
    /** Último n usado no ClOrdID, por papel. */
    public final Map<Papel, Integer> contadorN = new EnumMap<>(Papel.class);

    // ---- temporizadores e controle (voláteis) ----
    public Instant semProtecaoDesde;
    public boolean alertouSemProtecao;
    public Instant cancelProtecaoEnviadoEm;
    public Instant aguardandoStopDesde;
    public Instant proximoRepreco;
    public int repreco;
    public int rejeicoesProtecao;
    public Instant retentativaProtecaoEm;
    public Instant furoDesde;
    public boolean furado;
    /** Saída pedida com a entrada ainda trabalhando. */
    public boolean saidaAposEntrada;
    public Instant proximaSaidaEm;
    public boolean encerrouEntrada;
    /** Fill novo chegou com replace da 1014 em voo: ajustar quando ele responder. */
    public boolean protecaoDesatualizada;

    public int proximoN(Papel p) {
        int n = contadorN.getOrDefault(p, 0) + 1;
        contadorN.put(p, n);
        return n;
    }

    public String clordid(Papel p, int n) {
        return "KM-" + p.name() + "-" + operacaoId + "-" + n;
    }

    /** Posição do Kalman nesta operação (contratos, com sinal). */
    public int posicao() {
        int s = 0;
        for (Ordem o : ordens) s += o.posicao();
        return s;
    }

    public List<Ordem> doPapel(Papel p) {
        return ordens.stream().filter(o -> o.papel == p).toList();
    }

    public Optional<Ordem> viva(Papel p) {
        Ordem r = null;
        for (Ordem o : ordens) if (o.papel == p && o.isViva()) r = o;
        return Optional.ofNullable(r);
    }

    public int qtdEntrada() {
        return doPapel(Papel.E).stream().mapToInt(o -> o.qtdExec).sum();
    }

    public int qtdSaida() {
        return ordens.stream().filter(o -> o.papel != Papel.E).mapToInt(o -> o.qtdExec).sum();
    }

    public BigDecimal precoMedioEntrada() {
        return medio(ordens.stream().filter(o -> o.papel == Papel.E).toList());
    }

    public BigDecimal precoMedioSaida() {
        return medio(ordens.stream().filter(o -> o.papel != Papel.E).toList());
    }

    private static BigDecimal medio(List<Ordem> os) {
        BigDecimal soma = BigDecimal.ZERO;
        int q = 0;
        for (Ordem o : os) {
            if (o.qtdExec > 0 && o.precoMedio != null) {
                soma = soma.add(o.precoMedio.multiply(BigDecimal.valueOf(o.qtdExec)));
                q += o.qtdExec;
            }
        }
        return q == 0 ? null : soma.divide(BigDecimal.valueOf(q), 4, RoundingMode.HALF_EVEN);
    }

    /** PnL realizado em pts × contratos. */
    public BigDecimal pnlRealizado() {
        BigDecimal pe = precoMedioEntrada();
        BigDecimal ps = precoMedioSaida();
        int qs = Math.min(qtdSaida(), qtdEntrada());
        if (pe == null || ps == null || qs == 0) return BigDecimal.ZERO;
        return ps.subtract(pe).multiply(BigDecimal.valueOf((long) qs * lado.sinal));
    }

    /** PnL aberto em pts × contratos, a um preço de referência. */
    public BigDecimal pnlAberto(BigDecimal ultimo) {
        int pos = posicao();
        BigDecimal pe = precoMedioEntrada();
        if (pos == 0 || pe == null || ultimo == null) return BigDecimal.ZERO;
        return ultimo.subtract(pe).multiply(BigDecimal.valueOf(pos));
    }
}
