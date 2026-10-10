package br.com.gammaquant.kalmanordens.motor;

import br.com.gammaquant.kalmanordens.Config;
import br.com.gammaquant.kalmanordens.alerta.Alertas;
import br.com.gammaquant.kalmanordens.alerta.Notificador;
import br.com.gammaquant.kalmanordens.dominio.Cotacao;
import br.com.gammaquant.kalmanordens.dominio.EstadoOperacao;
import br.com.gammaquant.kalmanordens.dominio.EstadoOrdem;
import br.com.gammaquant.kalmanordens.dominio.Lado;
import br.com.gammaquant.kalmanordens.dominio.Operacao;
import br.com.gammaquant.kalmanordens.dominio.Ordem;
import br.com.gammaquant.kalmanordens.dominio.Papel;
import br.com.gammaquant.kalmanordens.dominio.PedidoOrdem;
import br.com.gammaquant.kalmanordens.dominio.Pendencia;
import br.com.gammaquant.kalmanordens.dominio.Recusa;
import br.com.gammaquant.kalmanordens.dominio.Tick;
import br.com.gammaquant.kalmanordens.fix.FixGateway;
import br.com.gammaquant.kalmanordens.fix.Mensagens;
import br.com.gammaquant.kalmanordens.fix.OuvinteFix;
import br.com.gammaquant.kalmanordens.persistencia.Repositorio;
import br.com.gammaquant.kalmanordens.preco.FontePreco;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import quickfix.FieldNotFound;
import quickfix.Message;
import quickfix.field.MsgSeqNum;
import quickfix.field.MsgType;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.time.Clock;
import java.time.DayOfWeek;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Deque;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * Núcleo do serviço. Todo o estado vive numa única thread ("motor"): pedidos HTTP,
 * mensagens FIX recebidas e o tick periódico entram na mesma fila e são processados
 * em ordem (Parte 7.1). Nada aqui roda na thread do QuickFIX/J.
 *
 * Regra de escopo: o motor não decide quando operar. Executa, protege, limita e registra.
 */
public final class Motor implements OuvinteFix {

    private static final Logger log = LoggerFactory.getLogger(Motor.class);
    private static final String CHAVE_PARADA = "parada";
    private static final String CHAVE_ZERAGEM = "zeragem_feita";
    private static final int[] ESPERA_MASS_STATUS_S = {1, 2, 5, 10, 10, 30};

    private final Config cfg;
    private final Clock relogio;
    private final Repositorio repo;
    private final FontePreco precos;
    private final Mensagens msgs;
    private final Tick tick;
    private final ObjectMapper json = new ObjectMapper();
    private final ScheduledExecutorService exec;
    private final Alertas alertas;
    private FixGateway fix;

    // ---- estado do dia ----
    private LocalDate dia;
    private final Map<Long, Operacao> operacoes = new LinkedHashMap<>();
    private final Map<String, Ordem> porClordid = new HashMap<>();
    private final Map<String, Ordem> por37 = new HashMap<>();
    private final Map<Ordem, Operacao> donoDa = new HashMap<>();
    /** MsgSeqNum enviado → ClOrdID, para ligar 35=3/35=j pelo 45. */
    private final Map<Integer, String> clordidPorSeq = new HashMap<>();
    private Parada parada;
    private boolean divergente;

    // ---- contadores dos limites ----
    private Instant ultimaEntrada;
    private final Deque<Instant> entradasRecentes = new ArrayDeque<>();
    private final Deque<Instant> enviosRecentes = new ArrayDeque<>();
    private final Deque<Instant> recusasPorLimite = new ArrayDeque<>();
    private int entradasHoje;
    private boolean zeragemFeita;
    private LocalDate avisoVencimentoEm;

    // ---- sessão e conciliação ----
    private boolean jaLogou;
    private boolean conciliando;
    private String massReqId;
    private Instant massUltimaResposta;
    private int massTentativas;
    private Instant massProximaTentativa;
    private final List<Message> massRespostas = new ArrayList<>();

    public Motor(Config cfg, Clock relogio, Repositorio repo, FontePreco precos, Notificador notificador) {
        this.cfg = cfg;
        this.relogio = relogio;
        this.repo = repo;
        this.precos = precos;
        this.msgs = new Mensagens(cfg, relogio);
        this.tick = new Tick(cfg.tick());
        this.exec = Executors.newSingleThreadScheduledExecutor(r -> new Thread(r, "motor"));
        this.alertas = new Alertas(notificador, t -> publicar("ALERTA", null, mapa("texto", t)), relogio,
                cfg.repeticaoAlertaCritico());
    }

    public void usar(FixGateway fix) {
        this.fix = fix;
    }

    // =====================================================================
    // Ciclo de vida
    // =====================================================================

    /** Parte 7.3: carrega o estado do dia antes de logar. Bloqueia até terminar. */
    public void iniciar() throws Exception {
        rodar(() -> {
            carregarDia();
            return null;
        }).get(30, TimeUnit.SECONDS);
        long ms = cfg.intervaloTick().toMillis();
        exec.scheduleWithFixedDelay(() -> protegido("tick", this::tick), ms, ms, TimeUnit.MILLISECONDS);
    }

