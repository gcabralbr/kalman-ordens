#!/usr/bin/env python3
"""pnt-md-hml: o /precos/atual da HML a partir do WebBroker Market Data (WB_MD) da PnT — só na .53,
ferramenta de teste (como a precos-hml, que serve o tick file; esta serve o que a HML toca de fato).

Conecta no /mdhub (SignalR, JSON), assina um ativo (LastTrade, AggrBook, SecurityStatus) e serve
GET /precos/atual?ativo=WINFUT em 127.0.0.1:9096 no MESMO contrato do marketdata
(docs/referencia/precos-atual.md). Sem ?ativo= vale WINFUT (o Kalman pede /precos/atual sem ativo);
outro ativo dá 400:

  preco = último LastTrade; bid/ask = topo do AggrBook; status84 = 0 com o papel em negociação
  ("Trading" / SecurityTradingStatus 17), senão o código (ou 1); ts/idade_seg = a chegada do campo
  mais fresco entre preco/bid/ask.

Seguro por padrão: sem conexão, feed indisponível (OnFeederUnavailable), token inválido ou sem dado
de hoje → corpo "{}" (o kalman-ordens recusa a entrada com SEM_PRECO). Cada queda e volta vai ao log.
Renova o token sozinho (refresh token; ver tools/pnt-md-spike/md_spike.py login-auto).

Grava num JSONL diário no formato do md_spike, em append: <--gravar-dir>/pnt-md-hml-<ativo>-AAAAMMDD.jsonl
(o WB_MD aceita uma conexão por usuário: quem precisar do feed lê daqui) — LastTrade, SecurityStatus,
quedas e voltas como chegaram; do AggrBook só o topo (--gravar-niveis por lado, ordenado, melhor
primeiro) e só quando muda. A escrita é numa thread à parte (fila); abaixo de --disco-min-gb livres a
gravação pausa (o preço continua); erro de escrita vai ao log uma vez, nova tentativa a cada 10 s.
--gravar-dir "" desliga.

Defesas (20/10: o AggrBook da HML veio Aggregated:false, MarketDepth 0, ~30 mil níveis duplicados e
cruzados, ~9,5 MB por mensagem; o cliente atrasou 3–11 s e o /precos/atual servia preço velho como
fresco):
  - topo calculado (maior bid, menor ask), nunca o [0] da lista;
  - book degenerado → fonte indisponível ({}): Aggregated false, MarketDepth 0, níveis acima de
    --niveis-max por lado, preço repetido no mesmo lado, ou book cruzado em negociação (status84 0);
  - atraso relativo: base = mínimo móvel (--janela-base-s) de (chegada − serverSendUtcMs); a mensagem
    que passa da base em mais de --atraso-max-s deixa a fonte indisponível até voltar. O relógio do
    PnT não é confiável (FIX: +1,65 a +1,96 s em 19/10, −0,45 s em 16/10): por isso a base, não um
    limite absoluto. A base vai no /saude.
  - reconexão automática (21/10: a conexão aberta antes do pré-abertura recebe o book degenerado o dia
    todo; uma nova depois da abertura vem normal): book degenerado em negociação por mais de
    --reconectar-s → close frame, espera o close do servidor, cliente novo (negotiate novo); no máximo
    uma a cada --reconectar-intervalo-s; CRITICAL no journal depois de --reconectar-alerta-apos
    seguidas sem book normal. Toda queda abre um cliente novo (a reconexão interna do pysignalr
    reusaria o ?id= antigo). POST /reconectar: o mesmo caminho, manual. SIGTERM fecha com close frame.

  pnt_md_hml.py [--ativo WINZ26] [--porta 9096] [--token /var/lib/kalman-pnt-md-hml/token.json]
  pnt_md_hml.py --simular var/pnt-md-WINZ26-20261016.jsonl [--velocidade 1]   # fonte gravada (teste)
"""
import argparse
import asyncio
import datetime as dt
import json
import logging
import os
import queue
import shutil
import signal
import threading
from collections import deque
import time
import uuid

import requests
from aiohttp import web

ISSUER = "https://auth-hml.pnt.api.br/PnTAuthorizationServerProvider"
HUB = "https://webbrokerMDhml.pnt.api.br/mdhub"
CLIENT_ID = "pnt_external_gammaq"
CONCEITO = "WINFUT"
# fora do /home (20/10: /home cheio) e com escrita atômica; o md_spike (login) grava no mesmo arquivo
TOKEN = "/var/lib/kalman-pnt-md-hml/token.json"
FUSO = dt.timezone(dt.timedelta(hours=-3))
TOPICOS = ("LastTrade", "AggrBook", "SecurityStatus")

log = logging.getLogger("pnt-md-hml")


# ------------------------------------------------------------------ estado e contrato

