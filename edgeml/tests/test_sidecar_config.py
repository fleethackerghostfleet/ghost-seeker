"""CLI configuration without starting servers, pinning CPUs or loading weights."""
from unittest.mock import Mock

import pytest

from edgeml.runtime import threads
from edgeml.sidecar import __main__ as cli, server, service, tomer


@pytest.fixture
def launch(monkeypatch):
    serve, engine, prioritizer, pin = Mock(), Mock(), Mock(), Mock()
    monkeypatch.setattr(server, "serve", serve)
    monkeypatch.setattr(service, "EdgemlEngine", engine)
    monkeypatch.setattr(tomer, "TomerPrioritizer", prioritizer)
    monkeypatch.setattr(threads, "pin_threads", pin)
    monkeypatch.setattr(cli.logging, "basicConfig", Mock())
    return serve, engine, prioritizer, pin


def test_default_detector_environment_then_existing_home_export(monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.delenv("EDGEML_DETECTOR", raising=False)
    assert cli.default_detector() is None
    export = tmp_path / "models/D/yolo11n-640/model.onnx"
    export.parent.mkdir(parents=True)
    export.touch()
    assert cli.default_detector() == str(export)
    monkeypatch.setenv("EDGEML_DETECTOR", "~/override.onnx")
    assert cli.default_detector() == "~/override.onnx"


def test_missing_detector_is_usage_error_without_startup(monkeypatch, tmp_path, launch, capsys):
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.delenv("EDGEML_DETECTOR", raising=False)
    with pytest.raises(SystemExit) as error:
        cli.main([])
    assert error.value.code == 2
    assert "--detector is required" in capsys.readouterr().err
    for dependency in launch:
        dependency.assert_not_called()


def test_mock_startup_does_not_consult_models_or_pin_threads(launch):
    serve, engine, prioritizer, pin = launch
    cli.main(["--mock", "--listen", "127.0.0.1:0"])
    args, kw = serve.call_args
    assert isinstance(args[0], service.MockEngine)
    assert args[1] == "127.0.0.1:0"
    assert kw == {"quiet": True, "prioritizer": None, "mode": "edgeml"}
    engine.assert_not_called()
    prioritizer.assert_not_called()
    pin.assert_not_called()


@pytest.mark.parametrize('options, enabled', [([], False), (['--marine-mammal', 'on'], True),
                                            (['--mm', '{"enabled":true}'], True),
                                            (['--marine-mammal', 'on', '--mm', '{"enabled":false}'], False)])
def test_marine_mammal_is_opt_in(launch, options, enabled):
    _, engine, _, _ = launch
    cli.main(['--detector', 'fixture.onnx', *options])
    assert engine.call_args.kwargs['marine_mammal'] == {'enabled': enabled}


def test_tomer_only_startup_forwards_options_without_detector(launch):
    serve, engine, prioritizer, pin = launch
    cli.main(["--prioritizer", "tomer", "--threads", "2", "--tomer-window", "5",
              "--tomer-dup-threshold", "0.7", "--tomer-policy", "policy.json", "-v"])
    assert isinstance(serve.call_args.args[0], service.NoopEngine)
    prioritizer.assert_called_once_with(None, "policy.json", threads=2, window=5, dup_threshold=0.7)
    assert serve.call_args.kwargs == {"quiet": False, "prioritizer": prioritizer.return_value, "mode": "tomer"}
    engine.assert_not_called()
    pin.assert_not_called()


@pytest.mark.parametrize("anomaly,expected", [([], None), (["--anomaly-fallback"], {}),
                                                    (["--anomaly-fallback", '{"z_thr":3.5}'], {"z_thr": 3.5})])
def test_real_engine_configuration_parses_overrides(monkeypatch, launch, anomaly, expected):
    serve, engine, prioritizer, pin = launch
    monkeypatch.setenv("EDGEML_DETECTOR", "env.onnx")
    cli.main(["--embed", "none", "--fps", "8", "--threads", "2", "--engine", "ort", "--tracker", "ocsort",
              "--asso", "iou", "--max-lost-s", "3", "--score-thr", "0.4", "--det-low-thr", "0.02",
              "--agg", '{"min_hits":3}', "--trk", '{"tentative_thresh":null}', "--text-tower", "torch",
              "--prioritizer", "both", *anomaly])
    engine.assert_called_once_with("env.onnx", "none", threads=2, fps=8.0, engine="ort", tracker="ocsort", asso="iou",
                                   max_lost_s=3.0, score_thr=0.4, det_low_thr=0.02, agg={"min_hits": 3},
                                   trk={"tentative_thresh": None}, text_tower="torch", anomaly_fallback=expected, live_tiling={},
                                   marine_mammal={"enabled": False})
    pin.assert_called_once_with(2)
    assert serve.call_args.args[0] is engine.return_value
    assert serve.call_args.kwargs["prioritizer"] is prioritizer.return_value
    assert serve.call_args.kwargs["mode"] == "both"


def test_explicit_detector_alias_wins_over_environment(monkeypatch, launch):
    monkeypatch.setenv("EDGEML_DETECTOR", "env.onnx")
    cli.main(["--det", "explicit.xml"])
    assert launch[1].call_args.args[0] == "explicit.xml"


def test_bad_json_override_does_not_construct_or_serve_engine(launch):
    with pytest.raises(ValueError):
        cli.main(["--det", "fixture.onnx", "--agg", "{"])
    launch[0].assert_not_called()
    launch[1].assert_not_called()
