"""Tracker adapters over boxmot (ByteTrack, OC-SORT, BoT-SORT with camera-motion compensation, BoT-SORT+ReID).

Tracker parameters that are really *times* (how long a lost track is kept) are given in seconds and converted
with the effective update rate `fps / detect_every`, so a variant behaves the same at 1 Hz and 5 Hz detection.
Output boxes are pixel [x,y,w,h]; `state` is 'confirmed' for boxmot's emitted tracks.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

import numpy as np

from ..types import Detection, Track, xywh_to_xyxy, xyxy_to_xywh

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

    def update(self, dets: list[Detection], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[Track]:
        if dets:
            arr = np.array([xywh_to_xyxy(d.box) + [d.score, d.label_id if d.label_id is not None else 0] for d in dets], dtype=np.float32)
        else:
            arr = np.zeros((0, 6), dtype=np.float32)
        out = self.t.update(arr, image_bgr)
        tracks: list[Track] = []
        if out is None or len(out) == 0:
            return tracks
        for row in np.asarray(out):
            x1, y1, x2, y2, tid, conf, cls = row[:7]
            det_ind = int(row[7]) if len(row) > 7 and row[7] >= 0 else -1
            label = dets[det_ind].label if 0 <= det_ind < len(dets) else self._labels.get(int(tid), "vessel")
            self._labels[int(tid)] = label
            tracks.append(Track(track_id=int(tid), box=xyxy_to_xywh([x1, y1, x2, y2]), score=float(conf), label=label,
                                frame_idx=frame_idx, t_s=t_s, state="confirmed", extra={"det_ind": det_ind, "cls": int(cls)}))
        return tracks


def make_tracker(variant: str, fps: float, **kw) -> BoxmotTracker:
    return BoxmotTracker(TrackerParams(variant=variant, fps=fps, **kw))
