#!/usr/bin/env python3
"""Sondagem do circuit breaker na Flexscan (26/10), direto na API, fora do kalman-ordens.

Pergunta (item 3 da conversa de 26/10): durante a suspensão, a B3/Flexscan aceita PUT de uma
stop-limite, POST de uma limite e DELETE das duas? Cada pedido, cada resposta, o retrato
(GET /orders/{id}) depois de cada ação e a leitura da 9090 no instante vão para
var/flex-cb-AAAAMMDD/registro.jsonl. Os webhooks dessas ordens chegam ao kalman-ordens (a 9098 é
dele), que grava todo corpo em kalmanordemevento (msgtype FLX-WH) antes de filtrar: as ordens
daqui têm name CB-..., sem KM-, e a ponte as ignora (não concilia, não cancela).

LOGIN: no máximo UM, pelo subcomando `login` (o token fica em var/flex-cb-AAAAMMDD/token, 0600).
Nenhum outro subcomando faz login: 401 = para e avisa. Ou FLEX_TOKEN no ambiente (zero login).
Senha e token nunca são impressos nem gravados no registro.

Subcomandos (na ordem de uso):
  login                 um login (FLEXSCAN_USUARIO/FLEXSCAN_SENHA do ambiente, ou pergunta)
  checar                GET /orders/filter da conta (confere o token; não cria nada)
  stop                  POST stop-limite de VENDA 1 contrato, gatilho = ref × (1 − STOP_PCT)
  suspensao             durante a suspensão: PUT da stop → POST limite de compra longe →
                        DELETE da limite → DELETE da stop, com retratos entre cada passo
  limpar                DELETE de toda ordem CB- viva da conta (rede de segurança)
"""
import argparse, datetime as dt, getpass, json, os, sys, time, urllib.error, urllib.request

URL = os.environ.get("FLEXSCAN_URL", "https://apisim.flexscan.com.br/v1").rstrip("/")
PRECOS = os.environ.get("PRECOS_9090", "http://172.16.110.53:9090/precos/atual?ativo=WINFUT")
RAIZ = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "var")
DIR = os.environ.get("FLEX_CB_DIR") or os.path.join(RAIZ, "flex-cb-" + dt.date.today().strftime("%Y%m%d"))
TOKEN_ARQ = os.path.join(DIR, "token")
ESTADO_ARQ = os.path.join(DIR, "estado.json")
REG_ARQ = os.path.join(DIR, "registro.jsonl")
TICK = 5


def agora():
    return dt.datetime.now().astimezone().isoformat(timespec="milliseconds")


def registrar(tipo, **campos):
    os.makedirs(DIR, exist_ok=True)
    with open(REG_ARQ, "a") as f:
        f.write(json.dumps({"em": agora(), "tipo": tipo, **campos}, ensure_ascii=False) + "\n")


def ler_9090():
    try:
        with urllib.request.urlopen(PRECOS, timeout=1) as r:
            return json.loads(r.read()).get("WINFUT") or {}
    except Exception as e:
        return {"_erro": f"{type(e).__name__}: {e}"}


def estado():
    try:
        return json.load(open(ESTADO_ARQ))
    except FileNotFoundError:
        return {}


def salvar_estado(e):
    os.makedirs(DIR, exist_ok=True)
    json.dump(e, open(ESTADO_ARQ, "w"), indent=1)


def ler_token(s):
    """O token puro ou o JSON que o `login` grava ({"token": ..., "em": ...}): aceita os dois (26/10:
    FLEX_TOKEN=$(cat token) levava o JSON inteiro para o Authorization)."""
    s = s.strip()
    if s.startswith("{"):
        return json.loads(s)["token"]
    return s


def token():
    t = os.environ.get("FLEX_TOKEN")
    if t:
        return ler_token(t)
    try:
        return ler_token(open(TOKEN_ARQ).read())
    except FileNotFoundError:
        sys.exit("sem token: rode `login` (um só) ou exporte FLEX_TOKEN")


class Sem401(Exception):
    pass


