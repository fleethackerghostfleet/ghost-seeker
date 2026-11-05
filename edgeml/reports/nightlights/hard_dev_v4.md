# Night lights: hard/dev

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --split dev --tier hard --version v4`

Synthetic benchmark only; not operational validation.

- light_precision: 0.7664
- light_recall: 0.5031
- rule_accuracy: 0.3393
- automatic_rule_accuracy: 0.2449
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.7589
- flashing_accuracy: 0.5828
- night_gate_accuracy: 0.9706
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 3 / 7 | 0.4286 |
| constrained_draught | 2 / 7 | 0.2857 |
| nuc | 1 / 7 | 0.1429 |
| trawling | 2 / 7 | 0.2857 |
| fishing | 2 / 7 | 0.2857 |
| pilot | 0 / 7 | 0.0000 |
| tow_long | 2 / 7 | 0.2857 |
| two_mastheads | 2 / 7 | 0.2857 |
| head_on | 0 / 7 | 0.0000 |
| port | 0 / 7 | 0.0000 |
| starboard | 3 / 7 | 0.4286 |
| white_ambiguous | 6 / 7 | 0.8571 |
| air_cushion | 1 / 7 | 0.1429 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | ambiguous_orange | miss |
|---|---:|---:|---:|---:|---:|---:|---:|
| red | 37 | 0 | 4 | 0 | 0 | 1 | 27 |
| green | 0 | 5 | 2 | 0 | 1 | 0 | 8 |
| white | 4 | 0 | 37 | 0 | 0 | 0 | 27 |
| yellow | 0 | 0 | 1 | 2 | 0 | 0 | 1 |
| blue | 0 | 0 | 0 | 0 | 1 | 0 | 5 |
| ambiguous_orange | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| background | 1 | 0 | 10 | 1 | 0 | 0 | 0 |

Unmatched predictions: {"white": 10, "red": 1, "yellow": 1}

False-positive sources: {"pier": 3, "shore": 9}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 32, "total": 76, "accuracy": 0.42105263157894735}, "unresolvable": {"correct": 6, "total": 36, "accuracy": 0.16666666666666666}}
