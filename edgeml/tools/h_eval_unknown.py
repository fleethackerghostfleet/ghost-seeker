#!/usr/bin/env python
"""Task H: measure/tune unknown-object handling on DEV (synthetic unusual contacts + real dev clips), evaluate TEST once.

Sub-commands
  cache   run the *sidecar* detector (YOLO11n ONNX 640, all COCO labels, score>=0.05) over sets -> det cache
  recall  class-agnostic detector recall per synthetic class (box + track level, several label filters)
  crops   embed GT crops (known real vessels / synthetic unknowns / detector-FP noise) with MobileCLIP-S1 -> .npz
  auroc   known-vs-unknown AUROC for classifier variants (calibration / kNN bank / temperature / prompt bank)
  replay  tracker + ZeroShotAggregator replay on cached dets: which synthetic tracks surface, are flagged unknown,
          with and without realness gating; false unknowns on sea-only frames of real clips
Dev-only guard: every sequence touched must be in configs/splits.yaml dev (or synth dev) unless --split test is passed,
and test mode never calibrates/sweeps (it needs a frozen artifact JSON/NPZ produced on dev).
"""
from __future__ import annotations

import argparse
import json
import sys
from collections import Counter, defaultdict
from pathlib import Path

import cv2
import numpy as np
import yaml

TOOLS = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS.parents[0] / "src"))
sys.path.insert(0, str(TOOLS))
from edgeml.data.seqs import MotSequence, list_sequences  # noqa: E402
from edgeml.detectors.cached import CachedDetector, dets_to_jsonl  # noqa: E402
from edgeml.eval.sightings import match_sightings  # noqa: E402
from edgeml.tracking.embed import crop  # noqa: E402
from run_c import det_cache_path  # noqa: E402

KNOWN_VESSEL = {"ship", "boat", "sailboat", "submarine", "vessel", "Vessel", "cargo ship", "passenger ship", "bulk carrier", "cargo", "tug", "ferry_passenger", "fishing", "small_boat", "warship", "tanker"}
MARITIME_KEEP = {"boat", "airplane", "bird", "person", "surfboard", "kite", "anomaly"}
CACHE = "yolo11n-640-ort-all-c05"          # overridden by --cache


# ----------------------------------------------------------------------------------------------------------- helpers
def splits_cfg():
    return yaml.safe_load((TOOLS.parents[0] / "configs" / "splits.yaml").read_text())


def load_seqs(data: Path, sets: list[str], split: str) -> list[MotSequence]:
    cfg = splits_cfg()
    out = []
    for s in sets:
        for q in list_sequences(data / s, split):
            if s != "synth_unknown":
                canon = cfg[split].get(s, [])
                if q.seq_id not in canon:
                    raise SystemExit(f"{s}/{q.seq_id} is not in configs/splits.yaml {split}; refusing")
                other = "test" if split == "dev" else "dev"
                assert q.seq_id not in cfg[other].get(s, []), q.seq_id
            out.append(q)
    return out


def synth_ids(seq: MotSequence) -> set[int]:
    return {int(t) for t in seq.meta.get("synth_tracks", [])} if seq.meta.get("synthetic") else set()


def iou(a, b):
    ax1, ay1, ax2, ay2 = a[0], a[1], a[0] + a[2], a[1] + a[3]
    bx1, by1, bx2, by2 = b[0], b[1], b[0] + b[2], b[1] + b[3]
    iw, ih = max(0, min(ax2, bx2) - max(ax1, bx1)), max(0, min(ay2, by2) - max(ay1, by1))
    inter = iw * ih
    return inter / (a[2] * a[3] + b[2] * b[3] - inter + 1e-9)


def auroc(pos: np.ndarray, neg: np.ndarray) -> float:
    from sklearn.metrics import roc_auc_score

    if len(pos) == 0 or len(neg) == 0:
        return float("nan")
    return float(roc_auc_score(np.r_[np.ones(len(pos)), np.zeros(len(neg))], np.r_[pos, neg]))


