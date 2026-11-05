"""Sighting aggregator: turns per-frame tracks into watch-stander style reports (CONTRACTS §1, literature.md §3, §7).

* `open`   once a track is confirmed (`min_hits` detections and `min_duration_s` of life). The first good-enough
           frame ships immediately so time-to-first-report is not delayed by waiting for the best shot.
* `update` only on a material change: size_class / aspect / closing change, bearing drift > `bearing_step_deg`,
           label flip, confidence jump > `score_jump`, or a key frame whose quality improves > `keyframe_improve`
           (30 %). Rate-limited by `min_update_interval_s`, capped by `max_updates`.
* `close`  once the object has been gone for `close_after_s`.
* Re-link: a new track appearing within `relink_gap_s` of a sighting losing its track, near where that object was
           heading, of similar size, and looking alike (cosine on crop embeddings, embedded once per track at key-frame
           time) continues the same sighting. Conservative by design: a missed re-link costs one extra report, a wrong
           one merges two contacts.
* Key frame: q = score × sqrt(area) × laplacian_var(crop) × border_margin (normalised to [0,1]); kept so the
           `forms[]` (fact/crop/thumb/full, exact JPEG byte sizes) can be pre-encoded for the selection step (§5b).
* Optional own-ship mask (`static_mask_s`): a track that sits on the same pixels for that long marks a static region;
           new tracks inside it are not reported (railings, masts, deck gear on an onboard camera).
"""
from __future__ import annotations

import json
import math
from dataclasses import asdict, dataclass, field
from typing import Optional

import cv2
import numpy as np

from ..types import Box, Form, Sighting, Track, box_iou
from .embed import ColorHistEmbedder, crop
from .realness import trajectory_realness


@dataclass
class AggregatorParams:
    min_hits: int = 2
    min_duration_s: float = 0.0
    close_after_s: float = 5.0
    relink_gap_s: float = 10.0
    relink_speed_frac: float = 0.05      # allowed drift per second, fraction of frame width
    relink_size_ratio: float = 3.0       # max area ratio between old and new box
    appearance_thr: float = 0.5          # min cosine similarity to re-link (1.0 = never by appearance, 0 = ignore)
    merge_iou: float = 0.5               # a new track overlapping a live sighting's box this much joins it (duplicate id)
    # material-change rules
    score_jump: float = 0.2
    bearing_step_deg: float = 10.0
    keyframe_improve: float = 0.3        # emit update when key-frame quality improves by > this fraction
    min_update_interval_s: float = 3.0
    max_updates: int = 6                 # cap on update events per sighting (open/close not counted)
    # value
    unknown_bonus_age_s: float = 2.0     # minimum age for unknown bonus and trajectory realness
    gate_unknown_bonus_on_realness: bool = False
    unknown_min_hits: int = 3
    unknown_threshold: float = 0.5
    realness_threshold: float = 0.5
    # camera / size geometry
    hfov_deg: float = 90.0
    size_px: tuple = (24.0, 64.0, 160.0)  # sqrt(area) thresholds: tiny | small | medium | large
    closing_rate: float = 0.08           # |log-area growth per s| above this -> closing / opening
    # forms (literature.md §2.3 defaults; WS D sweeps them)
    crop_pad: float = 0.25               # 1.5x box
    crop_min_short: int = 64             # ML crop (q 50)
    crop_hi_min_short: int = 96          # human crop (q 80)
    crop_max_side: int = 320
    thumb_width: int = 320
    full_max_side: int = 1280
    jpeg_q: dict = field(default_factory=lambda: {"crop_lo": 50, "crop_hi": 80, "thumb": 75, "full": 80})
    emit_closed_as_update: bool = True   # re-acquired after a close -> `update` with reason=relink
    static_mask_s: Optional[float] = None  # own-ship mask: suppress tracks static for this long (None = off)


@dataclass
class _State:
    s: Sighting
    status: str = "pending"             # pending | open | closed | masked
    hits: int = 0
    last_seen_t: float = 0.0
    last_seen_frame: int = 0
    last_report_t: float = -1e9
    last_report: dict = field(default_factory=dict)   # fields at the last report, for material-change tests
    n_updates: int = 0
    key_frame: Optional[np.ndarray] = None
    key_crop: Optional[np.ndarray] = None
    emb: Optional[np.ndarray] = None
    label_votes: dict = field(default_factory=dict)
    score_sum: float = 0.0
    area_hist: list = field(default_factory=list)
    traj: list = field(default_factory=list)   # (frame_idx, t_s, box, score)
    velocity: tuple = (0.0, 0.0)
    first_box: Optional[Box] = None


