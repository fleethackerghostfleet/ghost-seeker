# HTTP hardening audit

Baseline: `ece1cdde1f99044a72f7cccc743471bcab9c79b3` (`origin/dev` when
`devin/1793948647-http-hardening-audit` was created). This is a source and
regression-test audit, not a live deployment penetration test. Tables give
the resulting state and identify changes from the baseline.

## Go servers and listeners

Timeouts are seconds. `0` means no absolute deadline. A zero
`MaxHeaderBytes` field uses Go's bounded **1 MiB default**, not an unlimited
header. RHT/RT/WT/IT mean ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout.
There are no other non-test Go HTTP servers or network listeners in the tree.

| Location / consumer | RHT | RT | WT | IT | MaxHeaderBytes | Risk / disposition | Unauthenticated reachability |
|---|---:|---:|---:|---:|---|---|---|
| `internal/httpx.Server`, `cmd/edge/main.go` | 10 | 0 | 0 | 120 | 1 MiB explicit | Unchanged. Ingest has its own 30s read deadline; byte-capped unary Console handlers have no server body deadline. See inherited h2c issue below. | Headers/static UI/health; protected routes require bearer headers before their handlers consume bodies, except inherited h2c upgrade processing. |
| `internal/httpx.Server`, `cmd/shore/main.go` | 10 | 0 | 0 | 120 | 1 MiB explicit | Unchanged. SSE must not acquire an absolute write deadline. Shaped Sync transfers have airtime-dependent deadlines; a fixed 30s read deadline could truncate legitimate transfers. Stalled Sync body reads remain a residual risk. | Sync decoding happens before the unary unit-auth interceptor; Console and `/api/v1` have outer bearer auth. h2c caveat applies. |
| `cmd/ghost-seeker/main.go:serve` (simulation and live shorebridge) | 10 | 30 | 0 | 120 | 1 MiB explicit | Bounded reads; WT intentionally absent for `/api/stream`, bridge streams and proxied `/api/v1/stream`. Unchanged. Also supports TLS with the same server bounds. | Login/health/public branding; sessions/PIV protect APIs. Non-prod explicitly enabled dev modes can allow automatic authentication. |
| `cmd/rnd-grade/main.go:newHTTPServer` | 5 | **30** | **30** | **120** | **1 MiB explicit** | **Fixed:** previously only RHT was configured: slow bodies, writes and idle keep-alive connections had no deadlines. No streaming routes. RHT existed already. Explicit header setting preserves the previous effective default. | Yes, loopback by default, with Host/origin restrictions rather than token authentication; configurable listener. |
| `research/cmd/nl-priority/main.go` | 5 | 30 | 30 | 30 | 0 → 1 MiB effective | Bounded, nonstreaming research service; missing explicit MaxHeaderBytes is not an unbounded-header gap. Unchanged. | Yes; defaults to `127.0.0.1:8097`, configurable. |
| `internal/shaper.Shaper.ListenAndServe`, called by `cmd/ghost-seeker` | — | — | — | — | — | Raw TCP rate-shaping relay (`net.Listen`), not an HTTP parser/server. HTTP fields do not apply. No protocol changes made. | Depends on configured bind; intended trusted-network link. Upstream authentication remains necessary. |

`internal/server`, `internal/shorebridge`, `edge/console`, `shore/console`,
`shore/api`, `internal/rnd` and `research/nlpriority` expose handlers, not
additional listeners. Other `cmd/*` programs do not create HTTP servers.

### h2c and PR #195

The fetched baseline still imports `golang.org/x/net/http2/h2c` and uses
`h2c.NewHandler` in **both** `cmd/edge/main.go` and `cmd/shore/main.go`.
PR #195 (`devin/1793936863-go-static-analysis`) was **open**, not merged into
this `origin/dev`, when checked. Its standard-library `http.Server.Protocols`
replacement is therefore not in this branch's base. Nothing here reintroduces
h2c, and this audit does not duplicate that open PR's transport change.