# ------------------------------------------------------------------------------------------------------------- cache
def cmd_cache(a):
    from edgeml.runtime.detectors import load_detector

    data = Path(a.data).expanduser()
    if a.fusion is None:
        det = load_detector(Path(a.detector).expanduser(), engine="ort", threads=a.threads, score_thr=0.05, keep_labels=None)
    for seq in load_seqs(data, a.sets, a.split):
        out = det_cache_path(data, CACHE, seq)
        if out.exists() and not a.force:
            continue
        out.parent.mkdir(parents=True, exist_ok=True)
        if a.fusion is not None:                      # replay the cached YOLO dets, run only the anomaly proposer live
            from edgeml.detectors.anomaly import AnomalyFusionDetector, SeaAnomalyProposer

            base = CachedDetector(det_cache_path(data, a.base_cache, seq))
            det = AnomalyFusionDetector(base, SeaAnomalyProposer(**json.loads(a.fusion)))
        rows = []
        for fi, t, p in seq.frames():
            if a.fusion is not None:
                base.set_frame(fi)
            rows.append((fi, t, det.detect(cv2.imread(str(p)))))
        dets_to_jsonl(out, rows)
        print(f"{seq.seq_id}: {seq.n_frames} frames, {sum(len(r[2]) for r in rows)} dets", flush=True)


# ------------------------------------------------------------------------------------------------------------ recall
def cmd_recall(a):
    data = Path(a.data).expanduser()
    filt = {"any": None, "maritime": MARITIME_KEEP, "boat": {"boat"}}
    per = defaultdict(lambda: Counter())
    labels_fired = defaultdict(Counter)
    for seq in load_seqs(data, ["synth_unknown"], a.split):
        cls = seq.meta["synth_class"]
        det = CachedDetector(det_cache_path(data, CACHE, seq))
        tracks = seq.gt_tracks()
        for oid in synth_ids(seq):
            hits = Counter()
            for fr, box in tracks[oid]:
                dets = det.by_frame.get(fr, [])
                for name, keep in filt.items():
                    for thr in (0.05, 0.25):
                        ok = [d for d in dets if d.score >= thr and (keep is None or d.label in keep) and
                              (iou(box, d.box) >= a.iou or (box[0] <= d.box[0] + d.box[2] / 2 <= box[0] + box[2] and
                                                            box[1] <= d.box[1] + d.box[3] / 2 <= box[1] + box[3] and
                                                            d.box[2] * d.box[3] >= 0.1 * box[2] * box[3]))]
                        if ok:
                            hits[(name, thr)] += 1
                            if name == "any" and thr == 0.25:
                                labels_fired[cls][max(ok, key=lambda d: d.score).label] += 1
                per[cls]["boxes"] += 1
            per[cls]["tracks"] += 1
            for k, v in hits.items():
                per[cls][("box",) + k] += v
                if v >= 3:
                    per[cls][("trk3",) + k] += 1
    rows = []
    for cls, c in sorted(per.items()):
        r = {"class": cls, "tracks": c["tracks"], "boxes": c["boxes"]}
        for name in filt:
            for thr in (0.05, 0.25):
                r[f"box_recall_{name}@{thr}"] = c[("box", name, thr)] / max(1, c["boxes"])
                r[f"track_recall3_{name}@{thr}"] = c[("trk3", name, thr)] / max(1, c["tracks"])
        r["labels@0.25"] = dict(labels_fired[cls].most_common(4))
        rows.append(r)
    Path(a.out).expanduser().write_text(json.dumps(rows, indent=1))
    for r in rows:
        print(f"{r['class']:18s} trk={r['tracks']:2d} box_any@.05={r['box_recall_any@0.05']:.2f} box_any@.25={r['box_recall_any@0.25']:.2f} "
              f"box_boat@.25={r['box_recall_boat@0.25']:.2f} trk3_maritime@.25={r['track_recall3_maritime@0.25']:.2f} {r['labels@0.25']}")


# ------------------------------------------------------------------------------------------------------------- crops
def make_embedder(embed_dir: str):
    from edgeml.embed.clip_embedder import ClipEmbedder
    from edgeml.sidecar.service import _TextEmbedder

    d = Path(embed_dir).expanduser()
    meta = json.loads((d / "meta.json").read_text())
    return _TextEmbedder(ClipEmbedder(meta["model"], backend="ort", onnx_dir=d, threads=4, precision=meta.get("precision", "fp32")))


