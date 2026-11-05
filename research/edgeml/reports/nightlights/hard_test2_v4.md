# Night lights: hard/test2

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split test2 --version v4`

Synthetic benchmark only; not operational validation.

- light_precision: 0.8043
- light_recall: 0.4458
- rule_accuracy: 0.3036
- automatic_rule_accuracy: 0.2041
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.7946
- flashing_accuracy: 0.5301
- night_gate_accuracy: 0.9706
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 0 / 7 | 0.0000 |
| constrained_draught | 2 / 7 | 0.2857 |
| nuc | 3 / 7 | 0.4286 |
| trawling | 2 / 7 | 0.2857 |
| fishing | 0 / 7 | 0.0000 |
| pilot | 0 / 7 | 0.0000 |
| tow_long | 2 / 7 | 0.2857 |
| two_mastheads | 3 / 7 | 0.4286 |
| head_on | 1 / 7 | 0.1429 |
| port | 0 / 7 | 0.0000 |
| starboard | 5 / 7 | 0.7143 |
| white_ambiguous | 2 / 7 | 0.2857 |
| air_cushion | 0 / 7 | 0.0000 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | ambiguous_orange | miss |
|---|---:|---:|---:|---:|---:|---:|---:|
| red | 29 | 0 | 3 | 0 | 0 | 3 | 38 |
| green | 0 | 10 | 0 | 0 | 0 | 0 | 10 |
| white | 4 | 0 | 35 | 1 | 0 | 2 | 22 |
| yellow | 0 | 0 | 1 | 0 | 0 | 0 | 3 |
| blue | 0 | 0 | 0 | 0 | 0 | 0 | 5 |
| ambiguous_orange | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| background | 1 | 1 | 2 | 0 | 0 | 0 | 0 |

Unmatched predictions: {"red": 1, "white": 2, "green": 1}

False-positive sources: {"shore": 2, "pier": 2}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 32, "total": 78, "accuracy": 0.41025641025641024}, "unresolvable": {"correct": 2, "total": 34, "accuracy": 0.058823529411764705}}