This is an outstanding **high-risk inherited exception**: the h2c upgrade
wrapper reads the first HTTP/1 upgrade body before the mux's auth and byte
caps. The per-handler caps below do not repair that path. Land #195 before
claiming first-request transport hardening is complete.

### Read-timeout policy on the shaped link

Do not interpret IdleTimeout as an active-body deadline. Shared edge/shore
servers currently have no absolute ReadTimeout. In particular, the shore
SyncService is byte bounded but unauthenticated callers can stall a body
before its unary auth interceptor executes. `edge/scheduler/scheduler.go`
calculates an exchange deadline as three times expected airtime (minimum
10s); there is no fixed maximum. A blanket 30s server limit is not equivalent
and was deliberately not introduced. A transport idle-progress/auth-before-
decode design remains follow-up work, not a protection this patch claims.

## Inbound body-reader inventory

For production entrypoints, "No" means auth runs **before the listed
handler** reads the body, not that the socket is inaccessible. The legacy
h2c upgrade exception above still applies to edge/shore. Simulation handlers
are not wired into the prod executable; direct package-handler users must
supply the documented outer authentication.

| Location / handler(s) | Cap / decoder state after this change | Risk / finding | Reachable unauthenticated? |
|---|---|---|---|
| `internal/opauth/session.go:Sessions.login` | 4 KiB MaxBytesReader before ParseForm | **Fixed:** overflow now 413 rather than 400; successful/failed credential flows unchanged. No cookie on overflow. | **Yes**, sign-in is necessarily public. PIV mode authenticates differently. |
| `edge/ingest/live.go:Queue.Handler` → `readFrame`, multipart/image readers | Configurable cap, default 32 MiB; MaxBytesReader plus 30s request read deadline, bounded queue reservation, final body drain; metadata separately bounded at 64 KiB, JSON unmarshaled | Existing 413 for outer-body overflow; image and multipart reads use the same capped reader. No change. Structural/metadata validation errors remain 400. | No: operator/unit token checked before reservation/body parsing. |
| `edge/console/ship.go:Service.ShipPolicyHandler` POST | 64 KiB MaxBytesReader; bounded ReadAll then json.Unmarshal | **Fixed:** single Decode accepted valid prefixes followed by oversized whitespace, extra JSON or junk. Now whole input is consumed within the cap, overflow is 413 and trailing input is 400, before policy mutation. Unknown-field behavior preserved. | No: `cmd/edge` outer RequireBearer. |
| `cmd/edge` + `cmd/shore`: generated ConsoleService unary handlers | `connect.WithReadMaxBytes(1<<20)` | Existing protocol-level resource-exhausted response, not plain REST 413. Connect's own codec/framing, not the application's json.Decoder. No change. | No: outer RequireBearer before decoding. |
| `cmd/shore`: generated SyncService.Exchange | `connect.WithReadMaxBytes(8<<20)`, compressed/framed protocol handled by Connect | Existing finite message cap; transport resource-exhausted semantics preserved. Auth interceptor is inside unary dispatch, after decoding. See timeout residual above. | **Yes for bounded decoding**, before unit-auth rejects; successful Sync requires unit auth when enabled. |
| `internal/server/server.go`: `addDirective`, `compileDirective`, `setModel`, `uploadOverride`, `patchItem`, `decide`, `fullRes` | Each 64 KiB MaxBytesReader + strict decodeJSON (unknown-field rejection and EOF check) | Existing overflow-to-413 helper also preserves wrapped trailing-overflow errors. No change. | No in executable: outer Sessions/PIV; non-prod dev modes are explicit exceptions. |
| `internal/server/policy.go:setPolicyEMCON` | 64 KiB + strict decodeJSON | Existing 413/EOF enforcement, unchanged. | No, outer Sessions/PIV. |
| `internal/server/policy.go:validatePolicy`, `applyAdminPolicy` → `decodeProtoPolicy` | 2 MiB + **one sentinel byte**, reject excess as MaxBytesError; protojson.Unmarshal rejects trailing values/junk | **Fixed:** plain LimitReader at exactly 2 MiB could silently accept a truncated valid JSON prefix; now 413 for overflow, including trailing whitespace. Exactly 2 MiB remains valid input. | No: requireAdmin before any read, in addition to outer auth. |
| `internal/server/preview.go:previewDirectives` | 512 KiB MaxBytesReader + strict decodeJSON | Existing 413/EOF enforcement, unchanged. | No, outer Sessions/PIV. |
| `internal/server/review_sync.go:reviewSyncCreate`, `reviewSyncMutate` (vote/resolve) | 16 KiB MaxBytesReader + strict decodeJSON | Existing 413/EOF enforcement, unchanged. | No, outer Sessions/PIV. |
| `internal/server/reviews.go:reviewItem`, `correctReview` (reason/undo) | 64 KiB MaxBytesReader + strict decodeJSON | Existing 413/EOF enforcement, unchanged. | No, outer Sessions/PIV. |
| `internal/server/demo_reset_demo.go:resetDemo` | 64 KiB MaxBytesReader + strict decodeJSON; route only under !prod and explicit reset option | **Fixed:** decoding overflow no longer collapsed into the confirmation error's 400. Overflow 413; bad confirmation remains 400. | Only in explicitly permissive non-prod auth configurations; otherwise Sessions/PIV. |
| `internal/shorebridge/bridge.go:setPolicy`, `requestPull`, `override` | Each 64 KiB MaxBytesReader + decodeJSONBody (EOF check) | **Fixed:** existing byte caps returned 400 on overflow; now 413. Malformed/trailing input stays 400. | No, live operator's Sessions/PIV wrapper. |
| `internal/shorebridge/bridge.go:setEMCON` (also mounted by Console as `/api/policy/emcon`) | 4 KiB MaxBytesReader + decodeJSONBody | **Fixed:** overflow now 413, validation errors still 400. | No, Sessions/PIV. |
| `internal/shorebridge/console.go`: `patchItem`, `decide`, `fullRes`, `pull`, `addDirective`, `compile` | Each 64 KiB MaxBytesReader + decodeJSONBody | **Fixed:** overflow now 413, malformed/trailing input still 400. Existing permissive unknown-field policy preserved. | No, Sessions/PIV. |
| `internal/shorebridge/console.go:preview` | 256 KiB MaxBytesReader + decodeJSONBody | **Fixed:** overflow now 413. | No, Sessions/PIV. |
| `internal/shorebridge/reviews.go:reviewItem`, `correctReview` (reason/undo) | 64 KiB MaxBytesReader + strict decodeReviewBody | **Fixed:** EOF check used to erase the trailing MaxBytesError and return 400. It now preserves that error, yielding 413. First-decode overflow was already 413. | No, Sessions/PIV. |
| `internal/shorebridge/link_demo.go:setLink` | 4 KiB MaxBytesReader + decodeJSONBody; only !prod and explicit WithLinkOverride | **Fixed:** split decode error from semantic validation so overflow is 413, invalid rate remains 400. | Only explicitly permissive non-prod auth configurations; otherwise Sessions/PIV. |
| `internal/rnd/http.go:/api/grade` | 4 KiB MaxBytesReader + DisallowUnknownFields + EOF check | **Fixed:** both first-value and trailing-whitespace overflow now 413 rather than 400. Invalid requests cannot save a grade. | **Yes**, constrained by Host/origin checks, default loopback bind. |
| `research/nlpriority/http.go:decode`, used by POST `/api/compile`, `/api/preview`, `/api/apply`, `/api/undo`, `/api/items` | 50 MiB MaxBytesReader + DisallowUnknownFields + EOF check | **Fixed:** first/trailing overflow now 413; malformed/trailing JSON remains 400. Existing large research cap retained. | **Yes**, subject to an Origin-to-Host match check when Origin is present (not a Host allowlist); default loopback bind. |

