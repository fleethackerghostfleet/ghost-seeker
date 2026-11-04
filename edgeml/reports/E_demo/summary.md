# edgeml demo run — e_20261104_2145

`edgeml/scripts/demo.py --clip /home/ubuntu/data/edgeml_eval/ghost_fleet_5fps/A_ship_in_the_distance --clip /home/ubuntu/data/edgeml_eval/ghost_fleet_5fps/Cruise_ship_Mediterranean_sunset_off_Italy --clip /home/ubuntu/data/edgeml_eval/ghost_fleet_5fps/Submarine_surfacing --clip /home/ubuntu/data/edgeml_eval/ghost_fleet_5fps/Ship_in_the_Distance_Utopia_of_the_Seas --clip /home/ubuntu/data/edgeml_eval/fvessel_clip10/fvessel_clip01 --budgets 72000 480000 1920000 15000000 --out /home/ubuntu/runs/e_20261104_2145`

detector `yolo11n-int8-640-openvino` (5.8 MB), embedder `mobileclip-s1-ort-fp32`, tracker `bytetrack`/centroid, detect_every=1, gate=on, 4 threads on CPUs [0, 1, 2, 3], git `8b55def`

## Per clip

| clip | fps | frames (det / gated) | dets | tracks | sightings open / update / close | GT obj | reports / obj | object recall | dup / obj | false / min | TTFR s | ms/frame p50 / p95 | det ms p50 | embeds |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| A_ship_in_the_distance | 5 | 46 (44 / 2) | 32 | 1 | 1 / 0 / 1 | 1 | 1.0 | 1.00 | 0.00 | 0.0 | 0.2 | 39 / 88 | 34 | 5 |
| Cruise_ship_Mediterranean_sunset_off_Italy | 5 | 153 (153 / 0) | 63 | 4 | 4 / 0 / 4 | 3 | 1.0 | 1.00 | 0.00 | 2.0 | 0.4 | 42 / 94 | 35 | 20 |
| Submarine_surfacing | 5 | 372 (372 / 0) | 184 | 5 | 4 / 3 / 4 | 2 | 3.5 | 1.00 | 1.00 | 0.0 | 0.2 | 40 / 108 | 37 | 53 |
| Ship_in_the_Distance_Utopia_of_the_Seas | 5 | 75 (46 / 29) | 61 | 3 | 3 / 1 / 3 | 2 | 1.5 | 1.00 | 0.00 | 4.0 | 0.1 | 36 / 105 | 34 | 17 |
| fvessel_clip01 | 1 | 111 (12 / 99) | 18 | 2 | 2 / 3 / 2 | 2 | 2.5 | 1.00 | 0.00 | 0.0 | 9.0 | 4 / 42 | 34 | 2 |

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
| fvessel_clip01 | **sightings** | 5 | 2/2 / 2/2 / 35 | 2/2 / 2/2 / 211 | 2/2 / 2/2 / 256 | 2/2 / 2/2 / 256 |
| fvessel_clip01 | image_dedup | 4 | 0/2 / 2/2 / - | 2/2 / 2/2 / 179 | 2/2 / 2/2 / 179 | 2/2 / 2/2 / 179 |
| fvessel_clip01 | naive | 18 | 0/2 / 2/2 / - | 2/2 / 2/2 / 417 | 2/2 / 2/2 / 844 | 2/2 / 2/2 / 844 |
| **all GT clips** | **sightings** | 21 | 10/10 / 10/10 / 33 | 10/10 / 10/10 / 210 | 10/10 / 10/10 / 248 | 10/10 / 10/10 / 248 |
| **all GT clips** | image_dedup | 73 | 2/10 / 10/10 / 37 | 9/10 / 10/10 / 254 | 10/10 / 10/10 / 529 | 10/10 / 10/10 / 828 |
| **all GT clips** | naive | 358 | 2/10 / 10/10 / 72 | 9/10 / 10/10 / 338 | 9/10 / 10/10 / 1200 | 10/10 / 10/10 / 3442 |

## Artifacts per clip

`sightings.jsonl` (events with `value` + `forms`), `dets.jsonl`, `tracks.txt` (MOT), `selection_<budget>.json`, `contact_sheet_<budget>.jpg`, `timeline.png`, `metrics.json`.
