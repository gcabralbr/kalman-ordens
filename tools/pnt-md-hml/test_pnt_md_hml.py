"""Testes do pnt-md-hml: unidade (estado e contrato) e integração (o serviço com a gravação de 16/10).

  tools/pnt-md-hml/.venv/bin/python -m unittest -v tools/pnt-md-hml/test_pnt_md_hml.py
"""
import asyncio
import json
import os
import subprocess
import sys
import time
import unittest
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pnt_md_hml as m  # noqa: E402

RAIZ = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
GRAVACAO = os.path.join(RAIZ, "var", "pnt-md-WINZ26-20261016.jsonl")
FIXTURE = os.path.join(RAIZ, "cenarios", "referencia", "precos-atual-pnt-md-hml.json")

LT = {"Price": 209525.0, "Quantity": 1.0, "Time": "2026-10-15T18:07:12.111"}
BOOK = {"ListBids": [{"Price": 209520.0}], "ListOffers": [{"Price": 209525.0}]}


class Relogio:
    def __init__(self, t):
        self.t = t

    def __call__(self):
        return self.t


class TestEstado(unittest.TestCase):
    def novo(self):
        self.rel = Relogio(1792174033.0)  # 16/10 15:07:13 -03:00
        e = m.Estado("WINZ26", agora=self.rel)
        e.marcar(conectado=True, feed_ok=True)
        return e

    def test_sem_dado_vazio(self):
        self.assertEqual(self.novo().resposta(), {})

    def test_desconectado_vazio(self):
        e = m.Estado("WINZ26", agora=Relogio(1792174033.0))
        e.update("LastTrade", LT)
        self.assertEqual(e.resposta(), {})

    def test_contrato(self):
        e = self.novo()
        e.update("SecurityStatus", "Trading")
        e.update("AggrBook", BOOK)
        self.rel.t += 0.3
        e.update("LastTrade", LT)
        self.rel.t += 0.4
        r = e.resposta()
        self.assertEqual(list(r), ["WINFUT"])
        p = r["WINFUT"]
        self.assertEqual(set(p), {"ticker", "preco", "bid", "ask", "status84", "ts", "idade_seg"})
        self.assertEqual((p["ticker"], p["preco"], p["bid"], p["ask"], p["status84"]), ("WINZ26", 209525.0, 209520.0, 209525.0, 0))
        self.assertEqual(p["ts"], "2026-10-16T15:07:13-03:00")  # o mais fresco (o LastTrade), RFC3339
        self.assertEqual(p["idade_seg"], 0.4)

    def test_feed_indisponivel_e_volta(self):
        e = self.novo()
        e.update("SecurityStatus", "Trading")
        e.update("LastTrade", LT)
        e.marcar(feed_ok=False, motivo="OnFeederUnavailable")
        self.assertEqual(e.resposta(), {})
        e.marcar(feed_ok=True, motivo="OnFeederRecovered")
        self.assertIn("WINFUT", e.resposta())

    def test_token_invalido_vazio(self):
        e = self.novo()
        e.update("LastTrade", LT)
        e.marcar(token_ok=False, motivo="token")
        self.assertEqual(e.resposta(), {})

    def test_reconexao_limpa_o_preco(self):
        e = self.novo()
        e.update("LastTrade", LT)
        e.update("AggrBook", BOOK)
        e.marcar(conectado=False)
        e.marcar(conectado=True)
        self.assertEqual(e.resposta(), {})  # nada herdado da conexão anterior

    def test_dado_de_ontem_some(self):
        e = self.novo()
        e.update("LastTrade", LT)
        self.rel.t += 24 * 3600
        self.assertEqual(e.resposta(), {})

    def test_sem_status_nao_negociavel(self):
        e = self.novo()
        e.update("LastTrade", LT)
        self.assertEqual(e.resposta()["WINFUT"]["status84"], 1)

    def test_status84(self):
        self.assertEqual(m.status84("Trading"), 0)
        self.assertEqual(m.status84("Auction"), 1)
        self.assertEqual(m.status84({"SecurityTradingStatus": 17}), 0)
        self.assertEqual(m.status84({"SecurityTradingStatus": 21}), 21)
        self.assertEqual(m.status84(None), 1)

    def test_so_book_sem_preco(self):
        # leilão: bid/ask sem preço (o kalman-ordens recusa com SEM_PRECO/LEILAO)
        e = self.novo()
        e.update("SecurityStatus", {"SecurityTradingStatus": 21})
        e.update("AggrBook", BOOK)
        p = e.resposta()["WINFUT"]
        self.assertNotIn("preco", p)
        self.assertEqual(p["status84"], 21)


