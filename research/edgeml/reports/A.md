# Workstream A — identity-labelled evaluation sequences

Everything below is what was *verified by reading files* unless marked **inferred**.
Data: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/{fvessel_clip10,ghost_fleet_5fps}/` (MOTChallenge layout, CONTRACTS §3);
raw upstream: `gs://.../raw/fvessel/Clip-10/`. Loader: `edgeml/src/edgeml/data/mot.py` (`pytest edgeml/tests/test_mot.py`).

## 1. Public maritime datasets surveyed (identity GT?)

| Dataset | Identity across frames? | Viewpoint | Size | Licence | Decision |
|---|---|---|---|---|---|
| **FVessel** (HF `gy65896/FVessel`, Clip-10) | **Yes** — `tracking GT` per clip: `second,id,x1,y1,x2,y2`, IDs persistent (checked in files; AIS-fused MMSI-derived) | fixed shore / riverside camera, Yangtze | 10 clips, 2.5 GB upstream; we extract 1 Hz (GT is per second) → 1,565 frames | research use (CC BY-NC-SA on HF) | **chosen** (`fvessel_clip10`, `label_source=public_gt`) |
| Singapore Maritime Dataset (SMD) | VIS onboard sample in GCS: `object_id` is per-category, not per-track. Original `.mat` GT: conversion scripts expose bbox/object-type/motion/distance; a persistent track-id field was **not** verified from the files I could reach (archive not independently inspected to completion) | onboard + onshore, RGB + NIR | 81 videos | custom (research) | **deferred** — the locked `smd_vis_onboard_sample` test videos (MVI_0797/0801) keep their place as a held-out set; identity ingest is follow-up |
| MODS | **No** — `obstacles[].id` restarts at 0 every frame (checked in `mods.json`) | USV, small obstacles | 94 seqs, 8,175 frames, 4.4 GB | CC BY-NC-SA | rejected for identity (fine for detection) |
| LaRS | panoptic key frames + temporal context, no MOT identities verified | USV | 4k key frames | CC BY-NC-SA | rejected for identity |
| MVTD | single-object (GOT-10k/VOT style), one target per sequence — not MOT | mixed | 182 seqs | CC0 | noted; not MOT, could serve SOT ablations |
| SeaDronesSee MOT | yes (22 clips, 54k frames) but test labels withheld, accessible share link unavailable; UAV viewpoint | aerial | large | custom | rejected (viewpoint + access) |
| ABOships | detection only, per-size/occlusion tags, no tracks | onboard | 9.9k imgs | CC BY | not applicable |

## 2. Sequences delivered

### 2.1 `fvessel_clip10` (public_gt, 1 fps, 2560×1440, shore-fixed, platform_motion=static, hfov≈56°)
Split: dev = clips 01–06,08,10; test = `fvessel_clip07`, `fvessel_clip09`.
**Weak hold-out warning:** all 10 clips are the same fixed camera, same day; the test clips share source statistics with dev. Treat them as a same-source sanity hold-out only. The *real* held-out identity sets are the locked `ghost_fleet_5fps` test clips (§2.2) and SMD `MVI_0797/0801`.

| seq | split | frames | tracks | boxes | median box area px² (p10–p90) |
|---|---|---|---|---|---|
| fvessel_clip01 | dev | 111 | 2 | 218 | 10636 (5399–35275) |
| fvessel_clip02 | dev | 96 | 2 | 188 | 25696 (18074–72969) |
| fvessel_clip03 | dev | 222 | 2 | 338 | 9800 (6751–48122) |
| fvessel_clip04 | dev | 162 | 3 | 456 | 22222 (3379–40581) |
| fvessel_clip05 | dev | 185 | 3 | 469 | 10608 (4628–37118) |
| fvessel_clip06 | dev | 158 | 3 | 357 | 27966 (21458–40586) |
| fvessel_clip07 | test | 670 | 4 | 1018 | 11775 (4059–28373) |
| fvessel_clip08 | dev | 307 | 2 | 531 | 15134 (5928–33803) |
| fvessel_clip09 | test | 519 | 3 | 736 | 6372 (3536–20406) |
| fvessel_clip10 | dev | 166 | 4 | 450 | 15159 (3820–38556) |


