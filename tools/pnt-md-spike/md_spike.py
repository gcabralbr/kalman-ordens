#!/usr/bin/env python3
"""Protótipo (fora do serviço, só leitura): leitor do WebBroker Market Data (WB_MD) da HML da PnT.

Doc: docs/referencia/pnt-md-doc.html. OAuth2 authorization_code + PKCE (cliente público), hub
SignalR /mdhub (WebSocket, JSON). Assina LastTrade, AggrBook e SecurityStatus de um ativo e grava
cada callback em JSONL com a hora de chegada.

  .venv/bin/python md_spike.py login [--redirect-uri URI]   # 1ª vez: abre a URL no navegador e cola
                                                          # de volta a URL do redirecionamento
  .venv/bin/python md_spike.py login-auto [--usuario U] [--corretora SIMULACAO]   # preenche a tela de
                                                          # login da PnT (sem navegador)
  .venv/bin/python md_spike.py login-senha [--usuario U] [--scope "..."]   # fluxo password (ROPC):
                                                          # usuário e senha da HML no terminal
  .venv/bin/python md_spike.py run [--ativo WINZ26] [--segundos 300] [--saida ARQ]

O refresh token fica em ~/.config/pnt-md-spike/token.json (600), fora do repositório. Nada aqui
fala com o kalman-ordens nem com o Kalman.
"""
import argparse
import asyncio
import getpass
import base64
import datetime as dt
import hashlib
import json
import os
import secrets
import sys
import time
import urllib.parse
import uuid

import requests
from pysignalr.client import SignalRClient

ISSUER = "https://auth-hml.pnt.api.br/PnTAuthorizationServerProvider"
HUB = "https://webbrokerMDhml.pnt.api.br/mdhub"
CLIENT_ID = "pnt_external_gammaq"
# sem openid: o client não tem esse escopo e o authorize responde invalid_request (16/10)
SCOPE = "robot_signalr_api feeder_subscriber offline_access"
REDIRECT = "http://localhost:4200/callback"  # o do app de demonstração (mesmo client)
DIR_CFG = os.path.expanduser("~/.config/pnt-md-spike")
ARQ_TOKEN = os.path.join(DIR_CFG, "token.json")
FUSO = dt.timezone(dt.timedelta(hours=-3))
TOPICOS = ("LastTrade", "AggrBook", "SecurityStatus")


def agora_iso():
    return dt.datetime.now(FUSO).isoformat(timespec="milliseconds")


def descoberta():
    r = requests.get(ISSUER + "/.well-known/openid-configuration", timeout=15)
    r.raise_for_status()
    return r.json()


