#!/usr/bin/env python
"""Workstream C experiment runner: detector × tracker × detect_every × aggregator on MOT-layout sequences.

Examples
  # cache detector outputs once per frame (YOLO on CPU), then every variant replays them at any detect_every
  python edgeml/tools/run_c.py cache --data ~/data/edgeml_eval --sets fvessel_clip10 mvtd_5fps --detector yolo11s
  # dev matrix
  python edgeml/tools/run_c.py matrix --data ~/data/edgeml_eval --sets fvessel_clip10 mvtd_5fps --split dev \
      --detectors oracle yolo11s --trackers bytetrack ocsort botsort_cmc --every 1 2 5 --out runs/<run_id>
Outputs under --out: results.jsonl (one row per config × seq), summary.json (pooled per config), events/<cfg>/<seq>.jsonl,
tracks/<cfg>/<seq>.txt (MOT), trackeval.json.
"""
from __future__ import annotations

import argparse
import itertools
import json
from collections import defaultdict
import sys
import time
from dataclasses import asdict
from pathlib import Path

import cv2
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from edgeml.data.seqs import MotSequence, gt_by_frame, list_sequences  # noqa: E402
from edgeml.detectors import OracleDetector  # noqa: E402
from edgeml.detectors.cached import CachedDetector, dets_to_jsonl  # noqa: E402
from edgeml.eval.baselines import image_dedup_events, naive_events  # noqa: E402
from edgeml.eval.delivery import delivered_vs_budget, pool_delivery  # noqa: E402
from edgeml.eval.sightings import aggregate_metrics, match_sightings, sighting_metrics  # noqa: E402
from edgeml.stabilize import HorizonStabilizer  # noqa: E402
from edgeml.pipeline import AdaptiveRate, run_clip, write_mot  # noqa: E402
from edgeml.tracking import AggregatorParams, SightingAggregator, make_tracker  # noqa: E402


def _json(o):
    if isinstance(o, (np.floating, np.integer)):
        return o.item()
    if isinstance(o, Path):
        return str(o)
    raise TypeError(str(type(o)))


def load_sets(data: Path, sets: list[str], split: str | None, include_incomplete: bool = False) -> list[MotSequence]:
    """Sequences of the given sets/split. index.jsonl `gt_status=incomplete` sequences (A's label QA) are skipped unless asked."""
    seqs = []
    for s in sets:
        for q in list_sequences(data / s, split):
            if q.meta.get("gt_status") == "incomplete" and not include_incomplete:
                continue
            seqs.append(q)
    return seqs


def det_cache_path(data: Path, detector: str, seq: MotSequence) -> Path:
    return data / "_det_cache" / detector / seq.root.parent.name / f"{seq.seq_id}.jsonl"


# ----------------------------------------------------------------------------- cache
def cmd_cache(a):
    from edgeml.detectors import make_detector

    data = Path(a.data).expanduser()
    seqs = load_sets(data, a.sets, a.split)
    det = make_detector(a.detector, imgsz=a.imgsz, conf=a.conf, device=a.device)
    cache_name = a.name or Path(a.detector).stem           # cache dir / detector label used by `matrix --detectors`
    for seq in seqs:
        out = det_cache_path(data, cache_name, seq)
        if out.exists() and not a.force:
            print("skip", seq.seq_id)
            continue
        out.parent.mkdir(parents=True, exist_ok=True)
        rows, t0 = [], time.time()
        for fi, t, p in seq.frames():
            img = cv2.imread(str(p))
            rows.append((fi, t, det.detect(img)))
        dets_to_jsonl(out, rows)
        n = sum(len(r[2]) for r in rows)
        print(f"{seq.seq_id}: {seq.n_frames} frames, {n} dets, {1000*(time.time()-t0)/seq.n_frames:.0f} ms/frame", flush=True)


# ----------------------------------------------------------------------------- matrix
def make_det(name: str, seq: MotSequence, data: Path, a):
    if name.startswith("oracle"):
        # oracle | oracle-d0.2 | oracle-d0.2-j0.05
        d = j = 0.0
        for part in name.split("-")[1:]:
            if part.startswith("d"):
                d = float(part[1:])
            elif part.startswith("j"):
                j = float(part[1:])
        return OracleDetector(gt_by_frame(seq), seq.labels, dropout=d, jitter=j)
    p = det_cache_path(data, name, seq)
    if not p.exists():
        raise SystemExit(f"no detection cache for {name} / {seq.seq_id}; run `run_c.py cache` first")
    return CachedDetector(p, name=name, min_score=a.min_score)