### 2.2 `ghost_fleet_5fps` (own clips, 5 fps, teacher+human / teacher)
| seq | split | viewpoint / motion / sea | frames | tracks | boxes | classes | median box px² | label_source | gt_status |
|---|---|---|---|---|---|---|---|---|---|
| A_ship_in_the_distance | dev | shore_handheld / handheld_static / calm | 46 | 1 | 46 | ship | 5749 | teacher+human | complete |
| Canadian_Coast_Guard_Helicopter_Landing | dev | aerial_helicopter / aircraft_approach / calm | 151 | 2 | 98 | person, ship | 208534 | teacher+human | partial |
| Chase_N_Fish_Sharky | dev | onboard_small_boat / small_boat_moderate / slight_chop_glare | 75 | 0 | 0 | — | 0 | teacher+human | incomplete |
| Cruise_ship_Mediterranean_sunset_off_Italy | dev | onboard_cruise_ship / large_ship_slow / moderate_swell | 153 | 3 | 62 | ship | 9800 | teacher+human | complete |
| Dolphin_from_far_distance_in_Bay_of_Bengal | test | onboard_ship_downlook / large_ship_underway / calm | 298 | 0 | 0 | — | 0 | teacher | incomplete |
| Far_away_but_did_you_see_that_splash | dev | onboard_small_boat / small_boat_slow / calm_harbour | 114 | 0 | 0 | — | 0 | teacher+human | incomplete |
| Sailing_POV_Corfu_Paxos | dev | onboard_sailboat / sailboat_underway / calm | 300 | 0 | 0 | — | 0 | teacher+human | complete |
| Ship_in_the_Distance_Ian | test | onboard_ship / large_ship_slow / moderate_whitecaps | 158 | 1 | 158 | ship | 18474 | teacher+human | complete |
| Ship_in_the_Distance_Utopia_of_the_Seas | dev | shore_handheld / handheld_zoom / calm | 75 | 2 | 49 | boat, ship | 6564 | teacher+human | partial |
| Spotted_A_Submarine | test | onboard_small_boat / small_boat_underway / calm | 745 | 1 | 707 | submarine | 417780 | teacher+human | complete |
| Submarine_surfacing | dev | onboard_small_boat / small_boat_moderate / calm_swell | 372 | 2 | 157 | boat, submarine | 33916 | teacher+human | partial |
| Two_Dolphins_near_the_coast_of_Gibraltar | dev | onboard_ship_bow_downlook / large_ship_underway / calm | 63 | 3 | 14 | dolphin | 1141 | teacher+human | partial |

`gt_status`: `complete` = every visible object boxed in every frame I checked; `partial` = known unlabelled stretches (listed in §5, use frame ranges from the notes when scoring FPs); `incomplete` = objects present, no GT (exclude). All 12 at 5 fps; resolutions 1080×1920 (vertical phone) or 1920×1080; `hfov_deg` not recorded (consumer phones, unknown). Frames 1–N are the full clip; nothing filtered by box size.


## 3. Labelling method (own clips)
Semi-automatic, no labelling from scratch, no fine-tuning (`edgeml/scripts/autolabel_tracks.py`, run on `edgeml-a-l4`, g2-standard-8 / 1×L4):

1. **Teacher detector**: Grounding DINO base (`IDEA-Research/grounding-dino-base`, HF transformers) with the open-vocabulary prompt
   `ship . boat . sailboat . submarine . buoy . dolphin . whale . shark fin . bird . helicopter . airplane . person . jet ski . kayak`,
   `box_thr=0.20`, `text_thr=0.15` (lowered from 0.25/0.20 after run 1 missed every far object), longest-prompt class assignment,
   then **class-agnostic NMS (IoU 0.6)** — without it "ship" and "boat" fire on the same hull and the tracker emits two tracks per vessel (run 3 → run 4 below).
2. **Teacher tracker**: BoT-SORT from BoxMOT with OSNet ReID and GMC camera-motion compensation (needed on hand-held / onboard video),
   `track_high_thresh=0.22, track_low_thresh=0.08, new_track_thresh=0.25, track_buffer=50`. Tracks shorter than 5 hits are pruned; gaps ≤ 2 s are
   linearly interpolated (interpolated boxes are still evaluated: `conf=1`, flagged `visibility=0.5`).
