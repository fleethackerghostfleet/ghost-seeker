from datetime import datetime, timezone
from pathlib import Path
import json

import cv2
import numpy as np
import pytest

from edgeml.nightlights import analyze, detect_lights, gate, interpret, load_rules, solar_elevation
from edgeml.nightlights.core import colour


def point(c, x=100, y=50, flashing=False):
    return dict(colour=c, x=x, y=y, relative=[x/200, y/150], flashing=flashing, conf=1.)


@pytest.mark.parametrize("rule", load_rules(), ids=lambda r: r["id"])
def test_rules(rule):
    pattern = rule["pattern"]
    vertical = pattern.get("layout") == "vertical"
    lights = [point(c, 100 if vertical else 50+i*60, 30+i*25 if vertical else 50,
                    flashing=bool(rule["flashing"])) for i, c in enumerate(pattern.get("colours", []))]
    result = interpret(lights, True, signal=pattern.get("signal"))
    assert result["rule_id"] == rule["id"]
    assert result["boost"] == rule["priority_boost"]
    assert 0 <= result["boost"] <= 1


@pytest.mark.parametrize("h,s,expected", [(0, 240, "red"), (179, 240, "red"), (60, 240, "green"),
                                         (30, 240, "yellow"), (120, 240, "blue"), (0, 5, "white")])
def test_colour(h, s, expected):
    assert colour(h, s) == expected


def test_boost_requires_complete_pattern():
    stack = [point("red", y=30), point("red", y=60)]
    assert interpret(stack, True)["boost"] == .9
    for extras in [[point("white", x=180)], [point("green", x=180)],
                   [point("white", x=160), point("green", x=190), point("red", x=20)]]:
        result = interpret(stack+extras, True)
        assert result["rule_id"] == "nuc"
        assert result["boost"] == 0 and result["low_confidence"]
        assert result["confidence_note"] == "extra unexplained lights — verify, no priority boost"
    result = interpret(stack+[point("green", x=180)], True, signal="sos")
    assert result["boost"] == 1 and result["assisted"] and not result["low_confidence"]
    assert interpret(stack, False)["boost"] == 0
    assert interpret(stack, True, signal="invalid")["boost"] == 0


def test_pattern_confidence_preserves_rule_and_strict_coverage(monkeypatch):
    from edgeml.nightlights import core
    monkeypatch.setattr(core, 'load_config', lambda: {'lights': {'pattern_conf_threshold': .85}})
    stack = [dict(point('red', y=30), conf=.9), dict(point('red', y=60), conf=.8)]
    result = interpret(stack, True)
    assert result['rule_id'] == 'nuc' and result['boost'] == 0
    assert result['pattern_conf'] == .8
    stack[1]['conf'] = .95
    assert interpret(stack, True)['boost'] == .9
    assert interpret(stack+[dict(point('white', x=180), conf=1)], True)['boost'] == 0
    assert interpret([], True)['pattern_conf'] == 0


def test_four_frame_persistence_keeps_flashing_not_singletons(monkeypatch):
    from edgeml.nightlights import core
    red = dict(point('red'), contrast=200., conf=.9)
    white = dict(point('white', x=180), contrast=200., conf=.9)
    observations = iter([[red, white], [], [red], []])
    monkeypatch.setattr(core, 'detect_lights', lambda *args, **kwargs: next(observations) if not kwargs else [])
    image = np.zeros((100, 200, 3), np.uint8)
    result = analyze([image]*4, [0, 0, 200, 100])
    assert len(result['lights']) == 1
    assert result['lights'][0]['colour'] == 'red'
    assert result['lights'][0]['persistence'] == 2 and result['lights'][0]['flashing']


def test_bright_resolved_pair_confidence_with_faint_reflection():
    image = np.zeros((160, 200, 3), np.uint8)
    for y in (35, 60):
        cv2.circle(image, (105, y), 3, (0, 0, 255), -1)
    cv2.line(image, (105, 103), (105, 120), (0, 0, 80), 1)
    image = cv2.GaussianBlur(image, (3, 3), .7)
    result = analyze([image]*4, [40, 20, 80, 80])
    assert len(result['lights']) == 2
    assert all(0 <= light['conf'] <= 1 for light in result['lights'])
    assert result['interpretation']['pattern_conf'] >= .9
    assert result['interpretation']['rule_id'] == 'nuc'
    assert result['interpretation']['boost'] == .9


def test_demo_observations_are_shared_python_go_interpretation_fixtures():
    catalog = Path(__file__).resolve().parents[2]/'internal/catalog/nightlights.json'
    for image in json.loads(catalog.read_text())['nightLibrary']:
        for det in image['dets']:
            observation = det.get('track_observation', det)
            lights = observation['lights']
            assert all('conf' in p and 0 <= p['conf'] <= 1 for p in lights)
            result = interpret(lights, image['night'], signal=det.get('signal'))
            expected = det['interpretation']
            assert result['rule_id'] == expected['rule_id']
            assert result['boost'] == expected['boost']