class Estado:
    """O último preço, o topo do book e o status, com a hora de chegada; e se a fonte está boa."""

    def __init__(self, ticker, agora=time.time, niveis_max=50, atraso_max_s=1.0, janela_base_s=600.0):
        self.ticker, self.agora = ticker, agora
        self.niveis_max, self.atraso_max_ms, self.janela_base_s = niveis_max, atraso_max_s * 1000, janela_base_s
        self.preco = self.bid = self.ask = None
        self.preco_em = self.book_em = 0.0
        self.status84 = None  # sem SecurityStatus ainda: não negociável
        self.conectado = False
        self.feed_ok = True
        self.token_ok = True
        self.book_ok = True
        self.em_dia = True  # atraso dentro da base
        self.motivo = "sem conexão"
        self.book_motivo = ""
        self.book_degenerado = False  # o último AggrBook DESTA conexão veio degenerado (reconexão automática)
        self.niveis = (0, 0)
        # atraso relativo: (chegada, chegada − envio do servidor em ms), mínimo móvel na janela
        self.atrasos = deque()
        self.atraso_base_ms = self.atraso_ultimo_ms = None

    def disponivel(self):
        return self.conectado and self.feed_ok and self.token_ok and self.book_ok and self.em_dia

    def marcar(self, conectado=None, feed_ok=None, token_ok=None, book_ok=None, em_dia=None, motivo=""):
        antes = self.disponivel()
        if conectado is not None:
            self.conectado = conectado
        if feed_ok is not None:
            self.feed_ok = feed_ok
        if token_ok is not None:
            self.token_ok = token_ok
        if book_ok is not None:
            self.book_ok = book_ok
        if em_dia is not None:
            self.em_dia = em_dia
        depois = self.disponivel()
        if antes and not depois:
            self.motivo = motivo
            log.warning("fonte INDISPONÍVEL (%s): /precos/atual responde {}", motivo)
        elif depois and not antes:
            log.warning("fonte de volta (%s): depois de '%s'", motivo, self.motivo)
            self.motivo = ""
        if not self.conectado:
            # reconexão: o book e o preço da conexão anterior não valem até chegarem de novo; a base
            # do atraso recomeça (outra conexão, talvez outro servidor)
            self.preco = self.bid = self.ask = None
            self.book_degenerado = False
            self.atrasos.clear()
            self.atraso_base_ms = self.atraso_ultimo_ms = None
            self.em_dia = True

    def medir_atraso(self, chegada, enviado_ms):
        """Atraso relativo: a base é o mínimo de (chegada − envio do servidor) na janela; passar da base
        em mais de atraso_max deixa a fonte indisponível (o relógio do PnT não é confiável)."""
        d = chegada * 1000 - enviado_ms
        # mínimo móvel: deque monotônico (crescente em d)
        while self.atrasos and self.atrasos[-1][1] >= d:
            self.atrasos.pop()
        self.atrasos.append((chegada, d))
        while self.atrasos[0][0] < chegada - self.janela_base_s:
            self.atrasos.popleft()
        self.atraso_base_ms, self.atraso_ultimo_ms = self.atrasos[0][1], d
        excesso = d - self.atraso_base_ms
        if excesso > self.atraso_max_ms and self.em_dia:
            self.marcar(em_dia=False, motivo=f"mensagens {excesso / 1000:.1f} s atrás da base ({self.atraso_base_ms:.0f} ms)")
        elif excesso <= self.atraso_max_ms and not self.em_dia:
            self.marcar(em_dia=True, motivo=f"atraso de volta à base ({excesso:.0f} ms acima)")

    def update(self, topico, payload, chegada=None, enviado_ms=None):
        chegada = chegada if chegada is not None else self.agora()
        if isinstance(enviado_ms, (int, float)):
            self.medir_atraso(chegada, enviado_ms)
        if topico == "LastTrade" and isinstance(payload, dict) and payload.get("Price") is not None:
            self.preco, self.preco_em = float(payload["Price"]), chegada
        elif topico == "AggrBook" and isinstance(payload, dict):
            motivo, bid, ask, self.niveis = avaliar_book(payload, self.niveis_max, self.status84 == 0)
            self.book_degenerado = bool(motivo)
            if motivo:
                self.bid = self.ask = None
                if self.book_ok or motivo != self.book_motivo:
                    self.book_motivo = motivo
                    self.marcar(book_ok=False, motivo="book degenerado: " + motivo)
                return
            self.bid, self.ask, self.book_em = bid, ask, chegada
            if not self.book_ok:
                self.book_motivo = ""
                self.marcar(book_ok=True, motivo="book normal")
        elif topico == "SecurityStatus":
            self.status84 = status84(payload)

    def resposta(self):
        """O corpo do GET /precos/atual (dict); {} sem fonte ou sem dado de hoje."""
        if not self.disponivel():
            return {}
        agora = self.agora()
        hoje = dt.datetime.fromtimestamp(agora, FUSO).date()

        def de_hoje(t):
            return t and dt.datetime.fromtimestamp(t, FUSO).date() == hoje

        p = {"ticker": self.ticker}
        mais_fresco = 0.0
        if self.preco is not None and de_hoje(self.preco_em):
            p["preco"], mais_fresco = self.preco, max(mais_fresco, self.preco_em)
        if de_hoje(self.book_em):
            if self.bid is not None:
                p["bid"] = self.bid
            if self.ask is not None:
                p["ask"] = self.ask
            if self.bid is not None or self.ask is not None:
                mais_fresco = max(mais_fresco, self.book_em)
        if not mais_fresco:
            return {}
        p["status84"] = 1 if self.status84 is None else self.status84
        p["ts"] = dt.datetime.fromtimestamp(mais_fresco, FUSO).isoformat(timespec="seconds")
        p["idade_seg"] = round(max(agora - mais_fresco, 0.0), 1)
        return {CONCEITO: p}


