# Third-party software, models, weights and datasets

This is a source-backed starting inventory, **not legal approval or a complete
fielded SBOM**. Licenses below are recorded repository/generated metadata; verify
the exact distributed artifact, terms, notices and transitive/native components.
Unknown rights stay **TBD**. Public download access is not permission to distribute.

## Software evidence and gate scope

Sources: [go.mod](../../go.mod), [default hash lock](../../edgeml/requirements.lock),
[tracking lock](../../edgeml/requirements-tracking.lock),
[Python project extras](../../edgeml/pyproject.toml) and
[pipeline license policy](../security/PIPELINE.md).

Generated license outputs available during preparation were
`dist/security/go-licenses.csv` and `dist/security/python-licenses.json` (local,
untracked, not attached to this document). Earlier locked-environment gate output:

```text
licenses: 16 Go and 11 Python dependencies; 0 denied/unknown
```

This historical result is not a Task 5 rerun or legal clearance. The scanner notes
that `golang.org/x/sys/unix` assembly cannot be inspected for further dependencies.
The gate excludes first-party licensing, models/assets and evaluation/GPU/detect
extras; native wheel contents need separate review. GPL/AGPL allowlist is empty.
Regenerate inventories and `make sbom` for the selected release; image SBOM names
are in the [index](README.md). Attach artifacts, source commit, image digests,
architecture and timestamp before approval.

| Key software | Version/source in repository or named report | Recorded license / status |
| --- | --- | --- |
| First-party Go application / edgeml | Repository sources; edgeml 0.1.0 | No root LICENSE; owner/legal decision TBD |
| Connect Go | 1.18.1, `connectrpc.com/connect` | Apache-2.0 in Go license report |
| golang-jwt/jwt/v5 | 5.2.2 | MIT in Go report |
| oklog/ulid/v2 | 2.1.0 | Apache-2.0 in Go report |
| Go x/crypto, x/image, x/net; protobuf | go.mod (protobuf 1.36.6) | BSD-family metadata; production dependency closure is not every module in go.mod |
| modernc SQLite / libc / mathutil / memory | 1.37.1 / 1.65.7 / 1.7.1 / 1.11.0 | BSD-3-Clause in Go report |
| NumPy / SciPy | Default lock 2.4.6 / 1.17.1 | NumPy multi-license expression (BSD/0BSD/MIT/Zlib/CC0); SciPy BSD metadata |
| OpenCV headless / ONNX / ONNX Runtime | Default lock 5.0.0.93 / 1.23.2 / 1.31.1 | Apache-family / Apache-2.0 / MIT metadata; audit bundled native code |
| PyYAML / Python protobuf | Default lock 6.0.3 / 7.36.2 | MIT / BSD metadata |
| BoxMOT | Optional tracking lock; upstream package `boxmot` | AGPL-3.0 identified in tracking image; fielding blocked pending legal decision |
| Torch / torchvision and tracking transitive deps | Tracking lock / CPU package index in lock recipe | Per-artifact license review TBD here; default inventory does not approve the variant |
| Ultralytics | `detect`/`gpu` extras; [adapter](../../edgeml/src/edgeml/detectors/ultralytics_zoo.py) | AGPL/commercial licensing decision open; exported model rights remain separate from runtime package absence |
| PptxGenJS 3.12.0, bundled JSZip | [browser license](../../web/static/pptxgen.LICENSE), [README](../../README.md) briefing export | MIT notice for vendored bundle; retain notices and verify bundled dependency terms |
| NanoDet-derived algorithm material | [third-party notice](../../algorithm/ghost_priority/third_party/LICENSE-NANODET) | Notice retained; evaluate selected algorithm weights separately |
| Go/Python/base OS and platform images | [Go recipe](../../deploy/Dockerfile), [Python recipe](../../edgeml/Dockerfile), UDS Core 1.14.3 | Pinned base digests are provenance inputs, not a single blanket license or Iron Bank approval; platform/component inventory TBD |

Source package URLs/licenses for the complete measured Go closure are in the CSV;
the Python JSON records package/version/license. Do not infer a model license from
these package results or from its ONNX/OpenVINO file format.

## Models, weights and auxiliary artifacts

