import json
import unittest
from unittest.mock import Mock

import numpy as np
import cv2

from ghost_priority.analysis import Analyzer


class AnalyzerTests(unittest.TestCase):
    def setUp(self):
        self.analyzer = Analyzer(width=160, threads=1)

    def test_flat_images_have_no_visual_novelty(self):
        for value in (0, 100, 255):
            result = self.analyzer.analyze(np.full((80, 160, 3), value, np.uint8))
            self.assertLess(result['signals']['novelty'], 1e-5)
            self.assertEqual(result['regions'], [])
            self.assertIsNone(result['detections'])
            self.assertFalse(result['usable'])
            self.assertEqual(result['unusable_reason'], 'constant_image')

    def test_tiny_images_are_finite_and_json_serializable(self):
        for shape in ((1, 1, 3), (1, 5, 3), (5, 1, 3), (2, 2, 3)):
            image = np.arange(np.prod(shape), dtype=np.uint8).reshape(shape)
            result = self.analyzer.analyze(image)
            json.dumps(result, allow_nan=False)
            self.assertEqual(len(result['fingerprint']), 192)
            self.assertTrue(all(0 <= s <= 1 for s in result['signals'].values()))

    def test_visible_patch_scores_above_flat_image(self):
        image = np.full((200, 400, 3), 70, np.uint8)
        blank = self.analyzer.analyze(image)
        image[70:100, 190:230] = 240
        result = self.analyzer.analyze(image)
        self.assertGreater(result['signals']['novelty'], blank['signals']['novelty'] + 0.1)
        self.assertGreater(len(result['regions']), 0)
        for region in result['regions']:
            x, y, w, h = region['bbox_xywh']
            self.assertGreater(w, 0)
            self.assertGreater(h, 0)
            self.assertGreaterEqual(x, 0)
            self.assertGreaterEqual(y, 0)
            self.assertLessEqual(x + w, 400)
            self.assertLessEqual(y + h, 200)

    def test_sequential_opencv_mode(self):
        Analyzer(threads=0)
        self.assertEqual(cv2.getNumThreads(), 1)

    def test_low_contrast_pattern_does_not_get_normalized_to_high_novelty(self):
        image = np.full((120, 160, 3), 100, np.uint8)
        image[50:60, 75:85] = 101
        result = self.analyzer.analyze(image)
        self.assertLess(result['signals']['novelty'], 0.05)
        self.assertTrue(result['usable'])

    def test_constancy_check_preserves_original_pixel_detail(self):
        image = np.full((1000, 1000, 3), 100, np.uint8)
        image[500, 500] = 101
        result = self.analyzer.analyze(image)
        self.assertTrue(result['usable'])
        self.assertIsNone(result['unusable_reason'])

    def test_fingerprint_preserves_absolute_color(self):
        dark = self.analyzer.analyze(np.full((20, 20, 3), 10, np.uint8))
        bright = self.analyzer.analyze(np.full((20, 20, 3), 240, np.uint8))
        self.assertGreater(np.mean(bright['fingerprint']) - np.mean(dark['fingerprint']), 0.8)

    def test_analysis_is_stateless_for_unrelated_stills(self):
        a = np.full((60, 100, 3), 80, np.uint8)
        b = np.full((60, 100, 3), 230, np.uint8)
        first = self.analyzer.analyze(a)
        self.analyzer.analyze(b)
        second = self.analyzer.analyze(a)
        first.pop('timing_ms')
        second.pop('timing_ms')
        self.assertEqual(first, second)

    def test_invalid_inputs_fail_explicitly(self):
        for image in (None, np.zeros((10, 10), np.uint8), np.zeros((0, 10, 3), np.uint8), np.zeros((10, 10, 3), np.float32)):
            with self.assertRaises(ValueError):
                self.analyzer.analyze(image)

    def test_annotation_argument_is_not_accepted(self):
        with self.assertRaises(TypeError):
            self.analyzer.analyze(np.zeros((10, 10, 3), np.uint8), annotations={'boat': True})

    def test_letterbox_coordinates_and_empty_vs_unevaluated(self):
        self.analyzer.detector = Mock()
        # A 400x200 image is centered at y=104 in the 416x416 letterbox.
        self.analyzer.detector.infer.return_value = np.array([[104, 156, 312, 260, 0.9, 8]])
        result = self.analyzer.analyze(np.zeros((200, 400, 3), np.uint8))
        detection = result['detections'][0]
        self.assertEqual(detection['label'], 'boat')
        self.assertEqual(detection['bbox_xywh'], [100.0, 50.0, 200.0, 100.0])
        self.analyzer.detector.infer.return_value = np.empty((0, 6))
        self.assertEqual(self.analyzer.analyze(np.zeros((2, 2, 3), np.uint8))['detections'], [])

    def test_nanodet_pairs_outputs_independently_of_engine_order(self):
        from ghost_priority.third_party.nanodet import NanoDet
        detector = NanoDet.__new__(NanoDet)
        detector.pre_process = Mock(return_value=np.zeros((1, 3, 416, 416), np.float32))
        detector.net = Mock()
        detector.post_process = Mock(return_value=np.empty((0, 6)))
        scores = [np.zeros((1, n, 80), np.float32) for n in (2704, 676, 169)]
        boxes = [np.zeros((1, n, 32), np.float32) for n in (2704, 676, 169)]
        for outputs in (scores + boxes, [x for pair in zip(scores, boxes) for x in pair]):
            detector.net.forward.return_value = outputs
            detector.infer(np.zeros((416, 416, 3), np.uint8))
            paired = detector.post_process.call_args.args[0]
            self.assertEqual([x.shape for x in paired], [(1, n, c) for n in (2704, 676, 169) for c in (80, 32)])


if __name__ == '__main__':
    unittest.main()