def test_shared_light_confidence_cases():
    fixture = Path(__file__).resolve().parents[2]/'internal/catalog/light_confidence_cases.json'
    for case in json.loads(fixture.read_text()):
        result = interpret(case['lights'], True, signal=case['signal'] or None)
        assert result['rule_id'] == case['rule_id'], case['name']
        assert result['boost'] == case['boost'], case['name']


@pytest.mark.parametrize('value', [None, '0.95', True, -1, 1.1, float('nan'), float('inf')])
def test_invalid_light_confidence_is_conservative(value):
    lights = [dict(point('red', y=30), conf=value), point('red', y=60)]
    result = interpret(lights, True)
    assert result['rule_id'] == 'nuc' and result['pattern_conf'] == 0 and result['boost'] == 0
    assert interpret(lights, True, signal='sos')['boost'] == 1






def test_gate_image_and_solar():
    dark = np.zeros((100, 100, 3), np.uint8)
    bright = dark+180
    assert gate(dark, "2026-03-20T12:00:00Z", 0, 0)["night"]
    assert not gate(bright, "2026-03-20T00:00:00Z", 0, 0)["night"]
    ambiguous = dark+60
    midnight = gate(ambiguous, "2026-03-20T00:00:00Z", 0, 0)
    assert midnight["basis"] == "solar" and midnight["night"]
    assert not gate(ambiguous, "2026-03-20T12:00:00Z", 0, 0)["night"]
    assert gate(ambiguous)["basis"] == "image_ambiguous"
    assert solar_elevation(datetime(2026, 3, 20, 12, tzinfo=timezone.utc), 0, 0) > 85
    with pytest.raises(ValueError):
        solar_elevation("2026-03-20T12:00:00", 0, 0)
    with pytest.raises(ValueError):
        solar_elevation("2026-03-20T12:00:00Z", 91, 0)


@pytest.mark.parametrize("modality", ["IR", "SAR", "thermal", "radar"])
def test_no_colour_for_non_optical(modality):
    assert "Colour not observable" in interpret([point("red")], True, modality)["line"]
    assert "Colour not observable" in interpret([point("red")], False, modality)["line"]
    result = analyze(np.zeros((150, 200, 3), np.uint8), [0, 0, 200, 150], modality)
    assert result["lights"] == [] and result["interpretation"]["rule_id"] is None


def test_day_never_applies_light_rules():
    assert interpret([point("red"), point("green", x=150)], False)["rule_id"] is None
    image = np.full((150, 200, 3), 160, np.uint8)
    cv2.circle(image, (100, 50), 3, (0, 0, 255), -1)
    result = analyze(image, [0, 0, 200, 150])
    assert not result["night"] and not result["lights"] and result["interpretation"]["rule_id"] is None


@pytest.mark.parametrize("bgr,expected", [((0, 0, 255), "red"), ((0, 255, 0), "green"),
                                         ((255, 255, 255), "white"), ((0, 255, 255), "yellow"), ((255, 0, 0), "blue")])
def test_blobs_and_flashing(bgr, expected):
    image = np.zeros((150, 200, 3), np.uint8)
    cv2.circle(image, (100, 50), 3, bgr, -1)
    lights = detect_lights(image, [60, 40, 80, 80])
    assert len(lights) == 1 and lights[0]["colour"] == expected
    result = analyze([image, np.zeros_like(image), image], [60, 40, 80, 80])
    assert len(result["lights"]) == 1 and result["lights"][0]["flashing"]
    assert not analyze([image, image], [60, 40, 80, 80])["lights"][0]["flashing"]


def test_empty_and_errors():
    empty = np.zeros((100, 100, 3), np.uint8)
    assert detect_lights(empty, [10, 10, 80, 80]) == []
    assert detect_lights(empty, [300, 300, 80, 80]) == []
    assert interpret([], True)["rule_id"] is None
    assert interpret([point("yellow")], True)["rule_id"] is None
    with pytest.raises(ValueError):
        analyze([], [0, 0, 10, 10])
    with pytest.raises(ValueError):
        detect_lights(empty, [0, 0, 0, 10])


def test_aspect_and_stack_order():
    assert interpret([point("red")], True, signal="red_flare")["aspect"] == "unknown"
    assert interpret([point("red")], True)["aspect"] == "port_side_crossing_right_to_left"
    assert interpret([point("green")], True)["aspect"] == "starboard_side_crossing_left_to_right"
    assert interpret([point("red", 50), point("green", 150)], True)["aspect"] == "head_on"
    stack = [point("red", y=30), point("white", y=60), point("red", y=90)]
    result = interpret(stack, True)
    assert result["rule_id"] == "ram" and result["aspect"] == "unknown"
    assert interpret([point("white", 60, 40), point("white", 140, 65)], True)["aspect"] == "starboard_side_crossing_left_to_right"
    assert interpret([point("white", 140, 40), point("white", 60, 65)], True)["aspect"] == "port_side_crossing_right_to_left"
    assert interpret([point("white", y=30), point("red", y=60)], True)["rule_id"] == "pilot"
    assert interpret([point("red", 50), point("green", 150), point("white", 100, 20)], True)["rule_id"] == "head_on"
    assert interpret([point("green", 150), point("white", 100, 20)], True)["rule_id"] == "starboard"


