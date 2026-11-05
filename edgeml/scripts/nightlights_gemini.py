"""Cached, CPU-only contact-crop comparison; no truth or external signals in prompts."""
import argparse
import base64
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import math
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request

import cv2
import numpy as np

from edgeml.nightlights import analyze, load_rules
from nightlights_synth import observability


ENDPOINT = "https://aiplatform.googleapis.com/v1/projects/qwiklabs-gcp-02-34a870014566/locations/global/publishers/google/models/gemini-2.5-flash:generateContent"
COLORS = {"red", "green", "white", "yellow", "blue"}


def parse_prediction(response, rule_ids):
    parts = response["candidates"][0]["content"]["parts"]
    value = json.loads("".join(p.get("text", "") for p in parts if not p.get("thought")))
    if not isinstance(value, dict) or set(value) != {"lights", "rule_id"}:
        raise ValueError("expected lights and rule_id")
    if value["rule_id"] is not None and value["rule_id"] not in rule_ids:
        raise ValueError("unknown rule_id")
    if not isinstance(value["lights"], list) or len(value["lights"]) > 32:
        raise ValueError("invalid lights array")
    for p in value["lights"]:
        if not isinstance(p, dict) or set(p) != {"color", "rel_y"} or p["color"] not in COLORS:
            raise ValueError("invalid light")
        if type(p["rel_y"]) not in (int, float) or not math.isfinite(p["rel_y"]) or not 0 <= p["rel_y"] <= 1:
            raise ValueError("invalid rel_y")
    return value


def request(payload, token):
    req = urllib.request.Request(ENDPOINT, data=json.dumps(payload).encode(),
                                 headers={"Authorization": "Bearer "+token, "Content-Type": "application/json"})
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=90) as response:
            body = json.loads(response.read())
        return dict(response=body, latency_seconds=time.perf_counter()-start, error=None)
    except urllib.error.HTTPError as exc:
        return dict(response=None, latency_seconds=time.perf_counter()-start, error=f"HTTP {exc.code}")
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
        return dict(response=None, latency_seconds=time.perf_counter()-start, error=type(exc).__name__)


def crop_track(directory, record, crop_dir, stem):
    images, encoded = [], []
    x, y, w, h = record["box"]
    first = cv2.imread(str(directory/record["frames"][0]))
    ih, iw = first.shape[:2]
    x0, y0 = max(0, int(x-w*.2)), max(0, int(y-h*.2))
    x1, y1 = min(iw, math.ceil(x+w*1.2)), min(ih, math.ceil(y+h*1.2))
    for i, name in enumerate(record["frames"]):
        image = cv2.imread(str(directory/name))[y0:y1, x0:x1]
        ok, jpeg = cv2.imencode(".jpg", image, [cv2.IMWRITE_JPEG_QUALITY, 95])
        if not ok:
            raise ValueError("crop encoding failed")
        (crop_dir/f"{stem}-{i}.jpg").write_bytes(jpeg.tobytes())
        encoded.append(base64.b64encode(jpeg).decode())
        images.append(cv2.imdecode(jpeg, cv2.IMREAD_COLOR))
    return images, encoded, [x-x0, y-y0, w, h]


def score(examples, method):
    correct = predicted = actual = rules = 0
    strata = {k: dict(correct=0, total=0) for k in ["resolvable", "unresolvable"]}
    per_rule = {}
    per_color = {c: dict(correct=0, predicted=0, actual=0) for c in sorted(COLORS)}
    for e in examples:
        truth, prediction = e["truth"], e[method]
        gt = Counter(p["colour"] for p in truth["lights"] if not p.get("occluded"))
        counts = Counter(p["color"] for p in prediction["lights"]) if prediction else Counter()
        correct += sum((gt & counts).values())
        actual += sum(gt.values())
        predicted += sum(counts.values())
        ok = prediction is not None and prediction["rule_id"] == truth["rule_id"]
        rules += ok
        r = per_rule.setdefault(truth["rule_id"], dict(correct=0, total=0))
        r["correct"] += ok
        r["total"] += 1
        s = strata["unresolvable" if truth["unresolvable"] else "resolvable"]
        s["correct"] += ok
        s["total"] += 1
        for c in COLORS:
            per_color[c]["correct"] += min(gt[c], counts[c])
            per_color[c]["actual"] += gt[c]
            per_color[c]["predicted"] += counts[c]
    return dict(contacts=len(examples), light_precision=correct/max(1, predicted), light_recall=correct/max(1, actual),
                light_counts=dict(correct=correct, predicted=predicted, actual=actual),
                rule_accuracy=rules/max(1, len(examples)), rule_correct=rules, per_rule=per_rule,
                per_color={c: dict(v, precision=v["correct"]/max(1, v["predicted"]), recall=v["correct"]/max(1, v["actual"])) for c, v in per_color.items()},
                observability={k: dict(v, accuracy=v["correct"]/max(1, v["total"])) for k, v in strata.items()})