def per_frame_dets(seq: MotSequence, det, every: int):
    for fi, t, p in seq.frames(every=every):
        img = cv2.imread(str(p))
        det.set_frame(fi)
        yield fi, t, img, det.detect(img)


def eval_seq(seq: MotSequence, events: list[dict], sighting_tracks, selector_kw: dict | None = None) -> dict:
    gt = seq.gt_tracks()
    m = sighting_metrics(events, sighting_tracks, gt, seq.fps, seq.n_frames)
    sm = match_sightings(sighting_tracks, gt)
    d = delivered_vs_budget(events, sm, gt, seq.width, seq.height, duration_s=seq.duration_s, selector_kw=selector_kw)
    return {"sighting": m, "delivery": d}


def cmd_matrix(a):
    data = Path(a.data).expanduser()
    out = Path(a.out).expanduser()
    out.mkdir(parents=True, exist_ok=True)
    seqs = load_sets(data, a.sets, a.split)
    agg_over = json.loads(a.agg) if a.agg else {}
    trk_over = json.loads(a.trk) if a.trk else {}
    selector_kw = json.loads(a.selector) if a.selector else None
    print(f"{len(seqs)} sequences: {[s.seq_id for s in seqs]}", flush=True)
    (out / "config.json").write_text(json.dumps({**vars(a), "agg_params": asdict(AggregatorParams(**agg_over))}, indent=1, default=_json))

    results = open(out / "results.jsonl", "w")
    per_cfg: dict[str, dict[str, dict]] = {}
    track_files: dict[str, dict[str, Path]] = {}

    def record(cfg: str, seq: MotSequence, ev: list[dict], straj, extra: dict):
        r = eval_seq(seq, ev, straj, selector_kw)
        row = {"cfg": cfg, "seq": seq.seq_id, "set": seq.root.parent.name, "split": seq.meta.get("split"),
               "gt_status": seq.meta.get("gt_status", "complete"), "platform_motion": seq.meta.get("platform_motion"), **extra, **r}
        results.write(json.dumps(row, default=_json) + "\n")
        results.flush()
        per_cfg.setdefault(cfg, {})[seq.seq_id] = r
        d = out / "events" / cfg
        d.mkdir(parents=True, exist_ok=True)
        with open(d / f"{seq.seq_id}.jsonl", "w") as f:
            for e in ev:
                f.write(json.dumps(e, default=_json) + "\n")

    for det_name in a.detectors:
        for every in a.every:
            # baselines (per detector × rate)
            if not a.no_baselines:
                for seq in seqs:
                    det = make_det(det_name, seq, data, a)
                    pf = list(per_frame_dets(seq, det, every))
                    for bname, fn in (("naive", naive_events), ("imgdedup", image_dedup_events)):
                        ev, straj = fn(seq.seq_id, pf)
                        cfg = f"{det_name}|{bname}|every{every}"
                        record(cfg, seq, ev, straj, {"detector": det_name, "tracker": bname, "every": every})
                    print("baselines", det_name, every, seq.seq_id, flush=True)
            for trk_name in a.trackers:
                cfg = f"{det_name}|{trk_name}|every{every}"
                tdir = out / "tracks" / cfg.replace("|", "_")
                tdir.mkdir(parents=True, exist_ok=True)
                for seq in seqs:
                    det = make_det(det_name, seq, data, a)
                    eff_fps = seq.fps / every
                    trk = make_tracker(trk_name, fps=eff_fps, **trk_over)
                    agg = SightingAggregator(seq.seq_id, AggregatorParams(**agg_over), frame_size=(seq.width, seq.height))
                    t0 = time.time()
                    stab = HorizonStabilizer() if a.stabilize else None
                    res = run_clip(seq.frames(), det, trk, agg, detect_every=every, fps=seq.fps, record_dets=False, stabilizer=stab)
                    if stab is not None:
                        res["timing"]["stabilizer"] = stab.stats()
                    rows = [(fi, t.track_id, t.box, t.score) for fi, _t, trs in res["tracks"] for t in trs]
                    write_mot(tdir / f"{seq.seq_id}.txt", rows)
                    track_files.setdefault(cfg, {})[seq.seq_id] = tdir / f"{seq.seq_id}.txt"
                    record(cfg, seq, res["events"], res["sighting_tracks"],
                           {"detector": det_name, "tracker": trk_name, "every": every, "timing": res["timing"],
                            "n_embeds": agg.n_embeds, "wall_s": time.time() - t0})
                print("done", cfg, flush=True)
    results.close()

    # TrackEval per tracker config (class-agnostic, all sequences)
    te = {}
    if not a.no_trackeval:
        from edgeml.eval.trackeval_wrapper import run_trackeval

        for cfg, files in track_files.items():
            try:
                te[cfg] = run_trackeval([s.root for s in seqs], {k: str(v) for k, v in files.items()}, workdir=out / "_trackeval" / cfg.replace("|", "_"),
                                        frame_stride=int(cfg.rsplit("every", 1)[1]))
            except Exception as e:  # keep going; report the failure
                te[cfg] = {"error": repr(e)}
        (out / "trackeval.json").write_text(json.dumps(te, indent=1, default=_json))

    summary = {}
    for cfg, per in per_cfg.items():
        summary[cfg] = {
            "sighting": aggregate_metrics({k: v["sighting"] for k, v in per.items()}),
            "delivery": pool_delivery({k: v["delivery"] for k, v in per.items()}),
            "trackeval": te.get(cfg, {}).get("COMBINED_SEQ") if isinstance(te.get(cfg), dict) else None,
        }
    (out / "summary.json").write_text(json.dumps(summary, indent=1, default=_json))
    print_summary(summary)


