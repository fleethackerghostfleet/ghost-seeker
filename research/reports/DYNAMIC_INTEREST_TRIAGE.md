# dynamic-interest → ghost-seeker triage

`fleethackerghostfleet/dynamic-interest` is the parallel Python prototype (CPU-only DDIL
uplink prioritizer: NL → "Mission Card" compiler, EMCON states, precedence, SQLite WAL
ledger, lossy transport sim, night COLREGS lights, sailor console, Zarf packaging).
ghost-seeker is the integrated platform; this file records, per dynamic-interest
component and PR, whether it was already covered here, ported, or dropped, and where it
landed. Nothing was merged into dynamic-interest `main`; its open PRs were closed with a
pointer to the ghost-seeker PR that carries the useful part.

Verdicts: **covered** = ghost-seeker already had an equivalent, **ported** = carried over
(PR number), **dropped** = not useful for the ghost-seeker demo. ghost-seeker PR numbers
(#54, #55, #56, #57) refer to the in-flight PRs that carry the port; at the time of writing
they were validated against `main` but not yet merged, so the files they cite are not in this
tree until they land.

## dynamic-interest `main` (merged PRs #1, #2, #3, #7, #10)

| Component (path) | Verdict | Where it landed / why |
|---|---|---|
| NL → Mission Card compiler (`ship/compiler`: rules parser, local LLM backends, JSON-schema draft validation, vocab snapping, rules fallback, readback, guardrails, eval set) | **ported** | ghost-seeker **#54** `internal/command/{compile,llm,prompt,readback,guardrails}.go`; `POST /api/directives/compile`; readback/warnings in the sim app confirm dialog and in live shore mode (`make demo-ui` directive box, `shore.html`). Eval set translated to ghost-seeker labels (`internal/command/testdata/eval_set.json`, 39/48 rules-path floor). |
| EMCON phrases in directives (`vocab.json` `emcon_phrases`: go quiet / radio silence / burst mode / resume normal …) | **ported** | **#54** `internal/command/emcon.go` `ParseEmcon`; the shore bridge sets `Policy.Emcon` from the directive text and `shore.html` shows `EMCON SILENT/BURST`. |
| EMCON states SILENT / BURST / NORMAL (`scheduler/emcon.py`) | **covered** | `proto/ghostseeker/v1/policy.proto` `EmconMode`, `edge/scheduler` (SILENT → empty plan, BURST → 8 KB FACT-only budget), `EmconSetting` + echelon locks from #52; operator EMCON buttons + lock display in **#57**. |
| Precedence ROUTINE / PRIORITY / IMMEDIATE / FLASH (`scheduler/score.py`, `planner.py` shares) | **dropped** | ghost-seeker ranks by continuous score + weighted rules and the FACT/THUMB/CROP/FULL form ladder; a 4-level precedence enum adds nothing the weight field does not already express (see #57 "Precedence comes from rule weight"). |
| Policy echelon merge, lock/delegate, last-good store, lint (`policy/merge.py`, `store.py`, `lint.py`) | **ported** | **#57** `internal/policy` (Merge/Flatten/Locks/Store), edge `AcceptPolicy`, shore versioning, audit table. |
| Ed25519 policy signing (`transport/signing.py`) | **ported** | **#57** `policy.Sign/Verify`, `cmd/policy-keygen`, `-require-signed-policy`. |
| SQLite WAL ledger (`scheduler/queue.py`: items, offsets, acks, idempotency keys) | **covered** | `edge/store` (SQLite, resumable per-form offsets, dedup by sighting/hash) and `shore` store; chunked resumable sync already exists. |
| Planner byte budgets / fair shares (`scheduler/planner.py`) | **covered** | `edge/scheduler` budget per tick from the link estimator; EMCON caps; policy `Limits`. |
| Lossy transport sim, shaping, traces (`transport/{base,shaping,http,traces}.py`) | **covered** | `internal/shaper` (rate + up/down, used by `make demo-ui` link buttons), Toxiproxy option, `internal/bench` virtual-time link; `transport/control.py` (link control API) ≈ `/api/link`. |
| Metrics matrix / trace comparison (`metrics/compare.py`: time-to-first-FLASH, max wait, duplicates, bytes wasted) | **covered / dropped** | `make demo` → `scenarios/ghostfleet_demo/RESULTS.md` already compares naive/hash/sightings × link rates with items sent, frames dropped, objects-with-image timeline, bytes on air, bytes per delivered object. Time-to-first-FLASH has no equivalent concept (no precedence); "objects w/ image at t=…" covers the latency story. Not ported. |
| Cloud receiver (Postgres/JWT, chunk ingestion) (`cloud/`) | **covered** | ghost-seeker `shore` (SQLite, chunked sync, unit tokens via `cmd/mint-unit-token`, storage cap PR). |
| Ship API / relay / services (`ship/api.py`, `relay.py`, `services.py`) | **covered** | `internal/shorebridge` (live shore mode) + `internal/server` (sim). |
| Night COLREGS lights detector (`edge/night/`, PR #10): light detection + rule interpretation | **covered** | `edgeml/nightlights` (#47) + Patrick's UI. |
| Night burst frame rate + dark-contact flagging (PR #10) | **ported** | **#55** `edgeml/nightlights/burst.py`, `dark_contact`, sidecar `NightBurst`, `edge/ingest` replay burst, `edge/scoring` `DarkContact` scorer; `docs/reports/NIGHT_LIGHTS.md`. |
| Sailor web console (`web/index.html`: tasking readback, Go quiet, sync status, super admin) | **covered** | ghost-seeker operator app (`web/static`) + `shore.html`; readback (#54), EMCON control + admin policy editor (#57). |
| Edge simulator / frame sources / matcher (`edge/sim.py`, `frames.py`, `matcher.py`) | **covered** | `edge/ingest` replay, `cmd/sim`, `edgeml` detector + tracker + `-dedup sightings`. |
| Zarf / air-gap packaging, laptop bundle, compose, CI (`deploy/`, PR #2) | **ported** | **#56** `deploy/Dockerfile` per-binary targets, `deploy/zarf`, `make package-check`, `.github/workflows/ci.yml`, `docs/FIELDING.md`. The Python laptop bundle is replaced by the single Go binary with embedded UI. |
| `docs/LLM_OFFLINE.md` (running a local OpenAI-compatible model) | **ported** | Folded into #54's README section (`GS_LLM_URL`, `GS_LLM_MODEL`, `GS_LLM_TIMEOUT`; vLLM / llama.cpp / Ollama). |

## dynamic-interest open PRs

| PR | Verdict | Disposition |
|---|---|---|
| #4 NL → Mission Card compiler: local LLM backends, rules fallback, readback, guardrails, eval | **ported** | ghost-seeker #54 (incl. the compound-duration fix from #4). Closed. |
| #5 Edge detector adapters (YOLOv8n / OWLv2 / CLIP+SigLIP) + capability manifest registry | **covered / dropped** | Capability manifest ≈ `Capabilities` RPC already on the sidecar (`modelId`, `openVocabulary`, `fixedLabels`, `embeddingDim`) and the `CapabilityProvider` lint in #54. Detector adapters: `edgeml` benchmark (`edgeml/reports/B.md`) concluded open-vocab detectors do not beat COCO `boat` on this footage; detector/tracker defaults are owned by other sessions. Closed. |
| #6 SMD ground-truth loader, CPU torchvision detector, simulator replay | **dropped** | ghost-seeker's MOT eval already uses FVessel / MVTD / SMD-style ground truth through the GCS dataset catalog; torchvision Faster R-CNN on CPU is slower than the pinned yolo11n ONNX path. Closed. |
| #8 Open-vocabulary detection on SMD replay (OWLv2 / CLIP stacks) + evaluator | **dropped** | Same conclusion as `edgeml/reports/B.md` (open vocab < COCO boat); ghost-seeker's zero-shot subtype tagging (MobileCLIP) already covers the "subtype" use. Closed. |
| #9 Vocabulary explorer: prompt detectors on SMD / own frames and score vs ground truth | **dropped** | Research tool for #8's detectors; not demo-visible. Closed. |

## Not ported, on purpose

- Precedence enum, time-to-first-FLASH metric (no precedence concept in ghost-seeker).
- IR hot-blob dark contacts (ghost-seeker skips light rules in IR).
- Python laptop bundle / `build-laptop.sh` (single Go binary instead).
- dynamic-interest's FastAPI services and Postgres cloud receiver (Go shore already does this).
