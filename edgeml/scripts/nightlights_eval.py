"""Evaluate frozen night-light candidate or export the existing imagery catalog."""
import argparse
from collections import Counter
import json
import math
from pathlib import Path
import shlex
import sys

import cv2

from edgeml.nightlights import analyze, gate, load_rules
from nightlights_synth import observability


ROOT = Path(__file__).resolve().parents[2]
COLOURS = ["red", "green", "white", "yellow", "blue", "ambiguous_orange"]


def evaluate(data, split, report, tier="easy", version=""):
    directory = Path(data)/tier/split
    labels = json.loads((directory/"labels.json").read_text())
    confusion = {c: {p: 0 for p in COLOURS+["miss"]} for c in COLOURS+["background"]}
    false_positives = Counter()
    fp_sources = Counter()
    rules = {r["id"]: [0, 0] for r in load_rules()}
    matched = predicted = actual = flash_ok = aspect_ok = gate_ok = day_false = 0
    examples = []
    strata = {s: dict(correct=0, total=0) for s in ["resolvable", "unresolvable"]}
    for record in labels:
        record.update(observability(record))
        frames = [cv2.imread(str(directory/f)) for f in record["frames"]]
        result = analyze(frames, record["box"], signal=record.get("signal"),
                         timestamp=record.get("timestamp"), lat=record.get("lat"), lon=record.get("lon"))
        lights = result["lights"]
        predicted += len(lights)
        observable = [p for p in record["lights"] if not p.get("occluded")]
        actual += len(observable)
        gate_ok += result["night"] == record["night"]
        day_false += not record["night"] and result["night"]
        used = set()
        for truth in observable:
            candidates = [(math.hypot(p["x"]-truth["x"], p["y"]-truth["y"]), i) for i, p in enumerate(lights) if i not in used]
            distance, idx = min(candidates, default=(float("inf"), -1))
            if distance <= 6:
                used.add(idx)
                p = lights[idx]
                confusion[truth["colour"]][p["colour"]] += 1
                matched += p["colour"] == truth["colour"]
                flash_ok += p["flashing"] == truth["flashing"]
            else:
                confusion[truth["colour"]]["miss"] += 1
        for i, p in enumerate(lights):
            if i not in used:
                false_positives[p["colour"]] += 1
                confusion["background"][p["colour"]] += 1
                nuisance = min(record.get("distractors", []), key=lambda d: math.hypot(p["x"]-d["x"], p["y"]-d["y"]), default=None)
                source = nuisance["source"] if nuisance and math.hypot(p["x"]-nuisance["x"], p["y"]-nuisance["y"]) <= 6 else "glint_reflection_noise"
                fp_sources[source] += 1
        if record["rule_id"] is not None:
            correct = result["interpretation"]["rule_id"] == record["rule_id"]
            rules[record["rule_id"]][0] += correct
            rules[record["rule_id"]][1] += 1
            stratum = strata["unresolvable" if record["unresolvable"] else "resolvable"]
            stratum["correct"] += correct
            stratum["total"] += 1
            aspect_ok += result["interpretation"]["aspect"] == record["aspect"]
        examples.append(dict(truth=record, expected=record["rule_id"], result=result))
    contacts = sum(v[1] for v in rules.values())
    metrics = dict(split=split, tier=tier, night_contacts=contacts, gate_images=len(labels),
                   day_images=sum(not r["night"] and not r.get("gate_only", False) for r in labels), twilight_images=sum(r.get("gate_only", False) for r in labels),
                   light_precision=matched/predicted if predicted else 0, light_recall=matched/actual if actual else 0,
                   light_counts=dict(correct_colour=matched, predicted=predicted, actual=actual),
                   confusion_matrix=confusion, unmatched_predictions=dict(false_positives), false_positive_sources=dict(fp_sources),
                   per_colour={c: dict(precision=confusion[c][c]/max(1, sum(confusion[t][c] for t in COLOURS)+false_positives[c]),
                                       recall=confusion[c][c]/max(1, sum(confusion[c].values()))) for c in COLOURS},
                   per_rule={r: dict(correct=v[0], total=v[1], accuracy=v[0]/v[1]) for r, v in rules.items()},
                   rule_accuracy=sum(v[0] for v in rules.values())/contacts,
                   automatic_rule_accuracy=sum(v[0] for r, v in rules.items() if not r.startswith("distress_"))/sum(v[1] for r, v in rules.items() if not r.startswith("distress_")),
                   externally_identified_distress_accuracy=sum(v[0] for r, v in rules.items() if r.startswith("distress_"))/sum(v[1] for r, v in rules.items() if r.startswith("distress_")),
                   aspect_accuracy=aspect_ok/contacts, flashing_accuracy=flash_ok/actual,
                   night_gate_accuracy=gate_ok/len(labels), day_false_night_rate=day_false/sum(not r["night"] for r in labels),
                   notes=["Procedural labels, not real-world accuracy; four-frame static tracks. Hard tier includes optics, compression, occlusion and background nuisance lights.",
                          "Distress interpretation receives externally identified red_flare/SOS signals; no automatic flare or Morse recognition.",
                          "Permanently occluded GT lights excluded from light recall; latent full rule and aspect still scored. Missed observable GT lights count as flashing errors; colour P/R require position match within 6 pixels.",
                          "Aspect assumes isolated red/green points are sidelights; offset whites assume higher aft masthead, not towing.",
                          "Easy dev/test seed bases 1000/100000; hard dev/test bases 300000/900000, plus separate day/twilight seeds. No hard-test threshold tuning."])
    metrics["observability"] = {k: dict(v, accuracy=v["correct"]/max(1, v["total"])) for k, v in strata.items()}
    metrics["notes"].append("Unresolvable cases remain in all totals and are separately scored: permanent occlusion or a saturated one-pixel coloured emitter. Radius 0 renders one pixel, not a subpixel light. Hard-test2 seed base 2700000; hard-test v1 was not retuned or reevaluated.")
    report = Path(report)
    report.mkdir(parents=True, exist_ok=True)
    stem = f"{tier}_{split}" + (f"_{version}" if version else "")
    full = Path(data)/"reports"
    full.mkdir(parents=True, exist_ok=True)
    (full/f"{stem}.json").write_text(json.dumps(dict(metrics=metrics, contacts=examples), indent=2)+"\n")
    (report/f"{stem}.json").write_text(json.dumps(dict(metrics=metrics), indent=2)+"\n")
    lines = [f"# Night lights: {tier}/{split}", "", f"Command: `{shlex.join([sys.executable]+sys.argv)}`", "", "Synthetic benchmark only; not operational validation.", ""]
    for key in ["light_precision", "light_recall", "rule_accuracy", "automatic_rule_accuracy", "externally_identified_distress_accuracy", "aspect_accuracy", "flashing_accuracy", "night_gate_accuracy", "day_false_night_rate"]:
        lines.append(f"- {key}: {metrics[key]:.4f}")
    lines += ["", "| Rule | Correct / total | Accuracy |", "|---|---:|---:|"]
    lines += [f"| {r} | {v['correct']} / {v['total']} | {v['accuracy']:.4f} |" for r, v in metrics["per_rule"].items()]
    lines += ["", "## Colour confusion (position-matched)", "", "| GT \\ predicted | " + " | ".join(COLOURS+["miss"]) + " |", "|---|"+"---:|"*(len(COLOURS)+1)]
    lines += [f"| {c} | " + " | ".join(str(confusion[c][p]) for p in COLOURS+["miss"]) + " |" for c in COLOURS+["background"]]
    lines += ["", "Unmatched predictions: " + json.dumps(dict(false_positives)), "", "False-positive sources: " + json.dumps(dict(fp_sources)), ""] + metrics["notes"]
    lines += ["", "Observability strata (all remain in overall scores): " + json.dumps(metrics["observability"])]
    (report/f"{stem}.md").write_text("\n".join(lines)+"\n")
    print(json.dumps(metrics, indent=2))


