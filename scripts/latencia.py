#!/usr/bin/env python3
"""Latência por sinal, PnT × Flexscan lado a lado (o teste duplo de 23/11 e 24/11).

Só leitura: o histórico gravado no banco, pelos endpoints de leitura do serviço (127.0.0.1:9092:
GET /operacoes?dia= e GET /operacoes/{id}/historico?cru=1), sem a senha do banco e sem coleta ao
vivo. Nada é mandado ao serviço além desses GETs.

    scripts/latencia.py --ops 234844,234845          um sinal (as duas operações dele)
    scripts/latencia.py --dia 2026-11-24             o dia todo (os pares --uids, padrão 1450,5725)

Grava em var/latencia-2311/ dois CSV (separador ';', horas locais, intervalos em ms):
  latencia-<x>.csv          uma linha por sinal: a entrada, a stop inicial, a saída e o resumo dos ajustes
  latencia-<x>-ajustes.csv  uma linha por ajuste da stop (os dois lados juntos quando é o mesmo sinal
                            do Kalman: mesma origem a até 2 s; o STOP_MANUAL de cada um fica sozinho),
                            com o atraso entre a decisão e o envio de cada PUT/replace

O atraso decisão → envio do PUT da Flexscan usa a hora gravada do FLX-PUT, que é a hora em que o
motor registrou o evento (motor/flexscan.go, Registrar pelo entrarFIX), não a hora exata da saída do
PUT: com o motor ocupado, ela atrasa (e o FLX-202 sai com a mesma hora). A coluna
decisao_ate_corretora_ms (a decisão até o sendingTime da Flexscan / o 52 do PnT) não depende disso e
é a conferência: se ela é normal (~40 ms na Flexscan) e o atraso é grande, o PUT saiu na hora e o
atraso foi só no registro.

As horas da corretora: no PnT, o 52 da mensagem dele (a hora do PnT, não a da B3); na Flexscan, o
sendingTime da ordem na consulta. O escorregamento é em pontos, positivo = pior que o pedido: na
entrada contra o limite pedido, na saída pela stop contra o gatilho, na saída pedida pelo Kalman contra o limite da
primeira S (antes da ampliação do limite).
"""
import argparse, csv, datetime as dt, json, os, sys, urllib.request

BASE = "http://127.0.0.1:9092"
TZ = dt.timezone(dt.timedelta(hours=-3))
PAR_JANELA_S = 2.0
LADOS = (("pnt", "PNT"), ("flx", "FLEXSCAN"))


# ---------- leitura ----------

def obter(caminho, jsondir):
    if jsondir:  # cópias gravadas antes (teste ou releitura), com o mesmo caminho como nome
        nome = caminho.strip("/").replace("/", "_").replace("?", "_").replace("&", "_").replace("=", "-")
        with open(os.path.join(jsondir, nome + ".json")) as f:
            return json.load(f)
    with urllib.request.urlopen(BASE + caminho, timeout=30) as r:
        return json.load(r)


def historico(op, jsondir):
    return obter(f"/operacoes/{op}/historico?modulo=KALMAN&cru=1", jsondir)


# ---------- horas ----------

def h_iso(s):
    return dt.datetime.fromisoformat(s) if s else None


def h_fix(s):  # 20261124-12:38:57.890 (UTC)
    if not s:
        return None
    fmt = "%Y%m%d-%H:%M:%S.%f" if "." in s else "%Y%m%d-%H:%M:%S"
    return dt.datetime.strptime(s, fmt).replace(tzinfo=dt.timezone.utc).astimezone(TZ)


def hora(t):
    return t.astimezone(TZ).strftime("%H:%M:%S.%f")[:-3] if t else ""


def ms(a, b):  # b - a
    return round((b - a).total_seconds() * 1000) if a and b else ""


# ---------- as mensagens ----------

def campos(raw):
    if not raw or raw.startswith("{") or "=" not in raw:
        return {}
    d = {}
    for p in raw.split("|"):
        if "=" in p:
            k, v = p.split("=", 1)
            d.setdefault(k, v)
    return d


