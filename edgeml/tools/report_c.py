#!/usr/bin/env python
"""Figures for edgeml/reports/C.md from run_c.py outputs: summary tables (markdown), delivered-vs-budget chart,
duplicate-reduction chart, sighting timelines for a few clips, and failure contact sheets.

  python edgeml/tools/report_c.py --runs ~/runs/dev_oracle_centroid ~/runs/dev_yolo_centroid --data ~/data/edgeml_eval \
      --out edgeml/reports/C_assets --timeline-cfg "yolo11s|bytetrack|every1" --timeline-seqs fvessel_clip04 19-Boat MVI_0799
"""
from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path

import cv2
import matplotlib
import numpy as np

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.data.seqs import load_sequence  # noqa: E402
from edgeml.eval.sightings import match_sightings  # noqa: E402
from edgeml.types import box_iou  # noqa: E402

BUDGET_ORDER = ["9.6kbps", "64kbps", "256kbps", "2Mbps"]
BUDGET_LABEL = {"9.6kbps": "72 KB", "64kbps": "480 KB", "256kbps": "1.92 MB", "2Mbps": "15 MB"}


def fmt(v, p=2):
    if v is None:
        return "-"
    return f"{v:.{p}f}" if isinstance(v, float) else str(v)


def load_runs(run_dirs: list[Path]) -> dict[str, dict]:
    summ = {}
    for d in run_dirs:
        s = json.loads((d / "summary.json").read_text())
        tag = d.name
        for cfg, v in s.items():
            v["_run"] = tag
            summ[f"{tag}::{cfg}"] = v
    return summ


def markdown_table(summ: dict[str, dict], keys: list[str] | None = None) -> str:
    keys = keys or list(summ)
    head = "| run | detector | tracker | every | recall | dup/obj | false/min | TTFR s | reports/obj | n reports | HOTA | HOTA@.3 | IDF1 | IDSW | Frag | obj@72KB | obj@480KB | obj@1.92MB | obj@15MB | B/obj@480KB |"
    rows = [head, "|" + "---|" * (head.count("|") - 1)]
    for k in keys:
        v = summ[k]
        run, cfg = k.split("::")
        det, trk, ev = cfg.split("|")
        m, d, t = v["sighting"], v["delivery"]["budgets"], v.get("trackeval") or {}
        rows.append("| " + " | ".join([
            run, det, trk, ev.replace("every", ""), fmt(m["object_recall"]), fmt(m["duplicates_per_object"]), fmt(m["false_per_min"]),
            fmt(m["ttfr_mean_s"], 1), fmt(m["reports_per_object"], 1), fmt(m["n_reports"]), fmt(t.get("HOTA")), fmt(t.get("HOTA_a0.3")), fmt(t.get("IDF1")),
            fmt(t.get("IDSW")), fmt(t.get("Frag")), fmt(d["9.6kbps"]["frac_image"]), fmt(d["64kbps"]["frac_image"]), fmt(d["256kbps"]["frac_image"]),
            fmt(d["2Mbps"]["frac_image"]), fmt(d["64kbps"]["bytes_per_delivered_object"], 0)]) + " |")
    return "\n".join(rows)


def chart_delivery(summ: dict[str, dict], keys: list[str], labels: list[str], out: Path, title: str):
    fig, ax = plt.subplots(1, 2, figsize=(11, 4))
    x = np.arange(len(BUDGET_ORDER))
    for k, lab in zip(keys, labels):
        d = summ[k]["delivery"]["budgets"]
        ax[0].plot(x, [d[b]["frac_image"] or 0 for b in BUDGET_ORDER], marker="o", label=lab)
        ax[1].plot(x, [d[b]["frac_fact"] or 0 for b in BUDGET_ORDER], marker="o", label=lab)
    for a, t in zip(ax, ["unique GT objects delivered with an image (crop/thumb/full)", "unique GT objects delivered as a fact"]):
        a.set_xticks(x)
        a.set_xticklabels([BUDGET_LABEL[b] for b in BUDGET_ORDER])
        a.set_xlabel("bytes per 60 s window")
        a.set_ylabel("fraction of GT objects")
        a.set_ylim(0, 1.05)
        a.grid(alpha=0.3)
        a.set_title(t, fontsize=9)
    ax[0].legend(fontsize=8)
    fig.suptitle(title)
    fig.tight_layout()
    fig.savefig(out, dpi=110)
    plt.close(fig)


