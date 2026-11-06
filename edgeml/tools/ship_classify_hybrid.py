"""Confidence-gated hybrid: trust the linear probe when it is confident, else fall back to zero-shot.

The probe wins in-domain (SeaShips / IR / UAV) but collapses on onboard YouTube views (gf_*), where MobileCLIP zero-shot
is strong. The gate tau is picked on DEV sets only (mean macro-recall over --dev), then reported once on --test.

  python edgeml/tools/ship_classify_hybrid.py --out ~/data/shipcls_cov --probe mobileclip-s1-probe-public \
      --fallback mobileclip-s1-zeroshot --dev seaships_dev gf_dev ir_dev --test seaships_test gf_test ir_test sds_test
"""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent))
from ship_classify_report import score  # noqa: E402


def hybrid(probe, fb, tau):
    return {"idx": probe["idx"], "preds": [p if p["conf"] >= tau else f for p, f in zip(probe["preds"], fb["preds"])]}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--probe", required=True)
    ap.add_argument("--fallback", required=True)
    ap.add_argument("--dev", nargs="+", required=True)
    ap.add_argument("--test", nargs="+", required=True)
    a = ap.parse_args()
    load = lambda m, s: json.load(open(a.out / f"pred_{m}_{s}.json"))  # noqa: E731
    items = {s: json.load(open(a.out / f"items_{s}.json")) for s in a.dev + a.test}
    best = max(((float(np.mean([score(items[s], hybrid(load(a.probe, s), load(a.fallback, s), t))[1] for s in a.dev])), t)
                for t in np.round(np.arange(0.0, 1.0001, 0.05), 2)))
    tau = best[1]
    res = {"probe": a.probe, "fallback": a.fallback, "tau_dev": tau, "dev_mean_macro_recall": round(best[0], 3), "sets": {}}
    for s in a.dev + a.test:
        acc, mr, _, _ = score(items[s], hybrid(load(a.probe, s), load(a.fallback, s), tau))
        res["sets"][s] = {"top1": round(acc, 3), "macro_recall": round(mr, 3)}
    print(json.dumps(res, indent=1))


if __name__ == "__main__":
    main()
