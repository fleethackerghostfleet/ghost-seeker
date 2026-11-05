import json
import logging
import math
from datetime import datetime, timezone
from pathlib import Path

import cv2
import numpy as np


RULE_PATH = Path(__file__).resolve().parents[4] / "internal/catalog/colregs_lights.json"
CONFIG_PATH = Path(__file__).resolve().parents[4] / "internal/catalog/colregs_night_config.json"
_OPTICAL = {"EO", "RGB", "OPTICAL"}
logger = logging.getLogger(__name__)


def load_rules(path=RULE_PATH):
    return json.loads(Path(path).read_text())


def load_config(path=CONFIG_PATH):
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


def detect_lights(image, box, pad=0.2, max_candidates=8, *, associate=True):
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
    sharp_mask = ((v >= 100) & (top >= 65) & (v-local_background >= 65)).astype(np.uint8)
    _, sharp_labels, sharp_stats, sharp_centers = cv2.connectedComponentsWithStats(sharp_mask)
    response = cv2.GaussianBlur(v.astype(np.float32), (0, 0), .7)-local_background
    maxima = (response == cv2.dilate(response, np.ones((5, 5), np.uint8))) & (response > 8) & (v > 45)
    for index, (stat, center) in enumerate(zip(sharp_stats[1:], sharp_centers[1:]), 1):
        if stat[4] <= 70 and max(stat[2:4]) <= 13:
            maxima[sharp_labels == index] = False
            sx, sy = np.rint(center).astype(int)
            maxima[sy, sx] = True
    mask = maxima.astype(np.uint8)
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
        if not len(background) or peak-float(np.median(background)) < 30:
            continue
        if not associate:
            if peak >= 100 and peak-local_background[cy0, cx0] >= 65:
                cx, cy = centers[i] + [x0, y0]
                lights.append(dict(x=float(cx), y=float(cy)))
            continue
        sharp_id = sharp_labels[cy0, cx0]
        _, core_labels, core_stats, core_centers = cv2.connectedComponentsWithStats(
            (patch >= max(45, peak*.5)).astype(np.uint8))
        core_id = core_labels[cy0-ly, cx0-lx]
        _, _, core_w, core_h, _ = core_stats[core_id]
        compact = max(core_w, core_h) <= 13 and abs(core_centers[core_id][0]-(cx0-lx)) <= 2
        sharp = False
        if sharp_id:
            _, _, sw, sh, sa = sharp_stats[sharp_id]
            sharp_background = patch[sharp_labels[ly:ry, lx:rx] != sharp_id]
            sharp = (sa <= 70 and max(sw, sh) <= 13 and max(sw, sh)/max(1, min(sw, sh)) <= 3
                     and sa/(sw*sh) >= .5 and len(sharp_background) > 0
                     and float(sharp_background.std()) < 15
                     and peak-float(np.median(sharp_background)) >= 200)
        component = (labels == i).astype(np.uint8)
        halo = cv2.dilate(component, np.ones((7, 7), np.uint8)).astype(bool)
        pixels = hsv[halo & (v >= max(30, peak*.15))]
        chromatic = pixels[(pixels[:, 1] >= 85) & (pixels[:, 2] >= max(35, peak*.15))]
        chromatic_fraction = len(chromatic)/max(1, len(pixels))
        if len(chromatic) >= max(1, len(pixels)*.15):
            pixels = chromatic
        hue, sat = np.median(pixels[:, 0]), np.median(pixels[:, 1])
        # Red wraps around the hue origin.
        if np.mean((pixels[:, 0] < 18) | (pixels[:, 0] >= 170)) > 0.5:
            hue = float(np.median((pixels[:, 0].astype(float)+90) % 180-90)) % 180
        cx, cy = centers[i] + [x0, y0]
        if cy > y+h*.85:
            continue
        core_sat = float(hsv[cy0, cx0, 1])
        yy, xx = np.indices(v.shape)
        annulus = ((xx-cx0)**2+(yy-cy0)**2 > 4) & ((xx-cx0)**2+(yy-cy0)**2 <= 16) & (v > max(30, peak*.15))
        local_sat = float(np.median(hsv[:, :, 1][annulus])) if annulus.any() else core_sat
        if core_sat < 70 and local_sat < 100 and chromatic_fraction < .4:
            classified, margin = 'white', 1-local_sat/160
        elif hue < 24 or hue >= 170:
            classified = 'red'
            margin = max(.1, 1-min(hue, 180-hue)/40)
        elif hue < 29:
            classified, margin = 'ambiguous_orange', .1
        else:
            classified, margin = colour(hue, sat), .9
        # Water reflections support vessel association. The synthetic benchmark
        # draws these only for vessel lights, so its discrimination is optimistic.
        bx = int(round(cx))
        by = max(0, int(y+h))
        end = min(ih, by+max(5, int(h*.5)))
        strip = image[by:end, max(0, bx-2):min(iw, bx+3)].astype(float)
        sides = image[by:end, max(0, bx-12):min(iw, bx+13)].astype(float)
        if strip.size and sides.size:
            excess = strip-np.median(sides, axis=1)[:, None, :]
            b, g, r = np.moveaxis(excess, -1, 0)
            compatible = {'red': (r > b*1.3) & (r > g*1.1),
                          'green': (g > r*1.3) & (g > b*1.1),
                          'blue': (b > r*1.3) & (b > g*1.1),
                          'yellow': (r > b*1.5) & (g > b*1.5),
                          'white': excess.min(axis=2) > excess.max(axis=2)*.5}.get(classified, np.zeros_like(r, dtype=bool))
            strength = np.where(compatible, excess.max(axis=2), 0).max(axis=1)
            support = max(0., float(np.mean(np.sort(strength)[-max(3, int(h*.08)):]))-12)
        else:
            support = 0.
        lights.append(dict(colour=classified, x=float(cx), y=float(cy),
                           relative=[float((cx-x)/w), float((cy-y)/h)], flashing=False,
                           reflection=support,
                           sharp=bool(sharp),
                           compact=bool(compact),
                           colour_margin=float(margin),
                           conf=float(np.clip(min((peak-float(np.median(background)))/160, margin), 0, 1)),
                           sodium_ambiguous=bool(sat >= 85 and 10 <= np.median(pixels[:, 0]) < 27),
                           contrast=peak-float(np.median(background))))
    if not associate:
        return lights
    full_v = image.max(axis=2)
    peaks = ((full_v >= 135) & (full_v-cv2.GaussianBlur(full_v.astype(np.float32), (0, 0), 4) >= 65)).astype(np.uint8)
    _, _, row_stats, row_centers = cv2.connectedComponentsWithStats(peaks)
    row_centers = [c for s, c in zip(row_stats[1:], row_centers[1:]) if 1 <= s[4] <= 70]
    def pier_row(light):
        xs = sorted(c[0] for c in row_centers if abs(c[1]-light["y"]) <= 9)
        gaps = np.diff(xs)
        return light["colour"] in {"white", "yellow"} and len(xs) >= 6 and len(gaps) and np.median(gaps) > 3
    # Co-column emitters can share a water streak when their reflections overlap.
    for p in lights:
        p['reflection'] = max(q['reflection'] for q in lights if abs(q['x']-p['x']) <= 2)
    associated = [p for p in lights if not pier_row(p) and (p['reflection'] >= 12 or
                  p['reflection'] >= 5 and any(q is not p and q['colour'] == p['colour']
                                              and abs(q['x']-p['x']) <= 2
                                              and abs(q['y']-p['y']) >= 3 for q in lights))]
    def independent(light):
        if light['sodium_ambiguous'] or pier_row(light):
            return False
        if sum(abs(light['y']-q['y']) < 4 for q in lights) >= 4:
            return False
        return light['sharp'] or (light['compact'] and light['colour_margin'] >= .8 and light['contrast'] >= 100
               and any(q is not light and q['colour'] == light['colour']
                       and q['compact'] and q['colour_margin'] >= .8 and q['contrast'] >= 100
                       and not q['sodium_ambiguous'] and not pier_row(q)
                       and abs(q['x']-light['x']) <= max(2, w*.04)
                       and abs(q['y']-light['y']) >= 3 for q in lights))
    lights = [p for p in lights if p in associated or independent(p)]
    for p in lights:
        column = [q for q in lights if abs(q['x']-p['x']) <= max(2, w*.04)]
        stack = min(1., len(column)/3)
        association = max(min(1., p['reflection']/40), float(p['sharp']))
        p['conf'] = float(np.clip(.25*p['colour_margin'] + .25*min(1., p['contrast']/180)
                                 + .15*association + .35*stack, 0, 1))
        # A resolved, bright, colour-pure pair is not weaker merely because it
        # contains two emitters rather than three, or has a faint water streak.
        aligned = [q for q in column if q is not p and abs(q['y']-p['y']) >= 3]
        if aligned:
            purity = float(np.clip((p['colour_margin']-.4)/.45, 0, 1))
            clarity = min(purity, p['contrast']/160)
            p['conf'] = max(p['conf'], float(np.clip(.95*clarity, 0, 1)))
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


