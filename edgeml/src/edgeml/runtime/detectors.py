"""Model-agnostic detector adapters that run exported ONNX / OpenVINO graphs on CPU.

Three output families cover every candidate in workstream D:
  * ``yolo``     ultralytics export, output (1, 4+nc, N) with xywh in letterboxed input pixels
                 (YOLO11n/s, YOLO-World-S with a baked vocabulary)
  * ``hf_detr``  HF transformers RT-DETR / D-FINE export: logits (1, Q, nc) + boxes (1, Q, 4) normalised cxcywh
  * ``nanodet``  OpenCV-zoo NanoDet-Plus: per-level class scores (1, A, 80) + GFL distributions (1, A, 32)
Every adapter implements ``edgeml.interfaces.Detector`` and returns pixel [x, y, w, h] boxes in the
original image frame. Labels are the model's native names, with ``vessel_labels`` marking which ones
count as a vessel for the class-agnostic metrics.
"""
from __future__ import annotations

import json
from pathlib import Path

import cv2
import numpy as np

from ..types import Detection
from .backends import letterbox, nms_xyxy, open_session

COCO80 = (
    "person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat", "traffic light",
    "fire hydrant", "stop sign", "parking meter", "bench", "bird", "cat", "dog", "horse", "sheep", "cow",
    "elephant", "bear", "zebra", "giraffe", "backpack", "umbrella", "handbag", "tie", "suitcase", "frisbee",
    "skis", "snowboard", "sports ball", "kite", "baseball bat", "baseball glove", "skateboard", "surfboard",
    "tennis racket", "bottle", "wine glass", "cup", "fork", "knife", "spoon", "bowl", "banana", "apple",
    "sandwich", "orange", "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair", "couch",
    "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse", "remote", "keyboard",
    "cell phone", "microwave", "oven", "toaster", "sink", "refrigerator", "book", "clock", "vase", "scissors",
    "teddy bear", "hair drier", "toothbrush",
)
# COCO classes a maritime watch-stander cares about (everything else is reported but not a "vessel").
MARITIME_KEEP = {"boat", "airplane", "bird", "person", "surfboard", "kite"}
VESSEL_LABELS = {"boat", "ship", "vessel", "sailboat", "submarine", "warship", "buoy", "fishing boat", "speedboat",
                 "cargo ship", "tanker", "container ship", "yacht", "ferry", "kayak"}


