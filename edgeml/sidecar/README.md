# edgeml sidecar — `InferenceService` over Connect/HTTP+JSON

The sidecar wraps the phase-1 `edgeml` pipeline (detect → boxmot track → `SightingAggregator` → MobileCLIP zero-shot)
behind the `ghostseeker.v1.InferenceService` contract the Go edge already speaks (`edge/inference.Remote`,
`connect.WithProtoJSON()`), and adds **sightings** to `AnalyzeResponse` so the edge can store one item per object
instead of one per frame. Code lives in the package at `edgeml/src/edgeml/sidecar/` (`python -m edgeml.sidecar`).

```
python -m edgeml.sidecar --listen :9090 \
    --detector ~/models/D/yolo11n-640/model.onnx \      # YOLO11n FP32 ONNX on ONNX Runtime CPU (or int8/model.xml on OpenVINO)
    --embed    ~/models/D/mobileclip-s1 \               # MobileCLIP-S1 ONNX export: meta.json, model.onnx, text_cache.json
    --threads 4 --fps 5
python -m edgeml.sidecar --listen :9090 --mock           # scripted engine, no models (Go tests / CI)
```

Models (D's exports, see `edgeml/reports/E.md`):

```
B=gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models/D
gcloud storage cp -r $B/yolo11n-640 ~/models/D/                                   # model.onnx (FP32, ORT), int8/model.xml (OpenVINO)
gcloud storage cp $B/clip/mobileclip-s1/{meta.json,model.onnx,text_cache.json} ~/models/D/mobileclip-s1/
```

Dependencies: `pip install -e edgeml onnxruntime boxmot` (boxmot pulls torch, but the sidecar never imports it on
the hot path: detector and CLIP image tower run on ONNX Runtime, `pin_threads` fixes 4 CPU threads).

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
Frames with no tracked object return no sightings and the Go edge behaves exactly as in `hash` mode for them.

## Integration test: one item per object

```
# 1. sidecar (real models, or --mock)
python -m edgeml.sidecar --listen :9090 --detector ~/models/D/yolo11n-640/model.onnx --embed ~/models/D/mobileclip-s1

# 2. edge replaying a frame directory at 5 fps in sightings mode (a scenario JSON points `sets[].Path` at MOT img1 dirs)
go build -o bin/edge ./cmd/edge
./bin/edge -infer http://localhost:9090 -dedup sightings -scenario scenario.json -period 200ms -shore http://localhost:8080
#    (-frames <dir> replays the built-in demo scenario from that dir; -dedup naive|hash|sightings, default hash)

# 3. queue = one image item per object (+ a tiny RECORD contact report per closed sighting), duplicates counted
curl -s -X POST -H 'Content-Type: application/json' -d '{}' localhost:8081/ghostseeker.v1.ConsoleService/GetStatus   # itemsTotal, itemsDeduped
curl -s -X POST -H 'Content-Type: application/json' -d '{"limit":20}' localhost:8081/ghostseeker.v1.ConsoleService/GetQueue
```

Edge-side semantics (`edge/ingest/sightings.go`): OPEN → item with `tags{sighting_id,subtype,event}` and a CROP form
(key box × 1.5 margin, short side ≥ 96 px, JPEG q75); DUPLICATE → counted in `GetStatus.items_deduped`, no item;
UPDATE with a better key frame → thumb/crop/full + features replaced in place while nothing of the item has shipped,
otherwise a new item with the same `sighting_id`; non-key UPDATE → tags + FACT refreshed; CLOSE → ~200 B RECORD
contact report (sighting id, item id, label, subtype, first/last seen, n frames).

Tests: `pytest -q edgeml/tests/test_sidecar.py` (mock engine over real HTTP), `go test ./edge/ingest/` (scripted
sightings through the state machine).
