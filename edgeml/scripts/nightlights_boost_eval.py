"""Paired boost-gate evaluation over frozen v2 detector observations (no image reads)."""
import argparse
import json
from pathlib import Path
import shlex
import sys

from edgeml.nightlights import interpret, load_rules


ROOT = Path(__file__).resolve().parents[2]


def summarize(rows, rules):
    boosted = [r for r in rows if r["boost"] > 0]
    false = sum(r["rule_id"] != r["truth"] or rules[r["truth"]]["priority_boost"] == 0 for r in boosted)
    high = [r for r in rows if rules[r["truth"]]["severity"] == "urgent"]
    missed = sum(r["boost"] == 0 for r in high)
    missed_correct = sum(r["boost"] == 0 or r["rule_id"] != r["truth"] for r in high)
    return dict(contacts=len(rows), boosted=len(boosted), false_boosts=false,
                false_boost_rate=false/len(boosted) if boosted else None,
                false_boost_per_contact=false/len(rows) if rows else None,
                high_severity=len(high), missed_boosts=missed,
                missed_boost_rate=missed/len(high) if high else None,
                missed_correct_boosts=missed_correct,
                missed_correct_boost_rate=missed_correct/len(high) if high else None)


def evaluate(split):
    path = ROOT/f"edgeml/reports/nightlights/hard_{split}_v2.json"
    data = json.loads(path.read_text())
    rules = {r["id"]: r for r in load_rules()}
    before, after = [], []
    for contact in data["contacts"]:
        truth, result = contact["truth"], contact["result"]
        current = interpret(result["lights"], result["night"], signal=truth.get("signal"))
        previous = result["interpretation"]
        if previous["rule_id"] != current["rule_id"]:
            raise ValueError("boost gate changed interpretation rule ID")
        if truth["rule_id"] is None:
            continue
        common = dict(truth=truth["rule_id"], rule_id=current["rule_id"], assisted=bool(truth.get("signal")), seed=truth["seed"])
        before.append(dict(common, boost=previous["boost"]))
        after.append(dict(common, boost=current["boost"], low_confidence=current.get("low_confidence", False)))
    metrics = {}
    for name, rows in [("without_gate", before), ("with_gate", after)]:
        metrics[name] = dict(all=summarize(rows, rules), automatic=summarize([r for r in rows if not r["assisted"]], rules))
    output = dict(dataset=f"hard/{split}", observations=str(path.relative_to(ROOT)),
                  command=shlex.join([sys.executable]+sys.argv),
                  protocol="Exact colour-multiset coverage; zero white allowance. Signal-assisted distress exempt. Frozen detector observations; no detector rerun or threshold change.",
                  metrics=metrics, without_gate=before, with_gate=after)
    target = ROOT/f"edgeml/reports/nightlights/boost_gate_{split}.json"
    target.write_text(json.dumps(output, indent=2)+"\n")
    print(json.dumps(dict(dataset=output["dataset"], metrics=metrics), indent=2))


def refresh_demos():
    path = ROOT/"internal/catalog/nightlights.json"
    data = json.loads(path.read_text())
    for image in data["nightLibrary"]:
        for det in image["dets"]:
            det["interpretation"] = interpret(det["lights"], image["night"],
                                               signal=det.get("signal") if det.get("signal_assisted") else None)
    path.write_text(json.dumps(data, indent=2)+"\n")
    print("Refreshed demo interpretations only; detector observations and JPEGs unchanged")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--split", choices=["dev", "test2"])
    group.add_argument("--refresh-demos", action="store_true")
    args = parser.parse_args()
    print("COMMAND:", shlex.join([sys.executable]+sys.argv))
    if args.refresh_demos:
        refresh_demos()
    else:
        evaluate(args.split)