    public void parar() {
        exec.shutdown();
        try {
            exec.awaitTermination(5, TimeUnit.SECONDS);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private void carregarDia() {
        dia = hoje();
        operacoes.clear();
        porClordid.clear();
        por37.clear();
        donoDa.clear();
        for (Operacao op : repo.carregarDia(dia)) {
            operacoes.put(op.operacaoId, op);
            for (Ordem o : op.ordens) indexar(op, o);
            if (op.estado == EstadoOperacao.POSICIONADA_SEM_PROTECAO) op.semProtecaoDesde = agora();
            boolean entradaFinal = op.doPapel(Papel.E).stream().noneMatch(Ordem::isViva);
            op.encerrouEntrada = entradaFinal && op.estado != EstadoOperacao.ENTRANDO;
        }
        parada = repo.lerEstado(CHAVE_PARADA).map(this::lerParada).orElse(null);
        zeragemFeita = repo.lerEstado(CHAVE_ZERAGEM).map(dia.toString()::equals).orElse(false);
        // Contadores dos limites reconstruídos das tabelas, não zerados.
        entradasHoje = operacoes.size();
        ultimaEntrada = operacoes.values().stream().map(o -> o.criadoEm).filter(x -> x != null).max(Instant::compareTo).orElse(null);
        entradasRecentes.clear();
        Instant umMinuto = agora().minusSeconds(60);
        operacoes.values().stream().map(o -> o.criadoEm).filter(x -> x != null && x.isAfter(umMinuto)).sorted().forEach(entradasRecentes::add);
        enviosRecentes.clear();
        enviosRecentes.addAll(repo.enviosDesde(umMinuto));
        divergente = operacoes.values().stream().anyMatch(o -> o.divergente && !o.estado.isFinal());
        log.info("Dia {} carregado: {} operações, posição {}, parada {}", dia, operacoes.size(), posicaoTotal(), parada);
    }

    private Parada lerParada(String s) {
        try {
            return s.isBlank() ? null : json.readValue(s, Parada.class);
        } catch (Exception e) {
            log.error("Estado de parada ilegível, assumindo BLOQUEAR: {}", s);
            return new Parada(Parada.BLOQUEAR, "auto", "estado ilegível", agora().toString());
        }
    }

    // =====================================================================
    // Entrada pública (qualquer thread)
    // =====================================================================

    public CompletableFuture<Map<String, Object>> pedir(PedidoOrdem p) {
        return rodar(() -> {
            try {
                return p.isEntrada() ? pedirEntrada(p) : pedirSaida(p);
            } catch (Recusa r) {
                return recusar(p, r);
            }
        });
    }

    public CompletableFuture<Map<String, Object>> parar(String modo, String origem, String motivo) {
        return rodar(() -> {
            if (!Parada.BLOQUEAR.equals(modo) && !Parada.ZERAR.equals(modo)) {
                return mapa("aceito", false, "codigo", "MODO_INVALIDO", "motivo", "modo deve ser BLOQUEAR ou ZERAR");
            }
            ativarParada(modo, origem == null ? "?" : origem, motivo == null ? "" : motivo);
            return mapa("aceito", true, "parada", parada);
        });
    }

    public CompletableFuture<Map<String, Object>> retomar(String origem, String motivo) {
        return rodar(() -> {
            Parada antes = parada;
            parada = null;
            divergente = false;
            repo.gravarEstado(CHAVE_PARADA, "");
            alertas.alerta("RETOMADO por " + origem + ": " + motivo + " (estava: " + antes + ")");
            return mapa("aceito", true);
        });
    }

    public CompletableFuture<Map<String, Object>> saude() {
        return rodar(this::montarSaude);
    }

    public CompletableFuture<Map<String, Object>> consultar(long operacaoId) {
        return rodar(() -> {
            Operacao op = operacoes.get(operacaoId);
            return op == null ? mapa("existe", false) : descrever(op);
        });
    }

    public List<Repositorio.Publicado> eventos(long desde, int limite) {
        return repo.publicadosDesde(desde, limite);
    }

    // ---- OuvinteFix: só enfileira ----

    @Override
    public void aoLogar() {
        exec.execute(() -> protegido("logon", this::aoLogarNoMotor));
    }

    @Override
    public void aoCair() {
        exec.execute(() -> protegido("logout", () -> {
            conciliando = false;
            alertas.alerta("Sessão FIX caiu");
        }));
    }

    @Override
    public void aoReceber(Message m) {
        exec.execute(() -> protegido("recebida", () -> processar(m)));
    }

    // =====================================================================
    // Pedido de ENTRADA (Partes 2.1, 3.3 e 5)
    // =====================================================================

    private Map<String, Object> pedirEntrada(PedidoOrdem p) {
        verificarUid(p);
        if (p.operacaoId() == null) throw Recusa.simples("CAMPO_INVALIDO", "operacao_id ausente");
        Lado lado = lado(p.lado());
        if (p.qtd() == null || p.qtd() <= 0) throw Recusa.simples("CAMPO_INVALIDO", "qtd inválida");
        if (p.stopPts() == null || p.stopPts().signum() <= 0) throw Recusa.simples("SEM_STOP", "stop_pts ausente na ENTRADA: sem stop, não há ordem");

        Operacao existente = operacoes.get(p.operacaoId());
        if (existente != null || repo.operacaoExiste(p.operacaoId())) {
            boolean identico = existente != null && existente.lado == lado && existente.qtdPedida == p.qtd()
                    && existente.stopPts.compareTo(p.stopPts()) == 0;
            if (!identico) alertas.alerta("ENTRADA duplicada com conteúdo diferente: operacao " + p.operacaoId());
            throw Recusa.simples("DUPLICADA", "operação já recebida");
        }
        if (parada != null) throw Recusa.simples("PARADA", "serviço em parada " + parada.modo() + ": " + parada.motivo());
        if (divergente) throw Recusa.simples("DIVERGENTE", "estado divergente; requer retomada manual");
        if (fix == null || !fix.logado()) throw Recusa.simples("SEM_SESSAO", "sessão FIX não logada");
        if (conciliando) throw Recusa.simples("CONCILIANDO", "conciliação do logon em andamento");

        verificarLimitesEntrada(lado, p.qtd());
        Cotacao c = cotacaoParaEntrada();

        Operacao op = new Operacao();
        op.data = dia;
        op.operacaoId = p.operacaoId();
        op.uid = p.uid();
        op.lado = lado;
        op.qtdPedida = p.qtd();
        op.stopPts = p.stopPts();
        op.motivoEntrada = p.motivo();
        op.tsDecisao = p.tsDecisao();
        op.criadoEm = agora();
        op.estado = EstadoOperacao.ENTRANDO;
        repo.inserirOperacao(op);
        operacoes.put(op.operacaoId, op);

        BigDecimal ref = lado == Lado.C ? c.ask().add(cfg.colarEntrada()) : c.bid().subtract(cfg.colarEntrada());
        Ordem o = novaOrdem(op, Papel.E, lado, p.qtd(), Mensagens.ESTRATEGIA_AGENDADA);
        o.preco = tick.agressivo(lado, ref);
        o.motivoGestor = p.motivo();
        repo.inserirOrdem(o);
        indexar(op, o);

        ultimaEntrada = agora();
        entradasRecentes.add(agora());
        entradasHoje++;

        if (!enviar(o, msgs.nova(o), o.clordidAtual)) {
            o.estado = EstadoOrdem.FALHA_ENVIO;
            repo.atualizarOrdem(o);
            op.estado = EstadoOperacao.ENCERRADA_SEM_ENTRADA;
            op.encerrouEntrada = true;
            repo.atualizarOperacao(op);
            throw Recusa.simples("ENVIO_FALHOU", "a ordem não saiu para o PnT");
        }
        o.enviadaEm = agora();
        log.info("ENTRADA enviada {} preço {} (bid {} ask {})", o, o.preco, c.bid(), c.ask());
        return mapa("aceito", true, "operacao_id", op.operacaoId, "clordid", o.clordidAtual, "preco", o.preco);
    }

    private void verificarLimitesEntrada(Lado lado, int qtd) {
        Instant agora = agora();
        LocalDateTime local = LocalDateTime.now(relogio.withZone(Repositorio.FUSO));

        Optional<LocalDate> venc = cfg.vencimento();
        if (venc.isPresent() && !local.toLocalDate().isBefore(venc.get())) {
            throw Recusa.simples("VENCIMENTO", "dia do vencimento de " + cfg.simbolo() + ": trocar o símbolo");
        }
        LocalTime h = local.toLocalTime();
        if (h.isBefore(cfg.janelaInicio()) || !h.isBefore(cfg.janelaFim())) {
            throw Recusa.limite("FORA_JANELA", "fora da janela de entradas " + cfg.janelaInicio() + "–" + cfg.janelaFim());
        }
        if (qtd > cfg.qtdMaximaOrdem()) throw Recusa.limite("QTD_MAXIMA", "qtd " + qtd + " > " + cfg.qtdMaximaOrdem());
        int comprometido = Math.abs(posicaoTotal());
        for (Operacao op : operacoes.values()) {
            for (Ordem o : op.doPapel(Papel.E)) if (o.isViva()) comprometido += o.restante();
        }
        if (comprometido + qtd > cfg.posicaoMaxima()) {
            throw Recusa.limite("POSICAO_MAXIMA", "posição + entradas pendentes = " + comprometido + ", máximo " + cfg.posicaoMaxima());
        }
        if (ultimaEntrada != null && Duration.between(ultimaEntrada, agora).compareTo(cfg.intervaloMinimoEntradas()) < 0) {
            throw Recusa.limite("INTERVALO_MINIMO", "última entrada há " + Duration.between(ultimaEntrada, agora).toSeconds() + " s");
        }
        aparar(entradasRecentes, agora.minusSeconds(60));
        if (entradasRecentes.size() >= cfg.entradasPorMinuto()) throw Recusa.limite("ENTRADAS_POR_MINUTO", "limite de entradas por minuto");
        aparar(enviosRecentes, agora.minusSeconds(60));
        if (enviosRecentes.size() >= cfg.mensagensPorMinuto()) {
            alertas.alerta("Limite de mensagens por minuto atingido (" + enviosRecentes.size() + ")");
            throw Recusa.limite("ORDENS_POR_MINUTO", "limite de mensagens por minuto");
        }
        if (entradasHoje >= cfg.entradasPorDia()) {
            alertas.alerta("Limite de entradas por dia atingido (" + entradasHoje + ")");
            throw Recusa.limite("ENTRADAS_POR_DIA", "limite de entradas por dia");
        }
        Optional<Cotacao> c = cotacaoFresca();
        if (c.isEmpty()) throw Recusa.limite("PRECO_VELHO", "sem preço confiável (idade > " + cfg.idadeMaximaPreco().toMillis() + " ms)");
        BigDecimal perda = resultadoDoDia(c.get().ultimo());
        if (perda.compareTo(cfg.perdaDiariaPts().negate()) <= 0) {
            ativarParada(Parada.BLOQUEAR, "auto", "perda do dia " + perda + " pts");
            throw Recusa.limite("PERDA_DIARIA", "perda do dia " + perda + " pts");
        }
    }

    private Cotacao cotacaoParaEntrada() {
        Cotacao c = cotacaoFresca().orElseThrow(() -> Recusa.limite("PRECO_VELHO", "sem preço confiável"));
        if (c.status84() == null || !c.status84().equals(cfg.status84Negociacao())) {
            throw Recusa.limite("LEILAO", "status84 = " + c.status84());
        }
        if (c.meio().subtract(c.ultimo()).abs().compareTo(cfg.bookDescoladoPts()) > 0) {
            throw Recusa.limite("BOOK_DESCOLADO", "|meio − último| = " + c.meio().subtract(c.ultimo()).abs());
        }
        return c;
    }

    private Optional<Cotacao> cotacaoFresca() {
        return precos.atual().filter(Cotacao::completa).filter(c -> c.idade().compareTo(cfg.idadeMaximaPreco()) <= 0);
    }

    private Map<String, Object> recusar(PedidoOrdem p, Recusa r) {
        log.info("Pedido recusado {} {}: {} {}", p.papel(), p.operacaoId(), r.codigo, r.getMessage());
        if (r.porLimite) {
            Instant agora = agora();
            recusasPorLimite.add(agora);
            aparar(recusasPorLimite, agora.minusSeconds(60));
            if (recusasPorLimite.size() >= cfg.recusasParaParada() && parada == null) {
                ativarParada(Parada.BLOQUEAR, "auto", recusasPorLimite.size() + " recusas por limite em 1 min (último: " + r.codigo + ")");
            }
        }
        return mapa("aceito", false, "codigo", r.codigo, "motivo", r.getMessage());
    }

    // =====================================================================
    // Pedido de SAÍDA (Parte 3.5)
    // =====================================================================

    private Map<String, Object> pedirSaida(PedidoOrdem p) {
        verificarUid(p);
        if (p.operacaoId() == null) throw Recusa.simples("CAMPO_INVALIDO", "operacao_id ausente");
        Operacao op = operacoes.get(p.operacaoId());
        if (op == null || op.estado.isFinal()) {
            boolean jaZeradaPeloStop = op != null && "STOP_SERVIDOR".equals(op.origemSaida);
            if (!jaZeradaPeloStop) alertas.alerta("SAIDA sem posição: operação " + p.operacaoId());
            throw Recusa.simples("SEM_POSICAO", "operação sem posição real");
        }
        if (op.saidaPedidaEm != null) {
            if ("GESTOR".equals(op.origemSaida)) throw Recusa.simples("DUPLICADA", "saída já pedida");
            return mapa("aceito", true, "operacao_id", op.operacaoId, "ja_em_saida", true, "origem", op.origemSaida);
        }
        int pos = Math.abs(op.posicao());
        boolean entradaViva = op.viva(Papel.E).isPresent();
        if (pos == 0 && !entradaViva) {
            alertas.alerta("SAIDA sem posição: operação " + p.operacaoId());
            throw Recusa.simples("SEM_POSICAO", "posição real zero");
        }
        if (p.qtd() != null && p.qtd() < pos) {
            // v1: só existe saída total (posição máxima 1). Reduce-only continua valendo.
            alertas.alerta("SAIDA parcial pedida (" + p.qtd() + " de " + pos + ") tratada como total na v1: operação " + op.operacaoId);
        }
        iniciarSaida(op, p.motivo(), "GESTOR");
        return mapa("aceito", true, "operacao_id", op.operacaoId, "posicao", op.posicao());
    }

    /** Começa a saída da Parte 3.5. Idempotente. */
    private void iniciarSaida(Operacao op, String motivo, String origem) {
        if (op.estado.isFinal() || op.estado.isSaindo() || op.saidaAposEntrada) return;
        op.motivoSaida = motivo;
        op.origemSaida = origem;
        op.saidaPedidaEm = agora();
        op.viva(Papel.E).ifPresent(this::cancelar);
        if (op.posicao() == 0 && op.viva(Papel.E).isPresent()) {
            op.saidaAposEntrada = true;
            repo.atualizarOperacao(op);
            log.info("Saída pedida com entrada viva e sem fill: cancelando a entrada da operação {}", op.operacaoId);
            return;
        }
        prosseguirSaida(op);
    }

    /** Passo 2 em diante: cancela a 1014 (se houver) ou vai direto para a saída. */
    private void prosseguirSaida(Operacao op) {
        Optional<Ordem> p = op.viva(Papel.P);
        if (p.isPresent()) {
            mudar(op, EstadoOperacao.CANCELANDO_PROTECAO);
            op.cancelProtecaoEnviadoEm = agora();
            cancelar(p.get());
        } else {
            mudar(op, EstadoOperacao.SAINDO);
            enviarSaida(op);
        }
    }

    private void enviarSaida(Operacao op) {
        int pos = op.posicao();
        if (pos == 0) {
            zerada(op);
            return;
        }
        Lado lado = Lado.paraZerar(pos);
        BigDecimal preco = precoSaida(lado, 1);
        if (preco == null) {
            alertas.critico("SEM_PRECO_SAIDA:" + op.operacaoId, "sem preço para a saída da operação " + op.operacaoId);
            op.proximaSaidaEm = agora().plus(cfg.timeoutSaida());
            return;
        }
        alertas.resolver("SEM_PRECO_SAIDA:" + op.operacaoId);
        Ordem o = novaOrdem(op, Papel.S, lado, Math.abs(pos), Mensagens.ESTRATEGIA_AGENDADA);
        o.preco = preco;
        o.motivoGestor = op.motivoSaida;
        repo.inserirOrdem(o);
        indexar(op, o);
        op.repreco = 0;
        op.proximoRepreco = agora().plus(cfg.timeoutSaida());
        if (!enviar(o, msgs.nova(o), o.clordidAtual)) {
            o.estado = EstadoOrdem.FALHA_ENVIO;
            repo.atualizarOrdem(o);
            alertas.critico("SAIDA_NAO_SAIU:" + op.operacaoId, "a saída da operação " + op.operacaoId + " não saiu para o PnT; tentando de novo");
            op.proximaSaidaEm = agora().plus(cfg.timeoutSaida());
            return;
        }
        alertas.resolver("SAIDA_NAO_SAIU:" + op.operacaoId);
        o.enviadaEm = agora();
        log.info("SAIDA enviada {} preço {}", o, preco);
    }

    /** bid/ask ∓ COLAR_SAIDA × k. Sem bid/ask, usa o último. Preço velho é aceito: saída não espera. */
    private BigDecimal precoSaida(Lado lado, int k) {
        Optional<Cotacao> oc = precos.atual();
        if (oc.isEmpty()) return null;
        Cotacao c = oc.get();
        BigDecimal ref = lado == Lado.V ? c.bid() : c.ask();
        if (ref == null) ref = c.ultimo();
        if (ref == null) return null;
        if (c.idade() == null || c.idade().compareTo(cfg.idadeMaximaPreco()) > 0) {
            alertas.alerta("Saída com preço velho (" + (c.idade() == null ? "?" : c.idade().toMillis()) + " ms)");
        }
        BigDecimal colar = cfg.colarSaida().multiply(BigDecimal.valueOf(k));
        return tick.agressivo(lado, lado == Lado.V ? ref.subtract(colar) : ref.add(colar));
    }

    private void zerada(Operacao op) {
        if (op.estado.isFinal()) return;
        for (Ordem o : op.ordens) if (o.isViva()) cancelar(o);
        op.pnlPts = op.pnlRealizado();
        mudar(op, EstadoOperacao.ZERADA);
        for (String k : List.of("SEM_PROTECAO:", "STOP_FURADO:", "SAIDA_ESGOTOU:", "CANCEL_SEM_RESPOSTA:", "SEM_PRECO_SAIDA:", "SAIDA_NAO_SAIU:")) {
            alertas.resolver(k + op.operacaoId);
        }
        publicar("ZERADA", op, mapa("preco_medio_saida", op.precoMedioSaida(), "pnl_pts", op.pnlPts, "origem", op.origemSaida,
                "motivo", op.motivoSaida));
        log.info("Operação {} ZERADA, pnl {} pts", op.operacaoId, op.pnlPts);
    }

    // =====================================================================
    // Proteção 1014 (Parte 3.4)
    // =====================================================================

    private void ajustarProtecao(Operacao op) {
        if (op.estado.isSaindo() || op.estado.isFinal() || op.saidaAposEntrada) return;
        int pos = op.posicao();
        if (pos == 0) return;
        if (op.retentativaProtecaoEm != null && agora().isBefore(op.retentativaProtecaoEm)) return;
        op.retentativaProtecaoEm = null;
        Lado lado = Lado.paraZerar(pos);
        int qtd = Math.abs(pos);
        BigDecimal medio = op.precoMedioEntrada();
        BigDecimal stop;
        BigDecimal lim;
        if (lado == Lado.V) {
            // posição comprada: stop de venda abaixo; gatilho arredonda para cima (mais perto), limite para baixo (mais folga)
            stop = tick.acima(medio.subtract(op.stopPts));
            lim = tick.abaixo(stop.subtract(cfg.folgaStop()));
        } else {
            stop = tick.abaixo(medio.add(op.stopPts));
            lim = tick.acima(stop.add(cfg.folgaStop()));
        }
        Optional<Ordem> viva = op.viva(Papel.P);
        if (viva.isEmpty()) {
            if (op.semProtecaoDesde == null) op.semProtecaoDesde = agora();
            Ordem o = novaOrdem(op, Papel.P, lado, qtd, Mensagens.ESTRATEGIA_STOP);
            o.precoStop = stop;
            o.precoStopLim = lim;
            o.preco = lim;
            repo.inserirOrdem(o);
            indexar(op, o);
            if (!enviar(o, msgs.nova(o), o.clordidAtual)) {
                o.estado = EstadoOrdem.FALHA_ENVIO;
                repo.atualizarOrdem(o);
                op.retentativaProtecaoEm = agora().plus(cfg.esperaRetentativaProtecao());
                log.error("1014 não saiu para o PnT: {}", o);
                return;
            }
            o.enviadaEm = agora();
            log.info("PROTEÇÃO enviada {} gatilho {} limite {}", o, stop, lim);
            return;
        }
        Ordem p = viva.get();
        if (p.pendencia != Pendencia.NENHUMA || p.estado == EstadoOrdem.ENVIANDO || p.estado == EstadoOrdem.PENDENTE) {
            // Um pedido por vez: fica para quando o atual responder, só com o estado final.
            op.protecaoDesatualizada = true;
            return;
        }
        op.protecaoDesatualizada = false;
        if (p.qtd != qtd || p.precoStop == null || p.precoStop.compareTo(stop) != 0 || p.lado != lado) {
            replace(p, qtd, lim, stop, lim);
        }
    }

    private void protecaoRejeitada(Operacao op, String texto) {
        op.rejeicoesProtecao++;
        if (op.rejeicoesProtecao < 2) {
            alertas.alerta("1014 rejeitada (" + texto + "), nova tentativa em " + cfg.esperaRetentativaProtecao().toMillis() + " ms: operação " + op.operacaoId);
            op.retentativaProtecaoEm = agora().plus(cfg.esperaRetentativaProtecao());
        } else {
            alertas.alerta("1014 rejeitada de novo (" + texto + "): zerando a operação " + op.operacaoId);
            iniciarSaida(op, "PROTECAO_REJEITADA", "SERVICO");
        }
    }

    // =====================================================================
    // Envio de cancel / replace (um pedido por vez)
    // =====================================================================

    private void cancelar(Ordem o) {
        if (!o.isViva() || o.pendencia == Pendencia.CANCEL) return;
        if (o.pendencia == Pendencia.REPLACE) {
            o.cancelarAposPendencia = true;
            return;
        }
        Operacao op = donoDa.get(o);
        String cl = op.clordid(o.papel, op.proximoN(o.papel));
        o.cancelamentos++;
        o.ultimoCancelEm = agora();
        o.pendencia = Pendencia.CANCEL;
        o.clordidPendente = cl;
        o.pendenciaDesde = agora();
        porClordid.put(cl, o);
        if (!enviar(o, msgs.cancel(o, cl), cl)) {
            o.pendencia = Pendencia.NENHUMA;
            o.clordidPendente = null;
            alertas.alerta("Cancelamento não saiu para o PnT: " + o);
        } else {
            log.info("CANCEL enviado {} → {}", o, cl);
        }
    }

    private void replace(Ordem o, int qtd, BigDecimal preco, BigDecimal stop, BigDecimal stopLim) {
        if (!o.isViva() || o.pendencia != Pendencia.NENHUMA) return;
        Operacao op = donoDa.get(o);
        String cl = op.clordid(o.papel, op.proximoN(o.papel));
        o.pendencia = Pendencia.REPLACE;
        o.clordidPendente = cl;
        o.pendenciaDesde = agora();
        o.qtdPendente = qtd;
        o.precoPendente = preco;
        o.precoStopPendente = stop;
        o.precoStopLimPendente = stopLim;
        porClordid.put(cl, o);
        if (!enviar(o, msgs.replace(o, cl, qtd, preco, stop, stopLim), cl)) {
            limparPendencia(o);
            alertas.alerta("Replace não saiu para o PnT: " + o);
        } else {
            log.info("REPLACE enviado {} → {} qtd {} preço {} stop {}", o, cl, qtd, preco, stop);
        }
    }

    private static void limparPendencia(Ordem o) {
        o.pendencia = Pendencia.NENHUMA;
        o.clordidPendente = null;
        o.pendenciaDesde = null;
        o.qtdPendente = null;
        o.precoPendente = null;
        o.precoStopPendente = null;
        o.precoStopLimPendente = null;
    }

    private boolean enviar(Ordem o, Message m, String clordid) {
        boolean ok = fix != null && fix.enviar(m);
        String mt = campo(m.getHeader(), MsgType.FIELD);
        repo.inserirEvento(new Repositorio.RegistroEvento(o.id, "ENV", mt, null, null, clordid, o.orderId37, null,
                null, null, null, null, ok ? null : "NAO_ENVIADA", null, m.toString()));
        if (ok) {
            enviosRecentes.add(agora());
            try {
                clordidPorSeq.put(m.getHeader().getInt(MsgSeqNum.FIELD), clordid);
            } catch (FieldNotFound e) {
                // sem seq: não dá para ligar um 35=3 a esta mensagem
            }
        }
        return ok;
    }

    // =====================================================================
    // Mensagens recebidas (Parte 7.1)
    // =====================================================================

    private void processar(Message m) {
        String mt = campo(m.getHeader(), MsgType.FIELD);
        switch (mt) {
            case "8" -> execReport(m);
            case "9" -> cancelRejeitado(m);
            case "j" -> businessReject(m);
            case "3" -> rejeicaoSessao(m);
            default -> log.debug("Ignorada {}", mt);
        }
    }

    private void execReport(Message m) {
        String cl = campo(m, 11);
        String orig = campo(m, 41);
        String id37 = campo(m, 37);
        String execType = campo(m, 150);
        String massId = campo(m, 584);

        if ("I".equals(execType) && conciliando && massReqId != null && massReqId.equals(massId)) {
            massUltimaResposta = agora();
            if (ehNosso(cl) || ehNosso(orig)) massRespostas.add(m);
            else massRespostas.add(null); // ordem do PT na mesma conta: contada e ignorada
            return;
        }
        if (!ehNosso(cl) && !ehNosso(orig) && (id37 == null || !por37.containsKey(id37))) {
            log.debug("ER de ordem que não é do Kalman ignorado: 11={}", cl);
            return;
        }
        Ordem o = localizar(id37, cl, orig);
        if (o == null) {
            gravarRecebida(null, m);
            alertas.alerta("ER de ordem KM desconhecida: 11=" + cl + " 37=" + id37);
            return;
        }
        if (!gravarRecebida(o, m)) {
            log.info("ExecID repetido, ignorado: {} {}", campo(m, 17), o);
            return;
        }
        aplicarExecReport(donoDa.get(o), o, m);
    }

    /** Aplica um 35=8 (inclusive 150=I fora da conciliação). */
    private void aplicarExecReport(Operacao op, Ordem o, Message m) {
        String execType = campo(m, 150);
        String status = campo(m, 39);
        String id37 = campo(m, 37);
        if (id37 != null && !"NONE".equals(id37) && o.orderId37 == null) {
            o.orderId37 = id37;
            por37.put(id37, o);
        }
        String s198 = campo(m, 198);
        if (s198 != null) o.secId198 = s198;

        boolean estavaEncerrada = o.estado.isFinal() && o.estado != EstadoOrdem.FALHA_ENVIO;
        Integer cum = inteiro(m, 14);
        Integer lastQty = inteiro(m, 32);
        if (cum == null && "F".equals(execType) && lastQty != null) cum = o.qtdExec + lastQty;
        if (cum != null && cum > o.qtdExec) {
            aplicarFill(op, o, cum, decimal(m, 6), lastQty, decimal(m, 31), estavaEncerrada);
        }

        char et = execType == null ? '?' : execType.charAt(0);
        switch (et) {
            case 'A' -> {
                if (o.estado == EstadoOrdem.ENVIANDO) {
                    o.estado = EstadoOrdem.PENDENTE;
                    o.pendenteNovaDesde = agora();
                }
            }
            case '0' -> {
                if (!o.estado.isFinal()) o.estado = o.qtdExec > 0 ? EstadoOrdem.PARCIAL : EstadoOrdem.ATIVA;
                if (o.papel == Papel.P) protecaoAceita(op, o);
            }
            case '5', 'D' -> replaceConfirmado(op, o, campo(m, 11));
            case '4' -> {
                limparPendencia(o);
                o.estado = EstadoOrdem.CANCELADA;
                if (!estavaEncerrada) ordemEncerrada(op, o, "CANCELADA");
            }
            case 'C' -> {
                limparPendencia(o);
                o.estado = EstadoOrdem.EXPIRADA;
                if (!estavaEncerrada) ordemEncerrada(op, o, "EXPIRADA");
            }
            case '8' -> {
                String texto = campo(m, 58);
                String cod = campo(m, 103);
                limparPendencia(o);
                o.estado = EstadoOrdem.REJEITADA;
                o.motivoRejeicao = (cod == null ? "" : "[" + cod + "] ") + (texto == null ? "" : texto);
                if (!estavaEncerrada) ordemRejeitada(op, o, texto, cod);
            }
            case 'I' -> statusDaOrdem(op, o, status, estavaEncerrada);
            default -> {
                // F (trade), E (pending replace), 6 (pending cancel): o fill já foi aplicado acima
                if (status != null && o.estado == EstadoOrdem.PENDENTE && "0".equals(status)) o.estado = EstadoOrdem.ATIVA;
            }
        }
        if ("2".equals(status) && o.qtdExec < o.qtd) {
            // ex.: mass status de HML 2025 traz 39=2 com 14=0. Sem CumQty não há como saber a posição.
            marcarDivergente(op, "39=2 sem CumQty correspondente (" + o.qtdExec + "/" + o.qtd + "): " + o);
        } else if ("2".equals(status) && o.estado != EstadoOrdem.EXECUTADA && !estavaEncerrada) {
            limparPendencia(o);
            o.estado = EstadoOrdem.EXECUTADA;
            ordemEncerrada(op, o, "EXECUTADA");
        } else if ("1".equals(status) && (o.estado == EstadoOrdem.ATIVA || o.estado == EstadoOrdem.PENDENTE || o.estado == EstadoOrdem.ENVIANDO)) {
            o.estado = EstadoOrdem.PARCIAL;
        }
        repo.atualizarOrdem(o);
        repo.atualizarOperacao(op);
    }

    /** 150=I: status vindo do mass status. Concilia o estado. */
    private void statusDaOrdem(Operacao op, Ordem o, String status, boolean estavaEncerrada) {
        EstadoOrdem noPnt = status == null ? null : EstadoOrdem.do39(status.charAt(0));
        if (noPnt == null) return;
        if (estavaEncerrada && !noPnt.isFinal()) {
            alertas.alerta("Conciliação: ordem viva no PnT e encerrada na tabela, atualizando: " + o);
            o.estado = o.qtdExec > 0 ? EstadoOrdem.PARCIAL : EstadoOrdem.ATIVA;
            return;
        }
        if (!estavaEncerrada && noPnt.isFinal() && noPnt != EstadoOrdem.EXECUTADA) {
            alertas.alerta("Conciliação: ordem encerrada no PnT (" + status + ") e viva na tabela: " + o);
            limparPendencia(o);
            o.estado = noPnt;
            ordemEncerrada(op, o, noPnt.name());
            return;
        }
        if (o.estado == EstadoOrdem.DESCONHECIDA || o.estado == EstadoOrdem.ENVIANDO || o.estado == EstadoOrdem.PENDENTE) {
            if (!noPnt.isFinal()) o.estado = noPnt == EstadoOrdem.PENDENTE ? EstadoOrdem.PENDENTE : (o.qtdExec > 0 ? EstadoOrdem.PARCIAL : EstadoOrdem.ATIVA);
        }
    }

    /**
     * A posição é derivada do CumQty (14) mais alto de cada ordem: um fill repetido
     * (reconexão, ResendRequest, mass status) não muda nada.
     */
    private void aplicarFill(Operacao op, Ordem o, int cum, BigDecimal avg, Integer lastQty, BigDecimal lastPx, boolean estavaEncerrada) {
        int antes = o.qtdExec;
        int delta = cum - antes;
        BigDecimal px;
        if (lastQty != null && lastQty == delta && lastPx != null && lastPx.signum() > 0) {
            px = lastPx;
        } else if (avg != null && avg.signum() > 0 && o.precoMedio != null && antes > 0) {
            px = avg.multiply(BigDecimal.valueOf(cum)).subtract(o.precoMedio.multiply(BigDecimal.valueOf(antes)))
                    .divide(BigDecimal.valueOf(delta), 4, RoundingMode.HALF_EVEN);
        } else {
            px = avg != null && avg.signum() > 0 ? avg : lastPx;
        }
        if (avg != null && avg.signum() > 0) {
            o.precoMedio = avg;
        } else if (px != null) {
            BigDecimal soma = (o.precoMedio == null ? BigDecimal.ZERO : o.precoMedio.multiply(BigDecimal.valueOf(antes)))
                    .add(px.multiply(BigDecimal.valueOf(delta)));
            o.precoMedio = soma.divide(BigDecimal.valueOf(cum), 4, RoundingMode.HALF_EVEN);
        }
        o.qtdExec = cum;
        log.info("FILL {} +{} @ {} (cum {})", o, delta, px, cum);
        if (px == null) alertas.alerta("Fill sem preço: " + o);
        if (cum > o.qtd) {
            marcarDivergente(op, "execução acima da quantidade da ordem: " + o);
        }
        if (estavaEncerrada) {
            alertas.alerta("Fill em ordem já encerrada, aceito e contabilizado: " + o + " +" + delta);
        }

        if (o.papel == Papel.E) {
            publicar("ENTRADA_EXECUTADA", op, mapa("qtd", delta, "preco", px, "qtd_total", op.qtdEntrada(),
                    "preco_medio", op.precoMedioEntrada(), "tardio", estavaEncerrada));
            if (op.estado.isFinal()) {
                // posição reaberta depois de encerrada: não fica posição sem dono
                alertas.alerta("Fill de entrada depois da operação encerrada; zerando: operação " + op.operacaoId);
                op.estado = EstadoOperacao.POSICIONADA_SEM_PROTECAO;
                op.saidaPedidaEm = null;
                iniciarSaida(op, "FILL_TARDIO", "SERVICO");
                return;
            }
            if (op.saidaAposEntrada) {
                op.saidaAposEntrada = false;
                prosseguirSaida(op);
                return;
            }
            if (op.estado == EstadoOperacao.ENTRANDO) {
                mudar(op, EstadoOperacao.POSICIONADA_SEM_PROTECAO);
                op.semProtecaoDesde = agora();
            }
            ajustarProtecao(op);
            return;
        }

        // P ou S: saída
        String origem = o.papel == Papel.P ? "STOP_SERVIDOR" : (op.origemSaida == null ? "GESTOR" : op.origemSaida);
        if (o.papel == Papel.P && !op.estado.isSaindo()) {
            op.origemSaida = "STOP_SERVIDOR";
            op.motivoSaida = "STOP_SERVIDOR";
            if (op.saidaPedidaEm == null) op.saidaPedidaEm = agora();
        }
        publicar("SAIDA_EXECUTADA", op, mapa("qtd", delta, "preco", px, "origem", origem));
        int pos = op.posicao();
        if (pos == 0) {
            if (o.papel == Papel.P) op.origemSaida = "STOP_SERVIDOR";
            zerada(op);
        } else if (Integer.signum(pos) != op.lado.sinal) {
            marcarDivergente(op, "posição invertida (" + pos + ") na operação " + op.operacaoId);
            for (Ordem x : op.ordens) if (x.papel != Papel.E && x.isViva()) cancelar(x);
            mudar(op, EstadoOperacao.SAINDO);
            op.proximaSaidaEm = agora().plus(cfg.esperaRetentativaProtecao());
        }
    }

    private void protecaoAceita(Operacao op, Ordem p) {
        op.semProtecaoDesde = null;
        op.alertouSemProtecao = false;
        alertas.resolver("SEM_PROTECAO:" + op.operacaoId);
        if (op.estado == EstadoOperacao.POSICIONADA_SEM_PROTECAO || op.estado == EstadoOperacao.ENTRANDO) {
            mudar(op, EstadoOperacao.PROTEGIDA);
        }
        publicar("PROTEGIDA", op, mapa("preco_stop", p.precoStop, "preco_limite", p.precoStopLim, "qtd", p.qtd));
        if (op.protecaoDesatualizada) ajustarProtecao(op);
    }

    private void replaceConfirmado(Operacao op, Ordem o, String clNovo) {
        if (clNovo != null) o.clordidAtual = clNovo;
        if (o.qtdPendente != null) o.qtd = o.qtdPendente;
        if (o.precoPendente != null) o.preco = o.precoPendente;
        if (o.precoStopPendente != null) o.precoStop = o.precoStopPendente;
        if (o.precoStopLimPendente != null) o.precoStopLim = o.precoStopLimPendente;
        boolean cancelarDepois = o.cancelarAposPendencia;
        o.cancelarAposPendencia = false;
        limparPendencia(o);
        if (o.papel == Papel.P) {
            op.rejeicoesProtecao = 0;
            publicar("PROTEGIDA", op, mapa("preco_stop", o.precoStop, "preco_limite", o.precoStopLim, "qtd", o.qtd));
        }
        if (cancelarDepois) cancelar(o);
        else if (o.papel == Papel.P && op.protecaoDesatualizada) ajustarProtecao(op);
    }

    /** Ordem chegou a um estado final (executada, cancelada, expirada). */
    private void ordemEncerrada(Operacao op, Ordem o, String motivo) {
        if (o.cancelarAposPendencia) o.cancelarAposPendencia = false;
        switch (o.papel) {
            case E -> encerrarEntrada(op, motivo);
            case P -> {
                if (op.estado == EstadoOperacao.CANCELANDO_PROTECAO) {
                    alertas.resolver("CANCEL_SEM_RESPOSTA:" + op.operacaoId);
                    mudar(op, EstadoOperacao.SAINDO);
                    enviarSaida(op);
                } else if (!op.estado.isSaindo() && !op.estado.isFinal() && op.posicao() != 0) {
                    alertas.alerta("1014 encerrada (" + motivo + ") com posição aberta; recriando: operação " + op.operacaoId);
                    mudar(op, EstadoOperacao.POSICIONADA_SEM_PROTECAO);
                    op.semProtecaoDesde = agora();
                    ajustarProtecao(op);
                }
            }
            case S -> {
                if (op.estado == EstadoOperacao.SAINDO && op.posicao() != 0) op.proximaSaidaEm = agora();
            }
        }
    }

    private void ordemRejeitada(Operacao op, Ordem o, String texto, String codigo) {
        publicar("RECUSADA", op, mapa("papel", o.papel.name(), "motivo", texto, "codigo", codigo, "clordid", o.clordidAtual));
        alertas.alerta("Recusa do PnT " + o + ": [" + codigo + "] " + texto);
        switch (o.papel) {
            case E -> encerrarEntrada(op, "REJEITADA");
            case P -> protecaoRejeitada(op, texto);
            case S -> op.proximaSaidaEm = agora().plus(cfg.esperaRetentativaProtecao());
        }
    }

    private void encerrarEntrada(Operacao op, String motivo) {
        if (op.viva(Papel.E).isPresent()) return;
        if (!op.encerrouEntrada) {
            op.encerrouEntrada = true;
            publicar("ENTRADA_ENCERRADA", op, mapa("qtd_final", op.qtdEntrada(), "preco_medio", op.precoMedioEntrada(), "motivo", motivo));
        }
        if (op.qtdEntrada() == 0 && op.posicao() == 0) {
            op.saidaAposEntrada = false;
            mudar(op, EstadoOperacao.ENCERRADA_SEM_ENTRADA);
        } else if (op.saidaAposEntrada) {
            op.saidaAposEntrada = false;
            prosseguirSaida(op);
        }
    }

    private void cancelRejeitado(Message m) {
        Ordem o = localizar(campo(m, 37), campo(m, 11), campo(m, 41));
        if (o == null) {
            if (ehNosso(campo(m, 11)) || ehNosso(campo(m, 41))) alertas.alerta("35=9 de ordem KM desconhecida: 11=" + campo(m, 11));
            return;
        }
        gravarRecebida(o, m);
        Operacao op = donoDa.get(o);
        Pendencia era = o.pendencia;
        boolean cancelarDepois = o.cancelarAposPendencia;
        o.cancelarAposPendencia = false;
        limparPendencia(o);
        String texto = "[" + campo(m, 102) + "] " + campo(m, 58);
        log.warn("35=9 {} (era {}): {}", o, era, texto);
        boolean eraCancel = era == Pendencia.CANCEL || (era == Pendencia.NENHUMA && "1".equals(campo(m, 434)));
        if (eraCancel) {
            if (o.papel == Papel.P && op.estado == EstadoOperacao.CANCELANDO_PROTECAO) {
                alertas.resolver("CANCEL_SEM_RESPOSTA:" + op.operacaoId);
                if (op.furado) {
                    mudar(op, EstadoOperacao.SAINDO);
                    enviarSaida(op);
                } else {
                    // A 1014 provavelmente já disparou: não manda a saída, espera os fills.
                    alertas.alerta("Cancel da 1014 rejeitado (" + texto + "): aguardando fills do stop, operação " + op.operacaoId);
                    mudar(op, EstadoOperacao.AGUARDANDO_STOP);
                    op.aguardandoStopDesde = agora();
                }
            } else if (o.papel == Papel.E) {
                alertas.alerta("Cancel da entrada rejeitado (" + texto + "): " + o);
            }
        } else {
            alertas.alerta("Replace rejeitado (" + texto + "): " + o);
            if (o.papel == Papel.P) protecaoRejeitada(op, texto);
            if (cancelarDepois) cancelar(o);
        }
        repo.atualizarOrdem(o);
        repo.atualizarOperacao(op);
    }

    private void businessReject(Message m) {
        String refMsgType = campo(m, 372);
        String refId = campo(m, 379);
        String razao = campo(m, 380);
        if (MsgType.ORDER_MASS_STATUS_REQUEST.equals(refMsgType) || (massReqId != null && massReqId.equals(refId))) {
            gravarRecebida(null, m);
            if ("4".equals(razao)) {
                int i = Math.min(massTentativas, ESPERA_MASS_STATUS_S.length - 1);
                massProximaTentativa = agora().plusSeconds(ESPERA_MASS_STATUS_S[i]);
                massTentativas++;
                log.info("Mass status: sistema subindo (380=4), nova tentativa em {} s", ESPERA_MASS_STATUS_S[i]);
            } else {
                conciliando = false;
                alertas.critico("CONCILIACAO", "mass status rejeitado (380=" + razao + "): " + campo(m, 58));
                ativarParada(Parada.BLOQUEAR, "auto", "mass status rejeitado");
            }
            return;
        }
        rejeicaoGenerica(m, refId, "35=j [380=" + razao + "] " + campo(m, 58));
    }

    private void rejeicaoSessao(Message m) {
        rejeicaoGenerica(m, null, "35=3 [373=" + campo(m, 373) + " tag " + campo(m, 371) + "] " + campo(m, 58));
    }

    /** 35=3 / 35=j ligados à ordem pelo 45 (RefSeqNum) ou pelo 379. */
    private void rejeicaoGenerica(Message m, String refId, String texto) {
        Integer ref = inteiro(m, 45);
        String cl = refId != null ? refId : (ref == null ? null : clordidPorSeq.get(ref));
        Ordem o = cl == null ? null : porClordid.get(cl);
        gravarRecebida(o, m);
        if (o == null) {
            alertas.alerta("Rejeição sem ordem identificada: " + texto);
            return;
        }
        Operacao op = donoDa.get(o);
        if (o.pendencia != Pendencia.NENHUMA && cl.equals(o.clordidPendente)) {
            boolean eraCancel = o.pendencia == Pendencia.CANCEL;
            limparPendencia(o);
            alertas.alerta("Pedido rejeitado " + texto + ": " + o);
            if (o.papel == Papel.P && !eraCancel) protecaoRejeitada(op, texto);
            if (o.papel == Papel.P && eraCancel && op.estado == EstadoOperacao.CANCELANDO_PROTECAO) op.cancelProtecaoEnviadoEm = null;
        } else if (o.isViva()) {
            o.estado = EstadoOrdem.REJEITADA;
            o.motivoRejeicao = texto;
            ordemRejeitada(op, o, texto, null);
        }
        repo.atualizarOrdem(o);
        repo.atualizarOperacao(op);
    }

    // =====================================================================
    // Logon e conciliação (Parte 7.2)
    // =====================================================================

    private void aoLogarNoMotor() {
        if (jaLogou) alertas.alerta("Sessão FIX voltou");
        jaLogou = true;
        conciliando = true;
        massTentativas = 0;
        massProximaTentativa = null;
        pedirMassStatus();
    }

    private void pedirMassStatus() {
        massReqId = "KM-MS-" + agora().toEpochMilli();
        massRespostas.clear();
        massUltimaResposta = agora();
        massProximaTentativa = null;
        Message m = msgs.massStatus(massReqId);
        boolean ok = fix != null && fix.enviar(m);
        repo.inserirEvento(new Repositorio.RegistroEvento(null, "ENV", "AF", null, null, massReqId, null, null, null, null,
                null, null, ok ? null : "NAO_ENVIADA", null, m.toString()));
        if (!ok) {
            massProximaTentativa = agora().plusSeconds(1);
        }
    }

    private void conciliar() {
        conciliando = false;
        int ignoradas = 0;
        int divergencias = 0;
        Set<Ordem> vistas = new HashSet<>();
        for (Message m : massRespostas) {
            if (m == null) {
                ignoradas++;
                continue;
            }
            Ordem o = localizar(campo(m, 37), campo(m, 11), campo(m, 41));
            if (o == null) {
                divergencias++;
                gravarRecebida(null, m);
                alertas.alerta("Conciliação: ordem KM no PnT sem registro na tabela: 11=" + campo(m, 11) + " 37=" + campo(m, 37));
                continue;
            }
            vistas.add(o);
            if (gravarRecebida(o, m)) aplicarExecReport(donoDa.get(o), o, m);
        }
        for (Operacao op : operacoes.values()) {
            for (Ordem o : op.ordens) {
                if (o.isViva() && !vistas.contains(o)) {
                    // [PnT-13] o mass status pode não trazer ordens encerradas
                    divergencias++;
                    alertas.alerta("Conciliação: ordem viva na tabela e ausente no PnT, marcada DESCONHECIDA: " + o);
                    o.estado = EstadoOrdem.DESCONHECIDA;
                    limparPendencia(o);
                    repo.atualizarOrdem(o);
                }
            }
        }
        // Posição ≠ 0 sem 1014 viva → cria a 1014 imediatamente.
        for (Operacao op : operacoes.values()) {
            if (op.estado.isFinal() || op.estado.isSaindo() || op.posicao() == 0) continue;
            if (op.viva(Papel.P).isEmpty()) {
                alertas.alerta("Conciliação: posição " + op.posicao() + " sem 1014 viva, criando: operação " + op.operacaoId);
                mudar(op, EstadoOperacao.POSICIONADA_SEM_PROTECAO);
                op.semProtecaoDesde = agora();
                ajustarProtecao(op);
            }
            repo.atualizarOperacao(op);
        }
        if (divergencias > 0) {
            divergente = true;
            ativarParada(Parada.BLOQUEAR, "auto", "conciliação com " + divergencias + " divergência(s)");
        }
        alertas.resolver("CONCILIACAO");
        alertas.alerta("Conciliação: " + vistas.size() + " ordem(ns) KM, " + ignoradas + " de outros (ignoradas), " + divergencias
                + " divergência(s); posição " + posicaoTotal());
    }

    // =====================================================================
    // Parada (Parte 6)
    // =====================================================================

    private void ativarParada(String modo, String origem, String motivo) {
        boolean jaZerando = parada != null && Parada.ZERAR.equals(parada.modo());
        parada = new Parada(jaZerando ? Parada.ZERAR : modo, origem, motivo, LocalDateTime.now(relogio.withZone(Repositorio.FUSO)).toString());
        try {
            repo.gravarEstado(CHAVE_PARADA, json.writeValueAsString(parada));
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
        alertas.alerta("PARADA " + parada.modo() + " (" + origem + "): " + motivo);
        for (Operacao op : operacoes.values()) {
            if (op.estado.isFinal()) continue;
            op.viva(Papel.E).ifPresent(this::cancelar);
            if (Parada.ZERAR.equals(modo)) iniciarSaida(op, "PARADA", "PARADA");
        }
    }

    private void marcarDivergente(Operacao op, String motivo) {
        op.divergente = true;
        divergente = true;
        alertas.critico("DIVERGENTE:" + op.operacaoId, "DIVERGENTE: " + motivo);
        if (parada == null) ativarParada(Parada.BLOQUEAR, "auto", "divergência: " + motivo);
    }

    // =====================================================================
    // Tick: tudo o que depende de tempo
    // =====================================================================

    private void tick() {
        if (!hoje().equals(dia)) virarDia();
        alertas.tick();
        Instant agora = agora();
        boolean podeEnviar = fix != null && fix.logado() && !conciliando;

        if (conciliando && massProximaTentativa != null && !agora.isBefore(massProximaTentativa) && fix != null && fix.logado()) {
            pedirMassStatus();
        } else if (conciliando && massProximaTentativa == null && massUltimaResposta != null
                && Duration.between(massUltimaResposta, agora).compareTo(cfg.silencioMassStatus()) >= 0) {
            conciliar();
        }

        for (Operacao op : new ArrayList<>(operacoes.values())) {
            if (op.estado.isFinal()) continue;
            alertasDeTempo(op, agora);
            if (podeEnviar) acoesDeTempo(op, agora);
        }
        if (podeEnviar) {
            verificarZeragem();
            verificarPerdaDoDia();
        }
        verificarVencimento();
    }

    private void alertasDeTempo(Operacao op, Instant agora) {
        if (op.estado == EstadoOperacao.POSICIONADA_SEM_PROTECAO && op.semProtecaoDesde != null
                && Duration.between(op.semProtecaoDesde, agora).compareTo(cfg.alertaSemProtecao()) >= 0) {
            alertas.critico("SEM_PROTECAO:" + op.operacaoId, "posição " + op.posicao() + " sem proteção há mais de "
                    + cfg.alertaSemProtecao().toMillis() + " ms: operação " + op.operacaoId);
        }
        for (Ordem o : op.ordens) {
            if (o.estado == EstadoOrdem.PENDENTE && o.pendenteNovaDesde != null && !o.alertouPendente
                    && Duration.between(o.pendenteNovaDesde, agora).compareTo(cfg.alertaPendenteNova()) >= 0) {
                o.alertouPendente = true;
                alertas.alerta("Ordem em 39=A há mais de " + cfg.alertaPendenteNova().toMillis() + " ms: " + o);
            }
        }
    }

    private void acoesDeTempo(Operacao op, Instant agora) {
        // Entrada: cancela o restante no timeout. Não persegue o preço.
        op.viva(Papel.E).ifPresent(e -> {
            if (e.enviadaEm == null || e.pendencia != Pendencia.NENHUMA
                    || Duration.between(e.enviadaEm, agora).compareTo(cfg.timeoutEntrada()) < 0) return;
            if (e.cancelamentos == 0) {
                log.info("Timeout da entrada {}: cancelando o restante", e);
                cancelar(e);
            } else if (e.cancelamentos < 3 && Duration.between(e.ultimoCancelEm, agora).compareTo(cfg.esperaRetentativaProtecao()) >= 0) {
                cancelar(e);
            } else if (e.cancelamentos >= 3) {
                alertas.critico("ENTRADA_SEM_CANCELAR:" + op.operacaoId, "a entrada " + e + " não cancela (3 tentativas)");
            }
        });
        if (op.retentativaProtecaoEm != null && !agora.isBefore(op.retentativaProtecaoEm)) ajustarProtecao(op);

        switch (op.estado) {
            case PROTEGIDA -> verificarFuro(op, agora);
            case CANCELANDO_PROTECAO -> {
                Optional<Ordem> p = op.viva(Papel.P);
                if (p.isEmpty()) {
                    mudar(op, EstadoOperacao.SAINDO);
                    enviarSaida(op);
                } else if (p.get().pendencia == Pendencia.NENHUMA && !p.get().cancelarAposPendencia) {
                    // cancel que não saiu ou foi rejeitado por 35=3/35=j: tenta de novo, com intervalo
                    if (p.get().ultimoCancelEm == null
                            || Duration.between(p.get().ultimoCancelEm, agora).compareTo(cfg.esperaRetentativaProtecao()) >= 0) {
                        op.cancelProtecaoEnviadoEm = agora;
                        cancelar(p.get());
                    }
                } else if (op.cancelProtecaoEnviadoEm != null
                        && Duration.between(op.cancelProtecaoEnviadoEm, agora).compareTo(cfg.esperaStop()) >= 0) {
                    alertas.critico("CANCEL_SEM_RESPOSTA:" + op.operacaoId, "cancelamento da 1014 sem resposta: operação " + op.operacaoId);
                }
            }
            case AGUARDANDO_STOP -> {
                if (op.aguardandoStopDesde != null && Duration.between(op.aguardandoStopDesde, agora).compareTo(cfg.esperaStop()) >= 0) {
                    log.info("Stop não zerou a operação {} em {} ms; seguindo com a saída", op.operacaoId, cfg.esperaStop().toMillis());
                    mudar(op, EstadoOperacao.SAINDO);
                    enviarSaida(op);
                }
            }
            case SAINDO -> tickSaida(op, agora);
            default -> {
            }
        }
    }

    private void tickSaida(Operacao op, Instant agora) {
        if (op.posicao() == 0) {
            zerada(op);
            return;
        }
        Optional<Ordem> vs = op.viva(Papel.S);
        if (vs.isEmpty()) {
            if (op.ordens.stream().anyMatch(o -> o.papel != Papel.E && o.isViva())) return; // espera o cancel da 1014/saída antiga
            if (op.proximaSaidaEm == null || !agora.isBefore(op.proximaSaidaEm)) {
                op.proximaSaidaEm = null;
                enviarSaida(op);
            }
            return;
        }
        Ordem s = vs.get();
        if (s.pendencia != Pendencia.NENHUMA || s.estado == EstadoOrdem.ENVIANDO) return;
        if (op.proximoRepreco == null || agora.isBefore(op.proximoRepreco)) return;
        op.repreco++;
        if (op.repreco >= cfg.maxReprecoSaida()) {
            alertas.critico("SAIDA_ESGOTOU:" + op.operacaoId, "saída da operação " + op.operacaoId + " esgotou "
                    + cfg.maxReprecoSaida() + " repreços; continua reapregoando");
        }
        int k = Math.min(op.repreco, cfg.maxReprecoSaida()) + 1;
        BigDecimal preco = precoSaida(s.lado, k);
        op.proximoRepreco = agora.plus(cfg.timeoutSaida());
        if (preco == null) {
            alertas.critico("SEM_PRECO_SAIDA:" + op.operacaoId, "sem preço para reapregoar a saída da operação " + op.operacaoId);
            return;
        }
        int total = s.qtdExec + Math.abs(op.posicao());
        replace(s, total, preco, null, null);
    }

    /** Parte 3.6: último negócio além do limite da 1014 por mais de ESPERA_STOP_S. */
    private void verificarFuro(Operacao op, Instant agora) {
        Optional<Ordem> vp = op.viva(Papel.P);
        Optional<Cotacao> c = cotacaoFresca();
        if (vp.isEmpty() || c.isEmpty() || vp.get().precoStopLim == null) {
            op.furoDesde = null;
            return;
        }
        Ordem p = vp.get();
        BigDecimal ultimo = c.get().ultimo();
        boolean furou = p.lado == Lado.V ? ultimo.compareTo(p.precoStopLim) < 0 : ultimo.compareTo(p.precoStopLim) > 0;
        if (!furou || op.posicao() == 0) {
            op.furoDesde = null;
            return;
        }
        if (op.furoDesde == null) {
            op.furoDesde = agora;
            return;
        }
        if (Duration.between(op.furoDesde, agora).compareTo(cfg.esperaStop()) >= 0) {
            op.furado = true;
            alertas.critico("STOP_FURADO:" + op.operacaoId, "stop furado: último " + ultimo + " além do limite " + p.precoStopLim
                    + " com posição " + op.posicao() + ", operação " + op.operacaoId);
            op.motivoSaida = "STOP_FURADO";
            op.origemSaida = "SERVICO";
            op.saidaPedidaEm = agora;
            mudar(op, EstadoOperacao.CANCELANDO_PROTECAO);
            op.cancelProtecaoEnviadoEm = agora;
            cancelar(p);
        }
    }

    private void verificarZeragem() {
        if (zeragemFeita) return;
        LocalTime h = LocalTime.now(relogio.withZone(Repositorio.FUSO));
        if (h.isBefore(cfg.horaZeragem())) return;
        zeragemFeita = true;
        repo.gravarEstado(CHAVE_ZERAGEM, dia.toString());
        boolean algo = operacoes.values().stream().anyMatch(op -> !op.estado.isFinal() && (op.posicao() != 0 || op.viva(Papel.E).isPresent()));
        if (algo) {
            alertas.alerta("HORA_ZERAGEM " + cfg.horaZeragem() + " com posição " + posicaoTotal() + ": zerando (anomalia)");
            ativarParada(Parada.ZERAR, "auto", "hora de zeragem");
        }
    }

    private void verificarPerdaDoDia() {
        if (parada != null) return;
        Optional<Cotacao> c = cotacaoFresca();
        if (c.isEmpty()) return;
        BigDecimal r = resultadoDoDia(c.get().ultimo());
        if (r.compareTo(cfg.perdaDiariaPts().negate()) <= 0) {
            ativarParada(Parada.BLOQUEAR, "auto", "perda do dia " + r + " pts");
        }
    }

    private void verificarVencimento() {
        Optional<LocalDate> venc = cfg.vencimento();
        if (venc.isEmpty() || dia.equals(avisoVencimentoEm)) return;
        avisoVencimentoEm = dia;
        int pregoes = 0;
        for (LocalDate d = dia.plusDays(1); !d.isAfter(venc.get()); d = d.plusDays(1)) {
            if (d.getDayOfWeek() != DayOfWeek.SATURDAY && d.getDayOfWeek() != DayOfWeek.SUNDAY) pregoes++;
        }
        if (!dia.isBefore(venc.get())) {
            alertas.alerta("Vencimento de " + cfg.simbolo() + " é hoje (ou já passou): entradas recusadas até trocar ORDENS_SIMBOLO");
        } else if (pregoes <= cfg.pregoesAvisoVencimento()) {
            alertas.alerta("Vencimento de " + cfg.simbolo() + " em " + pregoes + " pregão(ões) (" + venc.get() + ")");
        }
    }

    private void virarDia() {
        boolean pendente = operacoes.values().stream().anyMatch(op -> !op.estado.isFinal());
        if (pendente) {
            alertas.critico("VIRADA_COM_OPERACAO", "virada de dia com operação não encerrada; estado mantido em memória");
            dia = hoje();
            return;
        }
        log.info("Virada de dia: {} → {}", dia, hoje());
        carregarDia();
    }

    // =====================================================================
    // Saúde e descrição
    // =====================================================================

    private Map<String, Object> montarSaude() {
        Instant agora = agora();
        aparar(enviosRecentes, agora.minusSeconds(60));
        aparar(entradasRecentes, agora.minusSeconds(60));
        aparar(recusasPorLimite, agora.minusSeconds(60));
        int[] seq = fix == null ? new int[] {0, 0} : fix.sequencias();
        Optional<Cotacao> c = precos.atual();
        List<Map<String, Object>> vivas = new ArrayList<>();
        for (Operacao op : operacoes.values()) {
            for (Ordem o : op.ordens) {
                if (!o.estado.isFinal()) vivas.add(mapa("clordid", o.clordidAtual, "orderid37", o.orderId37, "papel", o.papel.name(),
                        "estado", o.estado.name(), "qtd", o.qtd, "qtd_exec", o.qtdExec, "pendencia", o.pendencia.name()));
            }
        }
        Map<String, Object> limites = mapa(
                "entradas_hoje", entradasHoje,
                "entradas_ultimo_minuto", entradasRecentes.size(),
                "mensagens_ultimo_minuto", enviosRecentes.size(),
                "recusas_limite_ultimo_minuto", recusasPorLimite.size(),
                "ultima_entrada", ultimaEntrada == null ? null : ultimaEntrada.toString(),
                "resultado_dia_pts", resultadoDoDia(c.map(Cotacao::ultimo).orElse(null)));
        return mapa(
                "sessao_logada", fix != null && fix.logado(),
                "seq_entrada_esperada", seq[0],
                "seq_saida_proxima", seq[1],
                "conciliando", conciliando,
                "posicao", posicaoTotal(),
                "parada", parada,
                "divergente", divergente,
                "limites", limites,
                "idade_preco_ms", c.map(x -> x.idade() == null ? null : x.idade().toMillis()).orElse(null),
                "ordens_vivas", vivas,
                "alertas_criticos", alertas.ativos(),
                "simbolo", cfg.simbolo());
    }

    private Map<String, Object> descrever(Operacao op) {
        List<Map<String, Object>> os = new ArrayList<>();
        for (Ordem o : op.ordens) {
            os.add(mapa("papel", o.papel.name(), "clordid", o.clordidAtual, "orderid37", o.orderId37, "estado", o.estado.name(),
                    "qtd", o.qtd, "qtd_exec", o.qtdExec, "preco", o.preco, "preco_medio", o.precoMedio,
                    "preco_stop", o.precoStop, "preco_stop_lim", o.precoStopLim));
        }
        return mapa("existe", true, "operacao_id", op.operacaoId, "estado", op.estado.name(), "divergente", op.divergente,
                "posicao", op.posicao(), "preco_medio_entrada", op.precoMedioEntrada(), "preco_medio_saida", op.precoMedioSaida(),
                "motivo_saida", op.motivoSaida, "origem_saida", op.origemSaida, "pnl_pts", op.pnlPts, "ordens", os);
    }

    // =====================================================================
    // Utilitários
    // =====================================================================

    private Ordem novaOrdem(Operacao op, Papel papel, Lado lado, int qtd, int estrategia) {
        Ordem o = new Ordem();
        o.data = op.data;
        o.uid = op.uid;
        o.operacaoId = op.operacaoId;
        o.papel = papel;
        o.clordidBase = op.clordid(papel, op.proximoN(papel));
        o.clordidAtual = o.clordidBase;
        o.estrategia = estrategia;
        o.lado = lado;
        o.qtd = qtd;
        o.estado = EstadoOrdem.ENVIANDO;
        return o;
    }

    private void indexar(Operacao op, Ordem o) {
        if (!op.ordens.contains(o)) op.ordens.add(o);
        donoDa.put(o, op);
        porClordid.put(o.clordidBase, o);
        porClordid.put(o.clordidAtual, o);
        if (o.orderId37 != null) por37.put(o.orderId37, o);
    }

    private Ordem localizar(String id37, String cl, String orig) {
        Ordem o = null;
        if (id37 != null && !"NONE".equals(id37)) o = por37.get(id37);
        if (o == null && cl != null) o = porClordid.get(cl);
        if (o == null && orig != null) o = porClordid.get(orig);
        return o;
    }

    private boolean gravarRecebida(Ordem o, Message m) {
        String mt = campo(m.getHeader(), MsgType.FIELD);
        String cod = campo(m, 103);
        if (cod == null) cod = campo(m, 102);
        if (cod == null) cod = campo(m, 380);
        return repo.inserirEvento(new Repositorio.RegistroEvento(o == null ? null : o.id, "REC", mt, campo(m, 150), campo(m, 39),
                campo(m, 11), campo(m, 37), campo(m, 17), inteiro(m, 32), decimal(m, 31), inteiro(m, 14), decimal(m, 6),
                campo(m, 58), cod, m.toString()));
    }

    private void mudar(Operacao op, EstadoOperacao novo) {
        if (op.estado == novo) return;
        log.info("Operação {}: {} → {}", op.operacaoId, op.estado, novo);
        op.estado = novo;
        repo.atualizarOperacao(op);
    }

    private void publicar(String tipo, Operacao op, Map<String, Object> dados) {
        Map<String, Object> d = new LinkedHashMap<>();
        if (op != null) {
            d.put("operacao_id", op.operacaoId);
            d.put("uid", op.uid);
        }
        d.putAll(dados);
        try {
            long seq = repo.publicar(tipo, op == null ? null : op.operacaoId, json.writeValueAsString(d));
            log.info("Evento {} seq {} {}", tipo, seq, d);
        } catch (Exception e) {
            log.error("Falha ao publicar {}", tipo, e);
        }
    }

    private int posicaoTotal() {
        int s = 0;
        for (Operacao op : operacoes.values()) s += op.posicao();
        return s;
    }

    private BigDecimal resultadoDoDia(BigDecimal ultimo) {
        BigDecimal r = BigDecimal.ZERO;
        for (Operacao op : operacoes.values()) {
            r = r.add(op.pnlRealizado()).add(op.pnlAberto(ultimo));
        }
        return r;
    }

    private void verificarUid(PedidoOrdem p) {
        Optional<Integer> real = cfg.uidReal();
        if (real.isEmpty() || p.uid() == null || !real.get().equals(p.uid())) {
            throw Recusa.simples("UID_NAO_HABILITADO", "uid " + p.uid() + " não habilitado para ordens reais");
        }
    }

    private static Lado lado(String s) {
        if ("C".equals(s)) return Lado.C;
        if ("V".equals(s)) return Lado.V;
        throw Recusa.simples("CAMPO_INVALIDO", "lado deve ser C ou V");
    }

    private static boolean ehNosso(String clordid) {
        return clordid != null && clordid.startsWith("KM-");
    }

    private static void aparar(Deque<Instant> d, Instant limite) {
        while (!d.isEmpty() && d.peekFirst().isBefore(limite)) d.pollFirst();
    }

    private Instant agora() {
        return relogio.instant();
    }

    private LocalDate hoje() {
        return LocalDate.now(relogio.withZone(Repositorio.FUSO));
    }

    private <T> CompletableFuture<T> rodar(Callable<T> c) {
        CompletableFuture<T> f = new CompletableFuture<>();
        exec.execute(() -> {
            try {
                f.complete(c.call());
            } catch (Throwable t) {
                log.error("Erro no motor", t);
                f.completeExceptionally(t);
            }
        });
        return f;
    }

    private void protegido(String onde, Runnable r) {
        try {
            r.run();
        } catch (Throwable t) {
            log.error("Erro no motor ({})", onde, t);
            try {
                alertas.critico("ERRO_INTERNO", "erro interno no motor (" + onde + "): " + t);
            } catch (Throwable ignorado) {
                // o alerta não pode derrubar o motor
            }
        }
    }

    static String campo(quickfix.FieldMap m, int tag) {
        try {
            return m.isSetField(tag) ? m.getString(tag) : null;
        } catch (FieldNotFound e) {
            return null;
        }
    }

    static Integer inteiro(quickfix.FieldMap m, int tag) {
        String s = campo(m, tag);
        if (s == null || s.isBlank()) return null;
        try {
            return new BigDecimal(s.trim()).intValueExact();
        } catch (ArithmeticException | NumberFormatException e) {
            log.warn("Quantidade não inteira na tag {}: {}", tag, s);
            return new BigDecimal(s.trim()).setScale(0, RoundingMode.DOWN).intValue();
        }
    }

    static BigDecimal decimal(quickfix.FieldMap m, int tag) {
        String s = campo(m, tag);
        if (s == null || s.isBlank()) return null;
        try {
            return new BigDecimal(s.trim());
        } catch (NumberFormatException e) {
            return null;
        }
    }

    /** Mapa ordenado que aceita null (Map.of não aceita). */
    static Map<String, Object> mapa(Object... kv) {
        Map<String, Object> m = new LinkedHashMap<>();
        for (int i = 0; i < kv.length; i += 2) m.put((String) kv[i], kv[i + 1]);
        return m;
    }

    /** Para testes. */
    Collection<Operacao> operacoes() {
        return operacoes.values();
    }
}
