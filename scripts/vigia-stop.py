#!/usr/bin/env python3
"""Vigia da stop do PnT que não dispara (24/11, op 234852; SPEC v1.37 §7.2), só leitura, fora do serviço.

Avisa no Telegram quando uma stop (1014) do PnT no REAL está viva e aceita, o último passou do gatilho
por pelo menos 1 tick há N s (padrão 2) com o preço confiável, e o PnT não mandou nenhum sinal de disparo
(150=I, 39=B ou fill). Não manda ordem nenhuma: só GETs no serviço (127.0.0.1:9092) e na 9090.

    scripts/vigia-stop.py                              ao vivo (o alerta vai ao Telegram)
    scripts/vigia-stop.py --sem-telegram               ao vivo, só no log
    scripts/vigia-stop.py --replay CAPTURA --op 234852 --de 11:32:20 --ate 11:33:06
                                                       a mesma regra em cima da captura da Cedro e do
                                                       histórico gravado da operação (teste)

A URL do Telegram (tem o token do bot) vem de um arquivo, nunca da linha de comando, e nunca é
mostrada: --url-arquivo (padrão ~/.config/kalman-vigia/telegram.url), dono = quem roda, modo 600 ou
400; senão o vigia não sobe. O texto vai no parâmetro "text" (api.telegram.org/.../sendMessage?chat_id=)
ou "msg" (o bot local, como o ALERTA_WEBHOOK_URL do serviço).

O relógio de cada stop começa na 1ª leitura além do gatilho e não volta a zero se o preço voltar (o
toque bastava para disparar); volta a zero quando o gatilho muda. Um alerta por (operação, gatilho).
"""
import argparse, datetime as dt, json, os, re, stat, struct, sys, time, urllib.parse, urllib.request

SERVICO = "http://127.0.0.1:9092"
PRECOS = "http://172.16.110.53:9090/precos/atual?ativo=WINFUT"
TZ = dt.timezone(dt.timedelta(hours=-3))
TICK = 5
RE_P = re.compile(r"^KM-(?:(BOLETA)-)?P-(\d+)-\d+$")


def hora(t):
    return dt.datetime.fromtimestamp(t, TZ).strftime("%H:%M:%S.%f")[:-3]


def log(msg):
    print(f"{dt.datetime.now(TZ).strftime('%H:%M:%S.%f')[:-3]} {msg}", flush=True)


# ---------------- a regra (a mesma ao vivo e no replay) ----------------

class Regra:
    def __init__(self, n_s, ticks, idade_max_s):
        self.n, self.alem, self.idade_max = n_s, ticks * TICK, idade_max_s
        self.stops = {}  # op -> {gatilho, desde, alertado}

    def alem_do_gatilho(self, lado_stop, gatilho, ultimo):
        return ultimo <= gatilho - self.alem if lado_stop == "V" else ultimo >= gatilho + self.alem

    def passo(self, agora, op, lado_stop, gatilho, ultimo, preco_ok):
        """Devolve o instante do 1º cruzamento quando é hora de conferir o disparo (uma vez por gatilho)."""
        s = self.stops.get(op)
        if s is None or s["gatilho"] != gatilho:
            s = self.stops[op] = {"gatilho": gatilho, "desde": None, "alertado": False}
        if s["alertado"]:
            return None
        if s["desde"] is None:
            if preco_ok and ultimo is not None and self.alem_do_gatilho(lado_stop, gatilho, ultimo):
                s["desde"] = agora
            return None
        if round((agora - s["desde"]) * 1000) >= round(self.n * 1000):
            s["alertado"] = True
            return s["desde"]
        return None

    def esquecer(self, vivos):
        for op in list(self.stops):
            if op not in vivos:
                del self.stops[op]


def sinal_de_disparo(msgs, clordid, desde_em):
    """O PnT mandou algum sinal de disparo/execução da 1014 atual (150=I, 39=B, 150=1/F) desde desde_em?"""
    for m in msgs:
        if m.get("direcao") != "REC" or m.get("clordid") != clordid or m["em_s"] < desde_em:
            continue
        raw = m.get("fix_raw") or ""
        if m.get("exectype") in ("I", "1", "F") or "|39=B|" in raw:
            return True
    return False


def texto_alerta(op, uid, conta, lado_stop, gatilho, ultimo, desde, agora):
    lado = "venda" if lado_stop == "V" else "compra"
    return (f"[.53 vigia-stop] CRITICO STOP_SEM_DISPARO: uid {uid} op {op} (PnT, conta {conta}): stop de {lado} "
            f"gatilho {gatilho} sem disparo ha {agora - desde:.1f} s com o ultimo em {ultimo} "
            f"(alem do gatilho desde {hora(desde)}). Conferir no Profit; acao: cartao de emergencia")


# ---------------- Telegram ----------------

