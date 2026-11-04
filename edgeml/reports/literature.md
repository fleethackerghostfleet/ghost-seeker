# Workstream L — literature review that changes the experiment design

Scope: decision-oriented review for the phase-1 edge ML pipeline (`edgeml/README.md`, `edgeml/CONTRACTS.md`).
Every finding ends in an implication for workstream **A** (eval data), **B** (detector benchmark), **C** (tracker +
sighting aggregator + selector) or **D** (CPU runtime + embeddings). Written in hours, not days, so the evidence
grades are explicit:

- **[read]** claim checked against the paper's abstract/README/doc this session (arXiv API, DOI metadata, GitHub).
- **[recalled]** claim from memory of the paper; title/venue verified but the specific number was not re-read.
- **[inferred]** our extrapolation from the literature to our setting; treat as a hypothesis to test on `dev`.

---

## 1. Design implications (one page)

| # | Recommendation | WS | Evidence |
|---|---|---|---|
| 1 | **Ship crops, not frames; make the "fact" the default form.** Analytics-aware streaming (DDS, Vigil, Glimpse, EAAR) all converge on: detect locally, send only object regions and metadata, let the receiver ask for more. Our `forms` ladder (fact→crop→thumb→full) is the right shape. | C | DDS [1], Vigil [4], Glimpse [5], EAAR [6] [recalled]; §2 |
| 2 | **Crop with a context margin of ~1.5× the box (≥25% padding each side), minimum 64–96 px on the short side, JPEG q≈50 for ML / q≈75–80 for humans.** Open-vocabulary detectors feed CLIP 1.0× and 1.5× crops; CLIP-class embedders degrade sharply when the object is <32 px. Johnson criteria put human *recognition* at ≈8 px and *identification* at ≈13 px across the critical dimension; budget 3–4× that for JPEG loss and motion blur. | C, D | ViLD [20] [recalled], Johnson criteria [recalled], §2.3 |
| 3 | **Under a hard byte budget use greedy value/byte with a cheap-form-first ladder (what we already do), but cap bytes per object and give diminishing value to each later form of the same object.** Chameleon/VideoStorm/AWStream pick configurations by Pareto "quality vs cost" profiles and re-profile periodically; none of them sends two copies of the same object. | C | Chameleon [8], VideoStorm [9], AWStream [10] [recalled]; §2.4 |
| 4 | **Baseline tracker = ByteTrack (no appearance) with BoT-SORT-style camera motion compensation; add appearance (embedding) only in the aggregator's re-link step, not in per-frame association.** ByteTrack's two-stage association on *all* boxes is what suppresses the fragmented tracks that become duplicate sightings; OC-SORT's observation-centric re-update fixes drift during gaps; BoT-SORT's GMC (sparse optical flow / ECC) handles platform motion. All three are in `boxmot`. | C | ByteTrack [11] [read], OC-SORT [12] [read], BoT-SORT [13] [read]; §3.1 |
| 5 | **Stabilise around the horizon before tracking on ship-mounted cameras.** Horizon detection + roll/pitch correction is the standard trick in maritime tracking from moving platforms and is far cheaper than full image registration. | C, A | Fefilatyev & Goldgof [16] [recalled], Prasad et al. survey [22] [read]; §3.2 |
| 6 | **Best key frame = argmax of a cheap quality score (detector confidence × box area × sharpness × distance from image border), not just detector confidence.** Face/person "best shot" literature (QAN, SER-FIQ, MagFace) shows set-level recognition improves when frames are weighted by a learned quality that correlates with blur/size/occlusion; a hand-built proxy captures most of it. | C | QAN [17], SER-FIQ [18], MagFace [19] [recalled]; §3.3 |
| 7 | **Report `Frag` (fragmentations) and IDSW from TrackEval alongside HOTA/IDF1; our "duplicates per object" is `Frag` at the sighting layer.** Also report HOTA at a low localisation threshold (α=0.3) since far vessels have tiny boxes. | C, A | CLEAR MOT [14], HOTA [15] [read]; §3.4 |
| 8 | **Benchmark detectors class-agnostically ("vessel") and bucket by box area; expect zero-shot COCO detectors to be fine on near/large vessels and to collapse on <32² px.** ABOships measured exactly this for COCO-pretrained detectors; SAHI tiling recovers +5–7 AP for small objects with no fine-tuning. Tile **only a horizon band** to keep CPU cost flat. | B, D | ABOships [25] [read], SAHI [28] [read]; §4 |
| 9 | **CPU detector candidates, in order: YOLO11n/YOLO26n (≈40 mAP, ~2.5 M params) → D-FINE-N (42.8 AP) → YOLO-World-S only if open-vocab is needed at the detector. RT-DETR-R18 is too heavy for 4 threads at ≥2 FPS.** Expect ~2–3× speed-up from OpenVINO INT8 via NNCF PTQ at <1 mAP loss. | D, B | Ultralytics tables [31] [read], D-FINE [33] [read], NNCF [35] [read] ; §5 |
| 10 | **Frame gate: Reducto-style cheap differencing (pixel/edge/area features with a per-clip calibrated threshold), target 1–5 Hz into the detector; never gate below 1 Hz because time-to-first-report is a headline metric.** | D, C | Reducto [2], Glimpse [5], FilterForward [7] [read/recalled]; §5.3 |
| 11 | **Unknownness = 1 − max cosine to a vessel-prompt bank, combined with kNN distance to a dev-set embedding bank (MCM + kNN-OOD). Skip open-world detectors (ORE/OW-DETR/PROB) in phase 1.** Both scores are a few µs on an existing embedding; open-world detectors need training and still score low on unknown recall. | D, C | MCM [40], kNN-OOD [39], ORE/OW-DETR/PROB [41–43] [read]; §6 |
| 12 | **Sighting schema should mirror a lookout's contact report: bearing (from box x + camera FOV), relative size/range proxy, type, aspect/heading, and "closing/opening"; updates only when one of those changes.** This matches both the Navy lookout handbook and the Army SALUTE/SPOTREP structure. | C, A | Lookout handbook, SALUTE [recalled]; §7 |

