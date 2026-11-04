import json
import math
from datetime import datetime, timezone
from pathlib import Path

import cv2
import numpy as np


RULE_PATH = Path(__file__).resolve().parents[4] / "internal/catalog/colregs_lights.json"


def load_rules(path=RULE_PATH):
    return json.loads(Path(path).read_text())


def solar_elevation(timestamp, lat, lon):
    """NOAA fractional-year approximation, UTC; degrees above horizon."""
    if not -90 <= lat <= 90 or not -180 <= lon <= 180:
        raise ValueError("invalid latitude/longitude")
    t = datetime.fromisoformat(timestamp.replace("Z", "+00:00")) if isinstance(timestamp, str) else timestamp
    if t.tzinfo is None:
        raise ValueError("timestamp must be timezone-aware")
    t = t.astimezone(timezone.utc)
    hour = t.hour + t.minute / 60 + t.second / 3600
    g = 2 * math.pi / 365 * (t.timetuple().tm_yday - 1 + (hour - 12) / 24)
    eq = 229.18 * (0.000075 + 0.001868 * math.cos(g) - 0.032077 * math.sin(g)
                   - 0.014615 * math.cos(2*g) - 0.040849 * math.sin(2*g))
    dec = (0.006918 - 0.399912*math.cos(g) + 0.070257*math.sin(g)
           - 0.006758*math.cos(2*g) + 0.000907*math.sin(2*g)
           - 0.002697*math.cos(3*g) + 0.00148*math.sin(3*g))
    ha = math.radians((hour * 60 + eq + 4 * lon) / 4 - 180)
    la = math.radians(lat)
    return math.degrees(math.asin(np.clip(math.sin(la)*math.sin(dec) + math.cos(la)*math.cos(dec)*math.cos(ha), -1, 1)))


def gate(image, timestamp=None, lat=None, lon=None, modality="EO"):
    lum = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
    mean, dark = float(lum.mean()), float((lum < 55).mean())
    if modality.upper() in {"SAR", "RADAR"}:
        return dict(night=None, confidence=0.0, basis="not_applicable_radar", lum=mean,
                    dark_fraction=dark, solar_elevation=None)
    v = image.max(axis=2)
    shadow = float((v < 35).mean())
    elevation = solar_elevation(timestamp, lat, lon) if timestamp is not None and lat is not None and lon is not None else None
    if mean < 45 and dark > 0.8 and shadow > 0.65:
        night, confidence, basis = True, 0.95, "image"
    elif (mean > 95 and dark < 0.65) or mean > 150 or (float(v.mean()) > 65 and shadow < 0.2):
        night, confidence, basis = False, 0.95, "image"
    elif elevation is not None:
        night, confidence, basis = elevation < -6, 0.75, "solar"
    else:
        night, confidence, basis = mean < 65 and dark > 0.6 and shadow > 0.4, 0.5, "image_ambiguous"
    return dict(night=night, confidence=confidence, basis=basis, lum=mean, dark_fraction=dark, solar_elevation=elevation)


def colour(h, s):
    if s < 65:
        return "white"
    if h < 18 or h >= 170:
        return "red"
    if h < 38:
        return "yellow"
    if h < 95:
        return "green"
    return "blue"


