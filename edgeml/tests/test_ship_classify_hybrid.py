import copy
import importlib.util
import json
from pathlib import Path
import sys

import pytest

from edgeml.embed.shiptype import TYPES


@pytest.fixture
def tool(monkeypatch):
    path = Path(__file__).parents[1] / "tools" / "ship_classify_hybrid.py"
    monkeypatch.syspath_prepend(str(path.parent))
    spec = importlib.util.spec_from_file_location("ship_classify_hybrid", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_hybrid_selects_by_confidence_without_mutating_inputs(tool):
    probe = {"idx": [3, 7, 9], "preds": [{"type": "probe", "conf": c} for c in [0.2, 0.5, 0.9]]}
    fallback = {"idx": [3, 7, 9], "preds": [{"type": "fallback", "conf": 0.8}] * 3}
    before = copy.deepcopy((probe, fallback))

    result = tool.hybrid(probe, fallback, 0.5)

    assert result == {"idx": [3, 7, 9], "preds": [fallback["preds"][0], probe["preds"][1], probe["preds"][2]]}
    assert (probe, fallback) == before


def test_hybrid_accepts_empty_aligned_inputs(tool):
    assert tool.hybrid({"idx": [], "preds": []}, {"idx": [], "preds": []}, 0.5) == {"idx": [], "preds": []}


@pytest.mark.parametrize("probe_idx,probe_len,fallback_idx,fallback_len", [
    ([0, 1], 2, [1, 0], 2),
    ([0, 1], 2, [0, 2], 2),
    ([0, 1], 2, [0], 1),
    ([0], 1, [0, 1], 2),
    ([0, 1], 1, [0, 1], 2),
    ([0, 1], 2, [0, 1], 1),
    ([0, 1], 3, [0, 1], 2),
    ([0, 1], 2, [0, 1], 3),
    ([0, 1], 1, [0, 1], 1),
])
def test_hybrid_rejects_misalignment_and_truncation(tool, probe_idx, probe_len, fallback_idx, fallback_len):
    prediction = {"type": TYPES[0], "conf": 0.1}
    probe = {"idx": probe_idx, "preds": [prediction] * probe_len}
    fallback = {"idx": fallback_idx, "preds": [prediction] * fallback_len}
    with pytest.raises(ValueError):
        tool.hybrid(probe, fallback, 0.5)


@pytest.mark.parametrize("capped", [False, True])
def test_cli_validates_producer_artifacts_before_scoring(tool, tmp_path, monkeypatch, capsys, capped):
    items = [{"gt": TYPES[0]}, {"gt": TYPES[1]}]
    for split in ["dev", "test"]:
        (tmp_path / f"items_{split}.json").write_text(json.dumps(items))
        for model in ["probe", "fallback"]:
            idx = [0] if capped and model == "fallback" else [0, 1]
            artifact = {"idx": idx, "preds": [{"type": items[i]["gt"], "conf": 0.2} for i in idx]}
            (tmp_path / f"pred_{model}_{split}.json").write_text(json.dumps(artifact))
    monkeypatch.setattr(sys, "argv", ["ship_classify_hybrid.py", "--out", str(tmp_path),
                                     "--probe", "probe", "--fallback", "fallback", "--dev", "dev", "--test", "test"])

    if capped:
        with pytest.raises(ValueError, match="indices must match"):
            tool.main()
        assert capsys.readouterr().out == ""
    else:
        tool.main()
        report = json.loads(capsys.readouterr().out)
        assert report["dev_mean_macro_recall"] == 1.0
        assert report["sets"] == {"dev": {"top1": 1.0, "macro_recall": 1.0},
                                  "test": {"top1": 1.0, "macro_recall": 1.0}}
