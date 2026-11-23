# Outgoing webhooks

Gunkly can send a signed JSON message to a web address when something happens to a chore, so a home automation system or a script can react without polling. This is optional and **off by default**.

## Turn it on

1. An instance admin ticks **Let household owners add outgoing webhooks** under Administration, Instance settings (`webhooksEnabled` in the API). While it is off, nothing is sent and every webhook route answers `404`.
2. A household **owner** opens the household's settings, expands **Webhooks (advanced)** and adds a webhook: the address, the events and an optional description. Creating one asks for the password again (step-up).
3. Gunkly shows the **signing secret** (`whsec_...`) once. Copy it; it can not be shown again. **New secret** replaces it (the old one stops signing immediately).
4. **Send test** posts a signed `ping` event right away and shows the result in the log.

The address must be `https://`. An admin who has switched the egress guard off (to reach a service on the home network) also allows `http://`. With the guard on, addresses that point at your own machine or network (`localhost`, `127.0.0.1`, `10.x`, `192.168.x`, cloud metadata addresses and so on) are refused, both when saving and when sending. The address itself is stored encrypted and never shown again, only its host; treat it as a secret if it contains a token.

Webhooks have no API-token access: they are managed by owners in a browser session ([api/openapi.yaml](../api/openapi.yaml), tag `webhooks`). Each household can have 10 (`webhooks_per_household`).

## Events

| Type | When | `data` |
|-|-|-|
| `chore.completed` | A chore was completed (app, mark-done link, API token, assistant) | `chore`, `completion`, `actor` |
| `chore.completion_reverted` | A completion was undone or reverted | `chore`, `completion` (with `revertedAt`), `actor` |
| `chore.overdue` | A chore's due date ended without a completion (local midnight after it, household time zone) | `chore` |
| `chore.created` | A chore was created | `chore`, `actor` |
| `chore.updated` | A chore was edited | `chore`, `actor` |
| `chore.deleted` | A chore was deleted | `chore` (as it was), `actor` |
| `chore.snoozed` | The current occurrence was moved | `chore`, `actor` |
| `chore.skipped` | The current occurrence was skipped | `chore`, `actor` |
| `ping` | The test button | none |

`chore` shows the chore after the event (for `chore.completed` that means the next due date). `actor` is the member who did it; it is absent for `chore.overdue` and when a household channel's mark-done link completed the chore. There are no email addresses, notes, links or secrets in any payload.

## Request

```
POST /your/path HTTP/1.1
Content-Type: application/json
User-Agent: Gunkly-Webhook/0.4.0
Gunkly-Event: chore.completed
Gunkly-Delivery: 0192f8d2-5d9e-7c1b-9a41-6f2f0b6d7a10
Gunkly-Signature: t=1763899200,v1=5b0b0c0f...e1

{"id":"0192f8d2-5d7a-7a44-8e3b-1c0a0d9b5e22","type":"chore.completed","version":1,"createdAt":"2026-11-23T12:00:00Z","householdId":"0192f8c0-...","data":{"chore":{"id":"0192f8c1-...","name":"Dishwasher","dueDate":"2026-11-30","assignees":["0192f8c0-..."]},"completion":{"id":"0192f8d2-...","completedAt":"2026-11-23T12:00:00Z","source":"app"},"actor":{"id":"0192f8c0-...","displayName":"Sam"}}}
```

- `id`: the event, unique. `Gunkly-Delivery`: this delivery; the same on every retry. De-duplicate on either if you need exactly-once handling.
- `version`: the payload format, currently `1`. New optional fields may appear without a version change; a breaking change gets a new version.
- Times are UTC (RFC 3339). `dueDate` is a local calendar date (`YYYY-MM-DD`) in the household's time zone.

## Verify the signature

`Gunkly-Signature` is `t=<unix seconds>,v1=<hex>`, where `v1` is the HMAC-SHA256 of the string `<t>.<raw body>`, keyed with the secret exactly as shown (including the `whsec_` prefix). To verify:

1. Take `t` and `v1` from the header.
2. Compute the HMAC over `t`, a dot and the **raw request body** (the bytes as received, not re-serialised JSON).
3. Compare with `v1` in constant time.
4. Reject the request if `t` is more than **5 minutes** from your clock (replay window). Gunkly signs again on every retry, so a retry carries a fresh `t`.

Shell (OpenSSL):

```sh
# $secret = whsec_..., $header = Gunkly-Signature header, body in the file body.json
ts=${header#t=}; ts=${ts%%,*}
sig=${header##*v1=}
expected=$(printf '%s.%s' "$ts" "$(cat body.json)" | openssl dgst -sha256 -hmac "$secret" | sed 's/^.* //')
[ "$expected" = "$sig" ] && echo valid || echo invalid
```

Python:

```python
import hashlib, hmac, time

def verify(secret: str, header: str, body: bytes, tolerance: int = 300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    t, v1 = parts.get("t", ""), parts.get("v1", "")
    if not t.isdigit() or abs(time.time() - int(t)) > tolerance:
        return False
    expected = hmac.new(secret.encode(), t.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, v1)
```

Node.js:

```js
const crypto = require('node:crypto');

function verify(secret, header, body /* Buffer, the raw body */, tolerance = 300) {
  const parts = Object.fromEntries(header.split(',').map((p) => p.split('=', 2)));
  if (!/^\d+$/.test(parts.t || '') || Math.abs(Date.now() / 1000 - Number(parts.t)) > tolerance) return false;
  const expected = crypto.createHmac('sha256', secret).update(`${parts.t}.`).update(body).digest('hex');
  const a = Buffer.from(expected), b = Buffer.from(parts.v1 || '');
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}
```

## Responses, retries and the log

- Answer with any `2xx` quickly (Gunkly waits 15 seconds) and do slow work afterwards. The response body is ignored (at most 4 KiB is read).
- **Redirects are not followed**: a `3xx` counts as a failure. Give Gunkly the final address.
- `5xx`, `408`, `425`, `429`, timeouts and connection errors are retried after 1, 2, 4, 8 and 16 minutes (6 attempts in all). Other `4xx` responses fail at once.
- After **20 consecutive failed attempts** the webhook is switched off, an audit event is written and the household settings show a notice. Turn it on again after fixing the receiver; a single success resets the count.
- Each webhook keeps a log of its last 50 attempts: event, time, HTTP status, duration and a short error ("HTTP 503", "timed out", "connection failed", "destination address not allowed"). Errors never contain the address or what the receiver answered.
- Delivery is at least once. Order is not guaranteed across retries.

Design notes: [ADR 0017](adr/0017-outgoing-webhooks.md). Threats: [THREAT_MODEL.md](THREAT_MODEL.md).
