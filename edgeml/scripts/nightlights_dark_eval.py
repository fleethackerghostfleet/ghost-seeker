"""Deterministic synthetic evaluation for night-time dark contacts and burst requests."""
import argparse
import json
import sys
from pathlib import Path

import numpy as np

from edgeml.nightlights import BurstController, analyze, dark_contact, load_rules
sys.path.insert(0, str(Path(__file__).resolve().parent))
from nightlights_synth import hard_contact, hard_scene, render


BOX = [130, 112, 100, 70]
BURST_BOX = (130, 28, 100, 95)


def _lights_for_rule(rule, box, flashing=None):
    pattern = rule["pattern"]
    colours = pattern.get("colours") or ["white"]
    vertical = pattern.get("layout") == "vertical"
    x, y, w, h = box
    is_flashing = bool(rule.get("flashing")) if flashing is None else flashing
    return [dict(colour=colour,
                 x=float(x + w / 2 if vertical else x + w / 2 + (i - (len(colours) - 1) / 2) * w * 0.42),
                 y=float(y + h * 0.2 + i * h * 0.12 if vertical else y + h * 0.3),
                 flashing=is_flashing)
            for i, colour in enumerate(colours)]


def _dark_image(rng, tier, box, rule=None, day=False):
    if day:
        lights = _lights_for_rule(rule, box, flashing=False) if rule else []
        return render(rng, lights, box, day=True), box
    if tier == "easy":
        lights = _lights_for_rule(rule, box, flashing=False) if rule else []
        return render(rng, lights, box), box
    if rule:
        colours = rule["pattern"].get("colours") or ["white"]
        contact = hard_contact(rng, box, colours, rule["pattern"].get("layout") == "vertical",
                               flashing=bool(rule.get("flashing")))
    else:
        contact = hard_contact(rng, box, [], False)
    return hard_scene(rng, [contact], harbor=False)[0], box


def _dark_contact_eval(rng, tier, n, rules):
    scenarios = {
        "dark hull + vessel box": (True, "cargo vessel", None),
        "lit vessel + box": (False, "cargo vessel", "rule"),
        "day + box": (False, "cargo vessel", None),
        "dark hull + non-vessel label (buoy)": (False, "buoy", None),
    }
    counts = {}
    tp = fp = fn = 0
    for name, (positive, label, kind) in scenarios.items():
        flagged = 0
        for _ in range(n):
            rule = rules[int(rng.integers(len(rules)))] if kind == "rule" else None
            image, box = _dark_image(rng, tier, BOX, rule, day=name == "day + box")
            result = dark_contact(image, box, label=label, conf=0.8)
            flagged += result["flagged"]
            if positive:
                tp += result["flagged"]
                fn += not result["flagged"]
            else:
                fp += result["flagged"]
        counts[name] = {"flagged": int(flagged), "samples": n, "positive": positive}
    return {"scenarios": counts, "total": 4 * n, "tp": int(tp), "fp": int(fp), "fn": int(fn),
            "precision": tp / max(tp + fp, 1), "recall": tp / max(tp + fn, 1)}


def _burst_eval(seed):
    rng = np.random.default_rng(seed)
    scenarios = {}
    sample_scenes = {
        "flashing yellow 4-frame (air_cushion)": (
            [render(rng, [dict(colour="yellow", x=175, y=55, flashing=True)], BOX, frame=i) for i in range(4)],
            True,
        ),
        "single-frame yellow": (
            [render(rng, [dict(colour="yellow", x=175, y=55, flashing=False)], BOX)], True,
        ),
        "complete steady pattern": (
            [render(rng, [dict(colour="red", x=175, y=45, flashing=False),
                          dict(colour="red", x=175, y=62, flashing=False)], BOX)], False,
        ),
        "partial/low-confidence pattern": (
            [render(rng, [dict(colour="red", x=175, y=40, flashing=False),
                          dict(colour="red", x=175, y=56, flashing=False),
                          dict(colour="white", x=220, y=55, flashing=False)], BOX)], True,
        ),
        "day": (
            [render(rng, [dict(colour="yellow", x=175, y=55, flashing=False)], BOX, day=True)], False,
        ),
    }
    for name, (frames, expected) in sample_scenes.items():
        controller = BurstController.from_config()
        result = analyze(frames, BURST_BOX, burst=controller, t=1.0)
        requested = bool(result["burst"]["requested"])
        scenarios[name] = {"requested": requested, "expected": expected, "requests": int(requested)}
    sequence = BurstController.from_config()
    request_count = 0
    for second in range(120):
        request_count += sequence.request(float(second), "continuous trigger")
    return {"scenarios": scenarios,
            "continuous_triggers_120s_at_1fps": {
                "requests": request_count, "granted": sequence.granted, "denied": sequence.denied,
                "duty": sequence.duty(119.0), "duration_s": 120, "source_fps": 1.0,
            }}


def evaluate(split="dev", tier="easy", n=28):
    if split not in {"dev", "test2"}:
        raise ValueError(f"unsupported split: {split}")
    if tier not in {"easy", "hard"}:
        raise ValueError(f"unsupported tier: {tier}")
    seed = ({"easy": {"dev": 94109, "test2": 95477},
             "hard": {"dev": 97231, "test2": 98807}}[tier][split])
    rng = np.random.default_rng(seed)
    rules = load_rules()
    return {"split": split, "tier": tier, "seed": seed, "n_per_scenario": n,
            "dark_contact": _dark_contact_eval(rng, tier, n, rules),
            "burst": _burst_eval(seed + 19)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--split", choices=("dev", "test2"), required=True)
    parser.add_argument("--tier", choices=("easy", "hard"), required=True)
    parser.add_argument("--n", type=int, default=28)
    args = parser.parse_args()
    summary = evaluate(args.split, args.tier, args.n)
    output = Path(__file__).resolve().parents[1] / "reports/nightlights" / f"dark_burst_{args.tier}_{args.split}.json"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
