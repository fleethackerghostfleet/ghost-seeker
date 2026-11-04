#!/usr/bin/env python
"""Workstream E: end-to-end runnable demo (frames -> gate -> detector -> tracker -> embed -> sightings -> selection).

    taskset -c 0-3 python edgeml/scripts/demo.py --clip <MOT seq dir | frames dir | video> [--clip ...] \
        --budgets 72000 480000 1920000 15000000 --out runs/<run_id> [--det <model.xml|model.onnx>] [--embed <dir>]

Per clip it writes <out>/<clip>/: sightings.jsonl (open/update/close events with `value` + byte-exact `forms`),
dets.jsonl, tracks.txt (MOT), selection_<budget>.json (what greedy sends per 60 s window, bytes used, GT objects
delivered vs naive / image-dedup baselines when gt/gt.txt exists), contact_sheet_<budget>.jpg (what shore receives),
timeline.png (detections per frame vs sighting events) and metrics.json; plus <out>/summary.md and config.json.

Integrates C (tracking/aggregator/select/metrics/baselines) and D (ORT/OpenVINO detectors, frame gate, MobileCLIP
zero-shot subtype + unknownness) without re-implementing either.
"""
from __future__ import annotations

import argparse
import json
import os
import platform
import subprocess
import sys
import time
from collections import Counter, defaultdict
from dataclasses import asdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from edgeml.runtime.threads import pin_threads  # noqa: E402  (before onnxruntime / openvino import)

import cv2  # noqa: E402
import numpy as np  # noqa: E402

from edgeml.data.seqs import load_sequence  # noqa: E402
from edgeml.eval.baselines import image_dedup_events, naive_events  # noqa: E402
from edgeml.eval.delivery import delivered_vs_budget  # noqa: E402
from edgeml.eval.sightings import match_sightings, sighting_metrics  # noqa: E402
from edgeml.pipeline import write_mot  # noqa: E402
from edgeml.select import greedy  # noqa: E402
from edgeml.tracking import AggregatorParams, SightingAggregator, make_tracker  # noqa: E402
from edgeml.tracking.aggregator import _cut, _resize_max, crop_region  # noqa: E402
from edgeml.tracking.embed import crop  # noqa: E402

DEFAULT_DET_CANDIDATES = ["~/models/D/yolo11n-640/int8/model.xml", "~/models/D/yolo11n-640/model.onnx", "~/models/yolo11n/model.onnx"]
DEFAULT_EMBED = "~/models/D/mobileclip-s1"
VIDEO_EXT = {".mp4", ".mov", ".mkv", ".avi", ".webm"}


def _json(o):
    if isinstance(o, (np.floating, np.integer)):
        return o.item()
    if isinstance(o, np.ndarray):
        return o.tolist()
    if isinstance(o, Path):
        return str(o)
    raise TypeError(str(type(o)))


def budget_name(b: int) -> str:
    return f"{b // 1000}KB" if b < 1_000_000 else f"{b / 1e6:g}MB"


# ----------------------------------------------------------------------------- frame sources
class FrameSource:
    """Uniform access to a MOT sequence dir, a plain frames dir or a video. Frames are 1-based like MOT."""

    def __init__(self, path: str, fps: float | None, max_frames: int | None):
        self.path = Path(path).expanduser()
        self.seq = None
        self.video = None
        self.clip_id = self.path.stem if self.path.is_file() else self.path.name
        if self.path.is_dir() and (self.path / "seqinfo.ini").exists():
            self.seq = load_sequence(self.path)
            self.fps = self.seq.fps
            self.paths = [self.seq.frame_path(i) for i in range(1, self.seq.n_frames + 1)]
            self.width, self.height = self.seq.width, self.seq.height
        elif self.path.is_dir():
            self.paths = sorted(p for p in self.path.iterdir() if p.suffix.lower() in (".jpg", ".jpeg", ".png"))
            if not self.paths and (self.path / "img1").exists():
                self.paths = sorted((self.path / "img1").glob("*.jpg"))
            self.fps = fps or 1.0
            im = cv2.imread(str(self.paths[0]))
            self.height, self.width = im.shape[:2]
        elif self.path.suffix.lower() in VIDEO_EXT:
            self.video = cv2.VideoCapture(str(self.path))
            src_fps = self.video.get(cv2.CAP_PROP_FPS) or 25.0
            self.fps = fps or min(5.0, src_fps)
            self.step = max(1, int(round(src_fps / self.fps)))
            self.n_src = int(self.video.get(cv2.CAP_PROP_FRAME_COUNT))
            self.width, self.height = int(self.video.get(cv2.CAP_PROP_FRAME_WIDTH)), int(self.video.get(cv2.CAP_PROP_FRAME_HEIGHT))
            self.paths = None
        else:
            raise SystemExit(f"not a sequence dir, frames dir or video: {path}")
        self.max_frames = max_frames
        if self.paths is not None and max_frames:
            self.paths = self.paths[:max_frames]

    @property
    def n_frames(self) -> int:
        if self.paths is not None:
            return len(self.paths)
        n = (self.n_src + self.step - 1) // self.step
        return min(n, self.max_frames) if self.max_frames else n

    def frames(self):
        """Yields (frame_idx 1-based, t_s, image_bgr)."""
        if self.paths is not None:
            for i, p in enumerate(self.paths):
                yield i + 1, i / self.fps, cv2.imread(str(p))
            return
        self.video.set(cv2.CAP_PROP_POS_FRAMES, 0)
        i = src = 0
        while True:
            ok, im = self.video.read()
            if not ok or (self.max_frames and i >= self.max_frames):
                return
            if src % self.step == 0:
                i += 1
                yield i, (i - 1) / self.fps, im
            src += 1

    def read(self, frame_idx: int) -> np.ndarray:
        if self.paths is not None:
            return cv2.imread(str(self.paths[frame_idx - 1]))
        self.video.set(cv2.CAP_PROP_POS_FRAMES, (frame_idx - 1) * self.step)
        ok, im = self.video.read()
        return im if ok else None

    @property
    def gt_tracks(self):
        if self.seq is None or not self.seq.gt:
            return None
        n = self.n_frames
        gt = {oid: [(fr, b) for fr, b in rows if fr <= n] for oid, rows in self.seq.gt_tracks().items()}
        return {oid: rows for oid, rows in gt.items() if rows}


