"""Ship-type classification and visible characteristics for vessel detection crops.

Three interchangeable backends share one output record (``ShipClass`` dict, same fields the Go catalog stores):

* ``ShipTypeClassifier`` -- zero-shot text-prompt classification on any ``Embedder`` with a text tower
  (MobileCLIP / SigLIP / OpenCLIP via ``ClipEmbedder``). Edge-capable; produces type, aspect, size (type prior).
* ``ProbeClassifier`` -- a logistic-regression probe on frozen image embeddings (``DinoEmbedder`` or any
  ``Embedder``). Only knows the classes present in its training labels.
* ``GeminiShipClassifier`` -- Gemini 2.5 Flash with a JSON schema. Shore-side upper bound / teacher; also returns
  visible features (radar mast, cranes, helicopter deck, gun mount) and whether markings are visible.

Never identifies specific real hulls, names or hull numbers: markings are reported only as visible / not visible.
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field

import cv2
import numpy as np

# key -> (display label, typical size class, text prompts)
SHIP_TYPES: dict[str, tuple[str, str, list[str]]] = {
    "cargo": ("Cargo/bulk", "large", ["a cargo ship", "a bulk carrier ship", "a general cargo ship with cranes", "an ore carrier"]),
    "container": ("Container", "large", ["a container ship", "a ship loaded with stacked shipping containers"]),
    "tanker": ("Tanker", "large", ["an oil tanker", "a tanker ship", "a chemical tanker"]),
    "passenger": ("Passenger/ferry", "large", ["a cruise ship", "a passenger ferry", "a passenger ship"]),
    "fishing": ("Fishing", "medium", ["a fishing boat", "a fishing trawler", "a fishing vessel"]),
    "tug": ("Tug", "medium", ["a tugboat", "a harbor tug"]),
    "sailboat": ("Sailboat/yacht", "small", ["a sailboat", "a sailing yacht", "a motor yacht"]),
    "small_boat": ("Small boat", "small", ["a small motorboat", "a speedboat", "a rigid inflatable boat", "a small open boat"]),
    "warship": ("Warship", "large", ["a grey navy warship", "a navy destroyer", "a navy frigate", "an aircraft carrier", "a naval patrol boat"]),
    "submarine": ("Submarine", "large", ["a submarine", "a surfaced submarine with a conning tower"]),
    "coast_guard": ("Coast guard", "medium", ["a coast guard ship", "a coast guard cutter"]),
}
UNKNOWN = "unknown"
TYPES = list(SHIP_TYPES) + [UNKNOWN]
LABELS = {k: v[0] for k, v in SHIP_TYPES.items()} | {UNKNOWN: "Unknown"}
UNKNOWN_PROMPTS = ["the empty sea", "waves on the ocean", "a buoy", "a bird", "an airplane", "a blurry speck on the horizon", "the coastline"]
TEMPLATES = ["a photo of {}.", "{} at sea.", "a low-resolution photo of {}."]

ASPECTS = {
    "bow": ["the bow of a ship seen head-on, from the front", "a ship heading straight towards the camera"],
    "stern": ["the stern of a ship seen from behind", "a ship heading straight away from the camera"],
    "beam": ["a ship seen broadside from the side", "the side profile of a ship"],
}
FEATURES = ["radar mast", "cranes", "helicopter deck", "gun mount"]

# dataset label -> ship type (only where the mapping is clean)
SEASHIPS_TO_TYPE = {"ore carrier": "cargo", "bulk cargo carrier": "cargo", "general cargo ship": "cargo",
                    "container ship": "container", "fishing boat": "fishing", "passenger ship": "passenger"}
CATALOG_TO_TYPE = {"cargo ship": "cargo", "tanker": "tanker", "fishing vessel": "fishing", "small boat": "small_boat",
                   "warship": "warship", "submarine": "submarine"}
# ship type -> Ghost Seeker catalog class, where it maps cleanly (used to sanity-check against the detector label)
TYPE_TO_CATALOG = {"cargo": "cargo ship", "container": "cargo ship", "tanker": "tanker", "fishing": "fishing vessel",
                   "small_boat": "small boat", "sailboat": "small boat", "warship": "warship", "submarine": "submarine"}


def crop_box(img: np.ndarray, box, pad: float = 0.1, min_side: int = 8) -> np.ndarray:
    """BGR crop of an xywh box, padded by ``pad`` of each side for context and clipped to the image."""
    x, y, w, h = [float(v) for v in box]
    px, py = w * pad, h * pad
    x0, y0 = max(0, int(x - px)), max(0, int(y - py))
    x1, y1 = min(img.shape[1], int(np.ceil(x + w + px))), min(img.shape[0], int(np.ceil(y + h + py)))
    if x1 - x0 < min_side or y1 - y0 < min_side:
        cx, cy, s = (x0 + x1) // 2, (y0 + y1) // 2, min_side // 2
        x0, y0, x1, y1 = max(0, cx - s), max(0, cy - s), min(img.shape[1], cx + s), min(img.shape[0], cy + s)
    return img[y0:y1, x0:x1]


def square_pad(crop: np.ndarray) -> np.ndarray:
    """Letterbox a crop to a square (border = crop mean) so square-input towers don't centre-crop a long hull away."""
    h, w = crop.shape[:2]
    d = abs(h - w)
    if d == 0:
        return crop
    a, b = d // 2, d - d // 2
    pads = (a, b, 0, 0) if w > h else (0, 0, a, b)
    return cv2.copyMakeBorder(crop, *pads, cv2.BORDER_CONSTANT, value=[float(v) for v in crop.reshape(-1, 3).mean(0)])


