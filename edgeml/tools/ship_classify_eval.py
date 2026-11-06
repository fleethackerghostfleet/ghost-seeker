"""Evaluate ship-type classifiers (edgeml.embed.shiptype) on labelled vessel crops.

Sets (split rule from configs/splits.yaml -- tune on dev, run test sets once with the final config):
  seaships_train  SeaShips7000 train      (probe training only)
  ir_train/ir_dev/ir_test, sds_train/sds_test   clean v3 recording splits (IR: license unknown, internal dev only)
  seaships_dev    SeaShips7000 valid      (dev)
  gf_dev          ghost_fleet_5fps dev clips, tracks with an unambiguous type (human label from clip + QA sheet)
  seaships_test   SeaShips7000 test       (locked test)
  gf_test         ghost_fleet_5fps test clips (locked test)
  synthetic       web/static/imagery + internal/catalog/imagery.json vessel dets (generation label; report only)

  python tools/ship_classify_eval.py --sets seaships_dev gf_dev --models mobileclip-s1 ... --out ~/data/shipcls
"""
from __future__ import annotations

import argparse
import json
import random
import subprocess
import sys
import time
from collections import defaultdict
from pathlib import Path

import cv2
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from edgeml.embed import shiptype as st  # noqa: E402

REPO = Path(__file__).resolve().parents[2]
DATA = Path.home() / "data"
# (clip, track_id) -> ship type. Human labels from clip title + qa/overview.jpg; ambiguous tracks are excluded.
GF_TRACKS = {
    "gf_dev": {("Cruise_ship_Mediterranean_sunset_off_Italy", 1): "passenger", ("Cruise_ship_Mediterranean_sunset_off_Italy", 2): "passenger",
               ("Cruise_ship_Mediterranean_sunset_off_Italy", 3): "passenger", ("Ship_in_the_Distance_Utopia_of_the_Seas", 27): "passenger",
               ("Canadian_Coast_Guard_Helicopter_Landing", 1): "coast_guard", ("Submarine_surfacing", 8): "submarine"},
    "gf_test": {("Spotted_A_Submarine", 13): "submarine", ("US_Destroyer_POV_Warship_Crossing", 1): "warship",
                ("Ferry_Passing_By_On_River", 3): "passenger", ("Sailboat_on_the_horizon_seg0", 1): "sailboat",
                ("Sailboat_on_the_horizon_seg0", 12): "sailboat", ("Ship_in_the_Distance_Ian", 1): "cargo",
                ("Cruise_Ship_Docked_in_Ocean_with_Dingy_Boats", 10): "passenger"},
}
SEASHIPS_SPLIT = {"seaships_train": ("train", 100), "seaships_dev": ("valid", 50), "seaships_test": ("test", 50)}
GEMINI_CAP = {"seaships_dev": 30, "seaships_test": 30, "ir_dev": 30, "ir_test": 30, "sds_test": 30}  # per native class
# clean v3 sets (split by recording, edgeml/tools/clean_v3.py): (catalog dataset, eval_split, per native class)
CLEAN_V3_SETS = {"ir_train": ("ir_maritime_vessels", "train", 150), "ir_dev": ("ir_maritime_vessels", "dev", 60),
                 "ir_test": ("ir_maritime_vessels", "test", 60), "sds_train": ("seadronessee", "train", 300),
                 "sds_test": ("seadronessee", "test", 120)}
SDS_TO_TYPE = {"boat": "small_boat", "jetski": "small_boat"}  # aerial UAV view; SeaDronesSee has no finer vessel type


