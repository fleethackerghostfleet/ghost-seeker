# App demo frames

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_demo.py`

All detections are obtained by reopening the saved JPEGs, not copied from truth. Boxes are 480x300; light coordinates are 960x600 pixels.
Distress frames 05–08 use an externally supplied SOS signal, not automatic Morse recognition; track observations measure flashing across the saved frames.

| Frame | Truth → detected | Bytes |
|---|---|---:|
| imagery/night/night-01.jpg | head_on → nuc; port → fishing; starboard → two_mastheads | 19514 |
| imagery/night/night-02.jpg | ram → none; nuc → nuc; constrained_draught → constrained_draught | 20875 |
| imagery/night/night-03.jpg | trawling → trawling; fishing → fishing; pilot → tow_long | 33423 |
| imagery/night/night-04.jpg | two_mastheads → two_mastheads; tow_long → tow_long | 63536 |
| imagery/night/night-05.jpg | distress_sos → distress_sos | 35862 |
| imagery/night/night-06.jpg | distress_sos → distress_sos | 42302 |
| imagery/night/night-07.jpg | distress_sos → distress_sos | 35862 |
| imagery/night/night-08.jpg | distress_sos → distress_sos | 42302 |
