"""``Embedder`` implementations backed by open_clip (MobileCLIP, SigLIP, OpenCLIP ViT-B/32).

The image tower can run through torch (for GPU/eval) or through an exported ONNX / OpenVINO graph (laptop). Text
embeddings are computed once with torch and cached, so the laptop never needs the text tower at run time.
"""
from __future__ import annotations

import json
from pathlib import Path

import cv2
import numpy as np

# open_clip (model, pretrained) by short name. Sizes are the image tower's native input.
MODELS = {
    "mobileclip-s0": ("MobileCLIP2-S0", "dfndr2b", 256),  # MobileCLIP2-S0 is the S0-class tower in open_clip
    "mobileclip-s1": ("MobileCLIP-S1", "datacompdr", 256),
    "siglip-b16": ("ViT-B-16-SigLIP", "webli", 224),
    "openclip-b32": ("ViT-B-32", "laion2b_s34b_b79k", 224),
}


class ClipEmbedder:
    dim: int

    def __init__(self, short: str = "mobileclip-s1", backend: str = "torch", onnx_dir: str | Path | None = None,
                 threads: int = 4, precision: str = "fp32"):
        import torch

        self.short = short
        arch, tag, self.size = MODELS[short]
        self.backend = backend
        self.threads = threads
        self.name = f"{short}-{backend}-{precision}"
        self.model = None
        self.tokenizer = None
        self.mean, self.std = None, None
        self.model_bytes = 0
        self._session = None
        if backend == "torch" or onnx_dir is None:
            import open_clip

            self.model, _, pre = open_clip.create_model_and_transforms(arch, pretrained=tag)
            self.model.eval()
            self.tokenizer = open_clip.get_tokenizer(arch)
            norm = next(t for t in pre.transforms if t.__class__.__name__ == "Normalize")
            self.mean, self.std = np.array(norm.mean, np.float32), np.array(norm.std, np.float32)
            self.model_bytes = sum(p.numel() * p.element_size() for p in self.model.visual.parameters())
            with torch.no_grad():
                self.dim = int(self.model.encode_image(torch.zeros(1, 3, self.size, self.size)).shape[-1])
        if backend in ("openvino", "ort") and onnx_dir is not None:
            from ..runtime.backends import open_session

            d = Path(onnx_dir)
            meta = json.loads((d / "meta.json").read_text())
            self.mean, self.std = np.array(meta["mean"], np.float32), np.array(meta["std"], np.float32)
            self.size = meta["input_size"]
            self.dim = meta["dim"]
            path = d / ("model.xml" if backend == "openvino" else "model.onnx")
            self._session = open_session(path, engine=backend, threads=threads, precision=precision)
            self.model_bytes = self._session.model_bytes
            self.text_cache_path = d / "text_cache.json"
        self.last_ms = 0.0

    # ---------------------------------------------------------------- image side
    def preprocess(self, crops: list[np.ndarray]) -> np.ndarray:
        out = np.empty((len(crops), 3, self.size, self.size), np.float32)
        for i, c in enumerate(crops):
            # resize shorter side then centre-crop, like open_clip's eval transform
            h, w = c.shape[:2]
            s = self.size / min(h, w)
            r = cv2.resize(c, (max(self.size, round(w * s)), max(self.size, round(h * s))), interpolation=cv2.INTER_AREA if s < 1 else cv2.INTER_CUBIC)
            y0, x0 = (r.shape[0] - self.size) // 2, (r.shape[1] - self.size) // 2
            r = cv2.cvtColor(r[y0:y0 + self.size, x0:x0 + self.size], cv2.COLOR_BGR2RGB).astype(np.float32) / 255.0
            out[i] = ((r - self.mean) / self.std).transpose(2, 0, 1)
        return out

    def embed_crops(self, crops: list[np.ndarray]) -> np.ndarray:
        import time

        if not crops:
            return np.zeros((0, self.dim), np.float32)
        t0 = time.perf_counter()
        x = self.preprocess(crops)
        if self._session is not None:
            feats = np.concatenate([self._session.run(x[i:i + 1])[0] for i in range(len(x))])
        else:
            import torch

            with torch.no_grad():
                feats = self.model.encode_image(torch.from_numpy(x)).float().numpy()
        feats /= np.linalg.norm(feats, axis=1, keepdims=True) + 1e-8
        self.last_ms = (time.perf_counter() - t0) * 1e3
        return feats.astype(np.float32)

    # ---------------------------------------------------------------- text side
    def embed_text(self, texts: list[str]) -> np.ndarray:
        if self.model is None:
            cache = json.loads(Path(self.text_cache_path).read_text()) if Path(self.text_cache_path).exists() else {}
            missing = [t for t in texts if t not in cache]
            if missing:
                raise RuntimeError(f"text tower not loaded and {len(missing)} prompts missing from cache: {missing[:3]}")
            return np.asarray([cache[t] for t in texts], np.float32)
        import torch

        with torch.no_grad():
            f = self.model.encode_text(self.tokenizer(texts)).float().numpy()
        f /= np.linalg.norm(f, axis=1, keepdims=True) + 1e-8
        return f.astype(np.float32)

    def logit_scale(self) -> float:
        if self.model is None:
            return float(json.loads(Path(self.text_cache_path).read_text()).get("__logit_scale__", 100.0))
        return float(self.model.logit_scale.exp().item())

    # ---------------------------------------------------------------- export
    def export_image_tower(self, out_dir: str | Path, prompts: list[str] | None = None, int8_calib: list[np.ndarray] | None = None) -> Path:
        """Export the image tower to ONNX + OpenVINO IR (fp32/fp16, optional NNCF int8) and cache text embeddings."""
        import openvino as ov
        import torch

        d = Path(out_dir)
        d.mkdir(parents=True, exist_ok=True)

        class V(torch.nn.Module):
            def __init__(s, m):
                super().__init__()
                s.m = m

            def forward(s, x):
                return s.m.encode_image(x)

        torch.onnx.export(V(self.model).eval(), torch.zeros(1, 3, self.size, self.size), str(d / "model.onnx"), input_names=["image"],
                          output_names=["embedding"], opset_version=17, dynamo=False)
        m = ov.convert_model(str(d / "model.onnx"))
        ov.save_model(m, str(d / "model.xml"), compress_to_fp16=False)
        (d / "fp16").mkdir(exist_ok=True)
        ov.save_model(m, str(d / "fp16" / "model.xml"), compress_to_fp16=True)
        meta = {"model": self.short, "input_size": self.size, "dim": self.dim, "mean": self.mean.tolist(), "std": self.std.tolist()}
        (d / "meta.json").write_text(json.dumps(meta))
        (d / "fp16" / "meta.json").write_text(json.dumps(meta))
        if int8_calib:
            import nncf

            ds = nncf.Dataset(int8_calib, lambda c: self.preprocess([c]))
            q = nncf.quantize(m, ds, preset=nncf.QuantizationPreset.MIXED, model_type=nncf.ModelType.TRANSFORMER, subset_size=len(int8_calib))
            (d / "int8").mkdir(exist_ok=True)
            ov.save_model(q, str(d / "int8" / "model.xml"), compress_to_fp16=False)
            (d / "int8" / "meta.json").write_text(json.dumps(meta))
        if prompts:
            cache = {t: v.tolist() for t, v in zip(prompts, self.embed_text(prompts))}
            cache["__logit_scale__"] = self.logit_scale()
            for sub in ("", "fp16", "int8"):
                p = d / sub / "text_cache.json"
                if p.parent.exists():
                    p.write_text(json.dumps(cache))
        return d