def _light_confidence(light):
    value = light.get('conf', 0.)
    return float(value) if (isinstance(value, (int, float)) and not isinstance(value, bool)
                            and math.isfinite(value) and 0 <= value <= 1) else 0.


def interpret(lights, night, modality="EO", rules=None, signal=None):
    result = dict(rule_id=None, aspect="unknown", line="No recognized light pattern", boost=0.0)
    pattern_conf = min((_light_confidence(p) for p in lights), default=0.)
    result['pattern_conf'] = pattern_conf
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
            uncertain = not assisted and pattern_conf < load_config()['lights']['pattern_conf_threshold']
            return dict(result, rule_id=rule["id"], line=rule["line"],
                        boost=0.0 if low_confidence or uncertain else rule["priority_boost"],
                        ref=rule["ref"], severity=rule["severity"], assisted=assisted,
                        low_confidence=low_confidence or uncertain,
                        confidence_note="extra unexplained lights — verify, no priority boost" if low_confidence else
                        "uncertain light evidence — verify, no priority boost" if uncertain else "")
    return result


def analyze(frames, box, modality="EO", timestamp=None, lat=None, lon=None, rules=None, signal=None, *,
            label=None, conf=None, burst=None, t=None, config=None):
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
        light['persistence'] = sum(present)
    if len(frames) == 4:
        lights = [p for p in lights if p['persistence'] >= 2]
    lights = sorted(lights, key=lambda light: -light["contrast"])[:8]
    result = dict(decision, lights=lights, interpretation=interpret(lights, decision["night"], modality, rules, signal))
    cfg = config or load_config()
    reasons = burst_reasons(lights, result["interpretation"], len(frames), decision["night"], modality, cfg)
    granted = None
    fps = cfg["burst"]["burst_fps"]
    if burst is not None:
        granted = burst.request(t or 0.0, "; ".join(reasons)) if reasons else False
        fps = burst.fps(t or 0.0)
    result["burst"] = {"requested": bool(reasons), "reasons": reasons, "granted": granted,
                       "fps": fps, "duration_s": cfg["burst"]["duration_s"]}
    result["dark_contact"] = dark_contact(frames, box, label=label, conf=conf, gate_decision=decision,
                                           modality=modality, config=cfg)
    return result


