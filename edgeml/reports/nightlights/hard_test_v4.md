# Night lights: hard/test

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split test --version v4`

Synthetic benchmark only; not operational validation.

- light_precision: 0.8103
- light_recall: 0.5802
- rule_accuracy: 0.3571
- automatic_rule_accuracy: 0.2653
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.8036
- flashing_accuracy: 0.6420
- night_gate_accuracy: 0.9632
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 0 / 7 | 0.0000 |
| constrained_draught | 4 / 7 | 0.5714 |
| nuc | 1 / 7 | 0.1429 |
| trawling | 3 / 7 | 0.4286 |
| fishing | 1 / 7 | 0.1429 |
| pilot | 2 / 7 | 0.2857 |
| tow_long | 3 / 7 | 0.4286 |
| two_mastheads | 1 / 7 | 0.1429 |
| head_on | 2 / 7 | 0.2857 |
| port | 1 / 7 | 0.1429 |
| starboard | 2 / 7 | 0.2857 |
| white_ambiguous | 5 / 7 | 0.7143 |
| air_cushion | 1 / 7 | 0.1429 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | ambiguous_orange | miss |
|---|---:|---:|---:|---:|---:|---:|---:|
| red | 39 | 0 | 4 | 0 | 0 | 1 | 24 |
| green | 0 | 8 | 0 | 0 | 0 | 0 | 7 |
| white | 1 | 0 | 45 | 2 | 0 | 1 | 19 |
| yellow | 0 | 0 | 1 | 2 | 0 | 0 | 2 |
| blue | 0 | 0 | 0 | 0 | 0 | 0 | 6 |
| ambiguous_orange | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| background | 2 | 0 | 10 | 0 | 0 | 0 | 0 |

Unmatched predictions: {"white": 10, "red": 2}

False-positive sources: {"shore": 7, "pier": 5}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 36, "total": 75, "accuracy": 0.48}, "unresolvable": {"correct": 4, "total": 37, "accuracy": 0.10810810810810811}}
