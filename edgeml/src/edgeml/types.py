from dataclasses import dataclass, field
from typing import Literal, Optional

import numpy as np

Box = list[float]  # pixel [x, y, w, h], original image coordinates


@dataclass
class Detection:
    box: Box
    score: float
    label: str
    label_id: int | None = None
    embedding: np.ndarray | None = None


@dataclass
class Track:
    track_id: int
    box: Box
    score: float
    label: str
    frame_idx: int
    t_s: float
    state: Literal["tentative", "confirmed", "lost", "removed"] = "confirmed"
    embedding: np.ndarray | None = None
    extra: dict = field(default_factory=dict)


@dataclass
class Form:
    """One pre-encoded payload for the selection step (CONTRACTS §1 / §5b)."""

    kind: Literal["fact", "crop", "thumb", "full"]
    bytes: int
    quality: int
    w: int
    h: int
    uri: Optional[str] = None
    region: Optional[Box] = None  # image region covered by this payload (None = whole frame)

    def to_dict(self) -> dict:
        d = {"kind": self.kind, "bytes": int(self.bytes), "quality": int(self.quality), "w": int(self.w), "h": int(self.h)}
        if self.uri:
            d["uri"] = self.uri
        if self.region is not None:
            d["region"] = [float(v) for v in self.region]
        return d


@dataclass
class Sighting:
    """One record per real object per continuous pass. Events are emitted as dicts (CONTRACTS §1)."""

    sighting_id: str
    clip_id: str
    label: str
    first_t_s: float
    first_frame_idx: int
    last_t_s: float
    last_frame_idx: int
    box: Box
    best_frame_idx: int
    best_box: Box
    best_score: float
    n_frames: int = 0
    track_ids: list[int] = field(default_factory=list)
    label_scores: dict = field(default_factory=dict)
    subtype: Optional[str] = None
    unknownness: float = 0.0
    realness: float = 0.0
    unknown_real: bool = False
    frames: list[int] = field(default_factory=list)  # frames where the object was tracked
    # lookout-report fields (literature.md §7; optional in CONTRACTS)
    bearing_deg: Optional[float] = None   # relative bearing from the camera axis (box centre x, camera HFOV)
    size_class: Optional[str] = None      # tiny | small | medium | large (sqrt(box area) in px)
    aspect: Optional[str] = None          # bow_stern | quarter | beam (box w/h)
    closing: Optional[str] = None         # closing | opening | steady (sign of box-area growth)
    key_q: float = 0.0                    # key-frame quality score of best_frame_idx
    tentative: bool = False               # reported from a single (or unconfirmed) detection; cleared on confirmation

    def event(self, kind: str, t_s: float, frame_idx: int, value: Optional[float] = None,
              forms: Optional[list[Form]] = None, reason: Optional[str] = None) -> dict:
        ev = {
            "sighting_id": self.sighting_id,
            "clip_id": self.clip_id,
            "event": kind,
            "t_s": float(t_s),
            "frame_idx": int(frame_idx),
            "label": self.label,
            "label_scores": {k: float(v) for k, v in self.label_scores.items()},
            "subtype": self.subtype,
            "unknownness": float(self.unknownness),
            "realness": float(self.realness),
            "unknown_real": bool(self.unknown_real),
            "box": [float(v) for v in self.box],
            "best_frame_idx": int(self.best_frame_idx),
            "best_box": [float(v) for v in self.best_box],
            "best_score": float(self.best_score),
            "first_t_s": float(self.first_t_s),
            "last_t_s": float(self.last_t_s),
            "n_frames": int(self.n_frames),
            "track_ids": list(self.track_ids),
            "bearing_deg": None if self.bearing_deg is None else float(self.bearing_deg),
            "size_class": self.size_class,
            "aspect": self.aspect,
            "closing": self.closing,
            "key_q": float(self.key_q),
            "tentative": bool(self.tentative),
        }
        if value is not None:
            ev["value"] = float(value)
        if forms is not None:
            ev["forms"] = [f.to_dict() for f in forms]
        if reason:
            ev["reason"] = reason
        return ev


def box_iou(a: Box, b: Box) -> float:
    ax2, ay2 = a[0] + a[2], a[1] + a[3]
    bx2, by2 = b[0] + b[2], b[1] + b[3]
    iw = max(0.0, min(ax2, bx2) - max(a[0], b[0]))
    ih = max(0.0, min(ay2, by2) - max(a[1], b[1]))
    inter = iw * ih
    union = a[2] * a[3] + b[2] * b[3] - inter
    return inter / union if union > 0 else 0.0


def xyxy_to_xywh(b) -> Box:
    return [float(b[0]), float(b[1]), float(b[2] - b[0]), float(b[3] - b[1])]


def xywh_to_xyxy(b) -> list[float]:
    return [float(b[0]), float(b[1]), float(b[0] + b[2]), float(b[1] + b[3])]
