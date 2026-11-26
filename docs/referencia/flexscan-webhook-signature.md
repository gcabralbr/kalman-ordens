# Validação da assinatura dos webhooks

Quando um webhook é cadastrado com um `secret`, toda requisição `POST` enviada pelo Webhooks Notifier é assinada com HMAC-SHA256. Validando essa assinatura, o seu endpoint confirma que a mensagem foi enviada pela Investflex e que o corpo não foi alterado.

## Headers enviados

| Header               | Exemplo              | Descrição                                                                     |
|----------------------|----------------------|-------------------------------------------------------------------------------|
| `X-Iflex-Webhook-Id` | `42`                 | Id do webhook cadastrado.                                                     |
| `X-Iflex-Timestamp`  | `1764165456`         | Momento do envio, em segundos desde a época Unix (UTC).                       |
| `X-Iflex-Signature`  | `v1=5257a869e7ec...` | Prefixo da versão (`v1=`) seguido do HMAC-SHA256 em hexadecimal (minúsculo). |

> Se o webhook não tiver `secret` cadastrado, esses headers **não** são enviados.

## Como a assinatura é calculada

```
mensagem   = X-Iflex-Timestamp + "." + corpo_bruto_da_requisicao
assinatura = "v1=" + hex( HMAC_SHA256( chave = secret, mensagem ) )
```

- `secret` e `timestamp` são codificados em UTF-8.
- `corpo_bruto_da_requisicao` são os **bytes exatos** recebidos no body (JSON em UTF-8).

## Passo a passo para validar

1. Leia os headers `X-Iflex-Timestamp` e `X-Iflex-Signature`. Se algum estiver ausente, rejeite a requisição.
2. Verifique se a assinatura começa com `v1=`.
3. Verifique se o timestamp está dentro de uma janela de tolerância (sugerimos 5 minutos) em relação ao seu relógio. Isso protege contra *replay attacks*.
4. Monte a mensagem `timestamp + "." + corpo_bruto` e calcule o HMAC-SHA256 usando o seu `secret`.
5. Compare o valor calculado com o recebido usando **comparação em tempo constante**.
6. Se não bater, responda `401` e descarte a mensagem.

## Cuidados importantes

- **Use o corpo bruto.** Não faça parse do JSON e serialize de novo antes de validar: espaços, ordem das chaves e escapes mudam os bytes e a assinatura deixa de bater. Valide primeiro, faça o parse depois.
- **Comparação em tempo constante.** Use `hmac.compare_digest` (Python) ou `crypto.timingSafeEqual` (Node.js), nunca `==`.
- **Guarde o secret com segurança** (variável de ambiente, cofre de segredos). Não o versione nem o registre em logs.
- **Relógio sincronizado.** Mantenha o servidor com NTP para que a validação do timestamp funcione.

## Exemplo em Python (Flask)

```python
import hashlib
import hmac
import os
import time

from flask import Flask, abort, request

app = Flask(__name__)
WEBHOOK_SECRET = os.environ["IFLEX_WEBHOOK_SECRET"]
TOLERANCE_SECONDS = 300


def is_valid_signature(secret: str, timestamp: str, body: bytes, signature: str) -> bool:
    if not timestamp or not signature or not signature.startswith("v1="):
        return False
    try:
        if abs(time.time() - int(timestamp)) > TOLERANCE_SECONDS:
            return False
    except ValueError:
        return False

    expected = hmac.new(
        secret.encode("utf-8"),
        timestamp.encode("utf-8") + b"." + body,
        hashlib.sha256,
    ).hexdigest()
    return hmac.compare_digest("v1=" + expected, signature)


@app.post("/webhook")
def webhook():
    body = request.get_data()  # bytes brutos
    if not is_valid_signature(
        WEBHOOK_SECRET,
        request.headers.get("X-Iflex-Timestamp", ""),
        body,
        request.headers.get("X-Iflex-Signature", ""),
    ):
        abort(401)

    payload = request.get_json()
    # ... processe o payload ...
    return "", 200
```

## Exemplo em Node.js (Express)

```javascript
const crypto = require("crypto");
const express = require("express");

const app = express();
const WEBHOOK_SECRET = process.env.IFLEX_WEBHOOK_SECRET;
const TOLERANCE_SECONDS = 300;

function isValidSignature(secret, timestamp, body, signature) {
  if (!timestamp || !signature || !signature.startsWith("v1=")) return false;

  const ts = Number(timestamp);
  if (!Number.isInteger(ts) || Math.abs(Date.now() / 1000 - ts) > TOLERANCE_SECONDS) return false;

  const expected =
    "v1=" +
    crypto
      .createHmac("sha256", secret)
      .update(Buffer.concat([Buffer.from(`${timestamp}.`, "utf8"), body]))
      .digest("hex");

  const a = Buffer.from(expected, "utf8");
  const b = Buffer.from(signature, "utf8");
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}

// express.raw mantém o corpo como Buffer, sem re-serializar o JSON
app.post("/webhook", express.raw({ type: "application/json" }), (req, res) => {
  const ok = isValidSignature(
    WEBHOOK_SECRET,
    req.get("X-Iflex-Timestamp"),
    req.body,
    req.get("X-Iflex-Signature")
  );
  if (!ok) return res.sendStatus(401);

  const payload = JSON.parse(req.body.toString("utf8"));
  // ... processe o payload ...
  res.sendStatus(200);
});

app.listen(3000);
```

## Conferindo manualmente (debug)

Com o corpo salvo em `body.json` (bytes exatos recebidos):

```bash
TS=1764165456
SECRET='meu-secret'
printf '%s.' "$TS" | cat - body.json | openssl dgst -sha256 -hmac "$SECRET" -hex
```

O valor hexadecimal exibido, prefixado com `v1=`, deve ser igual ao header `X-Iflex-Signature`.
