# Frozen shore detector data and evaluation

The approved Singapore policy is `singapore_maritime_v2` in `configs/splits.yaml`:
26 train / 5 valid / 5 locked test videos. Quarantine is lifted **only** for this
policy, not the upstream frame splits or other Singapore catalog consumers.
MVI_1578_VIS is reserved for low-light validation. No NIR is included.

## Freeze

`tools/shore_export.py` reads only BigQuery `images_clean_v2` and
`annotations_clean_v2`, snapshots their rows and policy, uses the reviewed clean
MOT annotations, and applies the reviewed frame-selection quarantine. FVessel
clip05 and 13 black/blank submarine frames remain excluded.

The completed freeze is at:

`gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/shore/frozen-20261105-v2/`

Only a prefix with `COMPLETE.json` is a usable freeze. The abandoned v1 prefix
contains input metadata only; it failed closed on normalized onboard source IDs
before publishing a dataset. Do not use it.

| Set | Images | Tiny | Small | Medium | Large | Low-light frames / boxes |
|---|---:|---:|---:|---:|---:|---:|
| train | 15,101 | 2 | 1,494 | 33,646 | 40,762 | 16 / 13 |
| valid | 2,423 | 17 | 1,989 | 7,943 | 7,684 | 505 / 3,535 |
| test_sg | 1,984 | 30 | 3,395 | 7,616 | 5,010 | 0 / 0 |
| test_gf | 4,926 | 644 | 713 | 3,199 | 2,820 | 27 / 3 |
| test_fvessel | 1,189 | 0 | 1 | 871 | 882 | 0 / 0 |
| test_smd | 40 | 0 | 0 | 20 | 52 | 0 / 0 |
| test_seaships | 3,500 | 0 | 9 | 426 | 4,248 | 13 / 13 |

The freeze check verified all seven annotation hashes, image-shard hashes and
bucket counts, known source frame indices, zero exact-image/source-recording
overlap across partitions, and manifest files individually below 1 MB. SeaShips
recording independence remains unknown despite distinct catalog source IDs.

Layout:

```
dataset/train/{images,_annotations.coco.json}
dataset/valid/{images,_annotations.coco.json}
locked/test_sg/{images,_annotations.coco.json}
locked/test_gf/{images,_annotations.coco.json}
locked/test_fvessel/{images,_annotations.coco.json}
locked/test_smd/{images,_annotations.coco.json}
locked/test_seaships/{images,_annotations.coco.json}
inputs/                         # source snapshots, not committed
manifests/                      # identical to committed configs/shore-frozen-v2
COMPLETE.json
```

Manifests record annotation-file SHA-256, counts, source IDs, low-light counts,
and hashes of small image-list shards. Each shard records filename, byte SHA-256,
and vessel counts by size bucket. COCO image records preserve dataset, original
image ID, source video, frame index, FPS where known, and low-light provenance.
Catalog frame indices are extracted from original filenames; no FPS or tracks
are invented for Singapore/SMD. SeaShips recording/frame indices are unknown;
its `frame_index` is null rather than mistaking its image ID for a frame number.
MOT track IDs are scoped to dataset + sequence.
The two Norfolk segments represent one source recording for source-count metrics.

SeaShips upstream train/valid, historically called **dev** in the general split
policy, becomes **train for this project only**. Shore valid is the five Singapore
dev videos plus onboard MVI_0790 and MVI_0799. Other existing split entries have
not changed. Train also includes FVessel dev clips and Ghost Fleet 5fps dev GT;
PR #50 teacher-only frames and synthetic assets are excluded.

Example reproduction (choose a NEW prefix and NEW local directories):

```
python edgeml/tools/shore_export.py \
  --prefix edgeml/shore/<new-freeze-id> \
  --output ~/work/shore/<new-freeze-id> \
  --manifests ~/work/shore/<new-freeze-id>-manifests
```

Publication uses GCS generation-match-zero writes. Existing freezes cannot be
overwritten by the exporter. Manifests are content locks, not bucket retention
policies; privileged bucket administrators can still delete objects.
`--resume-incomplete` reuses the snapshotted catalog and accepts existing objects
only when their byte checksums match; it rejects any prefix with `COMPLETE.json`.
The initial export was resumed after identifying SMD's 40 non-box `horizon`
annotations. Those are excluded from detection COCO, not converted to fake boxes.

## Labels and limitations