def avaliar_book(p, niveis_max, negociando):
    """(motivo, bid, ask, (níveis bid, níveis offer)) do AggrBook; motivo vazio = book usável. O topo é
    o maior bid e o menor ask (a lista pode vir fora de ordem). Cruzado só conta em negociação (no
    leilão o book cruza; o status84 ≠ 0 já recusa a entrada)."""
    bids, offers = p.get("ListBids") or [], p.get("ListOffers") or []
    niveis = (len(bids), len(offers))
    if p.get("Aggregated") is False:
        return "Aggregated=false", None, None, niveis
    if "MarketDepth" in p and not p.get("MarketDepth"):
        return "MarketDepth=0", None, None, niveis
    if max(niveis) > niveis_max:
        return f"{niveis[0]}/{niveis[1]} níveis (máximo {niveis_max})", None, None, niveis
    pb = [float(x["Price"]) for x in bids if isinstance(x, dict) and x.get("Price")]
    po = [float(x["Price"]) for x in offers if isinstance(x, dict) and x.get("Price")]
    if len(set(pb)) != len(pb) or len(set(po)) != len(po):
        return "preço repetido no mesmo lado", None, None, niveis
    bid, ask = (max(pb) if pb else None), (min(po) if po else None)
    if negociando and bid is not None and ask is not None and bid >= ask:
        return f"book cruzado ({bid:.0f} ≥ {ask:.0f})", None, None, niveis
    return "", bid, ask, niveis


def topo_book(p, n, niveis_max=None):
    """O AggrBook reduzido para a gravação: n níveis por lado, ordenados (melhor primeiro). Com mais de
    niveis_max num lado (book degenerado, 20/10: ~30 mil), só o resumo — ordenar a cada mensagem
    aumentaria o atraso."""
    nb, no = len(p.get("ListBids") or []), len(p.get("ListOffers") or [])
    if niveis_max is not None and max(nb, no) > niveis_max:
        return {"Aggregated": p.get("Aggregated"), "MarketDepth": p.get("MarketDepth"), "Niveis": [nb, no], "Degenerado": True}

    def nivel(x):
        return {"Price": x.get("Price"), "Quantity": x.get("Quantity"), "NumOrders": x.get("NumOrders")}
    bids = sorted((x for x in p.get("ListBids") or [] if isinstance(x, dict) and x.get("Price")), key=lambda x: -x["Price"])
    offers = sorted((x for x in p.get("ListOffers") or [] if isinstance(x, dict) and x.get("Price")), key=lambda x: x["Price"])
    return {"Aggregated": p.get("Aggregated"), "MarketDepth": p.get("MarketDepth"),
            "Niveis": [len(p.get("ListBids") or []), len(p.get("ListOffers") or [])],
            "ListBids": [nivel(x) for x in bids[:n]], "ListOffers": [nivel(x) for x in offers[:n]]}


def status84(payload):
    """0 = negociação normal ("Trading" ou SecurityTradingStatus 17); senão o código, ou 1."""
    if isinstance(payload, str):
        return 0 if payload.strip().lower() == "trading" else 1
    if isinstance(payload, dict):
        c = payload.get("SecurityTradingStatus")
        if c == 17:
            return 0
        return int(c) if isinstance(c, int) and c != 0 else 1
    if isinstance(payload, int):
        return 0 if payload == 17 else (payload or 1)
    return 1


