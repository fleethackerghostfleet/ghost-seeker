# Ghost Fleet datasets

This directory is the source of truth for **what data we have and how it got there**. Pixels live in GCS; this repo holds the registry and the ingest scripts.

- Bucket: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker` (project `qwiklabs-gcp-02-34a870014566`, us-central1)
- Query everything in BigQuery: dataset `qwiklabs-gcp-02-34a870014566.ghost_fleet`, tables `datasets`, `images`, `annotations` (external tables over the JSONL catalog below, so they're always in sync with GCS)

## Bucket layout
```
raw/<dataset_id>/...                 # exact upstream files as downloaded (HF snapshot, zips, videos). Immutable.
datasets/<dataset_id>/images/<split>/<file>.jpg   # one image per object, ready for models (gs:// URIs usable by Vertex/Gemini)
frames/<Name>_frames/sec_NNNN.jpg    # legacy layout of the original repo datasets (kept as-is; cataloged like the rest)
zips/<Name>_frames.zip               # legacy zips mirrored from frames/ in this repo
sources/<Name>.json                  # legacy provenance files
catalog/datasets.jsonl               # one row per dataset
catalog/images/<dataset_id>.jsonl    # one row per image
catalog/annotations/<dataset_id>.jsonl  # one row per labelled object (ground truth now, model output later)
```

## Catalog schemas (newline-delimited JSON; every field always present, null when unknown)

`catalog/datasets.jsonl`
| field | type | notes |
|---|---|---|
| dataset_id | STRING | lowercase snake case, e.g. `seaships7000`, `video_cargo_ship_uncut_view` |
| name | STRING | human name |
| source_type | STRING | `huggingface` \| `youtube` \| `facebook` \| `motionarray` \| `openimages` \| `web` \| `repo` |
| source_url | STRING | |
| source_revision | STRING | HF commit sha, YouTube video id, file sha256, … |
| license | STRING | as published; `unknown` if not stated |
| viewpoint | STRING | `surface` (ship/shore eye level) \| `aerial` \| `satellite` \| `underwater` \| `mixed` |
| modality | STRING | `rgb` \| `ir` \| `sar` |
| synthetic | BOOL | |
| annotation_format | STRING | upstream format: `coco` \| `yolo` \| `voc` \| `mot` \| `none` \| … |
| native_classes | ARRAY<STRING> | upstream class names, verbatim |
| num_images | INT64 | rows in catalog/images for this dataset |
| num_annotations | INT64 | object boxes in catalog/annotations for this dataset (image-level rows such as SMD `horizon` are excluded) |
| bytes | INT64 | total bytes under datasets/<id>/ (or frames/ for legacy) |
| raw_prefix | STRING | gs:// prefix of raw files, null if none |
| images_prefix | STRING | gs:// prefix of model-ready images |
| ingested_at | TIMESTAMP | UTC ISO-8601 |
| ingest_script | STRING | repo path, e.g. `research/datasets/ingest/hf_seaships.py` |
| notes | STRING | |

`catalog/images/<dataset_id>.jsonl`
| field | type | notes |
|---|---|---|
| image_id | STRING | `<dataset_id>/<split>/<file stem>`; globally unique |
| dataset_id | STRING | |
| gcs_uri | STRING | `gs://.../datasets/<id>/images/<split>/<file>.jpg` (or the legacy frames/ URI) |
| split | STRING | upstream split (`train`/`val`/`test`) or `all` |
| width | INT64 | pixels |
| height | INT64 | pixels |
| bytes | INT64 | |
| sha256 | STRING | of the stored file |
| source_ref | STRING | upstream file name, or `<youtube_id>@<second>` for video frames |
| timestamp_s | FLOAT64 | seconds into source video, null for still images |
| has_annotations | BOOL | true if the image has ≥1 object box |

`catalog/annotations/<dataset_id>.jsonl` — box format matches `Detection` in dynamic-interest (`box` = [x, y, w, h] in **pixels**, top-left origin)
| field | type | notes |
|---|---|---|
| image_id | STRING | FK to images |
| dataset_id | STRING | |
| label | STRING | upstream class name, verbatim |
| label_norm | STRING | shared Ghost Fleet taxonomy; null until a mapping is defined for this dataset |
| box | ARRAY<FLOAT64> | [x, y, w, h] pixels; empty array for image-level labels |
| conf | FLOAT64 | 1.0 for ground truth |
| source | STRING | `gt` for upstream labels, `model:<name>` for model output (e.g. `model:gemini-2.5-flash`) |
| attrs | STRING | JSON-encoded object of extra upstream fields (track id, occlusion, …) or null |

## Rules
- Never overwrite `raw/`. To re-ingest, write a new revision and update the catalog rows.
- The catalog files for a dataset are rewritten as a whole by its ingest script (idempotent).
- Images we store are JPEG. Convert PNG/other upstream formats on ingest and keep the original in `raw/`.
- Every dataset gets a `registry.yaml` entry before its data lands in GCS.

## Registering new data

After uploading a clip with the `ghost-fleet-dataset-ingest` skill, run
`python research/datasets/ingest/video_frames.py` from the repo to refresh the catalog.
