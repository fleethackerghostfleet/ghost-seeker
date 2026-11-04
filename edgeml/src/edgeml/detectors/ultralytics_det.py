"""Adapter for anything the `ultralytics` package loads: YOLOv8/YOLO11, RT-DETR (ultralytics weights),
YOLO-World (open-vocab, `prompts=`), and community .pt checkpoints (e.g. the Argus maritime models)."""
from __future__ import annotations

from typing import Optional

import numpy as np

from ..types import Detection
from .base import BaseDetector, xyxy_to_xywh


class UltralyticsDetector(BaseDetector):
    def __init__(self, weights: str, name: Optional[str] = None, imgsz: int = 640, device: Optional[str] = None,
                 half: bool = True, iou: float = 0.7, prompts: Optional[list[str]] = None,
                 training_data: str = "COCO", **kw):
        super().__init__(**kw)
        import torch
        from ultralytics import YOLO, RTDETR

        self.torch = torch
        self.device = device or ("cuda:0" if torch.cuda.is_available() else "cpu")
        self.half = half and self.device.startswith("cuda")
        cls = RTDETR if "rtdetr" in weights.lower() else YOLO
        self.model = cls(weights)
        if prompts:
            self.model.set_classes(prompts)
        self.names = self.model.names
        self.imgsz = imgsz
        self.iou = iou
        self.name = name or weights.rsplit("/", 1)[-1].replace(".pt", "")
        self.training_data = training_data
        try:
            from ultralytics.utils.torch_utils import get_num_params, get_flops

            self.params_m = get_num_params(self.model.model) / 1e6
            self.gflops = get_flops(self.model.model, imgsz) or None
        except Exception:
            pass
        # warm-up
        self.model.predict(np.zeros((imgsz, imgsz, 3), dtype=np.uint8), imgsz=imgsz, device=self.device,
                           half=self.half, verbose=False)

    def _sync(self):
        if self.device.startswith("cuda"):
            self.torch.cuda.synchronize()

    def _infer(self, image_bgr: np.ndarray) -> list[Detection]:
        r = self.model.predict(image_bgr, imgsz=self.imgsz, conf=self.score_thr, iou=self.iou,
                               device=self.device, half=self.half, verbose=False, max_det=self.max_det)[0]
        out = []
        if r.boxes is None:
            return out
        xyxy = r.boxes.xyxy.cpu().numpy()
        conf = r.boxes.conf.cpu().numpy()
        cls = r.boxes.cls.cpu().numpy().astype(int)
        for b, s, c in zip(xyxy, conf, cls):
            out.append(Detection(box=xyxy_to_xywh(b), score=float(s), label=str(self.names[int(c)]), label_id=int(c)))
        return out