3. **Contact sheets** per track (`qa/track_NNN.jpg`) and a trajectory overview (`qa/overview.jpg`) for human review.
4. **Human QA** (me, not Gemini — Gemini spend was 0 images): per-track sheets + full-frame coordinate grids for the sequences where the teacher
   found nothing; corrections are declared in `qa/corrections.yaml` (drop / merge / relabel / trim / add_tracks with keyframes) and applied by
   `edgeml/scripts/qa_apply.py`, which keeps the untouched teacher output in `teacher/gt_teacher.txt`, rewrites `gt/gt.txt`, sets
   `label_source=teacher+human` **only** for sequences with `reviewed: true`, and rebuilds `index.jsonl` with teacher-vs-final counts.
5. **Vertical/thumbnail frames**: frames are the native 5 fps extraction (ffmpeg `fps=5`, `-q:v 2`); nothing resized, so tiny boxes stay tiny.

## 4. QA findings and label noise
**Label-noise summary for C** (teacher = run 4, Grounding DINO + NMS + BoT-SORT; final = after my QA):
teacher emitted **28 tracks / 2451 boxes**; QA **dropped 3 tracks** (false positives: emoji overlay, own-boat bow, duplicate hull box),
**merged 16 tracks into others** (fragments / concurrent duplicates of the same object), and **added 6 tracks** the teacher missed
(cruise ship ×3 passes, dolphins ×3), giving **15 tracks / 1291 boxes**. Of the teacher's 28 tracks only 9 survived as-is; i.e. a
per-frame detector + tracker produced 1.9× the true number of object-passes on these clips before dedup — the problem WS-C measures.
Earlier runs were worse: run 3 (no class-agnostic NMS) emitted 136 tracks (Spotted_A_Submarine alone: 99 → 14 → 1).

