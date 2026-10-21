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

from aiohttp import web

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pnt_md_hml as m  # noqa: E402

RAIZ = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
# as gravações ficam no var/ do checkout em uso (num worktree limpo: PNT_MD_TESTE_VAR=<repo>/var)
VAR = os.environ.get("PNT_MD_TESTE_VAR", os.path.join(RAIZ, "var"))
GRAVACAO = os.path.join(VAR, "pnt-md-WINZ26-20261016.jsonl")
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


class TestTokenAtomico(unittest.TestCase):
    """20/10: com o /home cheio, o O_TRUNC + json.dump deixaria o token vazio (refresh token perdido)."""

    def test_falha_no_meio_mantem_o_antigo(self):
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            arq = os.path.join(d, "token.json")
            m.gravar_atomico(arq, {"access_token": "A1", "refresh_token": "R1"})
            with self.assertRaises(TypeError):
                m.gravar_atomico(arq, {"access_token": object()})  # falha no meio do json.dump
            self.assertEqual(json.load(open(arq))["refresh_token"], "R1")
            self.assertEqual(os.listdir(d), ["token.json"])  # sem temporário largado
            self.assertEqual(os.stat(arq).st_mode & 0o777, 0o600)

    def test_disco_cheio_na_renovacao(self):
        import errno
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            arq = os.path.join(d, "token.json")
            m.gravar_atomico(arq, {"access_token": "A1", "refresh_token": "R1", "expires_in": 3600, "obtido_em": time.time()})
            t = m.Tokens(arq)
            original, falhas = m.gravar_atomico, [1]

            def cheio(c, dados):
                if falhas:
                    falhas.pop()
                    raise OSError(errno.ENOSPC, "No space left on device")
                original(c, dados)
            m.gravar_atomico = cheio
            try:
                t.guardar({"access_token": "A2", "refresh_token": "R2", "expires_in": 3600})
                self.assertEqual(t.t["refresh_token"], "R2")  # na memória
                self.assertEqual(json.load(open(arq))["refresh_token"], "R1")  # o arquivo antigo intacto
                self.assertEqual(t.atual(), "A2")  # e a próxima leitura grava o pendente
                self.assertEqual(json.load(open(arq))["refresh_token"], "R2")
                self.assertFalse(t.pendente)
            finally:
                m.gravar_atomico = original


class TestAoVivoGrava(unittest.TestCase):
    """O laço ao vivo com um cliente SignalR falso: o que chega vai para o Estado e para o JSONL."""

    def test_laco(self):
        import tempfile
        import types
        lt = {"Price": 209500.0, "Time": "2026-10-16T12:00:00.123", "TradeID": "77"}
        livro = {"ListBids": [{"Price": 209495.0}], "ListOffers": [{"Price": 209500.0}]}

        class Falso:
            n = 0

            def __init__(self, *a, **k):
                self.h = {}
                Falso.n += 1

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
                if Falso.n > 1:
                    raise asyncio.CancelledError  # o cliente novo depois do fechamento: termina o teste
                await self.aberto()
                await self.h["OnUpdateMarketData"](["WINZ26", "LastTrade", lt])
                await self.h["OnUpdateMarketData"](["WINZ26", "AggrBook", livro])
                await self.h["OnUpdateMarketData"](["WINZ26", "SecurityStatus", "Trading"])
                await self.h["OnFeederUnavailable"](["x"])
                await self.fechado()  # levanta ConexaoEncerrada: o laço abre um cliente novo

        mod = types.ModuleType("pysignalr.client")
        mod.SignalRClient = Falso
        antigo = sys.modules.get("pysignalr.client")
        sys.modules["pysignalr.client"] = mod
        try:
            with tempfile.TemporaryDirectory() as d:
                arq = os.path.join(d, "token.json")
                json.dump({"access_token": "A", "refresh_token": "R", "expires_in": 3600, "obtido_em": time.time()}, open(arq, "w"))
                args = types.SimpleNamespace(token=arq, hub="x", ativo="WINZ26", max_mb=64, gravar_niveis=5, niveis_max=50,
                                             espera_reconexao_s=0, fechar_timeout_s=1)
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