class OnnxDetector:
    """One class, three post-processors. Construct via :func:`load_detector`."""

    def __init__(self, model_path: str | Path, family: str, input_size: int, engine: str = "auto",
                 threads: int = 4, precision: str = "fp32", class_names: tuple[str, ...] = COCO80,
                 score_thr: float = 0.25, iou_thr: float = 0.6, keep_labels: set[str] | None = MARITIME_KEEP,
                 max_det: int = 100, name: str | None = None):
        self.session = open_session(model_path, engine=engine, threads=threads, precision=precision)
        self.family = family
        self.input_size = int(input_size)
        self.class_names = tuple(class_names)
        self.score_thr = score_thr
        self.iou_thr = iou_thr
        self.keep_labels = keep_labels
        self.max_det = max_det
        self.name = name or f"{Path(model_path).stem}-{self.session.name}-{input_size}"
        self.model_bytes = self.session.model_bytes
        self.last_timing_ms: dict[str, float] = {}
        if family == "nanodet":
            self._nanodet_anchors()

    # ---------------------------------------------------------------- preprocessing
    def preprocess(self, image_bgr: np.ndarray) -> tuple[np.ndarray, dict]:
        s = self.input_size
        if self.family == "yolo":
            canvas, scale, (px, py) = letterbox(image_bgr, s)
            x = cv2.cvtColor(canvas, cv2.COLOR_BGR2RGB).astype(np.float32) / 255.0
            meta = {"scale": scale, "pad": (px, py)}
        elif self.family == "hf_detr":
            h, w = image_bgr.shape[:2]
            x = cv2.resize(image_bgr, (s, s), interpolation=cv2.INTER_LINEAR)
            x = cv2.cvtColor(x, cv2.COLOR_BGR2RGB).astype(np.float32) / 255.0
            meta = {"w": w, "h": h}
        elif self.family == "nanodet":
            canvas, scale, (px, py) = letterbox(image_bgr, s, pad_value=0)
            mean = np.array([103.53, 116.28, 123.675], dtype=np.float32)
            std = np.array([57.375, 57.12, 58.395], dtype=np.float32)
            x = (canvas.astype(np.float32) - mean) / std  # NanoDet consumes BGR
            meta = {"scale": scale, "pad": (px, py)}
        else:
            raise ValueError(self.family)
        x = np.ascontiguousarray(x.transpose(2, 0, 1)[None])
        return x, meta

    # ---------------------------------------------------------------- postprocessing
    def postprocess(self, outs: list[np.ndarray], meta: dict, image_shape: tuple[int, int]) -> list[Detection]:
        h, w = image_shape
        if self.family == "yolo":
            pred = outs[0][0]
            if pred.ndim == 2 and pred.shape[1] == 6:  # end-to-end (NMS-free) export, e.g. YOLO26: (N, [x1,y1,x2,y2,conf,cls])
                m = pred[:, 4] >= self.score_thr
                xyxy, conf, cls = pred[m, :4], pred[m, 4], pred[m, 5].astype(int)
                order = np.argsort(-conf)[: self.max_det]
                xyxy, conf, cls = xyxy[order], conf[order], cls[order]
            else:  # (4+nc, N) raw head
                if pred.shape[0] < pred.shape[1]:
                    pred = pred.T
                boxes_cxcywh, scores = pred[:, :4], pred[:, 4:]
                cls = scores.argmax(1)
                conf = scores[np.arange(len(cls)), cls]
                m = conf >= self.score_thr
                boxes_cxcywh, cls, conf = boxes_cxcywh[m], cls[m], conf[m]
                xyxy = np.column_stack([boxes_cxcywh[:, 0] - boxes_cxcywh[:, 2] / 2, boxes_cxcywh[:, 1] - boxes_cxcywh[:, 3] / 2,
                                        boxes_cxcywh[:, 0] + boxes_cxcywh[:, 2] / 2, boxes_cxcywh[:, 1] + boxes_cxcywh[:, 3] / 2])
                keep = nms_xyxy(xyxy, conf, self.iou_thr)[: self.max_det]
                xyxy, cls, conf = xyxy[keep], cls[keep], conf[keep]
            px, py = meta["pad"]
            xyxy = (xyxy - [px, py, px, py]) / meta["scale"]
        elif self.family == "hf_detr":
            logits, boxes = outs[0][0], outs[1][0]
            if logits.shape[-1] == 4 and boxes.shape[-1] != 4:
                logits, boxes = boxes, logits
            probs = 1.0 / (1.0 + np.exp(-logits))  # DETR-style sigmoid focal head
            cls = probs.argmax(1)
            conf = probs[np.arange(len(cls)), cls]
            m = conf >= self.score_thr
            boxes, cls, conf = boxes[m], cls[m], conf[m]
            cx, cy, bw, bh = boxes.T
            xyxy = np.column_stack([(cx - bw / 2) * w, (cy - bh / 2) * h, (cx + bw / 2) * w, (cy + bh / 2) * h])
            order = conf.argsort()[::-1][: self.max_det]
            xyxy, cls, conf = xyxy[order], cls[order], conf[order]
        elif self.family == "nanodet":
            xyxy, cls, conf = self._nanodet_decode(outs)
            keep = nms_xyxy(xyxy, conf, self.iou_thr)[: self.max_det]
            xyxy, cls, conf = xyxy[keep], cls[keep], conf[keep]
            px, py = meta["pad"]
            xyxy = (xyxy - [px, py, px, py]) / meta["scale"]
        else:
            raise ValueError(self.family)
        dets = []
        for (x1, y1, x2, y2), c, s in zip(xyxy, cls, conf):
            label = self.class_names[int(c)] if int(c) < len(self.class_names) else str(int(c))
            if self.keep_labels is not None and label not in self.keep_labels:
                continue
            x1, y1 = max(0.0, float(x1)), max(0.0, float(y1))
            x2, y2 = min(float(w), float(x2)), min(float(h), float(y2))
            if x2 - x1 < 1 or y2 - y1 < 1:
                continue
            dets.append(Detection(box=[x1, y1, x2 - x1, y2 - y1], score=float(s), label=label, label_id=int(c)))
        return dets

    def _nanodet_anchors(self):
        self.strides = (8, 16, 32, 64)
        self.reg_max = 7
        self.project = np.arange(self.reg_max + 1, dtype=np.float32)
        anchors = []
        for st in self.strides:
            n = self.input_size // st
            xv, yv = np.meshgrid(np.arange(n) * st, np.arange(n) * st)
            anchors.append(np.column_stack([xv.ravel() + 0.5 * (st - 1), yv.ravel() + 0.5 * (st - 1)]).astype(np.float32))
        self.anchors = anchors

    def _nanodet_decode(self, outs):
        scores = {o.shape[-2]: o[0] for o in outs if o.shape[-1] == 80}
        dists = {o.shape[-2]: o[0] for o in outs if o.shape[-1] == 32}
        xyxy_all, cls_all, conf_all = [], [], []
        for st, anc in zip(self.strides, self.anchors):
            n = len(anc)
            if n not in scores:
                continue
            sc, dist = scores[n], dists[n]
            cls = sc.argmax(1)
            conf = sc[np.arange(n), cls]
            m = conf >= self.score_thr
            if not m.any():
                continue
            logits = dist[m].reshape(-1, self.reg_max + 1)
            e = np.exp(logits - logits.max(1, keepdims=True))
            d = (e / e.sum(1, keepdims=True)) @ self.project
            d = d.reshape(-1, 4) * st
            a = anc[m]
            xyxy_all.append(np.column_stack([a[:, 0] - d[:, 0], a[:, 1] - d[:, 1], a[:, 0] + d[:, 2], a[:, 1] + d[:, 3]]))
            cls_all.append(cls[m])
            conf_all.append(conf[m])
        if not xyxy_all:
            return np.zeros((0, 4), np.float32), np.zeros((0,), np.int64), np.zeros((0,), np.float32)
        return np.concatenate(xyxy_all), np.concatenate(cls_all), np.concatenate(conf_all)

    # ---------------------------------------------------------------- Detector protocol
    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        import time

        t0 = time.perf_counter()
        x, meta = self.preprocess(image_bgr)
        t1 = time.perf_counter()
        outs = self.session.run(x)
        t2 = time.perf_counter()
        dets = self.postprocess(outs, meta, image_bgr.shape[:2])
        t3 = time.perf_counter()
        self.last_timing_ms = {"pre": (t1 - t0) * 1e3, "infer": (t2 - t1) * 1e3, "post": (t3 - t2) * 1e3}
        return dets


