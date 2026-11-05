"""Render a fixed app demo, retaining detector failures rather than relabeling them."""
import json
from pathlib import Path
import shlex
import sys

import cv2
import numpy as np

from edgeml.nightlights import analyze, load_rules
from nightlights_synth import hard_contact, hard_scene


ROOT = Path(__file__).resolve().parents[2]
SCENES = [["head_on", "port", "starboard"], ["ram", "nuc", "constrained_draught"],
          ["trawling", "fishing", "pilot"], ["two_mastheads", "tow_long"]]


def generate_demo():
    out = ROOT/"web/static/imagery/night"
    out.mkdir(parents=True, exist_ok=True)
    rules = {r["id"]: r for r in load_rules()}
    entries, pending = [], []
    for scene in range(8):
        ids = SCENES[scene] if scene < 4 else ["distress_sos"]
        rng = np.random.default_rng(73000+min(scene, 4))
        contacts = []
        for i, rule_id in enumerate(ids):
            box = [65+i*295 if len(ids) > 1 else 360, 275+i*18, 215, 135]
            pattern = rules[rule_id]["pattern"]
            c = hard_contact(rng, box, pattern.get("colours", ["red"]),
                             pattern.get("layout") == "vertical" or rule_id == "two_mastheads",
                             flashing=scene >= 4, severity=.45)
            # Demo contacts use visible 3 px points; the benchmark retains random occlusions/tiny points.
            c["radius"], c["motion"], c["saturate"] = 3, 1, False
            for light in c["lights"]:
                light["occluded"] = False
            contacts.append(c)
        harbor = scene in (0, 2)
        image, distractors = hard_scene(np.random.default_rng(83000+min(scene, 4)), contacts,
                                        size=(960, 600), frame=max(0, scene-4), harbor=harbor)
        name = f"night-{scene+1:02}.jpg"
        if not (out/name).exists():
            cv2.imwrite(str(out/name), image, [cv2.IMWRITE_JPEG_QUALITY, 82])
        if (out/name).stat().st_size >= 150000:
            raise ValueError(f"demo exceeds 150 KB: {name}")
        pending.append((name, contacts, ids, harbor, distractors))
    sequence = [cv2.imread(str(out/item[0])) for item in pending[4:]]
    for scene, (name, contacts, ids, harbor, distractors) in enumerate(pending):
        image = cv2.imread(str(out/name))
        dets = []
        decision = None
        for contact, rule_id in zip(contacts, ids):
            signal = "sos" if scene >= 4 else None
            result = analyze(image, contact["box"], signal=signal)
            decision = result
            x, y, w, h = [v/2 for v in contact["box"]]
            det = dict(cls="fishing vessel" if rule_id in {"trawling", "fishing"} else "small boat" if rule_id in {"port", "starboard", "pilot", "distress_sos"} else "cargo ship",
                       b=dict(x=x, y=y, w=w, h=h), lights=result["lights"], interpretation=result["interpretation"],
                       truth_rule_id=rule_id, truth_lights=contact["lights"], signal_assisted=signal is not None)
            if signal:
                det["track_observation"] = analyze(sequence, contact["box"], signal=signal)
                det["sequence_files"] = [f"imagery/night/{item[0]}" for item in pending[4:]]
                det["signal"] = signal
                det["signal_note"] = "Red on/off distress proxy with externally reported SOS; not automatic flare or Morse recognition"
            dets.append(det)
        entries.append(dict(file=f"imagery/night/{name}", src="EO-P7" if harbor else "BY-12", scene="harbor" if harbor else "offshore",
                            night=decision["night"], lum=decision["lum"], basis=decision["basis"], dets=dets,
                            synthetic=True, seed=73000+min(scene, 4), nuisance_lights=distractors))
    path = ROOT/"internal/catalog/nightlights.json"
    catalog = json.loads(path.read_text()) if path.exists() else {}
    catalog["nightLibrary"] = entries
    path.write_text(json.dumps(catalog, indent=2)+"\n")
    lines = ["# App demo frames", "", f"Command: `{shlex.join([sys.executable]+sys.argv)}`", "",
             "All detections are obtained by reopening the saved JPEGs, not copied from truth. Boxes are 480x300; light coordinates are 960x600 pixels.",
             "Distress frames 05–08 use an externally supplied SOS signal, not automatic Morse recognition; track observations measure flashing across the saved frames.",
             "", "| Frame | Truth → detected | Bytes |", "|---|---|---:|"]
    for entry in entries:
        comparison = "; ".join(f"{d['truth_rule_id']} → {d['interpretation']['rule_id'] or 'none'} (boost {d['interpretation']['boost']:g})" for d in entry["dets"])
        size = (ROOT/"web/static"/entry["file"]).stat().st_size
        lines.append(f"| {entry['file']} | {comparison} | {size} |")
        print(entry["file"], comparison, size)
    (ROOT/"edgeml/reports/nightlights/demo.md").write_text("\n".join(lines)+"\n")


if __name__ == "__main__":
    print("COMMAND:", shlex.join([sys.executable]+sys.argv))
    generate_demo()
