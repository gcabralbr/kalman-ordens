#!/usr/bin/env python3
"""Medidas sobre uma gravação do md_spike (JSONL) — feitas para o pregão de 19/10/2026.

  medidas.py status  GRAVACAO                      # (a) SecurityStatus / status84 no leilão de abertura
  medidas.py fills   GRAVACAO [--uid 1450] [--fita CSV] [--desde HH:MM] [--ate HH:MM]
                                                   # (b) cada fill do uid contra o feed no mesmo instante
  medidas.py fita    GRAVACAO --fita CSV [--offsets 0,0.8,1]
                                                   # (c) book e negócio da HML contra a fita (tick file)

A fita é o tick file do Kalman do pregão que a HML reproduz (/var/lib/kalman/tick/winfut-AAAA-MM-DD.csv:
ts, preco_obs, bid, ask). Os fills vêm do journal do kalman-ordens (linhas FILL, com o horário do
serviço). Só leitura; não toca em nada.
"""
import argparse
import bisect
import csv
import datetime as dt
import json
import os
import re
import statistics
import subprocess
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "pnt-md-hml"))
from pnt_md_hml import status84  # noqa: E402  (o mesmo mapeamento do serviço)

FUSO = dt.timezone(dt.timedelta(hours=-3))


def hora(t):
    return dt.datetime.fromtimestamp(t, FUSO).strftime("%H:%M:%S.%f")[:-3]


def seg_do_dia(t):
    d = dt.datetime.fromtimestamp(t, FUSO)
    return d.hour * 3600 + d.minute * 60 + d.second + d.microsecond / 1e6


def ler_gravacao(arq):
    """Séries de (chegada, valor): negócios, book (bid, ask), status; e os eventos de conexão."""
    neg, book, sts, con = [], [], [], []
    for l in open(arq):
        r = json.loads(l)
        t = r["chegada_ms"] / 1000
        ev = r["evento"]
        if ev != "OnUpdateMarketData":
            con.append((t, ev, r.get("args")))
            continue
        _, top, p = r["args"][:3]
        if top == "LastTrade" and isinstance(p, dict) and p.get("Price") is not None:
            neg.append((t, float(p["Price"]), p.get("Time"), p.get("TradeID"), p.get("Quantity")))
        elif top == "AggrBook" and isinstance(p, dict):
            b, o = p.get("ListBids") or [], p.get("ListOffers") or []
            bid = float(b[0]["Price"]) if b and b[0].get("Price") else None
            ask = float(o[0]["Price"]) if o and o[0].get("Price") else None
            book.append((t, bid, ask))
        elif top == "SecurityStatus":
            sts.append((t, p))
    return neg, book, sts, con


def ultimo(serie, t, max_idade=None):
    """O último registro com chegada <= t (ou None; None também se mais velho que max_idade)."""
    i = bisect.bisect_right(serie._ts, t)
    if not i or (max_idade is not None and t - serie[i - 1][0] > max_idade):
        return None
    return serie[i - 1]


class Serie(list):
    def __init__(self, it):
        super().__init__(it)
        self._ts = [x[0] for x in self]


# ------------------------------------------------------------------ (a)

def cmd_status(a):
    neg, book, sts, con = ler_gravacao(a.gravacao)
    print(f"conexão: {len(con)} eventos")
    for t, ev, args in con:
        print(f"  {hora(t)} {ev} {json.dumps(args)[:120] if args else ''}")
    print(f"\nSecurityStatus: {len(sts)} mensagens (status84 = o que o pnt-md-hml serve)")
    for t, p in sts:
        print(f"  {hora(t)} status84={status84(p)}  {json.dumps(p, ensure_ascii=False)[:300]}")
    if neg:
        print(f"\nprimeiro negócio: {hora(neg[0][0])} preço {neg[0][1]:.0f} (Time {neg[0][2]})")
    if book:
        cruz = [x for x in book if x[1] and x[2] and x[1] >= x[2]]
        print(f"primeiro book: {hora(book[0][0])} bid {book[0][1]} ask {book[0][2]}; books cruzados/travados (bid >= ask): "
              f"{len(cruz)}" + (f", de {hora(cruz[0][0])} a {hora(cruz[-1][0])}" if cruz else ""))
    # o que o kalman-ordens teria visto: status84 em cada mudança, com negócio e book do momento
    neg_s, book_s = Serie(neg), Serie(book)
    print("\nlinha do tempo (cada status, com o último negócio e o topo do book naquele instante):")
    for t, p in sts:
        n, b = ultimo(neg_s, t), ultimo(book_s, t)
        print(f"  {hora(t)} status84={status84(p):>3}  último {n[1] if n else '-'}  bid/ask {b[1] if b else '-'}/{b[2] if b else '-'}")