class Msg:
    def __init__(self, m):
        self.em = h_iso(m["em"])
        self.dir = m["direcao"]
        self.tipo = m["msgtype"]
        self.clordid = m.get("clordid") or ""
        self.ex = m.get("exectype")
        self.raw = m.get("fix_raw") or ""
        self.f = campos(self.raw)
        self.js = None
        if self.tipo == "FLX-GET":
            try:
                self.js = json.loads(self.raw)
            except ValueError:
                self.js = {}

    def tag(self, k):
        return self.f.get(k)

    def h52(self):
        return h_fix(self.tag("52"))

    def h60(self):
        return h_fix(self.tag("60"))


def get_junto(msgs, ref, status):
    """A consulta da Flexscan gravada junto com o relatório ref (mesmo instante), com esse orderStatus."""
    for m in msgs:
        if m.tipo == "FLX-GET" and m.js is not None and abs((m.em - ref.em).total_seconds()) < 0.01 \
                and str(m.js.get("orderStatus")) in status:
            return m
    return None


def primeiro(msgs, cond, depois=None):
    for m in msgs:
        if (depois is None or m.em >= depois) and cond(m):
            return m
    return None


def num(v):
    if v in (None, ""):
        return None
    try:
        return int(round(float(v)))
    except ValueError:
        return None


def escorrega(lado_ordem, pedido, exec_):
    """Pontos, positivo = pior: compra pior se executou acima; venda, abaixo."""
    if pedido is None or exec_ is None:
        return ""
    return exec_ - pedido if lado_ordem == "1" else pedido - exec_


# ---------- uma operação ----------