# ----------------------------------------------------------------------------- embedding glue
class EmbedAdapter:
    """C's aggregator embeds the key-frame crop once per sighting (`embed_crops`); D's ClipEmbedder provides it.
    Counts calls and time so the summary can show the embed cost."""

    def __init__(self, clip):
        self.clip = clip
        self.dim = clip.dim
        self.calls = 0
        self.ms = 0.0

    def embed_crops(self, crops):
        t0 = time.perf_counter()
        out = self.clip.embed_crops(crops)
        self.ms += (time.perf_counter() - t0) * 1e3
        self.calls += len(crops)
        return out

    def embed_text(self, texts):
        return self.clip.embed_text(texts)

    def logit_scale(self):
        return self.clip.logit_scale()


class DemoAggregator(SightingAggregator):
    """C's SightingAggregator + D's zero-shot subtype / unknownness, filled in from the key-frame embedding that the
    aggregator already computes, right before each open/update report (so a better key frame can change the subtype)."""

    COARSE = {"aircraft": "aircraft", "buoy": "buoy", "marine mammal": "marine_mammal"}  # every other subtype is a vessel

    def __init__(self, *a, classifier=None, **kw):
        super().__init__(*a, **kw)
        self.classifier = classifier
        self.subtype_calls = 0
        self.zs: dict[str, dict] = {}  # sighting_id -> {"key", "subtype", "prob", "unknownness"}

    def _classify(self, st):
        """Zero-shot on the current key-frame embedding; re-run only when the key frame (hence st.emb) changed."""
        if self.classifier is None or st.emb is None:
            return None
        z = self.zs.get(st.s.sighting_id)
        if z is None or z["key"] is not st.emb:
            r = self.classifier.classify(np.asarray(st.emb, np.float32)[None])[0]
            z = self.zs[st.s.sighting_id] = {"key": st.emb, "subtype": r["subtype"], "prob": float(r["prob"]), "unknownness": float(r["unknownness"])}
            self.subtype_calls += 1
        return z

    def _apply(self, st):
        """`label` = coarse class of the zero-shot subtype (D's prompt set), `subtype`/`unknownness` from zero-shot;
        C's COCO vote survives as `detector_label` / `label_scores`."""
        z = self._classify(st)
        st.detector_label = max(st.label_votes, key=st.label_votes.get) if st.label_votes else st.s.label
        if z is not None:
            st.s.subtype, st.s.unknownness = z["subtype"], z["unknownness"]
            st.s.label = self.COARSE.get(z["subtype"], "boat")
        return z

    def _snapshot(self, st):
        self._apply(st)
        return super()._snapshot(st) | {"subtype": st.s.subtype}

    def _material_change(self, st, t_s):
        r = super()._material_change(st, t_s)
        if r:
            return r
        if t_s - st.last_report_t < self.p.min_update_interval_s or st.n_updates >= self.p.max_updates or not st.last_report:
            return None
        return "subtype_flip" if st.s.subtype != st.last_report.get("subtype") else None

    def _report(self, st, kind, frame_idx, t_s, reason=None):
        z = self._apply(st)
        ev = super()._report(st, kind, frame_idx, t_s, reason)
        ev["detector_label"] = st.detector_label
        if z is not None:
            ev["subtype_prob"] = z["prob"]
        return ev

    def _close(self, st, frame_idx, t_s):
        self._apply(st)
        ev = super()._close(st, frame_idx, t_s)
        ev["detector_label"] = st.detector_label
        return ev