class Telegram:
    def __init__(self, caminho):
        st = os.stat(caminho)
        if st.st_uid != os.getuid():
            sys.exit(f"{caminho}: o dono tem de ser quem roda o vigia")
        if stat.S_IMODE(st.st_mode) & 0o077:
            sys.exit(f"{caminho}: permissão {oct(stat.S_IMODE(st.st_mode))}; use chmod 600")
        with open(caminho) as f:
            self.url = f.read().strip()
        if not self.url.startswith(("https://", "http://")):
            sys.exit(f"{caminho}: não parece uma URL (o conteúdo não é mostrado)")
        self.param = "text" if "api.telegram.org" in self.url else "msg"

    def enviar(self, texto):
        sep = "&" if "?" in self.url else "?"
        try:
            with urllib.request.urlopen(self.url + sep + urllib.parse.urlencode({self.param: texto}), timeout=10) as r:
                return f"HTTP {r.status}"
        except urllib.error.HTTPError as e:  # nunca o str(e) inteiro: pode trazer a URL
            return f"falhou: HTTP {e.code}"
        except Exception as e:
            return f"falhou: {type(e).__name__}"


# ---------------- ao vivo ----------------

def get(url, timeout=3):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return json.load(r)


def em_s(iso):
    return dt.datetime.fromisoformat(iso).timestamp()


def ao_vivo(a):
    tg = None if a.sem_telegram else Telegram(os.path.expanduser(a.url_arquivo))
    regra = Regra(a.n, a.ticks, a.idade_max)
    avisos = {}  # degradação do próprio vigia: um aviso por motivo até voltar

    def avisar(texto):
        r = tg.enviar(texto) if tg else "sem telegram"
        log(f"ALERTA ({r}): {texto}")

    def degradado(chave, ruim, texto):
        if ruim and not avisos.get(chave):
            avisos[chave] = True
            avisar(f"[.53 vigia-stop] AVISO: {texto}")
        elif not ruim and avisos.get(chave):
            avisos[chave] = False
            avisar(f"[.53 vigia-stop] RESOLVIDO: {texto}")

    log(f"vigia no ar: N={a.n}s, {a.ticks} tick(s) além do gatilho, preço com idade <= {a.idade_max}s, "
        f"telegram {'desligado' if tg is None else 'ligado (' + tg.param + ')'}")
    if tg and a.teste_telegram:
        avisar("[.53 vigia-stop] teste: vigia no ar")
    while True:
        t0 = time.time()
        try:
            sau = get(SERVICO + "/saude")
            degradado("servico", False, "o serviço respondeu")
        except Exception as e:
            degradado("servico", True, f"o serviço não responde ({type(e).__name__})")
            time.sleep(a.intervalo)
            continue
        real = (sau.get("ambientes") or {}).get("REAL") or sau
        vivas = real.get("ordens_vivas") or []
        ops = {}
        for o in vivas:
            m = RE_P.match(o.get("clordid") or "")
            if m and o.get("conector") == "PNT":
                ops[int(m.group(2))] = "BOLETA" if m.group(1) else "KALMAN"
        regra.esquecer(set(ops))
        preco_ok, ultimo = False, None
        if ops:
            try:
                p = get(PRECOS)["WINFUT"]
                ultimo = p.get("preco")
                preco_ok = p.get("status84") == 0 and (p.get("idade_seg") or 0) <= a.idade_max and ultimo is not None
                degradado("preco", not preco_ok, f"preço da 9090 inutilizável (status84 {p.get('status84')}, idade {p.get('idade_seg')} s)")
            except Exception as e:
                degradado("preco", True, f"a 9090 não responde ({type(e).__name__})")
        for op, mod in ops.items():
            try:
                d = get(f"{SERVICO}/ordens/{op}?modulo={mod}")
            except Exception:
                continue
            sc, pos = d.get("stop_corretora") or {}, d.get("posicao") or 0
            if sc.get("estado") != "VIVA" or not sc.get("preco_stop") or pos == 0:
                continue
            lado = "V" if pos > 0 else "C"
            agora = time.time()
            desde = regra.passo(agora, op, lado, int(sc["preco_stop"]), ultimo, preco_ok)
            if desde is None:
                continue
            try:
                h = get(f"{SERVICO}/operacoes/{op}/historico?modulo={mod}&cru=1", timeout=5)
                msgs = [dict(m, em_s=em_s(m["em"])) for m in h.get("mensagens") or []]
            except Exception:
                msgs = None
            if msgs is not None and sinal_de_disparo(msgs, sc.get("clordid"), desde - 1):
                log(f"op {op}: além do gatilho {sc['preco_stop']} há {agora - desde:.1f} s, mas o PnT já sinalizou o disparo")
                continue
            avisar(texto_alerta(op, d.get("uid"), d.get("conta"), lado, int(sc["preco_stop"]), ultimo, desde, agora)
                   + ("" if msgs is not None else " (historico indisponivel)"))
        time.sleep(max(0.0, a.intervalo - (time.time() - t0)))


# ---------------- replay (teste) ----------------

