#!/usr/bin/env python3
"""Evaluate independently labeled commands through the actual research compiler API."""
import argparse
import collections
import hashlib
import json
import math
from pathlib import Path
import platform
import time
import urllib.error
import urllib.request


def post(url, payload, timeout=15):
    request = urllib.request.Request(url, json.dumps(payload).encode(), {"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as exc:
        return exc.code, json.load(exc)


def percentile(values, fraction):
    return sorted(values)[max(0, math.ceil(len(values) * fraction) - 1)] if values else None


def equivalent(actual, expected):
    # Fixed before execution: spelling/number variations are semantically equal.
    singular = {word + "s": word for word in (
        "ship", "boat", "tanker", "sailboat", "speedboat", "submarine", "buoy", "mammal",
        "tugboat", "raft", "kayak", "ski", "platform", "net", "container", "rowboat", "log",
        "yacht", "shell", "workboat", "catamaran", "hull", "dredger", "jacket", "helicopter",
        "barrel", "trawler", "tire", "marker", "float")}
    singular.update({"dinghies":"dinghy", "ferries":"ferry"})
    def concept(text):
        words = text.strip().lower().split()
        if words:
            words[-1] = singular.get(words[-1], words[-1])
        return " ".join(words)
    def canonical(rules):
        return sorted((concept(r["concept"]), r["action"], round(float(r["weight"]), 3)) for r in rules)
    return canonical(actual) == canonical(expected)


def run(url, corpus, timeout):
    records = []
    for case in corpus["cases"]:
        payload = {"text": case["text"], "rules": case.get("initial", [])}
        start = time.perf_counter()
        try:
            status, response = post(url.rstrip("/") + "/api/compile", payload, timeout)
            if case.get("reject"):
                correct = status == 422
            else:
                correct = status == 200 and equivalent(response.get("rules", []), case["expected"])
                if "operation" in case:
                    correct = correct and response.get("operation") == case["operation"]
            error = None
        except Exception as exc:
            status, response, correct, error = None, {}, False, str(exc)
        records.append({"id": case["id"], "category": case["category"], "text": case["text"],
                        "supported": not case.get("reject", False), "correct": correct,
                        "status": status, "path": response.get("path", "rejected" if status == 422 else "error"),
                        "latencyMs": (time.perf_counter() - start) * 1000,
                        "expected": case.get("expected", "reject"), "actual": response, "error": error})
        print(f'{case["id"]}: {"PASS" if correct else "FAIL"} ({records[-1]["path"]}, {records[-1]["latencyMs"]:.0f} ms)', flush=True)
    return records


def summarize(records):
    groups = collections.defaultdict(list)
    for record in records:
        groups["all"].append(record)
        groups["supported" if record["supported"] else "unsupported"].append(record)
        groups["path:" + record["path"]].append(record)
        groups["category:" + record["category"]].append(record)
    return {group: {"cases": len(rows), "correct": sum(r["correct"] for r in rows),
                    "accuracy": sum(r["correct"] for r in rows) / len(rows),
                    "p50Ms": percentile([r["latencyMs"] for r in rows], .5),
                    "p95Ms": percentile([r["latencyMs"] for r in rows], .95)}
            for group, rows in groups.items()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8088")
    parser.add_argument("--corpus", type=Path, default=Path(__file__).resolve().parents[1] / "eval" / "commands.json")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=15)
    parser.add_argument("--label", choices=("diagnostic", "heldout", "regression"), default="regression", help="Only call the first untouched-corpus run heldout")
    parser.add_argument("--category", help="Run one category (full acceptance requires all categories)")
    args = parser.parse_args()
    raw = args.corpus.read_bytes()
    corpus = json.loads(raw)
    if args.category:
        corpus["cases"] = [c for c in corpus["cases"] if c["category"] == args.category]
    records = run(args.url, corpus, args.timeout)
    report = {"schemaVersion": 1, "platform": platform.platform(), "architecture": platform.machine(),
              "corpusSha256": hashlib.sha256(raw).hexdigest(), "corpusVersion": corpus["version"],
              "partialRun": bool(args.category), "evaluationStage": args.label, "notes": corpus["notes"],
              "summary": summarize(records), "results": records}
    supported = report["summary"].get("supported", {})
    rejected = report["summary"].get("unsupported", {})
    report["acceptance"] = {"supportedAccuracyAtLeast95Percent": supported.get("accuracy", 0) >= .95,
                            "allUnsupportedRejected": rejected.get("accuracy", 0) == 1,
                            "completeCorpus": not args.category}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({"summary": report["summary"], "acceptance": report["acceptance"]}, indent=2))
    return 0 if all(report["acceptance"].values()) else 1


if __name__ == "__main__":
    raise SystemExit(main())
