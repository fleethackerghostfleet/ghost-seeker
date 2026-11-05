# Opt-in demo integration

This opt-in HTTP adapter connects IBM Granite to the demo's existing **Collection
Priorities** workbench on `demo`. Production promotion remains separate. Product
packages do not import research packages or load model weights.

## Launch

Provision the existing assets from `docs/DEMO.md`, then:

```sh
./research/nl-priority/demo.sh
# Subsequent offline start, after models and images are provisioned:
./research/nl-priority/demo.sh offline
```

Open http://localhost:8090/#reprioritize. The overlay adds pinned IBM Granite and
the research compiler on an internal network with separate state. Model ports
are not published. `GS_RESEARCH_COMPILER_URL` enables the operator adapter;
omitting the overlay retains the ordinary demo. `demo.sh down` preserves volumes.
Docker is unavailable on the implementation Mac: native execution is verified;
container startup still needs validation on a Docker-enabled host.

For native development, start Granite using `README.md`, then run separately:

```sh
go run ./research/cmd/nl-priority --listen 127.0.0.1:8097 \
  --data research/nl-priority/data/compiler --llm-url http://127.0.0.1:8099

go run ./cmd/ghost-seeker -addr 127.0.0.1:8094 \
  -research-compiler-url http://127.0.0.1:8097 \
  -backfill 180 -retain 600 -auto-cdr 0 -sim-link-bps 128000 \
  -review-log '' -dev-token-file research/nl-priority/data/demo-operator.token
```

At http://127.0.0.1:8094/#reprioritize, enter `look for fishing vessels`, review
Granite's interpretation and the priority changes, then Apply and confirm. The
recording uses the existing **simulation with synthetic catalog imagery**, actual
local Granite inference, and real policy/ranking changes. It does not establish
live detector or MobileCLIP classification accuracy.

## Scope and failure behavior

- Every compile request supplies an explicit empty research rule context; the
  standalone lab's active policy cannot leak into the demo.
- Exact supported categories adapt to existing demo classes: research `fishing
  boat` becomes demo `fishing vessel`. Existing scoring, locks and human exceptions
  remain authoritative. The live shore adapter transports both existing fishing
  category names so the sidecar's `fishing boat` label reaches the edge scorer,
  while the editable UI continues to show `fishing vessel`.
- Experimental concepts such as `big ship` block Apply instead of being broadened.
  Cached-embedding/matching integration is still needed for those labels in the
  demo; they remain available in the standalone Priority Lab.
- Clear, removal and undo use the demo's existing controls. The lab's history and
  separate queue are not imported.
- Configured inference failures, invalid output and unsupported concepts cannot
  silently fall back to another parser or change active policy.
- The language-quality limitations in `RESULTS.md` remain. This is a reviewable
  demo integration, not production promotion or a new acceptance pass.

## Checks

```sh
go test -race ./internal/command ./internal/server ./internal/shorebridge ./cmd/ghost-seeker
go test ./research/nlpriority ./research/cmd/nl-priority
node --test web/*.test.cjs
```

Tests cover preview/apply agreement through the existing scorer, exact category
adaptation, policy isolation, timeouts, malformed output, and pending/blocked/stale
preview protection. Promotion to production requires a separate user decision.
