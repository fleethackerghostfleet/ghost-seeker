# Security model and controls

Ghost Seeker has three network surfaces: the **shore node** (`cmd/shore`: SyncService for
edges, ConsoleService + `/blob/` for operators), the **edge node** (`cmd/edge`: ship-side
ConsoleService + `/blob/`) and the **operator web app** (`cmd/ghost-seeker`, which fronts the
shore/edge consoles for a browser). This page lists the controls, how to run them locally with
zero extra steps, and what production must set explicitly. `docs/reports/` holds the STIG /
pentest report; the Deep scan counts are at the bottom.

## 1. Operator authentication (shore console, edge console, web bridge, blobs)

One **operator bearer token** is shared by shore, edge and the web app. It is resolved the same
way as the unit sync credential (`internal/unitauth.Credential`):

| Process | Flag | Environment | Meaning |
|---|---|---|---|
| shore / edge / ghost-seeker | `-operator-token` | `GS_OPERATOR_TOKEN` | literal token (≥ 16 bytes); **production** |
| shore / edge / ghost-seeker | `-operator-token-file` | `GS_OPERATOR_TOKEN_FILE` | token file (0600 secret mount); **production**, wins over the literal |
| shore / edge / ghost-seeker | `-dev-token-file` | `GS_DEV_TOKEN_FILE` | **local demo only**: if no explicit token is set, read or auto-generate a 256-bit token here. Default `data/operator.token`; `none` disables it |
| shore / edge | `-cors-origins` | `GS_CORS_ORIGINS` | browser origins allowed cross-origin (default: same-origin only) |