# ----------------------------------------------------------------------------- the loop
def run_pipeline(src: FrameSource, detector, gate, tracker, agg, detect_every: int, out_dir: Path, score_thr: float = 0.25):
    dets_f = open(out_dir / "dets.jsonl", "w")
    per_frame_dets: dict[int, list] = {}
    events: list[dict] = []
    track_rows = []
    frame_ms, det_ms, gate_ms = [], [], []
    n_gated = n_det_frames = 0
    gate_reasons: dict[str, int] = defaultdict(int)
    k = 0
    last_t = 0.0
    wall0 = time.perf_counter()
    for fi, t_s, img in src.frames():
        last_t = t_s
        t0 = time.perf_counter()
        if k % max(1, detect_every) != 0:
            k += 1
            frame_ms.append((time.perf_counter() - t0) * 1e3)
            continue
        k = 1
        if gate is not None:
            g = gate(img, t_s)
            gate_ms.append(g.cost_ms)
            gate_reasons[g.reason] += 1
            if not g.run_detector:
                n_gated += 1
                frame_ms.append((time.perf_counter() - t0) * 1e3)
                continue
        td = time.perf_counter()
        dets = detector.detect(img)
        det_ms.append((time.perf_counter() - td) * 1e3)
        n_det_frames += 1
        tracks = tracker.update(dets, img, fi, t_s)  # all boxes incl. low-score ones for ByteTrack's 2nd stage
        dets = [d for d in dets if d.score >= score_thr]
        agg.set_frame(img)
        events.extend(agg.update(tracks, img, fi, t_s))
        frame_ms.append((time.perf_counter() - t0) * 1e3)
        per_frame_dets[fi] = dets
        dets_f.write(json.dumps({"frame_idx": fi, "t_s": t_s, "dets": [{"box": d.box, "score": d.score, "label": d.label} for d in dets]}, default=_json) + "\n")
        track_rows.extend((fi, tr.track_id, tr.box, tr.score) for tr in tracks)
    events.extend(agg.flush(last_t + 1e-3))
    dets_f.close()
    write_mot(out_dir / "tracks.txt", track_rows)
    with open(out_dir / "sightings.jsonl", "w") as f:
        for e in events:
            f.write(json.dumps(e, default=_json) + "\n")
    wall = time.perf_counter() - wall0
    pct = lambda a, q: float(np.percentile(a, q)) if a else None
    return events, per_frame_dets, track_rows, {
        "n_frames": len(frame_ms), "n_detector_frames": n_det_frames, "n_gated_out": n_gated, "gate_reasons": dict(gate_reasons),
        "n_detections": sum(len(v) for v in per_frame_dets.values()), "n_tracks": len({r[1] for r in track_rows}),
        "wall_s": wall, "processed_fps": len(frame_ms) / wall if wall else None,
        "frame_ms_p50": pct(frame_ms, 50), "frame_ms_p95": pct(frame_ms, 95),
        "detector_frame_ms_p50": pct([m for m in frame_ms if m > 1.0], 50), "det_ms_p50": pct(det_ms, 50), "det_ms_p95": pct(det_ms, 95),
        "gate_ms_p50": pct(gate_ms, 50),
    }


# ----------------------------------------------------------------------------- payload reconstruction (contact sheets)
def render_form(img: np.ndarray, ev: dict, form: dict, p: AggregatorParams) -> tuple[np.ndarray, int]:
    """Re-encode the key frame exactly like C's aggregator did for this form; returns (decoded image, bytes)."""
    H, W = img.shape[:2]
    if form["kind"] == "crop":
        region = form.get("region") or crop_region(ev["best_box"], W, H, p.crop_pad, p.crop_hi_min_short)
        im = _resize_max(_cut(img, region), p.crop_max_side)
    elif form["kind"] == "thumb":
        im = cv2.resize(img, (int(form["w"]), int(form["h"])), interpolation=cv2.INTER_AREA)
    else:
        im = _resize_max(img, p.full_max_side)
    ok, buf = cv2.imencode(".jpg", im, [int(cv2.IMWRITE_JPEG_QUALITY), int(form["quality"])])
    return cv2.imdecode(buf, cv2.IMREAD_COLOR), int(len(buf))


def _put_lines(canvas, lines, x, y, scale=0.45, color=(230, 230, 230), lh=16):
    for i, s in enumerate(lines):
        cv2.putText(canvas, s, (x, y + i * lh), cv2.FONT_HERSHEY_SIMPLEX, scale, color, 1, cv2.LINE_AA)