def burst_reasons(lights, interpretation, n_frames, night, modality, config):
    if not night or modality.upper() not in _OPTICAL:
        return []
    triggers = config["burst"]["triggers"]
    reasons = []
    if triggers.get("possible_flashing_yellow", False) and n_frames < 2 \
            and any(light["colour"] == "yellow" for light in lights):
        reasons.append("possible flashing yellow light")
    if triggers.get("flashing_rate_unresolved", False) and any(light.get("flashing") is True for light in lights):
        reasons.append("flashing light, rate unresolved at base frame rate")
    if triggers.get("unresolved_light_stack", False) and len(lights) >= 2 and interpretation.get("rule_id") is None:
        reasons.append("unresolved light stack")
    if triggers.get("low_confidence_pattern", False) and interpretation.get("low_confidence", False):
        reasons.append(f"low confidence {interpretation.get('rule_id')}")
    return reasons


def dark_contact(frames_or_image, box, label=None, conf=None, gate_decision=None, modality="EO", config=None):
    cfg = (config or load_config())["dark_contact"]
    if modality.upper() not in _OPTICAL:
        return {"flagged": False, "confidence": 0.0, "n_lights": 0, "basis": "not_optical"}
    decision = gate_decision
    if decision is None:
        first = frames_or_image[0] if isinstance(frames_or_image, (list, tuple)) else frames_or_image
        decision = gate(first, modality=modality)
    if not decision.get("night"):
        return {"flagged": False, "confidence": 0.0, "n_lights": 0, "basis": "day"}
    terms = cfg["vessel_terms"]
    if label is not None and not any(term in label.casefold() for term in terms):
        return {"flagged": False, "confidence": 0.0, "n_lights": 0, "basis": "not_vessel"}
    frames = [frames_or_image] if isinstance(frames_or_image, np.ndarray) else list(frames_or_image)
    x, y, w, h = map(float, box)
    expanded = (x - cfg["side_pad"] * w,
                y - cfg["above"] * max(h, 4.0),
                w * (1 + 2 * cfg["side_pad"]),
                h + cfg["above"] * max(h, 4.0) + cfg["below"] * h)
    n_lights = sum(len(detect_lights(frame, expanded, pad=0, associate=False)) for frame in frames)
    if n_lights:
        return {"flagged": False, "confidence": 0.0, "n_lights": n_lights, "basis": "lights_present"}
    gate_confidence = float(decision.get("confidence", 0.0))
    confidence = round(min(1.0, (1.0 if conf is None else float(conf)) * (0.6 + 0.4 * gate_confidence)), 3)
    return {"flagged": True, "confidence": confidence, "n_lights": 0, "basis": "no_lights"}


def frame_report(image, detections, burst=None, t=None, modality="EO", config=None):
    cfg = config or load_config()
    decision = gate(image, modality=modality)
    dark = []
    burst_result = None
    for index, (label, conf, box) in enumerate(detections):
        dark_result = dark_contact(image, box, label=label, conf=conf, gate_decision=decision,
                                   modality=modality, config=cfg)
        if dark_result["flagged"]:
            dark.append((index, dark_result["confidence"]))
        lights = detect_lights(image, box) if decision.get("night") and modality.upper() in _OPTICAL else []
        interpretation = interpret(lights, decision.get("night"), modality)
        reasons = burst_reasons(lights, interpretation, 1, decision.get("night"), modality, cfg)
        if burst is not None and reasons and burst.request(t or 0.0, "; ".join(reasons)):
            burst_result = {"fps": burst.fps(t or 0.0), "duration_s": cfg["burst"]["duration_s"],
                            "reason": "; ".join(reasons)}
    return {"night": decision["night"], "dark": dark, "burst": burst_result}
