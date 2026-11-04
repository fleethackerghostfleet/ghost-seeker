"""Pre-encoded bandwidth forms for a sighting (CONTRACTS §1 / §5b).

``encode_forms`` returns ``[{kind, bytes, quality, w, h, payload}]`` for fact / crop / thumb / full so a selector can
trade value per byte with exact sizes. ``payload`` is the encoded bytes (JSON for fact, JPEG/WebP otherwise); drop it
before serialising the event if only the sizes are needed.
"""
from __future__ import annotations

import json
import time
from dataclasses import dataclass

import cv2
import numpy as np


@dataclass
class FormSpec:
    kind: str                 # crop | thumb | full
    max_side: int             # longest side after resize (0 = native)
    quality: int              # JPEG / WebP quality
    codec: str = "jpeg"       # jpeg | webp
    context: float = 2.0      # crop only: box is expanded to context x its size (centre fixed)


DEFAULT_FORMS = [
    FormSpec("crop", 192, 60, "jpeg", 2.0),
    FormSpec("thumb", 256, 50, "jpeg"),
    FormSpec("full", 0, 75, "jpeg"),
]


def crop_with_context(image_bgr: np.ndarray, box_xywh, context: float = 2.0, min_side: int = 32) -> tuple[np.ndarray, list[int]]:
    """Square-ish crop around ``box`` scaled by ``context``; returns (crop, [x, y, w, h] actually used)."""
    H, W = image_bgr.shape[:2]
    x, y, w, h = box_xywh
    cx, cy = x + w / 2, y + h / 2
    side = max(w, h) * context
    side = max(side, min_side)
    x0, y0 = int(max(0, round(cx - side / 2))), int(max(0, round(cy - side / 2)))
    x1, y1 = int(min(W, round(cx + side / 2))), int(min(H, round(cy + side / 2)))
    if x1 - x0 < 2 or y1 - y0 < 2:
        x0, y0, x1, y1 = 0, 0, W, H
    return image_bgr[y0:y1, x0:x1], [x0, y0, x1 - x0, y1 - y0]


def resize_max_side(img: np.ndarray, max_side: int) -> np.ndarray:
    if max_side <= 0:
        return img
    h, w = img.shape[:2]
    s = max_side / max(h, w)
    if s >= 1.0:
        return img
    return cv2.resize(img, (max(1, round(w * s)), max(1, round(h * s))), interpolation=cv2.INTER_AREA)


def encode_image(img: np.ndarray, quality: int, codec: str = "jpeg") -> bytes:
    if codec == "jpeg":
        ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, int(quality), cv2.IMWRITE_JPEG_OPTIMIZE, 1])
    elif codec == "webp":
        ok, buf = cv2.imencode(".webp", img, [cv2.IMWRITE_WEBP_QUALITY, int(quality)])
    else:
        raise ValueError(codec)
    if not ok:
        raise RuntimeError("encode failed")
    return buf.tobytes()


def decode_image(payload: bytes) -> np.ndarray:
    return cv2.imdecode(np.frombuffer(payload, np.uint8), cv2.IMREAD_COLOR)


def encode_forms(image_bgr: np.ndarray, box_xywh, fact: dict, specs: list[FormSpec] = DEFAULT_FORMS) -> tuple[list[dict], float]:
    """Encode every form. Returns (forms, encode_ms). Each form carries exact ``bytes``."""
    t0 = time.perf_counter()
    forms = []
    payload = json.dumps(fact, separators=(",", ":")).encode()
    forms.append({"kind": "fact", "bytes": len(payload), "quality": None, "w": 0, "h": 0, "payload": payload})
    for s in specs:
        if s.kind == "crop":
            img, used = crop_with_context(image_bgr, box_xywh, s.context)
        else:
            img, used = image_bgr, [0, 0, image_bgr.shape[1], image_bgr.shape[0]]
        img = resize_max_side(img, s.max_side)
        data = encode_image(img, s.quality, s.codec)
        forms.append({"kind": s.kind, "bytes": len(data), "quality": s.quality, "codec": s.codec, "w": img.shape[1],
                      "h": img.shape[0], "src_box": used, "payload": data})
    return forms, (time.perf_counter() - t0) * 1e3