def prompt_texts() -> list[str]:
    groups = [p for _, _, p in SHIP_TYPES.values()] + [UNKNOWN_PROMPTS]
    prompts = [t.format(p) for ps in groups for p in ps for t in TEMPLATES]
    prompts.extend(p for group in ASPECTS.values() for p in group)
    return list(dict.fromkeys(prompts))


def size_from_type(t: str) -> str | None:
    return SHIP_TYPES[t][1] if t in SHIP_TYPES else None


def record(t: str, conf: float, aspect: str | None = None, size: str | None = None, features=None,
           markings: str | None = None, model: str = "") -> dict:
    out = {"type": t, "label": LABELS[t], "conf": round(float(conf), 3)}
    if aspect:
        out["aspect"] = aspect
    if size:
        out["size"] = size
    if features:
        out["features"] = list(features)
    if markings:
        out["markings"] = markings
    if model:
        out["model"] = model
    return out


def _softmax(z: np.ndarray) -> np.ndarray:
    z = z - z.max(axis=-1, keepdims=True)
    e = np.exp(z)
    return e / e.sum(axis=-1, keepdims=True)


@dataclass
class ShipTypeClassifier:
    """Zero-shot ship type + aspect from prompt prototypes. ``unknown_floor``: top-1 prob below it -> unknown."""
    embedder: object
    temperature: float = 100.0
    unknown_floor: float = 0.0
    square: bool = True
    name: str = ""
    _protos: np.ndarray = field(init=False, repr=False)
    _aspect: np.ndarray = field(init=False, repr=False)

    def __post_init__(self):
        groups = [p for _, _, p in SHIP_TYPES.values()] + [UNKNOWN_PROMPTS]
        self._protos = np.stack([self._proto([t.format(p) for p in ps for t in TEMPLATES]) for ps in groups])
        self._aspect = np.stack([self._proto(ps) for ps in ASPECTS.values()])
        self.name = self.name or getattr(self.embedder, "short", "embedder")

    def _proto(self, texts: list[str]) -> np.ndarray:
        e = np.asarray(self.embedder.embed_text(texts), np.float32).mean(0)
        return e / (np.linalg.norm(e) + 1e-9)

    def probs(self, emb: np.ndarray) -> np.ndarray:
        """(N, 12) probabilities over ``TYPES`` from L2-normalised image embeddings."""
        return _softmax(self.temperature * (np.asarray(emb, np.float32) @ self._protos.T))

    def classify_embeddings(self, emb: np.ndarray) -> list[dict]:
        p = self.probs(emb)
        a = _softmax(self.temperature * (np.asarray(emb, np.float32) @ self._aspect.T))
        out = []
        for pi, ai in zip(p, a):
            k = int(pi.argmax())
            unknown = k == len(TYPES) - 1
            rejected = not unknown and pi[k] < self.unknown_floor
            t = UNKNOWN if unknown or rejected else TYPES[k]
            conf = pi[k] if unknown or not rejected else 1 - pi[k]
            out.append(record(t, conf,
                              aspect=list(ASPECTS)[int(ai.argmax())] if t != UNKNOWN else None,
                              size=size_from_type(t), model=f"{self.name} zero-shot"))
        return out

    def classify(self, crops: list[np.ndarray]) -> list[dict]:
        crops = [square_pad(c) for c in crops] if self.square else crops
        return self.classify_embeddings(self.embedder.embed_crops(crops)) if crops else []


class DinoEmbedder:
    """Image-only ``Embedder`` (DINOv2 via timm). ``embed_text`` is unsupported -- use with ``ProbeClassifier``."""

    def __init__(self, arch: str = "vit_small_patch14_dinov2.lvd142m", size: int = 224, threads: int = 4):
        import timm
        import torch
        torch.set_num_threads(threads)
        self.torch = torch
        self.short = arch.split(".")[0]
        self.model = timm.create_model(arch, pretrained=True, num_classes=0, img_size=size).eval()
        self.size = size
        self.dim = self.model.num_features
        cfg = self.model.pretrained_cfg
        self.mean = np.array(cfg["mean"], np.float32)
        self.std = np.array(cfg["std"], np.float32)

    def embed_crops(self, crops: list[np.ndarray]) -> np.ndarray:
        x = np.stack([(cv2.cvtColor(cv2.resize(c, (self.size, self.size)), cv2.COLOR_BGR2RGB).astype(np.float32) / 255.0
                       - self.mean) / self.std for c in crops]).transpose(0, 3, 1, 2)
        with self.torch.no_grad():
            e = self.model(self.torch.from_numpy(x)).numpy()
        return e / (np.linalg.norm(e, axis=1, keepdims=True) + 1e-9)

    def embed_text(self, texts: list[str]) -> np.ndarray:
        raise NotImplementedError("DINOv2 has no text tower")


