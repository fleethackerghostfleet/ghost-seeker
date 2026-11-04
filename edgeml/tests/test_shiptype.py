import numpy as np

from edgeml.embed import shiptype as st


class FakeEmbedder:
    dim = 4

    def embed_text(self, texts):
        return np.ones((len(texts), 4), np.float32) / 2

    def embed_crops(self, crops):
        return np.ones((len(crops), 4), np.float32) / 2


def test_square_pad_and_crop():
    img = np.zeros((100, 200, 3), np.uint8)
    c = st.crop_box(img, [50, 40, 80, 20])
    assert c.shape[:2] == (24, 96)
    assert st.square_pad(c).shape[:2] == (96, 96)


def test_zero_shot_record_fields():
    r = st.ShipTypeClassifier(FakeEmbedder()).classify([np.zeros((10, 30, 3), np.uint8)])[0]
    assert r["type"] in st.TYPES and 0 <= r["conf"] <= 1 and r["label"] == st.LABELS[r["type"]]
    assert set(r) <= {"type", "label", "conf", "aspect", "size", "features", "markings", "model"}
    assert len(st.TYPES) == 12
