# Annotation round 2: `ghost_fleet_r2_1fps` (dev only)

All numbers below were measured by running the commands and scripts named here on the data named here. Estimates are labelled as estimates. Synthetic/unclassified hackathon data. Nothing here is a real-world operational accuracy claim.

## TL;DR
- **Added:** 23 MOT sequences / **1,130 frames** from **14 previously unlabelled dev source clips**, with **112 boxes / 89 tracks** after QA.
- **Data location:** GCS `edgeml/eval/ghost_fleet_r2_1fps/`. The set is registered in the catalog as `edgeml_ghost_fleet_r2_1fps` (1,130 image rows, 112 annotation rows).
- **Teacher:** Gemini 2.5 Flash for boxes, 15 classes, attributes and visibility, followed by a Gemini crop-verifier pass. The v1 single-pass Gemini output was unusable on bridge-cam and timelapse clips. **1,352 raw boxes became 114 after filtering and verification** (see §3).
- **QA:** stratified visual spot-check of **129 frames** (11.4% of the set). **124 pass / 5 fail = 96.1% frame pass rate.** On the 13 sampled frames that contained boxes, 13 of 15 boxes were correct and 2 were false positives (both dropped). There were 3 false-negative frames; these are unfixed and documented.
- **label_source:** every sequence stays `teacher`, because only about 11% of each sequence's frames were checked by a human. The 13 boxes in reviewed frames are marked `teacher+human` per box, in attrs and in the catalog.
- **Splits:** dev only. Every segment of every locked-test source was excluded. `splits.yaml` gains a `dev: ghost_fleet_r2_1fps` list, and `test:` is untouched.
- **Biggest caveat:** the teacher's recall is low. The verifier over-rejects distant contacts (Sunrise went from 40 to 9 boxes; Cruise_Ship_POV from 7 to 0), and moored-boat clutter in harbours is unlabelled. Treat this set as **precision-oriented dev data plus hard negatives**: storm, fog, night, own-ship clutter. It is not exhaustive ground truth.

## 1. Sources (step 1 inventory)
Sources are the GCS `frames/<Name>_frames/sec_NNNN.jpg` sets: 1 fps, native resolution, from the ghost-fleet-dataset-ingest pipeline. I picked those that are not in `splits.yaml` `test:` and were not already labelled in `ghost_fleet_5fps`, `fvessel_clip10` or SMD.

- **Excluded as locked test sources (all segments):** Spotted_A_Submarine, US_Destroyer_POV_Warship_Crossing, Ship_in_the_Distance_Ian, Dolphin_from_far_distance_in_Bay_of_Bengal, Sailboat_on_the_horizon, Arriving_In_Norfolk_USA_3hr_uncut, Cruise_Ship_Docked_in_Ocean_with_Dingy_Boats, Ferry_Passing_By_On_River.
- **`frames/*.zip` in the repo:** these are the SMD zips plus already-labelled clips. Nothing new to label.
- **Short clips:** taken whole, one sequence each.
- **Long clips** (30_Days_at_Sea_Timelapse 644 s, Cargo_Ship_Storm_first_hour 3,600 s, Cargo_Ship_Uncut_View 9,000 s): 4 windows of 60 contiguous frames at evenly spaced offsets. A window is swapped for the darkest 60-frame stretch when no window already contains 20 or more frames with luma < 60. This swap happened once: Cargo_Ship_Uncut_View, start sec 4515. The rule is in `build_r2_sequences.py`, and `edgeml/eval_qa/ghost_fleet_r2_1fps/seq_manifest.json` records the window starts.

| Source | Seqs | Frames | Frames w/ boxes | Boxes | Tracks |
|---|---:|---:|---:|---:|---:|
| 30_Days_at_Sea_Timelapse | 4 | 240 | 21 | 28 | 26 |
| Argentina_to_Alaska_Cruise_Bridge_Timelapse | 1 | 139 | 20 | 32 | 14 |
| Cargo_Ship_Storm_first_hour | 4 | 240 | 4 | 4 | 4 |
| Cargo_Ship_Uncut_View | 4 | 240 | 0 | 0 | 0 |
| Cruise_Ship_POV | 1 | 14 | 0 | 0 | 0 |
| Dolphins_Pungo_Ferry_Bridge | 1 | 5 | 3 | 5 | 4 |
| NCL_Breakaway_Bomb_Cyclone | 1 | 37 | 0 | 0 | 0 |
| Pacific_Ocean_at_night_from_Cruise_Balcony | 1 | 15 | 0 | 0 | 0 |
| Partially_Obstructed_Balcony | 1 | 17 | 0 | 0 | 0 |
| Sailing_drone_inside_a_hurricane | 1 | 49 | 0 | 0 | 0 |
| Ship_crossing_Mariana_Trench | 1 | 13 | 0 | 0 | 0 |
| Sunrise_Somewhere_in_the_Ocean | 1 | 40 | 9 | 9 | 9 |
| Tarpon_Offshore | 1 | 40 | 17 | 22 | 22 |
| Whale_Watching_in_Northern_Peru | 1 | 41 | 12 | 12 | 10 |
| **Total** | **23** | **1,130** | **86** | **112** | **89** |