# ------------------------------------------------------------------ (b)

RE_CLORD = re.compile(r"^KM-(?:[A-Z][A-Z0-9]{1,7}-)?([EPS])-(\d+)-\d+$")  # KM-E-… ou KM-<MODULO>-E-…


def clord(cl):
    """(papel, operacao_id) do ClOrdID, ou None."""
    m = RE_CLORD.match(cl or "")
    return (m.group(1), int(m.group(2))) if m else None


RE_LINHA = re.compile(r"^time=(\S+) level=\S+ msg=(\"[^\"]*\"|\S+)(.*)$")
RE_KV = re.compile(r'(\w+)=("(?:[^"\\]|\\.)*"|\S+)')


def journal(desde, ate, arq=None):
    if arq:
        linhas = open(arq).read().splitlines()
    else:
        cmd = ["journalctl", "-u", "kalman-ordens", "--no-pager", "-o", "cat", "--since", desde]
        if ate:
            cmd += ["--until", ate]
        linhas = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout.splitlines()
    for l in linhas:
        m = RE_LINHA.match(l)
        if not m:
            continue
        kv = {k: (v[1:-1].replace('\\"', '"') if v.startswith('"') else v) for k, v in RE_KV.findall(m.group(3))}
        yield dt.datetime.fromisoformat(m.group(1)).timestamp(), m.group(2).strip('"'), kv


def ler_fita(arq):
    """(segundo do dia, preco, bid, ask) do tick file."""
    out = []
    with open(arq) as f:
        for r in csv.DictReader(f):
            try:
                t = float(r["ts"])
            except (KeyError, ValueError):
                continue
            num = lambda k: float(r[k]) if r.get(k) not in (None, "") else None  # noqa: E731
            out.append((seg_do_dia(t), num("preco_obs"), num("bid"), num("ask")))
    out.sort()
    return Serie(out)


def offset_book(book, fita, offs=(-3, -2.5, -2, -1.5, -1, -0.5, 0, 0.5, 0.8, 1, 1.5, 2)):
    """O deslocamento (s) com o menor |meio HML − meio fita| por segundo: fita no instante t − off."""
    seg = {}
    for t, bid, ask in book:
        if bid and ask and bid < ask:
            seg[int(seg_do_dia(t))] = (bid + ask) / 2
    melhor = None
    for off in offs:
        d = [abs(m - (f[2] + f[3]) / 2) for s, m in seg.items()
             for f in [ultimo(fita, s + 0.999 - off, 2)] if f and f[2] and f[3]]
        if d and (melhor is None or statistics.mean(d) < melhor[0]):
            melhor = (statistics.mean(d), off)
    return melhor[1] if melhor else 0.8


