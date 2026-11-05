"""Tracker adapters over boxmot (ByteTrack, OC-SORT, BoT-SORT with camera-motion compensation, BoT-SORT+ReID).

Tracker parameters that are really *times* (how long a lost track is kept) are given in seconds and converted
with the effective update rate `fps / detect_every`, so a variant behaves the same at 1 Hz and 5 Hz detection.
Output boxes are pixel [x,y,w,h]; `state` is 'confirmed' for boxmot's emitted tracks.

Tentative tracks (`tentative_thresh`): every boxmot variant except OC-SORT needs a second detection before it emits
a track, and ByteTrack/BoT-SORT drop an unconfirmed track after a single miss, so an object detected in one frame
(or at frames k and k+2) is never seen by the aggregator. With `tentative_thresh` set, detections at or above it that
no emitted track covers are passed through as `state='tentative'` tracks with negative ids; consecutive leftovers are
linked by a per-size centroid gate (`tentative_gate_diag` box diagonals) and kept for `tentative_keep_s`. The
aggregator decides whether/when a tentative-only object is reported (AggregatorParams.tentative_after_s).

Jump guard (`max_jump_diag`): boxmot's association gates are frame-relative (centroid: `centroid_max_distance` of the
frame diagonal, ~330 px at 1080p), so among small adjacent boats a lost track is re-acquired on a *neighbour* and one
id hops between objects. With `max_jump_diag` set, an emitted track whose centre moved more than that many box
diagonals since it was last emitted (however long it was lost) is given a fresh id: a per-size association distance
on top of any boxmot variant.
"""
from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import Optional

import numpy as np

from ..types import Detection, Track, box_iou, xywh_to_xyxy, xyxy_to_xywh

VARIANTS = ("bytetrack", "ocsort", "botsort", "botsort_cmc", "botsort_reid")


@dataclass
class TrackerParams:
    variant: str = "bytetrack"
    fps: float = 1.0                 # effective tracker update rate (frames fed per second)
    max_lost_s: float = 3.0          # keep a lost track this long before removing it
    min_hits: int = 1                # confirmation is handled by the aggregator, keep the tracker permissive
    det_thresh: float = 0.25         # high-confidence threshold (first association stage)
    low_thresh: float = 0.05         # low-confidence second stage (ByteTrack / BoT-SORT)
    new_track_thresh: float = 0.25
    match_thresh: float = 0.8
    iou_threshold: float = 0.2
    cmc_method: str = "sof"          # sparse optical flow; 'ecc' / 'orb' / 'sift' also supported by boxmot
    asso_func: str = "iou"           # 'iou' | 'giou' | 'diou' | 'ciou' | 'hmiou' | 'centroid' (boxmot association)
    centroid_max_distance: float = 0.15   # for asso_func='centroid': max centre distance as a fraction of the frame
    reid_weights: Optional[str] = None
    tentative_thresh: Optional[float] = None   # pass unmatched detections >= this through as 'tentative' tracks (None = off)
    tentative_keep_s: float = 2.0              # how long a tentative id survives without a detection
    tentative_gate_diag: float = 1.5           # per-size link gate: centre distance <= this many box diagonals
    tentative_cover_iou: float = 0.3           # a detection overlapping an emitted track this much is 'covered'
    max_jump_diag: Optional[float] = None      # re-id a track whose centre jumps more than this many box diagonals (None = off)
    extra: dict = field(default_factory=dict)

    @property
    def max_lost_frames(self) -> int:
        return max(1, int(round(self.max_lost_s * self.fps)))


