# Coverage + retrain: more views (aerial UAV, IR), a multi-class detector fine-tune, a supervised ship-type probe

**Goal (Patrick):** cover as many ship viewpoints and scenarios as possible. Before this work the edge detector (COCO YOLO11n), the shore
candidates and ship typing (MobileCLIP-S1 zero-shot / Gemini) were not trained on our data, and the data was almost all surface-level RGB.

**TL;DR**
* Data (additive only): SeaDronesSee (aerial UAV, CC0) is ingested. ir_maritime_vessels (IR) is cleaned into **clean v3**, but its license is
  **unknown → internal dev only**. Clean v2 and every raw object/table are untouched.
* Detector: DETECTOR_TLDR
* Ship type: a linear probe on frozen MobileCLIP-S1 / DINOv2-B / SigLIP-B features scores 89–96 % on SeaShips test and 95–99 % on UAV test
  (zero-shot: 55 %, Gemini: 64 %). It **collapses on our onboard YouTube views** (gf_test 27–41 % vs zero-shot 93 % / Gemini 93 %).
  Ship type stays display-only, and no default changes (§3).

## 1. Phase A: data coverage

### 1a. SeaDronesSee (aerial UAV, CC0-1.0): ingested
* Source: HF `dronefreak/SeaDronesSee` @ `deb1f872ffe7efa4b7ee3a672562d16b6dc0ca5c` (public, not gated, `cardData.license: cc0-1.0`).
  Script: `datasets/ingest/seadronessee.py`. Raw copy in `raw/seadronessee/` (7.4 GB, immutable), model-ready copy in `datasets/seadronessee/images/`,
  catalog rows in `catalog/{images,annotations}/seadronessee.jsonl`. Registry `status: ingested`.
* 10,477 labelled images, 67,390 boxes: swimmer 43,302 · boat 15,236 · buoy 4,949 · jetski 2,650 · life_saving_appliances 1,253.
* **Split by recording, never by frame.** The HF mirror has no per-image video id, and its upstream train/val interleave frames from the same flights,
  so neither can be used as-is. `infer_sequences` rebuilds recordings: sort the numeric frame ids and start a new segment on any id gap, resolution
  change, or thumbnail jump. That gives 938 segments, grouped into 34 contiguous blocks, and whole blocks are assigned to train/dev/test.
  The 336 frames nearest a block boundary are marked `guard` and are used for nothing.
  Result: train 5,435 / dev 1,690 / test 3,016 / guard 336 images (`datasets/seadronessee/sequences.jsonl`).
  *Caveat:* the recordings are inferred, not the official `source.video` ids. A recording that the heuristic split into two blocks could straddle
  splits. The guard band reduces this risk but does not prove it never happens.

### 1b. ir_maritime_vessels (IR, surface): clean v3, internal dev only
* **License check:** the HF card (`hanchong/real-infrared-maritime-vessel-dataset` @ `64a6646`) declares no license and no upstream source/paper,
  and nothing else in the repo establishes one. **The license is unknown, so the set is `license_use: internal_dev_only`.** It is never used to train
  the detector or anything that ships. Here it is used only as an evaluation set and to train one explicitly internal-only classifier probe (`probe-all`, §3).
* `edgeml/tools/clean_v3.py` parses the Pascal VOC files, clips boxes to the frame, and maps the 7 native classes to the taxonomy (liner→passenger,
  bulk carrier→cargo, container ship→container, fishing boat→fishing, warship, sailboat, canoe→small_boat). It splits by VOC `<folder>` recording
  (233 recordings): train 6,864 / dev 457 / test 1,081 images, 26,445 boxes.
  Issues are logged, not hidden: 8,402 `xml_filename_differs_from_stem` (joined by stem, per DATA_CLEANUP.md) and 1,000 `upstream_test_unlabeled`, which are excluded.