AGORA = time.time()
AMOSTRA_20 = "/var/tmp/pnt-md-hml-amostra-20261020.jsonl.gz"
GRAVACAO_19 = os.path.join(VAR, "pnt-md-hml-WINZ26-20261019.jsonl.gz")
GRAVACAO_21 = os.path.join(VAR, "pnt-md-hml-WINZ26-20261021.jsonl")


def nivel(px, q=1):
    return {"Price": px, "Quantity": q, "NumOrders": 1, "OrderID": "", "Sender": ""}


def livro(bids, offers, **k):
    return dict({"Aggregated": True, "MarketDepth": 10, "ListBids": [nivel(x) for x in bids], "ListOffers": [nivel(x) for x in offers]}, **k)


class TestBook(unittest.TestCase):
    """v1.3 do pnt-md-hml (20/10): topo calculado e book degenerado = fonte indisponível."""

    def test_topo_calculado_fora_de_ordem(self):
        motivo, bid, ask, _ = m.avaliar_book(livro([209490, 209495, 209480], [209510, 209500]), 50, True)
        self.assertEqual((motivo, bid, ask), ("", 209495, 209500))

    def test_degenerados(self):
        casos = [
            (livro([1], [2], Aggregated=False), "Aggregated=false"),
            (livro([1], [2], MarketDepth=0), "MarketDepth=0"),
            (livro(list(range(100, 151)), [200]), "níveis"),
            (livro([209495, 209495], [209500]), "repetido"),
            (livro([209500], [209495]), "cruzado"),
        ]
        for p, esperado in casos:
            motivo, bid, ask, _ = m.avaliar_book(p, 50, True)
            self.assertIn(esperado, motivo)
            self.assertIsNone(bid)

    def test_cruzado_no_leilao_nao_conta(self):
        motivo, bid, ask, _ = m.avaliar_book(livro([229330], [187640]), 50, False)
        self.assertEqual((motivo, bid, ask), ("", 229330, 187640))

    def test_estado_indisponivel_e_volta(self):
        e = m.Estado("WINZ26", agora=lambda: AGORA)
        e.marcar(conectado=True)
        e.update("SecurityStatus", "Trading", AGORA)
        e.update("LastTrade", {"Price": 209500.0}, AGORA)
        e.update("AggrBook", livro([209495], [209500], Aggregated=False), AGORA)
        self.assertEqual(e.resposta(), {})
        self.assertFalse(e.book_ok)
        e.update("AggrBook", livro([209495], [209500]), AGORA)
        self.assertEqual(e.resposta()["WINFUT"]["bid"], 209495)


class TestAtraso(unittest.TestCase):
    """Atraso relativo à base (mínimo móvel de chegada − envio do servidor): o relógio do PnT pode estar
    ±2 s fora; só o excesso sobre a base conta."""

    def test_base_e_excesso(self):
        e = m.Estado("WINZ26", agora=lambda: AGORA)
        e.marcar(conectado=True)
        # o relógio do PnT 1,9 s adiantado: chegada − envio = −1.900 ms, estável
        for i in range(10):
            e.update("LastTrade", {"Price": 1.0}, AGORA + i, (AGORA + i) * 1000 + 1900)
        self.assertTrue(e.disponivel())
        self.assertAlmostEqual(e.atraso_base_ms, -1900, places=0)
        # o cliente atrasa 3 s: indisponível
        e.update("LastTrade", {"Price": 1.0}, AGORA + 20, (AGORA + 17) * 1000 + 1900)
        self.assertFalse(e.disponivel())
        self.assertIn("atrás da base", e.motivo)
        # volta ao normal
        e.update("LastTrade", {"Price": 1.0}, AGORA + 21, (AGORA + 21) * 1000 + 1900 - 300)
        self.assertTrue(e.disponivel())

    def test_janela_esquece_a_base_antiga(self):
        e = m.Estado("WINZ26", agora=lambda: AGORA, janela_base_s=60)
        e.marcar(conectado=True)
        e.update("LastTrade", {"Price": 1.0}, AGORA, AGORA * 1000 - 500)  # base 500
        e.update("LastTrade", {"Price": 1.0}, AGORA + 120, (AGORA + 120) * 1000 - 800)  # a de 500 saiu da janela
        self.assertAlmostEqual(e.atraso_base_ms, 800, places=0)


