# edgeml sidecar — `InferenceService` over Connect/HTTP+JSON

## Thermal hotspot fallback

Thermal fallback is **off by default** in both the CLI and `EdgemlEngine`.
Opt in with bare `--thermal-fallback` (frozen defaults) or, for example,
`--thermal-fallback '{"mode":"when_empty","unknownness_floor":0.9}'`.
`--thermal-fallback false` or `null` disables it; programmatic callers enable
it with `thermal_fallback={}` or `True`.

Opt-in is required because held-out annotation-empty IR sea crops produced
**1.005 false contacts/crop**, with **28%** of crops producing contacts.
Those contacts receive unknownness ≥0.9 by default, so enabling the fallback
can generate high-priority false items under unknown-first scoring. Thresholds
remain frozen; see [THERMAL_HOTSPOT.md](../reports/THERMAL_HOTSPOT.md).
Thermal-on latency rows there apply only when opted in. Deployment manifests
do not enable it. Source-local new-contact detection and its weight-1 scorer
remain enabled independently of this flag.

## Marine mammals

Marine-mammal verification is **off by default**, even with MobileCLIP-S1 `--embed`.
Opt in with `--marine-mammal on`; `--marine-mammal off` disables it.
`--mm '{"enabled":true,"scan_stride":5,"max_crops":8}'` can also enable it;
JSON configuration overrides the flag, including `enabled`. Calibrated thresholds remain
0.9480436444282532 and scan threshold 0.7265272736549377; see
[`marine_mammal_live.md`](../reports/marine_mammal_live.md) for data discipline and limitations.
Candidate YOLO crops and, on open-water frames, a bounded whole-frame/overlapping-grid scan
produce `marine mammal` frame detections and tracked sightings, with sighting subtypes
`whale`, `blow`, `fluke`, or `dolphin`. No torch is needed at runtime: additional text
embeddings ship in the package. Other embedding models skip verification with a log message.
Opt-in is required because held-out v3 precision is only 28.4%, with 46.4 false
positives/1000 negative frames (45/58 from the submarine clip). Relabeling vessel
or unknown-contact crops can suppress unknownness and demote real contacts;
CPU cost also rises from about 82 to 437 ms/frame with both MM paths enabled.

The sidecar wraps the `edgeml` pipeline (detect → boxmot track → `SightingAggregator` → MobileCLIP zero-shot)
behind the `ghostseeker.v1.InferenceService` contract the Go edge already speaks (`edge/inference.Remote`,
`connect.WithProtoJSON()`), and adds **sightings** to `AnalyzeResponse` so the edge can store one item per object
instead of one per frame. Code lives in the package at `edgeml/src/edgeml/sidecar/` (`python -m edgeml.sidecar`).

```
python -m edgeml.sidecar --listen :9090 \
    --detector ~/models/D/yolo11n-640/model.onnx \
    --embed ~/models/D/mobileclip-s1 \
    --threads 4 --fps 5
python -m edgeml.sidecar --listen :9090 --mock           # scripted engine, no models (Go tests / CI)
```

The detector above uses ONNX Runtime CPU; an OpenVINO `int8/model.xml` export
can be passed instead. The embed directory contains `meta.json`, `model.onnx`,
and `text_cache.json`.