def load_set(name: str) -> list[dict]:
    """Items: {img, box(xywh px), gt, group, native}."""
    items = []
    if name.startswith("seaships"):
        split, per = SEASHIPS_SPLIT[name]
        by = defaultdict(list)
        for line in open(DATA / "catalog/annotations/seaships7000.jsonl"):
            a = json.loads(line)
            if f"/{split}/" in a["image_id"] and min(a["box"][2:]) >= 20:
                by[a["label"]].append(a)
        rng = random.Random(0)
        for lab in sorted(by):
            for a in rng.sample(by[lab], min(per, len(by[lab]))):
                items.append({"img": str(DATA / "datasets/seaships7000/images" / split / (a["image_id"].split("/")[-1] + ".jpg")),
                              "box": a["box"], "gt": st.SEASHIPS_TO_TYPE[lab], "native": lab, "group": a["image_id"]})
    elif name in CLEAN_V3_SETS:
        ds, ev, per = CLEAN_V3_SETS[name]
        cat = DATA / "clean_v3/catalog"
        rows = {r["image_id"]: r for r in map(json.loads, open(cat / f"images/{ds}.jsonl")) if r["eval_split"] == ev}
        by = defaultdict(list)
        for line in open(cat / f"annotations/{ds}.jsonl"):
            a = json.loads(line)
            t = json.loads(a["attrs"]).get("ship_type") if ds == "ir_maritime_vessels" else SDS_TO_TYPE.get(a["label"])
            if a["image_id"] in rows and t and min(a["box"][2:]) >= 20:
                by[a["label"]].append((a, t))
        rng = random.Random(0)
        for lab in sorted(by):
            for a, t in rng.sample(by[lab], min(per, len(by[lab]))):
                r = rows[a["image_id"]]
                items.append({"img": str(DATA / r["gcs_uri"].split("/", 3)[3]), "box": a["box"], "gt": t, "native": lab,
                              "group": a["image_id"]})
    elif name.startswith("gf_"):
        for (clip, tid), t in GF_TRACKS[name].items():
            rows = [r.split(",") for r in open(DATA / "eval" / clip / "gt/gt.txt") if r.strip()]
            rows = [r for r in rows if int(r[1]) == tid and min(float(r[4]), float(r[5])) >= 12]
            for r in [rows[int(i)] for i in np.linspace(0, len(rows) - 1, min(8, len(rows)))]:
                items.append({"img": str(DATA / "eval" / clip / "img1" / f"{int(r[0]):06d}.jpg"),
                              "box": [float(v) for v in r[2:6]], "gt": t, "native": t, "group": f"{clip}#{tid}"})
    elif name == "synthetic":
        for im in json.load(open(REPO / "internal/catalog/imagery.json")):
            for i, d in enumerate(im["dets"]):
                if d["cls"] in st.CATALOG_TO_TYPE:
                    b = d["b"]  # catalog boxes are in 480x300; images are 960x600
                    items.append({"img": str(REPO / "web/static" / im["file"]), "box": [2 * b["x"], 2 * b["y"], 2 * b["w"], 2 * b["h"]],
                                  "gt": st.CATALOG_TO_TYPE[d["cls"]], "native": d["cls"], "group": f"{im['file']}#{i}", "src": im["src"]})
    return items


def crops_for(items, square=False):
    cache, out = {}, []
    for it in items:
        if it["img"] not in cache:
            cache = {it["img"]: cv2.imread(it["img"])}
        c = st.crop_box(cache[it["img"]], it["box"])
        out.append(st.square_pad(c) if square else c)
    return out


def make_embedder(m):
    if m == "dinov2-b14":
        return st.DinoEmbedder(arch="vit_base_patch14_dinov2.lvd142m", threads=4)
    if m.startswith("dinov2"):
        return st.DinoEmbedder(threads=4)
    from edgeml.embed.clip_embedder import ClipEmbedder
    return ClipEmbedder(m, threads=4)


def latency_ms(emb, crops, n=30):
    emb.embed_crops(crops[:2])
    t = time.perf_counter()
    for c in crops[:n]:
        emb.embed_crops([c])
    b1 = (time.perf_counter() - t) / min(n, len(crops)) * 1000
    t = time.perf_counter()
    emb.embed_crops(crops[:32])
    return round(b1, 1), round((time.perf_counter() - t) / min(32, len(crops)) * 1000, 1)


