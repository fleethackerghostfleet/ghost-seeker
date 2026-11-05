from pathlib import Path
import runpy
import numpy as np
from edgeml.nightlights import load_rules


def test_unresolvable_cases_are_labeled_not_removed():
    module = runpy.run_path(str(Path(__file__).resolve().parents[1]/"scripts/nightlights_synth.py"))
    record = dict(lights=[dict(colour="red", occluded=False)], degradation=dict(radius=0, saturate=False))
    assert not module["observability"](record)["unresolvable"]
    record["degradation"]["saturate"] = True
    assert module["observability"](record)["reasons"] == ["single_pixel_colour_erased_by_saturation"]
    record["lights"][0]["occluded"] = True
    assert module["observability"](record)["reasons"] == ["permanently_occluded_light"]


def test_hard_renderer_is_deterministic_and_labels_nuisances():
    module = runpy.run_path(str(Path(__file__).resolve().parents[1]/"scripts/nightlights_synth.py"))
    contact = module["hard_contact"](np.random.default_rng(88), [100, 90, 100, 90], ["red", "white", "red"], True)
    contact["lights"][0]["occluded"] = True
    image, nuisance = module["hard_scene"](np.random.default_rng(99), [contact])
    repeat, labels = module["hard_scene"](np.random.default_rng(99), [contact])
    assert np.array_equal(image, repeat) and nuisance == labels
    assert image.shape == (240, 360, 3) and image.dtype == np.uint8
    assert {p["source"] for p in nuisance} == {"pier", "shore"}
    assert len(nuisance) == 17 and len(contact["lights"]) == 3
    twilight, _ = module["hard_scene"](np.random.default_rng(66), [], twilight=60)
    assert twilight.mean() > image.mean()


def test_urgent_boost_metrics_include_background_and_confusions():
    module = runpy.run_path(str(Path(__file__).parents[1]/"scripts/nightlights_boost_eval.py"))
    rules = {r["id"]: r for r in load_rules()}
    summarize = module["summarize"]
    result = summarize([dict(truth="ram", rule_id="ram", boost=.9),
                        dict(truth="nuc", rule_id="ram", boost=.9),
                        dict(truth=None, rule_id="nuc", boost=.9),
                        dict(truth="port", rule_id="port", boost=0)], rules)
    assert result["urgent_rules"]["ram"] == dict(true_urgent_boosts=1, false_urgent_boosts=1)
    assert result["urgent_rules"]["nuc"] == dict(true_urgent_boosts=0, false_urgent_boosts=1)
    assert result["false_boosts"] == 2
    assert summarize([], rules)["urgent_rules"]["ram"]["true_urgent_boosts"] == 0


def test_boost_metric_denominators():
    module = runpy.run_path(str(Path(__file__).parents[1]/"scripts/nightlights_boost_eval.py"))
    rules = {r["id"]: r for r in load_rules()}
    summarize = module["summarize"]
    result = summarize([dict(truth="head_on", rule_id="nuc", boost=.9),
                        dict(truth="nuc", rule_id="nuc", boost=0)], rules)
    assert result["false_boost_rate"] == 1 and result["false_boost_per_contact"] == .5
    assert result["missed_boost_rate"] == 1
    assert summarize([], rules)["false_boost_rate"] is None


def test_benchmark_smoke():
    module = runpy.run_path(str(Path(__file__).resolve().parents[1] / "scripts/nightlights_dark_eval.py"))
    result = module["evaluate"](split="dev", tier="easy", n=1)
    assert result["dark_contact"]["total"] == 4
    assert len(result["burst"]["scenarios"]) == 5
    assert all(scenario["samples"] == 1 and scenario["requested"] in (0, 1)
               for scenario in result["burst"]["scenarios"].values())
    continuous = result["burst"]["continuous_triggers_120s_at_1fps"]
    assert continuous["accepted"] == continuous["new_intervals"] + continuous["extensions"]
    assert continuous["accepted"] + continuous["denied"] == 120
