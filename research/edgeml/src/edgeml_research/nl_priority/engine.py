"""Stateless candidate vocabulary comparisons; baseline unknownness is immutable."""
from __future__ import annotations
import base64
import hashlib
import io
import json
import re
from collections import OrderedDict
from pathlib import Path
import numpy as np
from edgeml.embed.zeroshot import SUBTYPES, UNKNOWN_PROMPTS, ZeroShotClassifier

MAX_CONCEPTS = 16
MAX_ITEMS = 10000


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def normalize(vectors):
    values = np.asarray(vectors, dtype=np.float32)
    if not np.isfinite(values).all():
        raise ValueError("embeddings must be finite")
    norms = np.linalg.norm(values, axis=-1, keepdims=True)
    if np.any(norms < 1e-8):
        raise ValueError("zero embedding")
    return values / norms


def validate_manifest(directory: Path):
    manifest = json.loads((directory / "manifest.json").read_text())
    if manifest.get("schemaVersion") != 1 or manifest.get("architecture") != "MobileCLIP-S1" or manifest.get("pretrained") != "datacompdr":
        raise ValueError("expected a MobileCLIP-S1/datacompdr research export")
    if not manifest.get("modelId") or not isinstance(manifest.get("dimension"), int) or manifest["dimension"] <= 0:
        raise ValueError("missing model identity/dimension")
    for name, expected in manifest["sha256"].items():
        path = (directory / name).resolve()
        if path.parent != directory.resolve() or not path.is_file() or sha256(path) != expected:
            raise ValueError(f"model asset checksum mismatch: {name}")
    for required in (manifest["textGraph"], "tokenizer.json", "base_prototypes.json"):
        if required not in manifest["sha256"]:
            raise ValueError(f"unchecked model asset: {required}")
    return manifest


class ONNXEncoder:
    def __init__(self, directory: str | Path, threads: int = 2):
        from .tokenizer import Tokenizer
        import onnxruntime as ort
        self.directory = Path(directory)
        self.manifest = validate_manifest(self.directory)
        self.model_id, self.dimension = self.manifest["modelId"], self.manifest["dimension"]
        self.tokenizer = Tokenizer(self.directory / "tokenizer.json")
        self.options = ort.SessionOptions()
        self.options.intra_op_num_threads = threads
        self.options.inter_op_num_threads = 1
        self.session = ort.InferenceSession(str(self.directory / self.manifest["textGraph"]), self.options, providers=["CPUExecutionProvider"])
        self.image_session = None

    def encode(self, texts):
        if not texts:
            return np.empty((0, self.dimension), np.float32)
        ids = self.tokenizer(texts)
        vectors = [self.session.run(None, {"tokens": row[None]})[0][0] for row in ids]
        result = normalize(vectors)
        if result.shape != (len(texts), self.dimension):
            raise ValueError("text output dimension disagrees with manifest")
        return result

    def encode_images(self, payloads):
        import onnxruntime as ort
        from PIL import Image, ImageOps
        image_graph = self.manifest.get("imageGraph")
        if not image_graph or image_graph not in self.manifest["sha256"]:
            raise ValueError("this export has no matching image encoder")
        if self.image_session is None:
            self.image_session = ort.InferenceSession(str(self.directory / image_graph), self.options, providers=["CPUExecutionProvider"])
        meta = self.manifest["imagePreprocess"]
        size = int(meta["size"])
        mean, std = np.asarray(meta["mean"], np.float32), np.asarray(meta["std"], np.float32)
        result = []
        for payload in payloads:
            raw = base64.b64decode(payload, validate=True)
            if len(raw) > 8 * 1024 * 1024:
                raise ValueError("crop exceeds 8 MB")
            with Image.open(io.BytesIO(raw)) as image:
                if image.width * image.height > 25_000_000:
                    raise ValueError("crop exceeds 25 megapixels")
                image = ImageOps.exif_transpose(image).convert("RGB")
                width, height = image.size
                resized = (size, int(size * height / width)) if width <= height else (int(size * width / height), size)
                image = image.resize(resized, getattr(Image.Resampling, meta["interpolation"].upper()))
                left, top = round((image.width - size) / 2), round((image.height - size) / 2)
                image = image.crop((left, top, left + size, top + size))
                x = ((np.asarray(image, np.float32) / 255 - mean) / std).transpose(2, 0, 1)[None]
                result.append(self.image_session.run(None, {"image": x})[0][0])
        return normalize(result)


