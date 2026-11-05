"""Export exact MobileCLIP-S1 image/text towers and a torch-free tokenizer.

Run in the export environment, never the lightweight runtime. FP32 parity and
INT8 drift are measured before choosing the text graph. Output checksums pin all
runtime assets; --expected-weights-sha256 pins the source state when reproducing.
"""
from __future__ import annotations
import argparse
import hashlib
import importlib.metadata
import json
import time
from pathlib import Path
import numpy as np
from .engine import normalize, sha256
from .tokenizer import Tokenizer
from edgeml.embed.zeroshot import SUBTYPES, UNKNOWN_PROMPTS

PARITY_PROMPTS = ["rescue boat", "a photo of a rescue boat", "orange inflatable life raft", "red cargo ship", "blue fishing boat",
                  "floating debris", "empty sea", "coast guard vessel", "ship's deck", "A boat &amp; its wake.",
                  "  Rescue\nboat  ", "a ship marked 123", "café boat", "<start_of_text>"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    parser.add_argument("--cache-dir")
    parser.add_argument("--checkpoint", help="local official OpenCLIP checkpoint; otherwise fetch datacompdr")
    parser.add_argument("--expected-weights-sha256", default="497a114a99bfedd5e3f5bbea2bf29288770eb6bc9262957f5ac6e3f368b7a4b7")
    parser.add_argument("--skip-image", action="store_true")
    parser.add_argument("--threads", type=int, default=2)
    args = parser.parse_args()
    out = Path(args.output)
    if (out / "manifest.json").exists():
        parser.error("output already contains an export; use a new directory")
    out.mkdir(parents=True, exist_ok=True)
    import torch
    import open_clip
    import onnxruntime as ort
    from onnxruntime.quantization import quantize_dynamic, QuantType
    torch.set_num_threads(args.threads)
    started = time.perf_counter()
    model, _, preprocess = open_clip.create_model_and_transforms("MobileCLIP-S1", pretrained=args.checkpoint or "datacompdr", cache_dir=args.cache_dir)
    model.eval()
    tokenizer = open_clip.get_tokenizer("MobileCLIP-S1")
    digest = hashlib.sha256()
    for name, value in sorted(model.state_dict().items()):
        digest.update(name.encode())
        digest.update(str(tuple(value.shape)).encode())
        digest.update(value.detach().cpu().contiguous().numpy().tobytes())
    weights_sha = digest.hexdigest()
    if args.expected_weights_sha256 and weights_sha != args.expected_weights_sha256:
        raise ValueError("source model weights do not match the pinned identity")
    model_id = f"mobileclip-s1:datacompdr:{weights_sha}"
    tokens = {"schemaVersion": 1, "clean": "lower", "contextLength": tokenizer.context_length,
              "sotToken": tokenizer.sot_token_id, "eotToken": tokenizer.eot_token_id,
              "encoder": tokenizer.encoder, "merges": [list(pair) for pair, rank in sorted(tokenizer.bpe_ranks.items(), key=lambda p: p[1])]}
    (out / "tokenizer.json").write_text(json.dumps(tokens, ensure_ascii=False))
    runtime_tokenizer = Tokenizer(out / "tokenizer.json")
    prompts = list(dict.fromkeys([p for group in SUBTYPES.values() for p in group] + UNKNOWN_PROMPTS + PARITY_PROMPTS))
    expected_tokens = tokenizer(prompts).numpy()
    actual_tokens = runtime_tokenizer(prompts)
    if not np.array_equal(expected_tokens, actual_tokens):
        raise ValueError("exported tokenizer does not match original OpenCLIP")

    class TextTower(torch.nn.Module):
        def __init__(self, original):
            super().__init__()
            self.original = original
        def forward(self, tokens):
            return self.original.encode_text(tokens, normalize=True)

    with torch.no_grad():
        reference = model.encode_text(torch.from_numpy(expected_tokens), normalize=True).float().numpy()
    torch.onnx.export(TextTower(model).eval(), torch.from_numpy(expected_tokens[:1]), str(out / "text.fp32.onnx"),
                      input_names=["tokens"], output_names=["embedding"], opset_version=17, dynamo=False)
    options = ort.SessionOptions()
    options.intra_op_num_threads, options.inter_op_num_threads = args.threads, 1
    def run_text(graph):
        session = ort.InferenceSession(str(graph), options, providers=["CPUExecutionProvider"])
        result = normalize([session.run(None, {"tokens": row[None]})[0][0] for row in expected_tokens])
        return result
    fp32 = run_text(out / "text.fp32.onnx")
    fp_cos = (reference * fp32).sum(1)
    if float(fp_cos.min()) < 0.9999:
        raise ValueError(f"FP32 export parity failed: min cosine {fp_cos.min()}")
    quantize_dynamic(str(out / "text.fp32.onnx"), str(out / "text.int8.onnx"), weight_type=QuantType.QInt8,
                     op_types_to_quantize=["MatMul", "Gemm"], per_channel=True)
    int8 = run_text(out / "text.int8.onnx")
    int8_cos = (reference * int8).sum(1)
    use_int8 = float(int8_cos.min()) >= 0.98 and float(int8_cos.mean()) >= 0.995
    text_graph = "text.int8.onnx" if use_int8 else "text.fp32.onnx"
    selected = int8 if use_int8 else fp32
    base = {"modelId": model_id, "prompts": {p: v.tolist() for p, v in zip(prompts, selected)}}
    (out / "base_prototypes.json").write_text(json.dumps(base))
    norm = next(t for t in preprocess.transforms if t.__class__.__name__ == "Normalize")
    resize = next(t for t in preprocess.transforms if t.__class__.__name__ == "Resize")
    resize_mode = str(resize.interpolation).split(".")[-1].lower()
    meta = {"size": 256, "mean": list(norm.mean), "std": list(norm.std), "interpolation": resize_mode}
    report = {"tokenizerExact": True, "tokenizerPrompts": len(prompts), "fp32MinCosine": float(fp_cos.min()),
              "int8MinCosine": float(int8_cos.min()), "int8MeanCosine": float(int8_cos.mean()),
              "int8Selected": use_int8, "thresholds": {"minCosine": 0.98, "meanCosine": 0.995},
              "qualityScope": "embedding numerical parity only; per-concept label quality needs held-out crops"}
    files = ["text.fp32.onnx", "text.int8.onnx", "tokenizer.json", "base_prototypes.json"]
    if not args.skip_image:
        class ImageTower(torch.nn.Module):
            def __init__(self, original):
                super().__init__()
                self.original = original
            def forward(self, image):
                return self.original.encode_image(image, normalize=True)
        torch.manual_seed(41)
        image_input = torch.rand(1, 3, 256, 256)
        with torch.no_grad():
            image_reference = model.encode_image(image_input, normalize=True).numpy()
        torch.onnx.export(ImageTower(model).eval(), image_input, str(out / "image.onnx"), input_names=["image"],
                          output_names=["embedding"], opset_version=17, dynamo=False)
        session = ort.InferenceSession(str(out / "image.onnx"), options, providers=["CPUExecutionProvider"])
        image_actual = normalize(session.run(None, {"image": image_input.numpy()})[0])
        image_cos = float((image_reference * image_actual).sum())
        if image_cos < 0.9999:
            raise ValueError(f"image export parity failed: {image_cos}")
        report["imageMinCosine"] = image_cos
        files.append("image.onnx")
    manifest = {"schemaVersion": 1, "architecture": "MobileCLIP-S1", "pretrained": "datacompdr", "modelId": model_id,
                "weightsSha256": weights_sha, "sourceRepository": "apple/MobileCLIP-S1-OpenCLIP", "dimension": int(reference.shape[1]),
                "textGraph": text_graph, "imageGraph": None if args.skip_image else "image.onnx", "imagePreprocess": meta,
                "logitScale": float(model.logit_scale.exp().item()), "sha256": {name: sha256(out / name) for name in files},
                "versions": {name: importlib.metadata.version(name) for name in ["torch", "torchvision", "open_clip_torch", "onnx", "onnxruntime", "numpy"]}}
    report["elapsedSeconds"] = time.perf_counter() - started
    report["assetBytes"] = {name: (out / name).stat().st_size for name in files}
    (out / "validation.json").write_text(json.dumps(report, indent=2) + "\n")
    (out / "manifest.json.tmp").write_text(json.dumps(manifest, indent=2) + "\n")
    (out / "manifest.json.tmp").replace(out / "manifest.json")
    print(json.dumps({"modelId": model_id, "output": str(out), "validation": report}, indent=2))


if __name__ == "__main__":
    main()
