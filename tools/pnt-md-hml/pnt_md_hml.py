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

Grava as mensagens brutas recebidas (cada LastTrade, AggrBook, SecurityStatus, e as quedas e voltas)
num JSONL diário no formato do md_spike, em append: <--gravar-dir>/pnt-md-hml-<ativo>-AAAAMMDD.jsonl
(o WB_MD aceita uma conexão por usuário: quem precisar do feed bruto lê daqui). A escrita é numa
thread à parte (fila); erro de escrita vai ao log e não derruba o serviço. --gravar-dir "" desliga.

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
import threading
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

    def __init__(self, ticker, agora=time.time):
        self.ticker, self.agora = ticker, agora
        self.preco = self.bid = self.ask = None
        self.preco_em = self.book_em = 0.0
        self.status84 = None  # sem SecurityStatus ainda: não negociável
        self.conectado = False
        self.feed_ok = True
        self.token_ok = True
        self.motivo = "sem conexão"

    def disponivel(self):
        return self.conectado and self.feed_ok and self.token_ok

    def marcar(self, conectado=None, feed_ok=None, token_ok=None, motivo=""):
        antes = self.disponivel()
        if conectado is not None:
            self.conectado = conectado
        if feed_ok is not None:
            self.feed_ok = feed_ok
        if token_ok is not None:
            self.token_ok = token_ok
        depois = self.disponivel()
        if antes and not depois:
            self.motivo = motivo
            log.warning("fonte INDISPONÍVEL (%s): /precos/atual responde {}", motivo)
        elif depois and not antes:
            log.warning("fonte de volta (%s): depois de '%s'", motivo, self.motivo)
            self.motivo = ""
        if not self.conectado:
            # reconexão: o book e o preço da conexão anterior não valem até chegarem de novo
            self.preco = self.bid = self.ask = None

    def update(self, topico, payload, chegada=None):
        chegada = chegada if chegada is not None else self.agora()
        if topico == "LastTrade" and isinstance(payload, dict) and payload.get("Price") is not None:
            self.preco, self.preco_em = float(payload["Price"]), chegada
        elif topico == "AggrBook" and isinstance(payload, dict):
            bids, offers = payload.get("ListBids") or [], payload.get("ListOffers") or []
            self.bid = float(bids[0]["Price"]) if bids and bids[0].get("Price") else None
            self.ask = float(offers[0]["Price"]) if offers and offers[0].get("Price") else None
            self.book_em = chegada
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
                                  "gravadas": getattr(grav, "gravadas", 0), "descartadas": getattr(grav, "descartadas", 0)})

    app = web.Application()
    app.router.add_get("/precos/atual", atual)
    app.router.add_get("/saude", saude)
    return app


# ------------------------------------------------------------------ gravação bruta

class Gravador:
    """JSONL diário (pelo dia da chegada), em append, escrito por uma thread: o laço de leitura só
    enfileira. Fila cheia (escrita travada) → descarta e conta; erro de escrita → log, segue."""

    def __init__(self, pasta, ativo, maximo=200_000):
        self.pasta, self.ativo = pasta, ativo
        self.fila = queue.Queue(maxsize=maximo)
        self.descartadas = self.gravadas = 0
        self.arq = self.dia = None
        self.falhou = False
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
            t, evento, dados = self.fila.get()
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
                    log.warning("gravação: de volta")
                    self.falhou = False
            except Exception as e:
                if not self.falhou:
                    log.error("gravação: falhou (%r); o serviço continua, as mensagens se perdem até voltar", e)
                    self.falhou = True
                self.arq, self.dia = None, None


class SemGravador:
    gravadas = descartadas = 0

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

        async def md(dados):
            chegada = time.time()
            grav.gravar("OnUpdateMarketData", dados, chegada)
            if len(dados) >= 3 and dados[0] == args.ativo:
                estado.update(dados[1], dados[2], chegada)

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
    regs = [json.loads(l) for l in open(args.simular)]
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
            estado.update(r["args"][1], r["args"][2])
    estado.marcar(conectado=False, motivo="gravação terminou")
    log.info("gravação terminou")


async def principal(args):
    estado = Estado(args.ativo)
    grav = Gravador(args.gravar_dir, args.ativo) if args.gravar_dir and not args.simular else SemGravador()
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
    ap.add_argument("--simular", help="JSONL gravado pelo md_spike (fonte simulada, para teste)")
    ap.add_argument("--velocidade", type=float, default=1.0)
    ap.add_argument("--ficar", action="store_true", help="com --simular, continua servindo depois da gravação")
    args = ap.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    asyncio.run(principal(args))


if __name__ == "__main__":
    main()
