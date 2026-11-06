from types import SimpleNamespace

import numpy as np
import pytest

from edgeml.detectors.marine_mammal import (PROMPTS, MarineMammalConfig, MarineMammalDetection,
                                         MarineMammalFusionDetector, MarineMammalVerifier)
from edgeml.sidecar.marine_mammal import MarineTracker, make_aggregator
from edgeml.sidecar.service import EdgemlEngine, _make_zeroshot_aggregator_class, _wire_subtype
from edgeml.tracking import AggregatorParams
from edgeml.types import Detection, Track


class Embedder:
    dim = len(PROMPTS)
    short = 'mobileclip-s1'

    def __init__(self, positive=True):
        self.positive, self.calls = positive, []

    def embed_text(self, prompts):
        return np.asarray([np.eye(self.dim)[i] for i, group in enumerate(PROMPTS.values()) for _ in group])

    def embed_crops(self, crops):
        self.calls.append(len(crops))
        return np.tile(np.eye(self.dim)[3 if self.positive else 4], (len(crops), 1))

    def logit_scale(self):
        return 20


IMAGE = np.zeros((60, 90, 3), np.uint8)


def fusion(detections=(), positive=True, **kwargs):
    embedder = Embedder(positive)
    base = SimpleNamespace(name='fake', detect=lambda image: list(detections))
    return MarineMammalFusionDetector(base, MarineMammalVerifier(embedder), MarineMammalConfig(**kwargs)), embedder


def test_verifier_batch_and_empty():
    embedder = Embedder()
    verifier = MarineMammalVerifier(embedder)
    results = verifier.score([IMAGE, IMAGE])
    assert embedder.calls == [2]
    assert results[0]['p'] > 0.99 and results[0]['subtype'] == 'dolphin'
    assert verifier.score([]) == []


def test_crop_relabel_and_non_candidates():
    detector, embedder = fusion([Detection([0, 0, 20, 20], .8, 'boat'), Detection([30, 0, 10, 10], .9, 'airplane')])
    result = detector.detect(IMAGE)
    assert result[0].label == 'marine mammal' and result[0].subtype == 'dolphin'
    assert result[1].label == 'airplane'
    assert embedder.calls == [1]


def test_verifier_consumes_live_tiled_results_and_scans_without_usable_detections():
    from edgeml.detectors.live_tiled import LiveTiledDetector

    base = SimpleNamespace(name='fake', detect=lambda image: [Detection([0, 30, 20, 20], .8, 'person')])
    tiled = LiveTiledDetector(base, horizon_fn=lambda image: 10)
    assert tiled.detect(IMAGE)[0].label == 'person in water'
    embedder = Embedder()
    detector = MarineMammalFusionDetector(tiled, MarineMammalVerifier(embedder))
    assert detector.detect(IMAGE)[0].label == 'marine mammal'
    assert embedder.calls == [1]
    base.detect = lambda image: [Detection([0, 0, 10, 10], .1, 'airplane')]
    detector.frame = 0
    result = detector.detect(IMAGE)
    assert result[-1].label == 'marine mammal'
    assert embedder.calls[-1] == 7


@pytest.mark.parametrize('positive', [True, False])
def test_open_water_and_stride(positive):
    detector, embedder = fusion(positive=positive, scan_stride=2)
    assert len(detector.detect(IMAGE)) == int(positive)
    assert detector.detect(IMAGE) == []
    assert len(detector.detect(IMAGE)) == int(positive)
    assert embedder.calls == [7, 7]


def test_budget_disabled_and_no_crop_budget():
    detector, embedder = fusion([Detection([0, 0, 10, 10], .1, 'bird')] * 5, max_crops=3)
    detector.detect(IMAGE)
    assert embedder.calls == [3]
    detector, embedder = fusion(enabled=False)
    assert detector.detect(IMAGE) == [] and embedder.calls == []
    detector, embedder = fusion(top_k=0, max_crops=0)
    assert detector.detect(IMAGE) == [] and embedder.calls == []