Source: `~/r2/stats.json`, computed from the final `gt/gt.txt` after `qa_apply.py`. Tracks come from a greedy class-agnostic IoU linker at 1 fps (IoU ≥ 0.2, gap ≤ 2 frames). Track identities are teacher-only and weak on timelapse clips, so use this set for detection, not identity, metrics.

## 2. Labels
- **Classes** (ids 1-15 in `gt/labels.json`): cargo, tanker, container, fishing, sailboat, small_boat, warship, submarine, ferry_passenger, tug, buoy, aircraft, person, marine_animal, unknown. `marine_animal` was added to the requested list because whale and dolphin clips are in scope. The prompt tells the teacher to prefer `unknown` over guessing a subtype.
- **Box:** pixel `[x, y, w, h]` in `gt.txt`, per CONTRACTS. The visibility column holds 1.0 for full, 0.5 for partial and 0.25 for heavy.
- **Attributes:** `gt/attrs.jsonl` holds one row per box: label, visibility, nav_lights_visible, lights[], approaching, wake, confidence, verifier_box_ok/reason, label_v1, label_source. `gt/frame_attrs.jsonl` holds per-frame fields: night_gemini, mean_luma, scene, qa_reviewed, qa_verdict.

**Class histogram (final, 112 boxes):** marine_animal 33, unknown 24, small_boat 16, ferry_passenger 13, cargo 10, buoy 5, fishing 4, aircraft 2, container 2, tug 2, warship 1. Tanker, sailboat, submarine and person: 0.

**Visibility:** full 74, heavy 21, partial 17.

**Attributes:**

| Attribute | Count |
|---|---|
| nav_lights_visible | 9 boxes |
| Lights by type | other 6, all_round_red 2, all_round_white 1, masthead_white 1, sidelight_red 1, sidelight_green 1 |
| Wake | 23 boxes |
| Approaching | true 0, false 1, null 111 |

A single frame rarely settles heading, so `approaching` is essentially unlabelled.

**Night / small contacts:**
- 102 frames are night according to Gemini, 80 have mean luma < 60, and 78 meet both criteria.
- 45 boxes have a short side under 32 px; 16 of them are under 16 px.

## 3. Teacher and why v1 was replaced
Scripts: `edgeml/scripts/autolabel_gemini.py`, run on frames built by `build_r2_sequences.py`.
- **Model:** `gemini-2.5-flash` on Vertex (global endpoint), temperature 0, thinkingBudget 0, JSON responseSchema, image passed as a gs:// fileData part.
- **Output:** the script writes the same layout as `autolabel_tracks.py`: `teacher/dets.jsonl`, `teacher/gt_teacher.txt`, `gt/gt.txt` and `gt/labels.json`. That means `qa_apply.py` works unchanged on this set.
- **Provenance:** `labels.json.method` records the sha256 of the detector and verifier prompts.

**v1 (single pass):** 1,352 raw boxes. The v1 contact sheets (`v1_failure_modes.png`) showed three systematic failures:
- runaway rows of identical tiny boxes along the horizon or star field (up to 42 per frame);
- boxes on the camera ship's own deck containers and on harbour quays;
- near-full-frame `unknown` boxes on black night frames.

None of these were fixable by hand at this scale.

**v2 (default now):** stages run in this order. All thresholds are in the script and in `labels.json`.

| Stage | Rule | Boxes left |
|---|---|---:|
| v1 raw | — | 1,352 |
| F2, frame runaway | A frame with ≥ 25 objects loses all its boxes | 412 |
| F1, repetition cluster | A cluster of ≥ 6 boxes with near-equal height and y-centre is dropped | 297 |
| V, Gemini crop verifier | Full frame plus a ×1.5 crop with the box drawn, verdict real_object or reject, label re-chosen; 0 verifier errors | 114 |
| QA drops | Two false positives removed (§4) | 112 |

**OWLv2 cross-check:** `autolabel_owlv2.py` runs OWLv2-B/16 on CPU at stride 3. At the time of writing it had finished 4 of 23 sequences, so **no agreement numbers are reported**. Its outputs (`teacher/owlv2_dets.jsonl`) sit next to each sequence that has finished. RT-DETRv2 was not run because there is no GPU on the VM.