def print_summary(summary: dict):
    cols = ["recall", "dup/obj", "false/min", "ttfr", "rep/obj", "n_rep", "HOTA", "IDF1", "IDSW", "Frag", "obj@72K", "obj@480K", "obj@1.9M", "obj@15M"]
    print(f"{'cfg':42s} " + " ".join(f"{c:>8s}" for c in cols))
    for cfg, s in summary.items():
        m, d, t = s["sighting"], s["delivery"]["budgets"], s.get("trackeval") or {}
        f = lambda v, p=2: ("-" if v is None else (f"{v:.{p}f}" if isinstance(v, float) else str(v)))
        vals = [f(m["object_recall"]), f(m["duplicates_per_object"]), f(m["false_per_min"]), f(m["ttfr_mean_s"], 1), f(m["reports_per_object"], 1), f(m["n_reports"]),
                f(t.get("HOTA")), f(t.get("IDF1")), f(t.get("IDSW")), f(t.get("Frag")),
                f(d["9.6kbps"]["frac_image"]), f(d["64kbps"]["frac_image"]), f(d["256kbps"]["frac_image"]), f(d["2Mbps"]["frac_image"])]
        print(f"{cfg:42s} " + " ".join(f"{v:>8s}" for v in vals))


# ----------------------------------------------------------------------------- delivery (E3: selector ablation on saved events)
def cmd_delivery(a):
    """Re-score saved events of a matrix run through `select.greedy` variants (no tracking rerun)."""
    data = Path(a.data).expanduser()
    run = Path(a.run).expanduser()
    seqs = {s.seq_id: s for s in load_sets(data, a.sets, a.split)}
    rows = [json.loads(l) for l in (run / "results.jsonl").read_text().splitlines() if l.strip()]
    variants = json.loads(a.variants)
    want = set(a.cfgs) if a.cfgs else None
    out: dict = {}
    for name, kw in variants.items():
        per_cfg: dict[str, dict] = defaultdict(dict)
        for r in rows:
            cfg, seq_id = r["cfg"], r["seq"]
            if (want and cfg not in want) or seq_id not in seqs:
                continue
            seq = seqs[seq_id]
            ep = run / "events" / cfg / f"{seq_id}.jsonl"
            events = [json.loads(l) for l in ep.read_text().splitlines() if l.strip()] if ep.exists() else []
            sm = {k: v for k, v in r["sighting"]["matched_sightings"].items()}
            for sid in r["sighting"].get("false_sighting_ids", []):
                sm.setdefault(sid, None)
            per_cfg[cfg][seq_id] = delivered_vs_budget(events, sm, seq.gt_tracks(), seq.width, seq.height, duration_s=seq.duration_s, selector_kw=kw)
        out[name] = {"selector": kw, "cfgs": {cfg: pool_delivery(per) for cfg, per in per_cfg.items()}}
        print(f"== selector {name} {kw}")
        for cfg, d in out[name]["cfgs"].items():
            b = d["budgets"]
            print(f"  {cfg:40s} " + " ".join(f"{n}:{(b[n]['frac_image'] or 0):.2f}/{(b[n]['bytes_per_delivered_object'] or 0)/1024:.0f}KB" for n in ("9.6kbps", "64kbps", "256kbps", "2Mbps")))
    (run / f"delivery_{a.tag}.json").write_text(json.dumps(out, indent=1, default=_json))


