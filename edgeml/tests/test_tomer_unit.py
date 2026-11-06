"""Tomer wrapper contracts without algorithm imports, models or sockets."""
import builtins
import itertools
import sys
from types import ModuleType, SimpleNamespace
from unittest.mock import Mock, call

import pytest

from edgeml.sidecar import tomer


@pytest.fixture
def fake_backend(monkeypatch):
    def make(records, comparisons=None, engine_name="fake", fail=None, **kwargs):
        analyzer = SimpleNamespace(analyze=Mock(side_effect=[dict(record) for record in records]))
        analysis = SimpleNamespace(Analyzer=Mock(return_value=analyzer))
        policy_data = {"duplicate_strength": 0.85, "similarity_method": "policy-fallback"}
        policy = SimpleNamespace(
            load_policy=Mock(return_value=policy_data),
            score_record=Mock(side_effect=lambda rec, config: {"score": rec.get("base", 0.8)}),
        )
        engine = SimpleNamespace(
            prepare=Mock(side_effect=lambda rec: rec.get("desc", rec["id"])),
            compare=Mock(side_effect=lambda a, b: (comparisons or {}).get((a, b), {"score": 0.0})),
        )
        if engine_name is not None:
            engine.name = engine_name
        similarity = SimpleNamespace(engine_from_policy=Mock(return_value=engine))
        stages = {"analyzer": analysis.Analyzer, "policy": policy.load_policy, "similarity": similarity.engine_from_policy}
        if fail is not None:
            stages[fail[0]].side_effect = fail[1]
        monkeypatch.setattr(tomer, "import_ghost_priority", lambda: (analysis, policy, similarity))
        monkeypatch.setattr(tomer, "time", SimpleNamespace(perf_counter=Mock(side_effect=itertools.count(0, 0.125))))
        prioritizer = tomer.TomerPrioritizer(**kwargs)
        return SimpleNamespace(prioritizer=prioritizer, analyzer=analyzer, analysis=analysis,
                               policy=policy, config=policy_data, engine=engine, similarity=similarity)

    return make


def test_algorithm_discovery_environment_and_parent_search(monkeypatch, tmp_path):
    explicit = tmp_path / "configured-algorithm"
    monkeypatch.setenv("GHOST_PRIORITY_PATH", str(explicit))
    assert tomer.find_algorithm_dir() == explicit
    monkeypatch.delenv("GHOST_PRIORITY_PATH")
    monkeypatch.setattr(tomer, "__file__", str(tmp_path / "package" / "tomer.py"))
    assert tomer.find_algorithm_dir() is None
    policy = tmp_path / "algorithm" / "ghost_priority" / "policy.py"
    policy.parent.mkdir(parents=True)
    policy.write_text("# discovery marker only\n")
    assert tomer.find_algorithm_dir() == tmp_path / "algorithm"


def fake_ghost_priority():
    package = ModuleType("ghost_priority")
    package.analysis, package.policy, package.similarity = object(), object(), object()
    return package


def test_installed_algorithm_does_not_change_import_path(monkeypatch):
    package = fake_ghost_priority()
    monkeypatch.setitem(sys.modules, "ghost_priority", package)
    discover = Mock(side_effect=AssertionError("installed package must not need discovery"))
    monkeypatch.setattr(tomer, "find_algorithm_dir", discover)
    before = list(sys.path)
    assert tomer.import_ghost_priority() == (package.analysis, package.policy, package.similarity)
    assert sys.path == before
    discover.assert_not_called()


@pytest.mark.parametrize("found", [False, True])
def test_algorithm_import_fallback_and_actionable_error(monkeypatch, tmp_path, found):
    package = fake_ghost_priority()
    real_import = builtins.__import__
    attempts = []

    def import_fake(name, *args, **kwargs):
        if name == "ghost_priority":
            attempts.append(name)
            if len(attempts) == 1:
                raise ImportError("not installed")
            return package
        return real_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, "__import__", import_fake)
    monkeypatch.setattr(tomer, "find_algorithm_dir", lambda: tmp_path if found else None)
    monkeypatch.setattr(sys, "path", list(sys.path))
    before = list(sys.path)
    if found:
        assert tomer.import_ghost_priority() == (package.analysis, package.policy, package.similarity)
        assert sys.path == [str(tmp_path), *before]
        assert len(attempts) == 2
    else:
        with pytest.raises(ImportError, match="GHOST_PRIORITY_PATH") as error:
            tomer.import_ghost_priority()
        assert error.value.__suppress_context__
        assert sys.path == before


