"""python -m edgeml.sidecar --listen :9090 --detector <onnx|xml> [--embed <mobileclip dir>] [--mock]"""
from __future__ import annotations

import argparse
import json
import os
import logging


# D's COCO YOLO11n export fetched by `make demo-models` (the G fine-tune did not win on the locked test, see
# research/edgeml/reports/G_detector.md); $EDGEML_DETECTOR overrides, e.g. to try ~/models/G/yolo11n-ft-640-v1/model.onnx.
DEFAULT_DETECTOR = "~/models/D/yolo11n-640/model.onnx"


def default_detector() -> str | None:
    env = os.environ.get("EDGEML_DETECTOR")
    if env:
        return env
    p = os.path.expanduser(DEFAULT_DETECTOR)
    return p if os.path.exists(p) else None


def main(argv=None) -> None:
    ap = argparse.ArgumentParser(prog="edgeml.sidecar", description=__doc__)
    ap.add_argument("--listen", default=":9090", help="host:port; a bare ':9090' binds 127.0.0.1 only, use 0.0.0.0:9090 to expose (default :9090)")
    ap.add_argument("--mock", action="store_true", help="scripted engine, no models (Go tests / CI)")
    ap.add_argument("--detector", "--det", default=None,
                    help=f"YOLO ONNX (ORT CPU) or OpenVINO IR; default $EDGEML_DETECTOR or {DEFAULT_DETECTOR} if present")
    ap.add_argument("--engine", default="auto", help="ort | openvino | auto")
    ap.add_argument("--embed", default=None, help="MobileCLIP ONNX export dir (meta.json, model.onnx, text_cache.json); 'none' disables")
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--fps", type=float, default=5.0, help="nominal frame rate used when requests carry no captured_at_unix_ms")
    ap.add_argument("--tracker", default="bytetrack")
    ap.add_argument("--asso", default="centroid")
    ap.add_argument("--max-lost-s", type=float, default=6.0)
    ap.add_argument("--score-thr", type=float, default=0.35, help="new tracks / reports need a detection at least this confident")
    ap.add_argument("--det-low-thr", type=float, default=0.05)
    ap.add_argument("--agg", default=None, help='AggregatorParams overrides as JSON, e.g. \'{"min_hits":2}\'')
    ap.add_argument("--trk", default=None, help='TrackerParams overrides as JSON, e.g. \'{"tentative_thresh":null}\'')
    ap.add_argument("--text-tower", default="cache", choices=["cache", "torch"],
                    help="EmbedText source: 'cache' = ORT text_cache.json only (no torch); 'torch' = fall back to the torch text tower for uncached text")
    ap.add_argument("--prioritizer", default="edgeml", choices=["edgeml", "tomer", "both"],
                    help="scoring backend: edgeml (default) | tomer (algorithm/ghost_priority image-level score, no model needed) | both")
    ap.add_argument("--tomer-model", default=None, help="NanoDet ONNX for ghost_priority's object provider (default: NoObjects)")
    ap.add_argument("--tomer-policy", default=None, help="ghost_priority policy JSON (default: DEFAULT_POLICY)")
    ap.add_argument("--tomer-dup-threshold", type=float, default=0.5,
                    help="combined color+pHash similarity at/above which a frame is flagged near_duplicate (default 0.5)")
    ap.add_argument("--tomer-window", type=int, default=64, help="kept frames per source compared against (default 64)")
    ap.add_argument("--anomaly-fallback", nargs="?", const="{}", default=None, metavar="JSON",
                    help='add the horizon-band sea-anomaly proposer behind the detector (optional SeaAnomalyProposer kwargs JSON, e.g. \'{"z_thr":3.5}\')')
    ap.add_argument("-v", "--verbose", action="store_true")
    a = ap.parse_args(argv)
    logging.basicConfig(level=logging.DEBUG if a.verbose else logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")

    if a.mock:
        from .service import MockEngine

        engine = MockEngine()
    elif a.prioritizer == "tomer" and not a.detector:
        from .service import NoopEngine

        engine = NoopEngine()
    else:
        a.detector = a.detector or default_detector()
        if not a.detector:
            ap.error(f"--detector is required unless --mock or --prioritizer tomer (no {DEFAULT_DETECTOR} found)")
        from edgeml.runtime.threads import pin_threads

        pin_threads(a.threads)
        from .service import EdgemlEngine

        engine = EdgemlEngine(a.detector, a.embed, threads=a.threads, fps=a.fps, engine=a.engine, tracker=a.tracker, asso=a.asso,
                              max_lost_s=a.max_lost_s, score_thr=a.score_thr, det_low_thr=a.det_low_thr,
                              agg=json.loads(a.agg) if a.agg else None, trk=json.loads(a.trk) if a.trk else None,
                              text_tower=a.text_tower,
                              anomaly_fallback=json.loads(a.anomaly_fallback) if a.anomaly_fallback is not None else None)
    prioritizer = None
    if a.prioritizer != "edgeml":
        from .tomer import TomerPrioritizer

        prioritizer = TomerPrioritizer(a.tomer_model, a.tomer_policy, threads=a.threads, window=a.tomer_window,
                                       dup_threshold=a.tomer_dup_threshold)
    from .server import serve

    serve(engine, a.listen, quiet=not a.verbose, prioritizer=prioritizer, mode=a.prioritizer)


if __name__ == "__main__":
    main()