---

## 2. Bandwidth-aware perception / task-oriented transmission (WS C, D)

### 2.1 What the systems literature does
The "analytics-aware streaming" line of work splits into three strategies, all relevant:

- **Send regions, server decides.** *Vigil* [4] detects at the camera and uploads only frames/regions with objects,
  ranked by objects added (order-of-magnitude bandwidth cut [recalled]). *Glimpse* [5] tracks locally and offloads
  only "trigger" frames. *EAAR* [6] encodes predicted ROIs at high quality and the rest low [recalled]. **DDS** [1]:
  send a low-quality pass, the server DNN returns uncertain regions, the camera re-sends only those at high quality
  [recalled — numbers not re-read]. *Elf* [36] predicts next-frame proposals and offloads them in parallel.
- **Encode for the DNN, not the eye.** *AccMPEG* [3] learns a per-macroblock "accuracy gradient" with a cheap model
  and sets QP from it [read]. Shao et al. [37] transmit information-bottleneck features with a temporal entropy model
  instead of pixels [read]; Gündüz et al. [38] survey this task-oriented framing [read].
- **Filter at the edge.** *Reducto* [2] drops frames using cheap difference features (pixel/edge/corner/area) with
  per-query calibrated thresholds; *FilterForward* [7] runs micro-classifiers and backhauls only matching events
  (order-of-magnitude reduction [read]).
- **Configuration adaptation.** *VideoStorm* [9], *Chameleon* [8], *AWStream* [10] profile accuracy vs cost over
  (resolution, fps, model) knobs, keep the Pareto front, and re-profile periodically because the best knob drifts
  [recalled].

**What transfers.** We are far more constrained (72 KB/min worst case; DDS assumes Mbps links), so the DDS
"server asks for more" loop is only viable as a *shore-pull* of `full` for a sighting already reported by `fact`.
The primitives that do transfer: (i) detect + track at the edge so the unit of transmission is an **object**;
(ii) metadata first, pixels second; (iii) spend bytes on the object, none on sea/sky — for us simply *crop, then
JPEG*, which discards background instead of coding it at low quality. **Implication (C):** keep the four-form
ladder; `fact` (~300 B) already fits 240×/min at the lowest budget, so no fifth form is needed.

