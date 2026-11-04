"""Workstream D: zero-shot maritime subtype + unknownness evaluation, and the E1 crop-legibility grid.

Modes (one run does all that are requested):
  --seaships  : SeaShips dev crops (6 native classes -> 4 subtypes) per embedder: accuracy, confusion matrix, cost.
  --smd       : SMD sample crops (labels Vessel/ship, Buoy, Boat, Speed boat): top-1 subtype per crop, buoy accuracy.
  --e5        : unknownness of (a) known GT crops vs (b) false-positive crops (detector boxes on dev clips/frames with no
                GT overlap, i.e. glare/whitecap/own-ship) vs (c) random sea/sky crops. Reports AUROC known-vs-not.
  --e1        : crop legibility grid margin x short-side x quality x codec: bytes, subtype agreement with the full-res crop
                and subtype accuracy, on N dev crops; writes a contact sheet.

  python tools/d_eval_zeroshot.py --embedders mobileclip-s0 mobileclip-s1 siglip-b16 openclip-b32 --seaships --smd --e5 --e1 \
     --seaships-ann ~/data/catalog/ann/seaships7000.jsonl --seaships-ids ~/data/seaships_dev_ids.txt --seaships-images ~/data/seaships/dev \
     --smd-ann ~/data/catalog/ann/smd_vis_onboard_sample.jsonl --smd-images ~/data/smd/all --det ~/models/export/yolo11n-416/int8/model.xml \
     --clips ~/data/clips/Cargo_Ship_Uncut_View ~/data/clips/Sailing_POV_Corfu_Paxos --out results/zeroshot
"""
from __future__ import annotations

import argparse
import json
import random
import sys
import time
from pathlib import Path

import cv2
import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))


def iou(a, b):
    ix = max(0, min(a[0] + a[2], b[0] + b[2]) - max(a[0], b[0]))
    iy = max(0, min(a[1] + a[3], b[1] + b[3]) - max(a[1], b[1]))
    i = ix * iy
    return i / (a[2] * a[3] + b[2] * b[3] - i + 1e-9)


def load_crops(ann, images_dir, ids=None, videos=None, max_per_class=150, context=1.5, seed=0, label_map=None):
    """-> list of (crop_bgr, native_label, image_stem, box). Balanced per class with a fixed seed."""
    keep = set(Path(ids).read_text().split()) if ids else None
    by_label = {}
    for line in Path(ann).read_text().splitlines():
        d = json.loads(line)
        if not d["box"]:
            continue
        if keep is not None and d["image_id"] not in keep:
            continue
        stem = d["image_id"].split("/")[-1]
        if videos and not any(stem.startswith(v) for v in videos):
            continue
        by_label.setdefault(d["label"], []).append((stem, d["box"]))
    rng = random.Random(seed)
    from edgeml.forms import crop_with_context

    out = []
    for lab, items in sorted(by_label.items()):
        rng.shuffle(items)
        for stem, box in items[:max_per_class]:
            p = Path(images_dir) / f"{stem}.jpg"
            if not p.exists():
                continue
            im = cv2.imread(str(p))
            crop, _ = crop_with_context(im, box, context)
            out.append((crop, lab, stem, box))
    return out


def confusion(y_true, y_pred, classes):
    idx = {c: i for i, c in enumerate(classes)}
    m = np.zeros((len(classes), len(classes)), int)
    for t, p in zip(y_true, y_pred):
        if t in idx and p in idx:
            m[idx[t], idx[p]] += 1
    return m