def pedir(metodo, caminho, corpo=None, rotulo=""):
    """Um pedido autenticado. Nunca faz login: 401 levanta Sem401."""
    dados = json.dumps(corpo).encode() if corpo is not None else None
    req = urllib.request.Request(URL + caminho, data=dados, method=metodo)
    req.add_header("Authorization", "Bearer " + token())
    if dados is not None:
        req.add_header("Content-Type", "application/json")
    px = ler_9090()
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=5) as r:
            st, b = r.status, r.read()
    except urllib.error.HTTPError as e:
        st, b = e.code, e.read()
    except Exception as e:
        st, b = 0, f"{type(e).__name__}: {e}".encode()
    ms = int((time.time() - t0) * 1000)
    try:
        resp = json.loads(b)
    except Exception:
        resp = b.decode("utf-8", "replace")[:2000]
    registrar("pedido", rotulo=rotulo, metodo=metodo, caminho=caminho, corpo=corpo, status=st, ms=ms, resposta=resp, p9090=px)
    print(f"{agora()[11:23]} {rotulo:<22} {metodo} {caminho[:60]} → {st} ({ms} ms)  status84={px.get('status84')} preco={px.get('preco')}")
    if st == 401:
        raise Sem401(f"{rotulo}: 401 — o token não vale mais (um login do serviço derrubou?). Sem login automático: pare e avise.")
    return st, resp


def retrato(id_, rotulo, vezes=(0.5, 2.0)):
    """GET /orders/{id} em t+0,5 s e t+2 s; mostra o essencial."""
    passado = 0.0
    for t in vezes:
        time.sleep(max(0, t - passado))
        passado = t
        st, r = pedir("GET", "/orders/" + id_, rotulo=f"{rotulo} GET+{t:g}s")
        if isinstance(r, dict):
            print(f"      orderStatus={r.get('orderStatus')} price={r.get('price')} stopPrice={r.get('stopPrice')} "
                  f"exec={r.get('quantityExecuted')} clxRejReason={r.get('clxRejReason')} text={str(r.get('text'))[:120]!r}")


def ref_preco(args):
    if args.ref:
        return args.ref, args.simbolo
    p = ler_9090()
    v = p.get("preco") or p.get("bid") or p.get("ask")
    if not v:
        sys.exit(f"sem preço na 9090 ({p}); passe --ref PONTOS")
    return int(v), args.simbolo or p.get("ticker")