| seq | teacher tracks → final | teacher boxes → final | dropped | merged | added | what QA found |
|---|---|---|---|---|---|---|
| A_ship_in_the_distance | 1 → 1 | 46 → 46 | 0 | 0 | 0 | Track 1 (ship, 46/46 frames) verified on contact sheet: tight boxes on the ferry-like vessel on the horizon, no ID switch. A second, smaller vessel appeared to be on the horizon in the overview thumbnail and was not labelled by the teacher; not confirmed by hand -> possible missed tiny object. |
| Canadian_Coast_Guard_Helicopter_Landing | 2 → 2 | 98 → 98 | 0 | 0 | 0 | Track 1 (CCGS vessel, frames 1-92) verified, no switch. The vessel stays in frame to the end (helicopter lands on it) but the teacher loses it from frame 93 when it fills most of the frame -> frames 93-151 have no ship box (known miss, not fixed: object is larger than the frame). Track 2 = deck crew member (person) in the open door, real, kept. |
| Chase_N_Fish_Sharky | 1 → 0 | 16 → 0 | 1 | 0 | 0 | Track 1 ("whale") sits on a burned-in emoji overlay -> false positive, dropped. The real shark fins and the small boat in this vertical phone clip were not detected and were not hand-labelled (glare, 16-px fins). Sequence kept with zero GT for FP review only; exclude from headline metrics. |
| Cruise_ship_Mediterranean_sunset_off_Italy | 0 → 3 | 0 → 62 | 0 | 0 | 3 | Teacher found 0 tracks (ship ~140x70 px on horizon at sunset, vertical video, camera pans away twice). Human-added: one physical cruise ship, 3 continuous passes in frame (ids 1-3) located by template matching the frame-1 ship crop (NCC>=0.85; background scores ~0.64). Box size held constant (zoom changes small). ids 1,2,3 are the SAME physical vessel re-entering the frame; counted as separate passes per CONTRACTS sighting definition. |
| Dolphin_from_far_distance_in_Bay_of_Bengal | 0 → 0 | 0 → 0 | 0 | 0 | 0 | Teacher found 0 tracks. Human check (frame 150 grid): dolphins are mostly submerged; only whitewater splashes are visible at 5 fps, with brief surfacings elsewhere in the clip. Objects ARE present but could not be labelled reliably by eye within budget, so this sequence has no GT boxes and must be excluded from headline recall / duplicate metrics (keep for qualitative false-positive review only). It stays in the locked test split. |
| Far_away_but_did_you_see_that_splash | 1 → 0 | 54 → 0 | 1 | 0 | 0 | Track 1 ("boat", 54 frames) is the bow of the camera's own boat -> own-platform false positive, dropped. The distant dolphin splashes were not detected and not hand-labelled. Zero GT, FP-review only; exclude from headline metrics. |
| Sailing_POV_Corfu_Paxos | 0 → 0 | 0 → 0 | 0 | 0 | 0 | Teacher found 0 tracks. Human check of frames 1/150/300 (full-frame grids): coastline, sky, water and a production watermark only; no vessels or marine life visible. Kept as a zero-object sequence (false-positive pressure for the sighting pipeline). Not every frame was inspected. |
| Ship_in_the_Distance_Ian | 1 → 1 | 158 → 158 | 0 | 0 | 0 | Track 1 (bulk carrier, 158/158 frames) verified on contact sheet, tight boxes, no ID switch. Run 3 (no NMS) had a duplicate second track on the same hull; run 4 NMS removed it. |
| Ship_in_the_Distance_Utopia_of_the_Seas | 4 → 2 | 66 → 49 | 1 | 1 | 0 | 27 (frames 43-57) and 29 (59-75) are the same cruise ship before/after a zoom cut -> merged into 27 (one continuous pass). 30 ("boat", 229x53) is a duplicate box on the cruise ship's hull/waterline -> dropped. 31 = tug alongside, real, kept. Frames 1-42 (heavier zoom-out, ship ~40 px) have no boxes -> missed. |
| Spotted_A_Submarine | 14 → 1 | 1854 → 707 | 0 | 13 | 0 | One submarine, in frame continuously from ~f31 to f745 (f1-30 are a black title card). The teacher split it into 14 concurrent/sequential fragments: alternating "sail only" boxes (2, 9, 109, 112) and "full hull" boxes (13, 110, 113, 114, 128, 131), plus zoomed-in close-ups of the sail after video cuts (76, 105, 126, 130). All verified on contact sheets to be the same vessel -> merged into id 13 (one object, one continuous pass). Per frame the larger box is kept, so box extent still flips between hull and sail where only the sail track existed; treat IoU at alpha<=0.3 as the trustworthy regime here. YouTube end-card overlays (PREVIOUS/PLAYLIST/SUBSCRIBE) appear from ~f635; no boxes on them. |
| Submarine_surfacing | 4 → 2 | 159 → 157 | 0 | 2 | 0 | Two real objects. Tug/escort boat: id 1 (f173-187) and id 14 (f314-372, labelled "ship") are the same boat -> merged into 1 (boat). Submarine: id 8 (first surfacing, f290-296) and id 11 (f295-372, labelled "boat") are the same white submarine -> merged into 8, relabelled submarine. Nothing labelled before f173 (submerged / boat out of frame); not hand-checked. |
| Two_Dolphins_near_the_coast_of_Gibraltar | 0 → 3 | 0 → 14 | 0 | 0 | 3 | Teacher found 0 tracks (dolphins are ~20x40 px dark shapes, airborne for <1 s). Human-added from a dark-blob finder (local background subtraction, rows 250-900) verified on frame crops 7 and 32: one dolphin leaping at right (frames 6-11), two dolphins leaping together (frames 29-32). Leaps between those frames were not found by the finder and were not hand-checked frame by frame, so additional brief surfacings may be unlabelled (treat extra detections in frames 12-28, 33-63 with caution). Boxes are blob extents +6 px. |

Contact sheets used for review (downscaled): `A_assets/Spotted_A_Submarine_0.jpg` (fragmentation), `A_assets/Submarine_surfacing_0.jpg`,
`A_assets/Ship_in_the_Distance_Utopia_of_the_Seas_0.jpg` (duplicate hull box), `A_assets/Far_away_but_did_you_see_that_splash_0.jpg` (own-bow FP),
`A_assets/cruise_f1_crop.jpg` and `A_assets/gibraltar_f32_crop.jpg` (grids used for hand keyframes). Full-size per-track sheets: `gs://.../edgeml/eval/ghost_fleet_5fps/<seq>/qa/track_NNN.jpg`.


