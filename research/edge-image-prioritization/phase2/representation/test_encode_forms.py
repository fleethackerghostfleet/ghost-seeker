"""Boundary and artifact-level checks for representation geometry and accounting."""
import copy
import hashlib
import tempfile
import unittest
from pathlib import Path

import cv2
import numpy as np

from encode_forms import crop_candidates, encode_record, padded_roi


class RepresentationTests(unittest.TestCase):
    def test_border_crop_preserves_detection_and_clamps_context(self):
        self.assertEqual(padded_roi([0.2, 1.1, 4.4, 5.2], 20, 20), [0, 0, 6, 8])
        self.assertEqual(padded_roi([17, 17, 3, 3], 20, 20), [16, 16, 4, 4])

    def test_outside_and_nonfinite_prediction_rejected(self):
        for box in [[-1, 0, 5, 5], [18, 0, 5, 5], [0, 0, 0, 2], [0, 0, float("nan"), 2]]:
            with self.assertRaises(ValueError):
                padded_roi(box, 20, 20)

    def test_unfiltered_classes_and_stable_top_two(self):
        dets = [{"label": label, "confidence": confidence, "bbox_xywh": [1, 1, 4, 4]}
                for label, confidence in [("person", .7), ("boat", .7), ("truck", .4), ("boat", .299)]]
        selected = crop_candidates({"width": 20, "height": 20, "detections": dets})
        self.assertEqual([x[0] for x in selected], [0, 1])

    def test_pixels_hash_bytes_geometry_and_evidence_preserved(self):
        # Small images must not be enlarged. A bright source rectangle allows
        # checking coordinate conversion against decoded pixels, not metadata alone.
        bgr = np.zeros((60, 100, 3), np.uint8)
        bgr[10:30, 20:60] = (220, 220, 220)
        record = {"id": "scene:000001", "width": 100, "height": 60,
                  "detections": [{"label": "boat", "confidence": .8, "bbox_xywh": [20, 10, 40, 20]}],
                  "signals": {"quality": .5}, "timestamp_s": 0}
        original = copy.deepcopy(record)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            encoded = encode_record(record, bgr, root)
            self.assertEqual(record, original)
            self.assertEqual(encoded["signals"], record["signals"])
            self.assertEqual(len(encoded["forms"]), 5)
            for form in encoded["forms"]:
                raw = (root / form["payload_path"]).read_bytes()
                self.assertEqual(form["bytes"], len(raw))
                self.assertEqual(form["sha256"], hashlib.sha256(raw).hexdigest())
                pixels = cv2.imdecode(np.frombuffer(raw, np.uint8), cv2.IMREAD_COLOR)
                self.assertEqual(pixels.shape[:2], (form["payload_height"], form["payload_width"]))
                self.assertLessEqual(form["payload_width"], form["roi"][2])
                self.assertLessEqual(form["payload_height"], form["roi"][3])
            crop = encoded["forms"][-1]
            self.assertEqual(crop["roi"], [10, 5, 60, 30])
            pixels = cv2.imread(str(root / crop["payload_path"]))
            self.assertGreater(float(pixels[10:20, 20:40].mean()), 200)
            self.assertLess(float(pixels[:3, :].mean()), 20)

    def test_missing_detections_keeps_context_forms(self):
        bgr = np.zeros((12, 20, 3), np.uint8)
        with tempfile.TemporaryDirectory() as tmp:
            result = encode_record({"id": "none", "width": 20, "height": 12, "detections": []}, bgr, Path(tmp))
            self.assertEqual(len(result["forms"]), 4)
            self.assertTrue(all(f["kind"] != "crop" for f in result["forms"]))


if __name__ == "__main__":
    unittest.main()
