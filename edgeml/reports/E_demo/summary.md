# edgeml demo run — e_20261104_2152

`edgeml/scripts/demo.py --clip /home/ubuntu/data/submarine_tail.mp4 --fps 5 --budgets 72000 480000 1920000 15000000 --out /home/ubuntu/runs/e_20261104_2152 --append`

detector `yolo11n-int8-640-openvino` (5.8 MB), embedder `mobileclip-s1-ort-fp32`, tracker `bytetrack`/centroid, detect_every=1, gate=on, 4 threads on CPUs [0, 1, 2, 3], git `edea094`

## Per clip

| clip | fps | frames (det / gated) | dets | tracks | sightings open / update / close | GT obj | reports / obj | object recall | dup / obj | false / min | TTFR s | ms/frame p50 / p95 | det ms p50 | embeds |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| A_ship_in_the_distance | 5 | 46 (44 / 2) | 32 | 1 | 1 / 0 / 1 | 1 | 1.0 | 1.00 | 0.00 | 0.0 | 0.2 | 56 / 119 | 49 | 5 |
| Cruise_ship_Mediterranean_sunset_off_Italy | 5 | 153 (153 / 0) | 63 | 4 | 4 / 0 / 4 | 3 | 1.0 | 1.00 | 0.00 | 2.0 | 0.4 | 71 / 163 | 59 | 20 |
| Submarine_surfacing | 5 | 372 (372 / 0) | 184 | 5 | 4 / 3 / 4 | 2 | 3.5 | 1.00 | 1.00 | 0.0 | 0.2 | 44 / 130 | 40 | 52 |
| Ship_in_the_Distance_Utopia_of_the_Seas | 5 | 75 (46 / 29) | 61 | 3 | 3 / 1 / 3 | 2 | 1.5 | 1.00 | 0.00 | 4.0 | 0.1 | 37 / 146 | 34 | 18 |
| fvessel_clip01 | 1 | 111 (12 / 99) | 18 | 2 | 2 / 6 / 2 | 2 | 4.0 | 1.00 | 0.00 | 0.0 | 9.0 | 4 / 43 | 34 | 2 |
| fvessel_clip01__nogate | 1 | 111 (111 / 0) | 154 | 7 | 7 / 11 / 7 | 2 | 5.5 | 1.00 | 0.50 | 2.2 | 0.0 | 39 / 112 | 34 | 22 |
| fvessel_clip01__force2s | 1 | 111 (56 / 55) | 78 | 3 | 3 / 7 / 3 | 2 | 4.0 | 1.00 | 0.00 | 0.5 | 1.0 | 37 / 84 | 33 | 10 |
| submarine_tail | 5 | 222 (222 / 0) | 183 | 5 | 4 / 3 / 4 | - | - | - | - | - | - | 38 / 104 | 34 | 46 |

`ms/frame` is wall time per input frame on the pinned CPUs including gate, detector, tracker, aggregator and form encoding (gated-out frames cost only the gate). `reports / obj` = open+update events per matched GT object. `false / min` counts sightings not matched to a GT track — on `partial` clips and FVessel (AIS-equipped vessels only) many of these are real unlabelled boats.

## Delivered to shore vs budget (60 s windows, `select.greedy`)

Unique GT objects with an image form (crop/thumb/full) delivered, out of all GT objects; `fact` column = objects with at least a fact. Baselines use the same detections: `naive` = one report per detection per frame (fact + full frame), `image_dedup` = frame-level near-duplicate suppression (no identity).

