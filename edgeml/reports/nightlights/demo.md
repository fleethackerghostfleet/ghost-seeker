# App demo frames

Command: `/home/ubuntu/nightlights-venv/bin/python edgeml/scripts/nightlights_demo.py`

All detections are obtained by reopening the saved JPEGs, not copied from truth. Boxes are 480x300; light coordinates are 960x600 pixels.
Distress frames 05–08 use an externally supplied SOS signal, not automatic Morse recognition; track observations measure flashing across the saved frames.

| Frame | Truth → detected | Bytes |
|---|---|---:|
| imagery/night/night-01.jpg | head_on → head_on (boost 0); port → port (boost 0); starboard → two_mastheads (boost 0) | 19514 |
| imagery/night/night-02.jpg | ram → none (boost 0); nuc → nuc (boost 0.9); constrained_draught → constrained_draught (boost 0.7) | 20875 |
| imagery/night/night-03.jpg | trawling → starboard (boost 0); fishing → fishing (boost 0.1); pilot → two_mastheads (boost 0) | 33423 |
| imagery/night/night-04.jpg | two_mastheads → two_mastheads (boost 0); tow_long → tow_long (boost 0.1) | 63536 |
| imagery/night/night-05.jpg | distress_sos → distress_sos (boost 1) | 35862 |
| imagery/night/night-06.jpg | distress_sos → distress_sos (boost 1) | 42302 |
| imagery/night/night-07.jpg | distress_sos → distress_sos (boost 1) | 35862 |
| imagery/night/night-08.jpg | distress_sos → distress_sos (boost 1) | 42302 |