class Reconexao:
    """Reconexão automática com o book degenerado (21/10: a conexão aberta antes do pré-abertura recebe
    o AggrBook não agregado e sem limite o dia todo; uma conexão nova depois da abertura vem normal).
    Pede reconexão quando o book desta conexão está degenerado com o papel em negociação (status84 0)
    por mais de limiar_s, no máximo uma a cada intervalo_s; depois de alerta_apos seguidas sem um book
    normal, alerta crítico no journal (e `reconexao_alerta` no /saude) a cada nova tentativa."""

    def __init__(self, limiar_s=10.0, intervalo_s=60.0, alerta_apos=5):
        self.limiar_s, self.intervalo_s, self.alerta_apos = limiar_s, intervalo_s, alerta_apos
        self.desde = None  # início da condição (degenerado e em negociação), contínua
        self.total = self.seguidas = 0
        self.ultima_em = None
        self.ultima = {}  # hora, motivo e se o servidor confirmou o close
        self.pedido = None  # POST /reconectar (manual: sem limiar nem intervalo)

    def observar(self, agora, estado):
        """O motivo da reconexão (str) quando é hora de reconectar; senão None."""
        if self.pedido:
            motivo, self.pedido = self.pedido, None
            return motivo
        if estado.book_ok and self.seguidas:
            log.warning("reconexão: book normal depois de %d reconexão(ões) seguida(s)", self.seguidas)
            self.seguidas = 0
        if not (estado.conectado and estado.book_degenerado and estado.status84 == 0):
            self.desde = None
            return None
        if self.desde is None:
            self.desde = agora
        if agora - self.desde < self.limiar_s:
            return None
        if self.ultima_em is not None and agora - self.ultima_em < self.intervalo_s:
            return None
        return f"book degenerado em negociação há {agora - self.desde:.0f} s ({estado.book_motivo})"

    def registrar(self, agora, motivo):
        self.total += 1
        self.seguidas += 1
        self.ultima_em, self.desde = agora, None
        self.ultima = {"em": dt.datetime.fromtimestamp(agora, FUSO).isoformat(timespec="seconds"), "motivo": motivo}
        if self.seguidas >= self.alerta_apos:
            log.critical("ALERTA: %d reconexões seguidas sem book normal (%s)", self.seguidas, motivo)
        else:
            log.warning("reconexão %d (seguida %d): %s", self.total, self.seguidas, motivo)

    def saude(self):
        return {"reconexoes_book": self.total, "reconexoes_seguidas": self.seguidas,
                "reconexao_alerta": self.seguidas >= self.alerta_apos, "ultima_reconexao": self.ultima or None}


def aplicativo(estado, grav=None, rec=None):
    async def atual(req):
        if req.query.get("ativo", CONCEITO) != CONCEITO:  # sem ativo: WINFUT (o pedido do Kalman)
            return web.Response(status=400, text="ativo desconhecido")
        return web.json_response(estado.resposta())

    async def saude(req):
        return web.json_response({**(rec.saude() if rec else {}), "disponivel": estado.disponivel(), "conectado": estado.conectado,
                                  "feed_ok": estado.feed_ok, "token_ok": estado.token_ok, "motivo": estado.motivo,
                                  "status84": estado.status84,
                                  "book_ok": estado.book_ok, "book_motivo": estado.book_motivo, "niveis": list(estado.niveis),
                                  "em_dia": estado.em_dia, "atraso_base_ms": estado.atraso_base_ms,
                                  "atraso_ultimo_ms": estado.atraso_ultimo_ms,
                                  "gravadas": getattr(grav, "gravadas", 0), "descartadas": getattr(grav, "descartadas", 0),
                                  "gravacao_pausada": getattr(grav, "pausado", False)})

    async def reconectar(req):
        # manual (só em 127.0.0.1): o mesmo caminho da automática — close frame, cliente novo
        if rec is None:
            return web.Response(status=404)
        rec.pedido = "pedido manual (POST /reconectar)"
        return web.json_response({"pedido": True})

    app = web.Application()
    app.router.add_get("/precos/atual", atual)
    app.router.add_get("/saude", saude)
    app.router.add_post("/reconectar", reconectar)
    return app


# ------------------------------------------------------------------ gravação bruta