def laplacian_var(img: np.ndarray) -> float:
    if img is None or img.size == 0:
        return 0.0
    g = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY) if img.ndim == 3 else img
    return float(cv2.Laplacian(g, cv2.CV_32F).var())


def _encode(img: np.ndarray, q: int) -> int:
    ok, buf = cv2.imencode(".jpg", img, [int(cv2.IMWRITE_JPEG_QUALITY), int(q)])
    return int(len(buf)) if ok else 0


def _resize_max(img: np.ndarray, max_side: int) -> np.ndarray:
    h, w = img.shape[:2]
    s = min(1.0, max_side / max(h, w))
    return img if s >= 1.0 else cv2.resize(img, (max(1, int(w * s)), max(1, int(h * s))), interpolation=cv2.INTER_AREA)


def crop_region(box: Box, W: int, H: int, pad: float, min_short: int) -> list[float]:
    """Crop window = box × (1+2·pad), grown to at least `min_short` px on the short side, clamped to the frame."""
    x, y, w, h = box
    cw, ch = w * (1 + 2 * pad), h * (1 + 2 * pad)
    short = min(cw, ch)
    if short < min_short:
        k = min_short / max(short, 1.0)
        cw, ch = cw * k, ch * k
    cx, cy = x + w / 2, y + h / 2
    x0, y0 = max(0.0, cx - cw / 2), max(0.0, cy - ch / 2)
    x1, y1 = min(float(W), cx + cw / 2), min(float(H), cy + ch / 2)
    return [x0, y0, max(1.0, x1 - x0), max(1.0, y1 - y0)]


def _cut(img: np.ndarray, r: list[float]) -> np.ndarray:
    x0, y0 = int(r[0]), int(r[1])
    x1, y1 = int(math.ceil(r[0] + r[2])), int(math.ceil(r[1] + r[3]))
    return img[y0:y1, x0:x1]


