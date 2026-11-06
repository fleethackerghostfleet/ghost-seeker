"""Replay policy-split MOT clips through the live engine; evaluate birth flags, not track coverage."""
from __future__ import annotations

import argparse
from dataclasses import asdict
import hashlib
import json
import os
from pathlib import Path
import platform
import time

import numpy as np

from edgeml.data.mot import load_set, read_index
from edgeml.detectors.oracle import OracleDetector
from edgeml.runtime.threads import pin_threads
from edgeml.sidecar.service import EdgemlEngine
from edgeml.types import Detection, box_iou


def best_match(box, targets, iou_thr=.3):
    """Highest IoU >= threshold; stable track-ID tie break. Targets are (ID, xywh)."""
    candidates = [(box_iou(box, target), oid) for oid, target in targets]
    eligible = [(iou, oid) for iou, oid in candidates if iou >= iou_thr]
    return min(eligible, key=lambda pair: (-pair[0], pair[1]))[1] if eligible else None


def birth_metrics(opens, gt_at, births, fps, window_s=5., mid_clip=False):
    """Credit each birth once; early matching startup flags are ignored in mid-clip metrics."""
    if fps <= 0 or window_s < 0:
        raise ValueError("fps must be positive and window_s nonnegative")
    credited, latencies, fp, ignored = set(), [], 0, 0
    ordered = sorted(opens, key=lambda e: (e["frame"], -e["score"], e["id"]))
    for e in ordered:
        frame = e["frame"]
        targets = gt_at.get(frame, [])
        spatial = best_match(e["box"], targets)
        if mid_clip and spatial is not None and births[spatial] == 1 and (frame - 1) / fps <= window_s:
            ignored += 1
            continue
        eligible = [(oid, box) for oid, box in targets if oid not in credited
                    and (not mid_clip or births[oid] > 1)
                    and 0 <= (frame - births[oid]) / fps <= window_s]
        oid = best_match(e["box"], eligible)
        if oid is None:
            fp += 1
        else:
            credited.add(oid)
            latencies.append((frame - births[oid]) / fps)
    return {"tp": len(credited), "fp": fp, "births": sum(not mid_clip or f > 1 for f in births.values()),
            "ignored_startup_signals": ignored, "latencies_s": latencies}


def suppressions(opens, gt_at):
    """A correct suppression matches a GT track seen in an earlier, different OPEN sighting."""
    previous, counts = {}, {"suppressed": 0, "correct_repeat": 0, "matched_not_previously_reported": 0, "unmatched": 0}
    for e in sorted(opens, key=lambda e: (e["frame"], -e["score"], e["id"])):
        oid = best_match(e["box"], gt_at.get(e["frame"], []))
        if not e["new_contact"]:
            counts["suppressed"] += 1
            if oid is None:
                counts["unmatched"] += 1
            elif any(sid != e["id"] for sid in previous.get(oid, ())):
                counts["correct_repeat"] += 1
            else:
                counts["matched_not_previously_reported"] += 1
        if oid is not None:
            previous.setdefault(oid, set()).add(e["id"])
    return counts


def pooled(rows):
    out = {}
    for signal in ("open", "new_contact"):
        out[signal] = {}
        for scope in ("all", "mid_clip"):
            metrics = [r[signal][scope] for r in rows]
            tp, fp, births = (sum(m[k] for m in metrics) for k in ("tp", "fp", "births"))
            latencies = [v for m in metrics for v in m["latencies_s"]]
            out[signal][scope] = {"tp": tp, "fp": fp, "births": births,
                                 "precision": tp / (tp + fp) if tp + fp else 0.,
                                 "recall": tp / births if births else 0.,
                                 "median_latency_s": float(np.median(latencies)) if latencies else None}
    out["suppressions"] = {k: sum(r["suppressions"][k] for r in rows) for k in rows[0]["suppressions"]} if rows else {}
    frames = sum(r["frames"] for r in rows)
    out["frames"], out["clips"] = frames, len(rows)
    out["is_new_us_per_frame"] = sum(r["is_new_ns"] for r in rows) / frames / 1000 if frames else 0.
    out["is_new_calls"] = sum(r["is_new_calls"] for r in rows)
    return out


class TimedNewContact:
    def __init__(self, detector):
        self.detector, self.ns, self.calls = detector, 0, 0

    def is_new(self, event, states):
        start = time.perf_counter_ns()
        result = self.detector.is_new(event, states)
        self.ns += time.perf_counter_ns() - start
        self.calls += 1
        return result


class DetectionCache:
    """Cache final live-stack detections, including empty frames; never bypass the tracker/aggregator."""
    def __init__(self, base, path, metadata):
        self.base, self.path, self.metadata = base, path, metadata
        self.frames, self.index, self.loaded = [], 0, False
        if path.exists():
            content = json.loads(path.read_text())
            if content["metadata"] != metadata or len(content["frames"]) != metadata["frames"]:
                raise ValueError(f"stale detection cache: {path}; use a fresh --cache directory")
            self.frames, self.loaded = content["frames"], True

    def detect(self, image):
        if self.loaded:
            detections = [Detection(**d) for d in self.frames[self.index]]
        else:
            detections = self.base.detect(image)
            self.frames.append([{**asdict(d), "box": [float(v) for v in d.box], "score": float(d.score)} for d in detections])
        self.index += 1
        return detections

    def save(self):
        if not self.loaded:
            if self.index != self.metadata["frames"]:
                raise ValueError("incomplete replay")
            self.path.parent.mkdir(parents=True, exist_ok=True)
            self.path.write_text(json.dumps({"metadata": self.metadata, "frames": self.frames}))


