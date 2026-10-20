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

  pnt_md_hml.py [--ativo WINZ26] [--porta 9096] [--token ~/.config/pnt-md-spike/token.json]
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


def aplicativo(estado, grav=None):
    async def atual(req):
        if req.query.get("ativo", CONCEITO) != CONCEITO:  # sem ativo: WINFUT (o pedido do Kalman)
            return web.Response(status=400, text="ativo desconhecido")
        return web.json_response(estado.resposta())

    async def saude(req):
        return web.json_response({"disponivel": estado.disponivel(), "conectado": estado.conectado,
                                  "feed_ok": estado.feed_ok, "token_ok": estado.token_ok, "motivo": estado.motivo,
                                  "status84": estado.status84,
                                  "book_ok": estado.book_ok, "book_motivo": estado.book_motivo, "niveis": list(estado.niveis),
                                  "em_dia": estado.em_dia, "atraso_base_ms": estado.atraso_base_ms,
                                  "atraso_ultimo_ms": estado.atraso_ultimo_ms,
                                  "gravadas": getattr(grav, "gravadas", 0), "descartadas": getattr(grav, "descartadas", 0),
                                  "gravacao_pausada": getattr(grav, "pausado", False)})

    app = web.Application()
    app.router.add_get("/precos/atual", atual)
    app.router.add_get("/saude", saude)
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

class Tokens:
    def __init__(self, arq):
        self.arq = arq
        self.endpoint = None
        self.t = self.ler()

    def ler(self):
        with open(self.arq) as f:
            return json.load(f)

    def guardar(self, t):
        t = dict(t, obtido_em=time.time())
        fd = os.open(self.arq, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as f:
            json.dump(t, f)
        self.t = t

    def atual(self):
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

async def fonte_ao_vivo(estado, args, grav):
    from pysignalr.client import SignalRClient

    tokens = Tokens(args.token)
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

        ren = asyncio.create_task(renovador())
        try:
            await cliente.run()
        except Exception as e:
            log.error("cliente terminou: %r; reconectando em 5 s", e)
        finally:
            ren.cancel()
        estado.marcar(conectado=False, motivo="cliente terminou")
        await asyncio.sleep(5)


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
    runner = web.AppRunner(aplicativo(estado, grav), access_log=None)  # o kalman-ordens consulta a cada 200 ms
    await runner.setup()
    await web.TCPSite(runner, "127.0.0.1", args.porta).start()
    log.info("pnt-md-hml: %s → http://127.0.0.1:%d/precos/atual?ativo=%s (%s)", args.ativo, args.porta, CONCEITO,
             "gravação " + args.simular if args.simular else "WB_MD " + args.hub)
    await (fonte_gravada(estado, args) if args.simular else fonte_ao_vivo(estado, args, grav))
    if args.simular and args.ficar:
        await asyncio.Event().wait()


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--ativo", default=os.environ.get("PNT_MD_ATIVO", "WINZ26"))
    ap.add_argument("--porta", type=int, default=int(os.environ.get("PNT_MD_PORTA", "9096")))
    ap.add_argument("--hub", default=os.environ.get("PNT_MD_HUB", HUB))
    ap.add_argument("--token", default=os.environ.get("PNT_MD_TOKEN", os.path.expanduser("~/.config/pnt-md-spike/token.json")))
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
    ap.add_argument("--simular", help="JSONL gravado pelo md_spike (fonte simulada, para teste)")
    ap.add_argument("--velocidade", type=float, default=1.0)
    ap.add_argument("--ficar", action="store_true", help="com --simular, continua servindo depois da gravação")
    args = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    asyncio.run(principal(args))


if __name__ == "__main__":
    main()
