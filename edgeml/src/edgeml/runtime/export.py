"""Export detector candidates to ONNX + OpenVINO IR (FP32 / FP16 weights / NNCF INT8).

Layout written under ``--out``::

    <model>-<size>/
      model.onnx            FP32 ONNX (ORT path: portable, any CPU / CoreML EP)
      model_fp16.onnx       FP16-weight ONNX (fp32 I/O) for the portable path
      fp32/model.xml|.bin   OpenVINO IR
      fp16/model.xml|.bin   IR with FP16 weights (OpenVINO upcasts on CPU; measures the size win only)
      int8/model.xml|.bin   NNCF post-training quantization, calibrated on --calib frames (dev split only)
      meta.json             {model, family, input_size, class_names, precision}

Every model goes through the same path so the leaderboard compares like with like.
"""
from __future__ import annotations

import argparse
import json
import shutil
from pathlib import Path

from .detectors import COCO80

YOLO_WORLD_VOCAB = ["boat", "ship", "cargo ship", "tanker", "container ship", "fishing boat", "sailboat", "speedboat",
                    "warship", "submarine", "buoy", "aircraft", "helicopter", "whale", "dolphin", "person"]


def _write_meta(d: Path, **meta):
    (d / "meta.json").write_text(json.dumps(meta, indent=1))


def export_ultralytics(weights: str, size: int, out: Path, vocab: list[str] | None = None) -> Path:
    from ultralytics import YOLO

    m = YOLO(weights)
    names = COCO80 if len(m.names) == 80 else tuple(str(m.names[i]) for i in sorted(m.names))  # fine-tunes keep their own names
    if vocab:
        m.set_classes(vocab)
        names = tuple(vocab)
    p = m.export(format="onnx", imgsz=size, opset=17, simplify=True, dynamic=False, half=False)
    out.mkdir(parents=True, exist_ok=True)
    shutil.move(p, out / "model.onnx")
    _write_meta(out, model=Path(weights).stem, family="yolo", input_size=size, class_names=list(names), precision="fp32")
    return out / "model.onnx"


def export_hf_detr(repo: str, size: int, out: Path, short: str) -> Path:
    import torch
    from transformers import AutoModelForObjectDetection

    model = AutoModelForObjectDetection.from_pretrained(repo).eval()
    id2label = model.config.id2label
    names = [id2label.get(i, id2label.get(str(i), str(i))) for i in range(len(id2label))]

    class Wrapper(torch.nn.Module):
        def __init__(self, m):
            super().__init__()
            self.m = m

        def forward(self, x):
            o = self.m(pixel_values=x)
            return o.logits, o.pred_boxes

    out.mkdir(parents=True, exist_ok=True)
    x = torch.zeros(1, 3, size, size)
    torch.onnx.export(Wrapper(model), x, str(out / "model.onnx"), input_names=["images"], output_names=["logits", "boxes"],
                      opset_version=17, do_constant_folding=True, dynamo=False)
    try:
        import onnx
        import onnxslim

        onnx.save(onnxslim.slim(onnx.load(str(out / "model.onnx"))), str(out / "model.onnx"))
    except Exception as e:  # noqa: BLE001 - slimming is optional
        print("onnxslim skipped:", e)
    _write_meta(out, model=short, family="hf_detr", input_size=size, class_names=names, precision="fp32")
    return out / "model.onnx"


def export_nanodet(onnx_path: str, size: int, out: Path) -> Path:
    if size != 416:
        raise ValueError("OpenCV-zoo NanoDet-Plus is a fixed 416 graph")
    out.mkdir(parents=True, exist_ok=True)
    shutil.copy(onnx_path, out / "model.onnx")
    _write_meta(out, model="nanodet-plus-m-1.5x", family="nanodet", input_size=416, class_names=list(COCO80), precision="fp32")
    return out / "model.onnx"


def to_onnx_fp16(onnx_path: Path) -> Path:
    """FP16-weight ONNX next to the FP32 one (portable: ORT CPU/CoreML upcast as needed). I/O stay fp32."""
    import onnx
    from onnxconverter_common import float16

    out = onnx_path.with_name("model_fp16.onnx")
    if out.exists():
        return out
    m = onnx.load(str(onnx_path))
    m.graph.ClearField("value_info")  # stale fp32 value_info makes ORT reject the converted graph (Resize casts)
    m16 = float16.convert_float_to_float16(m, keep_io_types=True)
    onnx.save(m16, str(out))
    return out


def to_openvino(onnx_path: Path, out: Path, fp16: bool) -> Path:
    import openvino as ov

    model = ov.convert_model(str(onnx_path))
    out.mkdir(parents=True, exist_ok=True)
    ov.save_model(model, str(out / "model.xml"), compress_to_fp16=fp16)
    meta = json.loads((onnx_path.parent / "meta.json").read_text())
    meta["precision"] = "fp16" if fp16 else "fp32"
    (out / "meta.json").write_text(json.dumps(meta, indent=1))
    return out / "model.xml"