* **Clean v3 = clean v2 copied unchanged + IR + SeaDronesSee.** Published to a new prefix,
  `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/clean/v3/coverage-20261105/`, and new BigQuery tables
  `ghost_fleet.{images,annotations,datasets}_clean_v3` (68,596 images / 215,302 boxes / 34 datasets).
  No raw object, no v1/v2 object, and no v2 table was modified or deleted.

### 1c. Coverage table (clean v3 + MOT eval sets; `edgeml/tools/coverage_table.py`)

Viewpoint is per dataset (SeaShips / Singapore / FVessel = fixed shore cameras; SMD onboard sample / Ghost Fleet YouTube = onboard; SeaDronesSee = aerial;
IR = surface). Light is a per-frame median-luma heuristic (night < 45, twilight < 80, else day; for videos it is sampled per clip and extrapolated).
IR uses its recording-hour metadata where present, and `unknown` means the heuristic could not run (e.g. Singapore videos not mirrored as frames).
Size bucket = longer box side rescaled to a 640 px long edge: **far < 24 px**, mid 24–96, near ≥ 96. Box counts are vessel boxes only.

| viewpoint | modality | light | images | far boxes | mid boxes | near boxes |
|---|---|---|---:|---:|---:|---:|
| aerial | rgb | day | 10,095 | 9,721 | 7,374 | 307 |
| aerial | rgb | night | 226 | 24 | 170 | 31 |
| aerial | rgb | twilight | 156 | 170 | 77 | 12 |
| fixed | rgb | day | 9,463 | 342 | 5,202 | 8,304 |
| fixed | rgb | night | 29 | 0 | 1 | 28 |
| fixed | rgb | twilight | 91 | 0 | 8 | 83 |
| fixed | rgb | unknown | 13,410 | 23,582 | 62,332 | 16,179 |
| onboard | rgb | day | 34,860 | 2,060 | 3,950 | 1,804 |
| onboard | rgb | night | 219 | 0 | 0 | 0 |
| onboard | rgb | twilight | 239 | 0 | 0 | 0 |
| shore | rgb | day | 290 | 1 | 7 | 59 |
| surface | ir | day | 6,346 | 7,788 | 7,784 | 7,482 |
| surface | ir | night | 764 | 0 | 61 | 1,079 |
| surface | ir | unknown | 1,292 | 342 | 908 | 1,001 |

**Remaining gaps**
* **Satellite / SAR: none.** No overhead EO or SAR imagery at all. Candidates (license checks still needed): xView3-SAR, HRSID, SSDD, ShipRSImageNet, DOTA ship.
* **Night RGB: almost none.** 219 onboard night frames with zero vessel boxes, 29 fixed-camera night frames, 226 aerial. Night is covered only by
  IR (764 frames), and IR is internal-only.
* **Far/small onboard vessels with boxes:** 2,060 far boxes onboard. This is the sidecar's main use case, and it is the cell the detector struggles on (§2).
* **Shore (handheld) views:** 290 frames. **Other weather (fog, rain, glare)** is not labelled anywhere.
* IR is the only thermal source, and it cannot ship until a license is found.

## 2. Phase B: edge detector

**Recipe (`edgeml/tools/cov_build_detector_dataset.py`, `cov_train.sh`).** Unlike G's `single_cls` fine-tune, the COCO 80-class head is kept.
Maritime GT is added as extra COCO `boat` (class 8) examples, and SeaDronesSee swimmers as `person`. Training is 22,398 images: 5,000 COCO val2017
replay images (all 80 classes), SeaShips train (1,746), SDS train (5,435), and FVessel/GF dev clips outside the holdout. Non-boat objects in maritime
frames get YOLO11x pseudo-labels (1,497 boxes). Far-target frames are repeated (4,696) and tiled at native resolution (4,181 tiles).
YOLO-val has 2,766 images. Two configs were trained from `yolo11n.pt` at 640 for 15 epochs on one T4: `yolo11n-cov-640` (full fine-tune) and
`yolo11n-cov-640-fz10` (first 10 layers frozen). IR is excluded (unknown license).

### 2a. Dev only (selection)

