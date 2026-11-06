#!/usr/bin/env python3
"""Reproducible held-out clean/v3 frame-tag evaluation (no GT proposal crops).

Consumes published v3 catalog JSONLs, samples by SHA256, calibrates on dev only,
then evaluates test once. Source images retain the catalog's GCS URI and hash.
The queried labels are NOT passed to any closed-set classifier. YOLO only
proposes generic object boxes; regions() also adds a full frame and 2x2 grid.
"""
import argparse
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import time

import cv2
import numpy as np
import requests

from edgeml.embed.clip_embedder import ClipEmbedder
from edgeml.runtime.detectors import load_detector
from edgeml.sidecar.open_vocab import ONNXTextTower, OpenVocabulary, TextCache, regions

QUERIES = {"buoy": "buoy", "jetski": "jetski", "life saving appliances": "life_saving_appliances"}


def rows(path):
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line]


def sample(catalog, count):
    annotations = defaultdict(set)
    for a in rows(catalog / "annotations/seadronessee.jsonl"):
        if a["source"] != "gt":
            raise ValueError("evaluation requires human ground truth")
        annotations[a["image_id"]].add(a["label"])
    images = rows(catalog / "images/seadronessee.jsonl")
    selected = {}
    for split, n in (("dev", count), ("test", count * 2)):
        pool = sorted([i for i in images if i["eval_split"] == split], key=lambda i: i["sha256"])
        for label in list(QUERIES.values()) + [None]:
            candidates = [i for i in pool if (label in annotations[i["image_id"]] if label else
                          not annotations[i["image_id"]].intersection(QUERIES.values()))]
            for im in candidates[:n]:
                if "/clean/v1/" in im["gcs_uri"]:
                    raise ValueError("v1 images are forbidden")
                selected[im["sha256"]] = {**im, "labels": sorted(annotations[im["image_id"]])}
    dev = {i["source_video"] for i in selected.values() if i["eval_split"] == "dev"}
    test = {i["source_video"] for i in selected.values() if i["eval_split"] == "test"}
    if dev.intersection(test):
        raise ValueError("dev/test source video leakage")
    return sorted(selected.values(), key=lambda i: (i["eval_split"], i["sha256"]))


def fetch(images, output, gcloud):
    token = subprocess.check_output([gcloud, "auth", "print-access-token"], text=True).strip()
    def one(im):
        dest = output / (im["sha256"] + ".jpg")
        if dest.is_file() and hashlib.sha256(dest.read_bytes()).hexdigest() == im["sha256"]:
            return
        response = requests.get("https://storage.googleapis.com/" + im["gcs_uri"][5:],
                                headers={"Authorization": "Bearer " + token}, timeout=90)
        response.raise_for_status()
        if hashlib.sha256(response.content).hexdigest() != im["sha256"]:
            raise ValueError("image hash mismatch: " + im["image_id"])
        dest.write_bytes(response.content)
    with ThreadPoolExecutor(8) as pool:
        list(pool.map(one, images))


def metrics(images, predictions, split):
    result = {}
    for j, (query, label) in enumerate(QUERIES.items()):
        selected = [(i, im) for i, im in enumerate(images) if im["eval_split"] == split]
        tp = sum(predictions[i, j] and label in im["labels"] for i, im in selected)
        fp = sum(predictions[i, j] and label not in im["labels"] for i, im in selected)
        positives = sum(label in im["labels"] for _, im in selected)
        negatives = len(selected) - positives
        result[query] = dict(tp=int(tp), fp=int(fp), fn=int(positives-tp), tn=int(negatives-fp),
                             precision=float(tp/(tp+fp)) if tp+fp else 0., recall=float(tp/positives) if positives else 0.,
                             negative_fpr=float(fp/negatives) if negatives else 0.)
    return result


def main(args):
    args.output.mkdir(parents=True, exist_ok=True)
    cache = args.output / "images"
    cache.mkdir(exist_ok=True)
    images = sample(args.catalog, args.sample)
    manifest = "".join(json.dumps(i, sort_keys=True)+"\n" for i in images)
    (args.output / "manifest.jsonl").write_text(manifest)
    print(f"Selected {len(images)} frames; fetching hash-verified images", flush=True)
    fetch(images, cache, args.gcloud)
    cv2.setNumThreads(1)
    clip = ClipEmbedder("mobileclip-s1", backend="ort", onnx_dir=args.models / "mobileclip-s1", threads=4)
    text = TextCache(ONNXTextTower(args.text, threads=4))
    matcher = OpenVocabulary(clip, text)
    detector = load_detector(args.models / "yolo11n-640/model.onnx", engine="ort", threads=4, score_thr=.05)
    features = args.output / "features"
    features.mkdir(exist_ok=True)
    scores, margins = [], []
    for i, im in enumerate(images):
        saved = features / (im["sha256"] + ".npz")
        if saved.exists():
            data = np.load(saved)
            feats = data["features"]
        else:
            image = cv2.imread(str(cache / (im["sha256"] + ".jpg")))
            boxes = regions(image, detector.detect(image))
            feats = clip.embed_crops([image[y:y+h, x:x+w] for x, y, w, h in boxes])
            np.savez_compressed(saved, features=feats, boxes=boxes)
        s, m = matcher.score(feats, list(QUERIES))
        scores.append(s)
        margins.append(m)
        if i % 25 == 0:
            print(f"Scored {i}/{len(images)}", flush=True)
    def predict(similarity, margin):
        return np.asarray([np.any((s >= similarity) & (m >= margin), axis=0) for s, m in zip(scores, margins)])
    candidates = []
    for sim in np.arange(.20, .501, .025):
        for margin in (0., .005, .01, .015, .02, .025, .03, .04, .05, .075, .1):
            result = metrics(images, predict(sim, margin), "dev")
            # All three queries, not just the mean, must meet the dev FPR budget.
            if max(x["negative_fpr"] for x in result.values()) <= .05:
                candidates.append((np.mean([x["recall"] for x in result.values()]), sim, margin))
    if not candidates:
        raise ValueError("no threshold meets the 5% per-query dev FPR budget")
    recall, similarity, margin = max(candidates, key=lambda x: (x[0], -x[1], -x[2]))
    predictions = predict(similarity, margin)
    result = {"manifest_sha256": hashlib.sha256(manifest.encode()).hexdigest(), "frames": len(images),
              "threshold": {"similarity": float(similarity), "margin": float(margin)},
              "dev": metrics(images, predictions, "dev"), "test": metrics(images, predictions, "test"),
              "hardware": {"platform": platform.platform(), "cpu_count": os.cpu_count(),
                           "cpu": subprocess.check_output(["lscpu"], text=True)},
              "catalog": str(args.catalog), "queries": QUERIES}
    (args.output / "metrics.json").write_text(json.dumps(result, indent=2)+"\n")
    print(json.dumps({k: v for k, v in result.items() if k != "hardware"}, indent=2), flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--catalog", type=Path, required=True)
    parser.add_argument("--models", type=Path, required=True)
    parser.add_argument("--text", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--sample", type=int, default=50, help="dev frames per query; test uses twice this")
    parser.add_argument("--gcloud", default="gcloud")
    main(parser.parse_args())