def cmd_fills(a):
    neg, book, _, _ = ler_gravacao(a.gravacao)
    neg_s, book_s = Serie(neg), Serie(book)
    fita = ler_fita(a.fita) if a.fita else None
    off = a.offset if a.offset is not None else (offset_book(book, fita) if fita else 0.8)
    if fita:
        print(f"fita no instante do fill − {off} s ({'--offset' if a.offset is not None else 'o melhor pelo book'})")
    uid_op, stop_op, limite_ord = {}, {}, {}
    fills = []
    hoje = dt.datetime.fromtimestamp(neg[0][0], FUSO).date().isoformat() if neg else dt.date.today().isoformat()
    for t, msg, kv in journal(f"{hoje} {a.desde}", f"{hoje} {a.ate}" if a.ate else None, a.journal):
        if msg == "evento" and "dados" in kv:
            try:
                d = json.loads(kv["dados"])
            except ValueError:
                continue
            if "operacao_id" in d and "uid" in d:
                uid_op[d["operacao_id"]] = d["uid"]
        elif msg in ("ENTRADA decidida", "SAÍDA decidida", "SAIDA decidida") or msg.endswith("decidida"):
            if "clordid" in kv and ("preco" in kv or "limite" in kv):
                limite_ord[kv["clordid"]] = float(kv.get("limite", kv.get("preco")))
            if "stop" in kv and "clordid" in kv:
                c = clord(kv["clordid"])
                if c:
                    stop_op[c[1]] = float(kv["stop"])
        elif msg == "FILL":
            fills.append((t, kv))
    print(f"{'hora':12} {'clordid':22} {'lado':5} {'qtd':>3} {'fill':>7} {'lim':>7} | {'bid':>7} {'ask':>7} {'último':>7} "
          f"{'idade':>5} | {'fill−ref':>8}" + (f" | fita bid/ask (t−{off} s)  fill−ref" if fita else ""))
    entrada_op = {}
    for t, kv in fills:
        c = clord(kv.get("clordid"))
        if c and c[0] == "E":
            entrada_op.setdefault(c[1], float(kv["preco"]))
    difs = []
    for t, kv in fills:
        cl = kv.get("clordid", "")
        c = clord(cl)
        if not c:
            continue
        papel, op = c
        if a.uid and uid_op.get(op) != a.uid:
            continue
        px = float(kv["preco"])
        # entrada vendida tem o stop acima do preço de entrada; saída e proteção são do lado oposto
        stop, ent = stop_op.get(op), entrada_op.get(op)
        if stop is None or ent is None:
            lado = "?"
        else:
            vende = (stop > ent) == (papel == "E")
            lado = "V" if vende else "C"
        n, b = ultimo(neg_s, t), ultimo(book_s, t)
        bid, ask = (b[1], b[2]) if b else (None, None)
        ref = ask if lado == "C" else bid if lado == "V" else None
        dif = (px - ref) if ref is not None and lado == "C" else (ref - px) if ref is not None else None
        if dif is not None:
            difs.append(dif)
        lin = (f"{hora(t):12} {cl:22} {lado:5} {kv.get('delta', ''):>3} {px:7.0f} {limite_ord.get(cl, float('nan')):7.0f} | "
               f"{bid or 0:7.0f} {ask or 0:7.0f} {n[1] if n else 0:7.0f} {(t - b[0]) if b else 0:5.1f} | "
               f"{dif if dif is not None else float('nan'):+8.0f}")
        if fita:
            f = ultimo(fita, seg_do_dia(t) - off, 2)
            if f and f[2] and f[3]:
                fr = f[3] if lado == "C" else f[2]
                fd = (px - fr) if lado == "C" else (fr - px)
                lin += f" | {f[2]:7.0f}/{f[3]:7.0f}  {fd:+6.0f}"
            else:
                lin += " | sem fita (o tick file vai até ~14:00): só contra o feed"
        print(lin)
    print("\nfill−ref: compra = fill − ask, venda = bid − fill (positivo = pior que o topo do book; negativo = melhor)")
    if difs:
        print(f"n={len(difs)} média {statistics.mean(difs):+.1f} mediana {statistics.median(difs):+.1f} "
              f"min {min(difs):+.0f} máx {max(difs):+.0f}")


# ------------------------------------------------------------------ (c)