The bridge's reverse proxy mounts are **GET-only** (`/blob/`, `/api/v1/`),
behind Sessions/PIV, forwarding to the configured shore. No JSON mutation
handler or upload API is exposed by that proxy. Its transport can forward a
GET request body without materializing it locally; upstream API handlers do
not interpret it. The operator's existing 30s ReadTimeout still applies.

`POST /logout`, undo controls without decoded inputs, DELETE controls and
read-only endpoints do not explicitly consume application request bodies.

## Streaming and outbound HTTP

No existing stream/server write deadline was changed. SSE producers are
`internal/server.Server.stream`, `internal/shorebridge.Bridge.stream`,
`internal/shorebridge.Console.stream` and `shore/api.API.stream`. Shore's
versioned stream already refreshes its per-write 10s deadline rather than
using an absolute server WT. Bridge reverse-proxy streaming remains intact.
A new Console regression flushes both snapshot and update and checks cleanup;
the default/prod suites also exercise the existing versioned stream contract.

MJPEG is an **outbound camera client**, not an additional HTTP listener:
`edge/ingest/mjpeg.go` has per-read idle deadlines and per-frame byte limits.
Neither its transport nor its frame-processing code changed. Existing MJPEG
ingestion tests are retained. Outbound response reads in `internal/command`
(research/LLM), `internal/pivauth` (CRL fetch) and `internal/shorebridge`
(RPC/blob fetch) are not incoming handlers and already have reader limits.
JSON decoders of on-disk grader/testset/log files are likewise not HTTP reads.