class BoxmotTracker:
    def __init__(self, params: TrackerParams):
        self.uses_image = params.variant in ('botsort_cmc', 'botsort_reid')
        import boxmot

        p = params
        self.params = p
        self.name = p.variant
        common = dict(min_hits=p.min_hits, iou_threshold=p.iou_threshold, max_age=p.max_lost_frames,
                      asso_func=p.asso_func, centroid_max_distance=p.centroid_max_distance)
        if p.variant == "bytetrack":
            cfg = boxmot.ByteTrackConfig(track_thresh=p.det_thresh, min_conf=p.low_thresh, match_thresh=p.match_thresh,
                                         max_lost_frames=p.max_lost_frames, **common, **p.extra)
            self.t = boxmot.ByteTrack(cfg)
        elif p.variant == "ocsort":
            cfg = boxmot.OcSortConfig(det_thresh=p.det_thresh, min_conf=p.low_thresh, **common, **p.extra)
            self.t = boxmot.OcSort(cfg)
        elif p.variant in ("botsort", "botsort_cmc", "botsort_reid"):
            use_cmc = p.variant in ("botsort_cmc", "botsort_reid")
            use_emb = p.variant == "botsort_reid"
            cfg = boxmot.BotSortConfig(track_high_thresh=p.det_thresh, track_low_thresh=p.low_thresh,
                                       new_track_thresh=p.new_track_thresh, match_thresh=p.match_thresh,
                                       max_lost_frames=p.max_lost_frames, use_cmc=use_cmc, cmc_method=p.cmc_method,
                                       use_embeddings=use_emb, det_thresh=p.low_thresh, **common, **p.extra)
            reid = None
            if use_emb and p.reid_weights:
                reid = boxmot.ReIDConfig(weights=p.reid_weights)
            self.t = boxmot.BotSort(cfg, reid=reid) if reid else boxmot.BotSort(cfg)
        else:
            raise ValueError(f"unknown tracker variant {p.variant!r}; choose from {VARIANTS}")
        self._labels: dict[int, str] = {}
        self._tent: dict[int, tuple[list[float], float]] = {}   # tentative id -> (box xywh, last t_s)
        self._next_tent = -1
        self._last: dict[int, tuple[list[float], int]] = {}      # boxmot id -> (box xywh, frame_idx) for the jump guard
        self._remap: dict[int, int] = {}                          # boxmot id -> emitted id (after a jump)
        self._next_id = 1_000_000

    def update(self, dets: list[Detection], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[Track]:
        if dets:
            arr = np.array([xywh_to_xyxy(d.box) + [d.score, d.label_id if d.label_id is not None else 0] for d in dets], dtype=np.float32)
        else:
            arr = np.zeros((0, 6), dtype=np.float32)
        out = self.t.update(arr, image_bgr)
        tracks: list[Track] = []
        for row in (np.asarray(out) if out is not None and len(out) else []):
            x1, y1, x2, y2, tid, conf, cls = row[:7]
            det_ind = int(row[7]) if len(row) > 7 and row[7] >= 0 else -1
            label = dets[det_ind].label if 0 <= det_ind < len(dets) else self._labels.get(int(tid), "vessel")
            self._labels[int(tid)] = label
            box = xyxy_to_xywh([x1, y1, x2, y2])
            tid = self._guard_jump(int(tid), box, frame_idx)
            tracks.append(Track(track_id=tid, box=box, score=float(conf), label=label,
                                frame_idx=frame_idx, t_s=t_s, state="confirmed", extra={"det_ind": det_ind, "cls": int(cls)}))
        if self.params.tentative_thresh is not None:
            tracks.extend(self._tentative(dets, tracks, frame_idx, t_s))
        return tracks

    # ------------------------------------------------------------------ jump guard (per-size association distance)
    def _guard_jump(self, tid: int, box: list[float], frame_idx: int) -> int:
        k = self.params.max_jump_diag
        if k is None:
            return tid
        prev = self._last.get(tid)
        self._last[tid] = (list(box), frame_idx)
        if prev is not None:
            pb, pf = prev
            dist = float(np.hypot(box[0] + box[2] / 2 - pb[0] - pb[2] / 2, box[1] + box[3] / 2 - pb[1] - pb[3] / 2))
            diag = 0.5 * (float(np.hypot(box[2], box[3])) + float(np.hypot(pb[2], pb[3])))
            if dist > k * diag:
                self._remap[tid], self._next_id = self._next_id, self._next_id + 1
        return self._remap.get(tid, tid)

    # ------------------------------------------------------------------ tentative pass-through
    def _tentative(self, dets: list[Detection], emitted: list[Track], frame_idx: int, t_s: float) -> list[Track]:
        p = self.params
        boxes = [tr.box for tr in emitted]
        used = {tr.extra.get("det_ind", -1) for tr in emitted}
        # an emitted track that now sits on a tentative object takes it over (the aggregator merges the ids)
        for tid in [tid for tid, (b, _t) in self._tent.items() if any(box_iou(b, eb) >= p.tentative_cover_iou for eb in boxes)]:
            del self._tent[tid]
        for tid in [tid for tid, (_b, lt) in self._tent.items() if t_s - lt > p.tentative_keep_s]:
            del self._tent[tid]
        left = [(i, d) for i, d in enumerate(dets)
                if d.score >= p.tentative_thresh and i not in used
                and not any(box_iou(d.box, eb) >= p.tentative_cover_iou for eb in boxes)]
        out: list[Track] = []
        if not left:
            return out
        # greedy nearest-first linking to live tentative ids, gated per object size
        pairs = []
        for li, (_i, d) in enumerate(left):
            cx, cy = d.box[0] + d.box[2] / 2, d.box[1] + d.box[3] / 2
            diag = float(np.hypot(d.box[2], d.box[3]))
            for tid, (b, _lt) in self._tent.items():
                dist = float(np.hypot(cx - b[0] - b[2] / 2, cy - b[1] - b[3] / 2))
                gate = p.tentative_gate_diag * 0.5 * (diag + float(np.hypot(b[2], b[3])))
                if dist <= gate:
                    pairs.append((dist / max(gate, 1e-6), li, tid))
        pairs.sort()
        assigned: dict[int, int] = {}
        taken: set[int] = set()
        for _c, li, tid in pairs:
            if li in assigned or tid in taken:
                continue
            assigned[li], taken = tid, taken | {tid}
        for li, (i, d) in enumerate(left):
            tid = assigned.get(li)
            if tid is None:
                tid, self._next_tent = self._next_tent, self._next_tent - 1
            self._tent[tid] = (list(d.box), t_s)
            out.append(Track(track_id=tid, box=list(d.box), score=float(d.score), label=d.label, frame_idx=frame_idx, t_s=t_s,
                             state="tentative", embedding=d.embedding, extra={"det_ind": i, "cls": int(d.label_id or 0)}))
        return out


def make_tracker(variant: str, fps: float, **kw) -> BoxmotTracker:
    return BoxmotTracker(TrackerParams(variant=variant, fps=fps, **kw))