def detect_lights(image, box, pad=0.2, max_candidates=8):
    x, y, w, h = map(float, box)
    if w <= 0 or h <= 0:
        raise ValueError("box dimensions must be positive")
    ih, iw = image.shape[:2]
    x0, y0 = max(0, int(x-w*pad)), max(0, int(y-h*pad))
    x1, y1 = min(iw, math.ceil(x+w*(1+pad))), min(ih, math.ceil(y+h*(1+pad)))
    if x1 <= x0 or y1 <= y0:
        return []
    hsv = cv2.cvtColor(image[y0:y1, x0:x1], cv2.COLOR_BGR2HSV)
    v = hsv[:, :, 2]
    local_background = cv2.GaussianBlur(v.astype(np.float32), (0, 0), 4)
    top = cv2.morphologyEx(v, cv2.MORPH_TOPHAT, np.ones((15, 15), np.uint8))
    mask = ((v >= 100) & (top >= 65) & (v-local_background >= 65)).astype(np.uint8)
    n, labels, stats, centers = cv2.connectedComponentsWithStats(mask)
    lights = []
    for i in range(1, n):
        _, _, bw, bh, area = stats[i]
        if not 1 <= area <= min(70, max(55, w*h*0.004)) or max(bw, bh) > 13 or max(bw, bh)/max(1, min(bw, bh)) > 3 or area/(bw*bh) < 0.5:
            continue
        contours, _ = cv2.findContours((labels == i).astype(np.uint8), cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
        perimeter = cv2.arcLength(contours[0], True)
        if perimeter and 4*math.pi*area/perimeter**2 < 0.45:
            continue
        cx0, cy0 = map(lambda q: int(round(q)), centers[i])
        lx, ly, rx, ry = max(0, cx0-9), max(0, cy0-9), min(v.shape[1], cx0+10), min(v.shape[0], cy0+10)
        patch = v[ly:ry, lx:rx]
        ring = (labels[ly:ry, lx:rx] != i)
        background = patch[ring]
        peak = float(v[labels == i].max())
        if not len(background) or peak-float(np.median(background)) < 70 or float(np.mean(background > peak*.55)) > .25:
            continue
        if float(background.std()) > 45:
            continue
        component = (labels == i).astype(np.uint8)
        halo = cv2.dilate(component, np.ones((5, 5), np.uint8)).astype(bool)
        pixels = hsv[halo & (v >= max(45, peak*.2))]
        chromatic = pixels[(pixels[:, 1] >= 85) & (pixels[:, 2] >= max(50, peak*.25))]
        if len(chromatic) >= max(1, len(pixels)*.15):
            pixels = chromatic
        hue, sat = np.median(pixels[:, 0]), np.median(pixels[:, 1])
        # Red wraps around the hue origin.
        if np.mean((pixels[:, 0] < 18) | (pixels[:, 0] >= 170)) > 0.5:
            hue = 0
        cx, cy = centers[i] + [x0, y0]
        if cy > y+h*.85:
            continue
        lights.append(dict(colour=colour(hue, sat), x=float(cx), y=float(cy),
                           relative=[float((cx-x)/w), float((cy-y)/h)], flashing=False,
                           sodium_ambiguous=bool(sat >= 85 and 10 <= np.median(pixels[:, 0]) < 27),
                           contrast=peak-float(np.median(background))))
    full_v = image.max(axis=2)
    peaks = ((full_v >= 135) & (full_v-cv2.GaussianBlur(full_v.astype(np.float32), (0, 0), 4) >= 65)).astype(np.uint8)
    _, _, row_stats, row_centers = cv2.connectedComponentsWithStats(peaks)
    row_centers = [c for s, c in zip(row_stats[1:], row_centers[1:]) if 1 <= s[4] <= 70]
    def pier_row(light):
        xs = sorted(c[0] for c in row_centers if abs(c[1]-light["y"]) <= 9)
        gaps = np.diff(xs)
        return light["colour"] in {"white", "yellow"} and len(xs) >= 6 and len(gaps) and np.median(gaps) > 3 and float(np.mean(abs(gaps-np.median(gaps)) < np.median(gaps)*.45)) >= .7
    lights = [p for p in lights if not pier_row(p) and sum(abs(p["y"]-q["y"]) < 4 for q in lights) < 4
              and (not p["sodium_ambiguous"] or any(q is not p and abs(q["relative"][0]-p["relative"][0]) < .09
                                                    and 4 < abs(q["y"]-p["y"]) < h*.6 for q in lights))]
    lights = sorted(lights, key=lambda light: -light["contrast"])[:max_candidates]
    return sorted(lights, key=lambda light: (light["y"], light["x"]))


def aspect(lights):
    sidelights = [p for p in lights if not any(q is not p and abs(q["relative"][0]-p["relative"][0]) < 0.09 for q in lights)]
    colours = {p["colour"] for p in sidelights}
    if {"red", "green"} <= colours:
        return "head_on"
    if "red" in colours and "green" not in colours:
        return "port_side_crossing_right_to_left"
    if "green" in colours and "red" not in colours:
        return "starboard_side_crossing_left_to_right"
    whites = sorted((p for p in lights if p["colour"] == "white"), key=lambda p: p["y"])
    if len(whites) == 2 and abs(whites[1]["relative"][0]-whites[0]["relative"][0]) > 0.15:
        return "starboard_side_crossing_left_to_right" if whites[1]["x"] > whites[0]["x"] else "port_side_crossing_right_to_left"
    return "unknown"


def interpret(lights, night, modality="EO", rules=None, signal=None):
    result = dict(rule_id=None, aspect="unknown", line="No recognized light pattern", boost=0.0)
    if modality.upper() in {"IR", "SAR", "THERMAL", "RADAR"}:
        return dict(result, line="Colour not observable in IR/SAR imagery; colour rules skipped")
    if not night:
        return dict(result, line="Day: navigation-light rules not applied")
    result["aspect"] = "unknown" if signal is not None else aspect(lights)
    whites = sorted((p for p in lights if p["colour"] == "white"), key=lambda p: p["y"])
    result["masthead_horizontal_offset"] = whites[-1]["x"]-whites[0]["x"] if len(whites) >= 2 else None
    result["aspect_basis"] = "isolated sidelights; offset whites conditionally assume higher aft/lower forward mastheads (Rule 23), not towing lights"
    stacks = []
    for light in lights:
        group = next((g for g in stacks if abs(g[0]["relative"][0]-light["relative"][0]) < 0.09), None)
        if group is None:
            stacks.append([light])
        else:
            group.append(light)
    colours = sorted(p["colour"] for p in lights)
    for rule in rules if rules is not None else load_rules():
        pattern = rule["pattern"]
        if signal is not None and "signal" not in pattern:
            continue
        if "signal" in pattern:
            matches = signal == pattern["signal"]
        elif pattern["layout"] == "vertical":
            matches = any([p["colour"] for p in sorted(g, key=lambda p: p["y"])] == pattern["colours"] for g in stacks)
        elif rule["id"] in {"head_on", "port", "starboard"}:
            isolated = [p["colour"] for p in lights if not any(q is not p and abs(q["relative"][0]-p["relative"][0]) < 0.09 for q in lights)]
            matches = sorted(c for c in isolated if c in {"red", "green"}) == sorted(pattern["colours"])
        elif rule["id"] == "two_mastheads":
            matches = colours.count("white") == 2
        else:
            matches = colours == sorted(pattern["colours"])
        flashing = rule["flashing"]
        if matches and (flashing is None or any(p["flashing"] == flashing for p in lights)):
            assisted = "signal" in pattern and signal == pattern["signal"]
            low_confidence = not assisted and colours != sorted(pattern["colours"])
            return dict(result, rule_id=rule["id"], line=rule["line"],
                        boost=0.0 if low_confidence else rule["priority_boost"],
                        ref=rule["ref"], severity=rule["severity"], assisted=assisted,
                        low_confidence=low_confidence,
                        confidence_note="extra unexplained lights — verify, no priority boost" if low_confidence else "")
    return result


def analyze(frames, box, modality="EO", timestamp=None, lat=None, lon=None, rules=None, signal=None):
    if isinstance(frames, np.ndarray):
        frames = [frames]
    if not frames:
        raise ValueError("at least one frame required")
    decision = gate(frames[0], timestamp, lat, lon, modality)
    observations = [detect_lights(f, box) for f in frames] if decision["night"] and modality.upper() not in {"IR", "SAR", "THERMAL", "RADAR"} else []
    lights = []
    for frame in observations:
        for light in frame:
            if not any(p["colour"] == light["colour"] and math.hypot(p["x"]-light["x"], p["y"]-light["y"]) < 6 for p in lights):
                lights.append(light.copy())
    for light in lights:
        present = [any(p["colour"] == light["colour"] and math.hypot(p["x"]-light["x"], p["y"]-light["y"]) < 6 for p in obs) for obs in observations]
        light["flashing"] = any(present) and not all(present)
    lights = sorted(lights, key=lambda light: -light["contrast"])[:8]
    return dict(decision, lights=lights, interpretation=interpret(lights, decision["night"], modality, rules, signal))