def auroc(pos, neg):
    """AUROC that `pos` scores higher than `neg` (rank-based)."""
    s = np.concatenate([pos, neg])
    r = np.argsort(np.argsort(s)) + 1
    n1, n0 = len(pos), len(neg)
    return float((r[:n1].sum() - n1 * (n1 + 1) / 2) / (n1 * n0))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--embedders", nargs="+", default=["mobileclip-s0"])
    ap.add_argument("--backend", default="torch", help="torch | openvino (needs --clip-dir)")
    ap.add_argument("--clip-dir", help="root of exported towers (<root>/<short>/[int8])")
    ap.add_argument("--clip-precision", default="fp32", help="subdir under <root>/<short>: '' | fp16 | int8")
    ap.add_argument("--threads", type=int, default=4)
    ap.add_argument("--seaships", action="store_true")
    ap.add_argument("--smd", action="store_true")
    ap.add_argument("--e5", action="store_true")
    ap.add_argument("--e1", action="store_true")
    ap.add_argument("--seaships-ann")
    ap.add_argument("--seaships-ids")
    ap.add_argument("--seaships-images")
    ap.add_argument("--smd-ann")
    ap.add_argument("--smd-images")
    ap.add_argument("--smd-videos", nargs="*", default=["MVI_0790", "MVI_0799"])
    ap.add_argument("--det", help="detector for E5 false-positive mining")
    ap.add_argument("--clips", nargs="*", default=[])
    ap.add_argument("--max-per-class", type=int, default=120)
    ap.add_argument("--context", type=float, default=1.5)
    ap.add_argument("--e1-n", type=int, default=60)
    ap.add_argument("--e5-seaships-bank-only", action="store_true", help="ablation: kNN bank from SeaShips only (single camera)")
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    from edgeml.runtime.threads import pin_threads

    pin_threads(args.threads)
    from edgeml.embed.clip_embedder import ClipEmbedder
    from edgeml.embed.zeroshot import (
        SEASHIPS_TO_SUBTYPE,
        SMD_TO_SUBTYPE,
        ZeroShotClassifier,
    )
    from edgeml.forms import (
        crop_with_context,
        decode_image,
        encode_image,
    )

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)
    results = {}

    seaships = load_crops(args.seaships_ann, args.seaships_images, args.seaships_ids, max_per_class=args.max_per_class, context=args.context) if args.seaships or args.e1 or args.e5 else []
    smd = load_crops(args.smd_ann, args.smd_images, videos=args.smd_videos, max_per_class=1000, context=args.context) if args.smd or args.e5 else []
    print(f"seaships crops {len(seaships)}  smd crops {len(smd)}", file=sys.stderr)

    # E5 negatives: detector boxes with no GT overlap on dev frames + random sea/sky patches from clips
    fp_crops, bg_crops = [], []
    if args.e5:
        rng = random.Random(0)
        for c in args.clips:
            frames = sorted(Path(c).glob("*.jpg"))
            for p in rng.sample(frames, min(15, len(frames))):
                im = cv2.imread(str(p))
                H, W = im.shape[:2]
                for _ in range(2):
                    s = rng.randint(40, 160)
                    x, y = rng.randint(0, W - s), rng.randint(int(H * 0.3), H - s)
                    bg_crops.append(im[y:y + s, x:x + s].copy())
        if args.det:
            from edgeml.runtime.detectors import load_detector

            det = load_detector(args.det, threads=args.threads, score_thr=0.15)
            gt = {}
            for line in Path(args.smd_ann).read_text().splitlines():
                d = json.loads(line)
                if d["box"]:
                    gt.setdefault(d["image_id"].split("/")[-1], []).append(d["box"])
            srcs = [p for v in args.smd_videos for p in sorted(Path(args.smd_images).glob(f"{v}_f*.jpg"))]
            for c in args.clips:
                srcs += sorted(Path(c).glob("*.jpg"))[:200:4]
            for p in srcs:
                im = cv2.imread(str(p))
                for d in det.detect(im):
                    if max([iou(d.box, g) for g in gt.get(p.stem, [])] or [0]) < 0.1 and p.stem in gt:
                        fp_crops.append(crop_with_context(im, d.box, args.context)[0])
                    elif p.stem not in gt and not any(p.stem.startswith(v) for v in args.smd_videos):
                        fp_crops.append(crop_with_context(im, d.box, args.context)[0])  # unlabeled clip: treat low-score det as FP candidate
        print(f"E5 negatives: {len(fp_crops)} detector FPs, {len(bg_crops)} sea/sky patches", file=sys.stderr)

    for short in args.embedders:
        onnx_dir = Path(args.clip_dir) / short / args.clip_precision if args.clip_dir else None
        emb = ClipEmbedder(short, backend=args.backend, onnx_dir=onnx_dir, threads=args.threads, precision=args.clip_precision or "fp32")
        zs = ZeroShotClassifier(emb)
        res = {"embedder": emb.name, "dim": emb.dim, "model_mb": emb.model_bytes / 1e6}
        if seaships:
            t0 = time.perf_counter()
            feats = np.concatenate([emb.embed_crops([c for c, *_ in seaships[i:i + 16]]) for i in range(0, len(seaships), 16)])
            ms = (time.perf_counter() - t0) * 1e3 / len(seaships)
            preds = zs.classify(feats)
            y_true = [SEASHIPS_TO_SUBTYPE[l] for _, l, *_ in seaships]
            y_pred = [p["subtype"] for p in preds]
            classes = sorted(set(y_true))
            classes_all = classes + sorted(set(y_pred) - set(classes))
            acc = float(np.mean([t == p for t, p in zip(y_true, y_pred)]))
            # "known vessel" accuracy restricted to the 4 target subtypes (argmax over those only)
            sub_idx = [zs.classes.index(c) for c in classes]
            y_pred_r = [classes[int(np.argmax([p["probs"][c] for c in classes]))] for p in preds]
            acc_r = float(np.mean([t == p for t, p in zip(y_true, y_pred_r)]))
            sims = np.array([p["max_sim"] for p in preds])
            res["seaships"] = {"n": len(seaships), "acc_open13": acc, "acc_restricted4": acc_r, "ms_per_crop_torch": ms,
                               "classes": classes_all, "confusion_open": confusion(y_true, y_pred, classes_all).tolist(),
                               "confusion_restricted": confusion(y_true, y_pred_r, classes).tolist(),
                               "max_sim_p10_p50_p90": [float(np.percentile(sims, q)) for q in (10, 50, 90)],
                               "unknownness_p50": float(np.median([p["unknownness"] for p in preds])),
                               "native_acc": {}}
            for lab in sorted({l for _, l, *_ in seaships}):
                m = [i for i, (_, l, *_) in enumerate(seaships) if l == lab]
                res["seaships"]["native_acc"][lab] = float(np.mean([y_true[i] == y_pred[i] for i in m]))
            _, _ = sub_idx, None
            known_feats_seaships = feats
        if smd:
            feats_smd = np.concatenate([emb.embed_crops([c for c, *_ in smd[i:i + 16]]) for i in range(0, len(smd), 16)])
            preds = zs.classify(feats_smd)
            rows = {}
            for (c, lab, stem, box), p in zip(smd, preds):
                r = rows.setdefault(lab, {"n": 0, "pred": {}, "vessel_like": 0, "unknownness": []})
                r["n"] += 1
                r["pred"][p["subtype"]] = r["pred"].get(p["subtype"], 0) + 1
                r["unknownness"].append(p["unknownness"])
                target = SMD_TO_SUBTYPE.get(lab)
                if (target is None and p["subtype"] not in ("buoy", "aircraft", "marine mammal")) or p["subtype"] == target:
                    r["vessel_like"] += 1
            for r in rows.values():
                r["acc"] = r["vessel_like"] / r["n"]
                r["unknownness_p50"] = float(np.median(r["unknownness"]))
                del r["unknownness"], r["vessel_like"]
            res["smd"] = rows
        if args.e5 and seaships:
            # dev-only calibration: even-indexed SeaShips dev crops form the bank, odd-indexed ones are scored
            # dev-only calibration: even-indexed SeaShips dev crops (+ even-indexed SMD dev crops, so the bank spans more
            # than one camera) form the bank; odd-indexed ones are scored
            bank_idx, score_idx = np.arange(0, len(seaships), 2), np.arange(1, len(seaships), 2)
            bank_feats = known_feats_seaships[bank_idx]
            if smd and not args.e5_seaships_bank_only:
                bank_feats = np.concatenate([bank_feats, feats_smd[0::2]])
            cal = zs.calibrate(bank_feats)
            cal.update(zs.fit_bank(bank_feats))
            known_pred = zs.classify(known_feats_seaships[score_idx])
            known = np.array([p["unknownness"] for p in known_pred])
            smd_pred = zs.classify(feats_smd[1::2] if not args.e5_seaships_bank_only else feats_smd) if smd else []
            known_smd = np.array([p["unknownness"] for p in smd_pred])
            e5 = {"calibration": cal, "known_seaships_unk_p50": float(np.median(known)), "known_smd_unk_p50": float(np.median(known_smd)) if len(known_smd) else None,
                  "known_seaships_mcm_p50": float(np.median([p["mcm"] for p in known_pred])), "known_seaships_knn_p50": float(np.median([p["knn"] for p in known_pred]))}
            for name, crops in (("det_fp", fp_crops), ("sea_sky", bg_crops)):
                if not crops:
                    continue
                f = np.concatenate([emb.embed_crops(crops[i:i + 16]) for i in range(0, len(crops), 16)])
                pr = zs.classify(f)
                u = np.array([p["unknownness"] for p in pr])
                e5[name] = {"n": len(u), "unk_p50": float(np.median(u)), "auroc_vs_seaships": auroc(u, known),
                            "auroc_vs_smd": auroc(u, known_smd) if len(known_smd) else None,
                            "mcm_p50": float(np.median([p["mcm"] for p in pr])), "knn_p50": float(np.median([p["knn"] for p in pr])),
                            "auroc_mcm_vs_seaships": auroc(np.array([p["mcm"] for p in pr]), np.array([p["mcm"] for p in known_pred])),
                            "auroc_knn_vs_seaships": auroc(np.array([p["knn"] for p in pr]), np.array([p["knn"] for p in known_pred])),
                            "top_subtypes": dict(sorted({s: [p["subtype"] for p in pr].count(s) for s in {p["subtype"] for p in pr}}.items(), key=lambda kv: -kv[1])[:4])}
            ex_dir = out_dir / f"unk_examples_{short}"
            ex_dir.mkdir(exist_ok=True)
            groups = [("known", [seaships[i][0] for i in score_idx], known)]
            for name, crops in (("det_fp", fp_crops), ("sea_sky", bg_crops)):
                if crops:
                    groups.append((name, crops, np.array([p["unknownness"] for p in zs.classify(np.concatenate([emb.embed_crops(crops[i:i + 16]) for i in range(0, len(crops), 16)]))])))
            rows = []
            for name, crops, u in groups:
                order = np.argsort(u)
                pick = list(order[:4]) + list(order[-4:])
                tiles = []
                for i in pick:
                    c = cv2.resize(crops[i], (128, 128))
                    cv2.putText(c, f"{name[:6]} {u[i]:.2f}", (2, 12), cv2.FONT_HERSHEY_SIMPLEX, 0.4, (0, 255, 255), 1)
                    tiles.append(c)
                rows.append(np.hstack(tiles))
            cv2.imwrite(str(ex_dir / "contact.jpg"), np.vstack(rows), [cv2.IMWRITE_JPEG_QUALITY, 80])
            # per-threshold: fraction of each group flagged unknown
            for thr in (0.5, 0.6, 0.7):
                e5[f"flag_rate@{thr}"] = {"seaships": float((known > thr).mean()), "smd": float((known_smd > thr).mean()) if len(known_smd) else None}
            res["e5"] = e5
        if args.e1 and seaships:
            rng = random.Random(1)
            sample = rng.sample(range(len(seaships)), min(args.e1_n, len(seaships)))
            # reference = full-res crop at the eval context
            ref_imgs = []
            for i in sample:
                im = cv2.imread(str(Path(args.seaships_images) / f"{seaships[i][2]}.jpg"))
                ref_imgs.append((im, seaships[i][3], SEASHIPS_TO_SUBTYPE[seaships[i][1]]))
            ref_feats = emb.embed_crops([crop_with_context(im, box, 1.5)[0] for im, box, _ in ref_imgs])
            ref_pred = [p["subtype"] for p in zs.classify(ref_feats)]
            grid = []
            for margin in (1.0, 1.25, 1.5, 2.0):
                for short_side in (48, 64, 96, 128):
                    for codec, q in (("jpeg", 40), ("jpeg", 50), ("jpeg", 75), ("webp", 50)):
                        crops, nbytes, enc_ms = [], [], []
                        for im, box, _ in ref_imgs:
                            c, _ = crop_with_context(im, box, margin)
                            h, w = c.shape[:2]
                            s = short_side / min(h, w)
                            c = cv2.resize(c, (max(1, round(w * s)), max(1, round(h * s))), interpolation=cv2.INTER_AREA if s < 1 else cv2.INTER_LINEAR)
                            t0 = time.perf_counter()
                            payload = encode_image(c, q, codec)
                            enc_ms.append((time.perf_counter() - t0) * 1e3)
                            nbytes.append(len(payload))
                            crops.append(decode_image(payload))
                        f = np.concatenate([emb.embed_crops(crops[i:i + 16]) for i in range(0, len(crops), 16)])
                        pred = [p["subtype"] for p in zs.classify(f)]
                        grid.append({"margin": margin, "short_side": short_side, "codec": codec, "q": q, "bytes_p50": float(np.median(nbytes)),
                                     "bytes_mean": float(np.mean(nbytes)), "agree_with_fullres": float(np.mean([a == b for a, b in zip(pred, ref_pred)])),
                                     "acc": float(np.mean([a == t for a, (_, _, t) in zip(pred, ref_imgs)])), "encode_ms_p50": float(np.median(enc_ms))})
            res["e1"] = {"n": len(sample), "fullres_acc": float(np.mean([a == t for a, (_, _, t) in zip(ref_pred, ref_imgs)])), "grid": grid}
            # contact sheet: 12 crops x (48/64/96/128 @ q50, margin 1.5)
            tiles = []
            for im, box, t in ref_imgs[:12]:
                row = []
                for ss in (48, 64, 96, 128):
                    c, _ = crop_with_context(im, box, 1.5)
                    h, w = c.shape[:2]
                    s = ss / min(h, w)
                    c = cv2.resize(c, (max(1, round(w * s)), max(1, round(h * s))), interpolation=cv2.INTER_AREA)
                    c = decode_image(encode_image(c, 50, "jpeg"))
                    c = cv2.resize(c, (160, 160), interpolation=cv2.INTER_NEAREST)
                    cv2.putText(c, f"{ss}px", (3, 14), cv2.FONT_HERSHEY_SIMPLEX, 0.45, (0, 255, 255), 1)
                    row.append(c)
                tiles.append(np.hstack(row))
            cv2.imwrite(str(out_dir / f"e1_contact_{short}.jpg"), np.vstack(tiles), [cv2.IMWRITE_JPEG_QUALITY, 80])
        results[short] = res
        print(json.dumps({k: v for k, v in res.items() if k != "e1"}, default=float)[:1500], file=sys.stderr)
        (out_dir / f"{short}.json").write_text(json.dumps(res, indent=1, default=float))
    (out_dir / "all.json").write_text(json.dumps(results, indent=1, default=float))


if __name__ == "__main__":
    main()