def cmd_crops(a):
    """Embed crops. known: real GT vessel tracks (<= per_track crops). unknown: synthetic tracks. noise: detector boxes
    (score>=0.25, maritime labels) on *complete-GT* real frames with IoU<0.1 to every GT box."""
    data = Path(a.data).expanduser()
    emb = make_embedder(a.embed)
    feats, kinds, classes, seqids, tids, scores = [], [], [], [], [], []
    todo = []
    for seq in load_seqs(data, a.sets, a.split):
        syn = synth_ids(seq)
        names = {int(k): v for k, v in seq.labels.get("classes", {}).items()}
        cls_of = {}
        for fr, oid, box, conf, c, vis in seq.gt:
            cls_of[oid] = names.get(c, str(c))
        for oid, rows in seq.gt_tracks().items():
            if oid in syn:
                kind, cname = "unknown", seq.meta["synth_class"]
            elif seq.meta.get("synthetic"):
                continue                                       # real vessels inside synth clips: already in the real dev seqs
            elif cls_of.get(oid) in KNOWN_VESSEL:
                kind, cname = "known", cls_of[oid]
            else:
                continue
            for i in np.linspace(0, len(rows) - 1, min(a.per_track, len(rows)), dtype=int):
                fr, box = rows[i]
                if box[2] < 4 or box[3] < 4 or not seq.frame_path(fr).exists():
                    continue
                todo.append((seq, fr, box, kind, cname, oid, 1.0))
        if not seq.meta.get("synthetic") and seq.meta.get("gt_status") == "complete":
            p = det_cache_path(data, CACHE, seq)
            if p.exists():
                det = CachedDetector(p)
                gt_at = defaultdict(list)
                for fr, oid, box, *_ in seq.gt:
                    gt_at[fr].append(box)
                n = 0
                for fr in sorted(det.by_frame):
                    for d in det.by_frame[fr]:
                        if d.score >= 0.25 and d.label in MARITIME_KEEP and all(iou(d.box, g) < 0.1 for g in gt_at.get(fr, [])) \
                                and d.box[2] >= 4 and d.box[3] >= 4:
                            todo.append((seq, fr, d.box, "noise", d.label, -1, d.score))
                            n += 1
                    if n >= a.max_noise:
                        break
    by_frame = defaultdict(list)
    for item in todo:
        by_frame[(item[0].seq_id, item[1])].append(item)
    crops, metas = [], []
    for (sid, fr), items in by_frame.items():
        img = cv2.imread(str(items[0][0].frame_path(fr)))
        for seq, fr, box, kind, cname, oid, sc in items:
            crops.append(crop(img, box, 0.25))
            metas.append((kind, cname, seq.seq_id, oid, sc))
    for i in range(0, len(crops), 32):
        feats.append(emb.embed_crops(crops[i:i + 32]))
    feats = np.concatenate(feats) if feats else np.zeros((0, emb.dim), np.float32)
    kinds, classes, seqids, tids, scores = map(np.asarray, zip(*metas)) if metas else ([],) * 5
    np.savez(Path(a.out).expanduser(), feats=feats, kind=kinds, cls=classes, seq=seqids, tid=tids.astype(int), score=scores.astype(float))
    print({k: int((kinds == k).sum()) for k in ("known", "unknown", "noise")}, dict(Counter(classes[kinds == "unknown"].tolist())))


# ------------------------------------------------------------------------------------------------------------- auroc
PROMPT_BANKS = {
    "default": None,
    "bg+": ["a photo of whitecaps and foam", "sun glint on the water", "a ship's wake", "a photo of a wave crest", "a photo of the coastline",
            "a photo of the sky"],
    "generic_unknown": ["an unidentified object floating at sea", "something unusual on the water", "a strange object on the ocean surface"],
}


def build_classifier(emb, bank_name: str, text_tower=None):
    from edgeml.embed.zeroshot import SUBTYPES, UNKNOWN_PROMPTS, ZeroShotClassifier

    extra = PROMPT_BANKS[bank_name]
    if extra is None:
        return ZeroShotClassifier(emb)
    prompts = UNKNOWN_PROMPTS + extra

    class _E:                                     # ORT cache lacks the new prompts -> torch text tower for those only
        dim = emb.dim

        def embed_text(self, texts):
            try:
                return emb.embed_text(texts)
            except Exception:  # noqa: BLE001
                return text_tower.embed_text(texts)

        def logit_scale(self):
            return emb.logit_scale()

        def embed_crops(self, c):
            return emb.embed_crops(c)

    return ZeroShotClassifier(_E(), subtypes=SUBTYPES, unknown_prompts=prompts)