class TestGravadorV13(unittest.TestCase):
    def test_guarda_de_disco(self):
        import tempfile
        livre = [10 * 2**30]
        with tempfile.TemporaryDirectory() as d:
            g = m.Gravador(d, "WINZ26", disco_min_gb=3, livre=lambda: livre[0], iniciar=False)
            g._uma(time.time(), "_conectado", [])
            self.assertEqual(g.gravadas, 1)
            livre[0] = 2 * 2**30
            g.disco_visto_em = 0
            g._uma(time.time(), "_conectado", [])
            self.assertTrue(g.pausado)
            self.assertEqual((g.gravadas, g.descartadas), (1, 1))
            livre[0] = 3.5 * 2**30  # acima do mínimo, mas não 1 GB acima: continua pausada
            g.disco_visto_em = 0
            g._uma(time.time(), "_conectado", [])
            self.assertTrue(g.pausado)
            livre[0] = 5 * 2**30
            g.disco_visto_em = 0
            g._uma(time.time(), "_conectado", [])
            self.assertFalse(g.pausado)
            self.assertEqual(g.gravadas, 2)

    def test_erro_de_escrita_espera_10s(self):
        g = m.Gravador("/nao/existe", "WINZ26", iniciar=False)
        g._uma(time.time(), "_conectado", [])
        self.assertTrue(g.falhou)
        self.assertGreater(g.tentar_em, time.time() + 9)
        g._uma(time.time(), "_conectado", [])  # dentro dos 10 s: nem tenta
        self.assertEqual(g.descartadas, 2)

    def test_topo_book_reduz_e_ordena(self):
        t = m.topo_book(livro([3, 5, 4, 1, 2, 6], [9, 7, 8]), 2)
        self.assertEqual([x["Price"] for x in t["ListBids"]], [6, 5])
        self.assertEqual([x["Price"] for x in t["ListOffers"]], [7, 8])
        self.assertEqual(t["Niveis"], [6, 3])
        r = m.topo_book(livro(list(range(100)), [200]), 5, 50)
        self.assertEqual(r, {"Aggregated": True, "MarketDepth": 10, "Niveis": [100, 1], "Degenerado": True})


def reproduzir(arq, limite=None, rec=None, ate=None):
    """Passa uma gravação pelo Estado com os horários gravados; devolve (estado, respostas). Com rec
    (Reconexao), a vigia é consultada a cada mensagem e as reconexões pedidas vão para rec.pedidas (a
    gravação não reconecta: só o _conectado gravado abre uma conexão nova). ate: "HH:MM:SS" de parada."""
    import gzip
    import itertools
    agora = [0.0]
    e = m.Estado("WINZ26", agora=lambda: agora[0])
    respostas = []
    abrir = gzip.open if arq.endswith(".gz") else open
    for l in itertools.islice(abrir(arq, "rt"), limite):
        try:
            r = json.loads(l)
        except ValueError:
            continue  # a última linha da amostra está cortada
        if ate and r["chegada"][11:19] >= ate:
            break
        agora[0] = r["chegada_ms"] / 1000
        if rec is not None:
            motivo = rec.observar(agora[0], e)
            if motivo:
                rec.registrar(agora[0], motivo)
                rec.pedidas.append(r["chegada"][11:19])
        if r["evento"] == "_conectado":
            e.marcar(conectado=False, motivo="nova conexão")  # a gravação não tem o _desconectado
            e.marcar(conectado=True, feed_ok=True, motivo="conectado")
        elif r["evento"] == "OnUpdateMarketData":
            a = r["args"]
            e.update(a[1], a[2], agora[0], a[3] if len(a) >= 4 else None)
            respostas.append((r["chegada"][11:23], e.resposta(), e.em_dia, e.book_ok))
    return e, respostas


