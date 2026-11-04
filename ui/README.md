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
(camelCase fields, enums as strings, int64 as strings). Example:

    fetch('/ghostseeker.v1.ConsoleService/GetQueue', {method:'POST',
      headers:{'content-type':'application/json'}, body:'{"limit":50}'})

CORS is open, so a static console can also be served from any other origin during
development and point at `http://localhost:8080` (shore) or `:8081` (edge).

The separate operator app is `cmd/ghost-seeker`, with embedded assets in
`web/static/`. It has the Live Feed, Review queue, Reprioritize workbench,
and Commander's Board, backed by its own in-memory simulation and `/api/`
endpoints. It is not yet connected to the edge/shore `ConsoleService`.
See the [operator web app instructions](../README.md#operator-web-app) to run it;
use a port such as `:8090` when shore is already using `:8080`.
