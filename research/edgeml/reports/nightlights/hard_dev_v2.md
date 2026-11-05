# Night lights: hard/dev

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split dev --version v2`

Synthetic benchmark only; not operational validation.

- light_precision: 0.1976
- light_recall: 0.4049
- rule_accuracy: 0.2232
- automatic_rule_accuracy: 0.1122
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.5446
- flashing_accuracy: 0.4601
- night_gate_accuracy: 0.9706
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 1 / 7 | 0.1429 |
| constrained_draught | 1 / 7 | 0.1429 |
| nuc | 1 / 7 | 0.1429 |
| trawling | 2 / 7 | 0.2857 |
| fishing | 1 / 7 | 0.1429 |
| pilot | 1 / 7 | 0.1429 |
| tow_long | 1 / 7 | 0.1429 |
| two_mastheads | 0 / 7 | 0.0000 |
| head_on | 1 / 7 | 0.1429 |
| port | 2 / 7 | 0.2857 |
| starboard | 0 / 7 | 0.0000 |
| white_ambiguous | 0 / 7 | 0.0000 |
| air_cushion | 0 / 7 | 0.0000 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | miss |
|---|---:|---:|---:|---:|---:|---:|
| red | 28 | 0 | 1 | 5 | 0 | 35 |
| green | 0 | 5 | 1 | 0 | 0 | 10 |
| white | 1 | 0 | 28 | 2 | 0 | 37 |
| yellow | 0 | 0 | 0 | 2 | 0 | 2 |
| blue | 0 | 0 | 1 | 0 | 3 | 2 |
| background | 55 | 0 | 196 | 6 | 0 | 0 |

Unmatched predictions: {"white": 196, "red": 55, "yellow": 6}

False-positive sources: {"shore": 168, "pier": 89}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 16, "total": 76, "accuracy": 0.21052631578947367}, "unresolvable": {"correct": 9, "total": 36, "accuracy": 0.25}}
