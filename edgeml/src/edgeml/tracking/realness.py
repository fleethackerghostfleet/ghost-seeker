"""Trajectory evidence, not a semantic classifier: stationary contacts are allowed."""
from __future__ import annotations

import numpy as np


def trajectory_realness(traj, min_hits: int = 3, min_duration_s: float = 2.0) -> float:
    """Hard persistence gate, then size CV and box-normalised constant-velocity residual.

    Duplicate observations of a frame are not independent hits. Missing/invalid boxes and
    non-finite inputs cannot establish realness. Camera motion and occlusion can lower it.
    """
    rows = {int(f): (t, b) for f, t, b, _ in traj}
    if len(rows) < max(2, min_hits):
        return 0.0
    ordered = sorted(rows.values(), key=lambda r: r[0])
    ts = np.asarray([r[0] for r in ordered], dtype=float)
    boxes = np.asarray([r[1] for r in ordered], dtype=float)
    if not np.isfinite(ts).all() or not np.isfinite(boxes).all() or (boxes[:, 2:] <= 0).any():
        return 0.0
    ts = ts - ts[0]
    if ts[-1] < min_duration_s or ts[-1] <= 0:
        return 0.0
    sizes = np.sqrt(boxes[:, 2] * boxes[:, 3])
    cv = float(sizes.std() / sizes.mean())
    centres = boxes[:, :2] + boxes[:, 2:] / 2
    design = np.column_stack([np.ones(len(ts)), ts])
    fitted = design @ np.linalg.lstsq(design, centres, rcond=None)[0]
    residual = float(np.sqrt(np.mean(np.sum((centres - fitted) ** 2, axis=1) / sizes ** 2)))
    return float(np.exp(-((cv / .5) ** 2 + (residual / .75) ** 2)))
