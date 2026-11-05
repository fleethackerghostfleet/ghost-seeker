"""Workstream G: export fine-tuned YOLO11 weights exactly like D did (ONNX -> OpenVINO FP32 -> NNCF INT8).

  python edgeml/tools/g_export.py --weights ~/runs/g/yolo11n-640/weights/best.pt --name yolo11n-ft-640 \
      --size 640 --calib ~/data/g_calib --out ~/models/G
Calibration frames must be DEV frames (g_build_dataset.py train/val images), never test.
"""
from __future__ import annotations

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.runtime.export import export_ultralytics, quantize_int8, to_openvino  # noqa: E402


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--weights", required=True)
    ap.add_argument("--name", required=True, help="artifact dir name, e.g. yolo11n-ft-640")
    ap.add_argument("--size", type=int, required=True)
    ap.add_argument("--calib", required=True, help="dir of dev .jpg frames for NNCF calibration")
    ap.add_argument("--n-calib", type=int, default=300)
    ap.add_argument("--out", default="~/models/G")
    a = ap.parse_args()
    d = Path(a.out).expanduser() / a.name
    if not (d / "model.onnx").exists():
        export_ultralytics(a.weights, a.size, d)
    if not (d / "fp32" / "model.xml").exists():
        to_openvino(d / "model.onnx", d / "fp32", fp16=False)
    calib = sorted(Path(a.calib).expanduser().glob("*.jpg"))
    if not (d / "int8" / "model.xml").exists():
        quantize_int8(d / "model.onnx", d / "int8", calib, n_calib=a.n_calib)
    print("exported", d)


if __name__ == "__main__":
    main()
