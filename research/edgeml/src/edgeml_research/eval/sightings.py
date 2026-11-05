"""Sighting metrics (CONTRACTS §5).

Matching: a sighting matches the GT track with the most frames at IoU≥0.3 over the sighting's frames, if that
covers ≥30% of the sighting's frames. Metrics over a sequence (or a set of sequences):
  object_recall      GT tracks with ≥1 matched sighting / GT tracks
  duplicates_per_obj mean over GT tracks of (matched sightings − 1)+   (0 for unmatched tracks)
  false_per_min      unmatched sightings / minutes of video
  ttfr_s             median/mean seconds from the GT track's first visible frame to the first `open` of a matched sighting
  reports_per_obj    mean over matched GT tracks of open+update events of its sightings
"""
from __future__ import annotations

from collections import defaultdict
from typing import Iterable, Optional

import numpy as np

from edgeml.types import box_iou


def match_sightings(sighting_tracks: Iterable[tuple[str, int, list[float], float]], gt_tracks: dict[int, list[tuple[int, list[float]]]],
                    iou_thr: float = 0.3, cover: float = 0.3) -> dict[str, Optional[int]]:
    gt_at: dict[int, dict[int, list[float]]] = defaultdict(dict)
    for oid, rows in gt_tracks.items():
        for fr, box in rows:
            gt_at[fr][oid] = box
    s_frames: dict[str, list[tuple[int, list[float]]]] = defaultdict(list)
    for sid, fr, box, _sc in sighting_tracks:
        s_frames[sid].append((fr, box))
    out: dict[str, Optional[int]] = {}
    for sid, rows in s_frames.items():
        votes: dict[int, int] = defaultdict(int)
        for fr, box in rows:
            best, best_iou = None, iou_thr
            for oid, gb in gt_at.get(fr, {}).items():
                i = box_iou(box, gb)
                if i >= best_iou:
                    best, best_iou = oid, i
            if best is not None:
                votes[best] += 1
        if votes:
            oid, n = max(votes.items(), key=lambda kv: kv[1])
            out[sid] = oid if n >= cover * len(rows) else None
        else:
            out[sid] = None
    return out


def sighting_metrics(events: list[dict], sighting_tracks: list[tuple[str, int, list[float], float]],
                     gt_tracks: dict[int, list[tuple[int, list[float]]]], fps: float, n_frames: int) -> dict:
    m = match_sightings(sighting_tracks, gt_tracks)
    sids = {e["sighting_id"] for e in events}
    # sightings with trajectories but no events (shouldn't happen) are ignored; events without traj count as unmatched
    for sid in sids:
        m.setdefault(sid, None)
    by_obj: dict[int, list[str]] = defaultdict(list)
    for sid, oid in m.items():
        if oid is not None:
            by_obj[oid].append(sid)
    n_gt = len(gt_tracks)
    matched_objs = [o for o in gt_tracks if o in by_obj]
    dup = [max(0, len(by_obj.get(o, [])) - 1) for o in gt_tracks]
    false_s = [sid for sid, oid in m.items() if oid is None]
    minutes = max(1e-9, n_frames / fps / 60.0)
    first_open: dict[str, float] = {}
    reports: dict[str, int] = defaultdict(int)
    for e in events:
        if e["event"] in ("open", "update"):
            reports[e["sighting_id"]] += 1
        if e["event"] == "open" and e["sighting_id"] not in first_open:
            first_open[e["sighting_id"]] = e["t_s"]
    ttfr = []
    for o in matched_objs:
        t_vis = (min(fr for fr, _ in gt_tracks[o]) - 1) / fps
        opens = [first_open[s] for s in by_obj[o] if s in first_open]
        if opens:
            ttfr.append(max(0.0, min(opens) - t_vis))
    rpo = [sum(reports[s] for s in by_obj[o]) for o in matched_objs]
    return {
        "n_gt_objects": n_gt,
        "n_sightings": len(m),
        "n_events": len(events),
        "n_reports": int(sum(reports.values())),
        "object_recall": (len(matched_objs) / n_gt) if n_gt else None,
        "duplicates_per_object": float(np.mean(dup)) if n_gt else None,
        "false_sightings": len(false_s),
        "false_per_min": len(false_s) / minutes,
        "ttfr_median_s": float(np.median(ttfr)) if ttfr else None,
        "ttfr_mean_s": float(np.mean(ttfr)) if ttfr else None,
        "n_ttfr": len(ttfr),
        "reports_per_object": float(np.mean(rpo)) if rpo else None,
        "matched_sightings": {sid: oid for sid, oid in m.items() if oid is not None},
        "false_sighting_ids": false_s,
        "minutes": minutes,
    }


def aggregate_metrics(per_seq: dict[str, dict]) -> dict:
    """Pool across sequences (object-weighted for per-object metrics, time-weighted for per-minute)."""
    n_gt = sum(v["n_gt_objects"] for v in per_seq.values())
    matched = sum(len({o for o in v["matched_sightings"].values()}) for v in per_seq.values())
    dups = sum(v["duplicates_per_object"] * v["n_gt_objects"] for v in per_seq.values() if v["duplicates_per_object"] is not None)
    minutes = sum(v["minutes"] for v in per_seq.values())
    false_n = sum(v["false_sightings"] for v in per_seq.values())
    ttfr_w = [(v["ttfr_mean_s"], v.get("n_ttfr", 1)) for v in per_seq.values() if v["ttfr_mean_s"] is not None]
    rpo_w = [(v["reports_per_object"], len({o for o in v["matched_sightings"].values()})) for v in per_seq.values() if v["reports_per_object"] is not None]
    return {
        "n_seqs": len(per_seq),
        "n_gt_objects": n_gt,
        "object_recall": matched / n_gt if n_gt else None,
        "duplicates_per_object": dups / n_gt if n_gt else None,
        "false_per_min": false_n / minutes if minutes else None,
        "false_sightings": false_n,
        "ttfr_mean_s": (sum(t * n for t, n in ttfr_w) / max(1, sum(n for _, n in ttfr_w))) if ttfr_w else None,
        "reports_per_object": (sum(r * n for r, n in rpo_w) / max(1, sum(n for _, n in rpo_w))) if rpo_w else None,
        "n_reports": sum(v["n_reports"] for v in per_seq.values()),
        "n_sightings": sum(v["n_sightings"] for v in per_seq.values()),
        "minutes": minutes,
    }
