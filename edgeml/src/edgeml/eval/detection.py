"""Detection metrics per CONTRACTS §5: COCO mAP50 / mAP50-95 (faster-coco-eval, pycocotools fallback),
class-agnostic "vessel" AP, and recall@score>=tau by box-area bucket (tiny <16², small <32², medium <96², large).

Two GT views of the same images:
  mode="vessel": GT = vessel labels; non-vessel GT (buoy/bird/other) are `ignore` regions so dets on them are
                 neither TP nor FP. Dets = those whose native label maps to "vessel".
  mode="allobj": GT = every labelled object; dets = every maritime det (vessel/buoy/object).
"""
from __future__ import annotations

import contextlib
import io
from typing import Iterable

import numpy as np

from ..detectors.labels import agnostic
from ..types import Detection

AREA_BUCKETS = {"tiny": (0, 16 ** 2), "small": (16 ** 2, 32 ** 2), "medium": (32 ** 2, 96 ** 2), "large": (96 ** 2, 1e10)}


def _coco_api():
    try:
        from faster_coco_eval import COCO, COCOeval_faster as COCOeval  # type: ignore
    except Exception:  # pragma: no cover
        from pycocotools.coco import COCO  # type: ignore
        from pycocotools.cocoeval import COCOeval  # type: ignore
    return COCO, COCOeval


def build_gt(images, mode: str = "vessel") -> dict:
    cats = [{"id": 1, "name": "vessel" if mode == "vessel" else "object"}]
    imgs, anns = [], []
    aid = 1
    for i, im in enumerate(images, start=1):
        imgs.append({"id": i, "file_name": im.image_id, "width": im.width, "height": im.height})
        for box, lab in zip(im.boxes, im.labels):
            a = agnostic(lab)
            if mode == "vessel":
                ignore = a != "vessel"
            else:
                ignore = a is None
            x, y, w, h = box
            anns.append({"id": aid, "image_id": i, "category_id": 1, "bbox": [x, y, w, h], "area": w * h,
                         "iscrowd": 1 if ignore else 0, "ignore": 1 if ignore else 0})
            aid += 1
    return {"images": imgs, "annotations": anns, "categories": cats}


def to_coco_dets(images, dets_by_image: dict[str, list[Detection]], mode: str = "vessel") -> list[dict]:
    out = []
    for i, im in enumerate(images, start=1):
        for d in dets_by_image.get(im.image_id, []):
            a = agnostic(d.label)
            if a is None or (mode == "vessel" and a != "vessel"):
                continue
            out.append({"image_id": i, "category_id": 1, "bbox": [float(v) for v in d.box], "score": float(d.score)})
    return out


def coco_metrics(gt: dict, dets: list[dict]) -> dict:
    COCO, COCOeval = _coco_api()
    with contextlib.redirect_stdout(io.StringIO()):
        cg = COCO()
        cg.dataset = gt
        cg.createIndex()
        if not dets:
            return {"mAP50": 0.0, "mAP50-95": 0.0, "AR100": 0.0, **{f"AR_{k}": 0.0 for k in AREA_BUCKETS}}
        cd = cg.loadRes(dets)
        ev = COCOeval(cg, cd, "bbox")
        ev.params.areaRng = [[0, 1e10]] + [list(v) for v in AREA_BUCKETS.values()]
        ev.params.areaRngLbl = ["all"] + list(AREA_BUCKETS)
        ev.params.maxDets = [1, 10, 100]
        ev.evaluate()
        ev.accumulate()
    p = ev.eval["precision"]  # [T, R, K, A, M]
    r = ev.eval["recall"]  # [T, K, A, M]

    def ap(t=None, a=0):
        x = p[:, :, :, a, -1] if t is None else p[t, :, :, a, -1]
        x = x[x > -1]
        return float(x.mean()) if x.size else 0.0

    def ar(a):
        x = r[:, :, a, -1]
        x = x[x > -1]
        return float(x.mean()) if x.size else float("nan")

    out = {"mAP50": ap(0), "mAP50-95": ap(), "AR100": ar(0)}
    for j, k in enumerate(AREA_BUCKETS, start=1):
        out[f"AR_{k}"] = ar(j)  # AR@[.5:.95] for that bucket
        x = p[0, :, :, j, -1]
        x = x[x > -1]
        out[f"AP50_{k}"] = float(x.mean()) if x.size else float("nan")
    return out


def _iou_matrix(a: np.ndarray, b: np.ndarray) -> np.ndarray:
    if len(a) == 0 or len(b) == 0:
        return np.zeros((len(a), len(b)))
    ax1, ay1, ax2, ay2 = a[:, 0], a[:, 1], a[:, 0] + a[:, 2], a[:, 1] + a[:, 3]
    bx1, by1, bx2, by2 = b[:, 0], b[:, 1], b[:, 0] + b[:, 2], b[:, 1] + b[:, 3]
    iw = np.clip(np.minimum(ax2[:, None], bx2[None]) - np.maximum(ax1[:, None], bx1[None]), 0, None)
    ih = np.clip(np.minimum(ay2[:, None], by2[None]) - np.maximum(ay1[:, None], by1[None]), 0, None)
    inter = iw * ih
    return inter / (a[:, 2:3] * a[:, 3:4] + (b[:, 2] * b[:, 3])[None] - inter + 1e-9)


