# edgeml — edge ML pipeline (phase 1: evaluate)

Python. Independent of `algorithm/` (Tomer's CPU prioritization baseline) and of the Go core. Nothing here
changes `edge/`, `shore/`, `proto/` or `algorithm/` in phase 1.

**Goal.** Turn a camera stream into **sightings**, which is what a human watch-stander reports. One report per
real object for each continuous pass, plus one best key frame. Not one detection per frame. It has to run on a
CPU-only Dell rugged laptop shared with other workloads. GCP GPUs run the *same* models fast for evaluation.
Fine-tuning is optional, used only if zero-shot models leave a measured gap.

```
frames ─► 0 gate ─► 1 detect ─► 2 track ─► 3 embed/classify (per track) ─► 4 sighting aggregator ─► sightings
         (cheap)    (1–5 Hz,    (ByteTrack/   (MobileCLIP-class, crop      (birth / material update / close;
                     tiled       OC-SORT +      zero-shot subtype,           best key frame; re-link short gaps)
                     horizon)    CMC)           unknownness)
```

See [CONTRACTS.md](CONTRACTS.md) for the interfaces, file formats, metrics and data rules every workstream follows.

## Layout
```
edgeml/
  CONTRACTS.md        interfaces + formats + metrics (source of truth)
  configs/splits.yaml dev/test split by source (test is locked)
  src/edgeml/
    types.py          shared dataclasses (Detection, Track, Sighting)
    interfaces.py     Detector / Tracker / Embedder / Aggregator protocols
    detectors/        one adapter per model family (workstream B)
    tracking/         tracker adapters + sighting aggregator (workstream C)
    embed/            embeddings, zero-shot subtype, unknownness (workstream D)
    runtime/          CPU/OpenVINO runtime, frame gate, latency bench (workstream D)
    eval/             metrics: detection, tracking (TrackEval), sightings (workstream C)
    data/             loaders for eval sets in GCS (workstream A)
  tools/              one-off scripts (labeling, export, GCP job launchers)
```