def analisar(op, jsondir):
    h = historico(op, jsondir)
    o = h["operacao"]
    con = o["conector"]
    msgs = sorted((Msg(m) for m in h["mensagens"]), key=lambda m: m.em)
    ped = h.get("pedidos") or []
    r = {"op": op, "uid": o["uid"], "conta": o["conta"], "conector": con, "estado": o["estado"],
         "pnl_pts": o.get("pnl_pts"), "motivo_saida": o.get("motivo_saida") or ""}
    flx = con == "FLEXSCAN"

    # --- entrada
    pe = next((p for p in ped if (p.get("corpo") or {}).get("papel") == "ENTRADA"), None)
    corpo = (pe or {}).get("corpo") or {}
    r["sinal_kalman"] = h_iso(corpo.get("ts_decisao"))
    e_id = f"KM-E-{op}-1"
    d = primeiro(msgs, lambda m: m.dir == "ENV" and m.tipo == "D" and m.clordid == e_id)
    r["e_decisao"] = d.h60() if d else None
    r["e_envio"] = d.em if d else None  # o 35=D gravado na saída do conector (a Flexscan: o POST)
    r["e_preco_pedido"] = num(d.tag("44")) if d else None
    lado_e = d.tag("54") if d else None
    r1 = primeiro(msgs, lambda m: m.dir == "REC" and m.tipo == "8" and m.clordid == e_id)
    r["e_resposta1"] = r1.em if r1 else None
    if flx:
        r["e_aceite"] = r1.em if r1 and r1.ex == "A" else None  # o 202
    else:
        a = primeiro(msgs, lambda m: m.dir == "REC" and m.clordid == e_id and m.ex == "0")
        r["e_aceite"] = a.em if a else None
    fe = primeiro(msgs, lambda m: m.dir == "REC" and m.clordid == e_id and m.ex in ("F", "1"))
    r["e_exec_vista"] = fe.em if fe else None
    r["e_preco_exec"] = num(fe.tag("31")) if fe else None
    if fe and flx:
        g = get_junto(msgs, fe, ("1", "2"))
        r["e_exec_corretora"] = h_fix(g.js.get("sendingTime")) if g else None
    else:
        r["e_exec_corretora"] = fe.h52() if fe else None
    r["e_escorregamento"] = escorrega(lado_e, r["e_preco_pedido"], r["e_preco_exec"])

    # --- stop inicial (a primeira P)
    p_id = f"KM-P-{op}-1"
    sp = primeiro(msgs, lambda m: m.dir == "ENV" and m.tipo == "D" and m.clordid == p_id)
    r["s_decisao"] = sp.h60() if sp else None
    r["s_envio"] = sp.em if sp else None
    r["s_stop"] = num(sp.tag("20828")) if sp else None
    r["s_limite"] = num(sp.tag("44")) if sp else None
    if flx:
        a = primeiro(msgs, lambda m: m.dir == "REC" and m.clordid == p_id and m.ex == "A")
        r["s_aceite"] = a.em if a else None
        c = primeiro(msgs, lambda m: m.dir == "REC" and m.clordid == p_id and m.ex == "0")
        g = get_junto(msgs, c, ("0",)) if c else None
        r["s_aceite_corretora"] = h_fix(g.js.get("sendingTime")) if g else None
    else:
        a = primeiro(msgs, lambda m: m.dir == "REC" and m.clordid == p_id and m.ex == "0")
        r["s_aceite"] = a.em if a else None
        r["s_aceite_corretora"] = a.h52() if a else None

    # --- ajustes (cada 35=G nosso)
    aj = []
    for g in (m for m in msgs if m.dir == "ENV" and m.tipo == "G"):
        cid = g.clordid
        pd = next((p for p in ped if (p.get("corpo") or {}).get("preco_stop") is not None
                   and abs((h_iso(p["em"]) - g.em).total_seconds()) < 0.05), None)
        x = {"op": op, "conector": con, "clordid": cid,
             "origem": ((pd or {}).get("corpo") or {}).get("origem", ""),
             "pedido": h_iso(pd["em"]) if pd else None, "decisao": g.h60(),
             "stop": num(g.tag("20828")), "limite": num(g.tag("44"))}
        if flx:
            put = primeiro(msgs, lambda m: m.dir == "ENV" and m.tipo == "FLX-PUT" and m.clordid == cid)
            x["envio"] = put.em if put else None
            r202 = primeiro(msgs, lambda m: m.dir == "REC" and m.tipo == "FLX-202" and m.clordid == cid)
            x["resposta1"] = r202.em if r202 else None
        else:
            x["envio"] = g.em
            e = primeiro(msgs, lambda m: m.dir == "REC" and m.clordid == cid and m.ex == "E")
            x["resposta1"] = e.em if e else None
        c5 = primeiro(msgs, lambda m: m.dir == "REC" and m.clordid == cid and m.ex == "5")
        x["aceite"] = c5.em if c5 else None
        if flx and c5:
            gg = get_junto(msgs, c5, ("5", "0"))
            x["aceite_corretora"] = h_fix(gg.js.get("sendingTime")) if gg else None
        else:
            x["aceite_corretora"] = c5.h52() if c5 else None
        rec = primeiro(msgs, lambda m: m.dir == "REC" and m.tipo == "9" and m.clordid == cid)
        x["recusa"] = (rec.tag("58") or "35=9") if rec else ""
        x["atraso_decisao_envio"] = ms(x["decisao"], x["envio"])
        aj.append(x)
    r["ajustes"] = aj

    # --- saída: o fill que fecha a posição (lado contrário ao da entrada), de qualquer ordem
    contra = {"1": "2", "2": "1"}.get(lado_e or "", "")
    sa = None
    for m in msgs:
        if m.dir == "REC" and m.ex in ("F", "1") and m.clordid != e_id and m.tag("54") == contra:
            sa = m
    ps = next((p for p in ped if (p.get("corpo") or {}).get("papel") == "SAIDA"), None)
    r["x_sinal_kalman"] = h_iso(((ps or {}).get("corpo") or {}).get("ts_decisao")) if ps else None
    r["x_pedido"] = h_iso(ps["em"]) if ps else None
    x_env = primeiro(msgs, lambda m: m.dir == "ENV" and m.tipo not in ("FLX-GET",), r["x_pedido"]) if ps else None
    r["x_envio"] = x_env.em if x_env else None
    r["x_replaces"] = sum(1 for e in h.get("eventos") or [] if e.get("tipo") == "SAIDA_POR_REPLACE")
    r["x_exec_vista"] = sa.em if sa else None
    r["x_preco_exec"] = num(sa.tag("31")) if sa else None
    r["x_ordem"] = sa.clordid if sa else ""
    ultima = None  # a última versão da ordem que executou: o gatilho e o limite dela
    if sa:
        for m in msgs:
            if m.dir == "ENV" and m.tipo in ("D", "G") and (m.clordid == sa.clordid or m.clordid.startswith(sa.clordid.rsplit("-", 1)[0] + "-")) \
                    and m.em <= sa.em:
                ultima = m
    r["x_stop"] = num(ultima.tag("20828")) if ultima else None
    r["x_limite"] = num(ultima.tag("44")) if ultima else None
    # a saída a mercado (a S): o preço pedido é o limite da primeira S, antes de qualquer ampliação
    s1 = primeiro(msgs, lambda m: m.dir == "ENV" and m.tipo == "D" and m.clordid == f"KM-S-{op}-1")
    r["x_limite_pedido"] = num(s1.tag("44")) if s1 and sa and sa.clordid.startswith(f"KM-S-{op}-") else None
    if sa and flx:
        g = get_junto(msgs, sa, ("1", "2"))
        r["x_exec_corretora"] = h_fix(g.js.get("sendingTime")) if g else None
        r["x_disparo"] = None  # a Flexscan não manda o disparo da stop
    else:
        r["x_exec_corretora"] = sa.h52() if sa else None
        dsp = primeiro(msgs, lambda m: m.dir == "REC" and m.ex == "I" and (m.tag("17") or "").startswith("PendFin"),
                       r["e_exec_vista"]) if sa else None
        r["x_disparo"] = dsp.h52() if dsp and dsp.em <= sa.em else None
    if sa and sa.clordid.startswith("KM-P-") and r["x_stop"]:
        base, r["x_base"] = r["x_stop"], "stop"
    elif r["x_limite_pedido"] is not None:
        base, r["x_base"] = r["x_limite_pedido"], "limite_pedido"
    else:
        base, r["x_base"] = r["x_limite"], ("limite" if r["x_limite"] is not None else "")
    r["x_escorregamento"] = escorrega(sa.tag("54") if sa else None, base, r["x_preco_exec"])
    return r