def test_padding_and_large_glare():
    image = np.zeros((150, 200, 3), np.uint8)
    cv2.circle(image, (58, 50), 2, (0, 0, 255), -1)
    cv2.rectangle(image, (90, 50), (130, 100), (255, 255, 255), -1)
    lights = detect_lights(image, [60, 40, 80, 80])
    assert len(lights) == 1 and lights[0]["x"] < 60


def test_mode_aware_gate():
    dark = np.zeros((150, 200, 3), np.uint8)
    assert gate(dark, modality="SAR")["night"] is None
    assert gate(dark, modality="radar")["basis"] == "not_applicable_radar"
    assert gate(dark, modality="IR")["night"] is True
    assert analyze(dark, [0, 0, 200, 150], "SAR")["night"] is None
    diffuse_blue = np.full_like(dark, (105, 65, 15))
    assert gate(diffuse_blue)["night"] is False


def test_texture_row_and_candidate_limit():
    rng = np.random.default_rng(55)
    texture = rng.integers(70, 240, (150, 200, 3), dtype=np.uint8)
    assert detect_lights(texture, [0, 0, 200, 150]) == []
    image = np.zeros_like(texture)
    for i in range(12):
        cv2.circle(image, (20+i*13, 50), 2, (255, 255, 255), -1)
    assert detect_lights(image, [0, 0, 200, 150]) == []
    image[:] = 0
    for y in (25, 60, 100):
        for x in (25, 75, 125):
            cv2.circle(image, (x, y), 2, (0, 0, 255), -1)
    assert len(detect_lights(image, [0, 0, 200, 150])) == 8
    assert len(detect_lights(image, [0, 0, 200, 150], max_candidates=3)) == 3
    assert len(analyze([image, image], [0, 0, 200, 150])["lights"]) <= 8


def test_tiny_isolated_light_and_low_contrast_rejection():
    image = np.zeros((100, 100, 3), np.uint8)
    image[30, 30] = (0, 0, 255)
    assert len(detect_lights(image, [0, 0, 100, 100])) == 1
    image[:] = 120
    cv2.circle(image, (30, 30), 2, (180, 180, 180), -1)
    assert detect_lights(image, [0, 0, 100, 100]) == []




def test_reflected_sibling_does_not_drop_unreflected_light():
    image = np.zeros((180, 200, 3), np.uint8)
    for x, y in [(60, 40), (66, 65)]:
        cv2.circle(image, (x, y), 2, (0, 0, 255), -1)
    box = (20, 20, 160, 100)
    before = detect_lights(image, box)
    assert len(before) == 2
    cv2.line(image, (60, 120), (60, 150), (0, 0, 80), 2)
    cv2.circle(image, (110, 50), 2, (20, 105, 235), -1)
    lights = detect_lights(image, box)
    assert [(p['x'], p['y']) for p in lights] == [(60, 40), (66, 65)]
    assert lights[0]['reflection'] >= 12 and lights[1]['reflection'] == 0
    assert all(p['colour'] == 'red' and p['conf'] >= .85 for p in lights)
    assert interpret(lights, True)['boost'] == .9


def test_unreflected_bloomed_pair_uses_colour_and_grouping():
    image = np.zeros((180, 200, 3), np.uint8)
    for y in (40, 65):
        cv2.circle(image, (60, y), 3, (0, 0, 255), -1)
    image = cv2.GaussianBlur(image, (0, 0), 2)
    lights = detect_lights(image, (20, 20, 160, 100))
    assert len(lights) == 2
    assert all(not p['sharp'] and p['reflection'] == 0 for p in lights)
    assert interpret(lights, True)['boost'] == .9
    image[53:] = 0
    assert detect_lights(image, (20, 20, 160, 100)) == []


def test_saturated_core_uses_chromatic_halo():
    image = np.zeros((120, 160, 3), np.uint8)
    cv2.circle(image, (80, 40), 4, (10, 60, 220), -1)
    cv2.circle(image, (80, 40), 2, (255, 255, 255), -1)
    lights = detect_lights(image, [20, 10, 120, 100])
    assert len(lights) == 1 and lights[0]["colour"] == "red"


def test_sodium_and_below_hull_rejection():
    image = np.zeros((120, 160, 3), np.uint8)
    cv2.circle(image, (80, 40), 2, (20, 105, 235), -1)
    assert detect_lights(image, [20, 10, 120, 100]) == []
    image[:] = 0
    cv2.circle(image, (80, 105), 2, (255, 255, 255), -1)
    assert detect_lights(image, [20, 10, 120, 100]) == []
