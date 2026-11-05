"""Optional RF-DETR adapter. The model's native resolution is retained."""
from __future__ import annotations

import numpy as np

from ..types import Detection
from .base import BaseDetector, xyxy_to_xywh


class RFDetector(BaseDetector):
    def __init__(self, variant: str, name: str, device: str | None = None,
                 pretrain_weights: str | None = None, **kw):
        super().__init__(**kw)
        import rfdetr
        from rfdetr.util.coco_classes import COCO_CLASSES

        self.device = device or "cpu"
        model_kwargs = {"device": self.device}
        if pretrain_weights is not None:
            model_kwargs["pretrain_weights"] = pretrain_weights
        self.model = getattr(rfdetr, variant)(**model_kwargs)
        self.names = COCO_CLASSES
        self.name = name
        self.imgsz = self.model.model.resolution
        self.params_m = sum(p.numel() for p in self.model.model.model.parameters()) / 1e6
        self.training_data = "COCO; DINOv2 visual backbone pretraining"
        self._infer(np.zeros((480, 640, 3), dtype=np.uint8))

    def _infer(self, image_bgr):
        from PIL import Image

        pred = self.model.predict(Image.fromarray(image_bgr[:, :, ::-1]), threshold=self.score_thr)
        return [Detection(box=xyxy_to_xywh(box), score=float(score), label=self.names[int(cls)], label_id=int(cls))
                for box, score, cls in zip(pred.xyxy, pred.confidence, pred.class_id)]

    def _sync(self):
        if self.device.startswith("cuda"):
            import torch

            torch.cuda.synchronize()


class FlorenceDetector(BaseDetector):
    def __init__(self, name="florence2-base-ft", device=None, **kw):
        if device not in (None, "cpu"):
            raise ValueError("This bake-off Florence adapter supports CPU only")
        super().__init__(**kw)
        import torch
        from transformers import AutoProcessor, Florence2ForConditionalGeneration

        self.torch = torch
        self.model_id = "florence-community/Florence-2-base-ft"
        self.model = Florence2ForConditionalGeneration.from_pretrained(self.model_id).eval()
        self.processor = AutoProcessor.from_pretrained(self.model_id)
        self.name = name
        self.params_m = sum(p.numel() for p in self.model.parameters()) / 1e6
        self.training_data = "FLD-5B + downstream fine-tuning; model-card overlap not independently audited"
        self.imgsz = self.processor.image_processor.size
        self._infer(np.zeros((480, 640, 3), dtype=np.uint8))

    def _infer(self, image_bgr):
        from PIL import Image

        image = Image.fromarray(image_bgr[:, :, ::-1])
        inputs = self.processor(text="<OD>", images=image, return_tensors="pt")
        with self.torch.inference_mode():
            ids = self.model.generate(**inputs, max_new_tokens=1024, do_sample=False, num_beams=3)
        text = self.processor.batch_decode(ids, skip_special_tokens=False)[0]
        parsed = self.processor.post_process_generation(text, task="<OD>", image_size=image.size)["<OD>"]
        # OD generation supplies boxes and labels, not calibrated detection confidences.
        return [Detection(box=xyxy_to_xywh(b), score=1.0, label=lab)
                for b, lab in zip(parsed["bboxes"], parsed["labels"])]


