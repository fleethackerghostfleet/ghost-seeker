"""End-to-end CPU run: gate -> detector -> ByteTrack -> per-track embedding/subtype -> sightings + forms.

Measures the whole loop under the edge budget (4 pinned threads) at a given detect rate on 1-fps clips.

  python -m edgeml.runtime.e2e --det ~/models/export/yolo11n-416/int8/model.xml --embed ~/models/clip/mobileclip-s0/int8 \
      --clips ~/data/clips/Submarine_surfacing ~/data/clips/Cargo_Ship_Uncut_View --detect-hz 1 --threads 4 --out e2e.json
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
from pathlib import Path

import numpy as np

from .gate import AdaptiveRate, FrameGate


def run_clip(frames: list[Path], fps: float, detector, gate, tracker, agg, rate: AdaptiveRate | None = None):
    import cv2

    from .backends import peak_rss_mb, rss_mb

    events, t_frames, t_det, n_det, n_gated, n_dets = [], [], [], 0, 0, 0
    hz_hist: list[float] = []
    tracks = []
    wall0 = time.perf_counter()
    for i, p in enumerate(frames):
        t_s = i / fps
        t0 = time.perf_counter()
        im = cv2.imread(str(p))
        if rate is not None:
            hz_hist.append(rate.target_hz(tracks, (im.shape[1], im.shape[0]), t_s))
            if not rate.due(t_s):
                t_frames.append((time.perf_counter() - t0) * 1e3)
                continue
        g = gate(im, t_s) if gate is not None else None
        if g is not None and not g.run_detector:
            n_gated += 1
            t_frames.append((time.perf_counter() - t0) * 1e3)
            continue
        td = time.perf_counter()
        dets = detector.detect(im)
        t_det.append((time.perf_counter() - td) * 1e3)
        n_det += 1
        n_dets += len(dets)
        tracks = tracker.update(dets, im, i, t_s)
        events += agg.update(tracks, im, i, t_s)
        t_frames.append((time.perf_counter() - t0) * 1e3)
    events += agg.flush(len(frames) / fps)
    wall = time.perf_counter() - wall0

    return events, {
        "n_frames": len(frames), "n_detected": n_det, "n_gated_out": n_gated, "n_raw_dets": n_dets, "wall_s": wall,
        "processed_fps": len(frames) / wall, "frame_ms_p50": float(np.percentile(t_frames, 50)), "frame_ms_p95": float(np.percentile(t_frames, 95)),
        "det_ms_p50": float(np.percentile(t_det, 50)) if t_det else None, "rss_mb": rss_mb(), "peak_rss_mb": peak_rss_mb(),
        "detect_hz_mean": float(np.mean(hz_hist)) if hz_hist else None,
        "frac_time_at_max_hz": float(np.mean([h == rate.max_hz for h in hz_hist])) if hz_hist and rate else None,
        "n_events": len(events), "n_sightings": sum(1 for e in events if e["event"] == "open"),
        "n_updates": sum(1 for e in events if e["event"] == "update"), **agg.stats,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--det", required=True)
    ap.add_argument("--engine", default="auto", choices=["auto", "openvino", "ort"],
                    help="auto: .onnx->ort (portable), .xml->openvino (Intel INT8 IR)")
    ap.add_argument("--ort-providers", default=None,
                    help="comma-separated ORT EPs, e.g. CoreMLExecutionProvider,CPUExecutionProvider (default env EDGEML_ORT_PROVIDERS or CPU)")
    ap.add_argument("--embed", help="exported image-tower dir (with meta.json/text_cache.json); omit to skip embedding")
    ap.add_argument("--embed-backend", default="openvino")
    ap.add_argument("--clips", nargs="+", required=True)
    ap.add_argument("--fps", type=float, default=1.0, help="clip frame rate (1-fps clips)")
    ap.add_argument("--detect-hz", type=float, default=1.0, help="detector floor rate (never below)")
    ap.add_argument("--max-hz", type=float, default=5.0, help="detector rate while tracks are young/tentative/near border")
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--no-gate", action="store_true")
    ap.add_argument("--score-thr", type=float, default=0.25)
    ap.add_argument("--max-frames", type=int, default=300)
    ap.add_argument("--out", required=True)
    ap.add_argument("--events-out")
    args = ap.parse_args()
    if args.ort_providers:
        os.environ["EDGEML_ORT_PROVIDERS"] = args.ort_providers
    from .threads import pin_threads

    pin_threads(args.threads)
    from ..embed.clip_embedder import ClipEmbedder
    from ..embed.zeroshot import ZeroShotClassifier
    from .detectors import load_detector
    from .sightings import TrackSightings
    from .tracker import ByteTrackTracker

    detector = load_detector(args.det, engine=args.engine, threads=args.threads, score_thr=args.score_thr)
    embedder = classifier = None
    if args.embed:
        meta = json.loads((Path(args.embed) / "meta.json").read_text())
        prec = meta.get("precision") or (Path(args.embed).name if Path(args.embed).name in ("fp32", "fp16", "int8") else "fp32")
        embedder = ClipEmbedder(meta["model"], backend=args.embed_backend, onnx_dir=args.embed, threads=args.threads, precision=prec)
        classifier = ZeroShotClassifier(embedder)
    results = []
    all_events = []
    for c in args.clips:
        frames = sorted(Path(c).glob("*.jpg"))[: args.max_frames]
        gate = None if args.no_gate else FrameGate()
        tracker = ByteTrackTracker(fps=args.detect_hz)
        agg = TrackSightings(Path(c).name, embedder, classifier)
        for _ in range(3):  # warm-up
            detector.detect(__import__("cv2").imread(str(frames[0])))
        rate = AdaptiveRate(floor_hz=args.detect_hz, max_hz=args.max_hz)
        events, stats = run_clip(frames, args.fps, detector, gate, tracker, agg, rate)
        stats.update({"clip": Path(c).name, "detector": detector.name, "embedder": getattr(embedder, "name", None), "detect_hz": args.detect_hz, "max_hz": args.max_hz,
                      "gate": not args.no_gate, "threads": args.threads})
        stats["budget_ok"] = bool(stats["processed_fps"] >= 2.0 and stats["peak_rss_mb"] <= 2048)
        print(json.dumps(stats), file=sys.stderr)
        results.append(stats)
        all_events += events
    Path(args.out).write_text(json.dumps(results, indent=1))
    if args.events_out:
        with open(args.events_out, "w") as f:
            f.writelines(json.dumps(e, default=float) + "\n" for e in all_events)


if __name__ == "__main__":
    main()