### 2.2 Split inference
*Neurosurgeon* [34] and the IB-feature line [37] win over shipping pixels only when the link is stable enough for a
continuous feature stream and both ends run the same model version. Neither holds on DDIL with a shore team that may
swap models. **Implication (D): don't bother with split inference in phase 1.** JPEG crops are model-agnostic and
human-legible.

### 2.3 Crop margin and resolution: what keeps objects identifiable
- **For a second-stage model.** CLIP-based open-vocab detectors (ViLD [20] [recalled]) classify crops padded to
  **1.0× and 1.5×** the box and ensemble them. CLIP/MobileCLIP take 224–256 px inputs; objects <~32 px are upsampled
  >7× and zero-shot accuracy collapses [inferred; measure].
- **For a human.** Johnson criteria (1958) [recalled]: *recognition* ≈4 line pairs (≈8 px), *identification* ≈6.4
  (≈13 px) across the critical dimension at 50% probability, noise-free; JPEG q≈50 and spray blur cost 2–3×.
- **Implication (C, D):** crop = box × 1.5 (clamped), short side **≥64 px for ML-only, ≥96–128 px for humans**, JPEG
  q≈50 (`crop`) and q≈75–80 (`thumb`, ≤320 px wide). WS D's bytes-vs-legibility test should sweep margin and short
  side and score both MobileCLIP subtype accuracy and a human read-off (E1).

### 2.4 Value per byte under a hard budget
Chameleon/VideoStorm/AWStream [8–10] maximise utility under a resource bound from precomputed quality–cost
profiles; greedy value/byte is near-optimal when items are small relative to the budget (true for facts and crops;
**false for `full` at 72 KB/min**, where one frame is the whole budget). Two refinements the literature supports:

1. **Diminishing returns per object.** DDS and Vigil value a region by the *new* information it adds. Multiply the
   k-th form of a sighting by a decay (e.g. 1, 0.5, 0.25, 0.1 for fact→crop→thumb→full) so bytes spread across objects
   before upgrading one; the Go scheduler's `ladder_weight` already has this shape — reuse its weights in
   `edgeml.select.greedy` so numbers are comparable.
2. **Byte cap per object per window.** At the two lowest budgets forbid `full` unless `value` is very high, or one
   frame starves everything. **Implication (C):** add `ladder_decay` and `max_bytes_per_object` to the selector.

---

## 3. Deduplication by identity (WS C, A)

### 3.1 Tracking-by-detection on CPU
- **ByteTrack** [11] [read]: two-pass association (high-score boxes, then low-score boxes to unmatched tracks);
  80.3 MOTA / 77.3 IDF1 / 63.1 HOTA on MOT17 and +1–10 IDF1 when added to nine other trackers. Kalman + IoU +
  Hungarian: negligible CPU. The low-score pass is what keeps a far, flickering vessel as **one** track instead of
  five — directly fewer duplicates per object.
- **OC-SORT** [12] [read]: fixes Kalman drift across missed frames via a virtual trajectory on re-observation. Same
  CPU profile; relevant because our detector runs at 1–5 Hz and misses frames.
- **BoT-SORT** [13] [read]: adds camera-motion compensation (sparse-feature global motion) and a better Kalman
  state; 80.5 MOTA / 80.2 IDF1 / 65.0 HOTA. Its ReID variant, **StrongSORT** [44] and **Deep OC-SORT** [45] add
  per-box appearance with heavier models — not needed per frame.
- **Implication (C):** baseline = ByteTrack + BoT-SORT GMC from `boxmot`; tune `track_thresh`, `match_thresh`,
  `track_buffer` on dev, with `track_buffer` in **seconds** so it survives gate-driven frame-rate changes. Embed
  **once per track at key-frame time**, for re-link only.

### 3.2 Camera motion on a moving platform
BoT-SORT's GMC assumes a mostly rigid background; on a ship the dominant motion is **roll/pitch**, which moves the
horizon and everything near it. The maritime literature (Fefilatyev & Goldgof, buoy camera [16] [recalled]; Prasad
et al. [22] [read]) detects the horizon line (Hough/RANSAC on edges or the sea–sky boundary), applies the
rotation+translation that pins it, and tracks in stabilised coordinates — O(image) per frame, far above 30 FPS on CPU.
**Implication (C):** optional horizon-pinned pre-step; measure IDSW/Frag with and without on onboard SMD dev
sequences (`MVI_0790`, `MVI_0799`). **Implication (A):** tag onboard sequences' sea state next to `viewpoint` in
`index.jsonl` so results split by platform motion.

