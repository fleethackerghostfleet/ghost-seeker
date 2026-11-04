"""Cheap appearance embedding used for re-linking when no learned Embedder (workstream D) is plugged in.

HSV colour histogram + 4x4 grey layout, L2-normalised. Implements the crop half of the Embedder protocol.
"""
from __future__ import annotations

import cv2
import numpy as np


class ColorHistEmbedder:
    dim = 16 * 8 + 16

    def embed_crops(self, crops: list[np.ndarray]) -> np.ndarray:
        out = np.zeros((len(crops), self.dim), np.float32)
        for i, c in enumerate(crops):
            if c is None or c.size == 0:
                continue
            hsv = cv2.cvtColor(c, cv2.COLOR_BGR2HSV)
            h = cv2.calcHist([hsv], [0, 1], None, [16, 8], [0, 180, 0, 256]).flatten()
            g = cv2.resize(cv2.cvtColor(c, cv2.COLOR_BGR2GRAY), (4, 4), interpolation=cv2.INTER_AREA).flatten().astype(np.float32)
            v = np.concatenate([h / (h.sum() + 1e-6), g / 255.0 * 0.25])
            out[i] = v / (np.linalg.norm(v) + 1e-6)
        return out

    def embed_text(self, texts: list[str]) -> np.ndarray:
        raise NotImplementedError("ColorHistEmbedder has no text tower; use workstream D's Embedder")


def crop(image_bgr: np.ndarray, box, pad: float = 0.1) -> np.ndarray:
    H, W = image_bgr.shape[:2]
    x, y, w, h = box
    px, py = w * pad, h * pad
    x0, y0 = int(max(0, x - px)), int(max(0, y - py))
    x1, y1 = int(min(W, x + w + px)), int(min(H, y + h + py))
    if x1 <= x0 or y1 <= y0:
        return np.zeros((1, 1, 3), np.uint8)
    return image_bgr[y0:y1, x0:x1]
