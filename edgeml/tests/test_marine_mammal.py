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
    return MarineMammalFusionDetector(base, MarineMammalVerifier(embedder), MarineMammalConfig(**{'enabled': True, **kwargs})), embedder


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
    detector = MarineMammalFusionDetector(tiled, MarineMammalVerifier(embedder), MarineMammalConfig(enabled=True))
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
    for subtype in ('whale', 'blow', 'fluke', 'dolphin'):
        assert _wire_subtype({'label': 'marine mammal', 'subtype': subtype, 'tentative': True}) == 'tentative'
        assert _wire_subtype({'label': 'marine mammal', 'subtype': subtype, 'reason': 'confirmed'}) == subtype
        assert _wire_subtype({'label': 'marine mammal', 'subtype': subtype, 'event': 'update'}) == subtype


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


def test_joint_live_calibration():
    import importlib.util
    from pathlib import Path
    spec = importlib.util.spec_from_file_location('mm_calibrate', Path(__file__).resolve().parents[1] / 'tools/mm_calibrate.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    rows = [dict(label=1, source='public:pos', subtype='whale', best_crop_p=.9, best_scan_p=0),
            dict(label=1, source='v3_mm:clip', subtype='dolphin', best_crop_p=0, best_scan_p=.85),
            dict(label=0, source='v3_neg:sea', subtype='none', best_crop_p=.8, best_scan_p=.8),
            dict(label=0, source='public:hardneg', subtype='none', best_crop_p=.88, best_scan_p=0)]
    frozen = module.calibrate(rows, target=0)
    result = module.summarize(rows, frozen['crop_thr'], frozen['scan_thr'])
    assert frozen['feasible']
    assert result['all']['f1'] == 1
    assert result['v3_neg']['fp_per_1000'] == 0
    assert result['subtype:dolphin']['recall'] == 1
    assert module.measure([], module.predictions([], .9, .9))['f1'] == 0
    row = dict(label=1, source='public:pos', subtype='dolphin', best_crop_p=0, best_scan_p=.99,
               scores=[dict(kind='scan', p=.99, subtype='whale'), dict(kind='scan', p=.95, subtype='dolphin')])
    result = module.summarize([row], .9, .9)['subtype:dolphin']
    assert result['recall'] == 1 and result['correct_subtype_tp'] == 0
    row['scores'].append(dict(kind='crop', p=.96, subtype='dolphin'))
    assert module.summarize([row], .9, .9)['subtype:dolphin']['correct_subtype_tp'] == 1
    with pytest.raises(ValueError):
        module.calibrate([])


@pytest.mark.parametrize('readable', [True, False])
def test_live_eval_uses_engine_stack(monkeypatch, tmp_path, readable):
    import importlib.util
    import json
    from pathlib import Path
    import sys
    import edgeml.sidecar.service as service

    spec = importlib.util.spec_from_file_location('mm_eval', Path(__file__).resolve().parents[1] / 'tools/mm_eval.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    detector = SimpleNamespace(frame=99, base=SimpleNamespace(n_calls=2),
                               last_scores=dict(best_crop_p=.97, best_scan_p=0., crops=1, mm_ms=.1, scores=[], subtype='whale'))

    def detect(image):
        assert detector.frame == 0
        return [MarineMammalDetection([0, 0, 10, 10], .97, 'marine mammal', subtype='whale')]

    detector.detect = detect
    monkeypatch.setattr(service, 'EdgemlEngine', lambda *a, **kw: SimpleNamespace(detector=detector, score_thr=.35, open_vocab=None))
    monkeypatch.setattr(module.cv2, 'imread', lambda path: IMAGE if readable else None)
    source, output = tmp_path / 'input.jsonl', tmp_path / 'scores.jsonl'
    source.write_text(json.dumps(dict(path='frame.jpg', label=1, source='public:pos', subtype='dolphin')) + '\n')
    monkeypatch.setattr(sys, 'argv', ['mm_eval', '--live', '--input', str(source), '--out', str(output)])
    if not readable:
        with pytest.raises(ValueError, match='unreadable image'):
            module.main()
    else:
        module.main()
        row = json.loads(output.read_text())
        assert row['emitted'] and row['tiles'] == 1 and row['best_crop_p'] == .97
        assert row['subtype'] == 'dolphin' and row['predicted_subtype'] == 'whale'


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
        update=lambda dets, image, frame, t: [Track(1, d.box, d.score, d.label, frame, t, extra={'det_ind': 0}) for d in dets[:1]]))
    monkeypatch.setattr(service, 'frame_report', lambda *a, **kw: None)
    default_engine = EdgemlEngine('fake', tmp_path, agg={'min_hits': 1})
    assert not isinstance(default_engine.detector, MarineMammalFusionDetector)
    assert default_engine.detector.detect(IMAGE)[0].label == 'boat'
    assert clip.calls == []
    assert not MarineMammalConfig().enabled
    engine = EdgemlEngine('fake', tmp_path, agg={'min_hits': 1}, marine_mammal={'enabled': True}, thermal_fallback=True)

    def open_detect(image, detections, labels):
        assert labels == ['bird']
        assert detections[0].label == 'marine mammal'
        return [dict(label='bird', confidence=.91, box=dict(x=0., y=0., w=.5, h=.5))]

    engine.open_vocab = SimpleNamespace(detect=open_detect)
    response = engine.analyze('cam', IMAGE, 1000, ['open-vocab:bird'], False)
    assert response['detections'][0]['label'] == 'marine mammal'
    assert response['sightings'][0]['keyDetection']['label'] == 'marine mammal'
    assert response['sightings'][0]['subtype'] == 'dolphin'
    assert response['detections'][-1]['label'] == 'bird'
    assert response['detections'][-1]['confidence'] == .91
    marine = engine.detector.base
    marine.base.base.detect = lambda image: []
    marine.cfg.scan_stride = 2
    for frame in range(1, 4):
        for source in ('camera-a', 'camera-b'):
            response = engine.analyze(source, IMAGE, frame * 1000, [], False)
            assert bool(response['detections']) == (frame % 2 == 1)