def gemini_creds():
    from google.oauth2.credentials import Credentials
    tok = subprocess.check_output(["gcloud", "auth", "print-access-token"], text=True).strip()
    return Credentials(tok)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--sets", nargs="+", required=True)
    ap.add_argument("--models", nargs="+", default=["mobileclip-s0", "mobileclip-s1", "siglip-b16", "openclip-b32", "dinov2-s14", "gemini-2.5-flash"])
    ap.add_argument("--out", default=str(DATA / "shipcls"))
    ap.add_argument("--square", action="store_true", help="letterbox crops to square before embedding (not used for Gemini)")
    ap.add_argument("--unknown-floor", type=float, default=0.0)
    ap.add_argument("--probes", nargs="+", default=["probe=seaships_train"],
                    help="linear probes as tag=train1+train2 (e.g. probe-all=seaships_train+ir_train+sds_train)")
    ap.add_argument("--project", default="qwiklabs-gcp-02-34a870014566")
    a = ap.parse_args()
    out = Path(a.out)
    (out / "emb").mkdir(parents=True, exist_ok=True)
    sets = {s: load_set(s) for s in a.sets}
    crops = {s: crops_for(v, False) for s, v in sets.items()}
    for s, v in sets.items():
        json.dump(v, open(out / f"items_{s}.json", "w"))
        print(s, len(v), dict(sorted(defaultdict(int, {g: sum(1 for i in v if i["gt"] == g) for g in {i["gt"] for i in v}}).items())))
    log = open(out / "runs.log", "a")
    for m in a.models:
        if m.startswith("gemini"):
            g = st.GeminiShipClassifier(a.project, credentials=gemini_creds(), model=m)
            for s, v in sets.items():
                idx = list(range(len(v)))
                if s in GEMINI_CAP:
                    by = defaultdict(list)
                    for i in idx:
                        by[v[i]["native"]].append(i)
                    idx = sorted(j for k in sorted(by) for j in by[k][:GEMINI_CAP[s]])
                t = time.perf_counter()
                preds = {}
                res = g.classify([crops[s][i] for i in idx], workers=8)
                wall = time.perf_counter() - t
                t1 = time.perf_counter()
                for i in idx[:5]:
                    g.classify_one(crops[s][i])
                seq = (time.perf_counter() - t1) / min(5, len(idx)) * 1000
                for i, r in zip(idx, res):
                    preds[i] = r
                json.dump({"model": m, "set": s, "idx": idx, "preds": [preds[i] for i in idx], "latency_ms_seq": round(seq),
                           "wall_s_8workers": round(wall, 1)}, open(out / f"pred_{m}_{s}.json", "w"))
                log.write(f"{time.strftime('%FT%T')} {m} {s} n={len(idx)}\n")
                print(m, s, "done", len(idx), f"seq {seq:.0f} ms/call", flush=True)
            continue
        emb = make_embedder(m)
        model_crops = {s: [st.square_pad(c) for c in v] if a.square else v for s, v in crops.items()}
        E = {}
        for s in sets:
            E[s] = emb.embed_crops(model_crops[s]) if model_crops[s] else np.zeros((0, emb.dim))
            np.save(out / "emb" / f"{m}_{s}.npy", E[s])
        lat = latency_ms(emb, next(c for c in model_crops.values() if c))
        clfs = []
        if not m.startswith("dinov2"):
            clfs.append(("zeroshot", st.ShipTypeClassifier(emb, unknown_floor=a.unknown_floor, name=m)))
        for spec in a.probes:
            tag, _, train = spec.partition("=")
            train = train.split("+")
            if all(t in sets for t in train):
                clfs.append((tag, st.ProbeClassifier(emb).fit(
                    np.concatenate([E[t] for t in train]), [i["gt"] for t in train for i in sets[t]])))
            else:
                print(f"{m} {tag} skipped: {train} not all in this run", flush=True)
        for kind, c in clfs:
            for s in sets:
                if s.endswith("_train"):
                    continue
                preds = c.classify_embeddings(E[s]) if len(E[s]) else []
                json.dump({"model": f"{m}-{kind}", "set": s, "idx": list(range(len(preds))), "preds": preds,
                           "latency_ms_b1": lat[0], "latency_ms_b32": lat[1], "unknown_floor": a.unknown_floor, "square": a.square},
                          open(out / f"pred_{m}-{kind}_{s}.json", "w"))
                log.write(f"{time.strftime('%FT%T')} {m}-{kind} {s} n={len(preds)} floor={a.unknown_floor} square={a.square}\n")
        print(m, "latency ms/crop b1,b32:", lat, flush=True)


if __name__ == "__main__":
    main()
