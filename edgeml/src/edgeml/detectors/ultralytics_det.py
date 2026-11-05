"""Ultralytics-backed detector (YOLO11 / RT-DETR / ONNX export) behind the Detector protocol.

Only a maritime-relevant subset of COCO is kept by default and every kept class is reported with
its COCO name; label_id is the COCO index so later stages can remap. Boxes are pixel [x,y,w,h].
"""
from __future__ import annotations

from typing import Iterable, Optional

import numpy as np

from ..types import Detection, xyxy_to_xywh

# COCO ids that can plausibly be a surface contact or something a watch-stander reports.
MARITIME_COCO = {8: "boat", 4: "airplane", 14: "bird", 0: "person", 33: "kite", 37: "surfboard"}


class UltralyticsDetector:
    def __init__(
        self,
        weights: str = "yolo11s.pt",
        imgsz: int = 1280,
        conf: float = 0.05,
        iou: float = 0.6,
        device: Optional[str] = None,
        keep: Optional[Iterable[int]] = (8,),
        half: bool = False,
        max_det: int = 100,
    ):
        from ultralytics import YOLO, RTDETR

        w = weights if weights.endswith((".pt", ".onnx", ".engine")) else weights + ".pt"
        self.model = RTDETR(w) if "rtdetr" in w.lower() else YOLO(w)
        self.name = w.rsplit("/", 1)[-1].rsplit(".", 1)[0] + f"@{imgsz}"
        self.imgsz, self.conf, self.iou, self.device, self.half, self.max_det = imgsz, conf, iou, device, half, max_det
        self.names = self.model.names
        # single-class fine-tunes (workstream G: {0: "boat"}) have no COCO ids to filter on; keep everything.
        # Any other checkpoint whose ids miss a non-empty `keep` entirely is a mismatch, not a reason to pass every class
        # (an empty keep still means "all classes", as before).
        if keep and not set(keep) & set(self.names):
            if len(self.names) != 1:
                raise ValueError(f"{w}: none of keep={sorted(keep)} in model classes {self.names}")
            keep = None
        self.keep = set(keep) if keep is not None else None

    def detect(self, image_bgr: np.ndarray) -> list[Detection]:
        r = self.model.predict(
            image_bgr, imgsz=self.imgsz, conf=self.conf, iou=self.iou, device=self.device, half=self.half,
            max_det=self.max_det, verbose=False, classes=sorted(self.keep) if self.keep else None,
        )[0]
        out: list[Detection] = []
        if r.boxes is None:
            return out
        xyxy = r.boxes.xyxy.cpu().numpy()
        conf = r.boxes.conf.cpu().numpy()
        cls = r.boxes.cls.cpu().numpy().astype(int)
        for b, s, c in zip(xyxy, conf, cls):
            out.append(Detection(box=xyxy_to_xywh(b), score=float(s), label=str(self.names.get(int(c), c)), label_id=int(c)))
        return out