def variants():
    out = [("before (sidecar default: MCM, sim_ref .30, no bank)", dict(cal=False, bank=False)),
           ("calibrated MCM", dict(cal=True, bank=False)),
           ("calibrated + kNN bank k=5", dict(cal=True, bank=True))]
    for k in (3, 10):
        out.append((f"calibrated + kNN k={k}", dict(cal=True, bank=True, k=k)))
    for w in (0.75, 1.0):
        out.append((f"calibrated + kNN, w_knn={w}", dict(cal=True, bank=True, w_knn=w)))
    for m in (0.5, 2.0):
        out.append((f"calibrated + kNN, temperature x{m}", dict(cal=True, bank=True, temp=m)))
    for b in ("bg+", "generic_unknown"):
        out.append((f"calibrated + kNN, prompts {b}", dict(cal=True, bank=True, prompts=b)))
    out.append(("combo: kNN k=10, w_knn=0.75, temp x0.5", dict(cal=True, bank=True, k=10, w_knn=0.75, temp=0.5)))
    out.append(("combo: kNN k=10, w_knn=0.75", dict(cal=True, bank=True, k=10, w_knn=0.75)))
    return out


def score_variant(clf, feats, v):
    """Unknownness per row under variant v (re-implements classify() mixing so w_knn/temp can be swept)."""
    res = clf.classify(feats)
    if v.get("w_knn") is None or clf.bank is None:
        return np.asarray([r["unknownness"] for r in res])
    knn = np.asarray([r["knn"] for r in res])
    mcm = np.asarray([r["mcm"] for r in res])
    unk = (1 - v["w_knn"]) * mcm + v["w_knn"] * knn
    bg = np.asarray([r["bg_sim"] for r in res])
    ms = np.asarray([r["max_sim"] for r in res])
    unk = np.where(bg > ms, np.maximum(unk, 0.5 + 0.5 * np.clip(bg - ms, 0, 0.1) / 0.1), unk)
    return np.clip(unk, 0, 1)


def fit_variant(emb, v, known_fit, text_tower=None):
    clf = build_classifier(emb, v.get("prompts", "default"), text_tower)
    if v.get("temp"):
        clf.scale = clf.scale * v["temp"]
    cal = {}
    if v.get("cal"):
        cal["calibration"] = clf.calibrate(known_fit)
    if v.get("bank"):
        cal["bank"] = clf.fit_bank(known_fit, k=v.get("k", 5))
    return clf, cal


def cmd_auroc(a):
    z = np.load(Path(a.crops).expanduser(), allow_pickle=True)
    feats, kind, cls, seq, tid = z["feats"], z["kind"], z["cls"], z["seq"], z["tid"]
    emb = make_embedder(a.embed)
    tower = None
    if any(v.get("prompts") for _, v in variants()):
        try:
            from edgeml.embed.clip_embedder import ClipEmbedder
            tower = ClipEmbedder("mobileclip-s1", backend="torch")
        except Exception as e:  # noqa: BLE001
            print("no torch text tower; prompt variants skipped:", str(e)[:100])
    known = kind == "known"
    # dev: fit on even real track ids, evaluate on odd ones so the bank never contains the evaluated crop
    key = np.asarray([hash((s, int(t))) % 2 for s, t in zip(seq, tid)])
    fit_mask, eval_mask = known & (key == 0), known & (key == 1)
    if a.fit_crops:                                   # test mode: frozen dev bank, evaluate all test known crops
        zf = np.load(Path(a.fit_crops).expanduser(), allow_pickle=True)
        known_fit = zf["feats"][zf["kind"] == "known"]
        eval_mask = known
    else:
        known_fit = feats[fit_mask]
    results = []
    for name, v in (variants() if not a.frozen else [(a.frozen_name, json.loads(Path(a.frozen).read_text())["variant"])]):
        if v.get("prompts") and tower is None:
            continue
        clf, cal = fit_variant(emb, v, known_fit, tower)
        u = score_variant(clf, feats, v)
        row = {"variant": name, "spec": v, "cal": cal, "n_known_eval": int(eval_mask.sum()),
               "auroc_unknown_vs_known": auroc(u[kind == "unknown"], u[eval_mask]),
               "auroc_noise_vs_known": auroc(u[kind == "noise"], u[eval_mask]),
               "auroc_unknown_vs_noise": auroc(u[kind == "unknown"], u[kind == "noise"]),
               "per_class": {c: auroc(u[(kind == "unknown") & (cls == c)], u[eval_mask]) for c in sorted(set(cls[kind == "unknown"]))},
               "median_unknown": float(np.median(u[kind == "unknown"])) if (kind == "unknown").any() else None,
               "median_known": float(np.median(u[eval_mask])), "median_noise": float(np.median(u[kind == "noise"])) if (kind == "noise").any() else None,
               "p90_known": float(np.percentile(u[eval_mask], 90))}
        results.append(row)
        print(f"{name:48s} AUROC unk/known={row['auroc_unknown_vs_known']:.3f} noise/known={row['auroc_noise_vs_known']:.3f} "
              f"unk/noise={row['auroc_unknown_vs_noise']:.3f} med unk={row['median_unknown']:.2f} known={row['median_known']:.2f} p90known={row['p90_known']:.2f}")
        print("   ", {c: round(x, 3) for c, x in row["per_class"].items()})
    Path(a.out).expanduser().write_text(json.dumps(results, indent=1))


