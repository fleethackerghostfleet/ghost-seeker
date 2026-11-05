# Night lights: hard/test2

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split test2 --version v2`

Synthetic benchmark only; not operational validation.

- light_precision: 0.1372
- light_recall: 0.3133
- rule_accuracy: 0.2143
- automatic_rule_accuracy: 0.1020
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.5357
- flashing_accuracy: 0.4217
- night_gate_accuracy: 0.9706
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 0 / 7 | 0.0000 |
| constrained_draught | 0 / 7 | 0.0000 |
| nuc | 0 / 7 | 0.0000 |
| trawling | 2 / 7 | 0.2857 |
| fishing | 2 / 7 | 0.2857 |
| pilot | 0 / 7 | 0.0000 |
| tow_long | 1 / 7 | 0.1429 |
| two_mastheads | 1 / 7 | 0.1429 |
| head_on | 0 / 7 | 0.0000 |
| port | 0 / 7 | 0.0000 |
| starboard | 4 / 7 | 0.5714 |
| white_ambiguous | 0 / 7 | 0.0000 |
| air_cushion | 0 / 7 | 0.0000 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | miss |
|---|---:|---:|---:|---:|---:|---:|
| red | 12 | 0 | 0 | 9 | 0 | 52 |
| green | 0 | 13 | 0 | 0 | 0 | 7 |
| white | 4 | 0 | 21 | 6 | 0 | 33 |
| yellow | 0 | 0 | 0 | 2 | 0 | 2 |
| blue | 0 | 0 | 0 | 0 | 4 | 1 |
| background | 59 | 0 | 232 | 15 | 2 | 0 |

Unmatched predictions: {"white": 232, "red": 59, "yellow": 15, "blue": 2}

False-positive sources: {"shore": 187, "pier": 121}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 22, "total": 78, "accuracy": 0.28205128205128205}, "unresolvable": {"correct": 2, "total": 34, "accuracy": 0.058823529411764705}}
