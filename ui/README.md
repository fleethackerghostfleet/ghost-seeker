# ui/

This is the static UI directory served by `edge` and `shore` through
`internal/httpx.StaticUI`. It currently contains only this README, not an
implemented console. Both services serve this directory at `/` when started
with `-ui ui` (the default); if the directory is missing, `StaticUI` serves a
small API-hint page instead. The services expose the same JSON API:

    POST /ghostseeker.v1.ConsoleService/{ListItems|GetItem|GetQueue|GetPolicy|SetPolicy|OverrideLabel|RequestPull|GetStatus}
    GET  /blob/{item_id}/{fact|thumb|crop|full}
    GET  /healthz

Request/response bodies are protobuf-JSON of `proto/ghostseeker/v1/console.proto`
(camelCase fields, enums as strings, int64 as strings). ConsoleService and blobs
require the operator bearer token; `/healthz` is public. From the repository root
with the local edge running:

    TOKEN=$(make -s demo-token)
    curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
      -d '{"limit":50}' localhost:8081/ghostseeker.v1.ConsoleService/GetQueue

CORS is same-origin by default. An explicitly allowed development origin can be
configured with `-cors-origins` / `GS_CORS_ORIGINS`; authentication is still required.
See [Security](../docs/SECURITY.md).

The separate operator app is `cmd/ghost-seeker`, with embedded assets in
`web/static/`. It has the Live Feed, Review queue, Reprioritize workbench,
and Commander's Board, backed by its own in-memory simulation and `/api/`
endpoints by default. With `-shore http://localhost:8080` it connects to the live
shore ConsoleService through `internal/shorebridge`, and `-edge` selects the
edge console used for queue status. The browser uses a session cookie; the bridge
adds the operator bearer token server-side.
See the [operator web app instructions](../README.md#operator-web-app) to run it;
use a port such as `:8090` when shore is already using `:8080`.
