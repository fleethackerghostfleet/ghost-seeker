"""ByteTrack adapter (``supervision.ByteTrack``) implementing the ``Tracker`` protocol."""
from __future__ import annotations

import numpy as np

from ..types import Detection, Track


class ByteTrackTracker:
    def __init__(self, fps: float = 1.0, track_thresh: float = 0.25, lost_s: float = 10.0, match_thresh: float = 0.8, min_hits: int = 1):
        import supervision as sv

        self.sv = sv
        # supervision's ByteTrack: lost_track_buffer is in frames; minimum_consecutive_frames gates "confirmed"
        self.bt = sv.ByteTrack(track_activation_threshold=track_thresh, lost_track_buffer=max(1, round(lost_s * fps)),
                               minimum_matching_threshold=match_thresh, frame_rate=max(1, round(fps)),
                               minimum_consecutive_frames=min_hits)
        self.name = f"bytetrack(thr={track_thresh},lost={lost_s}s,hits={min_hits})"

    def update(self, dets: list[Detection], image_bgr: np.ndarray, frame_idx: int, t_s: float) -> list[Track]:
        if dets:
            xyxy = np.array([[d.box[0], d.box[1], d.box[0] + d.box[2], d.box[1] + d.box[3]] for d in dets], np.float32)
            d = self.sv.Detections(xyxy=xyxy, confidence=np.array([d.score for d in dets], np.float32),
                                   class_id=np.array([d.label_id if d.label_id is not None else 0 for d in dets]),
                                   data={"label": np.array([d.label for d in dets])})
        else:
            d = self.sv.Detections.empty()
        out = self.bt.update_with_detections(d)
        tracks = []
        for i in range(len(out)):
            x1, y1, x2, y2 = out.xyxy[i].tolist()
            tracks.append(Track(track_id=int(out.tracker_id[i]), box=[x1, y1, x2 - x1, y2 - y1], score=float(out.confidence[i]),
                                label=str(out.data["label"][i]) if "label" in out.data else "object", frame_idx=frame_idx, t_s=t_s,
                                state="confirmed"))
        return tracks
