"""Bounded MobileCLIP phrase matching; no detector, tracker or closed-set mutation."""
from __future__ import annotations

from collections import OrderedDict
from pathlib import Path
import re
import threading

import numpy as np

PREFIX = "open-vocab:"
MAX_PROMPTS = 5
MAX_CROPS = 8
NEGATIVES = ("a photo of the open sea", "a photo of waves and sky", "an empty ocean horizon",
             "sea spray and foam", "a blurry photo of water", "a photo of a boat", "a photo of a person")
MIN_SIMILARITY = 0.275
MIN_MARGIN = 0.05


def labels_from_wire(known_labels):
    labels = []
    for value in known_labels:
        if not isinstance(value, str) or not value.startswith(PREFIX):
            continue
        label = " ".join(value[len(PREFIX):].lower().split())
        if not re.fullmatch(r"[a-z][a-z0-9 -]{0,63}", label) or len(label.split()) > 7:
            raise ValueError("invalid open-vocabulary label")
        if label not in labels:
            labels.append(label)
    if len(labels) > MAX_PROMPTS:
        raise ValueError(f"at most {MAX_PROMPTS} open-vocabulary prompts")
    return labels


def prompts(label):
    return [f"a photo of a {label}", f"a photo of a {label} at sea"]


class TextCache:
    def __init__(self, encode, capacity=128):
        self.encode, self.capacity = encode, capacity
        self.cache = OrderedDict()
        self.lock = threading.Lock()

    def __call__(self, texts):
        if not texts:
            return []
        with self.lock:
            missing = list(dict.fromkeys(t for t in texts if t not in self.cache))
            if missing:
                vecs = np.asarray(self.encode(missing), np.float32)
                if vecs.ndim != 2 or len(vecs) != len(missing) or not np.isfinite(vecs).all():
                    raise ValueError("invalid text embeddings")
                norms = np.linalg.norm(vecs, axis=1, keepdims=True)
                if np.any(norms < 1e-8):
                    raise ValueError("zero text embedding")
                for text, vec in zip(missing, vecs):
                    self.cache[text] = vec
            out = np.asarray([self.cache[t] for t in texts], np.float32)
            for text in texts:
                self.cache.move_to_end(text)
            while len(self.cache) > self.capacity:
                self.cache.popitem(last=False)
            return out


class ONNXTextTower:
    """Offline text encoder; assets are exported from the same MobileCLIP weights."""
    def __init__(self, directory, threads=4):
        import onnxruntime as ort
        from tokenizers import Tokenizer

        d = Path(directory)
        options = ort.SessionOptions()
        options.intra_op_num_threads = threads
        options.inter_op_num_threads = 1
        self.session = ort.InferenceSession(str(d / "text_model.onnx"), options, providers=["CPUExecutionProvider"])
        self.tokenizer = Tokenizer.from_file(str(d / "tokenizer.json"))
        self.tokenizer.enable_truncation(max_length=77)
        self.tokenizer.enable_padding(length=77, pad_id=0, pad_token="!")

    def __call__(self, texts):
        tokens = np.asarray([e.ids for e in self.tokenizer.encode_batch(texts)], np.int64)
        return self.session.run(None, {"text": tokens})[0]


def regions(image, detections):
    h, w = image.shape[:2]
    boxes = []
    for det in sorted(detections, key=lambda d: d.score, reverse=True)[:MAX_CROPS]:
        x, y, bw, bh = det.box
        x0, y0 = max(0, int(x)), max(0, int(y))
        x1, y1 = min(w, int(np.ceil(x + bw))), min(h, int(np.ceil(y + bh)))
        if x1 > x0 and y1 > y0:
            boxes.append((x0, y0, x1 - x0, y1 - y0))
    boxes.append((0, 0, w, h))
    for row in range(2):
        for col in range(2):
            x0, y0 = col * w // 2, row * h // 2
            x1, y1 = (col + 1) * w // 2, (row + 1) * h // 2
            if x1 > x0 and y1 > y0:
                boxes.append((x0, y0, x1 - x0, y1 - y0))
    return list(dict.fromkeys(boxes))


class OpenVocabulary:
    def __init__(self, embedder, text, similarity=MIN_SIMILARITY, margin=MIN_MARGIN):
        self.embedder, self.text = embedder, text
        self.similarity, self.margin = similarity, margin

    def score(self, features, labels):
        text = self.text([p for label in labels for p in prompts(label)] + list(NEGATIVES))
        text = text / (np.linalg.norm(text, axis=1, keepdims=True) + 1e-8)
        positive = text[:2 * len(labels)].reshape(len(labels), 2, -1).mean(1)
        positive /= np.linalg.norm(positive, axis=1, keepdims=True) + 1e-8
        features = np.asarray(features, np.float32)
        features = features / (np.linalg.norm(features, axis=1, keepdims=True) + 1e-8)
        scores = features @ positive.T
        negatives = np.max(features @ text[2 * len(labels):].T, axis=1)
        return scores, scores - negatives[:, None]

    def detect(self, image, detections, labels):
        if not labels:
            return []
        boxes = regions(image, detections)
        crops = [image[y:y + h, x:x + w] for x, y, w, h in boxes]
        scores, margins = self.score(self.embedder.embed_crops(crops), labels)
        h, w = image.shape[:2]
        out = []
        for j, label in enumerate(labels):
            accepted = np.flatnonzero((scores[:, j] >= self.similarity) & (margins[:, j] >= self.margin))
            if not len(accepted):
                continue
            i = max(accepted, key=lambda i: float(margins[i, j]))
            x, y, bw, bh = boxes[i]
            # Acceptance score, not a posterior probability.
            confidence = min(0.99, 0.5 + float(margins[i, j]))
            out.append({"label": label, "confidence": round(confidence, 5),
                        "box": {"x": x / w, "y": y / h, "w": bw / w, "h": bh / h}})
        return out
