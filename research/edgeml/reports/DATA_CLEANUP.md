# Data cleanup — measured audit and non-destructive clean catalog v2

## Verdict

Original GCS objects, original BigQuery tables, and the six Git LFS archives were retained. The repaired catalog is published, but it is **not a blanket certification for evaluation or redistribution**. Singapore Maritime remains quarantined; licenses, near-duplicates and raw annotation joins need review.

## Published outputs

- GCS: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/clean/v1/data-quality-20261105-reviewed/` — 13593 objects, 2738419289 bytes (publication receipt).
- BigQuery: `qwiklabs-gcp-02-34a870014566.ghost_fleet.images_clean_v2` (49,717 rows), `annotations_clean_v2` (121,467), `datasets_clean_v2` (32).
- Reviewed publication receipt: `publication.json`; independent query result: `verification.json`, both in the main reviewed prefix.
- Independent `bq query` verification: **0 orphan annotations; 0 SHA-256 hashes spanning dev and test** in the clean image table.
- Supplemental raw audit: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/clean/v1/raw-audit-20261104/`.
- Original generation/checksum inventory and earlier publication receipts: `gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/clean/v1/inventory-20261104/`.
- Each prefix contains `audit.json`, `issues.jsonl`, `changes.jsonl`, `frames.jsonl`, `selection.jsonl` and the input split policy. Main prefix also contains corrected MOT GT, clean catalog JSONL and reconstructed SMD JPEGs.
- Non-SMD clean image URIs deliberately reference immutable originals; this is a clean catalog/selection overlay, not a second complete physical image corpus. Use `eval_split` and selection status, not upstream split alone.

## Inventory

Measured by `inventory_gcs.py` and `validate_data.py`. Byte sizes are exact, not estimates. Catalog rows below are **clean** counts; archive sizes are compressed. Counts overlap between copies/artifacts and must not be summed as unique images. Archive labels are not supplied in ZIPs (shown as —).

