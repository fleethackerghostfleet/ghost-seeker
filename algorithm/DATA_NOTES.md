# Initial data audit

Audit date: 2026-11-04. Source checkout: `d04ffe2` on `main` before the local algorithm branch. This is an inspection of a small sample, not an accuracy evaluation or a new ground-truth annotation set.

## What was retrieved

The repository contains five Git LFS ZIP pointers. Their archives contain 13,388 image entries and total 414,586,043 bytes. The initial download selected up to 24 evenly spaced member indices per archive, preserving the archive source and member name in a manifest. It fetched 101 JPEG images using partial archive reads (about 6.46 MB transferred including ZIP directory overhead). The samples themselves total 4,937,527 bytes.

| Source archive | Archive images | Sampled | Sample dimensions |
|---|---:|---:|---|
| `30_Days_at_Sea_Timelapse_frames` | 644 | 24 | 1920 × 1080 |
| `Argentina_to_Alaska_Cruise_Bridge_Timelapse_frames` | 139 | 24 | 640 × 480 |
| `Cargo_Ship_Storm_first_hour_frames` | 3,600 | 24 | 426 × 226 |
| `Cargo_Ship_Uncut_View_frames` | 9,000 | 24 | 640 × 360 |
| `Dolphins_Pungo_Ferry_Bridge_frames` | 5 | 5 | 522 × 928 |

All 101 sampled files decoded successfully. There are 100 unique file SHA-256 values: the first and last sampled Uncut frames are identical all-black images. This exact-file check does not measure perceptual duplication.

## What the images show

- **30 Days:** a substantial portion of most images is the camera platform's deck, cargo and cranes. The sample spans open water, ports, sunlight, clouds, dusk and very dark night views. Mean grayscale intensity ranges from 3.13 to 144.97 on a 0–255 scale. Changes in exposure, scenery and lighting can dominate a simple novelty score. Sample `000168` contains a clearly visible separate vessel to the right of the platform's bow, useful for a human-reviewed positive example.
- **Argentina to Alaska:** the own-ship bow, dark bridge window frames and lower-image location captions persist. Ports and open water alternate. A detector or saliency map can respond strongly to the fixed structures and captions. Similar-looking samples are not necessarily duplicates of the same real-world event because the sequence is a time-lapse.
- **Storm:** own-ship structure occupies a large part of the low-resolution frame. Whitecaps, spray, lens/window blur and camera movement vary across the sequence. These are useful stress examples for visual change, but high change alone does not establish the presence of a new external object.
- **Uncut:** most sampled images have a large own-ship container/deck foreground, with ocean, clouds, rain and spray behind it. Samples `000000` and `008999` are all black. Treat them explicitly as invalid/blank visual input in analysis and reporting; whether a camera blackout itself warrants transmission is a separate operator policy.
- **Dolphins:** five very similar portrait frames show water, shoreline, sky and a fixed channel marker. A small dark surface feature is visible near the center of sample `000002`. The archive name does not establish a visual class label. Tiny features require inspection at source resolution and can become only a few pixels after resizing.

Contact sheets of all samples and full-resolution examples were visually inspected. No bounding boxes or authoritative object labels were created during this audit.

## Implications for the first benchmark

1. **Separate own-platform evidence from external-object evidence.** A generic `boat` prediction can include the ship carrying the camera. Report that ambiguity. A future sensor-specific exclusion mask for fixed deck/window regions could be useful after review, but it must not silently remove unreviewed image regions or the sky.
2. **Keep source proportions explicit.** Sampling 24 images from both a 139-frame and a 9,000-frame archive is a balanced smoke test, not an estimate of the incoming stream distribution. Report per-source results and do not infer deployment-wide rates from the aggregate.
3. **Do not invent timestamps.** Archive member indices are not established capture times. Treat this sample as still images with source groups; it cannot establish event recall, detection delay, or performance on continuously sampled video.
4. **Compare encoded payload bytes consistently.** Source JPEG sizes vary greatly with resolution and content. Apply the same output encoding and maximum dimensions to every selection method before comparing byte budgets. Also report the resulting detail loss, especially for small features.
5. **Include difficult background examples.** Whitecaps, glare, rain, clouds, crane silhouettes and captions are useful candidate false positives. They are not confirmed negative object annotations.
6. **Use labels only in evaluation.** Added detection annotations must remain separate from the analyzer and ranking inputs. Missing annotations cannot be treated as empty background until annotation scope is known. Separate observed object categories, operator relevance and unknown/uncertain review flags.
7. **Split by source recording.** Nearby frames and repeated scenes should not be randomly split between development and held-out test sets. The sampled images are development/smoke-test material; final performance needs another held-out group.
8. **Inspect what gets discarded.** Review low-ranking images and occasional representative selections, not only high-ranking results. An unknown object may be inconspicuous, while a harmless lighting change can be salient.

