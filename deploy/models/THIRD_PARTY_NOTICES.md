# Packaged model notices

Default image models originate from the separately pinned `demo-data-v1`
release (`models-D.tar.gz`) of fleethackerghostfleet/ghost-seeker.

* **Ultralytics YOLO11n**: stock COCO-pretrained ONNX export. AGPL-3.0 versus
  Enterprise licensing remains **UNRESOLVED**. https://www.ultralytics.com/license
* **Apple MobileCLIP-S1**: Apple ML Research terms remain **UNRESOLVED**.
  https://github.com/apple-aiml-research/ml-mobileclip#license
* **Optional offline text tower**: `text_model.onnx` in the new `models-text-v1`
  release is derived from the same MobileCLIP-S1 `datacompdr` weights, exported
  with open_clip 2.32.0 / torch 2.6.0 / ONNX opset 17. It retains the same Apple
  research terms; licensing remains **UNRESOLVED**. `tokenizer.json` is the CLIP
  tokenizer from `openai/clip-vit-base-patch32`. Export validation and hashes are
  included in that release's `text_provenance.json`; pinned runtime hashes are
  in `TEXT_SHA256SUMS`. `demo-data-v1` is unchanged.

Packaging is not legal approval to field these models. No licensing decision
is made here.
