"""Source-local novelty of OPEN events, distinct from tracker/sighting identity."""
from __future__ import annotations

import math

from ..types import box_iou


class NewContactDetector:
    def __init__(self, memory_s=30.0, iou_thr=.3, dist_diag=.5):
        if (not all(math.isfinite(v) for v in (memory_s, iou_thr, dist_diag))
                or memory_s < 0 or not 0 <= iou_thr <= 1 or dist_diag < 0):
            raise ValueError("invalid new-contact configuration")
        self.memory_s, self.iou_thr, self.dist_diag = memory_s, iou_thr, dist_diag

    def near(self, a, b):
        distance = math.hypot(a[0] + a[2] / 2 - b[0] - b[2] / 2,
                              a[1] + a[3] / 2 - b[1] - b[3] / 2)
        return (box_iou(a, b) >= self.iou_thr or
                distance <= self.dist_diag * max(math.hypot(*a[2:]), math.hypot(*b[2:])))

    def is_new(self, event, states):
        if event["event"] != "open":
            return False
        sid, first_t = event["sighting_id"], event["first_t_s"]
        own = states.get(sid)
        trajectory = getattr(own, "traj", ())
        box = trajectory[0][2] if trajectory else event.get("box") or event["best_box"]
        for other_id, other in states.items():
            if (other_id == sid or other.status not in {"open", "closed"}
                    or other.s.first_t_s >= first_t):
                continue
            # Confirmation can lag first observation; don't use a neighbour's future box.
            observations = getattr(other, "traj", ()) or [(0, other.last_seen_t, other.s.box, 0)]
            prior = next((ob for ob in reversed(observations) if ob[1] <= first_t), None)
            if prior is not None and first_t - prior[1] <= self.memory_s and self.near(box, prior[2]):
                return False
        return True