def contact_sheet(src: FrameSource, events: list[dict], sel_windows: list[dict], budget: int, p: AggregatorParams, path: Path,
                  tile: int = 300, cols: int = 4, max_tiles: int = 24) -> dict:
    """One tile per sighting that got anything through: the fact as text, plus the best image form that was sent
    (decoded from a byte-identical re-encode). Sightings with only a fact show text on a dark tile."""
    received: dict[str, dict] = {}
    for w in sel_windows:
        for c in w["selected"]:
            ev = events[w["event_indices"][c["event_idx"]]]
            r = received.setdefault(c["sighting_id"], {"ev": ev, "forms": [], "bytes": 0})
            r["forms"].append(c["form"] | {"event_idx": w["event_indices"][c["event_idx"]]})
            r["bytes"] += c["bytes"]
            r["ev"] = ev  # latest event wins for the text
    order = {"fact": 0, "crop": 1, "thumb": 2, "full": 3}
    sids = sorted(received, key=lambda s: -received[s]["bytes"])[:max_tiles]
    n = max(1, len(sids))
    rows = (n + cols - 1) // cols
    th = 112  # text band
    canvas = np.full((rows * (tile + th) + 40, cols * tile, 3), 24, np.uint8)
    cv2.putText(canvas, f"{src.clip_id}  budget {budget_name(budget)} / 60 s window  |  {len(received)} sightings received, "
                f"{sum(r['bytes'] for r in received.values()):,} B total", (8, 26), cv2.FONT_HERSHEY_SIMPLEX, 0.6, (255, 255, 255), 1, cv2.LINE_AA)
    for i, sid in enumerate(sids):
        r = received[sid]
        ev = r["ev"]
        x0, y0 = (i % cols) * tile, 40 + (i // cols) * (tile + th)
        best = max(r["forms"], key=lambda f: (order.get(f["kind"], 0), f.get("quality") or 0))
        img_tile = None
        if best["kind"] != "fact":
            kf = src.read(events[best["event_idx"]]["best_frame_idx"])
            if kf is not None:
                im, _ = render_form(kf, events[best["event_idx"]], best, p)
                if best["kind"] != "crop":  # draw the reported box on thumb / full so the viewer can find the object
                    s = im.shape[1] / kf.shape[1]
                    b = events[best["event_idx"]]["best_box"]
                    cv2.rectangle(im, (int(b[0] * s), int(b[1] * s)), (int((b[0] + b[2]) * s), int((b[1] + b[3]) * s)), (0, 220, 255), 1)
                h, w = im.shape[:2]
                sc = min(tile / w, tile / h)
                im = cv2.resize(im, (max(1, int(w * sc)), max(1, int(h * sc))), interpolation=cv2.INTER_AREA)
                img_tile = im
        if img_tile is not None:
            h, w = img_tile.shape[:2]
            canvas[y0 + (tile - h) // 2:y0 + (tile - h) // 2 + h, x0 + (tile - w) // 2:x0 + (tile - w) // 2 + w] = img_tile
        else:
            cv2.rectangle(canvas, (x0 + 4, y0 + 4), (x0 + tile - 4, y0 + tile - 4), (60, 60, 60), 1)
            _put_lines(canvas, ["FACT ONLY", "(no image budget)"], x0 + tile // 2 - 60, y0 + tile // 2, 0.55, (120, 120, 255))
        cnt = Counter(f["kind"] if f["kind"] == "fact" else f"{f['kind']}@q{f.get('quality')}" for f in r["forms"])
        kinds = " ".join(f"{k}x{v}" if v > 1 else k for k, v in sorted(cnt.items(), key=lambda kv: order.get(kv[0].split("@")[0], 0)))
        sub = f"{ev.get('subtype')} p={ev.get('subtype_prob', 0):.2f}" if ev.get("subtype") else ev["label"]
        lines = [f"{sid.split(':')[-1]}  {sub}  unk {ev.get('unknownness', 0):.2f}",
                 f"label {ev['label']}  det {ev.get('detector_label', '-')} {ev['best_score']:.2f}  value {ev.get('value', 0):.2f}",
                 f"brg {ev.get('bearing_deg') if ev.get('bearing_deg') is None else round(ev['bearing_deg'])} deg  {ev.get('size_class')}  {ev.get('aspect')}  {ev.get('closing')}",
                 f"t {ev['first_t_s']:.0f}-{ev['last_t_s']:.0f}s  frames {ev['n_frames']}  tracks {ev['track_ids']}",
                 f"sent {kinds}"[:60], f"{r['bytes']:,} B"]
        _put_lines(canvas, lines, x0 + 6, y0 + tile + 16)
        cv2.rectangle(canvas, (x0, y0), (x0 + tile - 1, y0 + tile + th - 1), (70, 70, 70), 1)
    cv2.imwrite(str(path), canvas, [int(cv2.IMWRITE_JPEG_QUALITY), 82])
    return {"n_sightings_received": len(received), "n_tiles": len(sids)}


# ----------------------------------------------------------------------------- selection
def select_windows(events: list[dict], budget: int, window_s: float, selector_kw: dict) -> list[dict]:
    windows: dict[int, list[int]] = defaultdict(list)
    for i, e in enumerate(events):
        if e.get("event") != "close" and e.get("forms"):
            windows[int(e["t_s"] // window_s)].append(i)
    out = []
    for w in sorted(windows):
        idx = windows[w]
        sel = greedy([events[i] for i in idx], budget, **selector_kw)
        out.append({"window": w, "t0_s": w * window_s, "event_indices": idx, **sel})
    return out


def timeline_png(src: FrameSource, per_frame_dets: dict[int, list], events: list[dict], stats: dict, path: Path, gt_tracks=None):
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    fps = src.fps
    n = src.n_frames
    t = np.arange(1, n + 1) / fps
    dets = np.zeros(n)
    for fi, d in per_frame_dets.items():
        if fi <= n:
            dets[fi - 1] = len(d)
    sids = sorted({e["sighting_id"] for e in events}, key=lambda s: min(e["t_s"] for e in events if e["sighting_id"] == s))
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(12, 3.2 + 0.28 * max(4, len(sids))), sharex=True,
                                   gridspec_kw={"height_ratios": [1, max(1.2, 0.22 * max(4, len(sids)))]})
    ax1.bar(t, dets, width=1 / fps, color="#7a9cc6", label=f"detections / frame ({int(dets.sum())} total)")
    if gt_tracks:
        gtc = np.zeros(n)
        for rows in gt_tracks.values():
            for fr, _ in rows:
                if fr <= n:
                    gtc[fr - 1] += 1
        ax1.step(t, gtc, where="mid", color="#2a9d8f", lw=1, label=f"GT objects visible ({len(gt_tracks)} tracks)")
    ax1.set_ylabel("count")
    ax1.set_title(f"{src.clip_id}: {stats['n_detections']} detections on {stats['n_detector_frames']} frames -> {stats['n_tracks']} tracks -> "
                  f"{sum(1 for e in events if e['event']=='open')} sightings ({sum(1 for e in events if e['event']=='update')} updates)")
    ax1.legend(loc="upper right", fontsize=8)
    mk = {"open": ("o", "#e76f51"), "update": ("^", "#f4a261"), "close": ("x", "#555555")}
    for row, sid in enumerate(sids):
        evs = [e for e in events if e["sighting_id"] == sid]
        ax2.hlines(row, min(e["first_t_s"] for e in evs), max(e["last_t_s"] for e in evs), color="#bbbbbb", lw=4, alpha=0.6)
        for e in evs:
            m, c = mk[e["event"]]
            ax2.plot(e["t_s"], row, m, color=c, ms=7 if e["event"] != "close" else 6, mew=1.5)
        lab = evs[-1].get("subtype") or evs[-1]["label"]
        ax2.text(max(e["last_t_s"] for e in evs) + 0.3, row, f"{sid.split(':')[-1]} {lab}", fontsize=7, va="center")
    for name, (m, c) in mk.items():
        ax2.plot([], [], m, color=c, label=f"{name} ({sum(1 for e in events if e['event']==name)})")
    ax2.set_yticks(range(len(sids)))
    ax2.set_yticklabels([s.split(":")[-1] for s in sids], fontsize=7)
    ax2.set_ylim(-0.8, max(len(sids) - 0.2, 3))
    ax2.set_xlabel("time (s)")
    ax2.set_ylabel("sighting")
    ax2.legend(loc="upper right", fontsize=8, ncol=3)
    ax2.grid(axis="x", alpha=0.3)
    fig.tight_layout()
    fig.savefig(path, dpi=110)
    plt.close(fig)


# ----------------------------------------------------------------------------- main
def fmt(v, p=2):
    if v is None:
        return "-"
    return f"{v:.{p}f}" if isinstance(v, float) else str(v)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--clip", action="append", required=True, help="MOT sequence dir, frames dir or video file (repeatable)")
    ap.add_argument("--budgets", nargs="+", type=int, default=[72_000, 480_000, 1_920_000, 15_000_000], help="bytes per 60 s window")
    ap.add_argument("--window-s", type=float, default=60.0)
    ap.add_argument("--out", required=True)
    ap.add_argument("--det", default=None, help="detector: OpenVINO .xml (INT8 IR) or ONNX; default = first existing of " + ", ".join(DEFAULT_DET_CANDIDATES))
    ap.add_argument("--engine", default="auto", choices=["auto", "ort", "openvino"])
    ap.add_argument("--embed", default=DEFAULT_EMBED, help="MobileCLIP export dir (model.onnx + meta.json + text_cache.json); 'none' disables")
    ap.add_argument("--embed-backend", default="ort", choices=["ort", "openvino"])
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--fps", type=float, default=None, help="frame rate for plain frames dirs / target rate for videos")
    ap.add_argument("--detect-every", type=int, default=1)
    ap.add_argument("--score-thr", type=float, default=0.25, help="detection threshold for tracker init / recorded dets / baselines")
    ap.add_argument("--det-low-thr", type=float, default=0.05, help="detector output floor; boxes in [low, score_thr) only feed ByteTrack's recovery stage")
    ap.add_argument("--tracker", default="bytetrack", help="bytetrack | ocsort | botsort | botsort_cmc")
    ap.add_argument("--asso", default="centroid", help="tracker association: iou | centroid (C: centroid wins on panning/onboard video)")
    ap.add_argument("--max-lost-s", type=float, default=6.0)
    ap.add_argument("--no-gate", action="store_true")
    ap.add_argument("--gate-force-s", type=float, default=10.0, help="FrameGate.force_every_s: run the detector at least this often even if the scene is unchanged")
    ap.add_argument("--tag", default=None, help="suffix appended to clip ids / output dirs (e.g. nogate) so variants can share one run dir")
    ap.add_argument("--append", action="store_true", help="merge this run's rows into an existing <out>/summary.{md,json} instead of overwriting")
    ap.add_argument("--agg", default=None, help="JSON overrides for AggregatorParams")
    ap.add_argument("--selector", default=None, help="JSON kwargs for select.greedy (ladder_decay, max_bytes_per_object)")
    ap.add_argument("--max-frames", type=int, default=None)
    a = ap.parse_args()

    pin_threads(a.threads)
    from edgeml.runtime.detectors import load_detector
    from edgeml.runtime.gate import FrameGate

    det_path = a.det or next((c for c in DEFAULT_DET_CANDIDATES if Path(c).expanduser().exists()), None)
    if det_path is None:
        raise SystemExit("no detector found; pass --det (gs://.../edgeml/models/yolo11n/model.onnx or D's yolo11n-640/int8/model.xml)")
    detector = load_detector(Path(det_path).expanduser(), engine=a.engine, threads=a.threads, score_thr=min(a.score_thr, a.det_low_thr))

    embedder = classifier = None
    if a.embed and a.embed.lower() != "none":
        from edgeml.embed.clip_embedder import ClipEmbedder
        from edgeml.embed.zeroshot import ZeroShotClassifier

        d = Path(a.embed).expanduser()
        meta = json.loads((d / "meta.json").read_text())
        clip = ClipEmbedder(meta["model"], backend=a.embed_backend, onnx_dir=d, threads=a.threads, precision=meta.get("precision", "fp32"))
        embedder = EmbedAdapter(clip)
        classifier = ZeroShotClassifier(embedder)

    out = Path(a.out).expanduser()
    out.mkdir(parents=True, exist_ok=True)
    agg_over = json.loads(a.agg) if a.agg else {}
    agg_params = AggregatorParams(**{"relink_gap_s": 20.0, "min_update_interval_s": 10.0, **agg_over})
    selector_kw = json.loads(a.selector) if a.selector else {}
    sha = subprocess.run(["git", "rev-parse", "--short", "HEAD"], capture_output=True, text=True, cwd=Path(__file__).parent).stdout.strip()
    config = {"command": " ".join(sys.argv), "detector": detector.name, "detector_path": str(det_path), "detector_bytes": detector.model_bytes,
              "embedder": getattr(getattr(embedder, "clip", None), "name", None), "tracker": a.tracker, "asso": a.asso, "max_lost_s": a.max_lost_s,
              "detect_every": a.detect_every, "score_thr": a.score_thr, "det_low_thr": a.det_low_thr, "gate": not a.no_gate, "gate_force_every_s": a.gate_force_s, "threads": a.threads, "cpus": sorted(os.sched_getaffinity(0)),
              "aggregator": asdict(agg_params), "selector": selector_kw, "budgets": a.budgets, "window_s": a.window_s,
              "hardware": platform.processor() or platform.machine(), "python": platform.python_version(), "git_sha": sha}
    cfg_name = f"config_{a.tag or 'append'}.json" if a.append and (out / "config.json").exists() else "config.json"
    (out / cfg_name).write_text(json.dumps(config, indent=1, default=_json))

    rows = []
    for clip_path in a.clip:
        src = FrameSource(clip_path, a.fps, a.max_frames)
        if a.tag:
            src.clip_id = f"{src.clip_id}__{a.tag}"
        cdir = out / src.clip_id
        cdir.mkdir(parents=True, exist_ok=True)
        print(f"== {src.clip_id}: {src.n_frames} frames @ {src.fps:g} fps, {src.width}x{src.height}, GT={'yes' if src.gt_tracks else 'no'}", flush=True)
        eff_fps = src.fps / a.detect_every
        tracker = make_tracker(a.tracker, fps=eff_fps, asso_func=a.asso, max_lost_s=a.max_lost_s, det_thresh=a.score_thr, low_thresh=a.det_low_thr)
        agg = DemoAggregator(src.clip_id, agg_params, embedder=embedder, frame_size=(src.width, src.height), classifier=classifier)
        gate = None if a.no_gate else FrameGate(force_every_s=a.gate_force_s)
        for _ in range(2):  # warm-up so p50/p95 reflect steady state
            detector.detect(src.read(1))
        if embedder is not None:
            embedder.embed_crops([src.read(1)[:256, :256]])
            embedder.calls, embedder.ms = 0, 0.0
        events, per_frame_dets, _rows, stats = run_pipeline(src, detector, gate, tracker, agg, a.detect_every, cdir, a.score_thr)
        stats.update({"n_open": sum(e["event"] == "open" for e in events), "n_update": sum(e["event"] == "update" for e in events),
                      "n_close": sum(e["event"] == "close" for e in events), "embed_calls": embedder.calls if embedder else 0,
                      "embed_ms_total": embedder.ms if embedder else 0.0, "n_relinks": sum(1 for e in events if e.get("reason") == "relink")})
        traj = agg.trajectories()

        # baselines: same detections, per-frame reports (C's eval/baselines), images re-read from disk
        def pf():
            for fi in sorted(per_frame_dets):
                yield fi, (fi - 1) / src.fps, src.read(fi), per_frame_dets[fi]
        naive_ev, naive_traj = naive_events(src.clip_id, pf())
        dedup_ev, dedup_traj = image_dedup_events(src.clip_id, pf())

        gt = src.gt_tracks
        metrics = {"stats": stats}
        budgets = {budget_name(b): b for b in a.budgets}
        matched = {}
        if gt:
            m = sighting_metrics(events, traj, gt, src.fps, src.n_frames)
            matched = match_sightings(traj, gt)
            metrics["sighting"] = {k: v for k, v in m.items() if k not in ("matched_sightings", "false_sighting_ids")}
            metrics["sighting"]["matched_sightings"] = m["matched_sightings"]
            metrics["delivery"] = {
                "sightings": delivered_vs_budget(events, matched, gt, src.width, src.height, budgets, a.window_s, src.n_frames / src.fps, selector_kw),
                "naive": delivered_vs_budget(naive_ev, match_sightings(naive_traj, gt), gt, src.width, src.height, budgets, a.window_s, src.n_frames / src.fps, selector_kw),
                "image_dedup": delivered_vs_budget(dedup_ev, match_sightings(dedup_traj, gt), gt, src.width, src.height, budgets, a.window_s, src.n_frames / src.fps, selector_kw),
            }
            for name, ev in (("naive", naive_ev), ("image_dedup", dedup_ev)):
                metrics["delivery"][name]["n_reports"] = len(ev)
        else:
            metrics["baseline_reports"] = {"naive": len(naive_ev), "image_dedup": len(dedup_ev)}

        # selection per budget + contact sheets
        metrics["selection"] = {}
        for b in a.budgets:
            wins = select_windows(events, b, a.window_s, selector_kw)
            sel = {"budget_bytes_per_window": b, "window_s": a.window_s, "selector": selector_kw, "n_windows": len(wins),
                   "bytes_used": sum(w["bytes_used"] for w in wins), "n_forms_sent": sum(len(w["selected"]) for w in wins),
                   "n_sightings_touched": len({c["sighting_id"] for w in wins for c in w["selected"]}),
                   "forms_by_kind": dict(Counter(c["kind"] for w in wins for c in w["selected"])),
                   "windows": [{"window": w["window"], "t0_s": w["t0_s"], "bytes_used": w["bytes_used"], "n_items": w["n_items"],
                                "sent": [{"sighting_id": c["sighting_id"], "event_idx": w["event_indices"][c["event_idx"]], "kind": c["kind"],
                                          "quality": c["quality"], "bytes": c["bytes"], "gain": c["gain"]} for c in w["selected"]]} for w in wins]}
            if gt:
                sel["delivered"] = {k: metrics["delivery"][k]["budgets"][budget_name(b)] for k in ("sightings", "naive", "image_dedup")}
            (cdir / f"selection_{budget_name(b)}.json").write_text(json.dumps(sel, indent=1, default=_json))
            cs = contact_sheet(src, events, wins, b, agg_params, cdir / f"contact_sheet_{budget_name(b)}.jpg")
            sel.update(cs)
            metrics["selection"][budget_name(b)] = {k: v for k, v in sel.items() if k != "windows"}
        timeline_png(src, per_frame_dets, events, stats, cdir / "timeline.png", gt)
        metrics["config"] = config  # per-clip copy so --append rows keep their own gate/detector settings
        (cdir / "metrics.json").write_text(json.dumps(metrics, indent=1, default=_json))
        rows.append((src.clip_id, src.fps, metrics))
        s = metrics.get("sighting", {})
        print(f"   {stats['n_detections']} dets / {stats['n_tracks']} tracks / {stats['n_open']} sightings (+{stats['n_update']} upd); "
              f"recall {fmt(s.get('object_recall'))}, rep/obj {fmt(s.get('reports_per_object'), 1)}, false/min {fmt(s.get('false_per_min'), 1)}; "
              f"{fmt(stats['frame_ms_p50'], 0)}/{fmt(stats['frame_ms_p95'], 0)} ms/frame p50/p95", flush=True)

    if a.append and (out / "summary.json").exists():
        prev = json.loads((out / "summary.json").read_text())
        new_ids = {r[0] for r in rows}
        names = {budget_name(b) for b in a.budgets}
        kept = []
        for cid, m in prev.items():
            if cid in new_ids:
                continue
            if not names <= set(m.get("selection", {})):
                print(f"!! --append: dropping earlier row {cid} (budgets {sorted(m.get('selection', {}))} != {sorted(names)})")
                continue
            kept.append((cid, m["fps"], m))
        rows = kept + rows
    write_summary(out, rows, config, a)
    print(f"wrote {out / 'summary.md'}")


def write_summary(out: Path, rows, config: dict, a):
    L = [f"# edgeml demo run — {out.name}", "",
         f"`{config['command']}`", "",
         f"detector `{config['detector']}` ({config['detector_bytes']/1e6:.1f} MB), embedder `{config['embedder']}`, tracker `{config['tracker']}`/{config['asso']}, "
         f"detect_every={config['detect_every']}, gate={'on' if config['gate'] else 'off'}, {config['threads']} threads on CPUs {config['cpus']}, git `{config['git_sha']}`", "",
         *([f"Rows with a different command (`--append`): " + "; ".join(f"`{cid}`: `{m['config']['command']}`" for cid, _, m in rows
                                                                       if m.get("config", {}).get("command") not in (None, config["command"])), ""]
           if any(m.get("config", {}).get("command") not in (None, config["command"]) for _, _, m in rows) else []),
         "## Per clip", "",
         "| clip | fps | frames (det / gated) | dets | tracks | sightings open / update / close | GT obj | reports / obj | object recall | dup / obj | false / min | TTFR s | ms/frame p50 / p95 | det ms p50 | embeds |",
         "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for cid, fps, m in rows:
        s, st = m.get("sighting", {}), m["stats"]
        L.append(f"| {cid} | {fps:g} | {st['n_frames']} ({st['n_detector_frames']} / {st['n_gated_out']}) | {st['n_detections']} | {st['n_tracks']} | "
                 f"{st['n_open']} / {st['n_update']} / {st['n_close']} | {s.get('n_gt_objects', '-')} | {fmt(s.get('reports_per_object'), 1)} | {fmt(s.get('object_recall'))} | "
                 f"{fmt(s.get('duplicates_per_object'))} | {fmt(s.get('false_per_min'), 1)} | {fmt(s.get('ttfr_median_s'), 1)} | "
                 f"{fmt(st['frame_ms_p50'], 0)} / {fmt(st['frame_ms_p95'], 0)} | {fmt(st['det_ms_p50'], 0)} | {st['embed_calls']} |")
    L += ["", "`ms/frame` is wall time per input frame on the pinned CPUs including gate, detector, tracker, aggregator and form encoding "
          "(gated-out frames cost only the gate). `reports / obj` = open+update events per matched GT object. `false / min` counts sightings "
          "not matched to a GT track — on `partial` clips and FVessel (AIS-equipped vessels only) many of these are real unlabelled boats.", "",
          "## Delivered to shore vs budget (60 s windows, `select.greedy`" + (f" {config['selector']}" if config['selector'] else "") + ")", "",
          "Unique GT objects with an image form (crop/thumb/full) delivered, out of all GT objects; `fact` column = objects with at least a fact. "
          "Baselines use the same detections: `naive` = one report per detection per frame (fact + full frame), `image_dedup` = frame-level "
          "near-duplicate suppression (no identity).", ""]
    names = [budget_name(b) for b in a.budgets]
    L.append("| clip | pipeline | reports | " + " | ".join(f"{n}: img / fact / KB per obj" for n in names) + " |")
    L.append("|---|---|---|" + "---|" * len(names))
    tot = defaultdict(lambda: defaultdict(lambda: [0, 0, 0, 0]))
    n_gt_tot = 0
    for cid, fps, m in rows:
        if "delivery" not in m:
            L.append(f"| {cid} | (no GT) | sightings {m['stats']['n_open'] + m['stats']['n_update']}, naive {m['baseline_reports']['naive']}, dedup {m['baseline_reports']['image_dedup']} | " + " | ".join(
                f"{m['selection'][n]['n_sightings_touched']} sightings, {m['selection'][n]['bytes_used']/1024:.0f} KB" for n in names) + " |")
            continue
        n_gt = m["delivery"]["sightings"]["n_gt_objects"]
        variant = "__" in cid
        if not variant:
            n_gt_tot += n_gt
        for k, label in (("sightings", "**sightings**"), ("image_dedup", "image_dedup"), ("naive", "naive")):
            d = m["delivery"][k]
            nrep = m["sighting"]["n_reports"] if k == "sightings" else d["n_reports"]
            cells = []
            for n in names:
                b = d["budgets"][n]
                cells.append(f"{b['objects_delivered_image']}/{n_gt} / {b['objects_delivered_fact']}/{n_gt} / {fmt((b['bytes_per_delivered_object'] or 0)/1024, 0) if b['objects_delivered_image'] else '-'}")
                if variant:
                    continue
                t = tot[k][n]
                t[0] += b["objects_delivered_image"]; t[1] += b["objects_delivered_fact"]; t[2] += b["bytes_used"]; t[3] += nrep
            L.append(f"| {cid} | {label} | {nrep} | " + " | ".join(cells) + " |")
    if n_gt_tot:
        for k, label in (("sightings", "**sightings**"), ("image_dedup", "image_dedup"), ("naive", "naive")):
            cells = [f"{tot[k][n][0]}/{n_gt_tot} / {tot[k][n][1]}/{n_gt_tot} / {fmt(tot[k][n][2]/1024/tot[k][n][0], 0) if tot[k][n][0] else '-'}" for n in names]
            L.append(f"| **all GT clips** | {label} | {tot[k][names[0]][3]} | " + " | ".join(cells) + " |")
    L += ["", "## Artifacts per clip", "",
          "`sightings.jsonl` (events with `value` + `forms`), `dets.jsonl`, `tracks.txt` (MOT), `selection_<budget>.json`, "
          "`contact_sheet_<budget>.jpg`, `timeline.png`, `metrics.json`.", ""]
    (out / "summary.md").write_text("\n".join(L))
    (out / "summary.json").write_text(json.dumps({cid: {"fps": fps, **m} for cid, fps, m in rows}, indent=1, default=_json))


if __name__ == "__main__":
    main()