Models (D's exports, see `edgeml/reports/E.md`):

```
B=gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D
gcloud storage cp -r $B/yolo11n-640 ~/models/D/                                   # model.onnx (FP32, ORT), int8/model.xml (OpenVINO)
gcloud storage cp $B/clip/mobileclip-s1/{meta.json,model.onnx,text_cache.json} ~/models/D/mobileclip-s1/
```

Dependencies: `pip install -e edgeml onnxruntime boxmot` (boxmot pulls torch, but the sidecar never imports it on
the hot path: detector and CLIP image tower run on ONNX Runtime, `pin_threads` fixes 4 CPU threads).

`--listen :9090` binds **127.0.0.1** (the service is unauthenticated); use `--listen 0.0.0.0:9090` to expose it. Request
bodies are capped at 32 MiB.

`--prioritizer edgeml` is the CLI default. `--prioritizer tomer` runs the
image-level `algorithm/ghost_priority` adapter without a detector when none is
specified; `both` adds its score to the edgeml output. `make demo-sidecar` uses
`both` by default. Select `-prioritizer tomer` on the Go edge to use that score;
the Go scheduler still selects forms and chunks, rather than calling the
offline whole-JPEG selector.

The live `EdgemlEngine` sets `gate_unknown_bonus_on_realness=True`, overriding
the aggregator library's `False` default. Pass
`--agg '{"gate_unknown_bonus_on_realness":false}'` to use the library behavior.
These defaults differ from the frozen operating point in the
[unknown-object evaluation](../../docs/reports/UNKNOWN_OBJECTS.md); those
measurements do not establish the live configuration's accuracy.

## RPCs

| RPC | What it does |
|---|---|
| `Analyze` | Decodes the frame, feeds the per-`source` pipeline (one tracker + aggregator per camera, keyed by the additive `AnalyzeRequest.source`; `captured_at_unix_ms` is the frame clock, falling back to `--fps`). Returns the stateless fields (`detections`, `embedding` = key-crop MobileCLIP embedding when a sighting was reported this frame, `unknownness`, `model_id`, `latency_ms`) plus `sightings`. |
| `EmbedText` | MobileCLIP text embeddings from the exported `text_cache.json` (covers the zero-shot prompt set). Arbitrary text needs the torch text tower: start with `--text-tower torch` (opt-in, loaded lazily, never touched by `Analyze`). |
| `Capabilities` | `model_id`, `open_vocabulary`, the zero-shot subtype list, `embedding_dim` (512 for S1). |

`sightings[]` per frame (`proto/ghostseeker/v1/inference.proto`):

| event | when | `is_key_frame` |
|---|---|---|
| `EVENT_OPEN` | aggregator confirmed a new object (`min_hits`) | true |
| `EVENT_UPDATE` | material change: better key frame, label/subtype flip, size/bearing jump (rate-limited by `min_update_interval_s`) | true when this frame became the key frame |
| `EVENT_DUPLICATE` | the object was tracked in this frame but nothing changed worth reporting | false |
| `EVENT_CLOSE` | not seen for `close_after_s` | false |

`key_detection` is the best key frame's label / confidence / normalized box, `subtype` comes from zero-shot
MobileCLIP on the key crop, `forms` are the byte-exact fact/crop/thumb/full sizes the aggregator would produce.
Frames with no reported sighting follow the Go edge's scene/tracker-warmup
handling described below; they do not all become ordinary hash-mode items.

## Integration test: one item per object

```
# 1. sidecar (real models, or --mock)
python -m edgeml.sidecar --listen :9090 --detector ~/models/D/yolo11n-640/model.onnx --embed ~/models/D/mobileclip-s1

# 2. in another terminal, start shore with the local demo signing key
make run-shore

# 3. in another terminal, mint a unit token and replay a scenario at 5 fps
# (scenario.json must point sets[].Path at the intended MOT img1 directories)
(umask 077; ./bin/mint-unit-token -id edge-1 -ttl 24h -signing-key-file data/unit.key > data/edge.jwt)
./bin/edge -id edge-1 -unit-token-file data/edge.jwt -infer http://localhost:9090 \
    -dedup sightings -scenario scenario.json -period 200ms -shore http://localhost:8080
#    (-frames <dir> replays the built-in demo scenario from that dir; -dedup naive|hash|sightings, default hash)

# 4. in another terminal, inspect the authenticated edge console
TOKEN=$(make -s demo-token)
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{}' localhost:8081/ghostseeker.v1.ConsoleService/GetStatus
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"limit":20}' localhost:8081/ghostseeker.v1.ConsoleService/GetQueue
```

Edge-side semantics (`edge/ingest/sightings.go`): OPEN → item with `tags{sighting_id,subtype,event}` and a CROP form
(key box × 1.5 margin, short side ≥ 96 px, JPEG q80 by default). Unknowns skip CROP by default,
and a crop no smaller than FULL is omitted. FULL retains the complete capture (JPEG/WebP bytes are kept as-is).
DUPLICATE → counted in `GetStatus.items_deduped`, no item;
UPDATE with a better key frame → thumb/crop/full + features replaced in place while no image form has started to ship
(manifest/FACT may already have been sent),
otherwise a new item with the same `sighting_id`; non-key UPDATE → tags + FACT refreshed; CLOSE → ~200 B RECORD
contact report (sighting id, item id, label, subtype, first/last seen, n frames). Frames with **no sighting and no
detection** (open ocean) become at most one `event=scene` item (FACT+THUMB sent automatically;
FULL retained locally and sent only on request, no CROP) per source per
`-scene-every` (default 60 s); the rest fold into `items_deduped`, as do frames with detections but no sighting yet
(tracker warming up — the OPEN follows with the key frame; this only applies once the engine has reported a sighting
for that source, so a stateless engine such as `-infer mock` keeps per-frame items for detected frames). `-scene-every 0` restores one item per object-free frame.

Tests: `pytest -q edgeml/tests/test_sidecar.py` (mock engine over real HTTP), `go test ./edge/ingest/` (scripted
sightings through the state machine).