## 5. Known weaknesses
- **Teacher recall on tiny/far objects is poor**: Grounding DINO at box_thr 0.2 found nothing in 4/12 own clips (cruise ship 140×70 px at sunset, 20×40 px dolphins, 16 px shark fins). Those GT boxes are human-added with tool help (template matching / dark-blob finder) and have ±5–10 px box error; evaluate tiny-box recall at IoU α≤0.3 (literature §3.5), not 0.5.
- **Box extent inconsistency on the submarine** (`Spotted_A_Submarine`): alternates between "sail only" and "full hull" where the teacher tracked them as separate fragments; after merge the larger box is kept per frame, but hull-only frames remain (~⅓ of the pass). Expect IoU-sensitive metrics to under-report on this sequence.
- **Incomplete sequences** (`gt_status=incomplete`, 3 clips: Sharky, Splash, Dolphin_Bay_of_Bengal): real objects present but no GT → exclude from recall/duplicate metrics; use for qualitative FP review only. `Dolphin_from_far_distance_in_Bay_of_Bengal` is a locked test clip and stays `label_source=teacher` (0 boxes) — WS-C should report test numbers on `Spotted_A_Submarine` + `Ship_in_the_Distance_Ian` only until it is hand-labelled.
- **Partial coverage** (`gt_status=partial`: Helicopter, Utopia, Submarine_surfacing, Two_Dolphins): CCGS vessel unlabelled from frame 93 (fills frame), Utopia cruise ship unlabelled in frames 1–42 (40 px), `Submarine_surfacing` frames <173 unchecked. Interpolated boxes (visibility 0.5) are 7 % of own-clip boxes.
- **Zero-object sequences** (`Sailing_POV_Corfu_Paxos`) spot-checked at 3 frames only.
- **Own-platform parts** (bow, deck, helicopter door) are *not* annotated as ignore regions; the pipeline must suppress them itself (one own-bow FP was removed from GT).
- **FVessel**: 1 Hz only (GT is per second), fixed shore camera, same site/day for all clips → weak hold-out (see §2.1); boxes are AIS-fused and occasionally lag the hull by a few px.
- **SMD identity**: not delivered; the locked SMD test videos remain boxes-only.
- Viewpoint / platform_motion / sea_state tags on own clips are my visual judgement, not sensor data.

## 6. Reproduce
```bash
# public set (needs ~2.5 GB raw download; keeps raw under gs://.../raw/fvessel/Clip-10/)
python edgeml/scripts/ingest_fvessel_mot.py --raw gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/raw/fvessel/Clip-10 \
    --out /tmp/eval/fvessel_clip10 && gcloud storage rsync -r /tmp/eval/fvessel_clip10 gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/fvessel_clip10
# own clips: 5 fps extraction (yt-dlp + ffmpeg fps=5) -> edgeml/eval/ghost_fleet_5fps/<seq>/img1 + seqinfo.ini, then on an L4:
python edgeml/scripts/autolabel_tracks.py --root ~/eval/ghost_fleet_5fps            # teacher: gt/, teacher/, qa/track_*.jpg
# human QA: edit <seq>/qa/corrections.yaml; to reproduce mine, copy the committed ones in first:
for f in edgeml/eval_qa/ghost_fleet_5fps/*.yaml; do s=$(basename $f .yaml); [ -d ~/eval/ghost_fleet_5fps/$s ] && mkdir -p ~/eval/ghost_fleet_5fps/$s/qa && cp $f ~/eval/ghost_fleet_5fps/$s/qa/corrections.yaml; done
python edgeml/scripts/qa_apply.py --root ~/eval/ghost_fleet_5fps --meta edgeml/eval_qa/ghost_fleet_5fps/clips_meta.yaml
gcloud storage rsync -r ~/eval/ghost_fleet_5fps gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_5fps
# loader / tests
pytest edgeml/tests/test_mot.py -q
python -c "from edgeml_research.data.mot import load_set; print([s.stats() for s in load_set('gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval','ghost_fleet_5fps')][:2])"
```
GPU: `edgeml-a-l4` (g2-standard-8, 1×L4, DLVM image), labels `team=edgeml,workstream=a`; 4 teacher runs ≈ 1 GPU-hour; **deleted** after the run. Gemini: 0 images.
Data added to the bucket: raw FVessel 2.5 GB + eval sets ≈ 1.6 GB (within the 20 GB cap). No `edgeml/runs/` entries: this workstream produces GT, not model runs.

