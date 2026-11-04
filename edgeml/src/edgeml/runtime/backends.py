"""Thin, uniform wrappers over ONNX Runtime CPU and OpenVINO CPU sessions.

Both expose ``run(np.ndarray) -> list[np.ndarray]`` plus ``model_bytes`` and ``name`` so the detector /
embedder adapters and the benchmark do not care which engine is underneath.
"""
from __future__ import annotations

import os
from collections.abc import Sequence
from pathlib import Path

import numpy as np


def _dir_or_file_bytes(path: str | Path) -> int:
    p = Path(path)
    if p.is_dir():
        return sum(f.stat().st_size for f in p.rglob("*") if f.is_file())
    total = p.stat().st_size
    sib = p.with_suffix(".bin")  # OpenVINO IR keeps weights next to the .xml
    if p.suffix == ".xml" and sib.exists():
        total += sib.stat().st_size
    return total


class OrtSession:
    engine = "ort"

    def __init__(
        self,
        model_path: str | Path,
        threads: int = 4,
        precision: str = "fp32",
        providers: list[str] | None = None,
    ):
        import onnxruntime as ort

        providers = providers or ort_providers()
        avail = ort.get_available_providers()
        missing = [p for p in providers if p not in avail]
        if missing:
            raise RuntimeError(f"ORT providers {missing} not available in this build; have {avail}")
        so = ort.SessionOptions()
        so.intra_op_num_threads = threads
        so.inter_op_num_threads = 1
        so.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        self.sess = ort.InferenceSession(str(model_path), so, providers=providers)
        self.providers = self.sess.get_providers()
        self.inputs = [i.name for i in self.sess.get_inputs()]
        self.input_shape = self.sess.get_inputs()[0].shape
        self.outputs = [o.name for o in self.sess.get_outputs()]
        self.model_bytes = _dir_or_file_bytes(model_path)
        self.precision = precision
        tag = self.providers[0].replace("ExecutionProvider", "").lower()
        self.name = f"ort-{precision}" if tag == "cpu" else f"ort-{tag}-{precision}"

    def run(self, x: np.ndarray) -> list[np.ndarray]:
        return self.sess.run(self.outputs, {self.inputs[0]: x})


class OvSession:
    engine = "openvino"

    def __init__(self, model_path: str | Path, threads: int = 4, precision: str = "fp32", device: str = "CPU"):
        import openvino as ov

        core = ov.Core()
        model = core.read_model(str(model_path))
        config = {
            "INFERENCE_NUM_THREADS": threads,
            "NUM_STREAMS": 1,
            "PERFORMANCE_HINT": "LATENCY",
            # fp16 "on CPU": OpenVINO CPU plugin upcasts f16 weights and runs bf16 only when
            # INFERENCE_PRECISION_HINT allows it; we ask for the matching precision explicitly.
            "INFERENCE_PRECISION_HINT": {"fp32": "f32", "fp16": "f32", "bf16": "bf16", "int8": "f32"}[precision],
        }
        self.compiled = core.compile_model(model, device, config)
        self.req = self.compiled.create_infer_request()
        self.inputs = [i.get_any_name() for i in self.compiled.inputs]
        self.input_shape = list(self.compiled.inputs[0].get_partial_shape().to_shape()) \
            if self.compiled.inputs[0].get_partial_shape().is_static else None
        self.outputs = list(self.compiled.outputs)
        self.model_bytes = _dir_or_file_bytes(model_path)
        self.precision = precision
        self.name = f"ov-{precision}"

    def run(self, x: np.ndarray) -> list[np.ndarray]:
        self.req.infer({self.inputs[0]: x})
        return [self.req.get_output_tensor(i).data.copy() for i in range(len(self.outputs))]


def ort_providers() -> list[str]:
    """ORT execution providers, most-preferred first.

    Selected by config, not code: env ``EDGEML_ORT_PROVIDERS`` (comma-separated), e.g.
    ``CPUExecutionProvider`` (default, portable), ``CoreMLExecutionProvider,CPUExecutionProvider``
    on Apple Silicon, ``OpenVINOExecutionProvider,CPUExecutionProvider`` with the Intel ORT build.
    """
    raw = os.environ.get("EDGEML_ORT_PROVIDERS", "CPUExecutionProvider")
    return [p.strip() for p in raw.split(",") if p.strip()]


def open_session(
    model_path: str | Path,
    engine: str = "auto",
    threads: int = 4,
    precision: str = "fp32",
    providers: list[str] | None = None,
    device: str = "CPU",
):
    """``engine="ort"`` is the portable path (any CPU, incl. Apple Silicon via CoreML EP); ``"openvino"``
    is the Intel-specific optimisation (needed for the NNCF INT8 IRs). ``"auto"`` picks by file type:
    ``*.onnx`` -> ort, ``*.xml`` -> openvino."""
    if engine == "auto":
        engine = "openvino" if str(model_path).endswith(".xml") else "ort"
    if engine == "ort":
        return OrtSession(model_path, threads=threads, precision=precision, providers=providers)
    if engine == "openvino":
        return OvSession(model_path, threads=threads, precision=precision, device=device)
    raise ValueError(engine)


def rss_mb() -> float:
    try:
        import psutil

        return psutil.Process(os.getpid()).memory_info().rss / 2**20
    except ImportError:
        with open("/proc/self/status") as f:
            for line in f:
                if line.startswith("VmRSS"):
                    return int(line.split()[1]) / 1024
    return float("nan")


def peak_rss_mb() -> float:
    import resource

    return resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024


def letterbox(image_bgr: np.ndarray, size: int, pad_value: int = 114) -> tuple[np.ndarray, float, tuple[int, int]]:
    """Resize keeping aspect ratio into a size x size canvas. Returns (canvas, scale, (pad_x, pad_y))."""
    import cv2

    h, w = image_bgr.shape[:2]
    scale = min(size / h, size / w)
    nw, nh = max(1, round(w * scale)), max(1, round(h * scale))
    resized = cv2.resize(image_bgr, (nw, nh), interpolation=cv2.INTER_LINEAR)
    canvas = np.full((size, size, 3), pad_value, dtype=np.uint8)
    px, py = (size - nw) // 2, (size - nh) // 2
    canvas[py:py + nh, px:px + nw] = resized
    return canvas, scale, (px, py)


def nms_xyxy(boxes: np.ndarray, scores: np.ndarray, iou_thr: float) -> np.ndarray:
    """Class-agnostic greedy NMS on [x1,y1,x2,y2] boxes. Returns kept indices (descending score)."""
    if len(boxes) == 0:
        return np.zeros((0,), dtype=np.int64)
    order = scores.argsort()[::-1]
    x1, y1, x2, y2 = boxes[:, 0], boxes[:, 1], boxes[:, 2], boxes[:, 3]
    areas = np.maximum(0, x2 - x1) * np.maximum(0, y2 - y1)
    keep = []
    while order.size:
        i = order[0]
        keep.append(i)
        xx1 = np.maximum(x1[i], x1[order[1:]])
        yy1 = np.maximum(y1[i], y1[order[1:]])
        xx2 = np.minimum(x2[i], x2[order[1:]])
        yy2 = np.minimum(y2[i], y2[order[1:]])
        inter = np.maximum(0, xx2 - xx1) * np.maximum(0, yy2 - yy1)
        iou = inter / (areas[i] + areas[order[1:]] - inter + 1e-9)
        order = order[1:][iou <= iou_thr]
    return np.asarray(keep, dtype=np.int64)


def as_sequence(x) -> Sequence:
    return x if isinstance(x, (list, tuple)) else [x]