## 4. QA
- **Tool:** `edgeml/tools/contact_sheets.py labels` (new subcommand). It draws a stratified sample, with strata of source × night (Gemini night or luma < 60) × has-boxes. The sample is 10% per stratum, at least 2 per stratum, capped at 130, seed 0.
- **Sample:** **129 frames**. The sample was drawn before v2 was built and re-used unchanged on v2, so it was not re-drawn after seeing results.
- **Review:** I reviewed v2 sheets 1-15 visually. A frame passes when every box is a real object with an acceptable class and box, and no clearly visible object is missed.
- **Verdicts:** `edgeml/eval_qa/ghost_fleet_r2_1fps/qa_verdicts.csv`. Corrections are in `edgeml/eval_qa/ghost_fleet_r2_1fps/<seq>.yaml` and were applied with `qa_apply.py`, which gained per-box `drop_boxes / relabel_boxes / fix_boxes / add_boxes` and attrs rewriting. Existing sets are byte-identical after the change.

| QA measure | Value |
|---|---|
| Frames sampled | 129 |
| Pass / fail | 124 / 5 (**96.1%**) |
| Night-stratum frames | 14, pass 14 |
| Sampled frames with ≥ 1 box | 13 |
| Boxes in those frames | 15: 13 correct, 2 false positives |
| FP fixed (drop_boxes) | 30_Days_w1 f24 (container box over quay), 30_Days_w196 f3 (cargo box over canal) |
| FN, not fixed | Argentina f18 (distant vessel ahead), Argentina f47 (moored boats in Valparaiso harbour), Sunrise f27 (distant contact the verifier rejected; v1 box was 69 px from the track, beyond the 40 px rule, so not restored) |

**How to read the 96.1%:** most sampled frames are empty (open sea, storm, fog, night), so the frame pass rate is dominated by correct negatives. The box-level precision on the reviewed sample is 13 of 15. Recall was not measured exhaustively. My estimate, not a measurement, is that it is materially below precision: the funnel counts in §3 and the 3 FN frames found point that way.

## 5. Where data lives
- **GCS:** `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/eval/ghost_fleet_r2_1fps/` (1,448 objects, checked after `gcloud storage rsync`). It holds, per sequence, `seqinfo.ini`, `img1/`, `gt/{gt.txt,labels.json,attrs.jsonl,frame_attrs.jsonl}`, `teacher/`, `frame_map.json` (MOT frame → source `sec_NNNN.jpg`) and `qa/`, plus `qa_round2/` sheets and `index.jsonl`.
- **Catalog:** `edgeml/scripts/register_r2_catalog.py` mirrors `datasets/ingest/common.py` from the catalog branch and upserts only its own row. It writes:
  - `catalog/images/edgeml_ghost_fleet_r2_1fps.jsonl`
  - `catalog/annotations/edgeml_ghost_fleet_r2_1fps.jsonl`
  - one `datasets.jsonl` row
- **BigQuery checks:**

| Query | Result |
|---|---|
| `SELECT COUNT(*) FROM ghost_fleet.images WHERE dataset_id='edgeml_ghost_fleet_r2_1fps'` | 1,130 |
| Same filter on `ghost_fleet.annotations`, `GROUP BY source` | `model:gemini-2.5-flash` 99, `model:gemini-2.5-flash+human` 13 |
| `SELECT COUNT(*) FROM ghost_fleet.datasets` | 45 before, 46 after |

- **Git** (no images): scripts, `splits.yaml` dev entries, `edgeml/eval_qa/ghost_fleet_r2_1fps/` (corrections, `qa_verdicts.csv`, `index.jsonl`, `seq_manifest.json`) and tests.

## 6. What's left
1. **Recall pass:** fully review the short, box-rich sequences (Sunrise, Tarpon, Whale, Dolphins: 126 frames) and add the missed boxes, then mark those sequences `reviewed: true` so they become `teacher+human`. The Argentina harbour frames need manual boxes for moored boats.
2. **Verifier calibration:** the crop verifier is too strict on distant contacts. Track-level voting (accept a track if ≥ 1/3 of its boxes are verified) is the obvious next step. It needs a fresh QA sample.
3. **OWLv2 agreement:** finish the CPU run (or run it on a GPU VM) and report Gemini↔OWLv2 agreement. RT-DETRv2 was not run.
4. **Empty sequences:** 11 sequences have no boxes. `qa_apply.py` validation flags them `gt.txt empty or missing`. They are useful as hard negatives, but evaluators must accept empty `gt.txt`, or those sequences have to be filtered out.
5. **Under-covered classes:** tanker, sailboat, submarine and person have 0 boxes, and container, warship and tug have 1-2. Better sources for these are the remaining long GCS clips, which still need dev/test assignment.
6. **Weak attributes:** `approaching` is not inferable per frame and needs track-level motion.
