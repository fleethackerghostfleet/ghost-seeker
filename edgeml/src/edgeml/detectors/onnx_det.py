"""ONNX Runtime adapters for models whose official releases are ONNX files: YOLOX-tiny (Megvii) and
NanoDet-Plus-m-1.5x (OpenCV zoo export). Both are COCO-trained; only COCO 'boat' (id 8) is maritime."""
from __future__ import annotations

import os
import urllib.request

import numpy as np

from ..types import Detection
from .base import BaseDetector, letterbox, nms_xywh

COCO80 = ["person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat", "traffic light",
          "fire hydrant", "stop sign", "parking meter", "bench", "bird", "cat", "dog", "horse", "sheep", "cow",
          "elephant", "bear", "zebra", "giraffe", "backpack", "umbrella", "handbag", "tie", "suitcase", "frisbee",
          "skis", "snowboard", "sports ball", "kite", "baseball bat", "baseball glove", "skateboard", "surfboard",
          "tennis racket", "bottle", "wine glass", "cup", "fork", "knife", "spoon", "bowl", "banana", "apple",
          "sandwich", "orange", "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair", "couch",
          "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse", "remote", "keyboard",
          "cell phone", "microwave", "oven", "toaster", "sink", "refrigerator", "book", "clock", "vase", "scissors",
          "teddy bear", "hair drier", "toothbrush"]

URLS = {
    "yolox_tiny.onnx": "https://github.com/Megvii-BaseDetection/YOLOX/releases/download/0.1.1rc0/yolox_tiny.onnx",
    "yolox_s.onnx": "https://github.com/Megvii-BaseDetection/YOLOX/releases/download/0.1.1rc0/yolox_s.onnx",
    "nanodet_plus_m15_416.onnx": "https://huggingface.co/opencv/object_detection_nanodet/resolve/main/object_detection_nanodet_2022nov.onnx",
}


def fetch(fname: str, cache_dir: str = os.path.expanduser("~/models")) -> str:
    os.makedirs(cache_dir, exist_ok=True)
    p = os.path.join(cache_dir, fname)
    if not os.path.exists(p):
        urllib.request.urlretrieve(URLS[fname], p)
    return p


def _session(path: str, device: str | None):
    import onnxruntime as ort

    avail = ort.get_available_providers()
    if device and device.startswith("cuda") and "CUDAExecutionProvider" in avail or device is None and "CUDAExecutionProvider" in avail:
        providers = ["CUDAExecutionProvider", "CPUExecutionProvider"]
    else:
        providers = ["CPUExecutionProvider"]
    so = ort.SessionOptions()
    so.log_severity_level = 3
    return ort.InferenceSession(path, so, providers=providers)


class YoloxOnnxDetector(BaseDetector):
    """Official YOLOX ONNX (raw head output [1, N, 85]); decode + NMS here. Input 416 for tiny."""

    def __init__(self, weights: str = "yolox_tiny.onnx", name: str = "yolox-tiny", imgsz: int = 416,
                 device: str | None = None, iou: float = 0.65, **kw):
        super().__init__(**kw)
        self.onnx_path = fetch(weights) if not os.path.exists(weights) else weights
        self.sess = _session(self.onnx_path, device)
        self.inp = self.sess.get_inputs()[0].name
        self.imgsz = imgsz
        self.iou = iou
        self.name = name
        self.params_m, self.gflops = (5.06, 6.45) if "tiny" in weights else (9.0, 26.8)
        strides = [8, 16, 32]
        grids, expanded = [], []
        for s in strides:
            hs = ws = imgsz // s
            yv, xv = np.meshgrid(np.arange(hs), np.arange(ws), indexing="ij")
            grids.append(np.stack((xv, yv), 2).reshape(-1, 2))
            expanded.append(np.full((hs * ws, 1), s))
        self.grids = np.concatenate(grids, 0).astype(np.float32)
        self.strides = np.concatenate(expanded, 0).astype(np.float32)

    def _infer(self, image_bgr: np.ndarray) -> list[Detection]:
        img, r = letterbox(image_bgr, self.imgsz)
        blob = img.transpose(2, 0, 1)[None].astype(np.float32)  # YOLOX >=0.1.1rc0: no normalisation, BGR
        out = self.sess.run(None, {self.inp: blob})[0][0]
        xy = (out[:, :2] + self.grids) * self.strides
        wh = np.exp(out[:, 2:4]) * self.strides
        scores_all = out[:, 4:5] * out[:, 5:]
        cls = scores_all.argmax(1)
        scores = scores_all[np.arange(len(cls)), cls]
        keep = scores >= self.score_thr
        boxes = np.concatenate([xy - wh / 2, wh], 1)[keep] / r
        scores, cls = scores[keep], cls[keep]
        k = nms_xywh(boxes, scores, self.iou)
        return [Detection(box=boxes[i].tolist(), score=float(scores[i]), label=COCO80[int(cls[i])], label_id=int(cls[i]))
                for i in k]