def quantize_int8(onnx_path: Path, out: Path, calib_frames: list[Path], n_calib: int = 300, preset: str = "mixed", ignore_head: bool = True) -> Path:
    """NNCF PTQ on the FP32 IR. Calibration frames are dev-split only (never test)."""
    import cv2
    import nncf
    import openvino as ov

    from .detectors import OnnxDetector

    meta = json.loads((onnx_path.parent / "meta.json").read_text())
    # Reuse the adapter's preprocessing so calibration tensors match inference exactly.
    pre = OnnxDetector.__new__(OnnxDetector)
    pre.family, pre.input_size = meta["family"], meta["input_size"]
    frames = calib_frames[:n_calib]

    def transform(p: Path):
        img = cv2.imread(str(p))
        x, _ = OnnxDetector.preprocess(pre, img)
        return x

    core = ov.Core()
    model = core.read_model(str(onnx_path.parent / "fp32" / "model.xml"))
    ds = nncf.Dataset(frames, transform)
    kw = {}
    if meta["family"] == "hf_detr":
        kw["model_type"] = nncf.ModelType.TRANSFORMER
    if meta["family"] == "yolo" and ignore_head:
        # keep the detection head's box/cls regression in FP32 (what ultralytics' own OpenVINO INT8 export does)
        head = [n for n in (op.get_friendly_name() for op in model.get_ops()) if "/model.23/" in n or "/model.22/" in n]
        kw["ignored_scope"] = nncf.IgnoredScope(names=head, types=["Multiply", "Subtract", "Sigmoid"])
    q = nncf.quantize(model, ds, preset=nncf.QuantizationPreset.MIXED if preset == "mixed" else nncf.QuantizationPreset.PERFORMANCE,
                      subset_size=len(frames), fast_bias_correction=True, **kw)
    out.mkdir(parents=True, exist_ok=True)
    ov.save_model(q, str(out / "model.xml"), compress_to_fp16=False)
    meta["precision"] = "int8"
    meta["calibration"] = {"n_frames": len(frames), "preset": preset, "ignore_head": ignore_head, "source": str(frames[0].parent) if frames else None}
    (out / "meta.json").write_text(json.dumps(meta, indent=1))
    return out / "model.xml"


CANDIDATES = {
    "yolo11n": ("ultralytics", "yolo11n.pt"),
    "yolo11s": ("ultralytics", "yolo11s.pt"),
    "yolo26n": ("ultralytics", "yolo26n.pt"),
    "yolov8s-world": ("ultralytics-world", "yolov8s-worldv2.pt"),
    "rtdetr-r18": ("hf", "PekingU/rtdetr_r18vd"),
    "dfine-s": ("hf", "ustc-community/dfine-small-coco"),
    "dfine-n": ("hf", "ustc-community/dfine-nano-coco"),
    "nanodet-plus": ("nanodet", "nanodet-plus-m-1.5x_416.onnx"),
}


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True)
    ap.add_argument("--models", nargs="+", default=list(CANDIDATES))
    ap.add_argument("--sizes", nargs="+", type=int, default=[320, 416, 640])
    ap.add_argument("--calib", help="directory of dev frames for NNCF calibration")
    ap.add_argument("--n-calib", type=int, default=300)
    ap.add_argument("--weights-dir", default=".", help="where .pt / nanodet .onnx files live")
    ap.add_argument("--skip-int8", action="store_true")
    args = ap.parse_args()
    out = Path(args.out)
    calib = sorted(Path(args.calib).glob("*.jpg")) if args.calib else []
    for name in args.models:
        kind, src = CANDIDATES[name]
        for size in args.sizes:
            if kind == "nanodet" and size != 416:
                continue
            d = out / f"{name}-{size}"
            if (d / "model.onnx").exists():
                print("exists", d)
            else:
                print("export", name, size)
                if kind == "ultralytics":
                    export_ultralytics(str(Path(args.weights_dir) / src), size, d)
                elif kind == "ultralytics-world":
                    export_ultralytics(str(Path(args.weights_dir) / src), size, d, vocab=YOLO_WORLD_VOCAB)
                elif kind == "hf":
                    export_hf_detr(src, size, d, short=name)
                else:
                    export_nanodet(str(Path(args.weights_dir) / src), size, d)
            onnx_path = d / "model.onnx"
            if not (d / "fp32" / "model.xml").exists():
                to_openvino(onnx_path, d / "fp32", fp16=False)
            if not (d / "fp16" / "model.xml").exists():
                to_openvino(onnx_path, d / "fp16", fp16=True)
            try:
                to_onnx_fp16(onnx_path)
            except Exception as e:  # noqa: BLE001 - optional artefact
                print(f"  fp16 onnx skipped for {d.name}: {e}")
            if calib and not args.skip_int8 and not (d / "int8" / "model.xml").exists():
                print("quantize", name, size)
                try:
                    quantize_int8(onnx_path, d / "int8", calib, n_calib=args.n_calib)
                except Exception as e:  # noqa: BLE001 - keep exporting other models
                    print("INT8 failed for", name, size, ":", repr(e)[:300])
    print("done")


if __name__ == "__main__":
    main()
