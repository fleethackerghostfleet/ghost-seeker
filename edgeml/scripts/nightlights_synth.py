"""Deterministic procedural benchmark, not evidence of real-world performance."""
import argparse
import json
from pathlib import Path
import shlex
import sys

import cv2
import numpy as np

from edgeml.nightlights import load_rules


BGR = {"red": (15, 15, 245), "green": (15, 245, 15), "white": (240, 240, 240),
       "yellow": (15, 235, 245), "blue": (245, 35, 15)}


def render(rng, lights, box, day=False, frame=0, radius=2, blur=0.5):
    h, w = 240, 360
    gradient = np.linspace(9, 24, h)[:, None, None]
    image = np.broadcast_to(gradient, (h, w, 3)).copy()
    image += rng.normal(0, 2.5, image.shape)
    if day:
        image += 145
    x, y, bw, bh = map(int, box)
    cv2.fillPoly(image, [np.array([[x, y+bh-10], [x+bw, y+bh-10], [x+bw-14, y+bh], [x+12, y+bh]])], (8, 8, 8))
    for sx in (18, 29, 326, 339):
        cv2.circle(image, (sx, 100), 2, (130, 160, 180), -1)
    for light in lights:
        if light["flashing"] and frame in (1, 3):
            continue
        p = (round(light["x"]), round(light["y"]))
        colour = BGR[light["colour"]]
        halo = np.zeros_like(image)
        cv2.circle(halo, p, radius+3, tuple(c*0.22 for c in colour), -1)
        image += cv2.GaussianBlur(halo, (13, 13), 3)
        cv2.line(image, (p[0], y+bh+3), (p[0]+1, y+bh+int(rng.integers(10, 25))), tuple(c*0.3 for c in colour), 1)
        cv2.circle(image, p, radius, colour, -1)
    image = np.clip(image, 0, 255).astype(np.uint8)
    return cv2.GaussianBlur(image, (3, 3), blur)