class Gravador:
    """JSONL diário (pelo dia da chegada), em append, escrito por uma thread: o laço de leitura só
    enfileira. Fila cheia (escrita travada) → descarta e conta; erro de escrita → log, segue."""

    def __init__(self, pasta, ativo, maximo=200_000, disco_min_gb=3.0, livre=None, iniciar=True):
        self.pasta, self.ativo = pasta, ativo
        self.fila = queue.Queue(maxsize=maximo)
        self.descartadas = self.gravadas = 0
        self.arq = self.dia = None
        self.falhou = False
        self.tentar_em = 0.0  # depois de um erro de escrita, a próxima tentativa
        self.disco_min = disco_min_gb * 2**30
        self.livre = livre or (lambda: shutil.disk_usage(self.pasta).free)
        self.pausado = False
        self.disco_visto_em = 0.0
        if iniciar:
            threading.Thread(target=self._escrever, name="gravador", daemon=True).start()

    def gravar(self, evento, dados, chegada=None):
        t = chegada if chegada is not None else time.time()
        try:
            self.fila.put_nowait((t, evento, dados))
        except queue.Full:
            self.descartadas += 1
            if self.descartadas in (1, 100, 10_000) or self.descartadas % 100_000 == 0:
                log.error("gravação: fila cheia, %d mensagem(ns) descartada(s)", self.descartadas)

    def caminho(self, t):
        d = dt.datetime.fromtimestamp(t, FUSO).strftime("%Y%m%d")
        return d, os.path.join(self.pasta, f"pnt-md-hml-{self.ativo}-{d}.jsonl")

    def _escrever(self):
        while True:
            self._uma(*self.fila.get())

    def _disco(self, agora):
        """Guarda de disco (a cada 5 s): abaixo do mínimo pausa; volta 1 GB acima dele."""
        if agora - self.disco_visto_em < 5:
            return
        self.disco_visto_em = agora
        try:
            livre = self.livre()
        except OSError:
            return
        if not self.pausado and livre < self.disco_min:
            self.pausado = True
            log.error("gravação PAUSADA: %.1f GB livres em %s (mínimo %.1f); o preço continua", livre / 2**30, self.pasta, self.disco_min / 2**30)
        elif self.pausado and livre > self.disco_min + 2**30:
            self.pausado = False
            log.warning("gravação de volta: %.1f GB livres", livre / 2**30)

    def _uma(self, t, evento, dados):
        agora = time.time()
        self._disco(agora)
        if self.pausado or agora < self.tentar_em:
            self.descartadas += 1
            return
        try:
            dia, caminho = self.caminho(t)
            if dia != self.dia:
                if self.arq:
                    self.arq.close()
                self.arq, self.dia = open(caminho, "a", buffering=1), dia
                log.info("gravação: %s", caminho)
            chegada = dt.datetime.fromtimestamp(t, FUSO).isoformat(timespec="milliseconds")
            self.arq.write(json.dumps({"chegada": chegada, "chegada_ms": int(t * 1000), "evento": evento, "args": dados},
                                      ensure_ascii=False, default=str) + "\n")
            self.gravadas += 1
            if self.falhou:
                log.warning("gravação: de volta (%d mensagem(ns) perdida(s) até aqui)", self.descartadas)
                self.falhou = False
        except Exception as e:
            if not self.falhou:
                log.error("gravação: falhou (%r); o serviço continua; nova tentativa a cada 10 s", e)
                self.falhou = True
            self.descartadas += 1
            self.tentar_em = agora + 10
            if self.arq:
                try:
                    self.arq.close()
                except OSError:
                    pass
            self.arq, self.dia = None, None


class SemGravador:
    gravadas = descartadas = 0
    pausado = False

    def gravar(self, *a, **k):
        pass


# ------------------------------------------------------------------ token

def gravar_atomico(caminho, dados):
    """JSON em caminho.tmp (600) no mesmo diretório, fsync e rename: quem lê vê o arquivo antigo ou o
    novo, nunca um pela metade (20/10: com o disco cheio, o O_TRUNC + json.dump deixaria o token vazio
    e o refresh token perdido). Erro: o arquivo antigo fica intacto e a exceção sobe."""
    tmp = f"{caminho}.{os.getpid()}.tmp"
    try:
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            json.dump(dados, f)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, caminho)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise
    d = os.open(os.path.dirname(os.path.abspath(caminho)), os.O_RDONLY)
    try:
        os.fsync(d)
    finally:
        os.close(d)


class Tokens:
    def __init__(self, arq):
        self.arq = arq
        self.endpoint = None
        self.t = self.ler()

    def ler(self):
        with open(self.arq) as f:
            return json.load(f)

    def guardar(self, t):
        # primeiro na memória: o refresh token antigo já foi girado pelo servidor; se o disco falhar, o
        # novo segue valendo aqui e a gravação é tentada de novo a cada atual()
        self.t = dict(t, obtido_em=time.time())
        self.pendente = True
        self.gravar_pendente()

    def gravar_pendente(self):
        try:
            gravar_atomico(self.arq, self.t)
        except OSError as e:
            if not getattr(self, "falhou", False):
                log.error("token: falha ao gravar %s (%r); o token novo segue na memória, nova tentativa a cada uso", self.arq, e)
            self.falhou = True
            return
        if getattr(self, "falhou", False):
            log.warning("token: gravado de novo em %s", self.arq)
        self.pendente = self.falhou = False

    def atual(self):
        if getattr(self, "pendente", False):
            self.gravar_pendente()
        if time.time() > self.t["obtido_em"] + int(self.t.get("expires_in", 3600)) - 300:
            # outro processo (o md_spike) pode ter renovado e girado o refresh token: relê antes
            disco = self.ler()
            if disco.get("obtido_em", 0) > self.t.get("obtido_em", 0):
                self.t = disco
            if time.time() > self.t["obtido_em"] + int(self.t.get("expires_in", 3600)) - 300:
                self.renovar()
        return self.t["access_token"]

    def renovar(self):
        if not self.endpoint:
            self.endpoint = requests.get(ISSUER + "/.well-known/openid-configuration", timeout=15).json()["token_endpoint"]
        rt = self.t.get("refresh_token")
        r = requests.post(self.endpoint, timeout=15, data={"grant_type": "refresh_token", "client_id": CLIENT_ID, "refresh_token": rt})
        if r.status_code != 200:
            raise PermissionError(f"renovação do token falhou: HTTP {r.status_code} {r.text[:200]}")
        novo = r.json()
        novo.setdefault("refresh_token", rt)
        self.guardar(novo)
        log.info("token renovado (expira em %s s)", novo.get("expires_in"))