class SightingAggregator:
    def __init__(self, clip_id: str, params: AggregatorParams | None = None, embedder=None, frame_size: tuple[int, int] | None = None):
        self.clip_id = clip_id
        self.p = params or AggregatorParams()
        self.embedder = embedder if embedder is not None else ColorHistEmbedder()
        self.frame_size = frame_size  # (W, H)
        self.states: dict[str, _State] = {}
        self.track_to_sid: dict[int, str] = {}
        self._n = 0
        self.events: list[dict] = []
        self._static: list[tuple[Box, float]] = []   # own-ship mask regions (box, t_added)
        self._current_frame: Optional[np.ndarray] = None
        self.n_embeds = 0
        self._archived_traj: list[tuple[str, int, Box, float]] = []   # trajectories of expired reported sightings

    # ------------------------------------------------------------------ public API (Aggregator protocol)
    def set_frame(self, image_bgr: np.ndarray) -> None:
        """Optional hook (the pipeline calls it before update) — update() also takes the image directly."""
        self._current_frame = image_bgr

    def update(self, tracks: list[Track], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[dict]:
        if self.frame_size is None and image_bgr is not None:
            self.frame_size = (image_bgr.shape[1], image_bgr.shape[0])
        self._current_frame = image_bgr
        out: list[dict] = []
        seen: set[str] = set()

        # pass 1: tracks already mapped to a sighting; pass 2: new track ids (re-link / merge / open)
        known = [tr for tr in tracks if tr.track_id in self.track_to_sid]
        fresh = [tr for tr in tracks if tr.track_id not in self.track_to_sid]
        for tr in known + fresh:
            sid = self.track_to_sid.get(tr.track_id)
            if sid is None:
                if self._masked(tr.box, t_s):
                    continue
                sid = self._merge_target(tr, frame_idx)
                if sid is None:
                    emb = self._embed_track(tr, image_bgr) if self._relink_candidates(t_s, frame_idx) else None
                    sid = self._relink(tr, emb, t_s, frame_idx)
                if sid is None:
                    sid = self._new_sighting(tr, frame_idx, t_s)
                    self.track_to_sid[tr.track_id] = sid
                else:
                    st = self.states[sid]
                    st.s.track_ids.append(tr.track_id)
                    self.track_to_sid[tr.track_id] = sid
                    if st.status == "closed" and self.p.emit_closed_as_update:
                        st.status = "open"
                        self._observe(st, tr, image_bgr, frame_idx, t_s)
                        out.append(self._report(st, "update", frame_idx, t_s, reason="relink"))
                        seen.add(sid)
                        continue
            st = self.states[sid]
            if sid in seen and tr.score <= st.s.best_score:   # two live tracks on one sighting: keep the better one
                continue
            self._observe(st, tr, image_bgr, frame_idx, t_s)
            seen.add(sid)
            if st.status == "pending":
                if self.p.static_mask_s and self._is_static(st, t_s):
                    st.status = "masked"
                    self._static.append((list(st.s.box), t_s))
                elif st.hits >= self.p.min_hits and (t_s - st.s.first_t_s) >= self.p.min_duration_s:
                    st.status = "open"
                    out.append(self._report(st, "open", frame_idx, t_s))
            elif st.status == "open":
                reason = self._material_change(st, t_s)
                if reason:
                    out.append(self._report(st, "update", frame_idx, t_s, reason=reason))

        # close / expire
        for sid, st in list(self.states.items()):
            if sid in seen:
                continue
            gone = t_s - st.last_seen_t
            if st.status == "open" and gone > self.p.close_after_s:
                st.status = "closed"
                out.append(self._close(st, frame_idx, t_s))
            elif st.status in ("pending", "masked") and gone > self.p.close_after_s:
                self._drop(sid)
            elif st.status == "closed" and gone > max(self.p.relink_gap_s, self.p.close_after_s) * 2:
                self._drop(sid)
        self.events.extend(out)
        return out

    def flush(self, t_s: float) -> list[dict]:
        out = []
        for st in self.states.values():
            if st.status == "open":
                st.status = "closed"
                out.append(self._close(st, st.last_seen_frame, t_s))
        self.events.extend(out)
        return out

    def needs_attention(self, t_s: float, active_for_s: float = 3.0) -> bool:
        """True while something is unconfirmed or just appeared — used by the adaptive detector rate."""
        for st in self.states.values():
            if st.status == "pending" and t_s - st.last_seen_t <= self.p.close_after_s:
                return True
            if st.status == "open" and t_s - st.s.first_t_s <= active_for_s:
                return True
        return False

    def trajectories(self) -> list[tuple[str, int, Box, float]]:
        """(sighting_id, frame_idx, box, score) for every frame of every *reported* sighting (eval matching)."""
        reported = {e["sighting_id"] for e in self.events}
        rows = [r for r in self._archived_traj if r[0] in reported]
        for sid, st in self.states.items():
            if sid not in reported:
                continue
            for f, _t, b, sc in st.traj:
                rows.append((sid, f, b, sc))
        return rows

    def params_dict(self) -> dict:
        return asdict(self.p)

    # ------------------------------------------------------------------ geometry helpers
    def _W(self) -> float:
        return float(self.frame_size[0]) if self.frame_size else 1920.0

    def _H(self) -> float:
        return float(self.frame_size[1]) if self.frame_size else 1080.0

    def bearing(self, box: Box) -> float:
        return ((box[0] + box[2] / 2) / self._W() - 0.5) * self.p.hfov_deg

    def size_class(self, box: Box) -> str:
        s = math.sqrt(max(1.0, box[2] * box[3]))
        t = self.p.size_px
        return "tiny" if s < t[0] else "small" if s < t[1] else "medium" if s < t[2] else "large"

    @staticmethod
    def aspect(box: Box) -> str:
        r = box[2] / max(1.0, box[3])
        return "beam" if r > 2.2 else "quarter" if r > 1.2 else "bow_stern"

    def _growth(self, st: _State) -> float:
        """log-area growth per second over the last few observations."""
        h = st.area_hist
        if len(h) < 2:
            return 0.0
        (t0, a0), (t1, a1) = h[max(0, len(h) - 6)], h[-1]
        if t1 <= t0 or a0 <= 0:
            return 0.0
        return math.log(max(a1, 1.0) / max(a0, 1.0)) / (t1 - t0)

    def closing(self, st: _State) -> str:
        g = self._growth(st)
        return "closing" if g > self.p.closing_rate else "opening" if g < -self.p.closing_rate else "steady"

    def key_quality(self, tr: Track, c: Optional[np.ndarray]) -> float:
        """q = score × sqrt(area) × laplacian_var(crop) × border_margin, each term normalised to [0,1]."""
        W, H = self._W(), self._H()
        size = min(1.0, math.sqrt(max(1.0, tr.box[2] * tr.box[3])) / (0.25 * W))
        lv = laplacian_var(c)
        sharp = lv / (lv + 300.0)
        x, y, w, h = tr.box
        m = min(x, y, W - (x + w), H - (y + h))
        border = min(1.0, max(0.0, m) / (0.05 * W)) * 0.8 + 0.2
        return float(tr.score * size * sharp * border)

    # ------------------------------------------------------------------ internals
    def _masked(self, box: Box, t_s: float | None = None) -> bool:
        if t_s is not None and self.p.static_mask_s:   # a mask region expires once nothing static has been seen there for 2x static_mask_s
            self._static = [(mb, t) for mb, t in self._static if t_s - t <= 2 * self.p.static_mask_s]
        for i, (mb, t) in enumerate(self._static):
            if box_iou(box, mb) > 0.5:
                if t_s is not None:
                    self._static[i] = (mb, t_s)
                return True
        return False

    def _is_static(self, st: _State, t_s: float) -> bool:
        return (st.first_box is not None and t_s - st.s.first_t_s >= self.p.static_mask_s
                and box_iou(st.first_box, st.s.box) > 0.8)

    def _relink_candidates(self, t_s: float, frame_idx: int) -> bool:
        return any(st.last_seen_frame < frame_idx and st.status in ("open", "closed") and 0 < t_s - st.last_seen_t <= self.p.relink_gap_s
                   for st in self.states.values())

    def _merge_target(self, tr: Track, frame_idx: int) -> Optional[str]:
        """A new track that sits on top of a sighting seen this frame (tracker spawned a duplicate id) joins it."""
        best, best_iou = None, self.p.merge_iou
        for sid, st in self.states.items():
            if st.status in ("open", "pending") and st.last_seen_frame == frame_idx:
                i = box_iou(tr.box, st.s.box)
                if i >= best_iou:
                    best, best_iou = sid, i
        return best

    def _embed_track(self, tr: Track, img: np.ndarray) -> Optional[np.ndarray]:
        if tr.embedding is not None:
            return np.asarray(tr.embedding, np.float32)
        if self.embedder is None or img is None:
            return None
        self.n_embeds += 1
        return self.embedder.embed_crops([crop(img, tr.box, self.p.crop_pad)])[0]

    def _new_sighting(self, tr: Track, frame_idx: int, t_s: float) -> str:
        self._n += 1
        sid = f"{self.clip_id}:{self._n:04d}"
        s = Sighting(sighting_id=sid, clip_id=self.clip_id, label=tr.label, first_t_s=t_s, first_frame_idx=frame_idx,
                     last_t_s=t_s, last_frame_idx=frame_idx, box=list(tr.box), best_frame_idx=frame_idx,
                     best_box=list(tr.box), best_score=tr.score, track_ids=[tr.track_id])
        self.states[sid] = _State(s=s, last_seen_t=t_s, last_seen_frame=frame_idx, first_box=list(tr.box))
        return sid

    def _drop(self, sid: str) -> None:
        st = self.states.pop(sid, None)
        if st is not None:
            if st.status == "closed":
                self._archived_traj.extend((sid, f, b, sc) for f, _t, b, sc in st.traj)
            for tid in st.s.track_ids:
                if self.track_to_sid.get(tid) == sid:
                    del self.track_to_sid[tid]

    def _relink(self, tr: Track, emb: Optional[np.ndarray], t_s: float, frame_idx: int) -> Optional[str]:
        W = self._W()
        cx, cy = tr.box[0] + tr.box[2] / 2, tr.box[1] + tr.box[3] / 2
        area = max(1.0, tr.box[2] * tr.box[3])
        best, best_cost = None, 1e9
        for sid, st in self.states.items():
            gap = t_s - st.last_seen_t
            if gap <= 0 or gap > self.p.relink_gap_s or st.last_seen_frame >= frame_idx or st.status not in ("open", "closed"):
                continue
            if st.status == "closed" and not self.p.emit_closed_as_update:
                continue
            ob = st.s.box
            ocx, ocy = ob[0] + ob[2] / 2 + st.velocity[0] * gap, ob[1] + ob[3] / 2 + st.velocity[1] * gap
            diag = math.hypot(ob[2], ob[3])
            allowed = self.p.relink_speed_frac * W * gap + 1.5 * diag
            dist = math.hypot(cx - ocx, cy - ocy)
            if dist > allowed:
                continue
            oarea = max(1.0, ob[2] * ob[3])
            ratio = max(area / oarea, oarea / area)
            if ratio > self.p.relink_size_ratio:
                continue
            sim = 1.0
            if emb is not None and st.emb is not None:
                sim = float(np.dot(emb, st.emb) / (np.linalg.norm(emb) * np.linalg.norm(st.emb) + 1e-9))
                if sim < self.p.appearance_thr:
                    continue
            cost = dist / max(allowed, 1.0) + (1.0 - sim) + 0.1 * math.log(ratio)
            if cost < best_cost:
                best, best_cost = sid, cost
        return best

    def _observe(self, st: _State, tr: Track, img: np.ndarray, frame_idx: int, t_s: float) -> None:
        s = st.s
        dt = t_s - st.last_seen_t
        if dt > 0 and st.hits > 0:
            vx = ((tr.box[0] + tr.box[2] / 2) - (s.box[0] + s.box[2] / 2)) / dt
            vy = ((tr.box[1] + tr.box[3] / 2) - (s.box[1] + s.box[3] / 2)) / dt
            st.velocity = (0.5 * st.velocity[0] + 0.5 * vx, 0.5 * st.velocity[1] + 0.5 * vy)
        st.hits += 1
        st.last_seen_t, st.last_seen_frame = t_s, frame_idx
        s.last_t_s, s.last_frame_idx, s.box, s.n_frames = t_s, frame_idx, list(tr.box), s.n_frames + 1
        s.frames.append(frame_idx)
        st.traj.append((frame_idx, t_s, list(tr.box), tr.score))
        st.label_votes[tr.label] = st.label_votes.get(tr.label, 0.0) + tr.score
        st.score_sum += tr.score
        s.label = max(st.label_votes, key=st.label_votes.get)
        tot = sum(st.label_votes.values()) or 1.0
        s.label_scores = {k: v / tot for k, v in st.label_votes.items()}
        s.unknownness = float(min(1.0, max(0.0, 1.0 - max(s.label_scores.values()) * min(1.0, st.score_sum / max(1, st.hits) + 0.3))))
        st.area_hist.append((t_s, tr.box[2] * tr.box[3]))
        s.bearing_deg, s.size_class, s.aspect, s.closing = self.bearing(tr.box), self.size_class(tr.box), self.aspect(tr.box), self.closing(st)
        if tr.score > s.best_score:
            s.best_score = tr.score
        # key frame (embed once per sighting, at key-frame time)
        c = crop(img, tr.box, self.p.crop_pad) if img is not None else None
        q = self.key_quality(tr, c)
        if q > s.key_q or st.key_frame is None:
            s.key_q, s.best_frame_idx, s.best_box = q, frame_idx, list(tr.box)
            st.key_frame, st.key_crop = img, c
            if tr.embedding is not None:
                st.emb = np.asarray(tr.embedding, np.float32)
            elif self.embedder is not None and c is not None and c.size > 3:
                self.n_embeds += 1
                st.emb = self.embedder.embed_crops([c])[0]

    def _snapshot(self, st: _State) -> dict:
        self._refresh_realness(st)
        s = st.s
        return {"label": s.label, "score": s.best_score, "bearing": s.bearing_deg, "size_class": s.size_class,
                "aspect": s.aspect, "closing": s.closing, "key_q": s.key_q, "unknown_real": s.unknown_real}

    def _material_change(self, st: _State, t_s: float) -> Optional[str]:
        if t_s - st.last_report_t < self.p.min_update_interval_s or st.n_updates >= self.p.max_updates:
            return None
        now, last = self._snapshot(st), st.last_report
        if not last:
            return None
        if self.p.gate_unknown_bonus_on_realness and now["unknown_real"] != last.get("unknown_real", False):
            return "unknown_real"
        if now["label"] != last["label"]:
            return "label_flip"
        if now["score"] - last["score"] > self.p.score_jump:
            return "confidence_jump"
        if now["size_class"] != last["size_class"]:
            return "size_class"
        if now["closing"] != last["closing"] and now["closing"] != "steady":
            return now["closing"]
        if now["aspect"] != last["aspect"]:
            return "aspect"
        if last["bearing"] is not None and abs(now["bearing"] - last["bearing"]) > self.p.bearing_step_deg:
            return "bearing"
        if last["key_q"] > 0 and now["key_q"] > last["key_q"] * (1.0 + self.p.keyframe_improve):
            return "better_keyframe"
        return None

    def _refresh_realness(self, st: _State) -> None:
        st.s.realness = trajectory_realness(st.traj, self.p.unknown_min_hits, self.p.unknown_bonus_age_s)
        st.s.unknown_real = (st.s.unknownness >= self.p.unknown_threshold
                             and st.s.realness > 0 and st.s.realness >= self.p.realness_threshold)

    def _value(self, st: _State) -> float:
        self._refresh_realness(st)
        s = st.s
        size = min(1.0, math.sqrt(max(1.0, s.best_box[2] * s.best_box[3])) / (0.15 * self._W()))
        age = s.last_t_s - s.first_t_s
        if self.p.gate_unknown_bonus_on_realness:
            unk = s.unknownness if s.unknown_real else 0.0
        else:
            unk = s.unknownness if age >= self.p.unknown_bonus_age_s else 0.0
        closing = min(1.0, max(0.0, self._growth(st) / 0.3))
        v = 0.5 * s.best_score + 0.2 * unk + 0.2 * size + 0.1 * closing
        if self.p.gate_unknown_bonus_on_realness and s.unknown_real:
            v = max(v, 0.3 + 0.2 * size)  # at least the confidence contribution of a 0.6 known contact
        return float(min(1.0, max(0.0, v)))

    def _forms(self, st: _State, fact: dict) -> list[Form]:
        p = self.p
        forms = [Form(kind="fact", bytes=len(json.dumps(fact, separators=(",", ":")).encode()), quality=0, w=0, h=0)]
        img = st.key_frame
        if img is None:
            return forms
        H, W = img.shape[:2]
        b = st.s.best_box
        for qk, min_short in (("crop_lo", p.crop_min_short), ("crop_hi", p.crop_hi_min_short)):
            region = crop_region(b, W, H, p.crop_pad, min_short)
            c = _resize_max(_cut(img, region), p.crop_max_side)
            forms.append(Form(kind="crop", bytes=_encode(c, p.jpeg_q[qk]), quality=p.jpeg_q[qk], w=c.shape[1], h=c.shape[0], region=region))
        tw = min(p.thumb_width, W)
        th = max(1, int(H * tw / W))
        thumb = cv2.resize(img, (tw, th), interpolation=cv2.INTER_AREA)
        forms.append(Form(kind="thumb", bytes=_encode(thumb, p.jpeg_q["thumb"]), quality=p.jpeg_q["thumb"], w=tw, h=th))
        full = _resize_max(img, p.full_max_side)
        forms.append(Form(kind="full", bytes=_encode(full, p.jpeg_q["full"]), quality=p.jpeg_q["full"], w=full.shape[1], h=full.shape[0]))
        return forms

    def _report(self, st: _State, kind: str, frame_idx: int, t_s: float, reason: Optional[str] = None) -> dict:
        s = st.s
        value = self._value(st)
        fact = s.event(kind, t_s, frame_idx, value=value, reason=reason)
        forms = self._forms(st, fact)
        ev = s.event(kind, t_s, frame_idx, value=value, forms=forms, reason=reason)
        st.last_report_t, st.last_report = t_s, self._snapshot(st)
        if kind == "update":
            st.n_updates += 1
        return ev

    def _close(self, st: _State, frame_idx: int, t_s: float) -> dict:
        self._refresh_realness(st)
        return st.s.event("close", t_s, frame_idx)
