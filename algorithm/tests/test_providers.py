"""Provider interchangeability, strict boundaries, and cached-output isolation."""
import json
import sys
import types
import unittest
from unittest.mock import patch

import numpy as np

from ghost_priority.analysis import Analyzer
from ghost_priority.contracts import ObjectResult, VisualResult
from ghost_priority.providers import load_provider


class FakeProvider:
    def __init__(self, result, name='test-provider'):
        self.name, self.result, self.calls, self.shape = name, result, 0, None

    def analyze(self, bgr):
        self.calls += 1
        self.shape = bgr.shape
        return self.result


class ProviderTests(unittest.TestCase):
    def setUp(self):
        self.image = np.arange(80 * 120 * 3, dtype=np.uint8).reshape(80, 120, 3)

    def test_all_providers_are_replaceable_with_original_pixels(self):
        objects = FakeProvider(ObjectResult(relevance=0.0, detections=[
            {'label': 'boat', 'confidence': 0.9, 'bbox_xywh': [70, 40, 40, 30]}]), 'direct-zero')
        visual = FakeProvider(VisualResult(0.8, {'texture': 0.3}, [
            {'bbox_xywh': [10, 20, 30, 40], 'score': 0.5, 'kind': 'patch'}]), 'my-visual')
        quality = FakeProvider(np.float32(0.75), 'my-quality')
        result = Analyzer(width=16, object_provider=objects, visual_provider=visual,
                          quality_provider=quality).analyze(self.image)
        self.assertEqual([p.calls for p in (objects, visual, quality)], [1, 1, 1])
        self.assertTrue(all(p.shape == (80, 120, 3) for p in (objects, visual, quality)))
        self.assertEqual(result['object_relevance'], 0.0)
        self.assertEqual(result['detections'][0]['bbox_xywh'], [70, 40, 40, 30])
        self.assertEqual(result['signals']['visual_interest'], 0.8)
        self.assertEqual(result['signals']['novelty'], 0.8)
        self.assertEqual(result['signals']['quality'], 0.75)
        self.assertEqual(result['providers'], {'object': 'direct-zero', 'visual': 'my-visual', 'quality': 'my-quality'})
        json.dumps(result, allow_nan=False)

    def test_not_run_no_hits_and_direct_zero_remain_distinct(self):
        cases = [(ObjectResult(), None, None), (ObjectResult(detections=[]), None, []),
                 (ObjectResult(relevance=0), 0, None)]
        for provided, relevance, detections in cases:
            result = Analyzer(object_provider=FakeProvider(provided)).analyze(self.image)
            self.assertEqual(result['object_relevance'], relevance)
            self.assertEqual(result['detections'], detections)

    def test_model_path_and_custom_object_provider_conflict(self):
        with self.assertRaisesRegex(ValueError, 'mutually exclusive'):
            Analyzer('does-not-exist.onnx', object_provider=FakeProvider(ObjectResult()))

    def test_scores_are_rejected_instead_of_clipped(self):
        for value in (-0.1, 1.1, 10**1000, float('nan'), float('inf'), True, '0.5'):
            for kwargs in (
                {'object_provider': FakeProvider(ObjectResult(relevance=value))},
                {'visual_provider': FakeProvider(VisualResult(value))},
                {'quality_provider': FakeProvider(value)},
            ):
                with self.subTest(value=value, role=next(iter(kwargs))):
                    with self.assertRaisesRegex(ValueError, 'finite number'):
                        Analyzer(**kwargs).analyze(self.image)

    def test_wrong_result_types_fail_clearly(self):
        for kwargs, expected in [
            ({'object_provider': FakeProvider({'relevance': 0.5})}, 'ObjectResult'),
            ({'visual_provider': FakeProvider({'interest': 0.5})}, 'VisualResult'),
        ]:
            with self.assertRaisesRegex(ValueError, expected):
                Analyzer(**kwargs).analyze(self.image)

    def test_invalid_detections_and_original_pixel_bounds(self):
        good = {'label': 'boat', 'confidence': 0.7, 'bbox_xywh': [10, 10, 20, 20]}
        changes = [{'label': ''}, {'confidence': 2}, {'bbox_xywh': [-1, 0, 20, 20]},
                   {'bbox_xywh': [100, 10, 40, 20]}, {'bbox_xywh': [0, 0, 1, 81]},
                   {'bbox_xywh': [0, 0, 0, 1]}, {'bbox_xywh': [0, 0, float('nan'), 1]}]
        for change in changes:
            with self.subTest(change=change), self.assertRaises(ValueError):
                Analyzer(object_provider=FakeProvider(ObjectResult(detections=[{**good, **change}]))).analyze(self.image)

    def test_visual_components_cannot_override_canonical_signals(self):
        for key in ('visual_interest', 'novelty', 'quality', 'object_relevance'):
            with self.assertRaisesRegex(ValueError, 'reserved'):
                Analyzer(visual_provider=FakeProvider(VisualResult(0.5, {key: 0.9}))).analyze(self.image)
        with self.assertRaisesRegex(ValueError, 'finite number'):
            Analyzer(visual_provider=FakeProvider(VisualResult(0.5, {'bad': object()}))).analyze(self.image)

    def test_visual_regions_are_validated(self):
        with self.assertRaisesRegex(ValueError, 'original image'):
            Analyzer(visual_provider=FakeProvider(VisualResult(0.5, regions=[
                {'bbox_xywh': [0, 0, 121, 1]}]))).analyze(self.image)

    def test_cached_results_do_not_alias_provider_owned_lists(self):
        result = ObjectResult(detections=[{'label': 'boat', 'confidence': 0.7, 'bbox_xywh': [1, 2, 3, 4]}])
        provider = FakeProvider(result)
        cached = Analyzer(object_provider=provider).analyze(self.image)
        result.detections[0]['confidence'] = 0.1
        result.detections[0]['bbox_xywh'][0] = 20
        self.assertEqual(cached['detections'][0]['confidence'], 0.7)
        self.assertEqual(cached['detections'][0]['bbox_xywh'], [1, 2, 3, 4])

    def test_provider_identity_and_analyze_are_required(self):
        with self.assertRaisesRegex(ValueError, 'nonempty'):
            Analyzer(object_provider=FakeProvider(ObjectResult(), name=''))
        with self.assertRaisesRegex(ValueError, 'callable analyze'):
            Analyzer(object_provider=types.SimpleNamespace(name='broken'))

    def test_explicit_factory_loading_and_errors(self):
        module = types.ModuleType('test_example_provider')
        module.create = lambda: FakeProvider(ObjectResult(), 'loaded')
        module.bad = 3
        module.wrong_shape = lambda: object()
        module.requires_argument = lambda required: required
        with patch.dict(sys.modules, {'test_example_provider': module}):
            self.assertEqual(load_provider('test_example_provider:create').name, 'loaded')
            for spec in ('test_example_provider:missing', 'test_example_provider:bad',
                         'test_example_provider:wrong_shape', 'test_example_provider:requires_argument',
                         'test_example_provider', ':create', 'test_example_provider:create:more'):
                with self.subTest(spec=spec), self.assertRaises(ValueError):
                    load_provider(spec)

    def test_dataclass_collection_defaults_are_independent(self):
        first, second = VisualResult(0.2), VisualResult(0.3)
        first.components['a'] = 0.1
        first.regions.append({})
        self.assertEqual(second.components, {})
        self.assertEqual(second.regions, [])


if __name__ == '__main__':
    unittest.main()
