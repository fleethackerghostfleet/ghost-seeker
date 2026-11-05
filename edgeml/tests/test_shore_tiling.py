"""Exercise the same offset and NMS path as the shore tiled baseline."""
import numpy as np

from edgeml.detectors.tiled import TiledDetector
from edgeml.types import Detection


class MarkerDetector:
    name = "marker"

    def detect(self, image):
        ys, xs = np.where(image[:, :, 0] > 0)
        if not len(xs):
            return []
        return [Detection(box=[float(xs.min()), float(ys.min()), float(xs.max() - xs.min() + 1),
                               float(ys.max() - ys.min() + 1)], score=.8, label="boat")]


def test_tiles_translate_input_pixel_boxes_and_nms_keeps_small_contact():
    image = np.zeros((1080, 1920, 3), dtype=np.uint8)
    image[700:720, 1500:1520, 0] = 255
    det = TiledDetector(MarkerDetector(), tile=640, overlap=.2, include_full=True)
    ds = det.detect(image)
    assert len(ds) == 1
    assert ds[0].box == [1500., 700., 20., 20.]
    assert det.n_calls == 9


def test_tiles_cover_small_images_and_empty_frames():
    det = TiledDetector(MarkerDetector(), tile=640, overlap=.2, include_full=True)
    assert det.detect(np.zeros((100, 200, 3), dtype=np.uint8)) == []
    assert det.n_calls == 2
