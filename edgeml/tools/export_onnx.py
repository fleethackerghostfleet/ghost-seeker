"""Export zoo detectors to ONNX at a fixed input size for CPU benchmarking (workstream D).

Writes <out>/<name>/model.onnx + export.json (settings, opset, input/output names, source checkpoint, sha256)
and optionally syncs to gs://.../edgeml/models/<name>/.

  python edgeml/tools/export_onnx.py --models yolo11n,yolo26n,dfine-n,yolov8s-world --imgsz 640 \
      --out ~/exports --gcs gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from edgeml.detectors import build  # noqa: E402

OPSET = 17


def sha256(p: Path) -> str:
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def export_ultralytics(det, imgsz: int, dst: Path) -> dict:
    # ultralytics exporter: static shape, fp32, simplified with onnxslim; YOLO-World keeps the prompt classes set on the model
    path = det.model.export(format="onnx", imgsz=imgsz, dynamic=False, half=False, simplify=True, opset=OPSET, nms=False)
    shutil.move(path, dst)
    return {"exporter": "ultralytics.export", "weights": det.weights, "nms_in_graph": False,
            "preprocess": "letterbox to imgsz (pad 114), BGR->RGB, /255, NCHW fp32",
            "postprocess": "ultralytics head output [1, 4+nc, anchors] (xywh+cls); NMS outside the graph",
            "prompts": getattr(det, "prompts", None)}


def _freeze_sincos(model, x):
    """RT-DETR/D-FINE build their encoder position embedding in float64 at every forward; ORT has no float64
    Cos/Sin kernel. With a fixed input size the embedding is a constant, so cache it from one eager forward and
    return the cached tensor while tracing -> it becomes an initializer."""
    import importlib
    import torch

    for modname in ("transformers.models.d_fine.modeling_d_fine", "transformers.models.rt_detr_v2.modeling_rt_detr_v2",
                    "transformers.models.rt_detr.modeling_rt_detr"):
        try:
            mod = importlib.import_module(modname)
        except Exception:  # noqa: BLE001
            continue
        orig = getattr(mod, "build_2d_sincos_position_embedding", None)
        if orig is None or getattr(orig, "_frozen", False):
            continue
        cache = {}

        def patched(*a, _orig=orig, _cache=cache, **k):
            key = (tuple(int(v) if isinstance(v, (int, torch.Tensor)) and not isinstance(v, bool) else str(v) for v in a),
                   tuple(sorted((kk, str(vv)) for kk, vv in k.items())))
            if key not in _cache:
                _cache[key] = _orig(*a, **k).detach().clone()
            return _cache[key]

        patched._frozen = True
        setattr(mod, "build_2d_sincos_position_embedding", patched)
        for cls in vars(mod).values():  # the encoder calls it as a staticmethod on the class
            if isinstance(cls, type) and "build_2d_sincos_position_embedding" in vars(cls):
                setattr(cls, "build_2d_sincos_position_embedding", staticmethod(patched))
    with torch.no_grad():
        model(pixel_values=x)


def export_hf(det, imgsz: int, dst: Path) -> dict:
    import torch

    model = det.model.eval().float().cpu()
    x = torch.randn(1, 3, imgsz, imgsz)
    _freeze_sincos(model, x)
    torch.onnx.export(model, (x,), str(dst), input_names=["pixel_values"], output_names=["logits", "pred_boxes"],
                      opset_version=OPSET, dynamo=False, do_constant_folding=True)
    try:
        import onnxslim

        onnxslim.slim(str(dst), str(dst))
    except Exception as e:  # noqa: BLE001
        print("onnxslim skipped:", e)
    return {"exporter": "torch.onnx.export", "hf_model_id": det.model_id, "nms_in_graph": False,
            "preprocess": f"resize to {imgsz}x{imgsz} (no letterbox), BGR->RGB, /255, ImageNet-free (RT-DETR/D-FINE use raw 0-1), NCHW fp32",
            "postprocess": "logits [1,Q,nc] sigmoid; pred_boxes [1,Q,4] cxcywh normalized; no NMS needed (set-prediction)"}


def export_copy(det, dst: Path) -> dict:
    shutil.copy(det.onnx_path, dst)
    return {"exporter": "upstream onnx (copied)", "source": det.onnx_path, "nms_in_graph": False}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--models", required=True)
    ap.add_argument("--imgsz", type=int, default=640)
    ap.add_argument("--out", default=os.path.expanduser("~/exports"))
    ap.add_argument("--gcs", default=None)
    args = ap.parse_args()
    for name in args.models.split(","):
        try:
            _export_one(name, args)
        except Exception:  # noqa: BLE001
            import traceback

            traceback.print_exc()


def _export_one(name, args):
    if True:
        det = build(name, imgsz=args.imgsz, device="cpu", half=False) if name not in ("yolox-tiny", "nanodet-plus") else build(name)
        d = Path(args.out) / name
        d.mkdir(parents=True, exist_ok=True)
        dst = d / "model.onnx"
        t0 = time.time()
        if hasattr(det, "onnx_path"):
            meta = export_copy(det, dst)
        elif hasattr(det, "model_id"):
            meta = export_hf(det, args.imgsz, dst)
        else:
            meta = export_ultralytics(det, args.imgsz, dst)
        import onnx
        import onnxruntime as ort

        m = onnx.load(str(dst))
        sess = ort.InferenceSession(str(dst), providers=["CPUExecutionProvider"])
        meta.update({
            "name": name, "detector_info": det.info(), "imgsz": det.info().get("imgsz", args.imgsz), "opset": OPSET,
            "inputs": [(i.name, i.shape) for i in sess.get_inputs()], "outputs": [(o.name, o.shape) for o in sess.get_outputs()],
            "ir_version": m.ir_version, "size_mb": round(dst.stat().st_size / 1e6, 2), "sha256": sha256(dst),
            "export_seconds": round(time.time() - t0, 1), "label_map": "COCO-80" if "COCO" in str(det.info().get("training_data")) else "see prompts",
        })
        (d / "export.json").write_text(json.dumps(meta, indent=1, default=str))
        print(name, meta["inputs"], meta["outputs"], meta["size_mb"], "MB")
        if args.gcs:
            subprocess.run(["gcloud", "storage", "rsync", "-r", str(d), f"{args.gcs.rstrip('/')}/{name}"], check=False)


if __name__ == "__main__":
    main()
