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
        self.history = {}

    @staticmethod
    def _observations(state):
        trajectory = getattr(state, "traj", ()) or [(0, state.last_seen_t, state.s.box, 0)]
        return [(ob[1], tuple(ob[2])) for ob in trajectory]

    def observe(self, states, t_s):
        anchor = min([t_s] + [st.s.first_t_s for st in states.values() if st.status == "pending"])
        cutoff = anchor - self.memory_s
        for sid, state in states.items():
            if state.status in {"open", "closed"}:
                observations = [ob for ob in self._observations(state) if ob[0] >= cutoff]
                if observations:
                    self.history[sid] = (state.s.first_t_s, observations)
        self.history = {sid: (first, [ob for ob in observations if ob[0] >= cutoff])
                        for sid, (first, observations) in self.history.items() if observations[-1][0] >= cutoff}

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
        candidates = dict(self.history)
        for other_id, other in states.items():
            if other.status in {"open", "closed"}:
                candidates[other_id] = (other.s.first_t_s, self._observations(other))
            else:
                candidates.pop(other_id, None)
        for other_id, (other_first, observations) in candidates.items():
            if other_id == sid or other_first >= first_t:
                continue
            prior = next((ob for ob in reversed(observations) if ob[0] <= first_t), None)
            if prior is not None and first_t - prior[0] <= self.memory_s and self.near(box, prior[1]):
                return False
        return True
