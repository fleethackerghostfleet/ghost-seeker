# Night lights: hard/test3

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --tier hard --split test3 --version v4`

Synthetic benchmark only; not operational validation.

- light_precision: 0.7328
- light_recall: 0.5280
- rule_accuracy: 0.3125
- automatic_rule_accuracy: 0.2143
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.7946
- flashing_accuracy: 0.6273
- night_gate_accuracy: 0.9706
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 2 / 7 | 0.2857 |
| constrained_draught | 4 / 7 | 0.5714 |
| nuc | 1 / 7 | 0.1429 |
| trawling | 0 / 7 | 0.0000 |
| fishing | 2 / 7 | 0.2857 |
| pilot | 1 / 7 | 0.1429 |
| tow_long | 2 / 7 | 0.2857 |
| two_mastheads | 2 / 7 | 0.2857 |
| head_on | 0 / 7 | 0.0000 |
| port | 2 / 7 | 0.2857 |
| starboard | 3 / 7 | 0.4286 |
| white_ambiguous | 2 / 7 | 0.2857 |
| air_cushion | 0 / 7 | 0.0000 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | ambiguous_orange | miss |
|---|---:|---:|---:|---:|---:|---:|---:|
| red | 36 | 0 | 5 | 0 | 0 | 4 | 25 |
| green | 0 | 8 | 2 | 0 | 0 | 0 | 8 |
| white | 3 | 0 | 40 | 0 | 0 | 1 | 20 |
| yellow | 0 | 0 | 1 | 1 | 0 | 0 | 3 |
| blue | 0 | 0 | 0 | 0 | 0 | 0 | 4 |
| ambiguous_orange | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| background | 3 | 0 | 12 | 0 | 0 | 0 | 0 |

Unmatched predictions: {"white": 12, "red": 3}

False-positive sources: {"shore": 9, "pier": 6}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 31, "total": 72, "accuracy": 0.4305555555555556}, "unresolvable": {"correct": 4, "total": 40, "accuracy": 0.1}}
