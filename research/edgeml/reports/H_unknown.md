# H — unknown / unusual-object detection via synthetic data, and calibration of `unknownness`

Branch `devin/1793846107-h-unknown-synth` (started from #46's branch, rebased onto `main` after #46 merged). Everything here is
dev-only except §6, which was run **once** at the end with every knob frozen.

## 0. TL;DR

| | before (main + #46 defaults) | after (this PR's sidecar defaults) |
|---|---|---|
| live `unknownness` | MCM vs 11 subtype prompts, `sim_ref=0.30`, no kNN bank → **AUROC 0.49** known-vs-unknown on synth dev (coin flip) | dev-fitted `sim_ref/sim_width` + 98-crop kNN bank auto-loaded from `unknown_calib.npz` → **AUROC 0.75–0.85** dev (two halves), **0.87** on the locked test (held-out classes vs real test vessels; before 0.57) |
| unknown flag (`unknown_real`) | ungated: 4/13 known dev vessels flagged "unknown"; gate opt-in | realness-gated by default: dev 0/13 known vessels flagged, 5/6 surfaced synthetic unusual contacts still flagged; locked test 9/21 known flagged (before 14/21), 5/8 surfaced synthetic flagged (before 3/8), 0 false unknown sightings (before 1) |
| YOLO11n on unusual contacts | box recall@0.25: semi-sub 0.00, jet-ski 0.20, buoy 0.26, USV 0.34 | unchanged by default. Opt-in `--anomaly-fallback` (horizon-band colour anomaly proposer) doubles surfaced synth tracks (6→12/24) but adds 95 false sightings on 391 sea-only frames → **not** a default |
| dataset | none | `edgeml_synth_unknown`: 35 MOT sequences / 1135 frames / 1208 synthetic boxes on dev footage, in the bucket + catalog |

Winner = calibration artifact + realness gate. Prompt-bank edits, temperature and kNN-weight tweaks did not beat
plain `calibrated + kNN k=5` reliably on dev, so the prompt bank and `unknown_threshold=0.5` are unchanged.

## 1. Data discipline — which set was used for what

| purpose | data |
|---|---|
| synthetic backgrounds | DEV clips only: `ghost_fleet_5fps` dev (A_ship_in_the_distance, Cruise_ship_Mediterranean…, Far_away_but_did_you_see_that_splash, Sailing_POV_Corfu_Paxos, Ship_in_the_Distance_Utopia…, Submarine_surfacing) + `ghost_fleet_r2_1fps` (Cruise_Ship_POV, Ship_crossing_Mariana_Trench, Sunrise_Somewhere…). Chosen by filtering `configs/splits.yaml`; `Spotted_A_Submarine` and all other test clips were never opened before §6 |
| cut-outs | 56 Gemini (`gemini-2.5-flash-image`, lab project) green-screen renders, chroma-keyed; 7 classes |
| synthetic DEV classes (tuning) | `semi_submersible`, `usv`, `jet_ski`, `buoy` — 20 sequences, 615 frames, 24 synthetic tracks |
| synthetic TEST classes (held out, unseen during tuning) | `periscope_mast`, `life_raft`, `debris_container` — 15 sequences, 520 frames, 23 synthetic tracks |
| calibration / kNN bank (`unknown_calib.npz`) | 98 GT crops of **known** vessels from dev real sequences only (ghost_fleet_5fps dev + fvessel_clip10 dev + r2 dev). No synthetic crops, no test crops |
| dev AUROC | known crops split by track id (crc32 parity): fit on one half, evaluate on the other, so no evaluated crop is in the bank |
| tuning (variants, thresholds, gate, proposer `z_thr`) | synth dev + real dev only |
| final evaluation (§6, run once) | synth TEST classes + the 9 locked `ghost_fleet_5fps` test clips (incl. `Spotted_A_Submarine`), frozen artifact fitted on dev only |

No training was done (nothing is fine-tuned); "fitting" = the two scalars `sim_ref/sim_width`, the kNN reference
scalars and the bank itself.

## 2. Synthetic set `synth_unknown` (`edgeml/tools/h_gen_sprites.py`, `h_synth_compose.py`)

* Sprites: Gemini renders each object on a flat green screen (3 views × classes, 1024px), keyed in HSV with a
  spill-suppressed alpha; tiny objects (periscopes) need the alpha-fraction floor at 0.2 %, not 2 %.
* Placement: horizon from the frame's dominant sky/sea edge (`HorizonBandDetector` estimate, EMA across the clip);
  pinhole scale `px = f · L / d` with camera height 15 m, HFOV 65°, object length per class (semi-sub 12 m, USV 7 m,
  jet-ski 3 m, buoy 2 m, periscope 0.6 m, life-raft 3 m, container 6 m) and distance from the row's depth below the
  horizon; constant velocity + sinusoidal bob/roll, tracked across the sequence (MOT `gt.txt` row per frame); the
  background pans with the real clip motion.
* Appearance matching: Reinhard tone transfer to the local sea patch, distance-dependent haze toward the horizon
  colour, Gaussian blur matched to the local Laplacian variance of the background, and ±1 px jitter.
* Existing GT vessels in the background frames stay as the *known* population (`attrs.synthetic=false`).
* Layout = the other `~/data/edgeml_eval/<set>` MOT sets (`seqinfo.ini`, `img1/`, `gt/gt.txt`, `gt/labels.json`,
  `synth.json`, set-level `index.jsonl` with `split`, `synth_class`, `synth_tracks`).

Contact sheets: [`H_unknown/contact_crops.jpg`](H_unknown/contact_crops.jpg) (one crop per synthetic track, dev
rows first, test rows last) and [`H_unknown/contact_frames.jpg`](H_unknown/contact_frames.jpg) (full frames with
GT boxes). Known weaknesses: the Gemini "periscope" renders are often small surfaced-submarine silhouettes rather
than a bare mast; a few buoys are placed on the inshore Corfu clip where scale is optimistic; one container crop is
nearly invisible (7×6 px). These are visible in the sheets and were left in (they are the hard cases).

Bucket: `gs://…/edgeml/eval/synth_unknown/` (frames + GT), `gs://…/edgeml/eval/synth_unknown_sprites/` (cut-outs).
Catalog: `edgeml_synth_unknown` (`edgeml/scripts/register_synth_unknown_catalog.py`; 1135 images, 1474 boxes of
which 1208 synthetic, `split` dev/test as above).

## 3. Dev measurements (before)

### 3a. Does YOLO11n (640, sidecar ORT export, all 80 COCO labels kept at ≥0.05) fire on them?

Match = IoU ≥ 0.3 **or** detection centre inside the GT box with ≥10 % of its area. "track3" = a GT track with
≥3 matched frames at the sidecar's score threshold 0.25 and a maritime-ish label (`boat`, `person`, `bird`,
`airplane`, `surfboard`, `kite`; the sidecar keeps those) — roughly "would the tracker get a chance".

| class | tracks | boxes | box recall any@0.05 | any@0.25 | `boat`@0.25 | track3 maritime@0.25 | labels fired @0.25 |
|---|---|---|---|---|---|---|---|
| buoy | 6 | 214 | 0.71 | 0.26 | 0.15 | 0.50 | boat 33, person 11, bird 9 |
| jet_ski | 5 | 69 | 0.46 | 0.20 | 0.16 | 0.20 | boat 10, person 3, surfboard 1 |
| semi_submersible | 7 | 118 | 0.07 | **0.00** | 0.00 | 0.00 | — |
| usv | 6 | 134 | 0.56 | 0.34 | 0.33 | 0.33 | boat 44 |

The closed-set detector is essentially blind to low-freeboard objects; what it does see it mostly calls `boat`
(fine — the subtype/unknownness stage sits behind it) or `person`/`bird`.

### 3b. Is `unknownness` higher for them than for known vessel types? (crop level, MobileCLIP-S1 ORT)

Crops: 98 known GT vessel crops (dev real), 138 synthetic unknown crops (dev classes), 21 "noise" crops
(YOLO false positives ≥0.25 on sea-only frames). Known crops split by track-id parity into fit/eval halves; the
two columns are the two possible assignments (dev set is small — 49 eval crops each — so read the spread).

| variant | AUROC unknown vs known (half A / half B) | noise vs known | per class (A): buoy / jet_ski / semi_sub / usv |
|---|---|---|---|
| **before**: sidecar default (MCM, `sim_ref` .30, no bank) | **0.49 / 0.53** | 0.78 / 0.82 | 0.24 / 0.57 / 0.84 / 0.46 |
| calibrated MCM only | 0.45 / 0.47 | 0.83 / 0.79 | 0.17 / 0.48 / 0.77 / 0.45 |
| **calibrated + kNN bank k=5 (shipped)** | **0.85 / 0.75** | 0.96 / 0.90 | 0.63 / 0.82 / 0.84 / 0.72 |
| calibrated + kNN k=3 | 0.85 / 0.75 | 0.97 / 0.90 | |
| calibrated + kNN k=10 | 0.86 / 0.74 | 0.96 / 0.90 | |
| … w_knn = 0.75 | 0.85 / 0.78 | 0.97 / 0.90 | p90 of known crops rises to 0.77 → more known tracks cross τ=0.5 |
| … w_knn = 1.0 (kNN only) | 0.84 / 0.76 | 0.92 / 0.78 | saturates at 1.0 |
| … temperature ×0.5 / ×2 | 0.86 / 0.77, 0.84 / 0.74 | 0.97, 0.96 | |
| … prompt bank `bg+` (extra sea/sky/wake/foam background prompts) | identical to k=5 | | background prompts never beat the max subtype similarity |
| … prompt bank `generic_unknown` (adds "unidentified floating object", "debris", …) | 0.75 / 0.70 | 0.93 / 0.88 | hurts: the generic prompts attract known vessels too |
| combo k=10, w .75, T ×0.5 | 0.86 / 0.75 | 0.97 / 0.92 | |

Take-aways: MCM alone cannot separate these (a semi-submersible crop is "a submarine", a buoy crop is "a buoy" —
both are *in* the subtype prompt bank, so by construction they are not "unknown" to MCM; `buoy` is the weakest
class for every variant for exactly that reason). The kNN distance to real dev vessel crops is what carries the
signal, consistent with D.md. Nothing beats plain calibrated+kNN k=5 on both halves, so that is what ships;
`w_knn`, temperature and prompt bank stay at their defaults.

### 3c. Does realness gating keep them while suppressing noise? (full replay: cached dets → ByteTrack → aggregator)

`replay` runs the sidecar's `_ZeroShotAggregator` over the cached detections of synth dev + 9 real dev clips;
`surfaced` = a sighting was emitted for the GT track, `flagged` = `unknown_real` (gate) or `unknownness ≥ 0.5`
(no gate). `false sightings` are sightings on frames of complete-GT real clips that carry no GT box.

| config | synth surfaced / 24 | synth flagged unknown | known vessels flagged unknown / 13 | false unknown sightings (391 sea-only frames) |
|---|---|---|---|---|
| before: default classifier, no gate | 6 | 1 | **9** | 1 |
| before + gate | 6 | 1 | 4 | 1 |
| **after: calibrated artifact, no gate** | 6 | **5** | 4 | 1 |
| **after + gate (shipped default)** | 6 | **5** | **0** | 1 |
| after, τ = 0.65 (gate) | 6 | 0 | 0 | 0 |

The gate removes every false "unknown" on known vessels (they are tracked, steady targets whose kNN distance is
still large-ish because the bank is only 98 crops) while keeping all five flagged synthetic contacts — on dev it
is strictly better, so `gate_unknown_bonus_on_realness=True` becomes the sidecar default
(`EdgemlEngine` only; `AggregatorParams` dataclass default is untouched for the offline tools). τ stays 0.5:
0.65 kills the synthetic flags.

## 4. Improvement candidates (dev)

1. **Calibration artifact** `unknown_calib.npz` (`ZeroShotClassifier.save_calibration/load_calibration`,
   `load_classifier()` auto-loads `<embed-dir>/unknown_calib.npz`): wins, see 3b/3c. Fitted with
   `h_eval_unknown.py fit --variant '{"cal":true,"bank":true,"k":5}'` on the 98 dev known crops; uploaded next to
   the MobileCLIP export, fetched by `make demo-models`.
2. **Realness gate on by default** in the sidecar: wins (3c).
3. Prompt bank / temperature / `w_knn`: no reliable gain → unchanged.
4. **Open-set score**: not added as a new field — with the gate, `unknown_real` already is
   `unknownness ≥ τ ∧ realness ≥ τ_r ∧ hits ≥ 3`; a weighted sum of det-conf + unknownness + realness was not
   separable from that on 24 synthetic tracks and would be another untestable knob.
5. **Cheap "anything-on-water" proposer** `detectors/anomaly.py` (`SeaAnomalyProposer`: per-row Lab colour
   statistics below the horizon at 480 px, 2-pass inlier re-estimation, z > 3.5, bright-achromatic (foam/glint)
   penalty, ≤8 proposals/frame, label `anomaly`; `AnomalyFusionDetector` adds proposals only where no closed-set
   detection ≥0.25 — the track-start score — covers them). `z_thr` 2.5 vs 3.5 swept on dev; 3.5 wins.

   | class | box recall any@0.25 YOLO → +anomaly | track3@0.25 YOLO → +anomaly |
   |---|---|---|
   | buoy | 0.26 → 0.78 | 0.50 → 0.83 |
   | jet_ski | 0.20 → 0.68 | 0.20 → 0.80 |
   | semi_submersible | 0.00 → 0.34 | 0.00 → 0.43 |
   | usv | 0.34 → 0.40 | 0.33 → 0.50 |

   Replay (after + gate): synth surfaced 6 → **12**/24, but false sightings on sea-only real frames 1 → **95**
   (the gate still removes every false *unknown* flag: 89 → 0, and keeps 6 synthetic flags). 0.24 extra sightings
   per sea frame would flood the shore queue, so this ships **opt-in** (`python -m edgeml.sidecar
   --anomaly-fallback`, optional JSON kwargs) and is not a default. It is the right next lever once the
   proposer gets a temporal-persistence stage (track-level, not frame-level) — see §7.

## 5. What changed for the demo

* `make demo-models` also fetches `edgeml/models/D/clip/mobileclip-s1/unknown_calib.npz`.
* `python -m edgeml.sidecar --embed ~/models/D/mobileclip-s1` auto-loads that file (logged via
  `EdgemlEngine.unknown_calib`; absent file → old MCM defaults, nothing breaks) and runs with
  `gate_unknown_bonus_on_realness=True`.
* `--anomaly-fallback [JSON]` opt-in.
* Nothing outside `edgeml/` + the two `Makefile` lines.

## 6. Locked TEST — run once (synthetic held-out classes + 9 locked `ghost_fleet_5fps` test clips)

Every knob (artifact, τ=0.5, gate, prompt bank, proposer `z_thr`=3.5) was frozen before this ran. Outputs:
`~/data/h_results/{recall_test,recall_test_fusion_z35,auroc_test,replay_test_before,replay_test_after,replay_test_fusion_after}.json`.
Note: the opt-in fusion rows below were measured before two Devin-Review fixes to the fallback path (`cover_thr` 0.05 → 0.25 and the sidecar label filter on the cached base, both in `edgeml/tools`/`detectors/anomaly.py`); the dev fusion numbers in §4 were re-measured after the fixes, the test rows were **not** re-run (test is evaluated once). The shipped default path (no fallback) is unaffected.

### 6a. YOLO11n class-agnostic recall on the held-out synthetic classes (same matcher as §3a)

| class | tracks | box recall any@0.05 | any@0.25 | `boat`@0.25 | track3 maritime@0.25 | labels fired @0.25 |
|---|---|---|---|---|---|---|
| debris_container | 8 | 0.52 | 0.35 | 0.31 | 0.38 | boat 83, person 5, bird 3, bench 1 |
| life_raft | 7 | 0.88 | 0.56 | 0.53 | 0.71 | boat 111, kite 4, bird 2, frisbee 1 |
| periscope_mast | 8 | 0.12 | **0.07** | 0.02 | 0.12 | person 6, boat 2 |
| *opt-in `--anomaly-fallback`* debris_container | 8 | 0.70 | 0.53 | 0.31 | 0.75 | + anomaly 48 |
| *opt-in* life_raft | 7 | 0.90 | 0.59 | 0.53 | 0.86 | + anomaly 5 |
| *opt-in* periscope_mast | 8 | 0.42 | 0.37 | 0.02 | 0.50 | + anomaly 36 |

Same picture as dev: the bigger, higher-contrast objects (life raft, container) get a `boat` box roughly half the
time; a periscope mast is invisible to the closed-set detector (0.07) and only the anomaly proposer finds it.

### 6b. `unknownness` AUROC on the locked test (crop level, bank frozen = the 98 dev crops shipped in `unknown_calib.npz`)

Crops: 263 known GT vessel crops from the 9 real test clips, 137 synthetic unknown crops (held-out classes),
171 noise crops (YOLO FPs ≥0.25 on sea-only test frames).

| variant | AUROC unknown vs known | noise vs known | unknown vs noise | per class: debris / life_raft / periscope | median known / p90 known |
|---|---|---|---|---|---|
| before: sidecar default (MCM, `sim_ref` .30, no bank) | **0.57** | 0.43 | 0.63 | 0.66 / 0.48 / 0.56 | 0.58 / 0.88 |
| **after: calibrated + kNN bank k=5 (shipped)** | **0.87** | 0.48 | 0.86 | 0.93 / 0.83 / 0.85 | 0.47 / 0.61 |

The dev gain (0.49→0.75–0.85) transfers to unseen object classes and unseen real clips (0.57→0.87). Known-crop
scores also drop below τ=0.5 at the median (0.58→0.47), but the p90 of known test crops (0.61) is still above τ —
the 98-crop dev bank does not cover every test vessel type, which is what §6c shows at the sighting level.

### 6c. Full replay (cached dets → ByteTrack → aggregator), 23 synthetic test tracks + 45 known tracks in the 9 locked clips

21 of the 45 known tracks are ever surfaced by the sidecar path (many GT tracks are tiny/short); 214 sea-only frames.

| config | synth surfaced / 23 | synth flagged unknown | known surfaced vessels flagged unknown / 21 | false unknown sightings | false sightings (any) |
|---|---|---|---|---|---|
| before: default classifier, no gate (= old sidecar default) | 8 | 3 | **14** | 1 | 2 |
| before + gate | 8 | 1 | 2 | 0 | 2 |
| after: calibrated artifact, no gate | 8 | **8** | 17 | 2 | 2 |
| **after + gate (shipped default)** | 8 | **5** | **9** | **0** | 2 |
| after + gate + opt-in anomaly fallback | 10 | 5 | 5 / 22 | 2 | **46** |

Against the real baseline (old sidecar default, ungated) the shipped default flags more of the unusual contacts
that the detector surfaces (5/8 vs 3/8), flags fewer known vessels as unknown (9/21 vs 14/21) and produces no
false unknown sighting on sea-only frames (0 vs 1). It does **not** reproduce the dev result of 0 known vessels
flagged: 9/21 surfaced known test vessels still cross τ with realness, consistent with the p90 in §6b (inference:
the bank is 98 dev crops and lacks the test clips' vessel appearances — e.g. the submarine, destroyer and docked
cruise-ship views exist only in test). The most conservative setting, `before + gate` (2/21 known flagged), only
keeps 1/8 synthetic flags, so the calibrated artifact is the right trade for a "surface the unusual" workflow;
growing the bank with more dev crops (§7) is the obvious next step. No synthetic periscope mast was surfaced at all
without the anomaly fallback (6a). The fallback again buys +2 surfaced synthetic tracks for 44 extra false sightings
on 214 sea-only frames, confirming the dev decision to ship it opt-in only.

## 7. Open items / next

* The dev set is tiny (24 synthetic tracks, 13 known tracks, 98 bank crops); AUROC moves ±0.05 with the fit/eval
  split and 9/21 known test vessels still get flagged (§6c). Growing the kNN bank with the remaining dev GT crops
  (fvessel/SMD/r2 dev, thousands of crops) is free, needs no test data, and should pull `p90_known` under τ.
* Anomaly proposer: add a 1–2 s persistence filter (ByteTrack already does some of this; the 95 false sightings are
  mostly glint/foam tracks that live ≥3 frames) before considering it as a default.
* `buoy` and `submarine` are *known* subtypes by prompt-bank design; if the product wants "buoy" flagged as unusual,
  drop it from `SUBTYPES` rather than fighting the score.
* No GPU VM was created for this workstream (everything ran on the session VM CPU + Vertex Gemini calls); cost ≈
  56 Gemini image generations only.