# --------------------------------------------------------------------------------------------------------------- fit
def cmd_fit(a):
    """Fit the chosen variant on ALL dev known crops and write the sidecar artifact unknown_calib.npz."""
    z = np.load(Path(a.crops).expanduser(), allow_pickle=True)
    known = z["feats"][z["kind"] == "known"]
    v = json.loads(a.variant)
    emb = make_embedder(a.embed)
    clf, cal = fit_variant(emb, v, known)
    if v.get("w_knn") is not None:
        clf.w_knn = float(v["w_knn"])
    srcs = sorted({str(s) for s in z["seq"][z["kind"] == "known"]})
    meta = {"variant": v, "fit": cal, "n_known": int(len(known)), "sources": srcs, "split": "dev",
            "embedder": Path(a.embed).expanduser().name, "note": "dev-only known GT crops; never test"}
    clf.save_calibration(Path(a.out).expanduser(), meta)
    print(json.dumps(meta, indent=1))


# ------------------------------------------------------------------------------------------------------------ replay
def cmd_replay(a):
    from edgeml.sidecar.service import _ZeroShotAggregator
    from edgeml.tracking import AggregatorParams, make_tracker
    from eval_unknown import CachedEmbedder

    data = Path(a.data).expanduser()
    emb = make_embedder(a.embed)
    from edgeml.embed.zeroshot import load_classifier

    clf = load_classifier(emb, calib=Path(a.artifact).expanduser() if a.artifact else None)
    cemb = CachedEmbedder(emb)
    base = {"relink_gap_s": 20.0, "min_update_interval_s": 10.0}
    variants_ = {"no_gate": dict(gate_unknown_bonus_on_realness=False), "gate": dict(gate_unknown_bonus_on_realness=True)}
    for ov in json.loads(a.agg) if a.agg else []:
        variants_[ov.pop("name")] = ov
    tally = {k: defaultdict(Counter) for k in variants_}
    detail = []
    for seq in load_seqs(data, a.sets, a.split):
        det = CachedDetector(det_cache_path(data, CACHE, seq))
        tracker = make_tracker("bytetrack", fps=seq.fps, asso_func="centroid", max_lost_s=6.0)
        rows = []
        for fi, t, p in seq.frames():
            img = cv2.imread(str(p))
            det.set_frame(fi)
            dets = [d for d in det.detect(img) if d.label in MARITIME_KEEP]
            rows.append((fi, t, tracker.update(dets, img, fi, t)))
        syn = synth_ids(seq)
        gt_tracks = seq.gt_tracks()
        annotated = {r[0] for r in seq.gt}
        for vname, ov in variants_.items():
            agg = _ZeroShotAggregator(seq.seq_id, AggregatorParams(**{**base, **ov}), frame_size=(seq.width, seq.height),
                                      embedder=cemb, classifier=clf)
            events = []
            for fi, t, tracks in rows:
                events.extend(agg.update(tracks, cv2.imread(str(seq.frame_path(fi))), fi, t))
            events.extend(agg.flush(seq.duration_s + agg.p.close_after_s + 1))
            matches = match_sightings(agg.trajectories(), gt_tracks)
            tau = agg.p.unknown_threshold
            by_gt = defaultdict(list)
            false_unknown, false_sightings = set(), set()
            for e in events:
                g = matches.get(e["sighting_id"])
                flagged = e.get("unknown_real") if ov.get("gate_unknown_bonus_on_realness") else e["unknownness"] >= tau
                if g is not None:
                    by_gt[g].append((e["unknownness"], bool(flagged), e.get("realness")))
                elif e["frame_idx"] not in annotated and seq.meta.get("gt_status") == "complete":
                    false_sightings.add(e["sighting_id"])
                    if flagged:
                        false_unknown.add(e["sighting_id"])
            c = tally[vname]
            if syn:
                cls = seq.meta["synth_class"]
                for oid in syn:
                    c[cls]["tracks"] += 1
                    ev = by_gt.get(oid, [])
                    if ev:
                        c[cls]["surfaced"] += 1
                        c[cls]["flagged_unknown"] += int(any(f for _, f, _ in ev))
                        c[cls]["unk_sum"] += max(u for u, _, _ in ev)
                    detail.append({"variant": vname, "seq": seq.seq_id, "class": cls, "tid": oid, "surfaced": bool(ev),
                                   "max_unknownness": max((u for u, _, _ in ev), default=None), "flagged": bool(any(f for _, f, _ in ev)),
                                   "realness": max((r for _, _, r in ev if r is not None), default=None)})
            else:
                c["_real"]["seqs"] += 1
                c["_real"]["sea_frames"] += seq.n_frames - len(annotated) if seq.meta.get("gt_status") == "complete" else 0
                c["_real"]["false_unknown_sightings"] += len(false_unknown)
                c["_real"]["false_sightings"] += len(false_sightings)
                known_ids = set(gt_tracks) - syn
                c["_real"]["known_tracks"] += len(known_ids)
                c["_real"]["known_surfaced"] += sum(1 for g in known_ids if by_gt.get(g))
                c["_real"]["known_flagged_unknown"] += sum(1 for g in known_ids if any(f for _, f, _ in by_gt.get(g, [])))
    out = {v: {k: dict(c) for k, c in t.items()} for v, t in tally.items()}
    Path(a.out).expanduser().write_text(json.dumps({"summary": out, "detail": detail}, indent=1))
    for v, t in out.items():
        print("==", v)
        for k, c in sorted(t.items()):
            print(f"  {k:18s} {c}")