Far/small dev slice (1,683 vessel objects < 32² px at 640, never trained on; IoU ≥ 0.3, conf ≥ 0.25, class `boat`; `cov_eval_farsmall.py`):

| detector | far/small recall | SDS dev (927) | SeaShips valid (194) | FVessel 08/10 (530) | GF holdout (32) | dets/img |
|---|---|---|---|---|---|---|
| yolo11n COCO 640 (default) | 0.343 | 0.334 | 0.634 | 0.226 | 0.781 | 1.36 |
| yolo11n-cov-640 | **0.739** | 0.852 | 0.866 | 0.477 | 1.000 | 2.75 |
FARSMALL_ROWS

C sighting metrics on dev (G's sidecar config: ByteTrack, centroid association, max_lost 6 s, min score 0.25; 15 dev clips, 34 GT objects;
`run_c.py matrix`, aggregated by `cov_agg_c.py`):

| detector | all dev: recall / false / false/min | holdout-only clips (10 obj): recall / false | reports |
|---|---|---|---|
| yolo11n COCO 640 (default) | 0.65 / 36 / 1.32 | 0.70 / 13 | 251 |
| yolo11n-cov-640 | 0.79 / 19 / 0.70 | 0.80 / 13 | 216 |
CDEV_ROWS

All-dev rows are optimistic for the fine-tunes, because 11 of the 15 dev clips (all but the 4 holdout clips) are in their training set.
The holdout-only column is the honest dev number.

B harness, class-agnostic vessel AP50 on dev (`bench_detectors.py --split dev`; SeaShips dev is skipped for fine-tunes because it is their training data, as in G §4b):

| detector | SMD onboard sample dev (MVI_0790/0799) | Singapore Maritime onshore dev | SeaShips dev |
|---|---|---|---|
| yolo11n COCO 640 | 0.639 (τ 0.35) | 0.067 | 0.452 |
| yolo11n-cov-640 | 0.571 (τ 0.25) | 0.054 | skipped (training data) |
BDEV_ROWS

