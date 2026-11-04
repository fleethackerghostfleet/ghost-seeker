"""Render markdown tables from bench_detectors metrics.json files (used to build edgeml/reports/B.md).

  python edgeml/tools/report_tables.py leaderboard --run ~/runs/20261104-b-dev640 --sets seaships7000__dev,smd_vis_onboard_sample__dev
  python edgeml/tools/report_tables.py compare --runs 640=~/runs/..-dev640,960=~/runs/..-dev960 --set seaships7000__dev
"""
from __future__ import annotations

import argparse
import json
import math
from pathlib import Path


def f(x, nd=3):
    if x is None or (isinstance(x, float) and math.isnan(x)):
        return "–"
    return f"{x:.{nd}f}" if isinstance(x, float) else str(x)


def buckets(t):
    """Recall for <32² (tiny+small merged, per literature.md), medium, large, with GT counts."""
    rb, n = t["recall_by_bucket"], t.get("n_gt_by_bucket", {})
    nt, ns = n.get("tiny", 0), n.get("small", 0)
    r32 = ((rb.get("tiny") or 0) * nt + (rb.get("small") or 0) * ns) / (nt + ns) if (nt + ns) else float("nan")
    return r32, nt + ns, rb.get("medium"), n.get("medium", 0), rb.get("large"), n.get("large", 0)


def bstr(t):
    r32, n32, rm, nm, rl, nl = buckets(t)
    return f"{f(r32, 2)} (n={n32}) / {f(rm, 2)} (n={nm}) / {f(rl, 2)} (n={nl})"


def load(run):
    return json.loads((Path(run).expanduser() / "metrics.json").read_text())


def leaderboard(args):
    m = load(args.run)
    sets = args.sets.split(",")
    hdr = ["model", "tier/train", "params M", "GFLOPs", "GPU ms p50"]
    for s in sets:
        short = s.split("__")[0].replace("seaships7000", "SeaShips").replace("smd_vis_onboard_sample", "SMD").replace("singapore_maritime", "SG")
        hdr += [f"{short} AP50", f"{short} AP", f"{short} R@τ", f"{short} P@τ", f"{short} τ", f"{short} R <32²/med/lg", f"{short} FP/sea-frame"]
    rows = []
    for name, sets_m in m.items():
        if "error" in sets_m and len(sets_m) == 1:
            rows.append([name, f"ERROR: {sets_m['error'][:60]}"] + [""] * (len(hdr) - 2))
            continue
        first = next((v for k, v in sets_m.items() if k != "error"), None)
        if first is None:
            continue
        info, lat = first["model"], first["latency"]
        row = [name, f"{info.get('training_data', '')}", f(info.get("params_m"), 1), f(info.get("gflops"), 1), f(lat.get("ms_p50"), 1)]
        sort_key = 0.0
        for s in sets:
            e = sets_m.get(s)
            if not e:
                row += ["–"] * 7
                continue
            v = e["vessel"]
            t = v["at_tau"]
            row += [f(v["mAP50"]), f(v["mAP50-95"]), f(t["recall"]), f(t["precision"]), f(e["tau"], 2),
                    bstr(t), f(t.get("fp_per_sea_only_frame"), 2)]
            if s == sets[0]:
                sort_key = v["mAP50"]
        rows.append((sort_key, row))
    rows = [r for _, r in sorted((r for r in rows if isinstance(r, tuple)), key=lambda x: -x[0])] + [r for r in rows if not isinstance(r, tuple)]
    print("| " + " | ".join(hdr) + " |")
    print("|" + "---|" * len(hdr))
    for r in rows:
        print("| " + " | ".join(r) + " |")


def compare(args):
    runs = dict(kv.split("=", 1) for kv in args.runs.split(","))
    ms = {k: load(v) for k, v in runs.items()}
    models = sorted({n for m in ms.values() for n in m if args.set in m[n]})
    hdr = ["model"] + [f"{k}: AP50 / AP / R@τ / R<32² / R_med / FP-sea / ms" for k in runs]
    print("| " + " | ".join(hdr) + " |")
    print("|" + "---|" * len(hdr))
    for n in models:
        cells = [n]
        for k in runs:
            e = ms[k].get(n, {}).get(args.set)
            if not e:
                cells.append("–")
                continue
            v, t = e["vessel"], e["vessel"]["at_tau"]
            r32, _, rm, _, _, _ = buckets(t)
            cells.append(f"{f(v['mAP50'])} / {f(v['mAP50-95'])} / {f(t['recall'], 2)} / {f(r32, 2)} / {f(rm, 2)} / {f(t.get('fp_per_sea_only_frame'), 2)} / {f(e['latency'].get('ms_p50'), 1)}")
        print("| " + " | ".join(cells) + " |")


def taus(args):
    m = load(args.run)
    out = {}
    for n, sets_m in m.items():
        per = {k.split("__")[0]: v["tau"] for k, v in sets_m.items() if k != "error"}
        if per:
            out[n] = per
    print(json.dumps(out, indent=1))


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    a = sub.add_parser("leaderboard"); a.add_argument("--run", required=True); a.add_argument("--sets", required=True); a.set_defaults(fn=leaderboard)
    c = sub.add_parser("compare"); c.add_argument("--runs", required=True); c.add_argument("--set", required=True); c.set_defaults(fn=compare)
    t = sub.add_parser("taus"); t.add_argument("--run", required=True); t.set_defaults(fn=taus)
    args = ap.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
