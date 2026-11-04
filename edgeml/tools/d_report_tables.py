"""Turn workstream-D result JSON/JSONL into the markdown tables used in reports/D.md.

  python tools/d_report_tables.py --bench results/bench60 --acc results/det_acc_*.jsonl --gate results/gate_default.json \
      --zeroshot results/zeroshot2 --e1 results/e1 --e2e results/e2e --embed results/embed/embed_cost.json --sustained results/sustained --calib results/calib
"""
from __future__ import annotations

import argparse
import glob
import json
from pathlib import Path


def _jload(path):
    with open(path) as f:
        return json.load(f)


def md_table(headers, rows):
    out = ["| " + " | ".join(headers) + " |", "|" + "|".join("---" for _ in headers) + "|"]
    for r in rows:
        out.append("| " + " | ".join(str(x) for x in r) + " |")
    return "\n".join(out)


def f1(x):
    return "-" if x is None else f"{x:.1f}"


def f3(x):
    return "-" if x is None else f"{x:.3f}"


def main():
    ap = argparse.ArgumentParser()
    for k in ("bench", "acc", "gate", "zeroshot", "e1", "e2e", "embed", "sustained", "calib"):
        ap.add_argument(f"--{k}", nargs="*")
    args = ap.parse_args()
    out = []
    if args.bench:
        rows = []
        for f in sorted(glob.glob(f"{args.bench[0]}/*.json")):
            d = _jload(f)
            rows.append((d["model"], d["engine"], d["precision"], d["input_size"], f1(d["ms_p50"]), f1(d["ms_p95"]), f1(d["infer_ms_p50"]), f1(d["fps_sustained"]),
                         f"{d['peak_rss_mb']:.0f}", f"{d['model_bytes'] / 1e6:.1f}", d["n_iters"]))
        rows.sort(key=lambda r: float(r[4]))
        out.append("### CPU leaderboard (4 pinned threads, 60 s each, SMD 1920x1080 frames)\n" + md_table(
            ["model", "engine", "prec", "in", "ms p50", "ms p95", "infer p50", "FPS", "peak RSS MB", "MB", "iters"], rows))
    if args.sustained:
        rows = []
        for f in sorted(glob.glob(f"{args.sustained[0]}/*.json")):
            d = _jload(f)
            rows.append((d["model"], f"{d['n_iters']}", f1(d["ms_p50"]), f1(d["ms_p95"]), f1(d["ms_mean"]), f1(d["fps_sustained"]), f"{d['peak_rss_mb']:.0f}"))
        out.append("### Sustained 10-min runs (4 pinned threads)\n" + md_table(["model", "iters", "ms p50", "ms p95", "ms mean", "FPS", "peak RSS MB"], rows))
    if args.calib:
        rows = []
        for f in sorted(glob.glob(f"{args.calib[0]}/*.json")):
            d = _jload(f)
            rows.append((d["model"], d["threads"], f1(d["ms_p50"]), f1(d["ms_p95"]), f1(d["fps_sustained"])))
        out.append("### Single-thread calibration point\n" + md_table(["model", "threads", "ms p50", "ms p95", "FPS"], rows))
    if args.acc:
        rows = {}
        for f in args.acc:
            for line in Path(f).read_text().splitlines():
                d = json.loads(line)
                rows.setdefault((d["model"], d["set"]), {})[d["precision"]] = d
        table = []
        for (m, s), p in sorted(rows.items()):
            fp = p.get("fp32")
            for prec, d in sorted(p.items()):
                delta = "" if prec == "fp32" or not fp else f"{(d['ap50'] - fp['ap50']) * 100:+.1f}"
                b = d["recall_by_bucket"]
                table.append((m, s, prec, f3(d["ap50"]), delta, f3(d["recall@0.25"]), f"{d['fp_per_image@0.25']:.2f}",
                              " ".join(f"{k}:{v['recall']:.2f}({v['n']})" for k, v in sorted(b.items())), d["n_gt"]))
        out.append("### Vessel AP50 FP32 vs INT8 (class-agnostic, dev only)\n" + md_table(
            ["model", "set", "prec", "AP50", "ΔAP50 pts", "recall@0.25", "FP/img", "recall by box bucket (n)", "GT"], table))
    if args.gate:
        d = _jload(args.gate[0])
        rows = [(c["clip"], c["n"], c["kept"], f"{c['saved_frac'] * 100:.0f}%", json.dumps(c["reasons"]), f1(c["cost_ms_p50"]), f1(c["cost_ms_p95"])) for c in d["clips"]]
        out.append(f"### Gate on 1-fps clips (config {json.dumps(d['config'])})\n" + md_table(["clip", "frames", "kept", "saved", "reasons", "cost p50 ms", "p95"], rows))
        rows = [(c["video"], c["n"], c["kept"], c["frames_with_gt"], c["dropped_with_gt"], c["dropped_unsafe"], json.dumps(c["reasons"])) for c in d["smd"]]
        out.append("### Gate GT safety on SMD dev\n" + md_table(["video", "frames", "kept", "frames w/ GT", "dropped w/ GT", "unsafe drops", "reasons"], rows))
    if args.zeroshot:
        rows, e5rows = [], []
        for f in sorted(glob.glob(f"{args.zeroshot[0]}/*.json")):
            if f.endswith("all.json"):
                continue
            d = _jload(f)
            s = d.get("seaships", {})
            smd = d.get("smd", {})
            rows.append((d["embedder"].replace("-torch-fp32", ""), f"{d['model_mb']:.0f}", d["dim"], f3(s.get("acc_open13")), f3(s.get("acc_restricted4")),
                         " ".join(f"{k.split()[0]}:{v:.2f}" for k, v in s.get("native_acc", {}).items()),
                         " ".join(f"{k}:{v['acc']:.2f}({v['n']})" for k, v in smd.items())))
            e = d.get("e5")
            if e:
                e5rows.append((d["embedder"].replace("-torch-fp32", ""), f3(e["known_seaships_unk_p50"]), f3(e["known_smd_unk_p50"]),
                               f3(e.get("det_fp", {}).get("unk_p50")), f3(e.get("det_fp", {}).get("auroc_vs_seaships")), f3(e.get("det_fp", {}).get("auroc_vs_smd")),
                               f3(e.get("sea_sky", {}).get("unk_p50")), f3(e.get("sea_sky", {}).get("auroc_vs_seaships")),
                               f"{e['flag_rate@0.5']['seaships']:.2f}/{e['flag_rate@0.5']['smd']:.2f}"))
            cm = s.get("confusion_open")
            if cm:
                cls = s["classes"]
                out.append(f"#### Confusion (rows=GT subtype, cols=predicted, open 13-way) — {d['embedder']}\n" + md_table(["GT \\ pred"] + [c[:9] for c in cls], [[cls[i]] + list(map(str, r)) for i, r in enumerate(cm) if any(r)]))
        out.insert(len(out) - sum(1 for o in out if o.startswith("#### Confusion")),
                   "### Zero-shot subtype (SeaShips dev crops, 1.5x context; SMD dev crops)\n" + md_table(
                       ["embedder", "MB", "dim", "acc 13-way", "acc 4-way", "per native class", "SMD per label acc(n)"], rows))
        if e5rows:
            out.append("### E5 unknownness (median; AUROC = negatives rank above known)\n" + md_table(
                ["embedder", "known SeaShips", "known SMD", "det-FP unk", "AUROC vs SeaShips", "vs SMD", "sea/sky unk", "AUROC", "flag@0.5 SeaShips/SMD"], e5rows))
    if args.e1:
        for f in sorted(glob.glob(f"{args.e1[0]}/*.json")):
            if f.endswith("all.json"):
                continue
            d = _jload(f)
            e = d.get("e1")
            if not e:
                continue
            rows = [(g["margin"], g["short_side"], g["codec"], g["q"], f"{g['bytes_p50']:.0f}", f3(g["agree_with_fullres"]), f3(g["acc"]), f"{g['encode_ms_p50']:.2f}") for g in e["grid"]]
            out.append(f"### E1 crop legibility — {d['embedder']} (n={e['n']}, full-res acc {e['fullres_acc']:.3f})\n" + md_table(
                ["margin", "short side", "codec", "q", "bytes p50", "agree w/ full-res", "acc", "encode ms"], rows))
    if args.embed:
        d = _jload(args.embed[0])
        out.append("### Embedding cost per crop (proxy VM, 4 threads, OpenVINO)\n" + md_table(
            ["embedder", "prec", "ms/crop p50", "p95", "ms/crop batch8", "MB", "RSS MB"],
            [(r["embedder"], r["precision"], f1(r["ms_per_crop_p50"]), f1(r["ms_per_crop_p95"]), f1(r["ms_per_crop_batch8"]), f"{r['model_mb']:.0f}", f"{r['rss_mb']:.0f}") for r in d]))
    if args.e2e:
        rows = []
        for f in sorted(glob.glob(f"{args.e2e[0]}/*.json")):
            for r in _jload(f):
                rows.append((r["detector"], (r.get("embedder") or "-").replace("-openvino", ""), "y" if r["gate"] else "n", r["clip"][:24], r["n_frames"], r["n_detected"], r["n_gated_out"],
                             f1(r["frame_ms_p50"]), f1(r["frame_ms_p95"]), f1(r["processed_fps"]), f"{r['peak_rss_mb']:.0f}", r["n_sightings"], r["n_updates"], r["embed_calls"],
                             f1(r["embed_ms"] / max(r["embed_calls"], 1)), f1(r["forms_ms"] / max(r["forms_calls"], 1)), "yes" if r["budget_ok"] else "NO"))
        out.append("### End-to-end budget table (gate → INT8 det → ByteTrack → per-track embed, 4 pinned threads)\n" + md_table(
            ["detector", "embedder", "gate", "clip", "frames", "detected", "gated", "frame ms p50", "p95", "proc FPS", "peak RSS", "sightings", "updates", "embed calls", "ms/embed", "ms/forms", "budget"], rows))
    print("\n\n".join(out))


if __name__ == "__main__":
    main()