def _meta_from_ws_b_export(exp: dict) -> dict:
    """Adapt workstream B's ``export.json`` sidecar (gs://.../edgeml/models/<name>/) to our ``meta.json`` schema."""
    name = exp.get("name", "")
    exporter = exp.get("exporter", "")
    if "yolo" in name or "ultralytics" in exporter:
        family = "yolo"
    elif "nanodet" in name:
        family = "nanodet"
    else:
        family = "hf_detr"  # rt-detr / d-fine: logits + pred_boxes (normalised cxcywh)
    return {"model": name, "family": family, "input_size": int(exp.get("imgsz", 640)), "precision": "fp32",
            "class_names": list(COCO80) if exp.get("label_map", "COCO-80") == "COCO-80" else None}


def load_detector(model_path: str | Path, engine: str = "auto", threads: int = 4, precision: str | None = None,
                  **kw) -> OnnxDetector:
    """Build an adapter from the ``<model>.meta.json`` sidecar written by :mod:`edgeml_research.runtime.export`."""
    model_path = Path(model_path)
    meta_path = model_path.with_suffix("").with_suffix(".meta.json") if model_path.suffix else model_path / "meta.json"
    if not meta_path.exists():
        meta_path = model_path.parent / "meta.json"
    if meta_path.exists():
        meta = json.loads(meta_path.read_text())
    elif (model_path.parent / "export.json").exists():
        meta = _meta_from_ws_b_export(json.loads((model_path.parent / "export.json").read_text()))
    else:
        raise FileNotFoundError(f"no meta.json / export.json next to {model_path}")
    precision = precision or meta.get("precision", "fp32")
    if engine == "auto":
        engine = "openvino" if model_path.suffix == ".xml" else "ort"
    return OnnxDetector(model_path, family=meta["family"], input_size=meta["input_size"], engine=engine, threads=threads,
                        precision=precision, class_names=tuple(meta.get("class_names") or COCO80),
                        name=f"{meta['model']}-{precision}-{meta['input_size']}-{engine}", **kw)