### 3.3 Best key frame per track
Video face/person recognition uses **quality-aware pooling**: QAN [17] learns per-frame quality weights; SER-FIQ
[18] uses embedding stability under dropout; MagFace [19] puts quality in embedding magnitude [recalled]. Quality
correlates with size, sharpness, un-occluded view and distance from the frame edge; a hand-built proxy captures most
of the gain [inferred]. **Implication (C):** `q = score × sqrt(area) × laplacian_var(crop) × border_margin`, updated
online; emit an `update` with a new `best_frame_idx` only if `q` improves > X% (start X=30%, tune on dev). Use
**"first good-enough frame"** semantics: the first frame with `q ≥ q_min` ships immediately so time-to-first-report
is not delayed by waiting for the best shot.

### 3.4 Re-identification after gaps
Vessel ReID literature is thin: a 2024 thermal paper reports 81.8% Top-1 on its own data [27] [read]; MVTD [26]
[read] shows SOTA trackers degrade on maritime video and recover only with fine-tuning — which we are not doing.
**Implication (C):** keep re-link conservative: within `relink_gap_s` (start 10–20 s), continue a sighting iff
cosine(embedding) ≥ τ_e **and** predicted position (Kalman or horizon-relative bearing) is within a window. Prefer
*missed* re-links (one extra report) over *wrong* ones (merging two contacts, which a watch-stander never forgives).

### 3.5 Metrics
HOTA [15] separates detection from association; IDF1 [14] rewards identity persistence; CLEAR MOT [14] adds
**Frag** and IDSW. CONTRACTS "duplicates per object" is Frag at the sighting layer; "reports per object" has no
standard analog. **Implication (C, A):** report HOTA/IDF1/MOTA/IDSW/Frag via TrackEval, plus HOTA at α=0.3 (IoU
thresholds tuned for pedestrians punish 10-px boxes).

---

## 4. Maritime detection (WS B, A)

### 4.1 Datasets and what they tell us
| Dataset | Content | Why it matters for us |
|---|---|---|
| SMD / Singapore Maritime [22] [read] | onshore + onboard + NIR video, tracks | only public *onboard* RGB video with identities; also NIR |
| SeaShips [23] [read, DOI] | 7,000 images, 6 ship classes, inshore fixed cam | large, near objects; easy; use for class-agnostic sanity |
| ABOships [25] [read] | 9 vessel types + seamarks + floaters, scale/occlusion tags; **evaluates COCO-pretrained detectors out-of-the-box by object size** | the published zero-shot-on-maritime baseline closest to WS B |
| MODS [24] [read] | USV viewpoint, obstacle detection + segmentation | onboard, low horizon, small obstacles |
| LaRS [29] [read] | panoptic, diverse maritime scenes | sea/sky/horizon segmentation; horizon prior |
| SeaDronesSee [30] [read] | aerial, humans/boats in water, tiny | tiny-object regime |
| MVTD [26] [read] | 182 seqs / 150k frames, boat/ship/sailboat/USV, VOT | maritime tracking difficulty; distractors (reflections) |
| FVessel [32] [read] | video + AIS, inland waterway | identity ground truth via AIS |
| VAIS [21] [read, DOI] | paired visible + IR ship images | the only paired RGB/IR vessel set |

**Implication (A):** ABOships' per-size breakdown and MODS' small-obstacle regime are the reference points; make sure
GT carries box area so WS B can report recall by tiny/small/medium/large.

### 4.2 Small / far objects
SAHI [28] [read]: **+6.8 / +5.1 / +5.3 AP** for FCOS/VFNet/TOOD on VisDrone/xView from inference-time slicing
alone. Cost is linear in tiles; the maritime prior is that far objects sit on the horizon. **Implication (B, D):**
full frame once plus **2–4 tiles in a ±10% band around the detected horizon**, merged with NMS/WBF (SAHI does this);
sweep tile size/overlap on dev and report CPU ms alongside AP.