| Dataset/artifact | Bytes | Frames | Labels | License/provenance |
|---|---:|---:|---:|---|
| archive:30_Days_at_Sea_Timelapse_frames.zip | 78422846 | 644 | — | see source manifest; unknown is not permission |
| archive:Argentina_to_Alaska_Cruise_Bridge_Timelapse_frames.zip | 5498925 | 139 | — | see source manifest; unknown is not permission |
| archive:Cargo_Ship_Storm_first_hour_frames.zip | 53967050 | 3600 | — | see source manifest; unknown is not permission |
| archive:Cargo_Ship_Uncut_View_frames.zip | 276458645 | 9000 | — | see source manifest; unknown is not permission |
| archive:Dolphins_Pungo_Ferry_Bridge_frames.zip | 238577 | 5 | — | see source manifest; unknown is not permission |
| archive:SMD_VIS_Onboard_frames.zip | 14699611 | 80 | — | see source manifest; unknown is not permission |
| synthetic | 3511009 | 48 | 121 | synthetic; per-image licence/source absent |
| seaships7000 | 1242753744 | 6987 | 9207 | unknown |
| singapore_maritime | 2282514973 | 13410 | 112118 | unknown |
| smd_vis_onboard_sample | 14852487 | 80 | 142 | unknown (academic research use) |
| video_30_days_at_sea_timelapse | 78283528 | 644 | 0 | unknown |
| video_a_ship_in_the_distance | 1089675 | 9 | 0 | Standard YouTube license (no license stated); internal use only |
| video_argentina_to_alaska_cruise_bridge_timelapse | 5463367 | 139 | 0 | unknown |
| video_arriving_in_norfolk_usa_3hr_uncut | 2381480465 | 10795 | 0 | Standard YouTube license (no license stated); internal use only |
| video_canadian_coast_guard_helicopter_landing | 5327194 | 30 | 0 | Facebook reel by Canadian Coast Guard; no redistribution license stated; internal use only |
| video_cargo_ship_storm_first_hour | 53167630 | 3600 | 0 | unknown |
| video_cargo_ship_uncut_view | 274565233 | 8998 | 0 | unknown |
| video_chase_n_fish_sharky | 1205927 | 15 | 0 | Facebook video by Chase N Fish Charters; no redistribution license stated; internal use only |
| video_cruise_ship_mediterranean_sunset_off_italy | 7320769 | 31 | 0 | Standard YouTube license (no license stated); internal use only |
| video_cruise_ship_pov | 6503758 | 14 | 0 | Motion Array stock (frames taken from the free 1080p preview stream, not a licensed download; internal use only) |
| video_dolphin_from_far_distance_in_bay_of_bengal | 4343565 | 60 | 0 | Standard YouTube license (no license stated); internal use only |
| video_dolphins_pungo_ferry_bridge | 237247 | 5 | 0 | unknown |
| video_far_away_but_did_you_see_that_splash | 1317281 | 21 | 0 | Standard YouTube license (no license stated); internal use only |
| video_ncl_breakaway_bomb_cyclone | 1502895 | 37 | 0 | Standard YouTube license (no license stated); internal use only |
| video_pacific_ocean_at_night_from_cruise_balcony | 1015851 | 15 | 0 | Standard YouTube license (no license stated); internal use only |
| video_partially_obstructed_balcony | 2495437 | 17 | 0 | Standard YouTube license (no license stated); internal use only |
| video_sailboat_on_the_horizon | 215346994 | 4256 | 0 | Standard YouTube license (no license stated); internal use only |
| video_sailing_drone_inside_a_hurricane | 3734130 | 49 | 0 | Standard YouTube license (no license stated); internal use only |
| video_sailing_pov_corfu_paxos | 8042383 | 60 | 0 | Standard YouTube license (no license stated); internal use only |
| video_ship_crossing_mariana_trench | 1661515 | 13 | 0 | Standard YouTube license (no license stated); internal use only |
| video_ship_in_the_distance_ian | 3932012 | 32 | 0 | Standard YouTube license (no license stated); internal use only |
| video_ship_in_the_distance_utopia_of_the_seas | 1858301 | 15 | 0 | Standard YouTube license (no license stated); internal use only |
| video_spotted_a_submarine | 7223289 | 146 | 0 | Standard YouTube license (no license stated); internal use only |
| video_submarine_surfacing | 5960928 | 74 | 0 | Standard YouTube license (no license stated); internal use only |
| video_sunrise_somewhere_in_the_ocean | 7531175 | 40 | 0 | Standard YouTube license (no license stated); internal use only |
| video_tarpon_offshore | 10298220 | 40 | 0 | Facebook video by Chase N Fish Charters; no redistribution license stated; internal use only |
| video_two_dolphins_near_the_coast_of_gibraltar | 2943660 | 13 | 0 | Standard YouTube license (no license stated); internal use only |
| video_us_destroyer_pov_warship_crossing | 558644 | 31 | 0 | Reddit repost of a released military video; no license stated; internal use only |
| video_whale_watching_in_northern_peru | 1691699 | 41 | 0 | Standard YouTube license (no license stated); internal use only |
| raw:ir_maritime_vessels | 1362445634 | 37947 | 26445 | not inferred; retain upstream README/source provenance |
| raw:seaships7000 | 1247315486 | 7001 | 0 | not inferred; retain upstream README/source provenance |

### Whole-bucket inventory (before new clean prefixes)

116,753 objects; **18,239,792,582 bytes**. The generation/MD5/CRC32C listing is the reproducibility input, not an assertion that every compressed video/archive was decoded.

| Prefix | Objects | Bytes |
|---|---:|---:|
| catalog/ | 65 | 49050991 |
| datasets/ | 9065 | 1599364983 |
| edgeml/ | 17715 | 3559391924 |
| frames/ | 29427 | 3114431296 |
| raw/ | 60423 | 6856707928 |
| sources/ | 27 | 12105 |
| zips/ | 31 | 3060833355 |

