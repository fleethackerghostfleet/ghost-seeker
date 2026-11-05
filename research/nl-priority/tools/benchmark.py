#!/usr/bin/env python3
"""Measure real command-to-preview latency and aggregate RSS for specified local processes."""
import argparse
import json
import platform
import subprocess
import threading
import time
import urllib.request
from pathlib import Path
from eval_commands import percentile, post


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8088")
    parser.add_argument("--endpoint", default="/api/preview")
    parser.add_argument("--payload", type=Path, required=True, help="JSON request body; use an independently populated 1000-item queue")
    parser.add_argument("--pid", type=int, action="append", required=True, help="One per additional component; include backend, text service and llama server")
    parser.add_argument("--repeats", type=int, default=30)
    parser.add_argument("--warmup", type=int, default=3)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--queue-size", type=int, required=True, help="Recorded queue size, verified via /api/state by operator")
    args = parser.parse_args()
    payload = json.loads(args.payload.read_text())
    payloads = payload if isinstance(payload, list) else [payload]
    if not payloads or not all(isinstance(p, dict) for p in payloads):
        raise ValueError("Payload must be one request object or a nonempty list")
    with urllib.request.urlopen(args.url.rstrip("/") + "/api/state", timeout=30) as response:
        state = json.load(response)
    actual_queue_size = len(state["items"])
    if actual_queue_size != args.queue_size:
        raise ValueError(f"Expected {args.queue_size} images, state contains {actual_queue_size}")
    samples = []
    stopped = threading.Event()
    missing = set()
    def monitor():
        while not stopped.is_set():
            values = {}
            for pid in args.pid:
                try:
                    rss = int(subprocess.check_output(["ps", "-o", "rss=", "-p", str(pid)], text=True).strip()) * 1024
                    values[str(pid)] = rss
                except (subprocess.CalledProcessError, ValueError):
                    missing.add(pid)
            samples.append({"at": time.time(), "rssBytes": values, "totalBytes": sum(values.values())})
            stopped.wait(.05)
    thread = threading.Thread(target=monitor, daemon=True)
    thread.start()
    measurements = []
    try:
        for n in range(args.warmup + args.repeats):
            start = time.perf_counter()
            selected = {"text":"prioritize cargo ships"} if n < args.warmup else payloads[(n-args.warmup) % len(payloads)]
            status, response = post(args.url.rstrip("/") + args.endpoint, selected, 30)
            elapsed = time.perf_counter() - start
            if status != 200:
                raise RuntimeError(f"Benchmark request failed: {status} {response}")
            if n >= args.warmup:
                measurements.append(elapsed)
    finally:
        stopped.set()
        thread.join()
    peak = max((s["totalBytes"] for s in samples), default=0)
    p95 = percentile(measurements, .95)
    report = {"platform": platform.platform(), "architecture": platform.machine(), "endpoint": args.endpoint,
              "queueSize": actual_queue_size, "uniqueMeasuredRequests": len(payloads), "repeats": len(measurements), "warmup": args.warmup,
              "p50Seconds": percentile(measurements, .5), "p95Seconds": p95,
              "latenciesSeconds": measurements, "aggregatePeakRSSBytes": peak, "pids": args.pid,
              "missingPids": sorted(missing), "samples": samples,
              "measurementNotes": "Warm service with explicit command list; OS RSS sum is sampled every 50 ms. Model download and cold start excluded. Container/VM overhead excluded for native runs. Queue size verified against /api/state. Distinct concepts can measure uncached text encoding; repeated requests measure cache hits.",
              "acceptance": {"atLeast1000Images": actual_queue_size >= 1000,
                             "addedRSSAtMost1GB": peak <= 1_000_000_000 and not missing,
                             "warmP95AtMost5Seconds": p95 is not None and p95 <= 5}}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({k:v for k,v in report.items() if k not in ("samples", "latenciesSeconds")}, indent=2))
    return 0 if all(report["acceptance"].values()) else 1


if __name__ == "__main__":
    raise SystemExit(main())
