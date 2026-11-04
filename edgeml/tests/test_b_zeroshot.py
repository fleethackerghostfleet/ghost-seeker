"""Workstream B: label mapping, class-agnostic matching/summary, horizon-band tiling (no model weights needed)."""
import numpy as np

from edgeml.data.catalog import EvalImage
from edgeml.detectors.labels import agnostic, is_vessel_gt
from edgeml.detectors.tiled import HorizonBandDetector
from edgeml.eval.detection import AREA_BUCKETS, match_all, summarize
from edgeml.types import Detection


def _img(image_id, boxes, labels):
    return EvalImage(image_id=image_id, dataset_id="t", gcs_uri="", local_path="", width=100, height=100,
                     split="dev", source_ref=image_id, boxes=boxes, labels=labels)


def test_label_mapping():
    assert agnostic("boat") == "vessel"
    assert agnostic("Vessel/ship") == "vessel"
    assert agnostic("cargo ship") == "vessel"
    assert agnostic("person") == "object"
    assert agnostic("sky") is None
    assert is_vessel_gt("ore carrier") and is_vessel_gt("Speed boat") and not is_vessel_gt("Buoy")


def test_match_and_summarize_counts_tp_fp_fn_and_sea_only_frames():
    images = [_img("a", [[10, 10, 20, 20]], ["boat"]), _img("b", [], [])]  # b = sea-only frame
    dets = {
        "a": [Detection(box=[11, 11, 20, 20], score=0.9, label="boat"), Detection(box=[60, 60, 10, 10], score=0.3, label="boat")],
        "b": [Detection(box=[50, 50, 10, 10], score=0.6, label="boat")],
    }
    m = match_all(images, dets, mode="vessel")
    hi = summarize(images, m, tau=0.5)
    assert hi["tp"] == 1 and hi["fp"] == 1 and hi["n_gt"] == 1
    assert hi["recall"] == 1.0 and hi["precision"] == 0.5
    assert hi["n_sea_only"] == 1 and hi["fp_per_sea_only_frame"] == 1.0
    lo = summarize(images, m, tau=0.2)
    assert lo["fp"] == 2 and lo["fp_per_image"] == 1.0
    assert hi["recall_by_bucket"]["small"] == 1.0 and hi["n_gt_by_bucket"]["small"] == 1
    assert list(AREA_BUCKETS) == ["tiny", "small", "medium", "large"]


class _Null:
    name = "null"

    def detect(self, img):
        return []


def test_horizon_band_uses_horizon_and_merges_nothing_on_empty():
    det = HorizonBandDetector(_Null(), band_frac=0.2, n_tiles=3, overlap=0.2, horizon_fn=lambda img: 540)
    img = np.zeros((1080, 1920, 3), np.uint8)
    assert det.detect(img) == []
    assert det.last_horizon_y == 540
    assert len(list(det.crops(img))) == 4  # full frame + 3 band tiles
    assert "hband20x3" in det.name