### Repository metadata and coverage

The main run decoded 74,298 image occurrences; the supplemental run decoded 44,948 raw image occurrences (37,947 IR and 7,001 SeaShips). These runs overlap in SeaShips content. All six local ZIPs were read, plus the synthetic catalog, QA assets, scenarios, catalog-backed images, and evaluation sequences. Zero unreadable images were observed in these two scans. Raw videos/PDFs and GCS ZIP-only payloads were inventoried, not independently decoded. Derived run/evaluation visualization PNGs outside the selected mirror were not decoded.

All six ZIPs use Git LFS (`git lfs ls-files`, `.gitattributes`); this is not 410 MB of ordinary Git blobs. Their exact checksums, byte sizes, LFS pointers and matching GCS generations are recorded in `frames/manifest.json`. Five have exact size/MD5 GCS matches. Cargo Ship Uncut has no exact archive match; retain it. Future large assets should use versioned GCS objects and a checksum manifest, not additional repository copies. No history rewrite or archive removal was performed.

### MOT and QA inventory

The validator read 28 QA index sequences and 19 QA YAML files. MOT rows below are original GT counts before corrections; size is decoded image-file bytes, excluding text metadata. Licenses remain inherited from upstream sources, not newly granted.

| Sequence | Image bytes | Frames | GT rows | License |
|---|---:|---:|---:|---|
| edgeml/eval/fvessel_clip10/fvessel_clip01 | 44007777 | 111 | 218 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip02 | 47029747 | 96 | 188 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip03 | 108914204 | 222 | 338 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip04 | 51670997 | 162 | 456 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip05 | 71254189 | 185 | 469 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip06 | 73082647 | 158 | 357 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip07 | 261969368 | 670 | 1018 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip08 | 123227055 | 307 | 531 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip09 | 198995521 | 519 | 736 | upstream / unresolved |
| edgeml/eval/fvessel_clip10/fvessel_clip10 | 67179203 | 166 | 450 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/A_ship_in_the_distance | 7117182 | 46 | 46 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Arriving_In_Norfolk_seg1h10 | 269487600 | 900 | 2635 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Arriving_In_Norfolk_seg1h13 | 241923898 | 900 | 729 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Canadian_Coast_Guard_Helicopter_Landing | 34749654 | 151 | 98 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Chase_N_Fish_Sharky | 7786912 | 75 | 0 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Cruise_Ship_Docked_in_Ocean_with_Dingy_Boats | 95118390 | 676 | 2225 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Cruise_ship_Mediterranean_sunset_off_Italy | 45128583 | 153 | 62 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Dolphin_from_far_distance_in_Bay_of_Bengal | 27962908 | 298 | 0 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Far_away_but_did_you_see_that_splash | 9447688 | 114 | 0 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Ferry_Passing_By_On_River | 27056869 | 209 | 67 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Sailboat_on_the_horizon_seg0 | 58826235 | 900 | 866 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Sailing_POV_Corfu_Paxos | 51022043 | 300 | 0 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Ship_in_the_Distance_Ian | 26632713 | 158 | 158 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Ship_in_the_Distance_Utopia_of_the_Seas | 11886312 | 75 | 49 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Spotted_A_Submarine | 48130262 | 745 | 707 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Submarine_surfacing | 37975917 | 372 | 157 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/Two_Dolphins_near_the_coast_of_Gibraltar | 18135823 | 63 | 14 | upstream / unresolved |
| edgeml/eval/ghost_fleet_5fps/US_Destroyer_POV_Warship_Crossing | 3618629 | 153 | 153 | upstream / unresolved |

## Main issue counts

Counts come from `clean-v3/audit.json`. Reasons overlap; candidate counts are frames flagged, not exhaustive pair counts. Black frames are also counted as blank. An absent issue key means zero observed, not proof about unscanned inputs.

