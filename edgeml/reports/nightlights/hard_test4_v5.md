# Night lights: hard/test4

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split test4 --version v5`

Synthetic benchmark only; not operational validation.

- light_precision: 0.7290
- light_recall: 0.5032
- rule_accuracy: 0.3036
- automatic_rule_accuracy: 0.2041
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.7857
- flashing_accuracy: 0.5548
- night_gate_accuracy: 0.9779
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 3 / 7 | 0.4286 |
| constrained_draught | 3 / 7 | 0.4286 |
| nuc | 2 / 7 | 0.2857 |
| trawling | 1 / 7 | 0.1429 |
| fishing | 1 / 7 | 0.1429 |
| pilot | 0 / 7 | 0.0000 |
| tow_long | 1 / 7 | 0.1429 |
| two_mastheads | 1 / 7 | 0.1429 |
| head_on | 0 / 7 | 0.0000 |
| port | 0 / 7 | 0.0000 |
| starboard | 1 / 7 | 0.1429 |
| white_ambiguous | 4 / 7 | 0.5714 |
| air_cushion | 3 / 7 | 0.4286 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | ambiguous_orange | miss |
|---|---:|---:|---:|---:|---:|---:|---:|
| red | 34 | 0 | 3 | 0 | 0 | 2 | 31 |
| green | 0 | 4 | 0 | 0 | 0 | 0 | 12 |
| white | 0 | 0 | 37 | 0 | 0 | 3 | 21 |
| yellow | 0 | 0 | 0 | 3 | 0 | 0 | 2 |
| blue | 0 | 0 | 0 | 0 | 0 | 0 | 3 |
| ambiguous_orange | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| background | 1 | 0 | 20 | 0 | 0 | 0 | 0 |

Unmatched predictions: {"white": 20, "red": 1}

False-positive sources: {"shore": 14, "pier": 7}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 29, "total": 70, "accuracy": 0.4142857142857143}, "unresolvable": {"correct": 5, "total": 42, "accuracy": 0.11904761904761904}}
