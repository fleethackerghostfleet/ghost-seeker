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
| shore / edge | `-public-hosts` | `GS_PUBLIC_HOSTS` | extra Host names this node answers to (defaults always include loopback, IP literals, machine hostname and `-listen` host) |

* **Shore and edge** wrap the whole `/ghostseeker.v1.ConsoleService/` prefix and `/blob/` in
  `opauth.RequireBearer`: every RPC — including ones added later, e.g. by policy signing — and
  every downlinked image needs `Authorization: Bearer <token>` or gets `401
  {"code":"unauthenticated"}` (constant-time compare). `/healthz` and the static UI stay public.
  SyncService keeps its own per-unit JWT (`-require-auth`, PR #53), **on by default**: shore
  refuses to start without a ≥ 32-byte signing key, and `-require-auth=false` (isolated lab links
  only) logs a warning.
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
    The sign-in page's logo files (`/brand/logo.png`, `/brand/favicon-32.png`,
    `/brand/apple-touch-icon.png`) are served to GET/HEAD without a session.
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

## 1a. CAC/PIV mode (offline)

Optional `cmd/ghost-seeker` browser authentication uses mutual TLS; token mode remains the
default. This is a synthetic/unclassified prototype, not a claim of DoD accreditation.
Hardware smartcards and middleware were **not tested**: the browser needs the OS smartcard
middleware / PKCS#11 provider to present the card certificate. Do not export operational
private keys. Never commit real certificates, private keys, EDIPIs, or PII.

| Flag | Environment | Default / purpose |
|---|---|---|
| `-auth-mode` | `GS_AUTH_MODE` | `token`; set `piv` for certificate login |
| `-tls-cert` | `GS_TLS_CERT` | Required server PEM certificate in PIV mode |
| `-tls-key` | `GS_TLS_KEY` | Required server private key in PIV mode |
| `-piv-ca-bundle` | `GS_PIV_CA_BUNDLE` | Required PEM file or directory: self-signed roots + intermediates |
| `-piv-crl-dir` | `GS_PIV_CRL_DIR` | Required directory of DER or PEM CRLs |
| `-piv-crl-grace` | `GS_PIV_CRL_GRACE` | `0s`; optional stale-CRL grace duration |
| `-piv-reload` | `GS_PIV_RELOAD` | `1m`; CRL/users names, size and mtime polling |
| `-piv-ocsp-url` | `GS_PIV_OCSP_URL` | Empty/off; optional administrator-selected on-prem responder |
| `-piv-policy-oids` | `GS_PIV_POLICY_OIDS` | Empty; comma-separated acceptable certificate policies |
| `-piv-users` | `GS_PIV_USERS` | Required YAML/JSON identity-to-role file |

**Handshake checks:** TLS 1.2+, a client certificate, local chain trust, validity, and signatures.
Only the configured roots are trust anchors; bundled/presented intermediates are chain builders.
Go's `RequireAndVerifyClientCert` requires clientAuth EKU and rejects Microsoft
smartcard-logon-only certificates. We therefore use `RequireAnyClientCert` plus
`VerifyConnection` and explicit local `x509.Verify` with `ExtKeyUsageAny`. The acceptable-CA
list contains roots and intermediates. The server keypair and CA bundle require restart to change.

**Every HTTP request except `/healthz` and the sign-in page's logo files (`/brand/logo.png`, `/brand/favicon-32.png`, `/brand/apple-touch-icon.png`):** repeats chain verification, then requires digitalSignature
key usage and either clientAuth or Microsoft smartcard-logon EKU (`Any` alone is insufficient),
optional RFC 5280 certificate-policy chain validation, revocation for the leaf **and every
non-root intermediate**, and a users-file lookup. A viewer may GET/HEAD but cannot mutate state;
an operator has normal write access. Logout is permitted for both. UI controls/hotkeys reflect
read-only status, but server authorization is authoritative. Invalid certificates fail the TLS
handshake before HTTP; a trusted but unauthorized/revoked certificate receives a clear 403 page
or API JSON reason.

### Offline trust and revocation operations

There is **no AIA chasing, public OCSP lookup, or automatic download**. With OCSP off, authentication
does not fetch anything. If an explicit on-prem OCSP URL is configured, only that responder is
contacted (POST, short timeout, no redirects). Issuer-verified, current Good/Revoked responses are
authoritative; errors/Unknown fall back to CRLs. Configure only approved on-prem URLs and network
egress controls; the app does not classify a URL as on-prem for you.