## 7. WS-A2: six more identity-labelled TEST sequences (test only)

Goal: thicken the held-out `ghost_fleet_5fps` test set (only Spotted_A_Submarine and Ship_in_the_Distance_Ian were complete).
Six new sequences were added to `test` in `edgeml/configs/splits.yaml` (nothing moved between dev and test; the 12 existing
`gt.txt`/`labels.json` are byte-identical before and after the re-run of `qa_apply.py`). **4 are `gt_status=complete`, 2 are
`partial`** (declared honestly below; use the partial ones for FP review / qualitative sightings, not headline recall).

Candidate search: the catalog's own clips (`sources/*.json`) had only 2 unseen clips that fit the brief (visible vessels, onboard
view, <=3 min): `US_Destroyer_POV_Warship_Crossing` (30 s, already a `ghost_fleet_clips` test clip, so test-safe) and the 71-min
`Sailboat_on_the_horizon` (first 3 min used). `Dolphin_from_far_distance_in_Bay_of_Bengal` stays incomplete (§4: submerged animals, splashes only). The rest are **new test sources** (never used in dev): two 3-min segments of the CC-BY
`Arriving In Norfolk` bridge-cam clip (1:10:00 and 1:13:40, chosen because an outbound ship, a tug+barge and a buoy pass close),
and two CC-BY YouTube clips ingested with the `ghost-fleet-dataset-ingest` skill (`sources/Cruise_Ship_Docked_in_Ocean_with_Dingy_Boats.json`,
`sources/Ferry_Passing_By_On_River.json`, 1 fps frame sets + zips uploaded; the BigQuery catalog refresh `datasets/ingest/video_frames.py`
was NOT re-run, it re-downloads every frame set — run it once from the catalog branch).

Pipeline identical to §3: ffmpeg `fps=5 -q:v 2` at native resolution, `autolabel_tracks.py` (Grounding DINO base + class-agnostic
NMS + BoT-SORT, unchanged) on one GPU VM, per-track contact-sheet review + full-frame crops where needed, `qa_apply.py`. One
deviation: every L4 shape (`g2-standard-4/8`) was `STOCKOUT` in all four `us-central1` zones, so the teacher ran on `edgeml-a2-t4`
(n1-standard-8 + 1x T4, labels team=edgeml,workstream=a2; deleted after the run). Teacher output is unchanged by the GPU type.
Gemini was not used (0 images).