Single category `vessel`. Catalog labels use `label_norm=vessel`. Ghost Fleet
MOT vessel classes are ship, boat, sailboat, submarine, jet ski and kayak; FVessel
uses class 1 vessel. Non-vessel GT is retained as `ignore=1, iscrowd=1`, following
the benchmark's vessel evaluation convention. Boxes retain original pixel units.
Low-light means PIL YCbCr mean Y <60 **or** the visually reviewed MVI_1578_VIS
source. This includes artificial dark fades in Ghost Fleet; it is not a solar
elevation classification. SeaShips night-camera overlap remains unresolved.

This is a reproducible data freeze, **not human-completeness certification**.
Ghost Fleet labels are partial; unlabelled regions may contain real contacts.
COCO crowd flags support evaluator ignore handling, not a guarantee that every
training implementation masks unknown regions. Review before fine-tuning.
Licenses remain unknown, research-restricted, or internal-use-only according to
the underlying source. FVessel shares a fixed camera across recordings.
Singapore has video IDs, not verified independent camera IDs. Adjacent frame
boxes and track windows are correlated; binomial bounds are nominal box-level
bounds, not independent-encounter confidence intervals.

## Evaluation and threshold freezing

`tools/shore_eval.py` consumes frozen COCO annotations and predictions of form
`{"metadata": {configuration}, "detections": [COCO result objects]}`.

```
python edgeml/tools/shore_eval.py --coco <valid/_annotations.coco.json> \
  --predictions predictions.json --fit-valid --thresholds threshold.json \
  --output metrics.json
```

Threshold fitting is rejected unless the COCO split is `valid`. It maximizes
recall subject to precision >=0.90, with tied recall resolved by higher precision
then higher threshold. Equal-score detections cannot be split. If no nonempty
operating point satisfies precision, `feasible=false` and a reject-all threshold
is recorded; this must not be interpreted as meeting the precision requirement.
Threshold JSON records the valid annotation hash and detector configuration;
fixed-threshold evaluation requires the same configuration. Do not fit or choose
tiling on locked tests.

Recall uses one-to-one score-ordered matching at IoU >=0.3 for GT area <1024,
otherwise >=0.5. Non-vessel crowd matches are ignored at IoU >=0.5. Outputs include
all/day/low-light and size strata, day medium+large combined, one-sided 95% exact
Clopper–Pearson recall lower bounds, precision, FP per vessel-empty frame, and
standard COCO AP50 (IoU .5, 101 recall points, maxDets=100, independent of operating
threshold). Empty denominators are null, never a passing score.

For size precision, TP bins use matched GT area and FP bins use predicted area.
FP/empty-frame bins use all vessel-empty frames of the relevant lighting group,
not frames merely missing that particular size. AP uses COCO area filtering.

R4 uses every sliding, fully observed two-second window of a MOT track:
`ceil(2*fps)` consecutive sampled frames, scoped by sequence; visibility gaps
break windows. Short tracks and incomplete tails are excluded. A window is a hit
if any GT observation in it was matched at the operating threshold. Track-window
counts are reported without a binomial independence claim. Catalog sets lacking
track IDs report R4 as unavailable, not zero.

## DEV-only zero-shot baseline

`tools/shore_baseline.py` explicitly rejects non-valid inputs and verifies the
committed annotation, shard, and image hashes. It runs the existing RF-DETR and
tiling wrappers, filtering COCO boat predictions to vessel. No training occurs.
Native resolution is asserted to be 512. Tiled mode uses 640px tiles, 20% overlap,
full frame included, class-agnostic NMS IoU .5 and max 300 merged detections.
Candidate score floor is .001; threshold search cannot recover detections below it.

Use the assigned `shore-det-a100` VM in `us-central1-c`, not other GPUs/VMs:

```
python edgeml/tools/shore_baseline.py --valid ~/shore-valid \
  --manifests edgeml/configs/shore-frozen-v2 --mode native --output ~/shore-native
python edgeml/tools/shore_baseline.py --valid ~/shore-valid \
  --manifests edgeml/configs/shore-frozen-v2 --mode tiled --output ~/shore-tiled
```

Latency measures synchronized preprocessing, GPU inference and merge on decoded
1920x1080 frames, excluding disk IO and five warmup passes. Other resolutions
contribute to accuracy but not 1080p latency. A100 timings cannot certify the
draft's L4 latency gate. The environment uses torch 2.9.1+cu129, RF-DETR 1.3.0,
transformers 4.57.1 and peft 0.17.1. The model checkpoint SHA-256 is
`d81979a9213a2109345158ce9232668df4c1ae52e9b8db3f2ec0a8cbad959b33`.

Tests:

```
PYTHONPATH=edgeml/src python -m pytest -q edgeml/tests/test_shore.py edgeml/tests/test_b_zeroshot.py
```