def _match_image(im, dets, mode: str, iou_thr: float):
    """Greedy score-ordered matching once per image. Because dets above any tau are a prefix of the
    score-sorted list, the greedy result for every tau is a prefix of this one. Returns
    (scores desc, kind per det: 1=TP, 0=FP, -1=ignored, matched-gt index per det or -1, gt areas, gt ignore mask)."""
    g_boxes, g_ign = [], []
    for box, lab in zip(im.boxes, im.labels):
        a = agnostic(lab)
        g_boxes.append(box)
        g_ign.append(a != "vessel" if mode == "vessel" else a is None)
    g = np.array(g_boxes, dtype=np.float64).reshape(-1, 4)
    g_ign = np.array(g_ign, dtype=bool)
    ds = [d for d in dets if agnostic(d.label) is not None and (mode != "vessel" or agnostic(d.label) == "vessel")]
    ds.sort(key=lambda d: -d.score)
    scores = np.array([d.score for d in ds], dtype=np.float64)
    d = np.array([x.box for x in ds], dtype=np.float64).reshape(-1, 4)
    iou = _iou_matrix(d, g)
    matched_g = np.zeros(len(g), dtype=bool)
    kind = np.zeros(len(d), dtype=np.int8)
    gidx = np.full(len(d), -1, dtype=np.int64)
    for i in range(len(d)):
        if len(g):
            cand = np.where(~matched_g & (iou[i] >= iou_thr))[0]
            if len(cand):
                bj = cand[np.argmax(iou[i, cand])]
                matched_g[bj] = True
                gidx[i] = bj
                kind[i] = 0 if g_ign[bj] else 1
                if g_ign[bj]:
                    kind[i] = -1
                continue
            if (iou[i, g_ign] >= iou_thr).any():
                kind[i] = -1  # overlaps an ignore region: neither TP nor FP
                continue
        kind[i] = 0
    areas = g[:, 2] * g[:, 3] if len(g) else np.zeros(0)
    return scores, kind, gidx, areas, g_ign


def match_all(images, dets_by_image, mode: str = "vessel", iou_thr: float = 0.5) -> dict:
    return {im.image_id: _match_image(im, dets_by_image.get(im.image_id, []), mode, iou_thr) for im in images}


def summarize(images, matches: dict, tau: float) -> dict:
    tp = fp = 0
    sea_only_n = sea_only_fp = 0  # frames with no (non-ignored) GT: FPs here become false sightings downstream
    gt_n = {k: 0 for k in AREA_BUCKETS}
    gt_hit = {k: 0 for k in AREA_BUCKETS}
    per_image = {}
    for im in images:
        scores, kind, gidx, areas, g_ign = matches[im.image_id]
        sel = scores >= tau
        itp = int((kind[sel] == 1).sum())
        ifp = int((kind[sel] == 0).sum())
        hit = np.zeros(len(areas), dtype=bool)
        hit[gidx[sel & (kind == 1)]] = True
        ifn = 0
        for j in range(len(areas)):
            if g_ign[j]:
                continue
            for k, (lo, hi) in AREA_BUCKETS.items():
                if lo <= areas[j] < hi:
                    gt_n[k] += 1
                    gt_hit[k] += int(hit[j])
            ifn += int(not hit[j])
        tp += itp
        fp += ifp
        if not (~g_ign).any():
            sea_only_n += 1
            sea_only_fp += ifp
        per_image[im.image_id] = (itp, ifp, ifn)
    n_gt = sum(gt_n.values())
    prec = tp / (tp + fp) if tp + fp else 0.0
    rec = tp / n_gt if n_gt else 0.0
    f1 = 2 * prec * rec / (prec + rec) if prec + rec else 0.0
    return {"tau": tau, "precision": prec, "recall": rec, "f1": f1, "tp": tp, "fp": fp, "n_gt": n_gt,
            "fp_per_image": fp / max(len(images), 1),
            "n_sea_only": sea_only_n, "fp_per_sea_only_frame": (sea_only_fp / sea_only_n) if sea_only_n else float("nan"),
            "recall_by_bucket": {k: (gt_hit[k] / gt_n[k] if gt_n[k] else float("nan")) for k in AREA_BUCKETS},
            "n_gt_by_bucket": gt_n, "per_image": per_image}


def match_at_threshold(images, dets_by_image, tau: float, mode: str = "vessel", iou_thr: float = 0.5) -> dict:
    """Precision/recall at score>=tau (IoU 0.5 greedy matching), recall per area bucket, FP per image and per
    sea-only frame, plus per-image (tp, fp, fn) for failure mining."""
    return summarize(images, match_all(images, dets_by_image, mode, iou_thr), tau)


def best_threshold(images, dets_by_image, mode="vessel", taus: Iterable[float] = None) -> dict:
    """Pick tau maximising F1 at IoU 0.5 (call on dev only)."""
    taus = list(taus) if taus is not None else [round(x, 2) for x in np.arange(0.05, 0.96, 0.05)]
    best = None
    curve = []
    matches = match_all(images, dets_by_image, mode)
    for t in taus:
        m = summarize(images, matches, t)
        curve.append({k: m[k] for k in ("tau", "precision", "recall", "f1", "fp_per_image")})
        if best is None or m["f1"] > best["f1"]:
            best = m
    best = {k: v for k, v in best.items() if k != "per_image"}
    best["curve"] = curve
    return best


def evaluate(images, dets_by_image, tau: float, mode: str = "vessel") -> dict:
    gt = build_gt(images, mode)
    m = coco_metrics(gt, to_coco_dets(images, dets_by_image, mode))
    at = match_at_threshold(images, dets_by_image, tau, mode)
    at.pop("per_image", None)
    m["at_tau"] = at
    m["n_images"] = len(images)
    m["n_gt"] = sum(1 for a in gt["annotations"] if not a["iscrowd"])
    return m
