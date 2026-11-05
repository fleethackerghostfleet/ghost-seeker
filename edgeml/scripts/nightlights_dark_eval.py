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


def _burst_scene(rng, tier, scenario, rules):
    x = int(rng.integers(65, 171))
    y = int(rng.integers(108, 133))
    w = int(rng.integers(90, 126))
    h = int(rng.integers(65, 86))
    hull_box = [x, y, w, h]
    top = max(0, y - 84)
    search_box = (x, top, w, y + h - top)
    steady_rules = [rule for rule in rules
                    if rule["id"] in {"nuc", "head_on", "two_mastheads"}]
    if scenario in {"flashing yellow 4-frame (air_cushion)", "single-frame yellow", "day"}:
        rule = next(rule for rule in rules if rule["id"] == "air_cushion")
    else:
        rule = steady_rules[int(rng.integers(len(steady_rules)))]
    pattern = rule["pattern"]
    colours = pattern.get("colours") or ["yellow"]
    vertical = pattern.get("layout") == "vertical"
    if scenario == "flashing yellow 4-frame (air_cushion)":
        flashing, n_frames, day, extra = True, 4, False, None
    elif scenario == "single-frame yellow":
        flashing, n_frames, day, extra = False, 1, False, None
    elif scenario == "complete steady pattern":
        flashing, n_frames, day, extra = False, 1, False, None
    elif scenario == "partial/low-confidence pattern":
        flashing, n_frames, day = False, 1, False
        extra = "white" if "white" not in colours else "red"
    else:
        flashing, n_frames, day, extra = False, 1, True, None
    colors = list(colours)
    if extra is not None:
        colors.append(extra)
    frames = []
    for frame in range(n_frames):
        if day or tier == "easy":
            lights = _lights_for_rule(rule, search_box, flashing=flashing)
            if extra is not None:
                lights.append(dict(colour=extra, x=x+w*.82, y=top+(y+h-top)*.48, flashing=False))
            image = render(rng, lights, hull_box, day=day, frame=frame,
                           radius=int(rng.integers(2, 4)), blur=float(rng.uniform(.35, .9)))
        else:
            contact = hard_contact(rng, hull_box, colors, vertical, flashing=flashing)
            image = hard_scene(rng, [contact], frame=frame, harbor=False)[0]
        frames.append(image)
    return frames, search_box


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


def _burst_eval(seed, tier, n, rules):
    scenarios = {}
    sample_scenarios = [
        ("flashing yellow 4-frame (air_cushion)", True),
        ("single-frame yellow", True),
        ("complete steady pattern", False),
        ("partial/low-confidence pattern", True),
        ("day", False),
    ]
    for scenario_index, (name, expected) in enumerate(sample_scenarios):
        requested = 0
        for sample in range(n):
            sample_seed = seed + (scenario_index + 1) * 100_003 + sample * 97
            rng = np.random.default_rng(sample_seed)
            frames, box = _burst_scene(rng, tier, name, rules)
            controller = BurstController.from_config()
            result = analyze(frames, box, burst=controller, t=1.0)
            requested += bool(result["burst"]["requested"])
        scenarios[name] = {"requested": int(requested), "samples": n, "expected": expected,
                           "request_rate": requested / n}
    sequence = BurstController.from_config()
    accepted = 0
    for second in range(120):
        accepted += sequence.request(float(second), "continuous trigger")
    if accepted != sequence.granted + sequence.extensions or accepted + sequence.denied != 120:
        raise AssertionError("continuous burst counters do not account for all request results")
    return {"scenarios": scenarios,
            "continuous_triggers_120s_at_1fps": {
                "accepted": accepted, "new_intervals": sequence.granted, "extensions": sequence.extensions,
                "denied": sequence.denied,
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
            "burst_n_per_scenario": n, "burst": _burst_eval(seed + 19, tier, n, rules)}


def summarize_observations(path):
    data = json.loads(Path(path).read_text())
    lit = [row for row in data['contacts'] if row['truth']['night']
           and row['truth']['rule_id'] is not None
           and any(not light.get('occluded') for light in row['truth']['lights'])]
    flagged = [row for row in lit if row['result']['dark_contact']['flagged']]
    return dict(observations=str(path),
                protocol='Saved observations only; false-dark counts on observable lit night contacts. No unlit-vessel ground truth; dark-contact recall is not measurable.',
                lit_night_contacts=len(lit), false_dark_contacts=len(flagged),
                false_dark_seeds=[row['truth'].get('seed') for row in flagged],
                night_gate_misses=sum(not row['result']['night'] for row in lit))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--split", choices=("dev", "test2"))
    source.add_argument("--observations", type=Path)
    parser.add_argument("--tier", choices=("easy", "hard"))
    parser.add_argument("--output", type=Path)
    parser.add_argument("--n", type=int, default=28)
    args = parser.parse_args()
    if args.observations:
        if args.output is None:
            parser.error('--observations requires --output')
        summary = summarize_observations(args.observations)
        output = args.output
    else:
        if args.tier is None:
            parser.error('--split requires --tier')
        summary = evaluate(args.split, args.tier, args.n)
        output = args.output or Path(__file__).resolve().parents[1] / "reports/nightlights" / f"dark_burst_{args.tier}_{args.split}.json"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
