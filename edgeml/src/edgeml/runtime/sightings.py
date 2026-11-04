"""Minimal per-track sighting emitter used by the workstream-D end-to-end CPU runs.

It implements the ``Aggregator`` protocol just enough to exercise the edge budget: one sighting per track, embedding +
zero-shot subtype computed **once at open and on key-frame updates** (not per frame), and ``forms`` encoded only for
open/update events. The reference aggregator with relinking lives in workstream A; this one is deliberately small.
"""
from __future__ import annotations

import time

import numpy as np

from ..forms import DEFAULT_FORMS, crop_with_context, encode_forms
from ..types import Track


class TrackSightings:
    def __init__(self, clip_id: str, embedder=None, classifier=None, close_after_s: float = 5.0, keyframe_area_ratio: float = 1.5,
                 keyframe_every_s: float = 30.0, score_jump: float = 0.2, forms=DEFAULT_FORMS, crop_context: float = 1.5):
        self.clip_id = clip_id
        self.embedder, self.classifier = embedder, classifier
        self.close_after_s, self.area_ratio, self.keyframe_every_s, self.score_jump = close_after_s, keyframe_area_ratio, keyframe_every_s, score_jump
        self.forms, self.crop_context = forms, crop_context
        self.open: dict[int, dict] = {}
        self.n_sightings = 0
        self.stats = {"embed_calls": 0, "embed_ms": 0.0, "forms_ms": 0.0, "forms_calls": 0, "events": 0}

    def _embed(self, image_bgr, track: Track) -> dict:
        if self.embedder is None:
            return {}
        crop, _ = crop_with_context(image_bgr, track.box, self.crop_context)
        t0 = time.perf_counter()
        emb = self.embedder.embed_crops([crop])
        out = {"embedding": emb[0]}
        if self.classifier is not None:
            r = self.classifier.classify(emb)[0]
            out.update({"subtype": r["subtype"], "subtype_prob": r["prob"], "unknownness": r["unknownness"]})
        self.stats["embed_calls"] += 1
        self.stats["embed_ms"] += (time.perf_counter() - t0) * 1e3
        return out

    def _event(self, kind: str, s: dict, track: Track | None, image_bgr, frame_idx: int, t_s: float) -> dict:
        ev = {"sighting_id": s["sighting_id"], "clip_id": self.clip_id, "event": kind, "t_s": t_s, "frame_idx": frame_idx,
              "label": s["label"], "label_scores": {s["label"]: s["best_score"]}, "subtype": s.get("subtype"),
              "unknownness": s.get("unknownness"), "box": [round(v, 1) for v in (track.box if track else s["best_box"])],
              "best_frame_idx": s["best_frame_idx"], "best_box": [round(v, 1) for v in s["best_box"]], "best_score": s["best_score"],
              "first_t_s": s["first_t_s"], "last_t_s": s["last_t_s"], "n_frames": s["n_frames"], "track_ids": [s["track_id"]]}
        if kind in ("open", "update") and track is not None:
            unk = s.get("unknownness") or 0.0
            area = track.box[2] * track.box[3] / (image_bgr.shape[0] * image_bgr.shape[1])
            ev["value"] = float(np.clip(0.5 * s["best_score"] + 0.3 * unk + 0.2 * min(1.0, area * 50), 0, 1))
            fact = {k: ev[k] for k in ("sighting_id", "clip_id", "event", "t_s", "label", "subtype", "unknownness", "box", "best_score")}
            t0 = time.perf_counter()
            ev["forms"], _ = encode_forms(image_bgr, track.box, fact, self.forms)
            self.stats["forms_ms"] += (time.perf_counter() - t0) * 1e3
            self.stats["forms_calls"] += 1
            for f in ev["forms"]:
                f.pop("payload", None)
        self.stats["events"] += 1
        return ev

    def update(self, tracks: list[Track], image_bgr, frame_idx: int, t_s: float) -> list[dict]:
        events = []
        seen = set()
        for tr in tracks:
            seen.add(tr.track_id)
            s = self.open.get(tr.track_id)
            if s is None:
                self.n_sightings += 1
                s = {"sighting_id": f"{self.clip_id}:{self.n_sightings:04d}", "track_id": tr.track_id, "label": tr.label, "best_score": tr.score,
                     "best_box": tr.box, "best_frame_idx": frame_idx, "first_t_s": t_s, "last_t_s": t_s, "n_frames": 1,
                     "key_area": tr.box[2] * tr.box[3], "key_t_s": t_s}
                s.update(self._embed(image_bgr, tr))
                self.open[tr.track_id] = s
                events.append(self._event("open", s, tr, image_bgr, frame_idx, t_s))
                continue
            s["n_frames"] += 1
            s["last_t_s"] = t_s
            material = False
            if tr.score > s["best_score"] + self.score_jump:
                material = True
            if tr.score >= s["best_score"]:
                s["best_score"], s["best_box"], s["best_frame_idx"] = tr.score, tr.box, frame_idx
            area = tr.box[2] * tr.box[3]
            if area >= self.area_ratio * s["key_area"] or area <= s["key_area"] / self.area_ratio or t_s - s["key_t_s"] >= self.keyframe_every_s:
                material = True
            if material:
                s["key_area"], s["key_t_s"] = area, t_s
                new = self._embed(image_bgr, tr)
                if new.get("subtype") != s.get("subtype") or material:
                    s.update(new)
                    events.append(self._event("update", s, tr, image_bgr, frame_idx, t_s))
        for tid, s in list(self.open.items()):
            if tid not in seen and t_s - s["last_t_s"] >= self.close_after_s:
                events.append(self._event("close", s, None, image_bgr, frame_idx, t_s))
                del self.open[tid]
        return events

    def flush(self, t_s: float) -> list[dict]:
        events = [self._event("close", s, None, None, -1, t_s) for s in self.open.values()]
        self.open.clear()
        return events