# ------------------------------------------------------------------ fontes

class ConexaoEncerrada(Exception):
    """Levantada no on_close: termina o cliente.run() em vez de deixar o pysignalr reconectar sozinho."""


async def fechar_ws(cliente, timeout):
    """Fecha o websocket do cliente com close frame (1000) e espera o close do servidor. Devolve o que
    o servidor respondeu ({"code", "reason"}) ou None (sem conexão, sem resposta no prazo). O pysignalr
    1.3.2 não tem método público para fechar: o websocket está em _transport._ws."""
    ws = getattr(getattr(cliente, "_transport", None), "_ws", None)
    if ws is None:
        return None
    try:
        await asyncio.wait_for(ws.close(1000, "reconexao"), timeout)
    except Exception as e:  # inclui o timeout: segue para o cliente novo
        log.error("reconexão: falha ao fechar o websocket: %r", e)
    rcvd = getattr(getattr(ws, "protocol", None), "close_rcvd", None)  # websockets 16: no protocol
    return {"code": rcvd.code, "reason": rcvd.reason} if rcvd is not None else None


async def fonte_ao_vivo(estado, args, grav, rec=None):
    from pysignalr.client import SignalRClient

    tokens = Tokens(args.token)
    rec = rec or Reconexao()
    while True:
        try:
            tok = tokens.atual()
            estado.marcar(token_ok=True, motivo="token válido")
        except Exception as e:  # token inválido: {} até renovar (nova tentativa em 60 s)
            estado.marcar(token_ok=False, motivo=f"token: {e}")
            await asyncio.sleep(60)
            continue
        # pysignalr 1.x só põe o token no header no handshake, depois do negotiate (401 sem ele)
        cab = {"Authorization": "Bearer " + tok}
        # o padrão do pysignalr é 1 MB: na abertura de 19/10 o WB_MD mandou mensagens maiores (1009, laço
        # de reconexão a cada ~2 s das 09:01 em diante)
        cliente = SignalRClient(args.hub, headers=cab, access_token_factory=tokens.atual, max_size=args.max_mb << 20)

        topo_gravado = [None]

        async def md(dados):
            chegada = time.time()
            enviado = dados[3] if len(dados) >= 4 else None
            if len(dados) >= 3 and dados[1] == "AggrBook" and isinstance(dados[2], dict):
                # do book só o topo, e só quando muda (20/10: 17 GB em 27 min com o book inteiro)
                topo = topo_book(dados[2], args.gravar_niveis, args.niveis_max)
                if topo != topo_gravado[0]:
                    topo_gravado[0] = topo
                    grav.gravar("OnUpdateMarketData", [dados[0], "AggrBook", topo] + list(dados[3:]), chegada)
            else:
                grav.gravar("OnUpdateMarketData", dados, chegada)
            if len(dados) >= 3 and dados[0] == args.ativo:
                estado.update(dados[1], dados[2], chegada, enviado)

        async def indisponivel(dados):
            grav.gravar("OnFeederUnavailable", dados)
            estado.marcar(feed_ok=False, motivo=f"OnFeederUnavailable {dados}")

        async def recuperado(dados):
            grav.gravar("OnFeederRecovered", dados)
            estado.marcar(feed_ok=True, motivo=f"OnFeederRecovered {dados}")

        async def erro_sub(dados):
            grav.gravar("OnSubscribeMarketDataError", dados)
            log.error("erro na assinatura/autorização: %s", dados)

        cliente.on("OnUpdateMarketData", md)
        cliente.on("OnFeederUnavailable", indisponivel)
        cliente.on("OnFeederRecovered", recuperado)
        cliente.on("OnSubscribeMarketDataError", erro_sub)
        cliente.on("OnAuthorizationError", erro_sub)

        async def aberto():
            grav.gravar("_conectado", [])
            estado.marcar(conectado=True, feed_ok=True, motivo="conectado")
            for t in TOPICOS:  # sem reassinatura automática: a cada (re)conexão
                await cliente.send("SubscribeMarketData", [str(uuid.uuid4()), args.ativo, t])

        async def fechado():
            grav.gravar("_desconectado", [])
            estado.marcar(conectado=False, motivo="conexão fechada")
            # a reconexão interna do pysignalr reusa a URL com o connectionId antigo (sem negotiate): em
            # vez dela, o laço abaixo abre um cliente novo (negotiate novo, connectionId novo)
            raise ConexaoEncerrada()

        async def erro(m):
            grav.gravar("_erro", [str(m)])
            log.error("erro no hub: %s", m)

        cliente.on_open(aberto)
        cliente.on_close(fechado)
        cliente.on_error(erro)

        async def renovador():
            while True:
                await asyncio.sleep(60)
                try:
                    cab["Authorization"] = "Bearer " + tokens.atual()
                    h = getattr(getattr(cliente, "_transport", None), "_headers", None)
                    if isinstance(h, dict):
                        h["Authorization"] = cab["Authorization"]
                except Exception as e:
                    estado.marcar(token_ok=False, motivo=f"token: {e}")

        async def vigia():
            # reconexão automática: fecha a conexão antiga (close frame) antes de abrir a nova
            while True:
                await asyncio.sleep(getattr(args, "vigia_s", 1.0))
                motivo = rec.observar(time.time(), estado)
                if motivo:
                    fechando.set()
                    rec.registrar(time.time(), motivo)
                    grav.gravar("_reconexao_book", [motivo])
                    close = await fechar_ws(cliente, args.fechar_timeout_s)
                    rec.ultima["close_servidor"] = close
                    log.warning("reconexão: websocket fechado; close do servidor: %s", close or "NÃO recebido")
                    rodando.cancel()  # se o on_close já encerrou o run(), não faz nada
                    return

        fechando = asyncio.Event()
        ren = asyncio.create_task(renovador())
        rodando = asyncio.create_task(cliente.run())
        vig = asyncio.create_task(vigia())
        try:
            await rodando
        except ConexaoEncerrada:
            log.warning("conexão fechada; cliente novo em %d s", args.espera_reconexao_s)
        except asyncio.CancelledError:
            if not fechando.is_set():
                # fim do serviço (SIGTERM do systemctl stop/restart): close frame, para o servidor soltar a
                # sessão antes de o próximo processo conectar
                ren.cancel()
                vig.cancel()
                close = await fechar_ws(cliente, 2)
                log.warning("encerrando: websocket fechado; close do servidor: %s", close or "NÃO recebido")
                raise
            log.warning("cliente encerrado pela reconexão; cliente novo em %d s", args.espera_reconexao_s)
        except Exception as e:
            log.error("cliente terminou: %r; cliente novo em %d s", e, args.espera_reconexao_s)
        finally:
            ren.cancel()
            if fechando.is_set() and not vig.done():
                # o on_close encerrou o run() antes de o close terminar: deixa a vigia registrar a resposta
                await asyncio.wait([vig], timeout=args.fechar_timeout_s + 1)
            vig.cancel()
        estado.marcar(conectado=False, motivo="cliente terminou")
        await asyncio.sleep(args.espera_reconexao_s)