### 4.3 False positives: own-ship structure, glare, whitecaps
Prasad et al. [22] [read] catalogue wakes, sun glint, whitecaps, reflections and own-ship structure in onboard
footage; MVTD [26] confirms reflections/low contrast break trackers. **Implication (C):** (1) a per-camera
**own-ship mask** (detections persistent at the same pixels for >N minutes are structure, not contacts); (2) open a
sighting only after ≥2 detections (ByteTrack `tentative`) and weight `value` by track age. **Implication (B):**
report class-agnostic "vessel" AP *and* false positives per frame on sea-only frames.

### 4.4 IR vs RGB
VAIS [21] found RGB and IR complementary with off-the-shelf CNN features weaker on IR [recalled]; the thermal ReID
paper [27] confirms shape must carry identity without colour. **Implication (B):** RGB only in phase 1; IR is a
fine-tuning-era question.

### 4.5 Zero-shot / open-vocabulary detectors
OWL-ViT [46], OWLv2 [47] (LVIS-rare AP 31.2→44.6 with self-training [read]), Grounding DINO [48], YOLO-World [49]
(35.4 LVIS AP, 52 FPS on V100 [read]). None reports maritime numbers; ABOships' COCO-pretrained baselines are the
closest evidence [read]. COCO's `boat` class already makes closed-set detectors "zero-shot vessel detectors" for
near/medium vessels. **Implication (B):** benchmark YOLO11n/26n (`boat`), D-FINE-N, YOLO-World-S with prompts
{boat, ship, vessel, sailboat, buoy, person}, plus one heavy open-vocab model (OWLv2 or Grounding DINO, L4 GPU) as
**upper bound / teacher** only — it will not run on the laptop.

---

## 5. Edge inference on CPU (WS D)

### 5.1 Detector budgets
Ultralytics' ONNX Runtime FP32 CPU latencies (32-core Xeon, 640 px): **YOLO11n 56 ms / 39.5 mAP, YOLO11s 90 ms /
47.0, YOLO26n 39 ms / 40.9, YOLO26s 87 ms / 48.6** [read]. Expect 2–4× slower on 4 threads of a 12th-gen U-series
[inferred]: `n` models at ~5–8 FPS, `s` at ~2–3 FPS, so only `n` leaves room for tiling and the embedder at ≥2
processed FPS. **D-FINE-N**: 42.8 AP, 472 FPS on T4 [read], CPU latency unpublished, ~4 M params — the one DETR worth
timing. RT-DETR-R18 (46.5 AP, ~20 M params [recalled]) is out of budget. **Implication (D, B):** time YOLO11n,
YOLO26n, D-FINE-N, YOLO-World-S at 640 px / 4 threads, ONNX Runtime vs OpenVINO FP32 vs INT8; the detector must stay
under ~150 ms so tiles and embedder fit.

### 5.2 INT8 and OpenVINO
NNCF [35] [read] post-training INT8: <1% accuracy drop on standard CV models, ~2–3× CPU speed-up [recalled].
Ultralytics exports directly (`format=openvino, int8=True`) from a small calibration set. **Implication (D):**
calibrate on **dev** frames only (calibration is tuning); report mAP delta and latency with 4 threads and the
OpenVINO `LATENCY` hint over sustained runs (§5.4).

### 5.3 Embeddings
MobileCLIP [50] [read]: 3–15 ms on iPhone-class hardware at 50–150 M params; MobileCLIP2 [51] [read] improves
accuracy at equal latency. Expect ~20–40 ms per crop on 4 CPU threads [inferred]. **Implication (D):** embed only at
key-frame events and re-link candidates, so embedder cost scales with sightings, not detections. MobileCLIP-S0/S1 (or
MobileCLIP2-S0) via `open_clip` → ONNX, text bank precomputed.

