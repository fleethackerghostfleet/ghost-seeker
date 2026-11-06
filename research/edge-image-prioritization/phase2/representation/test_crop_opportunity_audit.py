import unittest

from crop_opportunity_audit import clip_box, visible_geometry


class GeometryAuditTests(unittest.TestCase):
    def test_partial_crop_uses_retained_extent(self):
        fraction, side = visible_geometry([0, 0, 100, 100],
                                           {"roi": [20, 0, 80, 100], "payload_width": 40, "payload_height": 50})
        self.assertAlmostEqual(fraction, .8)
        self.assertAlmostEqual(side, 40)

    def test_visible_gt_clipping_not_hidden_offimage_pixels(self):
        clipped = clip_box([-20, -10, 100, 100], 100, 100)
        self.assertEqual(clipped, [0, 0, 80, 90])
        fraction, side = visible_geometry(clipped,
                                           {"roi": [0, 0, 100, 100], "payload_width": 50, "payload_height": 50})
        self.assertEqual(fraction, 1)
        self.assertEqual(side, 40)

    def test_outside_roi_has_no_detail(self):
        self.assertEqual(visible_geometry([0, 0, 10, 10],
                                         {"roi": [20, 20, 20, 20], "payload_width": 20, "payload_height": 20}), (0, 0))


if __name__ == "__main__":
    unittest.main()
