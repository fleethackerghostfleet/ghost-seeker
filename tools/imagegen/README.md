# Image generation

The app's sensor imagery (`web/static/imagery/`, 8 frames per source) is synthetic and unclassified, generated with `gemini-2.5-flash-image` on Vertex AI (global endpoint) in the hackathon GCP project.

1. `python3 gen.py` - generates one image per source/scene prompt into `~/genimg/raw`.
2. `MODEL=gemini-2.5-flash python3 annotate.py` - crops to 16:10, writes 960x600 JPEGs and labels boxes (480x300 app coordinates) into `library.json`. `gemini-2.5-pro` is the default model but placed top-down (UAV/SAR) boxes poorly; flash with thinking off did better.
3. `python3 sheet.py` - contact sheets with boxes drawn, for review.

Boxes in `internal/catalog/imagery.json` were reviewed and a few were corrected by hand. Requires `gcloud auth login` with the lab account.