def library(report):
    results = {}
    for entry in json.loads((ROOT/"internal/catalog/imagery.json").read_text()):
        image = cv2.imread(str(ROOT/"web/static"/entry["file"]))
        if image is None:
            raise ValueError(f"cannot read {entry['file']}")
        h, w = image.shape[:2]
        src = entry["src"]
        modality = "IR" if src.startswith("IR") else "SAR" if src.startswith("SAR") else "EO"
        result = gate(image, modality=modality)
        result.update(size=[w, h], modality=modality, dets=[])
        for det in entry["dets"]:
            b = det["b"]
            box = [b["x"]*w/480, b["y"]*h/300, b["w"]*w/480, b["h"]*h/300]
            observation = analyze(image, box, modality=modality)
            result["dets"].append(dict(cls=det["cls"], box=box, lights=observation["lights"], interpretation=observation["interpretation"]))
        results[entry["file"]] = result
    target = ROOT/"internal/catalog/nightlights.json"
    previous = json.loads(target.read_text()) if target.exists() else {}
    if "nightLibrary" in previous:
        results["nightLibrary"] = previous["nightLibrary"]
    ordinary = {f: r for f, r in results.items() if f != "nightLibrary"}
    target.write_text(json.dumps(results, indent=2)+"\n")
    night = [f for f, r in ordinary.items() if r["night"]]
    light_frames = {f: sum(len(d["lights"]) for d in r["dets"]) for f, r in ordinary.items() if any(d["lights"] for d in r["dets"])}
    summary = dict(frames=len(ordinary), night=night, not_applicable=[f for f, r in ordinary.items() if r["night"] is None], light_frames=light_frames, sizes=sorted({tuple(r["size"]) for r in ordinary.values()}))
    Path(report).mkdir(parents=True, exist_ok=True)
    (Path(report)/"library.md").write_text(f"# Library observations\n\nCommand: `{shlex.join(sys.argv)}`\n\n```json\n{json.dumps(summary, indent=2)}\n```\n\nBoxes scaled independently from 480x300. These are unvalidated bright-point candidates, not verified navigation lights. IR/SAR colour rules are suppressed.\n")
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--data", default="data/nightlights")
    p.add_argument("--split", choices=["dev", "test", "test2", "test3"])
    p.add_argument("--version", default="")
    p.add_argument("--tier", choices=["easy", "hard"], default="easy")
    p.add_argument("--library", action="store_true")
    p.add_argument("--report", default="edgeml/reports/nightlights")
    a = p.parse_args()
    print("COMMAND:", shlex.join([sys.executable]+sys.argv))
    if a.library:
        library(a.report)
    elif a.split:
        evaluate(a.data, a.split, a.report, a.tier, a.version)
    else:
        p.error("provide --split or --library")