class TestToken(unittest.TestCase):
    """Dois processos no mesmo arquivo de token: o que vê o próprio expirado relê o arquivo antes de
    renovar (o outro pode ter girado o refresh token)."""

    def test_usa_o_renovado_por_outro(self):
        import tempfile
        d = tempfile.mkdtemp()
        arq = os.path.join(d, "token.json")
        velho = {"access_token": "VELHO", "refresh_token": "R1", "expires_in": 3600, "obtido_em": time.time() - 4000}
        json.dump(velho, open(arq, "w"))
        t = m.Tokens(arq)
        novo = dict(velho, access_token="NOVO", refresh_token="R2", obtido_em=time.time())
        json.dump(novo, open(arq, "w"))
        t.renovar = lambda: self.fail("não devia renovar: o arquivo já tem um token válido")
        self.assertEqual(t.atual(), "NOVO")


class TestGravador(unittest.TestCase):
    """O JSONL bruto: formato do md_spike, append, um arquivo por dia; erro de escrita não derruba."""

    def esperar(self, g, n):
        fim = time.time() + 5
        while g.gravadas < n and time.time() < fim:
            time.sleep(0.01)
        self.assertEqual(g.gravadas, n)

    def test_formato_append_e_dia(self):
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            g = m.Gravador(d, "WINZ26")
            lt = {"Price": 209500.0, "Time": "2026-10-16T12:00:00.123", "TradeID": "1"}
            t1 = 1792411300.5  # 19/10 09:01:40,5 (-03)
            g.gravar("_conectado", [], t1)
            g.gravar("OnUpdateMarketData", ["WINZ26", "LastTrade", lt], t1 + 0.1)
            g.gravar("OnUpdateMarketData", ["WINZ26", "SecurityStatus", "Trading"], t1 + 0.2)
            g.gravar("OnUpdateMarketData", ["WINZ26", "LastTrade", lt], t1 + 86400)  # dia seguinte
            self.esperar(g, 4)
            g2 = m.Gravador(d, "WINZ26")  # outro processo (restart): append no mesmo arquivo
            g2.gravar("_conectado", [], t1 + 60)
            self.esperar(g2, 1)
            regs = [json.loads(l) for l in open(os.path.join(d, "pnt-md-hml-WINZ26-20261019.jsonl"))]
            self.assertEqual([r["evento"] for r in regs], ["_conectado", "OnUpdateMarketData", "OnUpdateMarketData", "_conectado"])
            self.assertEqual(regs[1]["args"][2], lt)
            self.assertEqual(regs[1]["chegada_ms"], int((t1 + 0.1) * 1000))
            self.assertTrue(regs[0]["chegada"].startswith("2026-10-19T09:01:40.500-03:00"))
            self.assertTrue(os.path.exists(os.path.join(d, "pnt-md-hml-WINZ26-20261020.jsonl")))

    def test_erro_de_escrita_nao_derruba(self):
        g = m.Gravador("/nao/existe", "WINZ26")
        g.gravar("_conectado", [])
        fim = time.time() + 5
        while not g.falhou and time.time() < fim:
            time.sleep(0.01)
        self.assertTrue(g.falhou)
        self.assertEqual(g.gravadas, 0)

    def test_fila_cheia_descarta(self):
        import queue
        g = m.Gravador.__new__(m.Gravador)  # sem a thread de escrita: a fila não esvazia
        g.fila, g.descartadas = queue.Queue(maxsize=2), 0
        for _ in range(5):
            g.gravar("_conectado", [])
        self.assertEqual(g.descartadas, 3)


class TestAoVivoGrava(unittest.TestCase):
    """O laço ao vivo com um cliente SignalR falso: o que chega vai para o Estado e para o JSONL."""

    def test_laco(self):
        import tempfile
        import types
        lt = {"Price": 209500.0, "Time": "2026-10-16T12:00:00.123", "TradeID": "77"}
        livro = {"ListBids": [{"Price": 209495.0}], "ListOffers": [{"Price": 209500.0}]}

        class Falso:
            def __init__(self, *a, **k):
                self.h = {}

            def on(self, ev, f):
                self.h[ev] = f

            def on_open(self, f):
                self.aberto = f

            def on_close(self, f):
                self.fechado = f

            def on_error(self, f):
                self.erro = f

            async def send(self, *a):
                pass

            async def run(self):
                await self.aberto()
                await self.h["OnUpdateMarketData"](["WINZ26", "LastTrade", lt])
                await self.h["OnUpdateMarketData"](["WINZ26", "AggrBook", livro])
                await self.h["OnUpdateMarketData"](["WINZ26", "SecurityStatus", "Trading"])
                await self.h["OnFeederUnavailable"](["x"])
                await self.fechado()
                raise asyncio.CancelledError  # termina o laço do teste

        mod = types.ModuleType("pysignalr.client")
        mod.SignalRClient = Falso
        antigo = sys.modules.get("pysignalr.client")
        sys.modules["pysignalr.client"] = mod
        try:
            with tempfile.TemporaryDirectory() as d:
                arq = os.path.join(d, "token.json")
                json.dump({"access_token": "A", "refresh_token": "R", "expires_in": 3600, "obtido_em": time.time()}, open(arq, "w"))
                args = types.SimpleNamespace(token=arq, hub="x", ativo="WINZ26", max_mb=64)
                e = m.Estado("WINZ26")
                g = m.Gravador(d, "WINZ26")
                with self.assertRaises(asyncio.CancelledError):
                    asyncio.run(m.fonte_ao_vivo(e, args, g))
                fim = time.time() + 5
                while g.gravadas < 6 and time.time() < fim:
                    time.sleep(0.01)
                (nome,) = [n for n in os.listdir(d) if n.startswith("pnt-md-hml-WINZ26-")]
                regs = [json.loads(l) for l in open(os.path.join(d, nome))]
                self.assertEqual([r["evento"] for r in regs], ["_conectado", "OnUpdateMarketData", "OnUpdateMarketData",
                                                               "OnUpdateMarketData", "OnFeederUnavailable", "_desconectado"])
                self.assertEqual(regs[1]["args"], ["WINZ26", "LastTrade", lt])
                self.assertEqual(e.status84, 0)
        finally:
            if antigo is not None:
                sys.modules["pysignalr.client"] = antigo
            else:
                del sys.modules["pysignalr.client"]