def chart_reduction(summ: dict[str, dict], keys: list[str], labels: list[str], out: Path, title: str):
    fig, ax = plt.subplots(figsize=(7, 3.6))
    rep = [summ[k]["sighting"]["reports_per_object"] or 0 for k in keys]
    rec = [summ[k]["sighting"]["object_recall"] or 0 for k in keys]
    bars = ax.bar(labels, rep, color=["#888", "#bbb"] + ["#2a7"] * (len(keys) - 2))
    ax.set_yscale("log")
    ax.set_ylabel("reports per GT object (log)")
    for b, r, v in zip(bars, rec, rep):
        ax.text(b.get_x() + b.get_width() / 2, v * 1.1, f"{v:.1f}\nrecall {r:.2f}", ha="center", fontsize=8)
    ax.set_title(title)
    plt.setp(ax.get_xticklabels(), rotation=20, ha="right", fontsize=8)
    fig.tight_layout()
    fig.savefig(out, dpi=110)
    plt.close(fig)


def _events(run_dir: Path, cfg: str, seq_id: str) -> list[dict]:
    p = run_dir / "events" / cfg / f"{seq_id}.jsonl"
    return [json.loads(l) for l in p.read_text().splitlines() if l.strip()] if p.exists() else []


def _tracks(run_dir: Path, cfg: str, seq_id: str) -> dict[int, list[tuple[int, list[float]]]]:
    p = run_dir / "tracks" / cfg.replace("|", "_") / f"{seq_id}.txt"
    out: dict[int, list] = defaultdict(list)
    if p.exists():
        for l in p.read_text().splitlines():
            f, i, x, y, w, h, *_ = l.split(",")
            out[int(i)].append((int(f), [float(x), float(y), float(w), float(h)]))
    return out


def timeline(run_dir: Path, cfg: str, seq, out: Path, naive_cfg: str | None = None, naive_run: Path | None = None):
    """GT tracks (grey bars), sightings (coloured bars with open/update ticks), naive detections per second (top)."""
    ev = _events(run_dir, cfg, seq.seq_id)
    gt = seq.gt_tracks()
    sids = sorted({e["sighting_id"] for e in ev})
    spans: dict[str, list[float]] = {}
    for e in ev:
        s = spans.setdefault(e["sighting_id"], [e["t_s"], e["t_s"]])
        s[0], s[1] = min(s[0], e["first_t_s"]), max(s[1], e["t_s"])
    # sighting -> gt match for colouring
    straj = []
    for e in ev:
        pass
    fig, ax = plt.subplots(figsize=(11, 1.2 + 0.35 * (len(gt) + len(sids))))
    y = 0
    ylab = []
    for oid, rows in sorted(gt.items()):
        t0, t1 = (rows[0][0] - 1) / seq.fps, (rows[-1][0] - 1) / seq.fps
        ax.broken_barh([(t0, max(t1 - t0, 0.5 / seq.fps))], (y - 0.3, 0.6), color="#bbb")
        ylab.append(f"GT {oid}")
        y += 1
    colors = plt.cm.tab10.colors
    for i, sid in enumerate(sids):
        t0, t1 = spans[sid]
        ax.broken_barh([(t0, max(t1 - t0, 0.5 / seq.fps))], (y - 0.3, 0.6), color=colors[i % 10], alpha=0.8)
        for e in ev:
            if e["sighting_id"] != sid:
                continue
            mk = {"open": "^", "update": "|", "close": "x"}[e["event"]]
            ax.plot(e["t_s"], y, marker=mk, color="k", ms=7 if e["event"] != "update" else 9, ls="none")
        ylab.append(sid.split(":")[-1])
        y += 1
    if naive_cfg:
        nev = _events(naive_run or run_dir, naive_cfg, seq.seq_id)
        per_s = defaultdict(int)
        for e in nev:
            per_s[int(e["t_s"])] += 1
        ax2 = ax.twinx()
        xs = sorted(per_s)
        ax2.step(xs, [per_s[x] for x in xs], where="post", color="#c33", lw=0.8, alpha=0.6)
        ax2.set_ylabel("naive reports / s", color="#c33", fontsize=8)
    ax.set_yticks(range(len(ylab)))
    ax.set_yticklabels(ylab, fontsize=8)
    ax.set_xlabel("time (s)")
    n_rep = sum(1 for e in ev if e["event"] in ("open", "update"))
    ax.set_title(f"{seq.seq_id} — {cfg}: {len(sids)} sightings, {n_rep} reports (▲ open, | update, × close) vs {len(gt)} GT objects", fontsize=9)
    ax.grid(axis="x", alpha=0.3)
    fig.tight_layout()
    fig.savefig(out, dpi=110)
    plt.close(fig)


