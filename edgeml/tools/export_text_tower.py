#!/usr/bin/env python3
"""Export MobileCLIP-S1 text ONNX + CLIP tokenizer for offline directive encoding.

Export-only dependencies: open_clip_torch==2.32.0, torch==2.6.0, onnx==1.17.0,
transformers==4.51.3. Runtime needs only tokenizers and onnxruntime.
Weights retain Apple's research terms; no licensing decision is made here.
"""
import argparse
import hashlib
import json
from pathlib import Path


def export(output):
    import numpy as np
    import open_clip
    import torch
    from transformers import CLIPTokenizerFast
    from edgeml.sidecar.open_vocab import ONNXTextTower

    torch.set_num_threads(4)
    model, _, _ = open_clip.create_model_and_transforms("MobileCLIP-S1", pretrained="datacompdr")
    model.eval()
    tokenizer = open_clip.get_tokenizer("MobileCLIP-S1")
    output.mkdir(parents=True, exist_ok=True)

    class Text(torch.nn.Module):
        def __init__(self):
            super().__init__()
            self.clip = model

        def forward(self, text):
            return self.clip.encode_text(text, normalize=True)

    torch.onnx.export(Text().eval(), tokenizer(["a photo of a life raft"]), str(output / "text_model.onnx"),
                      input_names=["text"], output_names=["embedding"], opset_version=17,
                      dynamic_axes={"text": {0: "batch"}, "embedding": {0: "batch"}}, dynamo=False)
    fast = CLIPTokenizerFast.from_pretrained("openai/clip-vit-base-patch32")
    fast.backend_tokenizer.save(str(output / "tokenizer.json"))
    tower = ONNXTextTower(output)
    samples = ["a photo of a life raft", "a photo of orange life jackets", "oil slick", "a photo of a kayak at sea",
               "a photo of waves and sky", "a navigation buoy", "a sailboat"]
    tokens = np.asarray([e.ids for e in tower.tokenizer.encode_batch(samples)], np.int64)
    np.testing.assert_array_equal(tokens, tokenizer(samples).numpy())
    with torch.no_grad():
        reference = model.encode_text(tokenizer(samples), normalize=True).numpy()
    actual = tower(samples)
    np.testing.assert_allclose(actual, reference, atol=2e-5, rtol=2e-4)
    meta = {"model": "MobileCLIP-S1", "pretrained": "datacompdr", "terms": "Apple research; UNRESOLVED",
            "tokenizer": "openai/clip-vit-base-patch32", "max_abs_error": float(np.max(np.abs(actual - reference))),
            "files": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(output.glob("*")) if p.is_file()}}
    (output / "text_provenance.json").write_text(json.dumps(meta, indent=2) + "\n")
    print(json.dumps(meta, indent=2))


if __name__ == "__main__":
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--output", type=Path, required=True)
    export(ap.parse_args().output)