## Additional non-Go listeners (inventory only)

The Go timeout field review above does not imply that Python's stdlib
servers have equivalent deadlines. These were noted but not changed in this
Go-focused pass; deployment exposure/socket-idle policy needs separate review.

| Location | Current state | Risk | Unauthenticated? |
|---|---|---|---|
| `edgeml/src/edgeml/sidecar/server.py:make_server` | ThreadingHTTPServer; POST Connect/JSON reads Content-Length bounded to 32 MiB, 413 above cap; json.loads rejects trailing input; default `:9090` resolves to loopback `127.0.0.1:9090`, CLI can override bind | No handler socket timeout configured; a stalled accepted request can hold a thread. Per-line/header-count limits are Python defaults, not Go's 1 MiB bound. | Yes, service trusts its network boundary; no bearer check in handler. |
| `research/edgeml/src/edgeml_research/nl_priority/server.py:main`, `handler_for` | HTTPServer, default loopback `127.0.0.1:8091`; `/embed`, `/classify`, `/image-embed` read Content-Length capped at 24 MiB; json.loads rejects trailing input; overflow returns 400 | No socket timeout; synchronous listener can be occupied by one stalled client. Oversize status is still 400 here. Not a deployed Go API. | Yes; local research service. |

## Regression coverage

* `cmd/rnd-grade/http_server_test.go`: explicit timeout/header configuration,
  real TCP incomplete-header and incomplete-body cutoff. Tests shorten server
  deadlines to 25ms and use 900ms client/test bounds, with **no sleeps**.
* `internal/httpx/body_test.go`: direct/wrapped MaxBytesError and ordinary errors.
* `edge/console/ship_body_test.go`: oversized value/whitespace, second JSON,
  junk and empty input; existing ship tests cover successful policy application.
* `internal/server/policy_body_test.go`: both actual admin routes, exact-limit
  acceptance, oversized valid-prefix suffix/value, empty/malformed/trailing JSON.
* `internal/opauth/body_limits_test.go`: unauthenticated login overflow cannot
  issue cookies; session and bearer auth reject protected requests without reading.
* `internal/rnd/body_limits_test.go`: both decode phases return 413, grades unchanged.
* `internal/shorebridge/body_limits*_test.go`: changed live routes plus review
  reason/undo, oversized first/trailing values, empty/malformed/trailing input;
  isolated demo-link test and Console SSE flush/cleanup test.
* `internal/server/body_limits_demo_test.go`: demo reset trailing overflow.
* `research/nlpriority/body_limits_test.go`: all five decode-backed routes,
  real 50 MiB overflows and malformed/empty/trailing requests; no model calls.

Existing normal/error-flow tests remain enabled. The existing oversized EMCON
assertion was updated from 400 to the deliberately changed 413; error text stays
unchanged. No CI/build settings, production JavaScript or transport protocols
were changed.