### 5.4 Frame gating, adaptive rate, thermal
Reducto [2] [read] gates on cheap difference features with offline-calibrated thresholds; Glimpse [5] on tracker
confidence; FilterForward [7] on micro-classifiers. The simplest high-yield maritime gate: detector at a 1 Hz floor,
raised to 5 Hz while tracks are young, tentative or near the border [inferred]. 15 W U-series parts throttle under
sustained all-core load, so short benchmarks overstate FPS [recalled, no paper]. **Implication (D):** measure
*sustained* FPS over ≥10 min with 4 threads pinned and the Go edge running; report p95 as well as p50.

---

## 6. Unknown / novel object scoring (WS D, C)

Open-world detectors (ORE [41], OW-DETR [42], PROB [43] [read]) learn an "unknown" class but need training and
report modest unknown recall; not phase-1 material. Embedding-space OOD is cheap: Mahalanobis [52], energy [53],
ViM [54], **kNN distance** [39] (non-parametric, strong across benchmarks [read]) and **MCM** [40] (zero-shot from
CLIP: max softmax over text prompts [read]). **Implication (D):** `unknownness = 1 − softmax_max(cos(e, text_bank)/T)`
blended with normalised kNN distance to a dev-set bank of known vessel crops — a dot product once `e` exists.
**Implication (C):** treat unknownness as a *bonus* in `value`, not a dominant term, until glare/whitecap false
positives are shown not to score "unknown" — they likely will [inferred]; gate the bonus on track age ≥ 2 s.

---

## 7. Human factors: how a watch-stander reports (WS C, A)

Navy lookout training [55] [recalled] teaches a fixed form — **"Bridge, [station], [object], bearing [relative],
range/position angle, [description/aspect], [closing/opening]"** — with follow-ups only when the contact changes
(bearing drift, aspect, CPA) and a "contact lost" closure. Army SPOTREP/SALUTE (Size, Activity, Location, Unit/type,
Time, Equipment) [recalled] has the same shape. Both are **one report per contact per episode, updated on material
change** — exactly the CONTRACTS `open`/`update`/`close` model. **Implication (C, A):** propose optional `Sighting`
fields `bearing_deg` (box centre x + camera FOV/heading), `size_class` (by area bucket), `aspect` (bow/stern/beam if
the subtype model can say), `closing` (sign of box-area growth); define "material update" as a change in any of these,
a label flip, or a confidence jump > 0.2.

---

## 8. Experiments the current briefs don't cover

- **E1 — Crop legibility sweep (C/D).** Margin {1.0,1.25,1.5,2.0}× × short side {48,64,96,128} px × JPEG q {40,50,
  75}: measure bytes, MobileCLIP zero-shot subtype accuracy vs the full-res crop, and a 3-person "can you say what it
  is?" read-off on 50 dev crops. Picks the `crop`/`thumb` parameters with evidence.
- **E2 — Horizon-pinned stabilisation ablation (C).** ByteTrack ± BoT-SORT GMC ± horizon stabilisation on onboard SMD
  dev sequences; report IDSW, Frag, duplicates per object, CPU ms.
- **E3 — Value decay / byte cap in the selector (C).** Greedy with ladder decay {none, 0.5^k, 0.25^k} and
  `max_bytes_per_object` ∈ {∞, 20 KB, 5 KB} at the four budgets; headline = unique objects delivered and bytes per
  delivered object.
- **E4 — Horizon-band tiling cost/benefit (B/D).** Full-frame only vs full + 2/4 horizon tiles at 640 px; recall by
  area bucket vs ms/frame on 4 threads.
- **E5 — Unknownness vs false positives (D/C).** Score glare/whitecap/own-ship false detections on dev with MCM and
  kNN unknownness; if they rank as "unknown", the `value` bonus needs the track-age gate.

Proposed contract changes (not applied here): optional `bearing_deg`, `size_class`, `aspect`, `closing` on
`Sighting`; selector params `ladder_decay` and `max_bytes_per_object`; TrackEval `Frag` added to the tracking row.

---

## References