@pytest.mark.parametrize("engine_name,expected", [("fake", "fake"), (None, "policy-fallback")])
def test_constructor_forwards_configuration(fake_backend, engine_name, expected):
    fake = fake_backend([], engine_name=engine_name, model_path="model.onnx", policy_path="policy.json",
                        width=160, threads=2, window=3, dup_threshold=0.75)
    fake.analysis.Analyzer.assert_called_once_with("model.onnx", width=160, threads=2)
    fake.policy.load_policy.assert_called_once_with("policy.json")
    fake.similarity.engine_from_policy.assert_called_once_with(fake.config)
    assert fake.prioritizer.engine == f"ghost_priority/{expected}"
    assert fake.prioritizer.window == 3 and fake.prioritizer.dup_threshold == 0.75
    assert fake.prioritizer._hist == {} and fake.prioritizer._n == 0


@pytest.mark.parametrize("stage,error", [
    ("analyzer", ValueError("invalid analyzer configuration")),
    ("policy", OSError("unreadable policy")),
    ("similarity", ValueError("unsupported similarity method")),
])
def test_configuration_errors_propagate(fake_backend, stage, error):
    with pytest.raises(type(error)) as caught:
        fake_backend([], fail=(stage, error))
    assert caught.value is error


@pytest.mark.parametrize("record", [{}, {"signals": None}, {"signals": {}}])
def test_empty_record_and_source_defaults(fake_backend, record):
    fake = fake_backend([record])
    frame = object()
    result = fake.prioritizer.score("", frame)
    fake.analyzer.analyze.assert_called_once_with(frame)
    assert result == {"engine": "ghost_priority/?", "frameId": "frame#1", "visualInterest": 0.0,
                      "quality": 0.0, "objectRelevance": -1.0, "nearestPriorId": "", "maxSimilarity": 0.0,
                      "nearDuplicate": False, "score": 0.8, "baseScore": 0.8, "latencyMs": "125"}
    fake.engine.compare.assert_not_called()
    assert len(fake.prioritizer._hist[""]) == 1


def test_unusable_frame_does_not_score_or_enter_history(fake_backend):
    fake = fake_backend([{"usable": False, "engine": "cpu", "object_relevance": 0,
                          "signals": {"visual_interest": "0.2", "quality": "0.3"}}, {}])
    result = fake.prioritizer.score("cam", object())
    assert result["score"] == result["baseScore"] == 0.0
    assert result["visualInterest"] == 0.2 and result["quality"] == 0.3
    assert result["objectRelevance"] == 0.0 and result["engine"] == "ghost_priority/cpu"
    assert result["nearestPriorId"] == "" and result["nearDuplicate"] is False
    assert result["latencyMs"] == "125" and fake.prioritizer._hist == {}
    fake.policy.score_record.assert_not_called()
    fake.engine.prepare.assert_not_called()
    second = fake.prioritizer.score("cam", object())
    assert second["frameId"] == "cam#2" and second["nearDuplicate"] is False
    assert list(fake.prioritizer._hist["cam"]) == [("cam#2", "cam#2")]


@pytest.mark.parametrize("similarity", [0.0, 0.499999, 0.5, 0.500001, 1.0])
def test_single_prior_frame_and_inclusive_duplicate_threshold(fake_backend, similarity):
    fake = fake_backend([{"desc": "first"}, {"desc": "second"}], {("second", "first"): {"score": similarity}})
    prior = fake.prioritizer.score("cam", object())
    result = fake.prioritizer.score("cam", object())
    assert result["nearDuplicate"] is (similarity >= 0.5)
    assert result["maxSimilarity"] == similarity
    assert result["nearestPriorId"] == (prior["frameId"] if similarity > 0 else "")
    assert result["score"] == pytest.approx(0.8 * (1 - 0.85 * similarity))
    assert len(fake.prioritizer._hist["cam"]) == (1 if similarity >= 0.5 else 2)


@pytest.mark.parametrize("comparison,expected", [
    ({"available": False}, 0.0),
    ({"available": False, "score": 1.0}, 0.0),
    ({"score": -0.1}, 0.0),
    ({"score": 0.25}, 0.25),
])
def test_unavailable_and_nonpositive_comparisons(fake_backend, comparison, expected):
    fake = fake_backend([{"desc": "a"}, {"desc": "b"}], {("b", "a"): comparison})
    fake.prioritizer.score("cam", object())
    result = fake.prioritizer.score("cam", object())
    assert result["maxSimilarity"] == expected and result["nearDuplicate"] is False
    assert result["nearestPriorId"] == ("cam#1" if expected > 0 else "")


def test_similarity_ties_keep_earliest_prior(fake_backend):
    fake = fake_backend([{"desc": "a"}, {"desc": "b"}, {"desc": "c"}],
                        {("b", "a"): {"score": 0.1}, ("c", "a"): {"score": 0.4}, ("c", "b"): {"score": 0.4}})
    for _ in range(2):
        fake.prioritizer.score("cam", object())
    result = fake.prioritizer.score("cam", object())
    assert result["nearestPriorId"] == "cam#1" and result["maxSimilarity"] == 0.4
    assert result["nearDuplicate"] is False