# ----------------------------------------------------------------------------- trackeval (re-score saved tracks)
def cmd_trackeval(a):
    from edgeml.eval.trackeval_wrapper import run_trackeval

    data = Path(a.data).expanduser()
    run = Path(a.run).expanduser()
    seqs = load_sets(data, a.sets, a.split)
    te = {}
    sp = run / "summary.json"
    names = {c.replace("|", "_"): c for c in json.loads(sp.read_text())} if sp.exists() else {}
    for d in sorted((run / "tracks").iterdir()):
        cfg = names.get(d.name, d.name)
        every = int(cfg.rsplit("every", 1)[1])
        files = {s.seq_id: str(d / f"{s.seq_id}.txt") for s in seqs}
        try:
            te[cfg] = run_trackeval([s.root for s in seqs], files, workdir=run / "_trackeval" / d.name, frame_stride=every)
        except Exception as e:
            te[cfg] = {"error": repr(e)}
    (run / "trackeval.json").write_text(json.dumps(te, indent=1, default=_json))
    # refresh summary.json trackeval column
    sp = run / "summary.json"
    if sp.exists():
        summary = json.loads(sp.read_text())
        for cfg in summary:
            t = te.get(cfg)
            summary[cfg]["trackeval"] = t.get("COMBINED_SEQ") if isinstance(t, dict) else None
        sp.write_text(json.dumps(summary, indent=1, default=_json))
        print_summary(summary)


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("cache")
    c.add_argument("--data", required=True)
    c.add_argument("--sets", nargs="+", required=True)
    c.add_argument("--split", default=None)
    c.add_argument("--detector", default="yolo11s", help="ultralytics weights name/path (.pt or .onnx, e.g. B's export)")
    c.add_argument("--name", default=None, help="cache name (default: weights file stem)")
    c.add_argument("--imgsz", type=int, default=1280)
    c.add_argument("--conf", type=float, default=0.05)
    c.add_argument("--device", default="cpu")
    c.add_argument("--force", action="store_true")
    m = sub.add_parser("matrix")
    m.add_argument("--data", required=True)
    m.add_argument("--sets", nargs="+", required=True)
    m.add_argument("--split", default="dev")
    m.add_argument("--detectors", nargs="+", default=["oracle"])
    m.add_argument("--trackers", nargs="+", default=["bytetrack"])
    m.add_argument("--every", nargs="+", type=int, default=[1])
    m.add_argument("--min-score", type=float, default=0.25, help="score floor applied to cached detections")
    m.add_argument("--agg", default=None, help="JSON overrides for AggregatorParams")
    m.add_argument("--trk", default=None, help="JSON overrides for TrackerParams (e.g. {\"asso_func\":\"centroid\"})")
    m.add_argument("--selector", default=None, help="JSON kwargs for select.greedy (ladder_decay, max_bytes_per_object)")
    m.add_argument("--no-baselines", action="store_true")
    m.add_argument("--stabilize", action="store_true", help="horizon-pinned stabilisation pre-step (E2 ablation)")
    m.add_argument("--no-trackeval", action="store_true")
    m.add_argument("--out", required=True)
    d = sub.add_parser("delivery", help="E3: re-score a run's saved events with selector variants")
    d.add_argument("--data", required=True)
    d.add_argument("--sets", nargs="+", required=True)
    d.add_argument("--split", default="dev")
    d.add_argument("--run", required=True)
    d.add_argument("--cfgs", nargs="*", default=None)
    d.add_argument("--variants", default='{"decay":{},"nodecay":{"ladder_decay":null},"decay_cap48k":{"max_bytes_per_object":49152},"nodecay_cap48k":{"ladder_decay":null,"max_bytes_per_object":49152}}')
    d.add_argument("--tag", default="e3")
    t = sub.add_parser("trackeval", help="re-run TrackEval on a run's saved tracks (detector-frame stride aware)")
    t.add_argument("--data", required=True)
    t.add_argument("--sets", nargs="+", required=True)
    t.add_argument("--split", default="dev")
    t.add_argument("--run", required=True)
    a = ap.parse_args()
    {"cache": cmd_cache, "matrix": cmd_matrix, "delivery": cmd_delivery, "trackeval": cmd_trackeval}[a.cmd](a)


if __name__ == "__main__":
    main()
