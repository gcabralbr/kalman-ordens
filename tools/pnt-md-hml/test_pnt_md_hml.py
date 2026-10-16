"""Testes do pnt-md-hml: unidade (estado e contrato) e integração (o serviço com a gravação de 16/10).

  tools/pnt-md-hml/.venv/bin/python -m unittest -v tools/pnt-md-hml/test_pnt_md_hml.py
"""
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
