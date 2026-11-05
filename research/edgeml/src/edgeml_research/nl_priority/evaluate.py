"""Evaluate candidate labels against separate held-out annotations.

Ground-truth JSONL schema: {id,labels:[concept,...]}. This evaluator sends only
id/embedding/modelId to inference; annotations never enter feature generation.
"""
from __future__ import annotations
import argparse
import json
import time
from pathlib import Path
from .ingest import post


def metrics(predictions, truth, concepts):
    output = {}
    for concept in concepts:
        tp = sum(predictions.get(key) == concept and concept in labels for key, labels in truth.items())
        fp = sum(predictions.get(key) == concept and concept not in labels for key, labels in truth.items())
        fn = sum(predictions.get(key) != concept and concept in labels for key, labels in truth.items())
        output[concept] = {"tp": tp, "fp": fp, "fn": fn, "support": tp + fn,
                           "precision": tp / (tp + fp) if tp + fp else None, "recall": tp / (tp + fn) if tp + fn else None}
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--items", required=True)
    parser.add_argument("--annotations", required=True)
    parser.add_argument("--concept", action="append", required=True)
    parser.add_argument("--vision-url", default="http://127.0.0.1:8098")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    items = json.loads(Path(args.items).read_text())["items"]
    annotations = [json.loads(line) for line in Path(args.annotations).read_text().splitlines() if line.strip()]
    if any(not isinstance(row, dict) or not isinstance(row.get("id"), str) or not isinstance(row.get("labels"), list)
           or any(not isinstance(label, str) for label in row["labels"]) for row in annotations):
        raise ValueError("annotation rows require an id and a list of label strings")
    truth = {row["id"]: row["labels"] for row in annotations}
    if len(truth) != len(annotations):
        raise ValueError("duplicate annotation IDs")
    if not truth or set(truth) - {i["id"] for i in items}:
        raise ValueError("annotations must refer to nonempty held-out item IDs")
    request = [{key: item[key] for key in ("id", "embedding", "modelId")} for item in items if item["id"] in truth]
    started = time.perf_counter()
    response = post(args.vision_url.rstrip("/") + "/classify", {"concepts": args.concept, "items": request})
    elapsed = time.perf_counter() - started
    predictions = {match["id"]: match["label"] for match in response["matches"] if match["accepted"]}
    report = {"modelId": response["modelId"], "items": len(truth), "concepts": args.concept,
              "abstentionRate": 1 - len(predictions) / len(truth), "elapsedSeconds": elapsed,
              "perConcept": metrics(predictions, truth, args.concept),
              "limitations": ["Experimental zero-shot labels; confidence is not calibrated probability.",
                              "Baseline labels and unknownness are never changed by this service.",
                              "Acceptance thresholds are preset research defaults, not fit on this evaluation set."]}
    Path(args.output).write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
