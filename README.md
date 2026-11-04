# ghost-seeker
## UI mockup

`ui-mockup/index.html` is a self-contained interactive mockup of the Ghost Seeker operator UI (open it in a browser, no server needed). All imagery is synthetic.

- **Live Feed** – dense grid of prioritized images from edge sensors; click an image for detections, priority reasoning, and processing chain.
- **Review queue** – one image at a time for sleep-deprived reviewers: `1`–`4` set P1–P4, `Space` agree with model, `X` not relevant, `C` send to CDR, `→` skip, `Z` undo, `N` night (red) mode.
- **Reprioritize** – natural-language directives (keyword-based in the mockup) that rescore the feed.

## Connected web app (Go)

A Go server that receives triaged images from (simulated) edge sensors, stores them, scores them with the command model and streams them live to the operator UI, ordered by priority for review. All imagery is synthetic SVG.

```sh
go run ./cmd/ghost-seeker          # http://localhost:8080 with 6 built-in simulated sensors
go run ./cmd/ghost-seeker -sim=false &
go run ./cmd/edge-sim -server http://localhost:8080   # run sensors as a separate process
go test ./...
```

Flags: `-addr` (`:8080`), `-data` (`data/`), `-sim`, `-backfill` (frames per sensor at startup), `-ingest-token` / `GHOST_INGEST_TOKEN` (require `Authorization: Bearer <token>` on ingest).

Pipeline: each sensor captures a frame, runs a coarse edge model (noisy-OR over detections) and drops frames scoring below 0.20. Kept frames are uploaded to `POST /api/ingest` as multipart (`meta` JSON report + `image`). The server stores the blob under `data/images/`, scores it with the active command model + natural-language directives (P1–P4), persists state to `data/state.json`, logs operator reviews to `data/feedback.jsonl` and pushes events over SSE.

| Endpoint | Purpose |
| --- | --- |
| `POST /api/ingest` | Edge upload (multipart `meta` + `image`) |
| `GET /api/images?src=&limit=` / `GET /api/images/{id}` / `GET /api/images/{id}/image` | Records, sorted newest first, and image blobs |
| `POST /api/images/{id}/review` | Operator priority / dismiss / send-to-CDR |
| `POST /api/directives`, `DELETE /api/directives[/{id}]`, `PUT /api/model` | Reprioritize; rescores every stored image |
| `GET /api/config`, `GET /api/stats` | Catalog, models, active config, pipeline counters |
| `GET /api/events` | SSE: `image`, `update`, `rescore` |