class Vocabulary:
    def __init__(self, encoder, min_similarity: float = 0.25, min_margin: float = 0.02):
        if not -1 <= min_similarity <= 1 or not 0 <= min_margin <= 1:
            raise ValueError("invalid abstention thresholds")
        self.encoder = encoder
        self.model_id, self.dimension = encoder.model_id, encoder.dimension
        self.min_similarity, self.min_margin = min_similarity, min_margin
        self.cache = OrderedDict()
        manifest = getattr(encoder, "manifest", None)
        if manifest:
            cached = json.loads((encoder.directory / "base_prototypes.json").read_text())
            if cached["modelId"] != self.model_id:
                raise ValueError("base prototypes use another model")
            for text, emb in cached["prompts"].items():
                value = np.asarray(emb, np.float32)
                if value.shape != (self.dimension,) or not np.isfinite(value).all():
                    raise ValueError("invalid cached text embedding")
                self.cache[text] = normalize(value)
        self.baseline = ZeroShotClassifier(self)
        self.labels = list(SUBTYPES)
        self.base, self.background = self.baseline.prototypes, self.baseline.unknown_protos

    def logit_scale(self):
        return float(getattr(self.encoder, "manifest", {}).get("logitScale", 100.0))

    def embed_text(self, texts):
        if not isinstance(texts, list) or len(texts) > 128 or any(not isinstance(t, str) or not t.strip() or len(t) > 512 for t in texts):
            raise ValueError("texts must contain at most 128 nonempty strings of at most 512 characters")
        if not texts:
            return np.empty((0, self.dimension), np.float32)
        missing = list(dict.fromkeys(t for t in texts if t not in self.cache))
        if missing:
            for text, vector in zip(missing, self.encoder.encode(missing)):
                self.cache[text] = vector
        result = np.stack([self.cache[t] for t in texts])
        for text in texts:
            self.cache.move_to_end(text)
        while len(self.cache) > 1024:
            self.cache.popitem(last=False)
        return result

    def concepts(self, concepts):
        if not isinstance(concepts, list) or len(concepts) > len(SUBTYPES) + MAX_CONCEPTS:
            raise ValueError("too many concepts")
        clean = []
        for concept in concepts:
            if not isinstance(concept, str):
                raise ValueError("concept must be a string")
            value = " ".join(concept.lower().split())
            if not re.fullmatch(r"[a-z0-9][a-z0-9 '\-]{0,79}", value):
                raise ValueError("concepts must be short English visual descriptions")
            if value not in clean:
                clean.append(value)
        additions = [c for c in clean if c not in SUBTYPES]
        if len(additions) > MAX_CONCEPTS:
            raise ValueError("at most 16 added concepts")
        return additions

    def classify(self, concepts, items):
        additions = self.concepts(concepts)
        if not isinstance(items, list) or len(items) > MAX_ITEMS:
            raise ValueError("items must be a list of at most 10000 embeddings")
        ids, values, seen = [], [], set()
        for item in items:
            if not isinstance(item, dict) or not isinstance(item.get("id"), str) or not item["id"] or item["id"] in seen:
                raise ValueError("item IDs must be nonempty and unique")
            if item.get("modelId") != self.model_id:
                raise ValueError(f"embedding model mismatch for {item['id']}")
            value = np.asarray(item.get("embedding"), np.float32)
            if value.shape != (self.dimension,):
                raise ValueError(f"embedding dimension mismatch for {item['id']}")
            ids.append(item["id"])
            seen.add(item["id"])
            values.append(value)
        labels, prototypes = self.labels + additions, self.base
        if additions:
            extra = []
            for concept in additions:
                vectors = self.embed_text([f"a photo of a {concept}", f"a photo of a {concept} at sea"])
                extra.append(normalize(vectors.mean(0)))
            prototypes = np.concatenate((self.base, np.stack(extra)))
        if not items:
            return {"modelId": self.model_id, "dimension": self.dimension, "vocabulary": labels, "matches": []}
        vectors = normalize(values)
        scores = vectors @ prototypes.T
        background = (vectors @ self.background.T).max(1)
        matches = []
        for i, item_id in enumerate(ids):
            ranked = np.argsort(scores[i])[::-1]
            best, second = int(ranked[0]), int(ranked[1])
            similarity = float(scores[i, best])
            margin = float(similarity - max(scores[i, second], background[i]))
            experimental = best >= len(self.labels)
            accepted = experimental and similarity >= self.min_similarity and margin >= self.min_margin
            reason = "accepted experimental candidate" if accepted else "base vocabulary wins" if not experimental else "weak or ambiguous visual match"
            matches.append({"id": item_id, "label": labels[best], "similarity": similarity, "margin": margin,
                            "accepted": bool(accepted), "experimental": experimental, "reason": reason})
        return {"modelId": self.model_id, "dimension": self.dimension, "vocabulary": labels, "matches": matches}