def test_bundle_fallback_and_mismatch():
    embedder = SimpleNamespace(short='mobileclip-s1', dim=512, logit_scale=lambda: 100,
                              embed_text=lambda prompts: (_ for _ in ()).throw(RuntimeError('cache miss')))
    verifier = MarineMammalVerifier(embedder)
    assert verifier.prototypes.shape == (len(PROMPTS), 512)
    embedder.dim = 3
    with pytest.raises(ValueError, match='mismatch'):
        MarineMammalVerifier(embedder)


def test_detection_tracker_aggregator_wire():
    detector, _ = fusion([Detection([0, 0, 30, 30], .9, 'boat')])
    detections = detector.detect(IMAGE)
    tracker = MarineTracker(SimpleNamespace(update=lambda dets, image, frame, t: [
        Track(1, dets[0].box, dets[0].score, dets[0].label, frame, t, extra={'det_ind': 0})]))
    classifier = SimpleNamespace(classify=lambda embedding: [{'subtype': 'cargo ship', 'unknownness': .5}])
    agg = make_aggregator(_make_zeroshot_aggregator_class(), 'cam', AggregatorParams(min_hits=1), embedder=Embedder(), classifier=classifier, coarse={})
    events = agg.update(tracker.update(detections, IMAGE, 1, 0), IMAGE, 1, 0)
    assert len(events) == 1
    engine = object.__new__(EdgemlEngine)
    wire = engine._sighting(events[0], 1, 90, 60, 0)
    assert wire['keyDetection']['label'] == 'marine mammal'
    assert wire['subtype'] == 'dolphin'
    assert _wire_subtype({'label': 'marine mammal', 'subtype': 'blow', 'tentative': True}) == 'blow'


@pytest.mark.parametrize('options', [{'scan_stride': 0}, {'rows': 0}, {'max_crops': -1}, {'scan_thr': 2}])
def test_invalid_config(options):
    with pytest.raises(ValueError):
        MarineMammalConfig(**options)


def test_eval_metrics_and_dev_threshold():
    import importlib.util
    from pathlib import Path
    spec = importlib.util.spec_from_file_location('mm_eval', Path(__file__).resolve().parents[1] / 'tools/mm_eval.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    rows = [{'split': 'dev', 'label_pos': True, 'score': .9},
            {'split': 'dev', 'label_pos': False, 'score': .8},
            {'split': 'test', 'label_pos': False, 'score': .99}]
    threshold, result = module.pick_threshold(rows, 0)
    assert threshold == .9 and result['f1'] == 1 and result['fp_per_1000'] == 0
    assert module.metrics([], .5)['f1'] == 0
    with pytest.raises(ValueError):
        module.pick_threshold([], 0)


def test_engine_live_frame_and_sighting(monkeypatch, tmp_path):
    import json
    from edgeml.embed import clip_embedder, zeroshot
    from edgeml.runtime import detectors
    from edgeml.sidecar import service
    import edgeml.tracking as tracking

    (tmp_path / 'meta.json').write_text(json.dumps({'model': 'mobileclip-s1'}))
    clip = Embedder()
    classifier = SimpleNamespace(classes=['cargo ship'], calibration_path=None,
                                 classify=lambda emb: [{'subtype': 'cargo ship', 'unknownness': .5}])
    monkeypatch.setattr(clip_embedder, 'ClipEmbedder', lambda *a, **kw: clip)
    monkeypatch.setattr(zeroshot, 'load_classifier', lambda *a, **kw: classifier)
    monkeypatch.setattr(detectors, 'load_detector', lambda *a, **kw: SimpleNamespace(
        name='fake', detect=lambda image: [Detection([0, 0, 30, 30], .9, 'boat')]))
    monkeypatch.setattr(tracking, 'make_tracker', lambda *a, **kw: SimpleNamespace(
        update=lambda dets, image, frame, t: [Track(1, dets[0].box, dets[0].score, dets[0].label, frame, t, extra={'det_ind': 0})]))
    monkeypatch.setattr(service, 'frame_report', lambda *a, **kw: None)
    engine = EdgemlEngine('fake', tmp_path, agg={'min_hits': 1})
    response = engine.analyze('cam', IMAGE, 1000, [], False)
    assert response['detections'][0]['label'] == 'marine mammal'
    assert response['sightings'][0]['keyDetection']['label'] == 'marine mammal'
    assert response['sightings'][0]['subtype'] == 'dolphin'