def compare(data, cache, report, workers=4, max_calls=400):
    if not 1 <= workers <= 8 or not 0 <= max_calls <= 400:
        raise ValueError("workers must be 1..8; max_calls must be 0..400")
    cache, report = Path(cache), Path(report)
    crop_dir = cache/"crops"
    crop_dir.mkdir(parents=True, exist_ok=True)
    report.mkdir(parents=True, exist_ok=True)
    rules = load_rules()
    rule_ids = {r["id"] for r in rules}
    prompt = ("Analyze the SAME vessel contact over these four consecutive frames. Identify navigation lights, "
              "ignoring shore/pier lights and reflections. Return each persistent physical light ONCE, "
              "including flashing lights visible in any frame. Infer only what is visible; do not invent obscured lights. "
              "Output strictly {\"lights\":[{\"color\":\"red|green|white|yellow|blue\",\"rel_y\":0.0}],\"rule_id\":null}. "
              "rel_y is normalized image y (0=top,1=bottom), list top to bottom. rule_id must be null or one of the "
              "following table IDs. Use null if ambiguous. No externally identified distress signal is provided. "
              "Rule table: "+json.dumps(rules))
    jobs = []
    for tier, split in [("hard", "dev"), ("hard", "test2"), ("easy", "test")]:
        directory = Path(data)/tier/split
        for record in json.loads((directory/"labels.json").read_text()):
            if record["rule_id"] is None:
                continue
            record.update(observability(record))
            frames, encoded, box = crop_track(directory, record, crop_dir, f"{tier}_{split}_{record['seed']}")
            ours = analyze(frames, box)
            prediction = dict(lights=[dict(color=p["colour"], rel_y=p["y"]/frames[0].shape[0]) for p in ours["lights"]], rule_id=ours["interpretation"]["rule_id"])
            parts = [{"text": prompt}]+[{"inlineData": {"mimeType": "image/jpeg", "data": b}} for b in encoded]
            payload = dict(contents=[dict(role="user", parts=parts)], generationConfig=dict(temperature=0, maxOutputTokens=1024, responseMimeType="application/json", thinkingConfig=dict(thinkingBudget=0)))
            key = hashlib.sha256((ENDPOINT+json.dumps(payload, sort_keys=True)).encode()).hexdigest()
            jobs.append(dict(tier=tier, split=split, truth=record, ours=prediction, payload=payload, key=key))
    missing = list({j["key"]: j for j in jobs if not (cache/f"{j['key']}.json").exists()}.values())
    if len(missing) > max_calls:
        raise ValueError(f"need {len(missing)} calls, cap is {max_calls}; no calls made")
    token = subprocess.check_output([str(Path.home()/"google-cloud-sdk/bin/gcloud"), "auth", "print-access-token"], text=True).strip() if missing else None
    def run(job):
        path = cache/f"{job['key']}.json"
        if path.exists():
            return
        result = request(job["payload"], token)
        path.write_text(json.dumps(result, indent=2)+"\n")
    if missing:
        run(missing[0])
        first = json.loads((cache/f"{missing[0]['key']}.json").read_text())
        if first["error"]:
            raise RuntimeError("Gemini preflight failed: "+first["error"]+"; cached, no further calls made")
        with ThreadPoolExecutor(max_workers=workers) as pool:
            list(pool.map(run, missing[1:]))
    examples = []
    for job in jobs:
        result = json.loads((cache/f"{job['key']}.json").read_text())
        prediction, invalid = None, False
        if not result["error"]:
            try:
                prediction = parse_prediction(result["response"], rule_ids)
            except (KeyError, IndexError, TypeError, ValueError):
                invalid = True
        examples.append({k: v for k, v in job.items() if k not in {"payload"}} | dict(gemini=prediction, latency_seconds=result["latency_seconds"], error=result["error"], invalid_json=invalid))
    summary = {}
    for tier, split in [("hard", "dev"), ("hard", "test2"), ("easy", "test")]:
        subset = [e for e in examples if (e["tier"], e["split"]) == (tier, split)]
        latencies = [e["latency_seconds"] for e in subset]
        summary[f"{tier}_{split}"] = dict(ours=score(subset, "ours"), gemini=score(subset, "gemini"),
            failures=sum(e["error"] is not None for e in subset), invalid_json=sum(e["invalid_json"] for e in subset),
            latency_seconds=dict(mean=float(np.mean(latencies)), median=float(np.median(latencies)), p95=float(np.percentile(latencies, 95))))
    output = dict(summary=summary, total_contacts=len(jobs), new_calls=len(missing), cached_calls=len(jobs)-len(missing),
                  endpoint=ENDPOINT, config=dict(workers=workers, max_calls=max_calls, temperature=0, thinking_budget=0, max_output_tokens=1024),
                  protocol="Both methods see identical four-frame padded JPEG crops. No external signals. Colour P/R are per-contact colour-multiset overlap, NOT spatial matching; not comparable to full-frame evaluator P/R. Rule accuracy uses all latent labels including unresolvable cases. API/JSON failures count as empty light predictions and incorrect rules. No retries; errors cached too.", examples=examples)
    full = Path(data)/"reports"
    full.mkdir(parents=True, exist_ok=True)
    (full/"gemini_comparison.json").write_text(json.dumps(output, indent=2)+"\n")
    (report/"gemini_comparison.json").write_text(json.dumps({k: v for k, v in output.items() if k != "examples"}, indent=2)+"\n")
    print(json.dumps({k: v for k, v in output.items() if k != "examples"}, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--data", default="data/nightlights")
    p.add_argument("--cache", default="data/nightlights/gemini")
    p.add_argument("--report", default="edgeml/reports/nightlights")
    p.add_argument("--workers", type=int, default=4)
    p.add_argument("--max-calls", type=int, default=400)
    args = p.parse_args()
    compare(args.data, args.cache, args.report, args.workers, args.max_calls)