async def fonte_gravada(estado, args):
    """Reproduz um JSONL do md_spike (o mesmo formato) no ritmo gravado (× velocidade)."""
    import gzip
    abrir = gzip.open if args.simular.endswith(".gz") else open
    regs = []
    for l in abrir(args.simular, "rt"):
        try:
            regs.append(json.loads(l))
        except ValueError:
            pass  # a última linha de uma amostra cortada
    t0 = regs[0]["chegada_ms"] / 1000
    inicio = time.time()
    for r in regs:
        alvo = inicio + (r["chegada_ms"] / 1000 - t0) / args.velocidade
        await asyncio.sleep(max(0.0, alvo - time.time()))
        ev = r["evento"]
        if ev == "_conectado":
            estado.marcar(conectado=True, feed_ok=True, motivo="gravação: conectado")
        elif ev == "_desconectado":
            estado.marcar(conectado=False, motivo="gravação: desconectado")
        elif ev == "OnFeederUnavailable":
            estado.marcar(feed_ok=False, motivo="gravação: OnFeederUnavailable")
        elif ev == "OnFeederRecovered":
            estado.marcar(feed_ok=True, motivo="gravação: OnFeederRecovered")
        elif ev == "OnUpdateMarketData" and r["args"][0] == args.ativo:
            a = r["args"]
            estado.update(a[1], a[2], None, a[3] if len(a) >= 4 else None)
    estado.marcar(conectado=False, motivo="gravação terminou")
    log.info("gravação terminou")