class ProbeClassifier:
    """Logistic-regression probe over frozen embeddings; predicts only the types seen in ``fit``."""

    def __init__(self, embedder: object, C: float = 1.0):
        from sklearn.linear_model import LogisticRegression
        self.embedder = embedder
        self.clf = LogisticRegression(C=C, max_iter=2000)
        self.name = f"{getattr(embedder, 'short', 'embedder')} probe"

    def fit(self, emb: np.ndarray, types: list[str]) -> "ProbeClassifier":
        self.clf.fit(emb, types)
        return self

    def classify_embeddings(self, emb: np.ndarray) -> list[dict]:
        p = self.clf.predict_proba(emb)
        return [record(str(self.clf.classes_[k]), pi[k], size=size_from_type(str(self.clf.classes_[k])), model=self.name)
                for pi, k in zip(p, p.argmax(1))]

    def classify(self, crops: list[np.ndarray]) -> list[dict]:
        return self.classify_embeddings(self.embedder.embed_crops(crops)) if crops else []


GEMINI_PROMPT = """You are a maritime imagery analyst. The image is a crop around ONE detected object at sea \
(it may be small, blurry, infrared or synthetic). Classify the main object in the crop.

ship_type must be exactly one of:
cargo (bulk carrier, general cargo, ore carrier, car carrier), container, tanker, passenger (cruise ship, ferry), \
fishing, tug, sailboat (sailboat or yacht), small_boat (motorboat, speedboat, RHIB, dinghy), \
warship (destroyer, frigate, carrier, naval patrol vessel), submarine, coast_guard, \
unknown (not a vessel, or too small/unclear to tell).

Also report only what is visible in this crop:
- aspect: bow (seen from the front), stern (from behind), beam (side-on), quarter (oblique), or unknown
- size: small (< ~15 m), medium (~15-80 m), large (> ~80 m), or unknown, judged from the vessel's appearance
- features: subset of ["radar mast", "cranes", "helicopter deck", "gun mount"] that are clearly visible
- markings: "visible" if any flag, hull number or name text is visible, else "not visible". \
Never read out or guess names, hull numbers or the identity of a specific real vessel.
- confidence: your probability (0-1) that ship_type is correct."""

GEMINI_SCHEMA = {
    "type": "object",
    "properties": {
        "ship_type": {"type": "string", "enum": TYPES},
        "confidence": {"type": "number"},
        "aspect": {"type": "string", "enum": ["bow", "stern", "beam", "quarter", "unknown"]},
        "size": {"type": "string", "enum": ["small", "medium", "large", "unknown"]},
        "features": {"type": "array", "items": {"type": "string", "enum": FEATURES}},
        "markings": {"type": "string", "enum": ["visible", "not visible"]},
    },
    "required": ["ship_type", "confidence", "aspect", "size", "features", "markings"],
}


class GeminiShipClassifier:
    """Gemini 2.5 Flash on Vertex AI. ``credentials``: google.auth credentials (defaults to ADC)."""

    def __init__(self, project: str, location: str = "us-central1", model: str = "gemini-2.5-flash",
                 credentials=None, min_side: int = 64):
        from google import genai
        from google.genai import types
        self.types = types
        self.client = genai.Client(vertexai=True, project=project, location=location, credentials=credentials)
        self.model = model
        self.min_side = min_side
        self.name = model
        self.config = types.GenerateContentConfig(
            temperature=0.0, response_mime_type="application/json", response_schema=GEMINI_SCHEMA,
            thinking_config=types.ThinkingConfig(thinking_budget=0))

    def classify_one(self, crop: np.ndarray) -> dict:
        s = self.min_side / max(1, min(crop.shape[:2]))
        if s > 1:  # upscale tiny crops so the model sees something
            crop = cv2.resize(crop, None, fx=s, fy=s, interpolation=cv2.INTER_CUBIC)
        ok, buf = cv2.imencode(".jpg", crop, [cv2.IMWRITE_JPEG_QUALITY, 92])
        part = self.types.Part.from_bytes(data=buf.tobytes(), mime_type="image/jpeg")
        r = self.client.models.generate_content(model=self.model, contents=[part, GEMINI_PROMPT], config=self.config)
        j = json.loads(r.text)
        t = j.get("ship_type") if j.get("ship_type") in TYPES else UNKNOWN
        return record(t, min(1.0, max(0.0, float(j.get("confidence", 0.0)))),
                      aspect=None if j.get("aspect") in (None, "unknown") else j["aspect"],
                      size=None if j.get("size") in (None, "unknown") else j["size"],
                      features=[f for f in j.get("features", []) if f in FEATURES],
                      markings=j.get("markings"), model=self.model)

    def classify(self, crops: list[np.ndarray], workers: int = 8) -> list[dict]:
        from concurrent.futures import ThreadPoolExecutor
        with ThreadPoolExecutor(workers) as ex:
            return list(ex.map(self.classify_one, crops))
