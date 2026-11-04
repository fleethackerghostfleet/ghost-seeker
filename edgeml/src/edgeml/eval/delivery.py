"""Headline system metric (CONTRACTS §5b): unique GT objects delivered to shore vs link budget.

Events are grouped into fixed windows (default 60 s). Each window's events go through `edgeml.select.greedy`
with the window's byte budget. An object counts as *delivered (image)* when a selected crop/full/thumb form
contains it: the form's region (crop region or the whole key frame) must cover ≥50% of a GT box of that object
on the form's frame. It counts as *delivered (fact)* when a fact of a sighting matched to that object is sent
(matching rule of §5). Also reports bytes per delivered object.
"""
from __future__ import annotations

from collections import defaultdict
from typing import Optional

from ..select import greedy

BUDGETS_60S = {"9.6kbps": 72_000, "64kbps": 480_000, "256kbps": 1_920_000, "2Mbps": 15_000_000}


def _covers(region: Optional[list[float]], gt_box: list[float], W: int, H: int, min_frac: float = 0.5) -> bool:
    if region is None:
        region = [0.0, 0.0, float(W), float(H)]
    rx0, ry0, rx1, ry1 = region[0], region[1], region[0] + region[2], region[1] + region[3]
    gx0, gy0, gx1, gy1 = gt_box[0], gt_box[1], gt_box[0] + gt_box[2], gt_box[1] + gt_box[3]
    iw = max(0.0, min(rx1, gx1) - max(rx0, gx0))
    ih = max(0.0, min(ry1, gy1) - max(ry0, gy0))
    area = max(1e-6, gt_box[2] * gt_box[3])
    return (iw * ih) / area >= min_frac


def delivered_vs_budget(events: list[dict], sighting_match: dict[str, Optional[int]], gt_tracks: dict[int, list[tuple[int, list[float]]]],
                        W: int, H: int, budgets: dict[str, int] | None = None, window_s: float = 60.0, duration_s: Optional[float] = None,
                        selector_kw: Optional[dict] = None) -> dict:
    """selector_kw is forwarded to `edgeml.select.greedy` (ladder_decay, max_bytes_per_object) for the E3 ablation."""
    budgets = budgets or BUDGETS_60S
    selector_kw = selector_kw or {}
    gt_at: dict[int, dict[int, list[float]]] = defaultdict(dict)
    for oid, rows in gt_tracks.items():
        for fr, box in rows:
            gt_at[fr][oid] = box
    windows: dict[int, list[dict]] = defaultdict(list)
    for e in events:
        if e.get("event") == "close" or not e.get("forms"):
            continue
        windows[int(e["t_s"] // window_s)].append(e)
    n_windows = int((duration_s or max((e["t_s"] for e in events), default=0.0)) // window_s) + 1
    out = {"window_s": window_s, "n_windows": n_windows, "n_gt_objects": len(gt_tracks), "budgets": {}}
    for name, b in budgets.items():
        img_objs, fact_objs = set(), set()
        bytes_used = 0
        n_forms = 0
        for w, evs in windows.items():
            sel = greedy(evs, b, **selector_kw)
            bytes_used += sel["bytes_used"]
            n_forms += len(sel["selected"])
            for c in sel["selected"]:
                ev = evs[c["event_idx"]]
                if c["kind"] == "fact":
                    oid = sighting_match.get(ev["sighting_id"])
                    if oid is not None:
                        fact_objs.add(oid)
                else:
                    fr = ev.get("best_frame_idx", ev["frame_idx"])
                    region = c["form"].get("region")
                    # per object, tolerate a 1-frame GT gap (e.g. FVessel labels start at frame 2): prefer the key frame's row
                    gt_here = {**gt_at.get(fr - 1, {}), **gt_at.get(fr + 1, {}), **gt_at.get(fr, {})}
                    for oid, gb in gt_here.items():
                        if _covers(region, gb, W, H):
                            img_objs.add(oid)
        n_img = len(img_objs)
        out["budgets"][name] = {
            "budget_bytes_per_window": b,
            "objects_delivered_image": n_img,
            "objects_delivered_fact": len(fact_objs),
            "objects_delivered_any": len(img_objs | fact_objs),
            "frac_image": n_img / len(gt_tracks) if gt_tracks else None,
            "frac_fact": len(fact_objs) / len(gt_tracks) if gt_tracks else None,
            "bytes_used": bytes_used,
            "bytes_per_delivered_object": (bytes_used / n_img) if n_img else None,
            "n_forms_sent": n_forms,
        }
    return out


def pool_delivery(per_seq: dict[str, dict]) -> dict:
    out: dict = {"budgets": {}}
    names = set()
    for v in per_seq.values():
        names |= set(v["budgets"])
    n_gt = sum(v["n_gt_objects"] for v in per_seq.values())
    for n in sorted(names):
        img = sum(v["budgets"][n]["objects_delivered_image"] for v in per_seq.values() if n in v["budgets"])
        fact = sum(v["budgets"][n]["objects_delivered_fact"] for v in per_seq.values() if n in v["budgets"])
        anyo = sum(v["budgets"][n]["objects_delivered_any"] for v in per_seq.values() if n in v["budgets"])
        by = sum(v["budgets"][n]["bytes_used"] for v in per_seq.values() if n in v["budgets"])
        out["budgets"][n] = {"objects_delivered_image": img, "objects_delivered_fact": fact, "objects_delivered_any": anyo,
                             "frac_image": img / n_gt if n_gt else None, "frac_fact": fact / n_gt if n_gt else None,
                             "frac_any": anyo / n_gt if n_gt else None,
                             "bytes_used": by, "bytes_per_delivered_object": by / img if img else None}
    out["n_gt_objects"] = n_gt
    return out
