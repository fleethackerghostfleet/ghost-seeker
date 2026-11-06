# Python HTTP listener hardening (K1)

Inventory covers Python sources and listener commands in shell scripts,
Dockerfiles, Compose, Zarf and other deployment helpers. This is a source and
local-socket audit, not verification of a live deployment's exposure.

| Listener / entry point | Classification and configured reachability | Bind defaults (unchanged) | Body / framing / timeout findings and disposition |
| --- | --- | --- | --- |
| `edgeml/src/edgeml/sidecar/server.py`, launched by `edgeml.sidecar` | Product inference sidecar; optional production Zarf Deployment + ClusterIP Service (`deploy/zarf/manifests/edge-inference.yaml`), also demo Compose (`deploy/compose/demo.compose.yml`). Unauthenticated; container-network clients can reach configured all-interface listeners. | Python CLI/API `:9090` resolves to `127.0.0.1:9090`; `edgeml/Dockerfile`, Compose and Zarf explicitly use `0.0.0.0:9090`. | Existing 32 MiB cap and 413 retained. **Fixed:** no socket deadline; absent/ambiguous Content-Length accepted; negative length incorrectly reported as overflow; truncated valid JSON accepted; non-object JSON accepted or reported as internal errors depending on method. Early path/content-type rejections left unread input on keepalive connections. Internal exception text was exposed to clients. |
| `research/edgeml/src/edgeml_research/nl_priority/server.py`, launched by `edgeml_research.nl_priority.server` | Research/dev vision API, not a product service. Runs in the research Compose stack (`research/nl-priority/compose.yml`, `vision/Dockerfile`); vision has no published host port, but is reachable on its container network or when explicitly bound outside loopback. Unauthenticated. | CLI `127.0.0.1:8091`; research vision Dockerfile explicitly uses `0.0.0.0:8098`. | Existing 24 MiB cap retained. **Fixed:** overflow returned 400 instead of 413; no socket deadline; duplicate/ambiguous framing and truncated valid JSON accepted. Existing malformed JSON/type validation remains 400 and internal failures remain generic 503. |
| `inference/stub/server.py` | Reference-only Connect/JSON example; not the product sidecar. Excluded from remediation as requested. | `127.0.0.1:8090`, explicit `--host` can expose it. | Existing 32 MiB cap / 413; no socket timeout; negative lengths also return 413, absent lengths act as empty objects, and non-integer lengths / malformed JSON raise uncaught errors. Not hardened or recommended for deployment. |

No additional Python HTTP server implementations were found in `tools/`,
`scripts/` or deployment helpers. HTTP client health checks are not listeners.
Existing and new test listeners reuse the implementations above on loopback
ephemeral ports; they are not independently deployed services.

## Resulting active-listener behavior

- POST requires exactly one ASCII nonnegative decimal Content-Length, allowing
  surrounding HTTP space/tab. Missing, negative, non-integer, duplicate or
  comma-combined lengths, and any unsupported Transfer-Encoding, return 400.
  Invalid framing closes the connection without dispatching inference.
- Positive lengths above the existing caps return 413 before reading the body.
  The caps are inclusive; tests exercise both sides of the boundary with a
  reduced test-only cap, as well as the real cap's oversized length header.
- Reads must produce the declared byte count. Premature EOF returns 400 even
  when the received prefix is valid JSON. Invalid UTF-8, malformed/trailing JSON
  and non-object JSON return 400, with no traceback in the response.
- Both handlers configure the stdlib stream handler's socket timeout to 10
  seconds. Idle/incomplete headers are disconnected; idle/incomplete bodies
  return 408, close the connection and never invoke inference. These are socket
  **inactivity** timeouts, not absolute header/body deadlines: a client that
  continually trickles bytes can extend the read. They do not time out model
  computation, bound connection counts or add authentication; network isolation
  remains required. The research server remains intentionally synchronous.
- Sidecar early path/content-type rejections close the connection instead of
  interpreting unread body bytes as another request. Normal HTTP/1.1 keepalive,
  health routes, Connect error envelopes and valid RPC response shapes remain
  intact. Explicit zero-length sidecar requests still mean an empty object;
  research's existing empty-body 400 behavior is retained. Internal sidecar
  failures retain status 500 / code `internal`, but no longer expose exception
  details to clients. Server-side diagnostic logging remains available.
  Backend timeouts retain their existing 5xx handling, rather than being
  misreported as request-body timeouts.
- Bind addresses, deployment configuration, caps and model/backend behavior are
  unchanged. In particular, container all-interface bindings are needed for
  Compose/Zarf connectivity and were not silently changed to loopback.

## Regression verification

`edgeml/tests/test_sidecar_http_hardening.py` and
`research/edgeml/tests/test_http_hardening.py` use actual loopback sockets on
ephemeral ports with fake inference implementations. Partial-header/body tests
use 50 ms socket timeouts, blocking reads bounded to one second, and no sleeps.
They also cover inclusive caps, framing rejection, malformed/empty/truncated
input, recovery, health and valid API responses; the sidecar tests additionally
check keepalive and unread-body replay prevention.

Independent real-socket probes of the original `origin/dev` implementations
reproduced sidecar missing/negative length, non-object JSON and truncated-body
behavior, research's overflow/truncation behavior, and both listeners' absent
timeouts. Non-integer length controls already returned 400. Artifacts, complete
test output and production-gate output are preserved outside the repo in
`/home/ubuntu/k1/`.