A CRL must match the issuer and verify with the issuer certificate from the verified chain.
Bad-signature CRLs are ignored/logged; if no usable CRL remains, authentication fails closed.
Missing CRLs and stale CRLs reject access. `-piv-crl-grace` permits an expired CRL only through
`NextUpdate + grace`, with a warning. A listed revoked serial always rejects. Do not use grace
as a substitute for a reliable refresh process.

On a connected, approved machine, obtain DISA's published DoD PKI bundles, for example
**“PKI CA Certificate Bundles: PKCS#7 for DoD PKI Only”** from the
[DoD Cyber Exchange public PKI page](https://public.cyber.mil/pki-pke/pkipke-document-library/),
and the relevant issuers' published CRLs. Verify publisher checksums/signatures and provenance
using your organization's approved procedures. Transfer using approved removable media into the
air-gapped host. Convert the PKCS#7 bundle, then review and install only the intended CA trust:

```sh
openssl pkcs7 -inform DER -in dod-bundle.p7b -print_certs -out dod-bundle.pem
# For a PEM-encoded PKCS#7 input, omit -inform DER.
install -m 0644 dod-bundle.pem /etc/ghost-seeker/pki/ca-bundle/dod-bundle.pem
```

Use a scheduled sneakernet refresh (or cron copying from an approved local staging area).
Copy CRLs outside the watched directory first, validate them, then atomically rename each
complete file into the CRL directory **on the same filesystem**. Do not leave partial/temp files
inside that directory: all files there are parsed. Mount the directory, rather than individual CRL
files, into containers so renames are visible. Polling reloads CRLs/users without restart;
invalid reloads retain the previous good snapshot and log an error. If refresh stops, existing
sessions also lose access once their issuer's CRL goes stale (plus any explicit grace).

Policy allowlists should use exact OIDs from the **current DoD/FPKI certificate policy documents**
and the intended PIV-authentication certificate profiles. Different policy generations/profiles
have different identifiers: do not infer an OID from a certificate name or copy an unverified
example. Configure the selected dotted OIDs as a comma-separated allowlist; chain policy
validation must succeed for at least one configured value, not merely find it on the leaf.

### Identity, authorization, and audit

ID extraction order: SAN otherName UPN (`1.3.6.1.4.1.311.20.2.3`, UTF8String); otherwise the
trailing 10+ digits of a dotted subject CN; otherwise `sha256:<leaf DER fingerprint>`.
Display names are derived from CN (for example synthetic `DOE.JANE.Q.1000000001` → Jane Q. Doe).
The fingerprint is SHA-256 of leaf DER.

Users files are YAML or JSON maps. Only `operator` and `viewer` roles are accepted. Unknown
identities fail closed; invalid reloads retain the prior map. Fake example:

```yaml
"1000000001@mil": operator
"1000000002": viewer
```

Equivalent JSON: `{"1000000001@mil":"operator","1000000002":"viewer"}`.
The users file reloads on change at the polling interval; it is not a certificate enrollment service.

The usual `gs_session` cookie is issued automatically (Secure, HttpOnly, SameSite=Strict), and
its server-side record binds identity and certificate fingerprint. Replaying it with another
certificate rejects with 401 and clears it. Certificates and roles are still checked on **every
request**, including an existing session. `GET /login` displays identity and a Continue link;
`GET /api/auth` returns mode/id/name/role plus authenticated/dev.

`POST /logout` clears the cookie and shows: “Signed out. Remove your card or close the browser
to end the certificate session; while the card is present the browser will sign you in again.”
Removing a cookie cannot terminate the browser's smartcard/TLS credential selection.

Authentication issuance/failures, fingerprint mismatches, and state-changing HTTP requests emit
JSON audit lines to stderr (protect and retain logs as identity-bearing security records):

```json
{"level":"INFO","msg":"operator authentication","event":"request","id":"1000000001@mil","role":"operator","method":"POST","path":"/api/directives","result":"completed","status":201,"remote":"127.0.0.1:45000","reason":""}
```

Browser action `IssuedBy`/requester attribution uses the certificate ID instead of client-supplied
or hardcoded actor values. The web app **does not accept the bearer token as login in PIV mode**.
Super-admin raw policy edits still require the bearer token as a second re-auth factor on top of
an operator certificate. Shore/edge links remain on the operator bearer token **behind the
bridge**, unchanged; keep that credential server-side and protect those links as before.

### Synthetic local demo and deployment

```sh
go run ./cmd/piv-testpki -out data/piv-demo
# Prints the exact HTTPS ghost-seeker command and generates only short-lived fake PKI.
# PEM client certificates/keys are produced; x/crypto/pkcs12 has no encoder, so no .p12 export.
```

`data/` is gitignored. The generator refuses an existing output directory. Use a disposable
browser profile and import the synthetic root/client credentials using platform tooling if needed;
never mix demonstration trust with operational trust. For containers, the nonroot UID 65532
must be granted read/traverse access to mounted trust, users and server key files; generated
private files are 0600, so arrange narrowly scoped ownership/ACLs before mounting (never  world-readable keys).

Example systemd unit (paths and service account must be provisioned first):

```ini
[Unit]
Description=Ghost Seeker PIV operator bridge
After=network.target
[Service]
User=ghost-seeker
Group=ghost-seeker
EnvironmentFile=/etc/ghost-seeker/operator.env
Environment=GS_AUTH_MODE=piv GS_DEV_TOKEN_FILE=none
Environment=GS_TLS_CERT=/etc/ghost-seeker/tls/server.crt GS_TLS_KEY=/etc/ghost-seeker/tls/server.key
Environment=GS_PIV_CA_BUNDLE=/etc/ghost-seeker/pki/ca-bundle GS_PIV_CRL_DIR=/etc/ghost-seeker/pki/crl
Environment=GS_PIV_USERS=/etc/ghost-seeker/users.yaml GS_PIV_RELOAD=1m GS_PIV_CRL_GRACE=0s
ExecStart=/usr/local/bin/ghost-seeker -addr :8443 -shore http://127.0.0.1:8080 -link-listen=
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
Restart=on-failure
[Install]
WantedBy=multi-user.target
```

Keep `operator.env` restricted; it supplies `GS_OPERATOR_TOKEN` shared with shore/edge.
The HTTPS server certificate must cover the actual deployed hostname (the generator covers
localhost/127.0.0.1 only). Mount the server private key read-only and restrict read access.

The existing Dockerfile `operator` target is used by the opt-in Compose service:

```sh
export GS_PIV_TLS_DIR=/etc/ghost-seeker/tls
export GS_PIV_CA_DIR=/etc/ghost-seeker/pki/ca-bundle
export GS_PIV_CRL_DIR=/etc/ghost-seeker/pki/crl
export GS_PIV_USERS_FILE=/etc/ghost-seeker/users.yaml
# Supply GS_OPERATOR_TOKEN, GS_SIGNING_KEY and GS_UNIT_TOKEN through approved secret handling.
docker compose -f deploy/compose/docker-compose.yml --profile piv up --build
```

`ghost-seeker-piv` serves HTTPS on 8443, mounts TLS/CA/CRL/users read-only, and uses
`-shore http://shore:8080` without inserting a second link shaper. The profile does not change
shore/edge authentication or make their plain HTTP links suitable for untrusted networks.

## 2. CSRF / cross-origin

* **Host header / DNS rebinding**: shore and edge reject requests with an unknown `Host` using
  **403** before setting CORS headers. Same-origin is accepted only when the Origin host is also
  allowlisted; IP literals, loopback names, the machine hostname and `-listen` host are always
  allowed.
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

* `httpx.Secure` sends `Strict-Transport-Security: max-age=31536000` only on direct TLS requests, never on plain HTTP.
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

## 5a. Zarf network isolation

The Zarf package includes `networking.k8s.io/v1` NetworkPolicies: namespace-wide
default-deny **ingress** in the required namespace component, and explicit TCP
allows alongside each optional workload. All packaged workloads currently use
the dedicated `ghost-seeker` namespace. Same-namespace pod labels admit
operator → shore:8080/edge:8081, edge → shore:8080/inference:9090, and optional
shore text search → inference:9090. Operator-to-edge is ready for deployments
that configure it; the packaged operator currently disables that connection.

External browsers, API/sensor clients, and routed ship/shore connections use
configurable source `ipBlock`s, not pod labels from another cluster. Set
`OPERATOR_INGRESS_CIDR` (UI:8090), `SHORE_INGRESS_CIDR` (sync/API/console:8080),
`EDGE_INGRESS_CIDR` (console/sensor push:8081), and, if needed,
`INFERENCE_INGRESS_CIDR` (remote inference:9090). Defaults are documentation-only
`192.0.2.0/32`, not deployment source addresses. Use approved, narrow CIDRs as
observed after routing/NAT. See [Fielding](FIELDING.md#zarf-network-isolation)
for the traffic matrix and split-cluster setup.

These are L3/L4 rules: allowing shore TCP 8080 does not limit clients to
`/api/v1`; SyncService, ConsoleService and blobs share that listener. Authentication,
TLS termination and any path restrictions remain separate requirements. Policies
do not create ingress gateways, expose ClusterIP Services, or constrain
`kubectl port-forward` authorization. Enforcing CNI support is required; Kubernetes
API acceptance alone does not prove isolation. Other policies can add permissions.

## 6. Residual risk / not in scope here

* No TLS inside the repo: terminate TLS in front of every listener; the operator token and
  session cookie are bearer secrets.
* The unit JWT authenticates the edge to shore; it does not authenticate plaintext
  shore responses to the edge. Use the TLS boundary above for transport protection.
  Ed25519 policy payload signing is implemented in `internal/policy` and remains
  opt-in: configure shore's `-policy-signing-key` and edge's
  `-require-signed-policy` / `-policy-keys`.
* Sessions live in process memory: a web-app restart signs everyone out (by design).
* Zarf ingress rules have static validation and traffic-matrix regression tests,
  not a runtime cluster/CNI test. Egress is intentionally unrestricted: shore,
  inference and camera destinations are configurable and deployment-specific;
  model provisioning/registries and DNS also need a site inventory. A complete
  egress policy (including DNS) remains a deployment hardening task. Default-deny
  applies to every pod in `ghost-seeker`, including any extra workloads placed there.

## 7. Deep scan: before / after

| Scan | Commit | High | Medium | Low |
|---|---|---|---|---|
| Baseline ([scan](https://app.devin.ai/code-scan/1ab9889fb66f418db035c105f68cecb5)) | `main@548487e` | 4 | 4 | 10 |
| After PR #60, branch ([scan](https://app.devin.ai/code-scan/21cd6c875f354480ba2a5b21d5a1d9f5)) | branch `655e371` | 1 | 3 | 6 |
| After PR #60 merged (incremental re-run of the baseline [scan](https://app.devin.ai/code-scan/1ab9889fb66f418db035c105f68cecb5)) | `main@a9bd43e` | 3 | 0 | 5 |
| After PR #75 (unit auth on by default) + #61–#74 (incremental re-run of the [post-#60 scan](https://app.devin.ai/code-scan/21cd6c875f354480ba2a5b21d5a1d9f5)) | `main@1a6382e` | 0 | 5 | 10 |
| **Current: after PR #77–#80 (remediation of the 5 mediums + 2 escape lows)** (incremental re-run of the same [scan](https://app.devin.ai/code-scan/21cd6c875f354480ba2a5b21d5a1d9f5)) | `main@7a0b048` | **0** | 1 | 10 |

The post-#60 run re-validated the 18 baseline findings on `main@a9bd43e`: 13 resolved, 3 highs
open (one root cause — the unit-auth default on the ship-to-shore link), 5 lows.

The post-#75 run covered #61–#75 (Patrick's console on live shore data, policy layers / locks /
signing, per-directive expiry, frame ring buffer): all 3 highs resolved, 5 mediums + 10 lows open.

The current run covers the remediation of those 5 mediums and the 2 HTML-escape lows:

* **Resolved by PR #79** — edge-supplied display identifiers rendered as raw HTML in the live
  feed, Commander View and workbench (1 medium + 2 lows). Shore normalizes invalid manifest
  sources to empty, `ids.ValidName` / `ids.ValidSightingID` allowlist `[A-Za-z0-9_.:/-]`, and every
  edge-supplied string is escaped in the browser.
* **Resolved by PR #80** — caller-chosen policy `Version` on shore `SetPolicy` and the edge console
  (2 mediums). The server assigns bounded monotonic versions; unsigned console writes cannot move
  the high-water mark that guards signed command downlinks; above-ship layers keep their deadline.
* **Resolved by PR #78** — shore items had no owning edge (1 medium). Items belong to the
  authenticated unit (`UpsertManifest`, `WriteChunk`, sync-path overrides); operator overrides stay
  cross-edge; legacy unowned items are adopted by the first writer.
* **Resolved by PR #77** — `CORSAllow` trusted `Host` (DNS rebinding, 1 medium). Shore and edge
  check the `-public-hosts` / `GS_PUBLIC_HOSTS` allowlist before CORS; compose and Zarf set it.

Open findings after the current run (the three new ones were introduced by the remediation /
echelon changes themselves and are candidates for the next batch):

* **Medium ×1 (new, from PR #80)** — if the shore's own policy-signing key is in its `-policy-keys`
  keyring, a holder of the operator token can replay an old shore-signed policy to restore a
  revoked command layer (e.g. EMCON SILENT) and move the stored command mark above command's next
  versions. Fix: refuse signed above-ship submissions whose `KeyId` is the shore's own signer and
  keep the command version inside the signed command layer.
* **Low ×1 (new, from PR #78)** — first-writer ownership plus edge item ULIDs seeded from
  `math/rand(hash(edge id))` lets a stolen unit token pre-claim another edge's predictable item
  ids. Fix: `crypto/rand` entropy for the ULIDs.
* **Low ×1 (new, from PR #73)** — removing the per-operator echelon check does not fail closed; a
  deployment still setting `GS_REQUIRE_OPERATOR_AUTH=true` starts silently without it. Fix: exit
  with an error when that variable is set.
* **Low ×7 (carried over)** — SSE stream has no subscriber cap / stalled-client eviction; EMCON
  SILENT still sends a heartbeat every tick; an attribute-only suppress directive can defeat a
  command-locked priority rule; 1-byte chunks make the shore re-read and re-hash a 256 MiB blob per
  chunk; login `next=` check
  bypassed by a tab/newline; JPEG fill bytes skip the sidecar's 50-megapixel cap; chunk uploads
  have no item-count / disk cap.

Earlier residuals (post-#60 run), kept for history:

* **High ×3 (one root cause: unit auth was opt-in in the Go default)** — `SyncService` accepted
  any caller as any edge (`shore/sync/sync.go`), so an unauthenticated uplink sender could rewrite
  or relabel items, replace delivered FULL/FACT forms, and read queued operator orders; the third
  high is the downlink side of the same link (an edge applies an unsigned shore policy). **Fixed
  after the re-run**: `cmd/shore` now defaults to `GS_REQUIRE_AUTH=true` and refuses to start
  without a signing key; `-require-auth=false` is an explicit, logged opt-out for isolated lab
  links. `make demo-shore` / `make demo-edge` (and `run-shore` / `run-edge`) share a generated
  `data/unit.key` and mint a 24 h edge token per start (`data/edge.jwt`), so the demo stays
  zero-config. Production was already enforcing it — `deploy/compose` sets `GS_REQUIRE_AUTH=true` with `${GS_SIGNING_KEY:?}` /
  `${GS_UNIT_TOKEN:?}` and Zarf defaults `SHORE_REQUIRE_AUTH=true` with required
  `shore-auth`/`edge-auth` Secrets, so they refuse to start without credentials; shore logs a WARN
  at startup when unit auth is off. PR #57 added Ed25519 policy signing, but it is **opt-in**:
  shore signs only when `-policy-signing-key` (`GS_POLICY_SIGNING_KEY`) is set and the edge
  verifies only with `-require-signed-policy` / `-policy-keys` (`GS_REQUIRE_SIGNED_POLICY`,
  `GS_POLICY_KEYS`); compose and Zarf do not configure a policy key yet, so their downlink
  policies are unsigned by default — fielded deployments should set all three. Shore items are
  bound to the authenticated unit (`sub` of the unit JWT / `ExchangeRequest.edge_id`): manifests,
  chunks and sync overrides from another unit are rejected and not acked; chunk-created stubs
  belong to the sender; legacy unowned items are adopted by the first writer; operator console
  overrides stay cross-edge; and pulls/overrides route to the owning edge when `-edge` is unset.
  Follow-up: take the expected chunk hash from the owning edge's manifest, not the chunk.
* **Low — dev-mode auto-login behind a same-host reverse proxy.** The web app defaults to
  `-dev-token-file data/operator.token`; a proxy on the same host that forwards to `127.0.0.1`
  with a loopback `Host` and no forwarding headers passes the loopback check. Dev mode must never
  sit behind a proxy; production sets `GS_DEV_TOKEN_FILE=none` (compose, Zarf) and has no
  auto-login at all. Candidate fix: make dev mode an explicit flag the Makefile passes.
* **Low — EMCON SILENT expires with the policy TTL** (`edge/scheduler`, 12 h default): the edge
  falls back to the default policy and resumes transmitting. Candidate fix: keep the expired
  policy's EMCON mode and only reset interests/thresholds.
* **Low — DoS residuals**: sidecar `image_dims` treats `FF 00` as a segment and can be steered
  to a decoy SOF (fix: `OPENCV_IO_MAX_IMAGE_PIXELS=50000000` in the sidecar env plus failing
  closed on stray marker bytes); night-lights analysis cost grows with bright spots × pixels;
  directive preview can trigger very large rescoring loops. None is reachable without operator
  auth except the sidecar, which binds `127.0.0.1` by default.

Resolved by PR #60 (verified by the re-run): unauthenticated shore/edge consoles and blob
downloads, wildcard CORS and cross-site console actions, sender-chosen blob `Content-Type`
(shore, edge and operator UI), unbounded `ListItems` page sizes and the negative page-token
panic, chunk-bounds overflow, image pixel caps (edge, thumbnails, sidecar), unbounded
`EmbedText`, sidecar bind-all default, directive body caps, simulation operator API CSRF.