class HostedDetector(BaseDetector):
    """REST inference only; no credentials or request headers are persisted."""

    PROMPT = ('Detect every visible boat, ship, vessel, buoy, or person in this maritime image, including '
              'small distant contacts. Return a JSON array; each item has label (boat, buoy, or person), '
              'box [ymin,xmin,ymax,xmax] in integers normalized to 0..1000, and score in 0..1. '
              'Do not invent objects; return [] for no objects. Score is your confidence. No commentary.')

    def __init__(self, name, device=None, **kw):
        super().__init__(**kw)
        import os
        from pathlib import Path

        import requests

        self.name = name
        self.project = os.environ.get("GOOGLE_CLOUD_PROJECT", "qwiklabs-gcp-02-34a870014566")
        self.session = requests.Session()
        self._refresh_auth()
        self.session.headers["x-goog-user-project"] = self.project
        self.raw_dir = Path(os.environ["BAKEOFF_API_RAW"]) / name
        self.raw_dir.mkdir(parents=True, exist_ok=True)
        self.training_data = "hosted service; training overlap and parameter count undisclosed"
        self.imgsz = None

    def _refresh_auth(self):
        import subprocess

        token = subprocess.check_output(["gcloud", "auth", "print-access-token"], text=True).strip()
        self.session.headers["Authorization"] = f"Bearer {token}"

    def _infer(self, image_bgr):
        import base64
        import hashlib
        import json
        import time

        import cv2

        h, w = image_bgr.shape[:2]
        encoded = cv2.imencode(".jpg", image_bgr, [cv2.IMWRITE_JPEG_QUALITY, 95])[1].tobytes()
        key = hashlib.sha256(encoded).hexdigest()
        data = base64.b64encode(encoded).decode()
        if self.name == "cloud-vision":
            url = "https://vision.googleapis.com/v1/images:annotate"
            payload = {"requests": [{"image": {"content": data}, "features": [{"type": "OBJECT_LOCALIZATION", "maxResults": 100}]}]}
        else:
            url = (f"https://aiplatform.googleapis.com/v1/projects/{self.project}/locations/global/"
                   f"publishers/google/models/{self.name}:generateContent")
            payload = {"contents": [{"role": "user", "parts": [{"text": self.PROMPT},
                       {"inlineData": {"mimeType": "image/jpeg", "data": data}}]}],
                       "generationConfig": {"temperature": 0, "maxOutputTokens": 8192,
                                            "responseMimeType": "application/json",
                                            "responseSchema": {"type": "ARRAY", "items": {
                                                "type": "OBJECT", "required": ["label", "box", "score"],
                                                "properties": {
                                                    "label": {"type": "STRING", "enum": ["boat", "buoy", "person"]},
                                                    "box": {"type": "ARRAY", "items": {"type": "INTEGER"}, "minItems": 4, "maxItems": 4},
                                                    "score": {"type": "NUMBER"}}}},
                                            "thinkingConfig": {"thinkingBudget": 1024}}}
        start = time.perf_counter()
        auth_refreshed = False
        for attempt in range(6):
            response = self.session.post(url, json=payload, timeout=240)
            if response.status_code == 401 and not auth_refreshed and attempt < 5:
                self._refresh_auth()
                auth_refreshed = True
                continue
            if response.status_code not in (429, 500, 502, 503, 504) or attempt == 5:
                break
            time.sleep(2 ** (attempt + 1))
        obj = response.json()
        (self.raw_dir / f"{key}.json").write_text(json.dumps({"status": response.status_code, "attempts": attempt + 1,
            "elapsed_ms": (time.perf_counter() - start) * 1000, "response": obj}))
        response.raise_for_status()
        if self.name == "cloud-vision":
            result = obj["responses"][0]
            if "error" in result:
                raise RuntimeError(result["error"])
            out = []
            for d in result.get("localizedObjectAnnotations", []):
                v = d["boundingPoly"]["normalizedVertices"]
                box = [min(p.get("x", 0) for p in v) * w, min(p.get("y", 0) for p in v) * h,
                       max(p.get("x", 0) for p in v) * w, max(p.get("y", 0) for p in v) * h]
                out.append(Detection(box=xyxy_to_xywh(box), score=d["score"], label=d["name"].lower()))
            return out
        candidate = obj["candidates"][0]
        if candidate.get("finishReason") != "STOP":
            raise RuntimeError(f"incomplete generation: {candidate.get('finishReason')}")
        content = "".join(p.get("text", "") for p in candidate["content"]["parts"] if not p.get("thought"))
        items = json.loads(content)
        if not isinstance(items, list):
            raise ValueError("Expected a JSON array")
        out = []
        for d in items:
            y0, x0, y1, x1 = d["box"]
            out.append(Detection(box=xyxy_to_xywh([x0 * w / 1000, y0 * h / 1000, x1 * w / 1000, y1 * h / 1000]),
                                 score=float(d["score"]), label=d["label"].lower()))
        return out