## UI handoff

The current UI is explicitly a synthetic mockup. Its simulated class names include vessel subtypes and its generated attributes include approaching, loitering and absence of AIS/transponder correlation. Those are not outputs established by the initial image baseline.

A generic detector's `boat` output must remain `boat`; do not relabel it as a fishing vessel or other subtype. A single image cannot establish track direction, loitering or AIS correlation. Pass supported labels, candidate regions and visual-novelty reasons through as measured. Unsupported operator requests should be shown as unsupported or awaiting another engine/data source. Priority scores are ranking heuristics, not calibrated probabilities of an object's identity or relevance.

## November 4 update: labelled SMD subset

Remote `main` at `095a8e9` adds `frames/SMD_VIS_Onboard_frames.zip` via data commit `3461516`. The archive is 14,699,611 bytes, SHA-256 `09581b624b7233329de95621bd4e3f6b289d59b61e439772e9d05704a55a4912`. It is worth adding as a small labelled evaluation set alongside the original stress imagery.

It contains 80 RGB images, 80 matching JSON labels, an index, and a README. All images decode at 1920×1080. Four video groups each contribute frames 0,30,...,570 with supplied timestamps 0..19 seconds. Source counts and image/label dimensions were checked; source MATLAB annotations were not independently re-extracted.

| Video | Images with object annotations | Empty annotation lists | Instances |
|---|---:|---:|---:|
| MVI_0790 | 10 | 10 | 10 |
| MVI_0797 | 20 | 0 | 42 |
| MVI_0799 | 16 | 4 | 20 |
| MVI_0801 | 20 | 0 | 30 |
| Total | 66 | 14 | 102 |

Instance labels comprise 86 Vessel/ship, 7 Boat, 6 Speed boat and 3 Buoy. These are repeated annotations across frames, not 102 distinct objects/events. Forty frames also have horizon annotations.

Review findings:

- Ten boxes slightly cross the image edges. Preserve source coordinates and clip a derived copy for any explicitly defined localization metric. Confirm the MATLAB coordinate origin before exact IoU evaluation.
- MVI_0799 frame 90 has a visibly bad horizon. Horizon labels remain evaluation data, not inference inputs.
- MVI_0799 frames 210/240/270 have empty annotation lists and severe blur with ambiguous dark forms. Preserve the supplied labels; say 'annotated-object image coverage' rather than 'physically empty scenes.'
- `object_id` repeats by category: all Vessel/ship entries have ID 3, including simultaneous objects. It cannot supply event/track identity.
- Many images repeat large external vessels; this is a useful detection/selection check but does not establish performance on unknown designs, tiny features, night scenes, or unseen operating conditions.

The unchanged default was run on all 80 images. At a **792,871-byte allowance**, uniform sampling selected 8 annotated-positive images out of 8 selected; novelty-only and detector-only each selected 11 positives out of 12; the combined default selected 9 positives out of 12. This historical experiment derived its allowance from the common JPEG collection and did not specify a transmission window; it should not be interpreted as a measured link rate. There are 66 positive images in the denominator. These are image-ID coverage results on a small correlated set, not mAP, unique-event recall, or a held-out generalization claim. No policy was tuned on these labels. Future development/test partitions should be grouped by video.

These results describe the earlier combined selector, before the provider and ranking/selection refactor. See [Ranking and selection](RANKING_AND_SELECTION.md) for the implemented separation. New connectivity comparisons should state absolute payload bandwidth and window duration, such as 64 kbps over 60 seconds, alongside the resulting byte allowance.