| clip | pipeline | reports | 72KB: img / fact / KB per obj | 480KB: img / fact / KB per obj | 1.92MB: img / fact / KB per obj | 15MB: img / fact / KB per obj |
|---|---|---|---|---|---|---|
| A_ship_in_the_distance | **sightings** | 1 | 1/1 / 1/1 / 25 | 1/1 / 1/1 / 103 | 1/1 / 1/1 / 103 | 1/1 / 1/1 / 103 |
| A_ship_in_the_distance | image_dedup | 2 | 0/1 / 1/1 / - | 1/1 / 1/1 / 160 | 1/1 / 1/1 / 160 | 1/1 / 1/1 / 160 |
| A_ship_in_the_distance | naive | 32 | 0/1 / 1/1 / - | 1/1 / 1/1 / 400 | 1/1 / 1/1 / 1825 | 1/1 / 1/1 / 2560 |
| Cruise_ship_Mediterranean_sunset_off_Italy | **sightings** | 4 | 3/3 / 3/3 / 18 | 3/3 / 3/3 / 152 | 3/3 / 3/3 / 251 | 3/3 / 3/3 / 251 |
| Cruise_ship_Mediterranean_sunset_off_Italy | image_dedup | 32 | 0/3 / 3/3 / - | 2/3 / 3/3 / 218 | 3/3 / 3/3 / 584 | 3/3 / 3/3 / 1582 |
| Cruise_ship_Mediterranean_sunset_off_Italy | naive | 63 | 0/3 / 3/3 / - | 2/3 / 3/3 / 219 | 2/3 / 3/3 / 871 | 3/3 / 3/3 / 3127 |
| Submarine_surfacing | **sightings** | 7 | 2/2 / 2/2 / 56 | 2/2 / 2/2 / 379 | 2/2 / 2/2 / 379 | 2/2 / 2/2 / 379 |
| Submarine_surfacing | image_dedup | 27 | 0/2 / 2/2 / - | 2/2 / 2/2 / 453 | 2/2 / 2/2 / 1251 | 2/2 / 2/2 / 1251 |
| Submarine_surfacing | naive | 184 | 0/2 / 2/2 / - | 2/2 / 2/2 / 458 | 2/2 / 2/2 / 1837 | 2/2 / 2/2 / 8347 |
| Ship_in_the_Distance_Utopia_of_the_Seas | **sightings** | 4 | 2/2 / 2/2 / 32 | 2/2 / 2/2 / 179 | 2/2 / 2/2 / 179 | 2/2 / 2/2 / 179 |
| Ship_in_the_Distance_Utopia_of_the_Seas | image_dedup | 8 | 2/2 / 2/2 / 28 | 2/2 / 2/2 / 214 | 2/2 / 2/2 / 258 | 2/2 / 2/2 / 258 |
| Ship_in_the_Distance_Utopia_of_the_Seas | naive | 61 | 2/2 / 2/2 / 35 | 2/2 / 2/2 / 227 | 2/2 / 2/2 / 937 | 2/2 / 2/2 / 2048 |
| fvessel_clip01 | **sightings** | 8 | 2/2 / 2/2 / 41 | 2/2 / 2/2 / 277 | 2/2 / 2/2 / 410 | 2/2 / 2/2 / 410 |
| fvessel_clip01 | image_dedup | 4 | 0/2 / 2/2 / - | 2/2 / 2/2 / 179 | 2/2 / 2/2 / 179 | 2/2 / 2/2 / 179 |
| fvessel_clip01 | naive | 18 | 0/2 / 2/2 / - | 2/2 / 2/2 / 417 | 2/2 / 2/2 / 844 | 2/2 / 2/2 / 844 |
| fvessel_clip01__nogate | **sightings** | 18 | 2/2 / 2/2 / 69 | 2/2 / 2/2 / 444 | 2/2 / 2/2 / 951 | 2/2 / 2/2 / 951 |
| fvessel_clip01__nogate | image_dedup | 5 | 0/2 / 2/2 / - | 2/2 / 2/2 / 235 | 2/2 / 2/2 / 235 | 2/2 / 2/2 / 235 |
| fvessel_clip01__nogate | naive | 154 | 0/2 / 2/2 / - | 2/2 / 2/2 / 422 | 2/2 / 2/2 / 1863 | 2/2 / 2/2 / 7341 |
| fvessel_clip01__force2s | **sightings** | 10 | 2/2 / 2/2 / 55 | 2/2 / 2/2 / 391 | 2/2 / 2/2 / 527 | 2/2 / 2/2 / 527 |
| fvessel_clip01__force2s | image_dedup | 6 | 0/2 / 2/2 / - | 2/2 / 2/2 / 287 | 2/2 / 2/2 / 287 | 2/2 / 2/2 / 287 |
| fvessel_clip01__force2s | naive | 78 | 0/2 / 2/2 / - | 2/2 / 2/2 / 428 | 2/2 / 2/2 / 1834 | 2/2 / 2/2 / 3795 |
| submarine_tail | (no GT) | sightings 7, naive 183, dedup 27 | 4 sightings, 64 KB | 4 sightings, 385 KB | 4 sightings, 740 KB | 4 sightings, 740 KB |
| **all GT clips** | **sightings** | 24 | 10/10 / 10/10 / 34 | 10/10 / 10/10 / 223 | 10/10 / 10/10 / 279 | 10/10 / 10/10 / 279 |
| **all GT clips** | image_dedup | 73 | 2/10 / 10/10 / 37 | 9/10 / 10/10 / 254 | 10/10 / 10/10 / 529 | 10/10 / 10/10 / 828 |
| **all GT clips** | naive | 358 | 2/10 / 10/10 / 72 | 9/10 / 10/10 / 338 | 9/10 / 10/10 / 1200 | 10/10 / 10/10 / 3442 |

## Artifacts per clip

`sightings.jsonl` (events with `value` + `forms`), `dets.jsonl`, `tracks.txt` (MOT), `selection_<budget>.json`, `contact_sheet_<budget>.jpg`, `timeline.png`, `metrics.json`.