class NanodetOnnxDetector(BaseDetector):
    """NanoDet-Plus-m-1.5x 416 from the OpenCV model zoo (GFL head: per-level cls [1,N,80] + dist [1,N,4*(reg_max+1)])."""

    def __init__(self, weights: str = "nanodet_plus_m15_416.onnx", name: str = "nanodet-plus-m1.5x", imgsz: int = 416,
                 device: str | None = None, iou: float = 0.6, reg_max: int = 7, **kw):
        super().__init__(**kw)
        self.onnx_path = fetch(weights) if not os.path.exists(weights) else weights
        self.sess = _session(self.onnx_path, device)
        self.inp = self.sess.get_inputs()[0].name
        self.imgsz, self.iou, self.reg_max, self.name = imgsz, iou, reg_max, name
        self.params_m, self.gflops = 2.44, 2.97
        self.mean = np.array([103.53, 116.28, 123.675], dtype=np.float32)
        self.std = np.array([57.375, 57.12, 58.395], dtype=np.float32)
        self.project = np.arange(reg_max + 1, dtype=np.float32)
        self.strides = [8, 16, 32, 64]

    def _anchors(self, n: int, stride: int):
        f = int(round(np.sqrt(n)))
        xv, yv = np.meshgrid(np.arange(f) * stride, np.arange(f) * stride)
        return np.stack([xv.ravel() + 0.5 * (stride - 1), yv.ravel() + 0.5 * (stride - 1)], 1).astype(np.float32)

    def _infer(self, image_bgr: np.ndarray) -> list[Detection]:
        img, r = letterbox(image_bgr, self.imgsz, pad_value=0)
        blob = ((img.astype(np.float32) - self.mean) / self.std).transpose(2, 0, 1)[None]
        outs = self.sess.run(None, {self.inp: blob})
        cls_outs = [o[0] for o in outs if o.shape[-1] == 80]
        reg_outs = [o[0] for o in outs if o.shape[-1] == 4 * (self.reg_max + 1)]
        cls_outs.sort(key=lambda a: -a.shape[0])
        reg_outs.sort(key=lambda a: -a.shape[0])
        boxes, scores, labels = [], [], []
        for cs, rg, stride in zip(cls_outs, reg_outs, self.strides):
            anchors = self._anchors(cs.shape[0], stride)
            d = rg.reshape(-1, self.reg_max + 1)
            d = np.exp(d - d.max(1, keepdims=True))
            d = (d / d.sum(1, keepdims=True)) @ self.project
            d = d.reshape(-1, 4) * stride
            x1 = anchors[:, 0] - d[:, 0]
            y1 = anchors[:, 1] - d[:, 1]
            x2 = anchors[:, 0] + d[:, 2]
            y2 = anchors[:, 1] + d[:, 3]
            c = cs.argmax(1)
            s = cs[np.arange(len(c)), c]
            m = s >= self.score_thr
            boxes.append(np.stack([x1, y1, x2 - x1, y2 - y1], 1)[m] / r)
            scores.append(s[m])
            labels.append(c[m])
        boxes, scores, labels = np.concatenate(boxes), np.concatenate(scores), np.concatenate(labels)
        k = nms_xywh(boxes, scores, self.iou)
        return [Detection(box=boxes[i].tolist(), score=float(scores[i]), label=COCO80[int(labels[i])], label_id=int(labels[i]))
                for i in k]