# ---------- os pares ----------

def pares_do_dia(dia, uids, jsondir):
    l = obter(f"/operacoes?dia={dia}&ambiente=REAL&modulo=KALMAN", jsondir)
    l = [o for o in l if o["uid"] in uids]
    pnt = [o for o in l if o["conector"] == "PNT"]
    flx = [o for o in l if o["conector"] == "FLEXSCAN"]
    usados, pares = set(), []
    for a in pnt:
        ta = h_iso(a["criada_em"])
        b = min((o for o in flx if o["operacao_id"] not in usados and o["lado"] == a["lado"]
                 and abs((h_iso(o["criada_em"]) - ta).total_seconds()) <= PAR_JANELA_S),
                key=lambda o: abs((h_iso(o["criada_em"]) - ta).total_seconds()), default=None)
        if b:
            usados.add(b["operacao_id"])
        pares.append((a["operacao_id"], b["operacao_id"] if b else None))
    pares += [(None, o["operacao_id"]) for o in flx if o["operacao_id"] not in usados]
    return sorted(pares, key=lambda p: min(x for x in p if x))


# ---------- as linhas ----------

COLS_LADO = [
    ("op", "op"), ("uid", "uid"), ("conta", "conta"),
    ("sinal_kalman", "h"), ("e_decisao", "h"), ("e_envio", "h"),
    ("e_resposta1_ms", ("e_envio", "e_resposta1")), ("e_aceite_ms", ("e_envio", "e_aceite")),
    ("e_exec_corretora", "h"), ("e_exec_corretora_ms", ("e_envio", "e_exec_corretora")),
    ("e_exec_vista", "h"), ("e_exec_vista_ms", ("e_envio", "e_exec_vista")),
    ("e_vista_menos_corretora_ms", ("e_exec_corretora", "e_exec_vista")),
    ("e_preco_pedido", "v"), ("e_preco_exec", "v"), ("e_escorregamento", "v"),
    ("s_decisao", "h"), ("s_envio", "h"), ("s_aceite_ms", ("s_envio", "s_aceite")),
    ("s_aceite_corretora_ms", ("s_envio", "s_aceite_corretora")), ("s_stop", "v"), ("s_limite", "v"),
    ("ajustes_n", "aj_n"), ("ajustes_decisao_envio_max_ms", "aj_max"), ("ajustes_aceite_max_ms", "aj_aceite"),
    ("x_motivo", "motivo_saida"), ("x_sinal_kalman", "h"), ("x_pedido", "h"), ("x_envio", "h"),
    ("x_replaces", "v"), ("x_pedido_ate_exec_ms", ("x_pedido", "x_exec_vista")), ("x_disparo", "h"),
    ("x_exec_corretora", "h"), ("x_exec_vista", "h"), ("x_vista_menos_corretora_ms", ("x_exec_corretora", "x_exec_vista")),
    ("x_stop", "v"), ("x_limite", "v"), ("x_limite_pedido", "v"), ("x_preco_exec", "v"), ("x_escorregamento", "v"), ("x_escorregamento_base", "x_base"),
    ("pnl_pts", "v"),
]


