import numpy as np

from edgeml.embed import shiptype as st


class FakeEmbedder:
    dim = 4

    def __init__(self):
        self.texts = []

    def embed_text(self, texts):
        self.texts.extend(texts)
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


def test_prompt_texts_cover_classifier_prototypes():
    embedder = FakeEmbedder()
    st.ShipTypeClassifier(embedder)
    assert set(embedder.texts) <= set(st.prompt_texts())


def test_unknown_floor_confidence_is_unknown_group_probability():
    classifier = st.ShipTypeClassifier(FakeEmbedder(), unknown_floor=0.2)
    embeddings = np.ones((1, 4), np.float32)
    top_probability = classifier.probs(embeddings)[0].max()

    assert top_probability < classifier.unknown_floor
    result = classifier.classify_embeddings(embeddings)[0]
    assert result["type"] == st.UNKNOWN
    assert np.isclose(result["conf"], classifier.probs(embeddings)[0][-1], atol=1e-3)
