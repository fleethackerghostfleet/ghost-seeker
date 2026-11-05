# Night lights: hard/test

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_eval.py --data data/nightlights --tier hard --split test`

Synthetic benchmark only; not operational validation.

- light_precision: 0.0872
- light_recall: 0.2407
- rule_accuracy: 0.1875
- automatic_rule_accuracy: 0.0714
- externally_identified_distress_accuracy: 1.0000
- aspect_accuracy: 0.6786
- flashing_accuracy: 0.3457
- night_gate_accuracy: 0.9632
- day_false_night_rate: 0.0000

| Rule | Correct / total | Accuracy |
|---|---:|---:|
| ram | 0 / 7 | 0.0000 |
| constrained_draught | 0 / 7 | 0.0000 |
| nuc | 0 / 7 | 0.0000 |
| trawling | 1 / 7 | 0.1429 |
| fishing | 0 / 7 | 0.0000 |
| pilot | 2 / 7 | 0.2857 |
| tow_long | 2 / 7 | 0.2857 |
| two_mastheads | 0 / 7 | 0.0000 |
| head_on | 0 / 7 | 0.0000 |
| port | 1 / 7 | 0.1429 |
| starboard | 1 / 7 | 0.1429 |
| white_ambiguous | 0 / 7 | 0.0000 |
| air_cushion | 0 / 7 | 0.0000 |
| law_enforcement | 0 / 7 | 0.0000 |
| distress_flare | 7 / 7 | 1.0000 |
| distress_sos | 7 / 7 | 1.0000 |

## Colour confusion (position-matched)

| GT \ predicted | red | green | white | yellow | blue | miss |
|---|---:|---:|---:|---:|---:|---:|
| red | 7 | 0 | 6 | 9 | 0 | 46 |
| green | 0 | 4 | 1 | 0 | 0 | 10 |
| white | 0 | 0 | 27 | 1 | 0 | 40 |
| yellow | 0 | 0 | 0 | 0 | 0 | 5 |
| blue | 0 | 0 | 0 | 0 | 1 | 5 |
| background | 0 | 0 | 277 | 114 | 0 | 0 |

Unmatched predictions: {"white": 277, "yellow": 114}

False-positive sources: {"pier": 248, "shore": 143}

Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning.
