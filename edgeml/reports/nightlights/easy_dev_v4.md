# Night lights: easy/dev

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --split dev --tier easy --version v4`

Synthetic benchmark only; not operational validation.

- light_precision: 1.0000
- light_recall: 1.0000
- rule_accuracy: 1.0000
- automatic_rule_accuracy: 1.0000
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 1.0000
- flashing_accuracy: 1.0000
- night_gate_accuracy: 1.0000
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 7 / 7 | 1.0000 |
| constrained_draught | 7 / 7 | 1.0000 |
| nuc | 7 / 7 | 1.0000 |
| trawling | 7 / 7 | 1.0000 |
| fishing | 7 / 7 | 1.0000 |
| pilot | 7 / 7 | 1.0000 |
| tow_long | 7 / 7 | 1.0000 |
| two_mastheads | 7 / 7 | 1.0000 |
| head_on | 7 / 7 | 1.0000 |
| port | 7 / 7 | 1.0000 |
| starboard | 7 / 7 | 1.0000 |
| white_ambiguous | 7 / 7 | 1.0000 |
| air_cushion | 7 / 7 | 1.0000 |
| law_enforcement | 7 / 7 | 1.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | ambiguous_orange | miss |
|---|---:|---:|---:|---:|---:|---:|---:|
| red | 84 | 0 | 0 | 0 | 0 | 0 | 0 |
| green | 0 | 21 | 0 | 0 | 0 | 0 | 0 |
| white | 0 | 0 | 77 | 0 | 0 | 0 | 0 |
| yellow | 0 | 0 | 0 | 7 | 0 | 0 | 0 |
| blue | 0 | 0 | 0 | 0 | 7 | 0 | 0 |
| ambiguous_orange | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| background | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

Unmatched predictions: {}

False-positive sources: {}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.

Observability strata (all remain in overall scores): {"resolvable": {"correct": 112, "total": 112, "accuracy": 1.0}, "unresolvable": {"correct": 0, "total": 0, "accuracy": 0.0}}