@unittest.skipUnless(os.path.exists(AMOSTRA_20), "sem a amostra de 20/10")
class TestAmostra20(unittest.TestCase):
    """A amostra de 20/10 (08:39–09:01:24): book degenerado desde 08:55 e o atraso de 3–11 s depois da
    abertura — o /precos/atual não serve preço, em vez de servir o book cruzado e velho."""

    @classmethod
    def setUpClass(cls):
        cls.e, cls.r = reproduzir(AMOSTRA_20)

    def test_book_degenerado(self):
        depois = [x for x in self.r if x[0] >= "08:55:01"]
        self.assertTrue(depois)
        self.assertFalse(any(x[3] for x in depois), "algum AggrBook de 20/10 foi aceito")
        self.assertTrue(all(x[1] == {} for x in depois))

    def test_nunca_serve_book_cruzado(self):
        for _, resp, _, _ in self.r:
            p = resp.get("WINFUT", {})
            if "bid" in p and "ask" in p and p.get("status84") == 0:
                self.assertLess(p["bid"], p["ask"])

    def test_atraso_detectado(self):
        self.assertTrue(any(not x[2] for x in self.r if x[0] >= "09:01:00"), "o atraso de 3–11 s não foi visto")
        self.assertFalse(any(not x[2] for x in self.r if x[0] < "09:00:50"))