| Reason | Count |
|---|---:|
| black_frame | 25 |
| blank_frame | 25 |
| clip_bounds | 7791 |
| dev_exact_content_leak | 5 |
| exact_content_cross_split | 5 |
| exact_copy_across_artifacts | 15449 |
| exact_duplicate_locked_test | 35 |
| exact_duplicate_within_dataset | 200 |
| label_norm_mismatch | 121427 |
| missing_license | 6 |
| missing_sequence_source | 10 |
| missing_sequence_source_url | 10 |
| missing_synthetic_license | 48 |
| mot_clip_bounds | 410 |
| near_content_cross_split_candidate | 3689 |
| near_duplicate_candidate | 58631 |
| orphan_or_quarantined_annotation | 14 |
| qa_split_mismatch | 1 |
| scenario_input_not_local | 4 |
| scenario_mixed_case_keys | 3 |
| source_quarantined | 13595 |
| source_video_cross_upstream_splits | 36 |
| unknown_license | 8 |

## Repairs and safety decisions

- **Singapore mis-join:** raw image_id restarts per video. Original catalog had 1,981 images; source Parquet has 13,410 paths and 112,118 annotations. Rebuilt IDs use `singapore_maritime/<image.path>` and annotations follow that path. All 36 videos cross upstream train/validation/test, so all 13,410 rebuilt images have `eval_split=quarantine`.
- **Leakage:** all 185 dev FVessel clip05 frames exactly match held-out clip07. Clip05 is quarantined in policy and clean selection; locked test is not moved. Five SeaShips dev records matching test content are excluded from the clean catalog. Source-video aliases prevent segmented/1-fps/5-fps names from defaulting to conflicting splits.
- **Boxes:** 7,791 catalog box-bound corrections and 410 MOT bound corrections; invalid/orphan annotations are excluded with reasons. 14 orphan/quarantined annotations are absent from the clean annotation table. Structured horizon metadata is retained, not forced into a box.
- **Classes:** recomputed 121,427 inconsistent normalized labels using the repository vessel/buoy/object taxonomy; preserved native labels. Added observed boat/aircraft/drone/shark-fin synonyms, plus IR liner.
- **Duplicates:** exact within-dataset duplicates and blank/black images are quarantined through the selection manifest; locked-test duplicates are explicitly preserved. Exact copies across distinct artifacts are reported, not universally discarded. Near-duplicates use 63-bit DCT pHash, Hamming radius 4, and are **review only**. Blank means grayscale std <1; black means grayscale 99th percentile <=3.
- **Metadata:** corrected source/split metadata only where source identity is supported. Missing license/source/sequence metadata is logged; no license or camera calibration is invented. The clean loader honors explicit eval_split and rejects a mixed legacy/clean split schema.
- Main change log: **358,950** before/after records or selection decisions, with reasons. Published catalog substitutions also record original image URIs. Publication uses generation-match-zero uploads and WRITE_EMPTY new BigQuery tables; destination collisions fail closed.

## Supplemental raw audit / remaining issues

The supplemental run checks image readability, exact/pHash candidates, and Pascal VOC XML geometry. It is an audit, **not a normalized raw training dataset**. Raw upstream partition names are not treated as an approved dev/test source-video policy.

| Raw reason | Count |
|---|---:|
| exact_duplicate_within_dataset | 2196 |
| near_duplicate_candidate | 10518 |
| raw_label_review_required | 2 |
| xml_clip_bounds | 7 |
| xml_image_name_mismatch | 8402 |

All 8,402 IR XML files declare filenames different from their own stem. This may reflect upstream renaming; do not silently join on the embedded filename. Seven XML box-bound issues also need review. XML/CSV/YOLO labels remain unchanged and are not admitted into the clean BigQuery training catalog. CSV/YOLO label semantics and raw-video source lineage require a dedicated follow-up. Raw selection has 12374 logged selection decisions; these are not XML label repairs.

