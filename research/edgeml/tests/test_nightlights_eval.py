import importlib.util
import json
import runpy
from pathlib import Path

import pytest


def test_day_and_twilight_counts_are_disjoint(tmp_path, monkeypatch):
    scripts = Path(__file__).resolve().parents[1]/"scripts"
    monkeypatch.syspath_prepend(str(scripts))
    spec = importlib.util.spec_from_file_location("evaluation", scripts/"nightlights_eval.py")
    evaluation = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(evaluation)
    light = dict(colour="red", x=10, y=10, flashing=False)
    labels = [dict(rule_id=r["id"], night=True, lights=[light]) for r in evaluation.load_rules()]
    labels += [dict(rule_id=None, night=False, lights=[]) for _ in range(8)]
    labels += [dict(rule_id=None, night=i < 8, lights=[], gate_only=True) for i in range(16)]
    for record in labels:
        record.update(frames=["frame.png"], box=[0, 0, 20, 20], aspect="unknown")
    directory = tmp_path/"hard"/"dev"
    directory.mkdir(parents=True)
    (directory/"labels.json").write_text(json.dumps(labels))
    results = iter(dict(lights=r["lights"], night=r["night"],
                        interpretation=dict(rule_id=r["rule_id"], aspect="unknown")) for r in labels)
    monkeypatch.setattr(evaluation, "analyze", lambda *args, **kwargs: next(results))
    monkeypatch.setattr(evaluation.cv2, "imread", lambda path: None)
    monkeypatch.setattr(evaluation, "observability", lambda record: dict(unresolvable=False))
    report = tmp_path/"summaries"
    evaluation.evaluate(tmp_path, "dev", report, tier="hard")
    summary = json.loads((report/"hard_dev.json").read_text())
    metrics = summary["metrics"]
    assert metrics["day_images"] == 8
    assert metrics["twilight_images"] == 16
    assert metrics["gate_images"] == metrics["night_contacts"] + metrics["day_images"] + metrics["twilight_images"]
    assert "contacts" not in summary
    assert len(json.loads((tmp_path/"reports"/"hard_dev.json").read_text())["contacts"]) == len(labels)


def test_boost_evaluation_versioned_dump(tmp_path, monkeypatch):
    scripts = Path(__file__).resolve().parents[1]/"scripts"
    spec = importlib.util.spec_from_file_location("boost_evaluation", scripts/"nightlights_boost_eval.py")
    evaluation = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(evaluation)
    monkeypatch.setattr(evaluation, "ROOT", tmp_path)
    with pytest.raises(FileNotFoundError):
        evaluation.evaluate("dev", "missing")
    observations = tmp_path/"data/nightlights/reports"
    observations.mkdir(parents=True)
    summaries = tmp_path/"research/edgeml/reports/nightlights"
    summaries.mkdir(parents=True)
    legacy = summaries/"boost_gate_dev.json"
    legacy.write_text("unchanged")
    (observations/"easy_dev_baseline.json").write_text(json.dumps(dict(contacts=[])))
    evaluation.evaluate("dev", "baseline", "easy")
    output = json.loads((summaries/"boost_gate_easy_dev_baseline.json").read_text())
    assert output["metrics"]["with_gate"]["all"]["contacts"] == 0
    assert output["observations"] == "data/nightlights/reports/easy_dev_baseline.json"
    assert legacy.read_text() == "unchanged"


def test_dark_summary_uses_saved_observations(tmp_path, monkeypatch):
    scripts = Path(__file__).resolve().parents[1]/'scripts'
    monkeypatch.syspath_prepend(str(scripts))
    module = runpy.run_path(str(scripts/'nightlights_dark_eval.py'))
    path = tmp_path/'observations.json'
    summarize = module['summarize_observations']
    with pytest.raises(FileNotFoundError):
        summarize(path)
    path.write_text(json.dumps(dict(contacts=[])))
    assert summarize(path)['lit_night_contacts'] == 0
    rows = []
    for seed, night, rule, lights, detected_night, flagged in [
            (1, True, 'nuc', [{}], True, True),
            (2, True, 'nuc', [{}], True, False),
            (3, True, 'nuc', [{}], False, False),
            (4, False, None, [], False, False),
            (5, True, None, [], True, True),
            (6, True, 'nuc', [dict(occluded=True)], True, True)]:
        rows.append(dict(truth=dict(seed=seed, night=night, rule_id=rule, lights=lights),
                         result=dict(night=detected_night, dark_contact=dict(flagged=flagged))))
    path.write_text(json.dumps(dict(contacts=rows)))
    result = summarize(path)
    assert result['lit_night_contacts'] == 3
    assert result['false_dark_contacts'] == 1 and result['false_dark_seeds'] == [1]
    assert result['night_gate_misses'] == 1
    path.write_text('{}')
    with pytest.raises(KeyError):
        summarize(path)