def ler_captura(caminho, de, ate):
    """[(t_receb, ultimo)] do WINZ26 entre de e ate (HHMMSS da Cedro), da captura TCEMDCAP."""
    out = []
    with open(caminho, "rb") as f:
        if f.read(8) != b"TCEMDCAP":
            sys.exit(f"{caminho}: não é uma captura TCEMDCAP")
        f.read(8)
        while True:
            h = f.read(12)
            if len(h) < 12:
                break
            ts, n = struct.unpack(">QI", h)
            p = f.read(n)
            if b"T:WINZ26:" not in p:
                continue
            for linha in p.split(b"\r\n"):
                if not linha.startswith(b"T:WINZ26:"):
                    continue
                partes = linha.rstrip(b"!").decode(errors="replace").split(":")
                if not (de <= partes[2] <= ate):
                    continue
                kv = dict(zip(partes[3::2], partes[4::2]))
                try:
                    v = float(kv.get("2") or "")
                except ValueError:
                    continue
                if v > 100000:
                    out.append((ts / 1000.0, int(v)))
    return sorted(out)


def replay(a):
    hh = lambda s: s.replace(":", "")
    precos = ler_captura(a.replay, hh(a.de), hh(a.ate))
    if not precos:
        sys.exit("nenhum negócio do WINZ26 na janela")
    h = json.load(open(a.historico)) if a.historico else get(f"{SERVICO}/operacoes/{a.op}/historico?modulo=KALMAN&cru=1")
    msgs = sorted((dict(m, em_s=em_s(m["em"])) for m in h["mensagens"]), key=lambda m: m["em_s"])
    ent = next(m for m in msgs if m["direcao"] == "ENV" and m["msgtype"] == "D" and (m["clordid"] or "").startswith("KM-E-"))
    lado = "V" if "|54=1|" in ent["fix_raw"] else "C"
    tag = lambda raw, k: (re.search(r"\|" + k + r"=([^|]*)", raw) or [None, None])[1]
    # a linha do tempo da 1014: VIVA com o gatilho do 150=0/5; AJUSTANDO entre o 35=G e o 150=5; fora no 35=F
    linha = []
    for m in msgs:
        c = m.get("clordid") or ""
        if not re.match(r"KM-P-", c):
            continue
        if m["direcao"] == "REC" and m.get("exectype") in ("0", "5") and tag(m["fix_raw"], "20828"):
            linha.append((m["em_s"], "VIVA", int(tag(m["fix_raw"], "20828")), c))
        elif m["direcao"] == "ENV" and m["msgtype"] == "G":
            linha.append((m["em_s"], "AJUSTANDO", None, c))
        elif m["direcao"] == "ENV" and m["msgtype"] == "F":
            linha.append((m["em_s"], "SAINDO", None, c))
    regra = Regra(a.n, a.ticks, a.idade_max)
    t0, fim, ip, il, ultimo, k = precos[0][0], precos[-1][0], 0, 0, None, 0
    t = t0
    estado = (None, None, None)
    alertas = 0
    while t <= fim:
        while ip < len(precos) and precos[ip][0] <= t:
            ultimo = precos[ip][1]
            ip += 1
        while il < len(linha) and linha[il][0] <= t:
            estado = linha[il][1:]
            il += 1
        st, gat, cl = estado
        if st == "VIVA":
            desde = regra.passo(t, a.op, lado, gat, ultimo, True)
            if desde is not None and not sinal_de_disparo([m for m in msgs if m["em_s"] <= t], cl, desde - 1):
                alertas += 1
                print(f"{hora(t)} ALERTA: " + texto_alerta(a.op, h["operacao"]["uid"], h["operacao"]["conta"], lado, gat, ultimo, desde, t))
        k += 1
        t = t0 + k / 10
    print(f"{alertas} alerta(s) entre {hora(precos[0][0])} e {hora(fim)}")


def main():
    global SERVICO, PRECOS
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--n", type=float, default=2.0, help="segundos além do gatilho sem disparo (padrão 2)")
    ap.add_argument("--ticks", type=int, default=1, help="ticks além do gatilho (padrão 1 = 5 pts)")
    ap.add_argument("--idade-max", type=float, default=3.0, help="idade máxima do preço, s (padrão 3)")
    ap.add_argument("--intervalo", type=float, default=0.5)
    ap.add_argument("--url-arquivo", default="~/.config/kalman-vigia/telegram.url")
    ap.add_argument("--sem-telegram", action="store_true")
    ap.add_argument("--teste-telegram", action="store_true", help="manda uma mensagem de teste ao subir")
    ap.add_argument("--servico", default=SERVICO, help="(teste) o HTTP do serviço")
    ap.add_argument("--precos", default=PRECOS, help="(teste) a URL do preço")
    ap.add_argument("--replay", help="captura da Cedro (teste)")
    ap.add_argument("--op", type=int)
    ap.add_argument("--historico", help="com --replay: o JSON do histórico gravado (senão, lê do serviço)")
    ap.add_argument("--de", default="09:00:00")
    ap.add_argument("--ate", default="18:00:00")
    a = ap.parse_args()
    SERVICO, PRECOS = a.servico, a.precos
    if a.replay:
        if not a.op:
            sys.exit("--replay exige --op")
        replay(a)
    else:
        ao_vivo(a)


if __name__ == "__main__":
    main()