Remaining: resolve unknown/restricted licenses (including all 48 synthetic per-image records), missing source URLs and seqinfo fields; reconcile the one original QA split mismatch with quarantine; review pHash candidates; establish disjoint source-video splits for SMD/IR before evaluation; inspect raw CSV/YOLO annotation semantics; decode remaining GCS archives/videos if full media certification is required. Do not redistribute unknown-license assets. Do not use original mis-joined SMD tables for evaluation.

Superseded outputs: `clean/v1/data-quality-20261104-final/` and BigQuery `*_clean_v1` contain the pre-review duplicate-selection bug; do not use them. They are retained, not overwritten. The reviewed rerun restored 203 locked-test selection records and nine locked-test catalog images (and their nine annotations). All 670 clip07 frames now avoid quarantine. The validator also now drops/logs MOT references absent from actual frame filenames, including gaps and fractional IDs; no such input GT rows occurred in this measured rerun. An earlier interrupted clean-only publication is also not authoritative. Use only the published destinations above. Original `catalog/` and BigQuery `images/annotations/datasets` were not replaced.

The prototype is synthetic/unclassified. Public dataset READMEs (notably IR) describe captured imagery, so not every input pixel is synthetic. These data checks make no operational accuracy claim.

## Reproduction and verification

Run from repository root with a fresh output directory each time. Cloud authentication is required; no credentials are embedded.

```sh
python -m pip install --upgrade pip setuptools
python -m pip install -e 'edgeml[test,audit]'
python edgeml/tools/inventory_gcs.py --project qwiklabs-gcp-02-34a870014566 --bucket qwiklabs-gcp-02-34a870014566-ghost-seeker --output objects.jsonl
# Save a generation/checksum listing and mirror catalog/, datasets/, frames/,
# edgeml/eval/, raw/singapore_maritime/ into MIRROR without modifying GCS.
python edgeml/tools/validate_data.py --mirror "$MIRROR" --object-inventory objects.jsonl --repair-smd --output clean-new
python edgeml/tools/validate_data.py --extra-only --image-root "$MIRROR/raw/ir_maritime_vessels" --image-root "$MIRROR/raw/seaships7000" --output raw-new
python edgeml/tools/publish_clean_data.py --help
# Choose a NEW prefix and table suffix; never reuse the published destination.
go build ./... && go vet ./... && go test ./...
~/.nvm/versions/node/v24.22.2/bin/node --test web/*.test.cjs
python -m pytest edgeml/tests -q
python -m compileall -q edgeml/tools
git diff --check
```

Executed verification: Go build/vet/tests passed; JavaScript 20 tests passed; Python 57 tests passed; compileall and whitespace checks passed. Go executable was installed at /home/ubuntu/go/bin and added to PATH; the requested Node executable was used. Python editable test/audit installation succeeded. The unit tests include SMD identity, duplicate/split quarantine, bounds, explicit clean split behavior and fail-closed output creation. No model tuning or locked-test accuracy evaluation was performed.

Independent BigQuery integrity query (executed with `bq query --use_legacy_sql=false --format=json`; result retained as verification.json in the reviewed prefix):

```sql
SELECT
  (SELECT COUNT(*) FROM ghost_fleet.images_clean_v2) AS images,
  (SELECT COUNT(*) FROM ghost_fleet.annotations_clean_v2) AS annotations,
  (SELECT COUNT(*) FROM ghost_fleet.datasets_clean_v2) AS datasets,
  (SELECT COUNT(*) FROM ghost_fleet.annotations_clean_v2 a
   LEFT JOIN ghost_fleet.images_clean_v2 i USING(dataset_id,image_id)
   WHERE i.image_id IS NULL) AS orphans,
  (SELECT COUNT(*) FROM (
    SELECT sha256 FROM ghost_fleet.images_clean_v2
    WHERE eval_split IN ('dev','test') GROUP BY sha256
    HAVING COUNT(DISTINCT eval_split)>1)) AS exact_cross_split
```