Systems / bandwidth
1. Du et al., *Server-Driven Video Streaming for Deep Learning Inference* (DDS), SIGCOMM 2020. https://doi.org/10.1145/3387514.3405887
2. Li et al., *Reducto: On-Camera Filtering for Resource-Efficient Real-Time Video Analytics*, SIGCOMM 2020. https://doi.org/10.1145/3387514.3405874
3. Du et al., *AccMPEG: Optimizing Video Encoding for Video Analytics*, MLSys 2022. https://arxiv.org/abs/2204.12534
4. Zhang et al., *The Design and Implementation of a Wireless Video Surveillance System* (Vigil), MobiCom 2015. https://doi.org/10.1145/2789168.2790123
5. Chen et al., *Glimpse: Continuous, Real-Time Object Recognition on Mobile Devices*, SenSys 2015. https://doi.org/10.1145/2809695.2809711
6. Liu, Li, Gruteser, *Edge Assisted Real-time Object Detection for Mobile Augmented Reality*, MobiCom 2019. https://doi.org/10.1145/3300061.3300116
7. Canel et al., *Scaling Video Analytics on Constrained Edge Nodes* (FilterForward), SysML 2019. https://arxiv.org/abs/1905.13536
8. Jiang et al., *Chameleon: Scalable Adaptation of Video Analytics*, SIGCOMM 2018. https://doi.org/10.1145/3230543.3230574
9. Zhang et al., *Live Video Analytics at Scale with Approximation and Delay-Tolerance* (VideoStorm), NSDI 2017. https://www.usenix.org/conference/nsdi17/technical-sessions/presentation/zhang
10. Zhang et al., *AWStream: Adaptive Wide-Area Streaming Analytics*, SIGCOMM 2018. https://doi.org/10.1145/3230543.3230554
34. Kang et al., *Neurosurgeon: Collaborative Intelligence Between the Cloud and Mobile Edge*, ASPLOS 2017. https://doi.org/10.1145/3037697.3037698
36. Zhang et al., *Elf: Accelerate High-Resolution Mobile Deep Vision with Content-Aware Parallel Offloading*, MobiCom 2021. https://doi.org/10.1145/3447993.3448628
37. Shao et al., *Task-Oriented Communication for Edge Video Analytics*, 2022. https://arxiv.org/abs/2211.14049
38. Gündüz et al., *Beyond Transmitting Bits: Context, Semantics, and Task-Oriented Communications*, 2022. https://arxiv.org/abs/2207.09353

Tracking / identity
11. Zhang et al., *ByteTrack*, ECCV 2022. https://arxiv.org/abs/2110.06864
12. Cao et al., *Observation-Centric SORT*, CVPR 2023. https://arxiv.org/abs/2203.14360
13. Aharon et al., *BoT-SORT*, 2022. https://arxiv.org/abs/2206.14651
14. Ristani et al., *Performance Measures and a Data Set for Multi-Target, Multi-Camera Tracking* (IDF1), ECCVW 2016. https://arxiv.org/abs/1609.01775 ; Bernardin & Stiefelhagen, *CLEAR MOT metrics*, 2008. https://doi.org/10.1155/2008/246309
15. Luiten et al., *HOTA*, IJCV 2020. https://arxiv.org/abs/2009.07736
16. Fefilatyev & Goldgof et al., *Detection and tracking of ships in open sea with rapidly moving buoy-mounted camera system*, Ocean Engineering 2012. https://doi.org/10.1016/j.oceaneng.2012.06.028
17. Liu et al., *Quality Aware Network for Set to Set Recognition*, CVPR 2017. https://arxiv.org/abs/1704.03373
18. Terhörst et al., *SER-FIQ*, CVPR 2020. https://arxiv.org/abs/2003.09373
19. Meng et al., *MagFace*, CVPR 2021. https://arxiv.org/abs/2103.06627
44. Du et al., *StrongSORT*, 2022. https://arxiv.org/abs/2202.13514
45. Maggiolino et al., *Deep OC-SORT*, 2023. https://arxiv.org/abs/2302.11813
20. Gu et al., *Open-vocabulary Object Detection via Vision and Language Knowledge Distillation* (ViLD), ICLR 2022. https://arxiv.org/abs/2104.13921