def guardar_token(t):
    os.makedirs(DIR_CFG, mode=0o700, exist_ok=True)
    t = dict(t, obtido_em=time.time())
    fd = os.open(ARQ_TOKEN, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(t, f)
    os.chmod(ARQ_TOKEN, 0o600)


def ler_token():
    with open(ARQ_TOKEN) as f:
        return json.load(f)


def login(args):
    d = descoberta()
    verificador = secrets.token_urlsafe(64)[:96]
    desafio = base64.urlsafe_b64encode(hashlib.sha256(verificador.encode()).digest()).rstrip(b"=").decode()
    estado, nonce = secrets.token_urlsafe(16), secrets.token_urlsafe(16)
    q = {"response_type": "code", "client_id": CLIENT_ID, "redirect_uri": args.redirect_uri, "scope": args.scope,
         "state": estado, "code_challenge": desafio, "code_challenge_method": "S256", "nonce": nonce}
    print("Abra no navegador e faça o login da PnT:\n")
    print(d["authorization_endpoint"] + "?" + urllib.parse.urlencode(q, quote_via=urllib.parse.quote))
    print("\nDepois do login o navegador vai para", args.redirect_uri, "com ?code=...")
    print("Cole aqui a URL completa (ou só o code) e Enter:")
    colado = sys.stdin.readline().strip()
    if "code=" in colado:
        p = urllib.parse.parse_qs(urllib.parse.urlparse(colado).query)
        if p.get("state", [estado])[0] != estado:
            sys.exit("state diferente: a URL colada não é deste login")
        if "error" in p:
            sys.exit("o servidor recusou: " + json.dumps(p))
        codigo = p["code"][0]
    else:
        codigo = colado
    r = requests.post(d["token_endpoint"], timeout=15, data={
        "grant_type": "authorization_code", "client_id": CLIENT_ID, "redirect_uri": args.redirect_uri,
        "code": codigo, "code_verifier": verificador})
    if r.status_code != 200:
        sys.exit(f"troca do code falhou: HTTP {r.status_code} {r.text[:500]}")
    t = r.json()
    t["redirect_uri"] = args.redirect_uri
    guardar_token(t)
    print(f"token guardado em {ARQ_TOKEN} (expira em {t.get('expires_in')} s; refresh_token: {'sim' if t.get('refresh_token') else 'NÃO'})")


def pkce():
    verificador = secrets.token_urlsafe(64)[:96]
    desafio = base64.urlsafe_b64encode(hashlib.sha256(verificador.encode()).digest()).rstrip(b"=").decode()
    return verificador, desafio


def login_auto(args):
    """authorization_code + PKCE sem navegador: preenche a tela de login da PnT e pega o code no
    redirecionamento para o redirect_uri (que não precisa existir). A senha é lida sem eco e não
    é gravada."""
    import re
    d = descoberta()
    usuario = args.usuario or input("usuário HML da PnT: ").strip()
    senha = getpass.getpass("senha (sem eco): ")
    verificador, desafio = pkce()
    estado, nonce = secrets.token_urlsafe(16), secrets.token_urlsafe(16)
    q = {"response_type": "code", "client_id": CLIENT_ID, "redirect_uri": args.redirect_uri, "scope": args.scope,
         "state": estado, "code_challenge": desafio, "code_challenge_method": "S256", "nonce": nonce}
    s = requests.Session()
    r = s.get(d["authorization_endpoint"], params=q, timeout=20)
    if "/Account/Login" not in r.url:
        sys.exit(f"o authorize não levou à tela de login: {r.url[:200]}")
    tok = re.search(r'name="__RequestVerificationToken" type="hidden" value="([^"]+)"', r.text)
    if not tok:
        sys.exit("tela de login sem __RequestVerificationToken")
    form = {"Username": usuario, "Password": senha, "Broker": args.corretora, "button": "login",
            "__RequestVerificationToken": tok.group(1)}
    r = s.post(r.url, data=form, timeout=20, allow_redirects=False)
    del senha, form
    codigo = None
    for _ in range(10):
        loc = r.headers.get("Location")
        if r.status_code not in (301, 302, 303) or not loc:
            break
        if loc.startswith(args.redirect_uri):
            p = urllib.parse.parse_qs(urllib.parse.urlparse(loc).query)
            if "error" in p:
                sys.exit("o servidor recusou: " + json.dumps(p))
            if p.get("state", [""])[0] != estado:
                sys.exit("state diferente no retorno")
            codigo = p["code"][0]
            break
        r = s.get(urllib.parse.urljoin(r.url, loc), timeout=20, allow_redirects=False)
    if not codigo:
        texto = re.sub(r"<[^>]+>", " ", r.text)
        erro = re.findall(r"(?:inv[aá]lid|incorret|erro|error)[^.<]{0,120}", texto, flags=re.I)[:3]
        sys.exit(f"login não devolveu o code (HTTP {r.status_code}, {r.url[:120]}): {erro}")
    r = requests.post(d["token_endpoint"], timeout=15, data={
        "grant_type": "authorization_code", "client_id": CLIENT_ID, "redirect_uri": args.redirect_uri,
        "code": codigo, "code_verifier": verificador})
    if r.status_code != 200:
        sys.exit(f"troca do code falhou: HTTP {r.status_code} {r.text[:500]}")
    t = r.json()
    t["redirect_uri"] = args.redirect_uri
    t["fluxo"] = f"authorization_code (login-auto, corretora {args.corretora})"
    guardar_token(t)
    print(f"token guardado em {ARQ_TOKEN} (expira em {t.get('expires_in')} s; refresh_token: "
          f"{'sim' if t.get('refresh_token') else 'NÃO'}; scope: {t.get('scope')})")


def login_senha(args):
    """Fluxo password (ROPC): a senha é lida sem eco e nunca gravada; guarda só os tokens."""
    d = descoberta()
    usuario = args.usuario or input("usuário HML da PnT: ").strip()
    senha = getpass.getpass("senha (sem eco): ")
    escopos = [args.scope] if args.scope else ["feeder_subscriber offline_access", "openid feeder_subscriber offline_access"]
    tentativas = []
    for escopo in escopos:
        for como in ("client_id no corpo", "Basic com segredo vazio"):
            dados = {"grant_type": "password", "username": usuario, "password": senha, "scope": escopo}
            auth = None
            if como == "client_id no corpo":
                dados["client_id"] = CLIENT_ID
            else:
                auth = (CLIENT_ID, "")
            r = requests.post(d["token_endpoint"], data=dados, auth=auth, timeout=15)
            resumo = r.text[:200].replace(senha, "***") if senha else r.text[:200]
            tentativas.append(f"  scope={escopo!r}, {como}: HTTP {r.status_code} {resumo}")
            if r.status_code == 200 and "access_token" in r.json():
                t = r.json()
                t["fluxo"] = f"password; scope={escopo}; {como}"
                guardar_token(t)
                del senha
                print("\n".join(tentativas))
                print(f"token guardado em {ARQ_TOKEN} (expira em {t.get('expires_in')} s; "
                      f"refresh_token: {'sim' if t.get('refresh_token') else 'NÃO'}; scope recebido: {t.get('scope')})")
                return
    print("\n".join(tentativas))
    sys.exit("nenhuma combinação foi aceita")


class Tokens:
    """Access token atual; renova pelo refresh token 5 min antes de expirar."""

    def __init__(self):
        self.d = descoberta()
        self.t = ler_token()

    def expira_em(self):
        return self.t["obtido_em"] + int(self.t.get("expires_in", 3600))

    def renovar(self):
        rt = self.t.get("refresh_token")
        if not rt:
            raise SystemExit("sem refresh_token: rode o login de novo")
        r = requests.post(self.d["token_endpoint"], timeout=15, data={
            "grant_type": "refresh_token", "client_id": CLIENT_ID, "refresh_token": rt})
        if r.status_code != 200:
            raise SystemExit(f"renovação falhou: HTTP {r.status_code} {r.text[:300]} (rode o login de novo)")
        novo = r.json()
        novo.setdefault("refresh_token", rt)
        novo["redirect_uri"] = self.t.get("redirect_uri")
        guardar_token(novo)
        self.t = ler_token()

    def atual(self):
        if time.time() > self.expira_em() - 300:
            # outro processo (o pnt-md-hml) pode ter renovado e girado o refresh token: relê antes
            disco = ler_token()
            if disco.get("obtido_em", 0) > self.t.get("obtido_em", 0):
                self.t = disco
            if time.time() > self.expira_em() - 300:
                self.renovar()
        return self.t["access_token"]


async def rodar(args):
    tokens = Tokens()
    tokens.atual()
    saida = open(args.saida, "a", buffering=1)
    cont = {}

    def gravar(evento, dados):
        agora = time.time()
        reg = {"chegada": agora_iso(), "chegada_ms": int(agora * 1000), "evento": evento, "args": dados}
        if evento == "OnUpdateMarketData" and len(dados) >= 4 and isinstance(dados[3], (int, float)):
            reg["atraso_ms"] = int(agora * 1000) - int(dados[3])  # chegada − serverSendUtcMs
            cont[dados[1]] = cont.get(dados[1], 0) + 1
        else:
            cont[evento] = cont.get(evento, 0) + 1
        saida.write(json.dumps(reg, ensure_ascii=False, default=str) + "\n")

    # pysignalr 1.x só põe o token no header no handshake, depois do negotiate (que leva 401 sem
    # ele): o header vai já na construção e é atualizado a cada renovação (reconexão também negocia)
    cabecalhos = {"Authorization": "Bearer " + tokens.atual()}
    cliente = SignalRClient(args.hub, headers=cabecalhos, access_token_factory=tokens.atual, max_size=64 << 20)  # 1 MB (padrão) não basta na abertura (19/10)

    def cabecalho_atual():
        token = "Bearer " + tokens.atual()
        cabecalhos["Authorization"] = token
        h = getattr(getattr(cliente, "_transport", None), "_headers", None)
        if isinstance(h, dict):
            h["Authorization"] = token
    for ev in ("OnUpdateMarketData", "OnSubscribeMarketDataError", "OnSubscribeMarketDataNotice", "OnAuthorizationError",
               "OnFeederUnavailable", "OnFeederRecovered"):
        async def h(dados, ev=ev):
            gravar(ev, dados)
            if ev in ("OnFeederUnavailable", "OnFeederRecovered", "OnSubscribeMarketDataError", "OnAuthorizationError"):
                print(agora_iso(), ev, dados, flush=True)
        cliente.on(ev, h)

    async def aberto():
        # não há auto-resubscribe: a cada (re)conexão, assina de novo
        gravar("_conectado", [])
        print(agora_iso(), "conectado; assinando", args.ativo, TOPICOS, flush=True)
        for topico in TOPICOS:
            await cliente.send("SubscribeMarketData", [str(uuid.uuid4()), args.ativo, topico])

    async def fechado():
        gravar("_desconectado", [])
        print(agora_iso(), "desconectado", flush=True)

    async def erro(m):
        gravar("_erro", [str(m)])
        print(agora_iso(), "erro:", m, flush=True)

    cliente.on_open(aberto)
    cliente.on_close(fechado)
    cliente.on_error(erro)

    async def renovador():
        while True:
            await asyncio.sleep(60)
            cabecalho_atual()  # renova se faltar menos de 5 min

    async def relogio():
        await asyncio.sleep(args.segundos)

    tarefas = [asyncio.create_task(cliente.run()), asyncio.create_task(renovador()), asyncio.create_task(relogio())]
    feito, pendente = await asyncio.wait(tarefas, return_when=asyncio.FIRST_COMPLETED)
    for t in pendente:
        t.cancel()
    for t in feito:
        if t.exception():
            print("terminou com erro:", repr(t.exception()), flush=True)
    saida.close()
    print(agora_iso(), "fim; mensagens por tópico/evento:", json.dumps(cont), flush=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    lg = sub.add_parser("login")
    lg.add_argument("--redirect-uri", default=REDIRECT, help="o redirect_uri registrado para o client")
    lg.add_argument("--scope", default=SCOPE)
    sub.add_parser("renovar", help="renova o access token pelo refresh token (teste antes de uma rodada agendada)")
    la = sub.add_parser("login-auto")
    la.add_argument("--usuario")
    la.add_argument("--corretora", default="SIMULACAO")
    la.add_argument("--redirect-uri", default=REDIRECT)
    la.add_argument("--scope", default=SCOPE)
    ls = sub.add_parser("login-senha")
    ls.add_argument("--usuario")
    ls.add_argument("--scope", help='um escopo só (ex.: "pntdata_live_resource offline_access"); sem ele, tenta os dois padrão')
    rn = sub.add_parser("run")
    rn.add_argument("--ativo", default="WINZ26")
    rn.add_argument("--segundos", type=int, default=300)
    rn.add_argument("--hub", default=HUB)
    rn.add_argument("--saida", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "var",
                                                    f"pnt-md-{dt.datetime.now(FUSO):%Y%m%d-%H%M%S}.jsonl"))
    a = ap.parse_args()
    if a.cmd == "login":
        login(a)
    elif a.cmd == "renovar":
        t = Tokens()
        t.renovar()
        print(agora_iso(), "token renovado; expira em", int(t.expira_em() - time.time()), "s")
    elif a.cmd == "login-auto":
        login_auto(a)
    elif a.cmd == "login-senha":
        login_senha(a)
    else:
        asyncio.run(rodar(a))


if __name__ == "__main__":
    main()
