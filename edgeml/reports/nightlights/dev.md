# Night lights: dev

Command: `edgeml/scripts/nightlights_eval.py --data data/nightlights --split dev`

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

| GT \ predicted | red | green | white | yellow | blue | miss |
|---|---:|---:|---:|---:|---:|---:|
| red | 84 | 0 | 0 | 0 | 0 | 0 |
| green | 0 | 21 | 0 | 0 | 0 | 0 |
| white | 0 | 0 | 77 | 0 | 0 | 0 |
| yellow | 0 | 0 | 0 | 7 | 0 | 0 |
| blue | 0 | 0 | 0 | 0 | 7 | 0 |

Unmatched predictions: {}

Procedural labels, not real-world accuracy; four-frame static tracks.
Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.
Flashing accuracy denominator includes missed GT lights as incorrect; colour P/R require position match within 6 pixels.
Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.
Dev seeds 1000..2506, test seeds 100000..101506, plus separate day seeds; no test threshold tuning.