async def principal(args):
    estado = Estado(args.ativo, niveis_max=args.niveis_max, atraso_max_s=args.atraso_max_s, janela_base_s=args.janela_base_s)
    grav = Gravador(args.gravar_dir, args.ativo, disco_min_gb=args.disco_min_gb) if args.gravar_dir and not args.simular else SemGravador()
    rec = Reconexao(args.reconectar_s, args.reconectar_intervalo_s, args.reconectar_alerta_apos)
    runner = web.AppRunner(aplicativo(estado, grav, rec), access_log=None)  # o kalman-ordens consulta a cada 200 ms
    await runner.setup()
    await web.TCPSite(runner, "127.0.0.1", args.porta).start()
    log.info("pnt-md-hml: %s → http://127.0.0.1:%d/precos/atual?ativo=%s (%s)", args.ativo, args.porta, CONCEITO,
             "gravação " + args.simular if args.simular else "WB_MD " + args.hub)
    # SIGTERM (systemctl stop/restart): cancela o serviço; a fonte ao vivo fecha o websocket antes de sair
    asyncio.get_running_loop().add_signal_handler(signal.SIGTERM, asyncio.current_task().cancel)
    try:
        await (fonte_gravada(estado, args) if args.simular else fonte_ao_vivo(estado, args, grav, rec))
        if args.simular and args.ficar:
            await asyncio.Event().wait()
    except asyncio.CancelledError:
        log.info("pnt-md-hml: encerrado (SIGTERM)")
    finally:
        await runner.cleanup()


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--ativo", default=os.environ.get("PNT_MD_ATIVO", "WINZ26"))
    ap.add_argument("--porta", type=int, default=int(os.environ.get("PNT_MD_PORTA", "9096")))
    ap.add_argument("--hub", default=os.environ.get("PNT_MD_HUB", HUB))
    ap.add_argument("--token", default=os.environ.get("PNT_MD_TOKEN", TOKEN))
    ap.add_argument("--max-mb", type=int, default=int(os.environ.get("PNT_MD_MAX_MB", "64")),
                    help="tamanho máximo de uma mensagem do WB_MD (MB)")
    ap.add_argument("--gravar-dir", default=os.environ.get("PNT_MD_GRAVAR_DIR", os.path.join(
        os.path.dirname(os.path.abspath(__file__)), "..", "..", "var")), help='JSONL diário das mensagens brutas ("" desliga)')
    ap.add_argument("--gravar-niveis", type=int, default=int(os.environ.get("PNT_MD_GRAVAR_NIVEIS", "5")),
                    help="níveis do book por lado na gravação (só quando o topo muda)")
    ap.add_argument("--disco-min-gb", type=float, default=float(os.environ.get("PNT_MD_DISCO_MIN_GB", "3")),
                    help="abaixo disto livre na pasta da gravação, ela pausa (o preço continua)")
    ap.add_argument("--niveis-max", type=int, default=int(os.environ.get("PNT_MD_NIVEIS_MAX", "50")),
                    help="mais níveis que isto num lado do AggrBook = book degenerado (fonte indisponível)")
    ap.add_argument("--atraso-max-s", type=float, default=float(os.environ.get("PNT_MD_ATRASO_MAX_S", "1")),
                    help="atraso acima da base (mínimo móvel de chegada − envio do servidor) = fonte indisponível")
    ap.add_argument("--janela-base-s", type=float, default=float(os.environ.get("PNT_MD_JANELA_BASE_S", "600")),
                    help="janela do mínimo móvel do atraso")
    ap.add_argument("--reconectar-s", type=float, default=float(os.environ.get("PNT_MD_RECONECTAR_S", "10")),
                    help="book degenerado em negociação por mais que isto: reconecta com cliente novo")
    ap.add_argument("--reconectar-intervalo-s", type=float, default=float(os.environ.get("PNT_MD_RECONECTAR_INTERVALO_S", "60")),
                    help="no máximo uma reconexão automática a cada")
    ap.add_argument("--reconectar-alerta-apos", type=int, default=int(os.environ.get("PNT_MD_RECONECTAR_ALERTA_APOS", "5")),
                    help="reconexões seguidas sem book normal até o alerta crítico")
    ap.add_argument("--espera-reconexao-s", type=int, default=int(os.environ.get("PNT_MD_ESPERA_RECONEXAO_S", "5")),
                    help="espera entre fechar uma conexão e abrir a nova")
    ap.add_argument("--fechar-timeout-s", type=float, default=5.0, help="espera pelo close do servidor")
    ap.add_argument("--simular", help="JSONL gravado pelo md_spike (fonte simulada, para teste)")
    ap.add_argument("--velocidade", type=float, default=1.0)
    ap.add_argument("--ficar", action="store_true", help="com --simular, continua servindo depois da gravação")
    args = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    asyncio.run(principal(args))


if __name__ == "__main__":
    main()