def cmd_fita(a):
    neg, book, _, _ = ler_gravacao(a.gravacao)
    fita = ler_fita(a.fita)
    offs = [float(x) for x in a.offsets.split(",")]
    # atraso da reprodução: chegada − horário original do negócio (só o horário do dia)
    atrasos = []
    for t, _, tm, _, _ in neg[:: max(1, len(neg) // 5000)]:
        if tm:
            o = dt.datetime.fromisoformat(tm[:23]).replace(tzinfo=dt.timezone.utc)
            atrasos.append(seg_do_dia(t) - seg_do_dia(o.timestamp()))
    datas = sorted({tm[:10] for _, _, tm, _, _ in neg if tm})
    if atrasos:
        print(f"negócios reproduzidos de {', '.join(datas)}; atraso chegada − horário original: "
              f"p50 {statistics.median(atrasos) * 1000:.0f} ms, min {min(atrasos) * 1000:.0f}, máx {max(atrasos) * 1000:.0f}")
    # por segundo: o topo do book da HML no fim do segundo contra a fita deslocada
    seg = {}
    for t, bid, ask in book:
        if bid and ask and bid < ask:
            seg[int(seg_do_dia(t))] = (bid + ask) / 2
    negs = {}
    for t, p, *_ in neg:
        negs[int(seg_do_dia(t))] = p
    print("\noffset (s) → |meio HML − meio fita| e |negócio HML − preço fita|, por segundo:")
    melhor = None
    for off in offs:
        db, dn = [], []
        for s, meio in seg.items():
            f = ultimo(fita, s + 0.999 - off, 2)
            if f and f[2] and f[3]:
                db.append(meio - (f[2] + f[3]) / 2)
        for s, p in negs.items():
            f = ultimo(fita, s + 0.999 - off, 2)
            if f and f[1]:
                dn.append(p - f[1])
        if not db:
            continue
        m = statistics.mean(abs(x) for x in db)
        print(f"  {off:4.1f}: book média |d| {m:6.1f} pt (n={len(db)}); negócio média |d| "
              f"{statistics.mean(abs(x) for x in dn) if dn else float('nan'):6.1f} pt, "
              f"igual em {100 * sum(1 for x in dn if x == 0) / max(len(dn), 1):.0f}% dos segundos")
        if melhor is None or m < melhor[0]:
            melhor = (m, off)
    off = melhor[1] if melhor else 0.8
    print(f"\npor minuto, offset {off} s: meio HML − meio fita (pts) e negócio HML − fita")
    print(f"{'min':5} {'n':>4} {'book méd':>9} {'book min':>9} {'book máx':>9} | {'neg méd':>8} {'neg máx|d|':>10}")
    por_min = {}
    for s, meio in seg.items():
        f = ultimo(fita, s + 0.999 - off, 2)
        if f and f[2] and f[3]:
            por_min.setdefault(s // 60, [[], []])[0].append(meio - (f[2] + f[3]) / 2)
    for s, p in negs.items():
        f = ultimo(fita, s + 0.999 - off, 2)
        if f and f[1]:
            por_min.setdefault(s // 60, [[], []])[1].append(p - f[1])
    for mi in sorted(por_min):
        b, n = por_min[mi]
        if not b:
            continue
        marca = "  <-- descolado" if max(abs(x) for x in b) > a.limiar else ""
        print(f"{mi // 60:02d}:{mi % 60:02d} {len(b):4d} {statistics.mean(b):+9.1f} {min(b):+9.0f} {max(b):+9.0f} | "
              f"{statistics.mean(n) if n else float('nan'):+8.1f} {max((abs(x) for x in n), default=float('nan')):10.0f}{marca}")


# ------------------------------------------------------------------ espera pela 1014

def api(url):
    import urllib.request
    with urllib.request.urlopen(url, timeout=10) as r:
        return json.loads(r.read())


def tags(raw):
    return dict(x.split("=", 1) for x in (raw or "").replace("\x01", "|").split("|") if "=" in x)


def cmd_saidas(a):
    """Cada saída que não foi por stop: do 35=F da 1014 ao fill da saída, e o que o book dava no
    instante da decisão (o custo da espera pelo cancelamento da 1014)."""
    neg, book, _, _ = ler_gravacao(a.gravacao)
    neg_s, book_s = Serie(neg), Serie(book)
    dia = dt.datetime.fromtimestamp(neg[0][0], FUSO).date().isoformat()
    ops = api(f"{a.base}/operacoes?dia={dia}&modulo={a.modulo}")
    custos = []
    for o in ops:
        if not o.get("origem_saida") or o.get("origem_saida") == "STOP_SERVIDOR":
            continue
        op = o["operacao_id"]
        h = api(f"{a.base}/operacoes/{op}/historico?modulo={a.modulo}&cru=1")
        ms = [(dt.datetime.fromisoformat(m["em"]).timestamp(), m["direcao"], tags(m.get("fix_raw"))) for m in h["mensagens"]]
        f = next((x for x in ms if x[1] == "ENV" and x[2].get("35") == "F" and "-P-" in x[2].get("41", "")), None)
        d = next((x for x in ms if x[1] == "ENV" and x[2].get("35") == "D" and "-S-" in x[2].get("11", "")), None)
        fills = [x for x in ms if x[1] == "REC" and x[2].get("35") == "8" and "-S-" in x[2].get("11", "") and x[2].get("150") in ("F", "1")]
        if not f or not d or not fills:
            print(f"op {op}: sem 35=F da 1014, saída ou fill ({o.get('motivo_saida')})")
            continue
        print(f"\nop {op} ({o['lado']} {o.get('qtd')}, saída {o.get('motivo_saida')}/{o.get('origem_saida')}, {o.get('pnl_pts')} pts)")
        print(f"  {hora(f[0])} → 35=F da 1014 (decisão de saída)")
        for x in ms:
            if f[0] <= x[0] <= d[0] and x[1] == "REC" and "-P-" in (x[2].get("11", "") + x[2].get("41", "")):
                print(f"  {hora(x[0])} ← 35={x[2].get('35')} 150={x[2].get('150', '-')} 39={x[2].get('39', '-')} (52 {x[2].get('52', '')[9:]}) "
                      f"+{(x[0] - f[0]) * 1000:.0f} ms  {x[2].get('58', '')[:70]}")
        print(f"  {hora(d[0])} → 35=D saída, limite {d[2].get('44')}  (+{(d[0] - f[0]) * 1000:.0f} ms)")
        qtd = sum(int(float(x[2].get("32", 0))) for x in fills)
        px = sum(float(x[2]["31"]) * int(float(x[2].get("32", 0))) for x in fills) / max(qtd, 1)
        tf = fills[-1][0]
        print(f"  {hora(tf)} ← fill {px:.0f} (+{(tf - d[0]) * 1000:.0f} ms do envio)")
        vende = o["lado"] == "C"
        lag = tf - d[0]
        b0, n0 = ultimo(book_s, f[0]), ultimo(neg_s, f[0])
        b1, n1 = ultimo(book_s, f[0] + lag), ultimo(neg_s, f[0] + lag)
        bd = ultimo(book_s, d[0])
        ref0 = b0[1] if vende else b0[2]
        ref1 = b1[1] if vende else b1[2]
        refd = bd[1] if vende else bd[2]
        sinal = 1 if vende else -1  # custo positivo = a espera custou
        print(f"  book na decisão {b0[1]:.0f}/{b0[2]:.0f} último {n0[1]:.0f}; no envio real {bd[1]:.0f}/{bd[2]:.0f}; "
              f"na decisão + {lag:.2f} s {b1[1]:.0f}/{b1[2]:.0f} último {n1[1]:.0f}")
        c0, c1 = sinal * (ref0 - refd), sinal * (ref1 - px)
        print(f"  custo da espera: {'bid' if vende else 'ask'} na decisão − no envio = {c0:+.0f} pts; "
              f"fill hipotético (topo na decisão + {lag:.2f} s) − fill real = {c1:+.0f} pts")
        custos.append((op, c0, c1))
    if custos:
        print(f"\n{len(custos)} saída(s): custo pelo topo na decisão {sum(c[1] for c in custos):+.0f} pts; "
              f"pelo fill hipotético {sum(c[2] for c in custos):+.0f} pts")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("status")
    s.add_argument("gravacao")
    s = sub.add_parser("fills")
    s.add_argument("gravacao")
    s.add_argument("--uid", type=int, default=1450)
    s.add_argument("--fita")
    s.add_argument("--desde", default="08:55")
    s.add_argument("--ate")
    s.add_argument("--offset", type=float, help="fita no instante t − OFFSET s (padrão: o melhor pelo book)")
    s.add_argument("--journal", help="arquivo com a saída de journalctl -o cat (em vez de chamar o journalctl)")
    s = sub.add_parser("fita")
    s.add_argument("gravacao")
    s.add_argument("--fita", required=True)
    s.add_argument("--offsets", default="-3,-2.5,-2,-1.5,-1,-0.5,0,0.5,0.8,1,1.5,2")
    s.add_argument("--limiar", type=float, default=50, help="marca o minuto com |d| do book acima disto")
    s = sub.add_parser("saidas")
    s.add_argument("gravacao")
    s.add_argument("--modulo", default="KHML")
    s.add_argument("--base", default="http://127.0.0.1:9092")
    a = ap.parse_args()
    {"status": cmd_status, "fills": cmd_fills, "fita": cmd_fita, "saidas": cmd_saidas}[a.cmd](a)


if __name__ == "__main__":
    main()