* **Shore and edge** wrap the whole `/ghostseeker.v1.ConsoleService/` prefix and `/blob/` in
  `opauth.RequireBearer`: every RPC — including ones added later, e.g. by policy signing — and
  every downlinked image needs `Authorization: Bearer <token>` or gets `401
  {"code":"unauthenticated"}` (constant-time compare). `/healthz` and the static UI stay public.
  SyncService keeps its own per-unit JWT (`-require-auth`, PR #53); shore logs a warning when it
  is off.
* **Web app**: `opauth.Sessions` wraps the entire mux. The token stays server-side; the bridge
  (`internal/shorebridge`) adds the bearer to every shore/edge call and to the `/blob/` reverse
  proxy (browser cookies and `Authorization` headers are stripped before forwarding). The
  browser gets a session cookie `gs_session` — `HttpOnly; SameSite=Strict; Path=/` (+ `Secure`
  behind TLS), 12 h, random 256-bit id, server-side session table — from:
  * `GET /login` (form) → `POST /login` with `token=<operator token>` → `303 /`;
  * **dev mode only** (token came from the dev file): a browser on **loopback** with no proxy
    headers is signed in automatically, so `make demo-ui` + `open http://localhost:8090` is
    unchanged. LAN clients still see `/login`.
  * Scripts may send `Authorization: Bearer <token>` to the web app directly.
  * Unauthenticated: `/api/*`, `/blob/*` and any non-GET → `401` JSON; pages → `302 /login`.
  * `POST /logout` clears the session; `GET /api/auth` → `{"authenticated":bool,"dev":bool}`.
* **Production**: set `GS_OPERATOR_TOKEN` (or `_FILE`) on all three processes and
  `GS_DEV_TOKEN_FILE=none` (as `deploy/compose/docker-compose.yml` does); the processes refuse to
  start without a token. Rotate by restarting with a new token.

### What the operator UI (web/static) has to do

Nothing for the happy path: `fetch('/api/shore/...')` and `<img src="/blob/...">` are same-origin,
so the cookie rides along by default (`credentials: 'same-origin'`). On a `401` from any
`/api/*` call, send the browser to `/login?next=<current path>` (dev loopback never sees one). A
"Sign out" control is `POST /logout` (a form or `fetch` + reload). Do **not** put the token in
JavaScript, query strings or `localStorage`.

## 2. CSRF / cross-origin

* **Shore / edge**: `httpx.CORSAllow` replaces the old `Access-Control-Allow-Origin: *`. A
  request with an `Origin` that is neither this host nor on `-cors-origins` gets **403** on
  any state-changing method (and on preflight) and no CORS headers on GET, so the browser blocks
  the read. `Sec-Fetch-Site: cross-site` is treated the same. Non-browser clients (no `Origin`)
  are unaffected; they need the bearer token anyway.
* **Web app**: `httpx.SameOrigin` (403 on cross-origin state changes via `Origin` /
  `Sec-Fetch-Site`) plus the `SameSite=Strict` session cookie: a third-party page cannot carry the
  operator's session at all, so no per-request CSRF token is needed. `/login`'s redirect target is
  restricted to same-site absolute paths.

## 3. Content types and CSP

* Blobs are served by `httpx.ServeBlob`: the Content-Type is **sniffed from the bytes**
  (`image/jpeg|png|webp|gif|bmp|avif`), FACT records must parse as JSON, text must be UTF-8;
  anything else is `application/octet-stream` with `Content-Disposition: attachment`. Every blob
  carries `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none';
  sandbox` and `Cache-Control: private, no-store`. The sender-declared MIME is never used for
  serving.
* Web app pages keep the strict CSP from `httpx.Secure` (`default-src 'self'`, no inline
  scripts, `frame-ancestors 'none'`, `form-action 'none'`); the login page has its own
  `default-src 'none'; form-action 'self'` policy. Blob responses proxied through the web app keep
  their own headers.

## 4. Resource limits

| Surface | Limit |
|---|---|
| Edge image decode (`edge/ingest`, `cmd/sim`) | `internal/imglimit`: `image.DecodeConfig` first, reject > 50 MP (`ErrTooLarge`) before any pixel allocation |
| Python sidecar `Analyze` | header-parsed dimensions (JPEG/PNG/GIF/BMP/WebP) must be ≤ 50 MP → else `413 resource_exhausted`; unknown containers → `400`; strict base64; body ≤ 32 MiB (existing) |
| Python sidecar `EmbedText` | ≤ 64 texts, ≤ 512 chars each → else `400 invalid_argument` |
| Sidecar / inference stub bind | `127.0.0.1` unless an interface is given (`--host 0.0.0.0`) |
| `ListItems` (shore + edge) | `limit` clamped to [1, 500] (default 100); `page_token` must be a non-negative integer ≤ 2³¹ or `invalid_argument` (fixes the negative-token panic); edge sort is now `sort.SliceStable` |
| `GetQueue` (edge) | `limit` clamped to [1, 1000] |
| `SetPolicy` interest lists (shore + edge) | ≤ 64 boost/suppress labels, ≤ 128 chars each; label filters/overrides ≤ 128 chars |
| Connect request bodies | `WithReadMaxBytes`: ConsoleService 1 MiB, SyncService 8 MiB |
| Shore blob assembly (`shore/store.WriteChunk`) | overflow-safe bounds: `offset ≤ total`, `len ≤ total − offset`, chunk ≤ 4 MiB, blob ≤ 256 MiB |
| HTTP servers (shore, edge, web app) | `ReadHeaderTimeout 10 s`, `IdleTimeout 120 s`, `MaxHeaderBytes 1 MiB` |
| Web app (`internal/server`, bridge) | existing: 64 KiB bodies on state changes, 500-char directives, ≤ 64 rules, ≤ 32 preview candidates |

## 5. Local demo (zero extra steps)

`make demo-shore`, `make demo-ui` and `make demo-edge` all pass `-dev-token-file
data/operator.token` (the `DEV_TOKEN` Make variable). The first process to start generates the
token (0600); the others read it; the web app signs a localhost browser in automatically. For
`curl`:

```sh
TOKEN=$(make -s demo-token)
curl -s -H "Authorization: Bearer $TOKEN" -H content-type:application/json -d '{}' localhost:8080/ghostseeker.v1.ConsoleService/GetStatus
curl -s -o /dev/null -w '%{http_code}\n' -d '{}' localhost:8080/ghostseeker.v1.ConsoleService/GetStatus   # 401
```

Delete `data/operator.token` to rotate. The Docker compose topology is the production posture
(explicit `GS_OPERATOR_TOKEN`, `GS_DEV_TOKEN_FILE=none`).

## 6. Residual risk / not in scope here

* No TLS inside the repo: terminate TLS in front of every listener; the operator token and
  session cookie are bearer secrets.
* The shore→edge downlink is authenticated at the transport by the unit JWT only; policy
  payload signing (Ed25519) is a separate change (`internal/policy`).
* Sessions live in process memory: a web-app restart signs everyone out (by design).

## 7. Deep scan: before / after

| Scan | Commit | High | Medium | Low |
|---|---|---|---|---|
| Baseline ([scan](https://app.devin.ai/code-scan/1ab9889fb66f418db035c105f68cecb5)) | `main@548487e` | 4 | 4 | 10 |
| After PR #60 ([scan](https://app.devin.ai/code-scan/21cd6c875f354480ba2a5b21d5a1d9f5)) | branch `655e371` | 1 | 3 | 6 |

Remaining open findings after the re-scan (7 further findings were auto-dismissed as duplicates):

* **High** — SyncService accepts unauthenticated edges by default (`-require-auth` is opt-in since
  PR #53). Decision: the Go default stays opt-in for the local `make demo-*` path; **production
  enforces it** — `deploy/compose` sets `GS_REQUIRE_AUTH=true` with `${GS_SIGNING_KEY:?}` /
  `${GS_UNIT_TOKEN:?}` and Zarf defaults `SHORE_REQUIRE_AUTH=true` with required (non-optional)
  `shore-auth`/`edge-auth` secrets, so they refuse to start without credentials. Shore logs a
  WARN at startup when unit auth is off.
* **Medium (residual, documented)** — shore items carry no owning edge, so one enrolled unit can
  overwrite or relabel another ship's contacts. Design for the follow-up: bind item ownership to
  the authenticated unit id (`sub` of the unit JWT) at `WriteManifest`/`WriteChunk`/override time
  and reject writes from a different unit (shore-side overrides by operators stay allowed).
* **Medium (dev-only, documented)** — DNS-rebinding variants of the dev-mode loopback auto-login
  and of the same-host CORS rule: dev mode requires a loopback peer *and* a loopback `Host`, and
  production (`GS_DEV_TOKEN_FILE=none`) has no auto-login at all.
* **Low** — one-byte chunks make the shore re-hash a large file per chunk; chunk uploads have no
  per-edge item-count/disk cap; the `original` full-size pull handler body is bounded only by the
  Connect read cap. (Fixed in the same PR after the scan: tab/newline in `/login?next`, the
  key-frame crop re-decode now goes through `imglimit`, JPEG fill bytes in the sidecar header
  parser.)