def replay(seq, dataset, mode, args, fingerprint):
    engine = EdgemlEngine(str(args.model), embed=None, fps=seq.info.frame_rate, threads=args.threads,
                          new_contact={"memory_s": 30., "iou_thr": .3, "dist_diag": .5})
    source = f"{dataset}/{seq.seq_id}"
    src = engine._source(source)
    timer = TimedNewContact(src.new_contact)
    src.new_contact = timer
    gt_at = {f: [(g.track_id, g.box) for g in boxes] for f, boxes in seq.gt_by_frame().items()}
    births = {oid: boxes[0].frame for oid, boxes in seq.gt_by_track().items()}
    if mode == "oracle-d0.3":
        detector = OracleDetector({f: [(g.track_id, g.box, g.class_id) for g in boxes]
                                   for f, boxes in seq.gt_by_frame().items()},
                                  seq.labels.get("classes", {}), dropout=.3, seed=1729)
    else:
        seq_hash = hashlib.sha256((seq.dir / "seqinfo.ini").read_bytes())
        for f in seq.frame_ids():
            p = seq.frame_path(f)
            seq_hash.update(f"{p.name}:{p.stat().st_size}:{p.stat().st_mtime_ns}".encode())
        detector = DetectionCache(engine.detector, args.cache / dataset / f"{seq.seq_id}.json",
                                  {"pipeline": fingerprint, "input": seq_hash.hexdigest(), "frames": len(seq),
                                   "threads": args.threads, "stack": "yolo+default-live-tiling+default-thermal"})
    engine.detector = detector
    opens = []
    start = time.perf_counter()
    for frame, ts, image in seq.iter_frames():
        if mode == "oracle-d0.3":
            detector.set_frame(frame)
        response = engine.analyze(source, image, 1700000000000 + round(ts * 1000), [], False)
        for sighting in response["sightings"]:
            if sighting["event"] != "EVENT_OPEN":
                if sighting.get("newContact"):
                    raise AssertionError("non-OPEN newContact")
                continue
            box = sighting["keyDetection"]["box"]
            opens.append({"frame": frame, "id": sighting["id"], "score": sighting["keyDetection"]["confidence"],
                          "box": [box["x"] * image.shape[1], box["y"] * image.shape[0],
                                  box["w"] * image.shape[1], box["h"] * image.shape[0]],
                          "new_contact": sighting.get("newContact", False)})
    if mode == "yolo":
        detector.save()
    result = {"dataset": dataset, "seq_id": seq.seq_id, "mode": mode, "frames": len(seq),
              "fps": seq.info.frame_rate, "license": seq.info.license, "label_source": seq.label_source,
              "is_new_ns": timer.ns, "is_new_calls": timer.calls, "replay_s": time.perf_counter() - start,
              "opens": opens, "suppressions": suppressions(opens, gt_at)}
    for signal in ("open", "new_contact"):
        selected = opens if signal == "open" else [e for e in opens if e["new_contact"]]
        result[signal] = {scope: birth_metrics(selected, gt_at, births, seq.info.frame_rate, args.window_s, mid_clip=scope == "mid_clip")
                          for scope in ("all", "mid_clip")}
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=Path.home() / "data/clean_v3/edgeml/eval")
    parser.add_argument("--model", type=Path, default=Path.home() / "models/D/yolo11n-640/model.onnx")
    parser.add_argument("--cache", type=Path, default=Path.home() / "data/newcontact-cache")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--split", choices=("dev", "test"), required=True)
    parser.add_argument("--threads", type=int, default=4)
    parser.add_argument("--window-s", type=float, default=5.)
    args = parser.parse_args()
    pin_threads(args.threads)
    digest = hashlib.sha256(args.model.read_bytes())
    for p in sorted((Path(__file__).resolve().parents[1] / "src").rglob("*.py")):
        digest.update(p.read_bytes())
    fingerprint = digest.hexdigest()
    rows, skipped = [], []
    for dataset in ("ghost_fleet_5fps", "fvessel_clip10"):
        index = {r["seq_id"]: r for r in read_index(args.data / dataset)}
        for seq in load_set(dataset, root=str(args.data), split=args.split):
            if index[seq.seq_id].get("gt_status") == "incomplete":
                skipped.append(f"{dataset}/{seq.seq_id}")
                continue
            for mode in ("yolo", "oracle-d0.3"):
                result = replay(seq, dataset, mode, args, fingerprint)
                result["gt_status"] = index[seq.seq_id].get("gt_status", "public_gt")
                rows.append(result)
                print(dataset, seq.seq_id, mode, result["suppressions"], f'{result["replay_s"]:.1f}s', flush=True)
    summary = {mode: {dataset: pooled([r for r in rows if r["mode"] == mode and (dataset == "pooled" or r["dataset"] == dataset)])
                      for dataset in ("ghost_fleet_5fps", "fvessel_clip10", "pooled")}
               for mode in ("yolo", "oracle-d0.3")}
    complete = {mode: pooled([r for r in rows if r["mode"] == mode and r["gt_status"] != "partial"])
                for mode in ("yolo", "oracle-d0.3")}
    output = {"split": args.split, "window_s": args.window_s, "iou_thr": .3, "new_contact": {"memory_s": 30, "iou_thr": .3, "dist_diag": .5},
              "cpu": platform.processor(), "affinity": sorted(os.sched_getaffinity(0)), "threads": args.threads,
              "pipeline_fingerprint": fingerprint, "skipped_incomplete": skipped,
              "summary": summary, "complete_gt_only": complete, "sequences": rows}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(output, indent=2) + "\n")
    print(json.dumps(summary, indent=2), flush=True)


if __name__ == "__main__":
    main()