def valores_lado(r):
    out = []
    for nome, como in COLS_LADO:
        if r is None:
            out.append("")
        elif como == "h":
            out.append(hora(r.get(nome)))
        elif como == "v" or como in ("op", "uid", "conta"):
            v = r.get(nome)
            out.append("" if v is None else v)
        elif isinstance(como, tuple):
            out.append(ms(r.get(como[0]), r.get(como[1])))
        elif como == "aj_n":
            out.append(len(r["ajustes"]))
        elif como == "aj_max":
            v = [a["atraso_decisao_envio"] for a in r["ajustes"] if a["atraso_decisao_envio"] != ""]
            out.append(max(v) if v else "")
        elif como == "aj_aceite":
            v = [ms(a["envio"], a["aceite"]) for a in r["ajustes"]]
            v = [x for x in v if x != ""]
            out.append(max(v) if v else "")
        else:
            v = r.get(como)
            out.append("" if v is None else v)
    return out


def linha_sinal(a, b):
    ref = next(r for r in (a, b) if r)
    t = ref.get("sinal_kalman") or ref.get("e_envio")
    dif = lambda k: ms(a.get(k), b.get(k)) if a and b else ""  # Flexscan - PnT
    comum = [t.astimezone(TZ).strftime("%Y-%m-%d") if t else "", hora(t),
             ms(a.get("sinal_kalman"), b.get("sinal_kalman")) if a and b else "",
             dif("e_exec_corretora"), dif("e_exec_vista"), dif("x_exec_vista"),
             (b["e_preco_exec"] - a["e_preco_exec"]) if a and b and a["e_preco_exec"] and b["e_preco_exec"] else "",
             (b["x_preco_exec"] - a["x_preco_exec"]) if a and b and a["x_preco_exec"] and b["x_preco_exec"] else ""]
    return comum + valores_lado(a) + valores_lado(b)


CAB_COMUM = ["dia", "sinal", "sinal_flx_menos_pnt_ms", "e_exec_corretora_flx_menos_pnt_ms",
             "e_exec_vista_flx_menos_pnt_ms", "x_exec_vista_flx_menos_pnt_ms",
             "e_preco_flx_menos_pnt", "x_preco_flx_menos_pnt"]

COLS_AJ = ["op", "clordid", "origem", "decisao", "envio", "atraso_decisao_envio_ms", "decisao_ate_corretora_ms",
           "resposta1_ms", "aceite_ms", "aceite_corretora_ms", "stop", "limite", "recusa"]