Maritime data
21. Zhang et al., *VAIS: A dataset for recognizing maritime imagery in the visible and infrared spectrums*, CVPRW 2015. https://doi.org/10.1109/CVPRW.2015.7301291
22. Prasad et al., *Video Processing from Electro-optical Sensors for Object Detection and Tracking in Maritime Environment: A Survey* (SMD), 2016. https://arxiv.org/abs/1611.05842
23. Shao et al., *SeaShips*, IEEE TMM 2018. https://doi.org/10.1109/TMM.2018.2865686
24. Bovcon et al., *MODS*, 2021. https://arxiv.org/abs/2105.02359
25. Iancu et al., *ABOShips*, 2021. https://arxiv.org/abs/2102.05869
26. *MVTD: A Benchmark Dataset for Maritime Visual Object Tracking*, 2025. https://arxiv.org/abs/2506.02866
27. *Vessel Re-identification and Activity Detection in Thermal Domain for Maritime Surveillance*, 2024. https://arxiv.org/abs/2406.08294
28. Akyon et al., *Slicing Aided Hyper Inference* (SAHI), ICIP 2022. https://arxiv.org/abs/2202.06934
29. Žust et al., *LaRS*, ICCV 2023. https://arxiv.org/abs/2308.09618
30. Varga et al., *SeaDronesSee*, WACV 2022. https://arxiv.org/abs/2105.01922
32. Guo et al., *Asynchronous Trajectory Matching-Based Multimodal Maritime Data Fusion* (FVessel), 2023. https://arxiv.org/abs/2302.11283

Detectors / runtime
31. Ultralytics YOLO11 / YOLO26 model tables (CPU ONNX latencies). https://docs.ultralytics.com/models/yolo11/ , https://github.com/ultralytics/ultralytics
33. Peng et al., *D-FINE*, ICLR 2025. https://arxiv.org/abs/2410.13842 ; DEIM, 2024. https://arxiv.org/abs/2412.04234
35. Kozlov et al., *Neural Network Compression Framework for fast model inference* (NNCF), 2020. https://arxiv.org/abs/2002.08679
46. Minderer et al., *Simple Open-Vocabulary Object Detection with Vision Transformers* (OWL-ViT), 2022. https://arxiv.org/abs/2205.06230
47. Minderer et al., *Scaling Open-Vocabulary Object Detection* (OWLv2), 2023. https://arxiv.org/abs/2306.09683
48. Liu et al., *Grounding DINO*, 2023. https://arxiv.org/abs/2303.05499
49. Cheng et al., *YOLO-World*, CVPR 2024. https://arxiv.org/abs/2401.17270
50. Vasu et al., *MobileCLIP*, CVPR 2024. https://arxiv.org/abs/2311.17049
51. Faghri et al., *MobileCLIP2*, 2025. https://arxiv.org/abs/2508.20691
— Zhao et al., *DETRs Beat YOLOs on Real-time Object Detection* (RT-DETR), 2023. https://arxiv.org/abs/2304.08069

Open-set / OOD
39. Sun et al., *Out-of-Distribution Detection with Deep Nearest Neighbors*, ICML 2022. https://arxiv.org/abs/2204.06507
40. Ming et al., *Delving into Out-of-Distribution Detection with Vision-Language Representations* (MCM), NeurIPS 2022. https://arxiv.org/abs/2211.13445
41. Joseph et al., *Towards Open World Object Detection* (ORE), CVPR 2021. https://arxiv.org/abs/2103.02603
42. Gupta et al., *OW-DETR*, CVPR 2022. https://arxiv.org/abs/2112.01513
43. Zohar et al., *PROB*, CVPR 2023. https://arxiv.org/abs/2212.01424
52. Lee et al., *A Simple Unified Framework for Detecting Out-of-Distribution Samples* (Mahalanobis), NeurIPS 2018. https://arxiv.org/abs/1807.03888
53. Liu et al., *Energy-based Out-of-distribution Detection*, NeurIPS 2020. https://arxiv.org/abs/2010.03759
54. Wang et al., *ViM: Out-Of-Distribution with Virtual-logit Matching*, CVPR 2022. https://arxiv.org/abs/2203.10807

Human factors
55. U.S. Navy *Lookout Training Handbook* (NAVEDTRA 12968 series) and Army SPOTREP/SALUTE format (ATP 3-21.8 / FM 3-21.8) — [recalled; not re-read this session]. Johnson, J., *Analysis of Image Forming Systems*, 1958 (Johnson criteria) — [recalled].