class TestAtivo(unittest.TestCase):
    """?ativo= opcional (WINFUT quando ausente, o pedido do Kalman); outro ativo continua 400."""

    def test_ativo(self):
        from aiohttp import web

        agora = time.time()
        e = m.Estado("WINZ26", agora=lambda: agora)
        e.marcar(conectado=True)
        e.update("LastTrade", {"Price": 209500.0}, chegada=agora - 1)
        e.update("AggrBook", {"ListBids": [{"Price": 209495.0}], "ListOffers": [{"Price": 209500.0}]}, chegada=agora - 1)
        e.update("SecurityStatus", "Trading")

        async def rodar():
            runner = web.AppRunner(m.aplicativo(e))
            await runner.setup()
            site = web.TCPSite(runner, "127.0.0.1", 0)
            await site.start()
            porta = site._server.sockets[0].getsockname()[1]
            base = f"http://127.0.0.1:{porta}/precos/atual"
            loop = asyncio.get_running_loop()

            def get(url):
                try:
                    with urllib.request.urlopen(url, timeout=2) as r:
                        return r.status, json.loads(r.read())
                except urllib.error.HTTPError as err:
                    return err.code, None

            try:
                return [await loop.run_in_executor(None, get, base + q) for q in ("", "?ativo=WINFUT", "?ativo=WDOFUT", "?ativo=")]
            finally:
                await runner.cleanup()

        sem, com, outro, vazio = asyncio.run(rodar())
        self.assertEqual(sem, com)
        self.assertEqual(sem[0], 200)
        self.assertEqual(sem[1]["WINFUT"]["preco"], 209500.0)
        self.assertEqual(outro[0], 400)
        self.assertEqual(vazio[0], 400)


@unittest.skipUnless(os.path.exists(GRAVACAO), "sem a gravação de 16/10")
class TestComGravacao(unittest.TestCase):
    """O serviço com a gravação de 16/10 como fonte (×20), consultado por HTTP durante a reprodução."""

    def test_reproducao(self):
        porta = 19096
        p = subprocess.Popen([sys.executable, os.path.join(os.path.dirname(__file__), "pnt_md_hml.py"),
                              "--simular", GRAVACAO, "--velocidade", "20", "--porta", str(porta), "--ficar"],
                             stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        try:
            url = f"http://127.0.0.1:{porta}/precos/atual?ativo=WINFUT"
            respostas, inicio = [], time.time()
            while time.time() - inicio < 15:
                try:
                    with urllib.request.urlopen(url, timeout=2) as r:
                        respostas.append(json.loads(r.read()))
                except OSError:
                    pass
                time.sleep(0.2)
            cheias = [r["WINFUT"] for r in respostas if r]
            self.assertGreater(len(cheias), 30, "o serviço não serviu preço durante a gravação")
            for c in cheias:
                self.assertEqual(c["ticker"], "WINZ26")
                self.assertEqual(c["status84"], 0)
                self.assertLess(c["bid"], c["ask"])
                self.assertLessEqual(abs((c["bid"] + c["ask"]) / 2 - c["preco"]), 150)
                self.assertLessEqual(c["idade_seg"], 3)
            # a gravação terminou: fonte fechada, {}
            self.assertEqual(respostas[-1], {})
            with urllib.request.urlopen(f"http://127.0.0.1:{porta}/precos/atual?ativo=OUTRO", timeout=2) as r:
                self.fail("ativo desconhecido devia dar 400")
        except urllib.error.HTTPError as e:
            self.assertEqual(e.code, 400)
            # a fixture do contrato (lida pelo preco.Interpretar do kalman-ordens no teste Go)
            with open(FIXTURE, "w") as f:
                json.dump({"WINFUT": cheias[len(cheias) // 2]}, f, ensure_ascii=False)
                f.write("\n")
        finally:
            p.terminate()
            p.wait(5)


if __name__ == "__main__":
    unittest.main()