def tick(v, para_baixo=True):
    return (int(v) // TICK) * TICK if para_baixo else -(-int(v) // TICK) * TICK


# ------------------------------------------------------------------ subcomandos

def cmd_login(args):
    if os.path.exists(TOKEN_ARQ) and not args.forcar:
        sys.exit(f"já há token de hoje em {TOKEN_ARQ}: sem segundo login (use --forcar só com certeza)")
    usuario = os.environ.get("FLEXSCAN_USUARIO") or input("usuário Flexscan: ")
    senha = os.environ.get("FLEXSCAN_SENHA") or getpass.getpass("senha Flexscan: ")
    registrar("login", usuario=usuario)  # antes de ir à rede: a tentativa conta mesmo se falhar
    req = urllib.request.Request(URL + "/connect/login", data=json.dumps({"user": usuario, "psswd": senha}).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            st, b = r.status, r.read()
    except urllib.error.HTTPError as e:
        st, b = e.code, e.read()
    try:
        d = json.loads(b)
    except Exception:
        d = {}
    tok = d.get("auth_token")
    if st != 200 or not tok:
        d.pop("auth_token", None)
        registrar("login_falhou", status=st, resposta=d)
        sys.exit(f"login falhou ({st}): {d}. NÃO tente de novo sem conferir o motivo (bloqueio de senha).")
    os.makedirs(DIR, exist_ok=True)
    fd = os.open(TOKEN_ARQ, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump({"token": tok, "em": agora()}, f)
    exp = ""
    try:
        import base64
        p = tok.split(".")[1]
        exp = dt.datetime.fromtimestamp(json.loads(base64.urlsafe_b64decode(p + "=" * (-len(p) % 4)))["exp"]).astimezone().isoformat()
    except Exception:
        pass
    registrar("login_ok", expira=exp)
    print(f"login ok; token em {TOKEN_ARQ} (expira {exp or 'desconhecido'})")
    print("confira agora o /saude do kalman-ordens (flexscan.logado, ultimo_erro) e rode `checar`.")


def cmd_checar(args):
    st, r = pedir("GET", f"/orders/filter?account={args.conta}&limit=1000&offset=0&detailed=1", rotulo="checar")
    lst = r if isinstance(r, list) else (r.get("records") or r.get("orders") or []) if isinstance(r, dict) else []
    for o in lst:
        if str(o.get("name", "")).startswith("CB-"):
            print(f"  {o.get('name')} {o.get('id')} orderStatus={o.get('orderStatus')} price={o.get('price')} stopPrice={o.get('stopPrice')}")
    return lst


def cmd_stop(args):
    e = estado()
    if e.get("stop_id") and not args.forcar:
        sys.exit(f"já há stop CB de hoje ({e['stop_id']}); `limpar` antes, ou --forcar")
    ref, simbolo = ref_preco(args)
    gat = tick(ref * (1 - args.stop_pct / 100))
    lim = gat - args.stop_folga
    nome = "CB-P-" + dt.datetime.now().strftime("%H%M%S")
    print(f"ref {ref} {simbolo}: stop de VENDA 1, gatilho {gat}, limite {lim} ({args.stop_pct}% abaixo)")
    corpo = {"symbol": simbolo, "side": "2", "qtty": 1, "account": args.conta, "exchange": "XBMF", "timeInForce": "0",
             "name": nome, "ordType": "4", "stopPrice": gat, "price": lim}
    st, r = pedir("POST", "/orders", corpo, rotulo="stop POST")
    if not isinstance(r, dict) or not r.get("id"):
        sys.exit(f"POST da stop sem id ({st}): {r}")
    e.update(stop_id=r["id"], stop_nome=nome, stop_gatilho=gat, stop_limite=lim, simbolo=simbolo)
    salvar_estado(e)
    retrato(r["id"], "stop")


def cmd_suspensao(args):
    e = estado()
    if not e.get("stop_id"):
        sys.exit("sem stop CB de hoje: rode `stop` antes")
    registrar("suspensao_inicio", p9090=ler_9090())
    sid = e["stop_id"]
    retrato(sid, "1 stop antes", vezes=(0,))
    time.sleep(args.pausa)

    gat, lim = e["stop_gatilho"] - args.put_pts, e["stop_limite"] - args.put_pts
    pedir("PUT", "/orders/" + sid, {"price": lim, "stopPrice": gat}, rotulo="2 stop PUT")
    retrato(sid, "2 stop")
    time.sleep(args.pausa)

    ref, simbolo = ref_preco(args)
    px = tick(ref * (1 - args.lim_pct / 100))
    nome = "CB-L-" + dt.datetime.now().strftime("%H%M%S")
    st, r = pedir("POST", "/orders", {"symbol": simbolo or e["simbolo"], "side": "1", "qtty": 1, "account": args.conta,
                                      "exchange": "XBMF", "timeInForce": "0", "name": nome, "ordType": "2", "price": px},
                  rotulo="3 limite POST")
    lid = r.get("id") if isinstance(r, dict) else None
    if lid:
        e.update(lim_id=lid, lim_nome=nome)
        salvar_estado(e)
        retrato(lid, "3 limite")
    time.sleep(args.pausa)

    if lid:
        pedir("DELETE", "/orders/" + lid, rotulo="4 limite DELETE")
        retrato(lid, "4 limite")
        time.sleep(args.pausa)
    pedir("DELETE", "/orders/" + sid, rotulo="5 stop DELETE")
    retrato(sid, "5 stop", vezes=(0.5, 2.0, 5.0))
    registrar("suspensao_fim", p9090=ler_9090())
    print("\nfim. Se alguma ficou viva (DELETE parado até a reabertura): rode `checar` a cada minuto e `limpar` na reabertura.")


def cmd_limpar(args):
    for o in cmd_checar(args):
        if str(o.get("name", "")).startswith("CB-") and str(o.get("orderStatus")) in ("0", "1", "5", "A", "6", "E"):
            pedir("DELETE", "/orders/" + o["id"], rotulo="limpar DELETE")
            retrato(o["id"], "limpar", vezes=(1.0,))


def main():
    a = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    a.add_argument("cmd", choices=["login", "checar", "stop", "suspensao", "limpar"])
    a.add_argument("--conta", default=os.environ.get("FLEX_CONTA", "S-1117150"))
    a.add_argument("--simbolo", default=None, help="padrão: o ticker da 9090")
    a.add_argument("--ref", type=int, default=None, help="preço de referência em pontos (padrão: a 9090)")
    a.add_argument("--stop-pct", type=float, default=15.0, help="gatilho da stop abaixo da ref, %% (padrão 15)")
    a.add_argument("--stop-folga", type=int, default=100, help="limite = gatilho − folga (padrão 100)")
    a.add_argument("--put-pts", type=int, default=100, help="o PUT afasta gatilho e limite em N pts (padrão 100)")
    a.add_argument("--lim-pct", type=float, default=5.0, help="limite de compra abaixo da ref, %% (padrão 5)")
    a.add_argument("--pausa", type=float, default=2.0, help="segundos entre os passos da suspensão")
    a.add_argument("--forcar", action="store_true")
    args = a.parse_args()
    registrar("cmd", cmd=args.cmd, args={k: v for k, v in vars(args).items() if k != "cmd"}, url=URL)
    try:
        {"login": cmd_login, "checar": cmd_checar, "stop": cmd_stop, "suspensao": cmd_suspensao, "limpar": cmd_limpar}[args.cmd](args)
    except Sem401 as e:
        registrar("parou_401", motivo=str(e))
        sys.exit(str(e))


if __name__ == "__main__":
    main()