INT8 OpenVINO CPU latency (`edgeml.runtime.bench`, G's harness: 4 pinned threads, 80 SMD 1080p frames; NNCF calibration on 300 YOLO-val frames).
Measured interleaved A/B on this session's 8-vCPU AVX2 box, which drifts by ~±15 % between runs:

| detector | run 1 p50 / p95 ms | run 2 p50 / p95 ms |
|---|---|---|
| yolo11n COCO 640 | 54.4 / 79.4 | 63.7 / 91.3 |
| yolo11n-cov-640 | 50.5 / 79.6 | 61.6 / 96.9 |

The two INT8 graphs are identical (1,066 layers, 73 FakeQuantize nodes, same weight size), so latency is unchanged by construction. G measured
32.4 / 40.9 ms for the same baseline on a quieter box. The p95 ≤ 50 ms target in G's rule is not reproducible on this VM for either model, so the gate here is
"same graph and within noise of the baseline".

**Decision rule (written before the test run, same as G §5).** A candidate wins if, on dev, holdout sighting recall goes up, holdout false sightings are not worse,
far/small recall is not worse, and latency matches the baseline. Then, on the **locked test** (once), sighting recall must go up, false/min must not be worse,
**and SeaShips-test R_small must not drop below the baseline's**. Only then would the default change.

DEV_PICK

### 2b. Locked test (evaluated once per final candidate; `splits.yaml` test)

DETECTOR_TEST

## 3. Ship-type classifier (12-way taxonomy of `docs/reports/SHIP_CLASSIFICATION.md`)

**Setup.** `edgeml/tools/ship_classify_eval.py`: GT crops (shorter side ≥ 20 px), class-balanced samples per native class, fixed seed.
A logistic-regression probe is trained on frozen features (square-padded crops) from three backbones: MobileCLIP-S1 (the edge default),
DINOv2-B/14 and SigLIP-B/16. Three training pools:
* `probe-ss` = SeaShips train;
* `probe-public` = SeaShips train + SeaDronesSee train (both shippable licenses);
* `probe-all` = probe-public + IR train (**internal only**, because of the IR license).

Zero-shot uses the MobileCLIP / SigLIP text prompts from SHIP_CLASSIFICATION.md. Gemini 2.5 Flash uses the existing `GeminiShipClassifier` prompt.
Native→taxonomy maps:
* SeaShips (`SEASHIPS_TO_TYPE`): ore carrier / bulk cargo carrier / general cargo ship→cargo, container ship→container, fishing boat→fishing, passenger ship→passenger.
* IR: liner→passenger, bulk carrier→cargo, container ship→container, fishing boat→fishing, warship, sailboat, canoe→small_boat.
* SeaDronesSee: boat/jetski→small_boat.
* Ghost Fleet: per-track human labels.

Held-out sets (never in any probe's training pool):

| set | source | split rule | crops |
|---|---|---|---:|
| seaships_dev / seaships_test | SeaShips7000 upstream valid / **test (locked)** | upstream split | 300 / 300 |
| gf_dev / gf_test | Ghost Fleet 5 fps dev clips / **test clips (locked)** | splits.yaml, by clip | 48 / 56 |
| ir_dev / ir_test | IR clean v3 dev / test | by VOC recording | 251 / 360 |
| sds_test | SeaDronesSee clean v3 test | by inferred recording | 240 |

**Full held-out sets: top-1 (macro recall, n)**

| model | seaships_dev top-1 (macro-recall, n) | seaships_test top-1 (macro-recall, n) | gf_dev top-1 (macro-recall, n) | gf_test top-1 (macro-recall, n) | ir_dev top-1 (macro-recall, n) | ir_test top-1 (macro-recall, n) | sds_test top-1 (macro-recall, n) | ms/crop b1 | ms/crop b32 |
|---|---|---|---|---|---|---|---|---|---|
| dinov2-b14-probe-all | 93.0% (90.5%, 300) | 92.3% (90.2%, 300) | 31.2% (15.6%, 48) | 32.1% (36.2%, 56) | 85.7% (84.7%, 251) | 87.2% (87.2%, 360) | 95.8% (95.8%, 240) | 201.7 | 172.1 |
| dinov2-b14-probe-public | 94.7% (93.0%, 300) | 96.0% (95.3%, 300) | 10.4% (5.2%, 48) | 26.8% (28.7%, 56) | 35.5% (22.4%, 251) | 30.3% (30.3%, 360) | 98.3% (98.3%, 240) | 201.7 | 172.1 |
| dinov2-b14-probe-ss | 95.3% (94.0%, 300) | 96.3% (95.8%, 300) | 20.8% (10.4%, 48) | 33.9% (33.8%, 56) | 30.3% (26.0%, 251) | 26.1% (26.1%, 360) | 0.0% (0.0%, 240) | 201.7 | 172.1 |
| mobileclip-s1-probe-all | 88.3% (84.2%, 300) | 89.7% (85.5%, 300) | 0.0% (0.0%, 48) | 35.7% (42.5%, 56) | 70.9% (70.8%, 251) | 90.3% (90.3%, 360) | 97.1% (97.1%, 240) | 136.3 | 114.0 |
| mobileclip-s1-probe-public | 89.7% (85.5%, 300) | 89.7% (85.8%, 300) | 8.3% (4.2%, 48) | 28.6% (30.0%, 56) | 30.7% (18.5%, 251) | 22.5% (22.5%, 360) | 98.3% (98.3%, 240) | 136.3 | 114.0 |
| mobileclip-s1-probe-ss | 92.3% (89.5%, 300) | 92.0% (89.7%, 300) | 10.4% (5.2%, 48) | 26.8% (27.5%, 56) | 21.1% (18.0%, 251) | 20.8% (20.8%, 360) | 0.0% (0.0%, 240) | 136.3 | 114.0 |
| mobileclip-s1-zeroshot | 51.3% (41.7%, 300) | 55.0% (49.8%, 300) | 22.9% (30.2%, 48) | 92.9% (95.0%, 56) | 41.0% (35.5%, 251) | 38.6% (38.6%, 360) | 82.5% (82.5%, 240) | 136.3 | 114.0 |
| siglip-b16-probe-all | 83.0% (76.8%, 300) | 83.0% (76.2%, 300) | 0.0% (0.0%, 48) | 41.1% (46.2%, 56) | 69.3% (63.9%, 251) | 86.1% (86.1%, 360) | 98.8% (98.8%, 240) | 175.6 | 134.8 |
| siglip-b16-probe-public | 85.3% (79.3%, 300) | 85.3% (79.7%, 300) | 8.3% (4.2%, 48) | 37.5% (36.2%, 56) | 30.7% (18.5%, 251) | 24.7% (24.7%, 360) | 99.6% (99.6%, 240) | 175.6 | 134.8 |
| siglip-b16-probe-ss | 89.7% (85.5%, 300) | 89.7% (86.2%, 300) | 8.3% (4.2%, 48) | 37.5% (36.2%, 56) | 27.9% (24.5%, 251) | 23.9% (23.9%, 360) | 0.0% (0.0%, 240) | 175.6 | 134.8 |
| siglip-b16-zeroshot | 33.0% (37.2%, 300) | 33.3% (38.0%, 300) | 35.4% (58.3%, 48) | 82.1% (80.0%, 56) | 30.7% (23.0%, 251) | 31.9% (31.9%, 360) | 73.3% (73.3%, 240) | 175.6 | 134.8 |
| gemini-2.5-flash | 61.7% (50.8%, 180) | 63.9% (59.2%, 180) | 93.8% (93.8%, 48) | 92.9% (95.0%, 56) | 47.9% (50.5%, 142) | 52.2% (52.2%, 180) | 76.7% (76.7%, 60) | 1248 |  |

**Same items as Gemini** (Gemini was capped at 30 crops per native class because of the shared Vertex quota; every model is re-scored on exactly those items):

| model | seaships_test top-1 (macro-recall, n) | gf_test top-1 (macro-recall, n) | ir_test top-1 (macro-recall, n) | sds_test top-1 (macro-recall, n) | ms/crop b1 | ms/crop b32 |
|---|---|---|---|---|---|---|
| dinov2-b14-probe-all | 92.8% (90.8%, 180) | 32.1% (36.2%, 56) | 91.1% (91.1%, 180) | 91.7% (91.7%, 60) | 201.7 | 172.1 |
| dinov2-b14-probe-public | 95.6% (95.0%, 180) | 26.8% (28.7%, 56) | 31.1% (31.1%, 180) | 95.0% (95.0%, 60) | 201.7 | 172.1 |
| dinov2-b14-probe-ss | 96.1% (95.8%, 180) | 33.9% (33.8%, 56) | 26.7% (26.7%, 180) | 0.0% (0.0%, 60) | 201.7 | 172.1 |
| mobileclip-s1-probe-all | 89.4% (85.8%, 180) | 35.7% (42.5%, 56) | 92.8% (92.8%, 180) | 95.0% (95.0%, 60) | 136.3 | 114.0 |
| mobileclip-s1-probe-public | 90.0% (86.7%, 180) | 28.6% (30.0%, 56) | 24.4% (24.4%, 180) | 96.7% (96.7%, 60) | 136.3 | 114.0 |
| mobileclip-s1-probe-ss | 92.2% (90.6%, 180) | 26.8% (27.5%, 56) | 22.2% (22.2%, 180) | 0.0% (0.0%, 60) | 136.3 | 114.0 |
| mobileclip-s1-zeroshot | 56.7% (52.2%, 180) | 92.9% (95.0%, 56) | 38.3% (38.3%, 180) | 81.7% (81.7%, 60) | 136.3 | 114.0 |
| siglip-b16-probe-all | 83.9% (78.1%, 180) | 41.1% (46.2%, 56) | 90.0% (90.0%, 180) | 95.0% (95.0%, 60) | 175.6 | 134.8 |
| siglip-b16-probe-public | 86.1% (81.4%, 180) | 37.5% (36.2%, 56) | 27.8% (27.8%, 180) | 98.3% (98.3%, 60) | 175.6 | 134.8 |
| siglip-b16-probe-ss | 90.0% (87.2%, 180) | 37.5% (36.2%, 56) | 26.1% (26.1%, 180) | 0.0% (0.0%, 60) | 175.6 | 134.8 |
| siglip-b16-zeroshot | 34.4% (39.4%, 180) | 82.1% (80.0%, 56) | 28.9% (28.9%, 180) | 80.0% (80.0%, 60) | 175.6 | 134.8 |
| gemini-2.5-flash | 63.9% (59.2%, 180) | 92.9% (95.0%, 56) | 52.2% (52.2%, 180) | 76.7% (76.7%, 60) | 1248 |  |

Latency is per crop on 4 CPU threads of the T4 VM's Xeon. The probe adds < 1 ms on top of the backbone. Gemini is a network round trip.

**Confidence-gated hybrid** (`edgeml/tools/ship_classify_hybrid.py`): trust the probe when its max probability ≥ τ, otherwise fall back to
MobileCLIP zero-shot. τ is chosen on dev only (mean macro recall over seaships_dev, gf_dev and ir_dev).

| probe → fallback | τ (dev) | seaships_test | gf_test | ir_test | sds_test |
|---|---:|---:|---:|---:|---:|
| mobileclip-s1-probe-public → zero-shot | 0.70 | 72.7 % (66.3) | 57.1 % (62.5) | 32.5 % | 93.8 % |
| mobileclip-s1-probe-all → zero-shot (internal) | 0.35 | 88.3 % (84.2) | 39.3 % (46.3) | 83.9 % | 96.3 % |

**Reading**
* **Supervised probes win in-domain by a wide margin.** On SeaShips test: 89.7 % (MobileCLIP probe) and 96 % (DINOv2 probe), vs 55 % zero-shot and 64 % Gemini.
  On UAV (SDS test): 96–99 % vs 82 % / 77 %.
  For IR, the probe trained with IR (internal) scores 86–90 % vs 39 % / 52 %. Without IR in training the probes score ~25 % on IR, i.e. IR → RGB transfer is poor.
* **They do not transfer to our onboard YouTube views.** gf_test is 27–41 % for every probe vs 93 % for both MobileCLIP zero-shot and Gemini, and gf_dev is the same.
  The probes' training pools contain no warships, submarines, coast guard or tugs from onboard angles, and they over-predict `cargo` / `small_boat`.
  The open-vocabulary models generalise to those views, and the probes overfit their sources.
* gf_dev/gf_test are small (48/56 crops from 6 + 7 tracks), so these cells carry about ±10–15 pt binomial noise. The gap to zero-shot (≥ 50 pt) is far outside that noise.
* The dev-chosen hybrid recovers part of each side but does not beat zero-shot on gf_test or the probe on its own domains. With 6 dev tracks, τ is poorly identified.
* **Decision: ship type stays display-only, and the MobileCLIP-S1 zero-shot default is unchanged.**
  No single model wins on all views on held-out data, and the only probe that covers IR uses an internal-only set.
  The probe tooling (`--probes`, clean v3 sets, the hybrid gate) stays in so that onboard-view labels can be added later.
  The next step is labelled onboard warship / coast guard / tug crops from new GF clips, split by clip.

## 4. Data discipline

Nothing below the double line was used for any training, threshold or model choice. Locked test = `edgeml/configs/splits.yaml` `test`
(+ clean v3 `eval_split=test` for the two new sets); each final detector candidate was scored on it exactly once.

| dataset (license) | detector train | detector YOLO-val (best.pt pick) | far/small dev slice (selection) | classifier probe train | classifier dev | locked test |
|---|---|---|---|---|---|---|
| COCO val2017 (CC BY 4.0 annotations) | 5,000 images, all 80 classes (replay) | — | — | — | — | — |
| SeaShips7000 | upstream train (1,746 img) | upstream valid (1,741 img) | 194 objects from upstream valid | seaships_train (upstream train) | seaships_dev (upstream valid) | upstream test: AP50 / R_small (§2), seaships_test |
| SeaDronesSee (CC0) | clean v3 train (5,435 img; boat/jetski→`boat`, swimmer→`person`) | clean v3 dev, every 4th frame (422 img) | 927 objects from those dev frames | sds_train | — | sds_test (classifier only) |
| FVessel clip10 | dev clips 01–04, 06 | holdout dev clips 08, 10 | 530 objects (clips 08, 10) | — | — | clips 07, 09 (C sighting metrics) |
| Ghost Fleet 5 fps | dev clips minus holdout | holdout dev clips Submarine_surfacing, Ship_in_the_Distance_Utopia_of_the_Seas | 32 objects (holdout clips) | — | gf_dev (dev clips) | 9 test clips (C sighting metrics), gf_test |
| SMD onboard sample | — | — | — | — | B harness dev (MVI_0790/0799) | MVI_0797/0801 (AP50) |
| ir_maritime_vessels (**license unknown → internal dev only**) | **excluded** | — | — | ir_train, **probe-all only (internal)** | ir_dev | ir_test (classifier only) |
| fvessel_clip05 | never (quarantined, overlaps clip07) | — | — | — | — | — |
| SDS `guard` frames (336) | never | — | — | — | — | — |

* Pseudo-labels: YOLO11x (COCO) at 1280 px, conf ≥ 0.5, keeps the non-boat COCO classes alive on maritime frames (1,497 boxes; teacher `boat` boxes
  are dropped, because maritime GT is the only boat source). The teacher never sees test frames.
* Far/small oversampling: training frames with a GT vessel whose long side is < 24 px at 640 are repeated (4,696 repeats) and cut into
  native-resolution 640 tiles around the far target (4,181 tiles). Only train-split frames are used.
* Far/small dev slice = every vessel GT with area < 32² px after rescaling the frame to a 640 long edge (COCO tiny+small, the same
  buckets as `eval/detection.py`), taken from never-trained dev frames only: 1,683 objects (SeaShips valid 194, SDS dev 927, FVessel 08/10 530, GF holdout 32).
  It is scored by `edgeml/tools/cov_eval_farsmall.py` (IoU ≥ 0.3, conf ≥ 0.25).
* Detector selection used only the YOLO-val mAP (for best.pt) and the far/small dev slice + C dev matrix (for the candidate).

## 5. Caveats

* **SeaDronesSee recordings are inferred.** The HF mirror has no `source.video` ids. The split is by contiguous frame-id blocks with guard bands,
  so a recording could still leak across splits. SDS dev/test numbers (far/small slice, sds_test) are therefore somewhat optimistic.
  The fix is to re-ingest from the official COCO json (which carries `video_id`); the ingest's `infer_sequences` would then be replaced by a join on that id.
* **IR license unknown.** IR is internal dev only. `probe-all` (and its IR numbers) must not ship. No detector saw IR.
* **Lighting is a heuristic.** Median luma cannot tell night from dark water or overcast scenes, and video frames are classified from samples.
  The night/twilight rows are indicative, not labels.
* **Small held-out sets.** gf_dev/gf_test (48/56 crops, 6/7 tracks), the GF far/small slice (32 objects) and the C sighting test (52 objects) are small,
  so differences of a few points are noise.
* **One training seed per config** on a T4 (L4 was stocked out), 15 epochs. No hyper-parameter search beyond the two configs in `cov_train.sh`.
* **Latency box.** CPU latency is measured on this session's 8-vCPU AVX2 Xeon (no AVX-512/VNNI, like G's box), with 4 pinned threads (`taskset -c 0-3`) on the 80 SMD 1080p frames.
  Absolute p95 on a shared VM is noisy, so the baseline and candidate were measured back-to-back, and only their ratio is meaningful.
* Gemini was run on a per-native-class capped subset because of the shared Vertex quota. Compare it only with the "same items" table.
* Ship-type crops are GT boxes. End-to-end (detector box → type) accuracy is lower for every model, as in SHIP_CLASSIFICATION.md.

## 6. Reproduce

```sh
# Phase A (GCS writes are additive; refuses to overwrite)
python datasets/ingest/seadronessee.py --snapshot <HF snapshot @ deb1f87>      # raw/ + datasets/ + catalog, sequences.jsonl
python edgeml/tools/clean_v3.py --v2 <clean v2 catalog/> --ir-raw <IR raw> --ir-catalog <catalog/images> \
    --sds-catalog <catalog> --sds-sequences sequences.jsonl --output ~/data/clean_v3      # issues.jsonl, audit.json
python edgeml/tools/coverage_table.py --catalog ~/data/clean_v3/catalog --data-root ~/data \
    --ir-recordings ~/data/clean_v3/ir_recordings.jsonl --eval-root ~/data/edgeml_eval --out coverage.json
# detector (GPU box)
python edgeml/tools/cov_build_detector_dataset.py --catalog ~/data/clean_v3/catalog --out ~/data/yolo_cov --device 0
bash edgeml/tools/cov_train.sh ~/data/yolo_cov/data.yaml ~/runs/cov
python edgeml/tools/cov_eval_farsmall.py --slice ~/data/yolo_cov/farsmall_dev.json --catalog ~/data/clean_v3/catalog \
    --weights yolo11n.pt ~/runs/cov/<run>/weights/best.pt --out farsmall_dev.json
python edgeml/tools/run_c.py cache --data ~/data/edgeml_eval --sets ghost_fleet_5fps fvessel_clip10 --split dev \
    --detector <best.pt> --name <run> --imgsz 640 --conf 0.05 --device 0
python edgeml/tools/run_c.py matrix --data ~/data/edgeml_eval --sets ghost_fleet_5fps fvessel_clip10 --split dev \
    --detectors yolo11n-640 <run> --trackers bytetrack --trk '{"asso_func":"centroid","max_lost_s":6}' --min-score 0.25 --out ...
#   locked test: the same two commands with --split test, once per final candidate; B harness:
EDGEML_ZOO_EXTRA="<run>=<best.pt>:seaships7000" python edgeml/tools/bench_detectors.py --split test --taus taus.json \
    --sets seaships7000:5,smd_vis_onboard_sample:1 --models yolo11n,<run> ...
python edgeml/tools/g_export.py --weights <best.pt> --name <run> --size 640 --calib <dev frames> --out ~/models/COV --class-names model
taskset -c 0-3 python -m edgeml.runtime.bench --model ~/models/COV/<run>/int8/model.xml --engine openvino --frames <1080p frames> --threads 4 --duration 60
# ship type
python edgeml/tools/ship_classify_eval.py --sets seaships_train ir_train sds_train seaships_dev seaships_test gf_dev gf_test ir_dev ir_test sds_test \
    --models mobileclip-s1 dinov2-b14 siglip-b16 --square --probes probe-public=seaships_train+sds_train \
    probe-all=seaships_train+ir_train+sds_train probe-ss=seaships_train --out ~/data/shipcls_cov
python edgeml/tools/ship_classify_eval.py --sets seaships_test gf_test ir_test sds_test --models gemini-2.5-flash --out ~/data/shipcls_cov
python edgeml/tools/ship_classify_report.py --out ~/data/shipcls_cov --sets ... [--subset-from gemini-2.5-flash]
python edgeml/tools/ship_classify_hybrid.py --out ~/data/shipcls_cov --probe mobileclip-s1-probe-public --fallback mobileclip-s1-zeroshot \
    --dev seaships_dev gf_dev ir_dev --test seaships_test gf_test ir_test sds_test
```