| Asset / role | Repository source and selection | License / provenance decision |
| --- | --- | --- |
| YOLO11n ONNX detector | [Zarf command](../../deploy/zarf/manifests/edge-inference.yaml): `/models/yolo11n-640/model.onnx`; [zoo](../../edgeml/src/edgeml/detectors/zoo.py) uses upstream `yolo11n.pt` | Ultralytics AGPL/commercial decision open; actual approved weights, SHA-256, immutable upstream revision and authority TBD |
| MobileCLIP-S1 image/text embedding | [embedder](../../edgeml/src/edgeml/embed/clip_embedder.py): `MobileCLIP-S1`, `datacompdr`; demo `/models/mobileclip-s1` | Weight/model license not established by local package gate; upstream model-card/terms review TBD; not enabled by default Zarf detector-only command |
| MobileCLIP2-S0, SigLIP-B16, OpenCLIP-B32 | Same embedder mapping: `dfndr2b`, `webli`, `laion2b_s34b_b79k` | Research/options, not automatically shipped; exact license/revision/pretraining-overlap review TBD |
| Other detector zoo / fine-tuned checkpoints | [zoo](../../edgeml/src/edgeml/detectors/zoo.py), [bakeoff](../../edgeml/reports/MODEL_BAKEOFF.md), [shore finetuning report](../../edgeml/reports/SHORE_FINETUNE.md) | Research inventory, not an approved release selection; training lineage, evaluation and licenses TBD per checkpoint |
| Calibration, metadata, tokenizer/text cache and external ONNX tensors | [integrity verifier](../../edgeml/src/edgeml/sidecar/integrity.py); model bundle manifest | Hash/provenance verification required for covered runtime assets; no independent license approval implied |
| OpenVINO IR / runtime graph files | [runtime backends](../../edgeml/src/edgeml/runtime/backends.py); selected model directory, including `.xml`/`.bin` where used | Optional runtime format; actual deployed artifacts, revision, hashes, runtime license and weight rights TBD |
| Optional ReID / tracker weights | [sidecar CLI](../../edgeml/src/edgeml/sidecar/__main__.py), [tracker adapter](../../edgeml/src/edgeml/tracking/boxmot_adapter.py) | Local verified paths required when selected; no default ReID asset is approved by this inventory; model/data license and tracking decision TBD |
| Night-light and COLREG configuration/catalog | [light catalog](../../internal/catalog/colregs_lights.json), [configuration](../../internal/catalog/colregs_night_config.json), packaged by [sidecar recipe](../../edgeml/Dockerfile) | Runtime configuration, not learned weights; first-party licensing/source/validation decision still required |
| Demo D bundle | [Makefile](../../Makefile) `demo-models`, [download helper](../../deploy/demo-data.sh): release `demo-data-v1`, `models-D.tar.gz`; [manifest helper](../../scripts/demo_model_manifest.py) | Demo trust-on-first-use; local hashes are not externally approved provenance. Never promote automatically |
| Mission scoring weights / optional model policy | [catalog](../../internal/catalog/catalog.go), [retune workflow](../HUMAN_REVIEW.md), [integrity verifier](../../edgeml/src/edgeml/sidecar/integrity.py) | First-party rights decision plus mission-owner evaluation/approval TBD; signed command policy is separate from weight/model promotion |

For each fielded asset add immutable source URL/revision, precise filename and
SHA-256, export/toolchain parameters, training dataset lineage, license/notices,
evaluation/limitations, approver and rollback artifact. None of those site-specific
records is supplied by a directory name alone. Export/evaluation download helpers
remain outside the approved runtime path and need their own audit (STIG F-16).

## Dataset registry (not a distribution approval)

The fixed human-review selection is
[`datasets/review/clean_v2_dev_review.jsonl`](../../datasets/review/clean_v2_dev_review.jsonl)
and its [selection SQL](../../datasets/review/clean_v2_dev_review.sql), not a grant
of image rights. [HUMAN_REVIEW.md](../HUMAN_REVIEW.md) describes 2,283 dev frames,
ignored `data/review_set/` materialization, exclusions, and locked test/quarantine
restrictions. Keep this selection fixed; no test/quarantine review or tuning.
Demo/scenario imagery is materialized via [demo-data helper](../../deploy/demo-data.sh)
and includes [imagery catalog](../../internal/catalog/imagery.json) references;
rights and redistribution approval are TBD per asset, not covered by code licenses.

The [registry](../../datasets/registry.yaml) is authoritative for the following
recorded metadata; [schema/rules](../../datasets/README.md) explain source URLs,
revisions and raw-data preservation. “Planned” means registered, not proof of
ingestion. Dataset catalogs/pixels may live outside the checkout.

| Dataset ID | Source recorded in registry | Recorded license / use | Registry status |
| --- | --- | --- | --- |
| `video_frames` | Mixed video sources; no single URL | unknown; per-video rights TBD | ingested |
| `seaships7000` | HF `dronefreak/SeaShips` | unknown | ingested |
| `singapore_maritime` | HF `ARG-NCTU/Singapore_Maritime_Dataset_coco` | unknown | ingested |
| `mvtd` | HF `AhsanBB/Maritime_Visual_Tracking_Dataset_MVTD` | cc0-1.0 as recorded; upstream confirmation TBD | planned |
| `seadronessee` | HF `dronefreak/SeaDronesSee`, pinned registry revision | cc0-1.0 as recorded; mirror lineage/terms review required | ingested |
| `ir_maritime_vessels` | HF `hanchong/real-infrared-maritime-vessel-dataset` | unknown; **internal_dev_only; never in a shipped model** | ingested |
| `boat_dataset_coco_2024` | HF `ARG-NCTU/Boat_dataset_coco_2024` | unknown | planned |
| `synthetic_boats` | HF `Vivid3Dai/boat-detection-synthetic-data` | cc-by-4.0 as recorded; attribution obligations require review | planned |
| `openimages_maritime` | Open Images V7 site | unknown in registry; do not substitute a guessed license | planned |
| `youtube_maritime` | YouTube; individual source videos TBD | unknown | planned |
| `lars` | `lojzezust.github.io/lars-dataset` | unknown | planned |
| `mods` | `vicos.si/resources/mods_dataset` | unknown | planned |
| `aboships` | research.abo.fi dataset page | unknown | planned |
| `smd_vis_onboard_sample` | Original Singapore Maritime Dataset site | unknown (academic research use) | ingested |

Research/internal-use labels must propagate to derived checkpoints and exports.
Review training-versus-test overlap and per-source permission before any model
release. A public mirror, benchmark score, human review label or passing SBOM
gate does not remove original data restrictions. Operational sensor imagery is
a separate inventory with classification, ownership and retention **TBD**.
