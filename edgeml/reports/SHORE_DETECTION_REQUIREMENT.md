# Shore vessel detection — accuracy requirement (DRAFT, pending approval)

**UNCLASSIFIED / public-data prototype.** Status: draft for Patrick's approval. Numbers marked *(proposed)* are choices, not measurements.

## 1. What "95%" means

The shore detector must find vessels in uplinked EO frames. The headline number is **vessel recall**: the share of ground-truth vessels the detector finds.

| # | Metric | Target *(proposed)* | Why |
|---|---|---|---|
| R1 | Frame-level vessel recall, IoU ≥ 0.5 (IoU ≥ 0.3 for boxes < 32² px) | **≥ 95%**, measured as the one-sided 95% Clopper–Pearson lower bound | The "95%" the operator asked for. Using the lower bound means we can't pass on luck with a small sample |
| R2 | Precision at the same threshold | ≥ 90% | Stops recall being bought with false alarms |
| R3 | False alarms on vessel-empty sea frames | ≤ 0.05 per frame | Glint, whitecaps and birds must not flood operators |
| R4 | Track-level recall: a vessel visible for ≥ 2 s is detected in ≥ 1 frame of that window | ≥ 98% | What operators actually experience; a single-frame miss is tolerable |
| R5 | Latency on shore GPU (L4), p95 per 1080p frame including tiling | ≤ 250 ms | Keeps up with uplink rate |

Recall is a single class ("vessel"); subtype is out of scope (see ship classification, PR #48).

## 2. Where it must hold (strata)

R1–R3 are reported **per stratum**, and the 95% gate applies to each **gated** stratum separately. Pooling would let easy large ships hide misses on far contacts.

| Stratum | Definition | Gate *(proposed)* |
|---|---|---|
| Large/medium, day, EO | box ≥ 32² px | **95% gate** |
| Small, day, EO | 16² – < 32² px | **95% gate** |
| Tiny, day, EO | < 16² px | Reported. Target 80%; becomes a gate once the data exists |
| Night / low light, EO | sun below horizon or visibly dark frame | Reported. Gated once ≥ 300 vessels are labelled |
| IR / thermal | — | Out of scope until IR joins are repaired (DATA_CLEANUP.md) |

## 3. How many test vessels are needed

To prove ≥ 95% (one-sided 95% CP lower bound), each gated stratum needs enough vessels:

| vessels in stratum | max misses allowed | observed recall needed |
|---|---|---|
| 100 | 1 | 99.0% |
| 300 | 8 | 97.3% |
| 500 | 16 | 96.8% |
| 1000 | 38 | 96.2% |

**Proposed: ≥ 300 vessels per gated stratum**, drawn from ≥ 5 source videos/cameras not used in training or tuning.

## 4. Test protocol

- **Locked test set:** sources follow `edgeml/configs/splits.yaml` test entries plus new held-out sources. Split by source video, never by frame, and use clean catalog v2 only (FVessel clip05 is quarantined). The thresholds and tiling configuration are frozen on dev before the single test run.
- **Labels:** completeness is checked by a human. PR #50 found distant and moored boats under-labelled, and a missing label would make real detections count as false positives.
- **Result:** the pass/fail table per stratum, plus failure examples.

## 5. Open decisions for Patrick

1. Is frame-level recall (R1) the right headline, or should R4 (per-vessel over 2 s) be the headline?
2. Should small boats (16–32 px) be in the 95% gate, or only medium/large?
3. Should night be a gate now (this needs new labelling) or reported only?
4. Are the false-alarm budget (R3) and the latency target (R5) acceptable?