| seq | frames, px | view / motion | teacher tracks → final | teacher boxes → final | dropped | merged | added | gt_status | what QA found |
|---|---|---|---|---|---|---|---|---|---|
| US_Destroyer_POV_Warship_Crossing | 153, 854×472 | bridge wing of USS Chung-Hoon, large ship underway | 5 → 1 | 449 → 153 | 4 | 0 | 0 | complete | Track 1 = PLA Navy destroyer crossing the bow, in frame all 153 frames, tight boxes, no switch (126×59 → 30×35 px as it opens range). 2, 3, 8, 9 = own bow / forward gun mount / deck edge → own-platform FPs dropped. |
| Sailboat_on_the_horizon_seg0 | 900, 1920×1080 | fixed mount on a boat, gentle roll | 5 → 2 | 870 → 866 | 0 | 3 | 1 (1-frame gap fill) | complete | One sailboat right→left f6-523 was split into 1 (f6-170), 3 (f39-64, fills a gap of 1), 6 (f172-515), 11 (hull only, leaving the left edge) → merged into 1. A sailboat enters from the right at f552 and stays to f900 (id 12); kept as a second identity (cannot be the same hull without a cut). ~80×112 px boxes, verified tile by tile. |
| Arriving_In_Norfolk_seg1h10 | 900, 1920×1080 | container-ship bow cam, slow ahead in Thimble Shoal | 13 → 6 | 3124 → 2635 | 6 | 1 | 1 (gap fill) | complete | Kept: red pilot boat (1), outbound MSC ship passing to port (2), low barge on the horizon (3), tiny white vessel dead ahead (4: fragments 4+6 merged and the 160-frame gap f125-285 filled by interpolation after checking crops, 12-22 px), tug+barge to port (10), second tiny vessel (11, low confidence). Dropped: 5, 9, 12 whole-frame boxes on own deck cargo; 13, 15, 16 = parts/duplicates of the tug+barge already tracked as 10. |
| Arriving_In_Norfolk_seg1h13 | 900, 1920×1080 | same cam, 3.5 min later | 10 → 4 | 2738 → 729 | 5 | 1 | 1 (gap fill) | **partial** | Kept: outbound ship to port (1, relabelled ship), red lateral buoy passing close to starboard (9+10 merged, relabelled buoy, 38-frame gap filled), distant vessel ahead only where the teacher boxed it (21: 7 fr, 22: 29 fr). Dropped 2, 3, 8, 19, 20 = own-deck boxes. Partial: 1-2 horizon specks <15 px (and the vessel of 21/22 for most of the clip) and a green buoy far to port are visible but not reliably boxable at 1080p. |
| Cruise_Ship_Docked_in_Ocean_with_Dingy_Boats | 676, 1920×1080 | onboard a cruise ship, tender operation, camera cuts | 38 → 29 | 2848 → 2225 | 8 | 1 | 0 | **partial** | Ship fragments 10 (f5-119) and 118 (f150-483) merged (same docked ship across a cut). 20 tender tracks verified as real boats with boxes on the tender; 62 trimmed to f37-96. Dropped 8 tracks (68, 121, 147, 153, 159, 164, 170, 173) whose boxes sit on the cruise ship's lifeboat/hull rows while the tender is below them — a systematic Grounding DINO "boat" failure mode worth a WS-C check. Partial: several tenders never boxed, tender identities fragmented across cuts, aerial scene f485+ has only one boat (180) + a 9-frame group (182). |
| Ferry_Passing_By_On_River | 209, 1920×1080 | fixed shore camera | 1 → 1 | 67 → 67 | 0 | 0 | 0 | complete | One passenger ferry left→right, track 3 f68-134, tight boxes (verified on f85/110/130 full frames); f1-67 and f135-209 empty. Two <10 px white specks (buoys/gulls) and a distant aircraft deliberately unlabelled. |

**Label-noise summary for C (A2 only):** teacher 72 tracks / 10 096 boxes → final 43 tracks / 6 675 boxes; dropped 23 tracks,
merged 6, added 3 gap fills. Dominant teacher failure modes on these clips: (i) **own-platform boxes** on the filming ship — whole
deck-cargo boxes in both Norfolk segments and bow/gun boxes on the destroyer (11 of the 23 drops); (ii) **boxes locking onto the
large ship's hull instead of the small boat beside it** (8 drops, Cruise); (iii) **fragmentation of one slow object into several
sequential tracks** (sailboat 4→1, Norfolk horizon vessel 2→1, buoy 2→1, cruise ship 2→1); (iv) **loss of 10-20 px objects** for tens
of seconds (filled by interpolation only where crops confirmed the object stayed put). Duplicate part-boxes of one tug+barge
(Norfolk 13/15/16 vs 10) are the same duplicate-identity problem WS-C measures.

Weaknesses specific to A2: both Norfolk segments share one camera/day (treat as one domain, not two independent samples); the
Norfolk source is a 4K60 timelapse-free clip fetched at 1080p, so horizon targets are blurrier than native; Cruise has camera
cuts inside the sequence (tracker resets are expected, not errors); the destroyer clip is 854×472 and the only non-1080p sequence.
Review evidence (downscaled): `A_assets/a2_destroyer_overview.jpg`, `A_assets/a2_norfolk1h10_tracks.jpg`, `A_assets/a2_cruise_hull_drift.jpg`
(hull-drift tracks 68/121/147), `A_assets/a2_norfolk_gap_crops.jpg` (gap-fill check). Full-size sheets and the untouched teacher output:
`gs://.../edgeml/eval/ghost_fleet_5fps/<seq>/{qa,teacher}/`; corrections mirrored in `edgeml/eval_qa/ghost_fleet_5fps/<seq>.yaml`.