def _tile(img, box, size=160, pad=0.6):
    H, W = img.shape[:2]
    x, y, w, h = box
    cx, cy, s = x + w / 2, y + h / 2, max(w, h) * (1 + 2 * pad)
    s = max(s, 48)
    x0, y0 = int(max(0, cx - s / 2)), int(max(0, cy - s / 2))
    x1, y1 = int(min(W, cx + s / 2)), int(min(H, cy + s / 2))
    c = img[y0:y1, x0:x1].copy()
    if c.size == 0:
        c = np.zeros((size, size, 3), np.uint8)
    k = size / max(c.shape[:2])
    c = cv2.resize(c, (max(1, int(c.shape[1] * k)), max(1, int(c.shape[0] * k))))
    canvas = np.zeros((size, size, 3), np.uint8)
    canvas[: c.shape[0], : c.shape[1]] = c
    bx, by = int((x - x0) * k), int((y - y0) * k)
    cv2.rectangle(canvas, (bx, by), (int(bx + w * k), int(by + h * k)), (0, 255, 0), 1)
    return canvas


def contact_sheet(tiles: list[tuple[np.ndarray, str]], out: Path, cols=6, size=160):
    if not tiles:
        return False
    rows = (len(tiles) + cols - 1) // cols
    sheet = np.full((rows * (size + 18), cols * size, 3), 30, np.uint8)
    for i, (t, cap) in enumerate(tiles):
        r, c = divmod(i, cols)
        y, x = r * (size + 18), c * size
        sheet[y: y + size, x: x + size] = t
        cv2.putText(sheet, cap[:26], (x + 2, y + size + 13), cv2.FONT_HERSHEY_SIMPLEX, 0.38, (255, 255, 255), 1)
    cv2.imwrite(str(out), sheet, [cv2.IMWRITE_JPEG_QUALITY, 80])
    return True