def hard_scene(rng, contacts, size=(360, 240), frame=0, twilight=None, harbor=True):
    """Render imperfect optics and labeled nuisance lights; no detector feedback."""
    w, h = size
    horizon = int(h*0.43)
    yy, xx = np.mgrid[:h, :w]
    base = 12 if twilight is None else twilight
    image = np.zeros((h, w, 3), np.float32)
    image[:] = np.array([1.2, 0.8, 0.65])*base
    image += (yy/h*6)[:, :, None]
    image[horizon:] *= 0.7
    wave = rng.normal(0, 2, (h, w)).astype(np.float32)
    image[horizon:] += wave[horizon:, :, None]
    distractors = []
    if harbor:
        for i in range(14):
            px = int(w*(0.12+i*0.055))
            py = horizon+int(rng.integers(-6, 8))
            c = "white" if i % 3 else "yellow"
            distractors.append(dict(x=px, y=py, colour=c, source="pier"))
            cv2.line(image, (px, py), (px, py+int(h*0.08)), (18, 24, 35), 1)
        for contact in contacts:
            bx, by, bw, bh = contact["box"]
            for i in range(3):
                distractors.append(dict(x=float(bx+rng.uniform(-0.1, 1.1)*bw),
                                        y=float(by+rng.uniform(-0.1, 0.45)*bh),
                                        colour="yellow" if i == 0 else "white", source="shore"))
    light_layer = np.zeros_like(image)
    for d in distractors:
        c = (20, 105, 235) if d["colour"] == "yellow" else (225, 225, 235)
        cv2.circle(light_layer, (round(d["x"]), round(d["y"])), int(rng.integers(1, 3)), c, -1)
    for contact in contacts:
        x, y, bw, bh = contact["box"]
        hull = np.array([[x, y+bh*.8], [x+bw, y+bh*.8], [x+bw*.88, y+bh], [x+bw*.1, y+bh]], np.int32)
        cv2.fillPoly(image, [hull], (5, 6, 7))
        cv2.rectangle(image, (int(x+bw*.35), int(y+bh*.65)), (int(x+bw*.7), int(y+bh*.8)), (9, 10, 11), -1)
        for light in contact["lights"]:
            if light.get("occluded") or light["flashing"] and frame in (1, 3):
                continue
            px, py = round(light["x"]), round(light["y"])
            c = np.array(BGR[light["colour"]], dtype=float)
            shift = contact["shift"]
            if light["colour"] == "white":
                c *= [1-shift*.65, 1-shift*.15, 1]
            elif light["colour"] == "red":
                c[1] += shift*130
            elif light["colour"] == "green":
                c[0] += shift*180
            cv2.circle(light_layer, (px, py), contact["radius"], tuple(c), -1)
            if contact["saturate"]:
                cv2.circle(light_layer, (px, py), max(0, contact["radius"]-1), (255, 255, 255), -1)
            cv2.line(image, (px, int(y+bh)), (px+int(rng.integers(-2, 3)), min(h-1, int(y+bh+rng.uniform(.1, .4)*bh))), tuple(c*.5), max(1, contact["radius"]//2))
    bloom = max(c["bloom"] for c in contacts) if contacts else 1.5
    image += light_layer + cv2.GaussianBlur(light_layer, (0, 0), bloom)*2.4
    # Broad moonlit sea glints, unrelated to navigation lights.
    cx, cy = rng.uniform(.1, .9)*w, rng.uniform(.6, .95)*h
    glint = np.exp(-((xx-cx)/(w*.09))**2-((yy-cy)/(h*.05))**2)
    image += glint[:, :, None]*rng.uniform(40, 115)
    image += rng.normal(0, rng.uniform(3, 12), image.shape)
    image = np.clip(image, 0, 255).astype(np.uint8)
    sigma = max(c["blur"] for c in contacts) if contacts else .5
    image = cv2.GaussianBlur(image, (0, 0), sigma)
    motion = max(c["motion"] for c in contacts) if contacts else 1
    kernel = np.zeros((motion, motion), np.float32)
    kernel[motion//2, :] = 1/motion
    image = cv2.filter2D(image, -1, kernel)
    ok, encoded = cv2.imencode(".jpg", image, [cv2.IMWRITE_JPEG_QUALITY, int(rng.integers(35, 85))])
    if not ok:
        raise RuntimeError("JPEG encoding failed")
    return cv2.imdecode(encoded, cv2.IMREAD_COLOR), distractors


def hard_contact(rng, box, colours, vertical, flashing=False, severity=1):
    x, y, w, h = box
    spacing = rng.uniform(.07, .25)*h
    lights = [dict(colour=c, x=float(x+w/2 if vertical else x+w/2+(i-(len(colours)-1)/2)*w*.42),
                   y=float(y+h*.18+i*spacing if vertical else y+h*.3), flashing=flashing,
                   occluded=False) for i, c in enumerate(colours)]
    if lights and rng.random() < .28*severity:
        lights[int(rng.integers(len(lights)))]["occluded"] = True
    return dict(box=box, lights=lights, radius=int(rng.choice([0, 1, 1, 2, 3])), shift=float(rng.uniform(.1, .95)*severity),
                saturate=bool(rng.random() < .3*severity), bloom=float(rng.uniform(1, 4)*severity),
                blur=float(rng.uniform(.4, 1.5)*severity), motion=int(rng.choice([1, 3, 5])))


def observability(record):
    reasons = []
    contact = record.get("degradation") or {}
    if any(p.get("occluded") for p in record["lights"]):
        reasons.append("permanently_occluded_light")
    if contact.get("radius") == 0 and contact.get("saturate") and any(p["colour"] != "white" and not p.get("occluded") for p in record["lights"]):
        reasons.append("single_pixel_colour_erased_by_saturation")
    return dict(unresolvable=bool(reasons), reasons=reasons)


def generate(out, split, tier="easy"):
    out = Path(out) / tier / split
    out.mkdir(parents=True, exist_ok=True)
    records = []
    seed_base = ({"dev": 1000, "test": 100000, "test2": 1700000, "test3": 4200000} if tier == "easy" else {"dev": 300000, "test": 900000, "test2": 2700000, "test3": 4100000})[split]
    for ri, rule in enumerate(load_rules()):
        for repeat in range(7):
            seed = seed_base + ri*100 + repeat
            rng = np.random.default_rng(seed)
            bw, bh = int(rng.integers(105, 165)), int(rng.integers(65, 95))
            box = [180-bw//2, 82, bw, bh]
            pattern = rule["pattern"]
            colours = pattern.get("colours", ["red"] if rule["id"] == "distress_flare" else ["white"])
            vertical = pattern.get("layout") == "vertical" or rule["id"] == "two_mastheads" and repeat % 2 == 0
            lights = []
            for i, colour in enumerate(colours):
                px = 180 if vertical else 180+(i-(len(colours)-1)/2)*bw*0.55
                py = 96+i*bh*0.24 if vertical else 110 + (i*16 if rule["id"] == "two_mastheads" else 0)
                lights.append(dict(colour=colour, x=px, y=py, flashing=bool(rule["flashing"]) or "signal" in pattern))
            expected_aspect = {"head_on": "head_on", "port": "port_side_crossing_right_to_left", "starboard": "starboard_side_crossing_left_to_right"}.get(rule["id"], "unknown")
            if rule["id"] == "two_mastheads" and not vertical:
                expected_aspect = "starboard_side_crossing_left_to_right"
            if "signal" in pattern:
                expected_aspect = "unknown"
            contact = None
            if tier == "hard":
                bw, bh = int(rng.integers(45, 150)), int(rng.integers(40, 90))
                box = [180-bw//2, int(rng.integers(86, 125)), bw, bh]
                contact = hard_contact(rng, box, colours, vertical, bool(rule["flashing"]) or "signal" in pattern)
                lights = contact["lights"]
                if rule["id"] == "two_mastheads" and not vertical:
                    lights[-1]["y"] += 16
            files = []
            radius, blur = int(rng.integers(2, 4)), float(rng.uniform(0.35, 0.9))
            for frame in range(4):
                name = f"{seed}-{frame}.png"
                if contact is None:
                    image, distractors = render(rng, lights, box, frame=frame, radius=radius, blur=blur), []
                else:
                    # Fix the nuisance layout across the short track.
                    image, distractors = hard_scene(np.random.default_rng(seed+50000), [contact], frame=frame)
                cv2.imwrite(str(out/name), image)
                files.append(name)
            records.append(dict(seed=seed, frames=files, box=box, lights=lights, rule_id=rule["id"],
                                aspect=expected_aspect, night=True, signal=pattern.get("signal"),
                                distractors=distractors, degradation=contact))
    for i in range(8):
        rng = np.random.default_rng(seed_base+9000+i)
        name = f"day-{i}.png"
        cv2.imwrite(str(out/name), render(rng, [], [110, 82, 140, 80], day=True))
        records.append(dict(frames=[name], box=[110, 82, 140, 80], lights=[], rule_id=None, aspect="unknown", night=False))
    if tier == "hard":
        for i in range(16):
            rng = np.random.default_rng(seed_base+9500+i)
            name = f"twilight-{i}.png"
            image, distractors = hard_scene(rng, [], twilight=float(rng.uniform(35, 85)))
            cv2.imwrite(str(out/name), image)
            night = i % 2 == 0
            records.append(dict(frames=[name], box=[70, 90, 220, 100], lights=[], rule_id=None,
                                aspect="unknown", night=night, gate_only=True, distractors=distractors,
                                timestamp="2026-03-20T19:00:00Z" if night else "2026-03-20T18:10:00Z", lat=0, lon=0))
    for record in records:
        record.update(observability(record))
    (out/"labels.json").write_text(json.dumps(records, indent=2)+"\n")
    print(f"{tier}/{split}: 112 night contacts + 8 day images + {16 if tier == 'hard' else 0} twilight images -> {out}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", default="data/nightlights")
    parser.add_argument("--split", choices=["dev", "test", "test2", "test3"], required=True)
    parser.add_argument("--tier", choices=["easy", "hard"], default="easy")
    args = parser.parse_args()
    print("COMMAND:", shlex.join([sys.executable]+sys.argv))
    generate(args.out, args.split, args.tier)
