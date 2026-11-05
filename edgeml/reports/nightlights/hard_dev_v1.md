# Night lights: hard/dev

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier hard --split dev`

Synthetic benchmark only; not operational validation.

- light_precision: 0.0920
- light_recall: 0.2393
- rule_accuracy: 0.1607
- automatic_rule_accuracy: 0.0408
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.6161
- flashing_accuracy: 0.3006
- night_gate_accuracy: 0.9706
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 0 / 7 | 0.0000 |
| constrained_draught | 0 / 7 | 0.0000 |
| nuc | 1 / 7 | 0.1429 |
| trawling | 1 / 7 | 0.1429 |
| fishing | 1 / 7 | 0.1429 |
| pilot | 0 / 7 | 0.0000 |
| tow_long | 1 / 7 | 0.1429 |
| two_mastheads | 0 / 7 | 0.0000 |
| head_on | 0 / 7 | 0.0000 |
| port | 0 / 7 | 0.0000 |
| starboard | 0 / 7 | 0.0000 |
| white_ambiguous | 0 / 7 | 0.0000 |
| air_cushion | 0 / 7 | 0.0000 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | miss |
|---|---:|---:|---:|---:|---:|---:|
| red | 9 | 0 | 2 | 6 | 0 | 52 |
| green | 0 | 1 | 0 | 0 | 0 | 15 |
| white | 0 | 0 | 24 | 2 | 0 | 42 |
| yellow | 0 | 0 | 1 | 2 | 0 | 1 |
| blue | 0 | 0 | 0 | 0 | 3 | 3 |
| background | 1 | 0 | 265 | 108 | 0 | 0 |

Unmatched predictions: {"white": 265, "yellow": 108, "red": 1}

False-positive sources: {"shore": 157, "pier": 217}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
