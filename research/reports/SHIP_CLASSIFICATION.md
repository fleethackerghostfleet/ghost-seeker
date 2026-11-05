# Ship classification from imagery (per-contact type + visible characteristics)

Synthetic / UNCLASSIFIED prototype. All numbers below were measured on this VM (8 vCPU, no GPU) with the commands listed
under *Reproduce*; anything not measured is marked **estimate** or **not evaluated**.

## What shipped

* `edgeml/src/edgeml/embed/shiptype.py` — three backends behind the edgeml `Embedder` convention, one output record
  (`type, label, conf, aspect, size, features, markings, model`, same fields as Go `catalog.ShipClass`):
  * `ShipTypeClassifier(embedder)` — zero-shot prompt prototypes over any text+image `Embedder`
    (`ClipEmbedder`: MobileCLIP-S0/S1, SigLIP-B/16, OpenCLIP ViT-B/32). Crops are letterboxed to square
    (`square_pad`) so the square-input towers don't centre-crop long hulls away. Gives type, aspect (bow/stern/beam
    prompts) and size (**type prior**, not measured from pixels).
  * `DinoEmbedder` + `ProbeClassifier` — DINOv2-S/14 (timm) frozen features + logistic-regression probe (also run on the
    CLIP towers' image features). Only knows the classes present in its training labels.
  * `GeminiShipClassifier` — Gemini 2.5 Flash on Vertex AI, JSON schema, `thinking_budget=0`. Returns type, self-reported
    confidence, aspect, size estimate, visible features (radar mast / cranes / helicopter deck / gun mount) and markings
    *visible / not visible*. The prompt forbids reading or guessing names, hull numbers or specific real vessels.
  * 12 types: cargo/bulk, container, tanker, passenger/cruise/ferry, fishing, tug, sailboat/yacht, small boat/RHIB,
    warship (destroyer/frigate/carrier/patrol), submarine, coast guard, unknown.
* `edgeml/tools/ship_classify_eval.py` (eval), `ship_classify_report.py` (tables), `ship_classify_library.py`
  (writes `ship` records into `internal/catalog/imagery.json`).
* Go: `catalog.ShipClass`; `LibraryDet.Ship` and `command.Detection.Ship` (`json:"ship,omitempty"`); the sensor copies the
  library record onto each detection; catalog test validates the fields. **No scoring / prioritization weights changed**
  — `score.go` and catalog models are untouched (the detector class still drives priority; the ship type is
  operator information only).
* UI: image detail drawer shows `Type: Tanker (95%) · Aspect: bow · Size: large` and `Visible: radar mast · markings not
  visible` under each vessel; Operator Review adds a *Classification* row (top 3 contacts).
* `internal/catalog/imagery.json`: 76 vessel detections across the 48 library frames now carry a Gemini 2.5 Flash `ship`
  record (non-vessel detections untouched).

## Data and protocol

| set | split | source | n crops | label source |
|---|---|---|---|---|
| seaships_train | train (probe fit only) | SeaShips7000 train, 100/native class, min side ≥ 20 px, seed 0 | 600 | dataset GT |
| seaships_dev | **dev** | SeaShips7000 valid, 50/native class | 300 | dataset GT |
| gf_dev | **dev** | ghost_fleet_5fps dev clips, 6 tracks with an unambiguous type, ≤ 8 frames/track | 48 | human (clip title + QA sheet) on teacher+human boxes |
| seaships_test | **locked test** | SeaShips7000 test, 50/native class | 300 | dataset GT |
| gf_test | **locked test** | ghost_fleet_5fps test clips, 7 tracks | 56 | human (clip title + QA sheet) |
| synthetic | report-only | 48 library frames, 76 vessel dets from `imagery.json` | 76 | generation label (`cls`) |

SeaShips native → type: ore carrier / bulk cargo carrier / general cargo ship → cargo; container ship → container;
fishing boat → fishing; passenger ship → passenger. Gemini was run on a 30/native-class subset (180) of the SeaShips sets.
Ambiguous ghost-fleet tracks (Norfolk approach boats, cruise-ship tenders, distant RoRo) were excluded rather than guessed.

Protocol (CONTRACTS.md): everything was chosen on dev — square letterboxing (+14 pts SeaShips dev for MobileCLIP-S1:
37.3% → 51.3%), unknown floor = 0, edge model choice. Then `seaships_test`, `gf_test` and `synthetic` were run **once**
with that frozen config (`~/data/shipcls_sq/runs.log`). Prompts were written once and not iterated on any set.
All classifiers predict over the full 12-way taxonomy (open set), except probes (4 SeaShips classes).

## Results — top-1 accuracy (macro recall, n)

| model | seaships_dev top-1 (macro-recall, n) | gf_dev top-1 (macro-recall, n) | seaships_test top-1 (macro-recall, n) | gf_test top-1 (macro-recall, n) | synthetic top-1 (macro-recall, n) | ms/crop b1 | ms/crop b32 |
|---|---|---|---|---|---|---|---|
| dinov2-s14-probe | 91.7% (90.2%, 300) | 16.7% (8.3%, 48) | 94.7% (93.0%, 300) | 26.8% (18.8%, 56) | 19.7% (17.7%, 76) | 63.2 | 48.8 |
| gemini-2.5-flash | 63.3% (52.2%, 180) | 93.8% (93.8%, 48) | 64.4% (59.4%, 180) | 92.9% (95.0%, 56) | 48.7% (69.8%, 76) | 1158 |  |
| mobileclip-s0-probe | 90.0% (85.7%, 300) | 16.7% (8.3%, 48) | 90.3% (86.5%, 300) | 32.1% (27.5%, 56) | 23.7% (20.3%, 76) | 176.3 | 69.8 |
| mobileclip-s0-zeroshot | 48.7% (38.3%, 300) | 22.9% (33.3%, 48) | 54.0% (45.3%, 300) | 83.9% (82.5%, 56) | 46.1% (66.5%, 76) | 176.3 | 69.8 |
| mobileclip-s1-probe | 92.3% (89.5%, 300) | 10.4% (5.2%, 48) | 92.0% (89.7%, 300) | 26.8% (27.5%, 56) | 23.7% (20.3%, 76) | 295.0 | 107.4 |
| mobileclip-s1-zeroshot | 51.3% (41.7%, 300) | 22.9% (30.2%, 48) | 55.0% (49.8%, 300) | 92.9% (95.0%, 56) | 44.7% (68.4%, 76) | 295.0 | 107.4 |
| openclip-b32-probe | 90.7% (86.3%, 300) | 12.5% (6.2%, 48) | 88.7% (85.0%, 300) | 23.2% (17.5%, 56) | 23.7% (21.8%, 76) | 64.5 | 32.1 |
| openclip-b32-zeroshot | 34.7% (30.3%, 300) | 27.1% (38.5%, 48) | 36.7% (32.7%, 300) | 91.1% (93.8%, 56) | 44.7% (66.8%, 76) | 64.5 | 32.1 |
| siglip-b16-probe | 89.7% (85.5%, 300) | 8.3% (4.2%, 48) | 89.7% (86.2%, 300) | 37.5% (36.2%, 56) | 21.1% (23.1%, 76) | 169.7 | 105.5 |
| siglip-b16-zeroshot | 33.0% (37.2%, 300) | 35.4% (58.3%, 48) | 33.3% (38.0%, 300) | 82.1% (80.0%, 56) | 39.5% (64.1%, 76) | 169.7 | 105.5 |

Latency column = the eval run (other jobs sharing the CPU). A clean re-run (`/tmp/lat.py` equivalent: 40 SeaShips crops,
torch fp32, 4 threads, nothing else running) gave ms/crop batch-1 / batch-32: MobileCLIP-S0 80 / 68, MobileCLIP-S1 214 / 108,
SigLIP-B/16 156 / 107, OpenCLIP B/32 71 / 27, DINOv2-S/14 66 / 44. Run-to-run spread on this VM is ~2×, so treat these as
CPU-torch ballpark only; ONNX/OpenVINO export (already supported by `ClipEmbedder`) was **not** measured here.
Gemini 2.5 Flash: 1.0–1.5 s per sequential call (median of 5 per set), ~180 crops in ~20 s with 8 parallel workers.

### Confusion matrices (locked test + synthetic)

**gemini-2.5-flash — seaships_test** (top-1 64.4%, n=180)

| gt \ pred | cargo | container | tanker | passenger | fishing | tug | sailboat | small_boat | warship | coast_guard | unknown | n |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **cargo** | 67 | 2 | 6 | · | 3 | 8 | · | 3 | 1 | · | · | 90 |
| **container** | 2 | 27 | · | · | · | · | · | · | · | · | 1 | 30 |
| **passenger** | · | · | · | 5 | · | 4 | 6 | 8 | · | 7 | · | 30 |
| **fishing** | · | · | · | · | 17 | · | · | 5 | 2 | · | 6 | 30 |

**gemini-2.5-flash — gf_test** (top-1 92.9%, n=56)

| gt \ pred | cargo | passenger | sailboat | warship | submarine | unknown | n |
|---|---|---|---|---|---|---|---|
| **cargo** | 8 | · | · | · | · | · | 8 |
| **passenger** | · | 15 | · | · | · | 1 | 16 |
| **sailboat** | · | · | 13 | · | · | 3 | 16 |
| **warship** | · | · | · | 8 | · | · | 8 |
| **submarine** | · | · | · | · | 8 | · | 8 |

**gemini-2.5-flash — synthetic** (top-1 48.7%, n=76)

| gt \ pred | cargo | container | tanker | fishing | tug | sailboat | small_boat | warship | submarine | coast_guard | unknown | n |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **cargo** | 9 | 5 | · | · | · | 1 | · | · | 1 | · | 3 | 19 |
| **tanker** | 4 | · | 3 | · | · | · | · | · | · | · | · | 7 |
| **fishing** | · | · | · | 7 | · | · | · | · | · | · | · | 7 |
| **small_boat** | · | · | · | 1 | 6 | · | 10 | 5 | · | 1 | 12 | 35 |
| **warship** | · | · | · | · | · | · | · | 6 | · | · | · | 6 |
| **submarine** | · | · | · | · | · | · | · | · | 2 | · | · | 2 |

**mobileclip-s1-zeroshot — seaships_test** (top-1 55.0%, n=300)

| gt \ pred | cargo | container | tanker | passenger | fishing | tug | sailboat | small_boat | warship | submarine | coast_guard | n |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **cargo** | 98 | 1 | 16 | · | 3 | 24 | · | 4 | 3 | 1 | · | 150 |
| **container** | 14 | 32 | · | · | · | 4 | · | · | · | · | · | 50 |
| **passenger** | · | · | · | 13 | · | 8 | 2 | 18 | 1 | · | 8 | 50 |
| **fishing** | 2 | · | 4 | · | 22 | 9 | · | 8 | 4 | 1 | · | 50 |

**mobileclip-s1-zeroshot — gf_test** (top-1 92.9%, n=56)

| gt \ pred | cargo | passenger | tug | sailboat | small_boat | warship | submarine | n |
|---|---|---|---|---|---|---|---|---|
| **cargo** | 8 | · | · | · | · | · | · | 8 |
| **passenger** | · | 13 | 2 | · | 1 | · | · | 16 |
| **sailboat** | · | · | · | 15 | · | 1 | · | 16 |
| **warship** | · | · | · | · | · | 8 | · | 8 |
| **submarine** | · | · | · | · | · | · | 8 | 8 |

**mobileclip-s1-zeroshot — synthetic** (top-1 44.7%, n=76)

| gt \ pred | cargo | container | tanker | fishing | tug | sailboat | small_boat | warship | submarine | coast_guard | unknown | n |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **cargo** | 9 | 3 | 3 | · | · | · | 1 | · | 3 | · | · | 19 |
| **tanker** | 1 | · | 4 | · | · | · | · | · | 2 | · | · | 7 |
| **fishing** | · | · | · | 6 | · | · | · | · | · | 1 | · | 7 |
| **small_boat** | 3 | · | 2 | 2 | 5 | 1 | 7 | 4 | 6 | 1 | 4 | 35 |
| **warship** | · | · | · | · | · | · | · | 6 | · | · | · | 6 |
| **submarine** | · | · | · | · | · | · | · | · | 2 | · | · | 2 |

**dinov2-s14-probe — seaships_test** (top-1 94.7%, n=300)

| gt \ pred | cargo | container | passenger | fishing | n |
|---|---|---|---|---|---|
| **cargo** | 147 | · | 1 | 2 | 150 |
| **container** | 4 | 46 | · | · | 50 |
| **passenger** | · | · | 50 | · | 50 |
| **fishing** | 9 | · | · | 41 | 50 |

**dinov2-s14-probe — gf_test** (top-1 26.8%, n=56)

| gt \ pred | cargo | passenger | fishing | sailboat | warship | submarine | n |
|---|---|---|---|---|---|---|---|
| **cargo** | · | · | 8 | · | · | · | 8 |
| **passenger** | 1 | 15 | · | · | · | · | 16 |
| **sailboat** | · | 3 | 13 | · | · | · | 16 |
| **warship** | · | 8 | · | · | · | · | 8 |
| **submarine** | 8 | · | · | · | · | · | 8 |

## Chosen approach

1. **Library / shore enrichment: Gemini 2.5 Flash** (chosen on dev: 93.8% on gf_dev vs ≤ 35% for every CLIP zero-shot; it is the only
   backend that returns features and markings). Held-out: 92.9% gf_test, 64.4% SeaShips test, 48.7% synthetic. This is what
   is stored in `imagery.json` and shown in the UI (`model: gemini-2.5-flash`).
2. **Edge fallback: MobileCLIP-S1 zero-shot, square-padded** (best zero-shot on SeaShips dev, 51.3%; it is the same tower the
   edge already embeds with, so the type costs only a 12×dim matmul on embeddings the edge already computes).
   Held-out: 55.0% SeaShips test, 92.9% gf_test, 44.7% synthetic. Gemini confidence is self-reported; CLIP confidence is a
   softmax over 12 prompt groups (T = 100) — **neither is calibrated**.
3. **Probes are not used for the UI**: excellent in-domain (DINOv2 probe 94.7% SeaShips test) but they only know the
   4 SeaShips classes and collapse off-domain (19–38% on gf_test / synthetic). Worth revisiting once we have per-site labels
   covering the full taxonomy.

## Limits / caveats

* **Small, correlated test sets.** gf_test is 7 tracks (56 frames); per-frame accuracy overstates independence. The
  MobileCLIP-S1 gf_dev → gf_test jump (22.9% → 92.9%) shows how clip-dependent these numbers are.
* **Label semantics.** SeaShips "passenger ship" and "fishing boat" are mostly small Chinese river/coastal craft; Gemini and
  CLIP often call them small_boat / tug / coast guard. That is a taxonomy mismatch as much as a model error.
* **Synthetic set**: labels are the generation-prompt class; many "small boat" dets are tiny IR/SAR blobs, where Gemini
  answers *unknown* (12/35) — arguably correct abstention, scored as wrong here. Cargo↔container↔tanker confusions dominate.
* **Aspect, size, features and markings are not evaluated** (no ground truth). CLIP size is a type prior, not a measurement.
  Gemini features are a visual opinion and can hallucinate on low-resolution crops.
* No identification of specific vessels is attempted or stored; markings are only *visible / not visible*.
* The ship type is display-only and does not change priority. If it is later used for scoring, only map types that are
  unambiguous to existing classes (e.g. warship, submarine, tanker) and re-run `go run ./cmd/bench` (synthetic oracle, not
  field accuracy).
* Gemini needs network + GCP credentials; the edge path must not depend on it.

## Reproduce

Data: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/datasets/seaships7000` → `~/data/datasets/seaships7000`,
`.../edgeml/eval/ghost_fleet_5fps/<clip>` → `~/data/eval/<clip>`, SeaShips annotations `~/data/catalog/annotations/seaships7000.jsonl`.

```bash
cd edgeml
# dev (tuning); ablation: same command without --square into ~/data/shipcls
python tools/ship_classify_eval.py --square --sets seaships_train seaships_dev gf_dev \
  --models mobileclip-s1 mobileclip-s0 siglip-b16 openclip-b32 dinov2-s14 --out ~/data/shipcls_sq
python tools/ship_classify_eval.py --sets seaships_dev gf_dev --models gemini-2.5-flash --out ~/data/shipcls_sq
# locked test + synthetic, once, frozen config
python tools/ship_classify_eval.py --square --sets seaships_train seaships_test gf_test synthetic \
  --models mobileclip-s1 mobileclip-s0 siglip-b16 openclip-b32 dinov2-s14 --out ~/data/shipcls_sq
python tools/ship_classify_eval.py --sets seaships_test gf_test synthetic --models gemini-2.5-flash --out ~/data/shipcls_sq
python tools/ship_classify_report.py --out ~/data/shipcls_sq --sets seaships_dev gf_dev seaships_test gf_test synthetic \
  --confusion gemini-2.5-flash mobileclip-s1-zeroshot dinov2-s14-probe
# write the library records
python tools/ship_classify_library.py --pred ~/data/shipcls_sq/pred_gemini-2.5-flash_synthetic.json
```
Gemini auth used a gcloud user access token (`gcloud auth print-access-token`), since ADC was not configured.