@pytest.mark.parametrize("window", [0, 1, 2])
def test_bounded_history_eviction_and_zero_window(fake_backend, window):
    fake = fake_backend([{"desc": str(i)} for i in range(4)], window=window)
    for _ in range(4):
        fake.prioritizer.score("cam", object())
    assert list(fake.prioritizer._hist["cam"]) == [(f"cam#{i + 1}", str(i)) for i in range(4-window, 4)]
    expected = [call(str(i), str(j)) for i in range(4) for j in range(max(0, i-window), i)]
    assert fake.engine.compare.call_args_list == expected


def test_source_isolation_reset_and_duplicate_suppression(fake_backend):
    fake = fake_backend([{"desc": "a"}, {"desc": "b"}, {"desc": "a"}, {"desc": "a"}, {"desc": "b"}],
                        {("a", "a"): {"score": 0.9}, ("b", "b"): {"score": 0.9}})
    p = fake.prioritizer
    p.score("cam", object())
    other = p.score("other", object())
    assert other["nearestPriorId"] == "" and other["nearDuplicate"] is False
    assert p.score("cam", object())["nearDuplicate"] is True
    assert list(p._hist["cam"]) == [("cam#1", "a")]
    p.reset("missing")
    assert set(p._hist) == {"cam", "other"}
    p.reset("cam")
    assert set(p._hist) == {"other"}
    recovered = p.score("cam", object())
    assert recovered["frameId"] == "cam#4" and recovered["nearDuplicate"] is False
    p.reset()
    assert p._hist == {}
    recovered = p.score("other", object())
    assert recovered["frameId"] == "other#5" and recovered["nearDuplicate"] is False


@pytest.mark.parametrize("stage", ["analyze", "score", "prepare", "compare"])
def test_scoring_failures_preserve_kept_history_and_recover(fake_backend, stage):
    fake = fake_backend([{"desc": "a"}, {"desc": "b"}, {"desc": "c"}])
    p = fake.prioritizer
    p.score("cam", object())
    target = {"analyze": fake.analyzer.analyze, "score": fake.policy.score_record,
              "prepare": fake.engine.prepare, "compare": fake.engine.compare}[stage]
    previous = target.side_effect
    error = RuntimeError(f"{stage} failed")
    target.side_effect = error
    with pytest.raises(RuntimeError) as caught:
        p.score("cam", object())
    assert caught.value is error
    assert list(p._hist["cam"]) == [("cam#1", "a")]
    target.side_effect = previous
    result = p.score("cam", object())
    assert result["frameId"] == ("cam#2" if stage == "analyze" else "cam#3")
    assert result["nearDuplicate"] is False and len(p._hist["cam"]) == 2


@pytest.mark.parametrize("args,flag", [
    (["--prioritizer=invalid"], "--prioritizer"),
    (["--tomer-window=not-an-int"], "--tomer-window"),
    (["--tomer-dup-threshold=not-a-float"], "--tomer-dup-threshold"),
    (["--threads=not-an-int"], "--threads"),
])
def test_cli_rejects_invalid_argument_types(monkeypatch, capsys, args, flag):
    from edgeml.sidecar import __main__ as cli
    from edgeml.sidecar import server

    serve = Mock()
    monkeypatch.setattr(server, "serve", serve)
    with pytest.raises(SystemExit) as error:
        cli.main(args)
    assert error.value.code == 2 and flag in capsys.readouterr().err
    serve.assert_not_called()


def test_cli_tomer_configuration_uses_only_fakes(monkeypatch):
    from edgeml.sidecar import __main__ as cli
    from edgeml.sidecar import server, service

    engine, prioritizer = object(), object()
    noop = Mock(return_value=engine)
    constructor, serve = Mock(return_value=prioritizer), Mock()
    monkeypatch.setattr(service, "NoopEngine", noop)
    monkeypatch.setattr(tomer, "TomerPrioritizer", constructor)
    monkeypatch.setattr(server, "serve", serve)
    monkeypatch.setattr(cli.logging, "basicConfig", Mock())
    verify = Mock()
    monkeypatch.setattr(cli, "verify_models", verify)
    cli.main(["--prioritizer=tomer", "--tomer-model=model.onnx", "--tomer-policy=policy.json",
              "--model-manifest=manifest.json", "--threads=2", "--tomer-window=3", "--tomer-dup-threshold=0.75", "--listen=127.0.0.1:0"])
    verify.assert_called_once_with(["model.onnx", "policy.json"], "manifest.json")
    noop.assert_called_once_with()
    constructor.assert_called_once_with("model.onnx", "policy.json", threads=2, window=3, dup_threshold=0.75)
    serve.assert_called_once_with(engine, "127.0.0.1:0", quiet=True, prioritizer=prioritizer, mode="tomer")