def valores_aj(x):
    if x is None:
        return [""] * len(COLS_AJ)
    return [x["op"], x["clordid"], x["origem"], hora(x["decisao"]), hora(x["envio"]), x["atraso_decisao_envio"],
            ms(x["decisao"], x["aceite_corretora"]), ms(x["envio"], x["resposta1"]), ms(x["envio"], x["aceite"]), ms(x["envio"], x["aceite_corretora"]),
            "" if x["stop"] is None else x["stop"], "" if x["limite"] is None else x["limite"], x["recusa"]]


def linhas_ajustes(a, b):
    la = list(a["ajustes"]) if a else []
    lb = list(b["ajustes"]) if b else []
    out = []
    for x in la:  # o mesmo sinal do Kalman (TRSP etc., não o STOP_MANUAL): mesma origem a até 2 s
        y = None
        if x["origem"] != "STOP_MANUAL" and x["pedido"]:
            y = next((z for z in lb if z["origem"] == x["origem"] and z["pedido"]
                      and abs((z["pedido"] - x["pedido"]).total_seconds()) <= PAR_JANELA_S), None)
        if y:
            lb.remove(y)
        out.append((x, y))
    out += [(None, y) for y in lb]
    out.sort(key=lambda p: min(z["decisao"] or z["envio"] for z in p if z))
    sinal = hora((a or b).get("sinal_kalman"))
    return [[sinal, (x or y)["origem"], ms(x["decisao"], y["decisao"]) if x and y else ""] + valores_aj(x) + valores_aj(y)
            for x, y in out]


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--dia", help="AAAA-MM-DD: todos os sinais do dia dos --uids")
    g.add_argument("--ops", help="PNT,FLEXSCAN: as duas operações de um sinal (uma delas pode ser 0)")
    ap.add_argument("--uids", default="1450,5725", help="com --dia (padrão 1450,5725)")
    ap.add_argument("--saida", default="var/latencia-2311")
    ap.add_argument("--json", help="lê de cópias gravadas (teste), não do serviço")
    a = ap.parse_args()

    if a.ops:
        p = [int(x) or None for x in a.ops.split(",")]
        if len(p) != 2:
            sys.exit("--ops PNT,FLEXSCAN")
        pares, nome = [tuple(p)], "ops-" + "-".join(str(x or 0) for x in p)
    else:
        pares, nome = pares_do_dia(a.dia, {int(u) for u in a.uids.split(",")}, a.json), a.dia

    os.makedirs(a.saida, exist_ok=True)
    f1 = os.path.join(a.saida, f"latencia-{nome}.csv")
    f2 = os.path.join(a.saida, f"latencia-{nome}-ajustes.csv")
    cab_lado = [c for c, _ in COLS_LADO]
    with open(f1, "w", newline="") as s1, open(f2, "w", newline="") as s2:
        w1, w2 = csv.writer(s1, delimiter=";"), csv.writer(s2, delimiter=";")
        w1.writerow(CAB_COMUM + ["pnt_" + c for c in cab_lado] + ["flx_" + c for c in cab_lado])
        w2.writerow(["sinal", "origem", "decisao_flx_menos_pnt_ms"] + ["pnt_" + c for c in COLS_AJ] + ["flx_" + c for c in COLS_AJ])
        n = 0
        for po, fo in pares:
            ra = analisar(po, a.json) if po else None
            rb = analisar(fo, a.json) if fo else None
            for r, quer in ((ra, "PNT"), (rb, "FLEXSCAN")):
                if r and r["conector"] != quer:
                    sys.exit(f"op {r['op']}: conector {r['conector']}, esperado {quer} (--ops PNT,FLEXSCAN)")
            w1.writerow(linha_sinal(ra, rb))
            for l in linhas_ajustes(ra, rb):
                w2.writerow(l)
            n += 1
    print(f"{n} sinal(is): {f1}")
    print(f"ajustes: {f2}")


if __name__ == "__main__":
    main()
