"""Task H: calibration artifact round-trip / auto-load, and the sea-anomaly proposer + fusion (no model weights)."""
import numpy as np

from edgeml.detectors.anomaly import AnomalyFusionDetector, SeaAnomalyProposer
from edgeml.embed.zeroshot import CALIB_FILE, SUBTYPES, UNKNOWN_PROMPTS, ZeroShotClassifier, load_classifier
from edgeml.types import Detection


class _FakeEmbedder:
    dim = 16

    def embed_text(self, texts):
        rng = np.random.default_rng(abs(hash(tuple(texts))) % (2 ** 32))
        v = rng.normal(size=(len(texts), self.dim)).astype(np.float32)
        return v / np.linalg.norm(v, axis=1, keepdims=True)

    def logit_scale(self):
        return 100.0


def _unit(rng, n):
    v = rng.normal(size=(n, 16)).astype(np.float32)
    return v / np.linalg.norm(v, axis=1, keepdims=True)


def test_calibration_roundtrip_and_autoload(tmp_path):
    rng = np.random.default_rng(0)
    clf = ZeroShotClassifier(_FakeEmbedder())
    known = _unit(rng, 40)
    clf.calibrate(known)
    clf.fit_bank(known, k=7)
    clf.w_knn = 0.75
    clf.save_calibration(tmp_path / CALIB_FILE, {"split": "dev"})
    probe = _unit(rng, 5)
    want = [r["unknownness"] for r in clf.classify(probe)]

    loaded = load_classifier(_FakeEmbedder(), embed_dir=tmp_path)
    assert loaded.calibration_meta == {"split": "dev"} and loaded.calibration_path.endswith(CALIB_FILE)
    assert loaded.knn_k == 7 and loaded.w_knn == 0.75 and loaded.bank.shape == (40, 16)
    assert np.allclose([r["unknownness"] for r in loaded.classify(probe)], want)
    # bank members are close to themselves -> low kNN unknownness; far random vectors -> higher
    bank_u = np.mean([r["knn"] for r in loaded.classify(known[:10])])
    far_u = np.mean([r["knn"] for r in loaded.classify(_unit(rng, 10))])
    assert bank_u < far_u

    plain = load_classifier(_FakeEmbedder(), embed_dir=tmp_path / "missing")
    assert plain.bank is None and plain.calibration_path is None and plain.sim_ref == 0.30
    assert list(plain.classes) == list(SUBTYPES) and len(UNKNOWN_PROMPTS) >= 1


def _sea_frame(h=240, w=320, blob=None, seed=1):
    rng = np.random.default_rng(seed)
    img = np.zeros((h, w, 3), np.uint8)
    img[: h // 3] = (230, 200, 170)                                   # sky
    sea = np.stack([rng.normal(140, 6, (h - h // 3, w)), rng.normal(110, 6, (h - h // 3, w)), rng.normal(60, 6, (h - h // 3, w))], -1)
    img[h // 3:] = np.clip(sea, 0, 255).astype(np.uint8)
    if blob is not None:
        x, y, bw, bh = blob
        img[y:y + bh, x:x + bw] = (30, 30, 30)
    return img


def test_anomaly_proposer_finds_dark_blob_and_fusion_suppresses_covered():
    prop = SeaAnomalyProposer(work_width=320, z_thr=3.5)
    img = _sea_frame(blob=(150, 160, 14, 8))
    dets = prop.detect(img)
    assert dets and dets[0].label == "anomaly" and 0.3 <= dets[0].score <= 0.9
    x, y, w, h = dets[0].box
    assert abs(x + w / 2 - 157) < 6 and abs(y + h / 2 - 164) < 6
    assert not prop.detect(_sea_frame(seed=2))                      # plain sea: nothing above threshold

    class _Base:
        name = "base"

        def __init__(self, dets):
            self.dets = dets

        def detect(self, image):
            return self.dets

    covered = AnomalyFusionDetector(_Base([Detection(box=[148, 158, 18, 12], score=0.6, label="boat")]), prop)
    out = covered.detect(img)
    assert [d.label for d in out] == ["boat"]
    uncovered = AnomalyFusionDetector(_Base([Detection(box=[20, 100, 10, 10], score=0.6, label="boat")]), prop)
    labels = [d.label for d in uncovered.detect(img)]
    assert labels[0] == "boat" and "anomaly" in labels and "anomaly" in uncovered.name