def main():
    global CACHE
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("--data", default="~/data/edgeml_eval")
    common.add_argument("--split", default="dev", choices=["dev", "test"])
    common.add_argument("--embed", default="~/models/D/mobileclip-s1")
    common.add_argument("--cache", default=CACHE, help="detection cache name")
    c = sub.add_parser("cache", parents=[common])
    c.add_argument("--sets", nargs="+", required=True)
    c.add_argument("--detector", default="~/models/D/yolo11n-640/model.onnx")
    c.add_argument("--threads", type=int, default=4)
    c.add_argument("--force", action="store_true")
    c.add_argument("--fusion", default=None, help="JSON SeaAnomalyProposer kwargs: build a YOLO+anomaly cache from --base-cache")
    c.add_argument("--base-cache", default=CACHE)
    r = sub.add_parser("recall", parents=[common])
    r.add_argument("--iou", type=float, default=0.3)
    r.add_argument("--out", required=True)
    k = sub.add_parser("crops", parents=[common])
    k.add_argument("--sets", nargs="+", required=True)
    k.add_argument("--per-track", type=int, default=6)
    k.add_argument("--max-noise", type=int, default=60)
    k.add_argument("--out", required=True)
    u = sub.add_parser("auroc", parents=[common])
    u.add_argument("--crops", required=True)
    u.add_argument("--fit-crops", default=None, help="TEST mode: dev crops .npz used to fit the frozen bank")
    u.add_argument("--frozen", default=None, help="TEST mode: artifact JSON with the single frozen variant")
    u.add_argument("--frozen-name", default="frozen")
    u.add_argument("--out", required=True)
    f = sub.add_parser("fit", parents=[common])
    f.add_argument("--crops", required=True)
    f.add_argument("--variant", required=True, help="JSON variant spec, e.g. '{\"cal\": true, \"bank\": true, \"k\": 10, \"w_knn\": 0.75}'")
    f.add_argument("--out", required=True)
    p = sub.add_parser("replay", parents=[common])
    p.add_argument("--sets", nargs="+", required=True)
    p.add_argument("--artifact", default=None, help="unknown_calib.npz (dev-fit); omit for the sidecar default")
    p.add_argument("--agg", default=None, help='JSON list of extra AggregatorParams variants, each with a "name"')
    p.add_argument("--out", required=True)
    a = ap.parse_args()
    CACHE = a.cache
    {"cache": cmd_cache, "recall": cmd_recall, "crops": cmd_crops, "auroc": cmd_auroc, "fit": cmd_fit, "replay": cmd_replay}[a.cmd](a)


if __name__ == "__main__":
    main()
