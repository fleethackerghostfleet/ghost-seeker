"""Adapters for HuggingFace `transformers` detectors.

HFClosedSetDetector: RT-DETRv2 (PekingU/rtdetr_v2_*), D-FINE (ustc-community/dfine-*), COCO label space.
HFOpenVocabDetector: OWLv2 (google/owlv2-*) and Grounding DINO (IDEA-Research/grounding-dino-*) with text prompts.
"""
from __future__ import annotations

import numpy as np

from ..types import Detection
from .base import BaseDetector, xyxy_to_xywh
from .labels import MARITIME_PROMPTS


def _count_params(m) -> float:
    return sum(p.numel() for p in m.parameters()) / 1e6


class HFClosedSetDetector(BaseDetector):
    def __init__(self, model_id: str, name: str | None = None, imgsz: int = 640, device: str | None = None,
                 half: bool = True, **kw):
        super().__init__(**kw)
        import torch
        from transformers import AutoImageProcessor, AutoModelForObjectDetection

        self.torch = torch
        self.device = device or ("cuda:0" if torch.cuda.is_available() else "cpu")
        self.dtype = torch.float16 if (half and self.device.startswith("cuda")) else torch.float32
        self.processor = AutoImageProcessor.from_pretrained(model_id, size={"height": imgsz, "width": imgsz})
        self.model = AutoModelForObjectDetection.from_pretrained(model_id).to(self.device, self.dtype).eval()
        self.model_id = model_id
        self.id2label = self.model.config.id2label
        self.imgsz = imgsz
        self.name = name or model_id.rsplit("/", 1)[-1]
        self.params_m = _count_params(self.model)
        self._infer(np.zeros((imgsz, imgsz, 3), dtype=np.uint8))

    def _sync(self):
        if self.device.startswith("cuda"):
            self.torch.cuda.synchronize()

    def _infer(self, image_bgr: np.ndarray) -> list[Detection]:
        rgb = np.ascontiguousarray(image_bgr[:, :, ::-1])
        inputs = self.processor(images=rgb, return_tensors="pt")
        inputs = {k: (v.to(self.device, self.dtype) if v.is_floating_point() else v.to(self.device)) for k, v in inputs.items()}
        with self.torch.inference_mode():
            outputs = self.model(**inputs)
        h, w = image_bgr.shape[:2]
        res = self.processor.post_process_object_detection(
            outputs, threshold=self.score_thr, target_sizes=self.torch.tensor([[h, w]], device=self.device))[0]
        out = []
        for b, s, c in zip(res["boxes"].float().cpu().numpy(), res["scores"].float().cpu().numpy(),
                           res["labels"].cpu().numpy()):
            out.append(Detection(box=xyxy_to_xywh(b), score=float(s), label=str(self.id2label[int(c)]), label_id=int(c)))
        return out


class HFOpenVocabDetector(BaseDetector):
    def __init__(self, model_id: str, name: str | None = None, prompts: list[str] | None = None,
                 imgsz: int | None = None, device: str | None = None, half: bool = True,
                 text_thr: float = 0.1, **kw):
        super().__init__(**kw)
        import torch
        from transformers import AutoModelForZeroShotObjectDetection, AutoProcessor

        self.torch = torch
        self.device = device or ("cuda:0" if torch.cuda.is_available() else "cpu")
        self.dtype = torch.float16 if (half and self.device.startswith("cuda")) else torch.float32
        self.is_gdino = "grounding" in model_id.lower()
        self.prompts = list(prompts or MARITIME_PROMPTS)
        pkw = {}
        if imgsz and self.is_gdino:
            pkw["size"] = {"shortest_edge": imgsz, "longest_edge": max(imgsz, int(imgsz * 16 / 9) + 1)}
        self.processor = AutoProcessor.from_pretrained(model_id, **pkw)
        self.model = AutoModelForZeroShotObjectDetection.from_pretrained(model_id).to(self.device, self.dtype).eval()
        self.model_id = model_id
        self.imgsz = imgsz or (960 if self.is_gdino else 960)
        self.text_thr = text_thr
        self.name = name or model_id.rsplit("/", 1)[-1]
        self.params_m = _count_params(self.model)
        self.training_data = "open-vocab pretrain (OWLv2: WebLI/O365/VG; GDINO: O365/GoldG/Cap4M)"
        self._text = ". ".join(self.prompts) + "." if self.is_gdino else [self.prompts]
        self._infer(np.zeros((480, 640, 3), dtype=np.uint8))

    def _sync(self):
        if self.device.startswith("cuda"):
            self.torch.cuda.synchronize()

    def _label_from(self, res, i) -> str:
        tl = res.get("text_labels")
        if tl is not None and i < len(tl) and tl[i]:
            lab = tl[i]
            # grounding dino can return merged phrases like "ship boat"; take the first known prompt
            for p in self.prompts:
                if p == lab or lab.startswith(p) or p in lab.split():
                    return p
            return lab
        labs = res["labels"]
        li = labs[i]
        if isinstance(li, str):
            return li
        return self.prompts[int(li)] if int(li) < len(self.prompts) else str(li)

    def _infer(self, image_bgr: np.ndarray) -> list[Detection]:
        from PIL import Image

        rgb = Image.fromarray(np.ascontiguousarray(image_bgr[:, :, ::-1]))
        h, w = image_bgr.shape[:2]
        inputs = self.processor(images=rgb, text=self._text, return_tensors="pt")
        inputs = {k: (v.to(self.device, self.dtype) if v.is_floating_point() else v.to(self.device)) for k, v in inputs.items()}
        with self.torch.inference_mode():
            outputs = self.model(**inputs)
        ts = self.torch.tensor([[h, w]], device=self.device)
        if self.is_gdino:
            res = self.processor.post_process_grounded_object_detection(
                outputs, inputs["input_ids"], threshold=self.score_thr, text_threshold=self.text_thr, target_sizes=ts)[0]
        else:
            res = self.processor.post_process_grounded_object_detection(
                outputs, threshold=self.score_thr, target_sizes=ts, text_labels=self._text)[0]
        out = []
        boxes = res["boxes"].float().cpu().numpy()
        scores = res["scores"].float().cpu().numpy()
        for i, (b, s) in enumerate(zip(boxes, scores)):
            lab = self._label_from(res, i)
            out.append(Detection(box=xyxy_to_xywh(b), score=float(s), label=lab,
                                 label_id=self.prompts.index(lab) if lab in self.prompts else None))
        return out