@unittest.skipUnless(os.path.exists(GRAVACAO_19), "sem a gravação de 19/10")
class TestGravacao19(unittest.TestCase):
    """19/10 (book agregado, 10 níveis, sem atraso): a fonte fica disponível e o topo nunca cruza."""

    def test_disponivel(self):
        e, r = reproduzir(GRAVACAO_19, 60000)
        com_preco = [x for x in r if x[1]]
        self.assertGreater(len(com_preco), 0.99 * len(r))
        self.assertTrue(all(x[2] for x in r), "atraso acusado em 19/10")
        # 19/10 09:13:08.988: um book travado (209.010 / 209.010) numa mensagem — indisponível só ali
        self.assertLessEqual(sum(1 for x in r if not x[3]), len(r) // 1000)
        for _, resp, _, _ in com_preco:
            p = resp["WINFUT"]
            if "bid" in p and "ask" in p:
                self.assertLess(p["bid"], p["ask"])



def com_pedidas(**k):
    r = m.Reconexao(**k)
    r.pedidas = []
    return r


class TestReconexao(unittest.TestCase):
    """A decisão de reconectar: book degenerado nesta conexão, em negociação, por mais de 10 s; no
    máximo uma a cada 60 s; alerta depois de 5 seguidas sem book normal."""

    def novo(self):
        self.rel = Relogio(1792584000.0)
        e = m.Estado("WINZ26", agora=self.rel)
        e.marcar(conectado=True, feed_ok=True)
        return e

    def test_so_em_negociacao(self):
        e, r = self.novo(), m.Reconexao()
        e.update("SecurityStatus", "Pre-Open")
        e.update("AggrBook", livro([1, 2], [3], Aggregated=False))
        for t in range(0, 30):
            self.assertIsNone(r.observar(1000.0 + t, e))
        e.update("SecurityStatus", "Trading")  # a contagem começa aqui, não no primeiro book degenerado
        self.assertIsNone(r.observar(1030.0, e))
        self.assertIsNone(r.observar(1039.9, e))
        self.assertIn("book degenerado em negociação", r.observar(1040.0, e))

    def test_book_normal_zera_a_contagem(self):
        e, r = self.novo(), m.Reconexao()
        e.update("SecurityStatus", "Trading")
        e.update("AggrBook", livro([1], [3], Aggregated=False))
        r.observar(1000.0, e)
        e.update("AggrBook", livro([1], [3]))  # um book normal no meio
        self.assertIsNone(r.observar(1005.0, e))
        e.update("AggrBook", livro([1], [3], Aggregated=False))
        self.assertIsNone(r.observar(1006.0, e))
        self.assertIsNone(r.observar(1015.0, e))
        self.assertIsNotNone(r.observar(1016.0, e))

    def test_intervalo_e_alerta(self):
        e, r = self.novo(), m.Reconexao()
        e.update("SecurityStatus", "Trading")
        feitas = []
        for t in range(0, 400):
            # cada reconexão é uma conexão nova que também vem degenerada
            if feitas and feitas[-1] == t - 6:
                e.marcar(conectado=True, feed_ok=True)
                e.update("AggrBook", livro([1], [3], Aggregated=False))
            elif not feitas:
                e.update("AggrBook", livro([1], [3], Aggregated=False))
            motivo = r.observar(1000.0 + t, e)
            if motivo:
                r.registrar(1000.0 + t, motivo)
                feitas.append(t)
                e.marcar(conectado=False, motivo="reconexão")
        self.assertEqual(feitas[0], 10)
        self.assertTrue(all(b - a >= 60 for a, b in zip(feitas, feitas[1:])), feitas)
        self.assertEqual(len(feitas), 7)
        self.assertTrue(r.saude()["reconexao_alerta"])
        self.assertEqual(r.saude()["reconexoes_book"], 7)
        e.marcar(conectado=True, feed_ok=True)
        e.update("AggrBook", livro([1], [3]))
        r.observar(1500.0, e)
        self.assertEqual(r.saude()["reconexoes_seguidas"], 0)
        self.assertFalse(r.saude()["reconexao_alerta"])
        self.assertEqual(r.saude()["reconexoes_book"], 7)

    def test_pedido_manual(self):
        e, r = self.novo(), m.Reconexao()
        r.pedido = "manual"
        self.assertEqual(r.observar(1000.0, e), "manual")  # sem limiar, sem book degenerado
        self.assertIsNone(r.observar(1000.1, e))

    def test_desconectado_nao_conta(self):
        e, r = self.novo(), m.Reconexao()
        e.update("SecurityStatus", "Trading")
        e.update("AggrBook", livro([1], [3], Aggregated=False))
        e.marcar(conectado=False, motivo="caiu")
        e.marcar(conectado=True, feed_ok=True)  # conexão nova sem AggrBook ainda
        for t in range(30):
            self.assertIsNone(r.observar(1000.0 + t, e))


@unittest.skipUnless(os.path.exists(AMOSTRA_20), "sem a amostra de 20/10")
class TestReconexao20(unittest.TestCase):
    def test_pede_depois_da_abertura(self):
        r = com_pedidas()
        reproduzir(AMOSTRA_20, rec=r)
        # Trading às 09:00:51.438; a amostra vai até 09:01:24: uma reconexão ~10 s depois
        self.assertEqual(r.pedidas, ["09:01:01"])


@unittest.skipUnless(os.path.exists(GRAVACAO_21), "sem a gravação de 21/10")
class TestReconexao21(unittest.TestCase):
    def test_pede_depois_do_trading_e_para_com_a_conexao_nova(self):
        r = com_pedidas()
        reproduzir(GRAVACAO_21, rec=r, ate="09:06:00")
        # Trading às 09:00:32.979; a conexão nova das 09:02:27 vem normal
        self.assertEqual(r.pedidas[0][:7], "09:00:4")
        self.assertTrue(all(p < "09:02:27" for p in r.pedidas), r.pedidas)
        self.assertEqual(len(r.pedidas), 2)  # 09:00:4x e 09:01:4x
        self.assertEqual(r.saude()["reconexoes_seguidas"], 0)


@unittest.skipUnless(os.path.exists(GRAVACAO_19), "sem a gravação de 19/10")
class TestReconexao19(unittest.TestCase):
    def test_nenhuma(self):
        r = com_pedidas()
        reproduzir(GRAVACAO_19, 60000, rec=r)
        self.assertEqual(r.pedidas, [])



class TestReconexaoComServidor(unittest.TestCase):
    """O pysignalr de verdade contra um /mdhub falso (aiohttp, local): a reconexão automática manda o
    close frame (o servidor recebe o 1000), e toda conexão nova passa por um negotiate novo
    (connectionId novo) — também quando é o servidor que fecha (sem a reconexão interna do pysignalr,
    que reusaria o ?id= antigo)."""

    def rodar(self, servidor_fecha, limiar_s=0.2):
        import tempfile
        import types
        from aiohttp import WSMsgType
        sep = "\x1e"
        vistos = {"negotiate": 0, "ids": [], "close": []}

        async def negotiate(req):
            vistos["negotiate"] += 1
            return web.json_response({"connectionId": f"c{vistos['negotiate']}", "negotiateVersion": 0,
                                      "availableTransports": [{"transport": "WebSockets", "transferFormats": ["Text"]}]})

        async def hub(req):
            ws = web.WebSocketResponse()
            await ws.prepare(req)
            n = len(vistos["ids"])
            vistos["ids"].append(req.query.get("id"))
            await ws.receive()  # handshake do SignalR
            await ws.send_str("{}" + sep)

            def md(*a):
                return json.dumps({"type": 1, "target": "OnUpdateMarketData", "arguments": list(a)}) + sep

            await ws.send_str(md("WINZ26", "SecurityStatus", "Trading"))
            degenerado = n == 0
            book = livro([1, 2], [3], Aggregated=False) if degenerado else livro([1, 2], [3])

            async def enviar():
                for i in range(200):
                    if ws.closed:
                        return
                    await ws.send_str(md("WINZ26", "AggrBook", book))
                    await asyncio.sleep(0.05)
                    if servidor_fecha and n == 0 and i == 5:
                        await ws.close(code=1001)
                        return
            tarefa = asyncio.create_task(enviar())
            async for msg in ws:
                if msg.type == WSMsgType.CLOSE:
                    break
            if ws.close_code is not None and not (servidor_fecha and n == 0):
                vistos["close"].append((n, ws.close_code))
            tarefa.cancel()
            return ws

        async def cenario():
            app = web.Application()
            app.router.add_post("/mdhub/negotiate", negotiate)
            app.router.add_get("/mdhub", hub)
            runner = web.AppRunner(app)
            await runner.setup()
            site = web.TCPSite(runner, "127.0.0.1", 0)
            await site.start()
            porta = site._server.sockets[0].getsockname()[1]
            with tempfile.TemporaryDirectory() as d:
                arq = os.path.join(d, "token.json")
                json.dump({"access_token": "A", "refresh_token": "R", "expires_in": 3600, "obtido_em": time.time()}, open(arq, "w"))
                args = types.SimpleNamespace(token=arq, hub=f"http://127.0.0.1:{porta}/mdhub", ativo="WINZ26", max_mb=64,
                                             gravar_niveis=5, niveis_max=50, espera_reconexao_s=0, fechar_timeout_s=2,
                                             vigia_s=0.05)
                e = m.Estado("WINZ26")
                rec = m.Reconexao(limiar_s=limiar_s, intervalo_s=1.0, alerta_apos=5)
                t = asyncio.create_task(m.fonte_ao_vivo(e, args, m.SemGravador(), rec))
                fim = time.time() + 15
                while time.time() < fim and not (len(vistos["ids"]) >= 2 and e.book_ok and e.conectado and e.bid):
                    await asyncio.sleep(0.05)
                vistos["pronto_em"] = time.time()
                t.cancel()
                try:
                    await t
                except asyncio.CancelledError:
                    pass
            vistos["parado_em"] = time.time()
            await runner.cleanup()
            return e, rec

        return vistos, *asyncio.run(cenario())

    def test_reconexao_automatica_fecha_com_close_frame(self):
        vistos, e, rec = self.rodar(servidor_fecha=False)
        self.assertEqual(vistos["ids"][:2], ["c1", "c2"])  # negotiate novo, connectionId novo
        self.assertEqual(vistos["negotiate"], len(vistos["ids"]))
        self.assertEqual(vistos["close"][0], (0, 1000))  # o servidor recebeu o close da conexão antiga
        self.assertEqual(vistos["close"][1], (1, 1000))  # e o da conexão nova no fim do serviço (cancel)
        self.assertEqual(rec.total, 1)
        self.assertEqual(rec.ultima["close_servidor"]["code"], 1000)  # e o cliente recebeu o close de volta
        self.assertTrue(e.book_ok)
        rec.observar(time.time(), e)  # a vigia zera as seguidas no primeiro book normal
        self.assertEqual(rec.seguidas, 0)

    def test_servidor_fecha_cliente_novo(self):
        vistos, e, rec = self.rodar(servidor_fecha=True, limiar_s=30)
        self.assertEqual(vistos["ids"][:2], ["c1", "c2"])  # sem reusar o ?id=c1
        self.assertEqual(vistos["negotiate"], len(vistos["ids"]))
        self.assertEqual(rec.total, 0)


if __name__ == "__main__":
    unittest.main()
