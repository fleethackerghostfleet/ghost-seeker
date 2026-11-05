"""Latency / throughput / RSS benchmark for one exported detector on real frames.

Reports ms/frame p50/p95 (end-to-end incl. pre/post), model-only ms, sustained FPS over a timed window, RSS
after warm-up, peak RSS, model bytes and the thread/CPU pinning in effect. Runs as its own process so RSS is
attributable to one model.
"""
from __future__ import annotations

import argparse
import json
import os
import platform
import time
from pathlib import Path

import numpy as np


def bench_detector(det, frames: list[np.ndarray], warmup: int = 10, duration_s: float = 15.0, min_iters: int = 50) -> dict:
    from edgeml.runtime.backends import peak_rss_mb, rss_mb

    for i in range(warmup):
        det.detect(frames[i % len(frames)])
    rss_after_warmup = rss_mb()
    lat, infer = [], []
    t_start = time.perf_counter()
    i = 0
    while (time.perf_counter() - t_start < duration_s) or i < min_iters:
        t0 = time.perf_counter()
        det.detect(frames[i % len(frames)])
        lat.append((time.perf_counter() - t0) * 1e3)
        infer.append(det.last_timing_ms.get("infer", float("nan")))
        i += 1
    wall = time.perf_counter() - t_start
    lat = np.asarray(lat)
    return {
        "n_iters": int(i),
        "ms_p50": float(np.percentile(lat, 50)),
        "ms_p95": float(np.percentile(lat, 95)),
        "ms_mean": float(lat.mean()),
        "infer_ms_p50": float(np.nanpercentile(infer, 50)),
        "fps_sustained": float(i / wall),
        "rss_mb_after_warmup": rss_after_warmup,
        "peak_rss_mb": peak_rss_mb(),
        "model_bytes": int(det.model_bytes),
    }


def load_frames(frames_dir: str, n: int = 64) -> list[np.ndarray]:
    import cv2

    paths = sorted(Path(frames_dir).glob("*.jpg"))
    step = max(1, len(paths) // n)
    frames = [cv2.imread(str(p)) for p in paths[::step][:n]]
    return [f for f in frames if f is not None]


def hardware_info() -> dict:
    info = {"platform": platform.platform(), "python": platform.python_version(), "cpu_count": os.cpu_count()}
    try:
        with open("/proc/cpuinfo") as f:
            for line in f:
                if line.startswith("model name"):
                    info["cpu_model"] = line.split(":", 1)[1].strip()
                    break
        flags = Path("/proc/cpuinfo").read_text()
        info["isa"] = {k: (k in flags) for k in ("avx2", "avx512f", "avx512_vnni", "avx_vnni", "amx_int8", "amx_bf16")}
    except OSError:
        pass
    from edgeml.runtime.threads import affinity_cpus

    info["affinity_cpus"] = affinity_cpus()
    info["env_isa_cap"] = os.environ.get("ONEDNN_MAX_CPU_ISA")
    return info


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", required=True, help="model.onnx or model.xml (with meta.json beside it)")
    ap.add_argument("--engine", default="auto", choices=["auto", "openvino", "ort"],
                    help="auto: .onnx->ort (portable), .xml->openvino (Intel INT8 IR)")
    ap.add_argument("--ort-providers", default=None,
                    help="comma-separated ORT EPs, e.g. CoreMLExecutionProvider,CPUExecutionProvider (default env EDGEML_ORT_PROVIDERS or CPU)")
    ap.add_argument("--frames", required=True)
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--duration", type=float, default=15.0)
    ap.add_argument("--n-frames", type=int, default=64)
    ap.add_argument("--out", help="append one JSON line here")
    args = ap.parse_args()
    if args.ort_providers:
        os.environ["EDGEML_ORT_PROVIDERS"] = args.ort_providers

    from edgeml.runtime.threads import pin_threads

    pin_threads(args.threads)
    from edgeml.runtime.detectors import load_detector

    frames = load_frames(args.frames, args.n_frames)
    t0 = time.perf_counter()
    det = load_detector(args.model, engine=args.engine, threads=args.threads)
    load_s = time.perf_counter() - t0
    res = bench_detector(det, frames, duration_s=args.duration)
    res.update({"model": det.name, "model_path": args.model, "engine": args.engine, "precision": det.session.precision,
                "input_size": det.input_size, "threads": args.threads, "load_s": load_s, "frame_shape": list(frames[0].shape),
                "hardware": hardware_info()})
    print(json.dumps(res))
    if args.out:
        with open(args.out, "a") as f:
            f.write(json.dumps(res) + "\n")


if __name__ == "__main__":
    main()