def failure_sheets(run_dir: Path, cfg: str, seqs: list, out_dir: Path, max_tiles=18):
    """Three sheets: duplicates (several sightings on one GT object), missed GT objects, merged/false sightings."""
    dup_tiles, miss_tiles, false_tiles = [], [], []
    for seq in seqs:
        ev = _events(run_dir, cfg, seq.seq_id)
        if not ev and not seq.gt_tracks():
            continue
        gt = seq.gt_tracks()
        # rebuild sighting trajectories from the track files via track_ids
        tracks = _tracks(run_dir, cfg, seq.seq_id)
        sid_tids = {}
        for e in ev:
            sid_tids.setdefault(e["sighting_id"], set()).update(e["track_ids"])
        straj = [(sid, f, b, 1.0) for sid, tids in sid_tids.items() for tid in tids for f, b in tracks.get(tid, [])]
        m = match_sightings(straj, gt)
        by_obj = defaultdict(list)
        for sid, oid in m.items():
            if oid is not None:
                by_obj[oid].append(sid)
        opens = {e["sighting_id"]: e for e in ev if e["event"] == "open"}
        for oid, sids in by_obj.items():
            if len(sids) > 1 and len(dup_tiles) < max_tiles:
                for sid in sids[:3]:
                    e = opens.get(sid)
                    if e is None:
                        continue
                    img = cv2.imread(str(seq.frame_path(e["best_frame_idx"])))
                    dup_tiles.append((_tile(img, e["best_box"]), f"{seq.seq_id[:10]} gt{oid} {sid.split(':')[-1]} t{e['t_s']:.0f}"))
        for oid, rows in gt.items():
            if oid not in by_obj and len(miss_tiles) < max_tiles:
                f, b = rows[len(rows) // 2]
                img = cv2.imread(str(seq.frame_path(f)))
                miss_tiles.append((_tile(img, b), f"{seq.seq_id[:10]} gt{oid} {len(rows)}fr {int(np.sqrt(b[2]*b[3]))}px"))
        for sid, oid in m.items():
            if oid is None and sid in opens and len(false_tiles) < max_tiles:
                e = opens[sid]
                img = cv2.imread(str(seq.frame_path(e["best_frame_idx"])))
                false_tiles.append((_tile(img, e["best_box"]), f"{seq.seq_id[:10]} {sid.split(':')[-1]} {e['label'][:8]} {e['best_score']:.2f}"))
    made = {}
    for name, tiles in (("duplicates", dup_tiles), ("missed", miss_tiles), ("false_or_merged", false_tiles)):
        p = out_dir / f"failures_{name}.jpg"
        if contact_sheet(tiles, p):
            made[name] = (p, len(tiles))
    return made


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", nargs="+", required=True)
    ap.add_argument("--data", required=True)
    ap.add_argument("--sets", nargs="+", default=["fvessel_clip10", "mvtd_5fps", "smd_vis_onboard_1fps"])
    ap.add_argument("--out", required=True)
    ap.add_argument("--timeline-run", default=None)
    ap.add_argument("--timeline-cfg", default=None)
    ap.add_argument("--timeline-seqs", nargs="*", default=[])
    ap.add_argument("--naive-run", default=None, help="run dir holding the naive baseline events for the timeline overlay")
    ap.add_argument("--failure-run", default=None)
    ap.add_argument("--failure-cfg", default=None)
    ap.add_argument("--chart-keys", nargs="*", default=[], help="run::cfg keys for the delivery/reduction charts")
    ap.add_argument("--chart-labels", nargs="*", default=[])
    ap.add_argument("--title", default="dev")
    a = ap.parse_args()
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    runs = [Path(r).expanduser() for r in a.runs]
    summ = load_runs(runs)
    (out / f"table_{a.title}.md").write_text(markdown_table(summ) + "\n")
    if a.chart_keys:
        labels = a.chart_labels or a.chart_keys
        chart_delivery(summ, a.chart_keys, labels, out / f"delivery_{a.title}.png", f"Objects delivered vs budget ({a.title})")
        chart_reduction(summ, a.chart_keys, labels, out / f"reduction_{a.title}.png", f"Reports per GT object ({a.title})")
    data = Path(a.data).expanduser()
    seq_by_id = {}
    for s in a.sets:
        for d in sorted((data / s).iterdir()):
            if (d / "seqinfo.ini").exists():
                seq_by_id[d.name] = load_sequence(d)
    if a.timeline_cfg and a.timeline_seqs:
        rd = Path(a.timeline_run or a.runs[0]).expanduser()
        det = a.timeline_cfg.split("|")[0]
        for sid in a.timeline_seqs:
            timeline(rd, a.timeline_cfg, seq_by_id[sid], out / f"timeline_{sid}.png", naive_cfg=f"{det}|naive|{a.timeline_cfg.split('|')[2]}",
                     naive_run=Path(a.naive_run).expanduser() if a.naive_run else None)
    if a.failure_cfg:
        rd = Path(a.failure_run or a.runs[0]).expanduser()
        made = failure_sheets(rd, a.failure_cfg, list(seq_by_id.values()), out)
        print("failure sheets:", {k: (str(p), n) for k, (p, n) in made.items()})
    print("wrote", out)


if __name__ == "__main__":
    main()
