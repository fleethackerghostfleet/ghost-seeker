"""python -m edgeml.sidecar --listen :9090 --detector <onnx|xml> [--embed <mobileclip dir>] [--mock]"""
from __future__ import annotations

import argparse
import json
import logging


def main(argv=None) -> None:
    ap = argparse.ArgumentParser(prog="edgeml.sidecar", description=__doc__)
    ap.add_argument("--listen", default=":9090", help="host:port (default :9090)")
    ap.add_argument("--mock", action="store_true", help="scripted engine, no models (Go tests / CI)")
    ap.add_argument("--detector", "--det", help="YOLO ONNX (ORT CPU) or OpenVINO IR, e.g. ~/models/D/yolo11n-640/model.onnx")
    ap.add_argument("--engine", default="auto", help="ort | openvino | auto")
    ap.add_argument("--embed", default=None, help="MobileCLIP ONNX export dir (meta.json, model.onnx, text_cache.json); 'none' disables")
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--fps", type=float, default=5.0, help="nominal frame rate used when requests carry no captured_at_unix_ms")
    ap.add_argument("--tracker", default="bytetrack")
    ap.add_argument("--asso", default="centroid")
    ap.add_argument("--max-lost-s", type=float, default=6.0)
    ap.add_argument("--score-thr", type=float, default=0.25)
    ap.add_argument("--det-low-thr", type=float, default=0.05)
    ap.add_argument("--agg", default=None, help='AggregatorParams overrides as JSON, e.g. \'{"min_hits":2}\'')
    ap.add_argument("--text-tower", default="cache", choices=["cache", "torch"],
                    help="EmbedText source: 'cache' = ORT text_cache.json only (no torch); 'torch' = fall back to the torch text tower for uncached text")
    ap.add_argument("-v", "--verbose", action="store_true")
    a = ap.parse_args(argv)
    logging.basicConfig(level=logging.DEBUG if a.verbose else logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")

    if a.mock:
        from .service import MockEngine

        engine = MockEngine()
    else:
        if not a.detector:
            ap.error("--detector is required unless --mock")
        from edgeml.runtime.threads import pin_threads

        pin_threads(a.threads)
        from .service import EdgemlEngine

        engine = EdgemlEngine(a.detector, a.embed, threads=a.threads, fps=a.fps, engine=a.engine, tracker=a.tracker, asso=a.asso,
                              max_lost_s=a.max_lost_s, score_thr=a.score_thr, det_low_thr=a.det_low_thr,
                              agg=json.loads(a.agg) if a.agg else None, text_tower=a.text_tower)
    from .server import serve

    serve(engine, a.listen, quiet=not a.verbose)


if __name__ == "__main__":
    main()
