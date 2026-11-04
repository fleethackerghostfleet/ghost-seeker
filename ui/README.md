# ui/

Reserved for the operator console (HTML mock first, then whatever the UI
workstream lands). Both `edge` and `shore` serve this directory as static
files at `/` when started with `-ui ui`, and expose the same JSON API:

    POST /ghostseeker.v1.ConsoleService/{ListItems|GetItem|GetQueue|GetPolicy|SetPolicy|OverrideLabel|RequestPull|GetStatus}
    GET  /blob/{item_id}/{fact|thumb|crop|full}
    GET  /healthz

Request/response bodies are protobuf-JSON of `proto/ghostseeker/v1/console.proto`
(camelCase fields, enums as strings, int64 as strings). Example:

    fetch('/ghostseeker.v1.ConsoleService/GetQueue', {method:'POST',
      headers:{'content-type':'application/json'}, body:'{"limit":50}'})

CORS is open, so the mock can also be served from any other origin during
development and point at `http://localhost:8080` (shore) or `:8081` (edge).
