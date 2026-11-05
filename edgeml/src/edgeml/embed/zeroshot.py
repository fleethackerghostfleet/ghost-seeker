"""Zero-shot maritime subtype over a fixed prompt set, plus an ``unknownness`` score.

unknownness blends two signals (MCM + kNN-OOD, see reports/literature.md §6):
  * mcm  = 1 - max softmax prob over the known-class text prototypes (temperature = CLIP logit scale), and the gap
           between the best known-class cosine and the dev-calibrated reference ``sim_ref`` (CLIP cosines live in a
           narrow, model-specific band, so an absolute distance needs calibration: call ``calibrate(known_feats)``).
  * knn  = normalised distance to the nearest neighbours in a bank of *dev* crops of known vessels (``fit_bank``).
Without a bank, unknownness = 0.5*(1-p_max) + 0.5*gap; with a bank, (mcm + knn)/2.  High unknownness means "not
confidently any of the things we have names for / seen before", which is what a watch-stander wants flagged.
"""
from __future__ import annotations

import numpy as np

SUBTYPES: dict[str, list[str]] = {
    "cargo ship": ["a photo of a cargo ship at sea", "a bulk carrier ship", "a general cargo ship", "an ore carrier ship"],
    "tanker": ["a photo of an oil tanker ship", "a tanker vessel at sea"],
    "container ship": ["a photo of a container ship", "a ship stacked with shipping containers"],
    "fishing boat": ["a photo of a fishing boat", "a small fishing vessel with nets"],
    "sailboat": ["a photo of a sailboat", "a yacht with sails on the ocean"],
    "speedboat": ["a photo of a speedboat", "a small motorboat moving fast on the water"],
    "passenger ship": ["a photo of a cruise ship", "a passenger ferry"],
    "warship": ["a photo of a navy warship", "a destroyer warship at sea", "a military frigate"],
    "submarine": ["a photo of a submarine on the surface", "a submarine sail above the water"],
    "coast guard": ["a photo of a coast guard patrol boat", "a coast guard cutter with an orange stripe"],
    "buoy": ["a photo of a navigation buoy in the water", "a floating marker buoy"],
    "aircraft": ["a photo of a helicopter", "a photo of an airplane in the sky", "an aircraft flying over the sea"],
    "marine mammal": ["a photo of a whale", "a photo of a dolphin", "a dolphin fin breaking the water"],
}
UNKNOWN_PROMPTS = ["a photo of the open sea", "a photo of waves and sky", "an empty ocean horizon", "sea spray and foam", "a blurry photo of water"]

# Native dataset labels -> subtype used for the confusion matrices.
SEASHIPS_TO_SUBTYPE = {"ore carrier": "cargo ship", "bulk cargo carrier": "cargo ship", "general cargo ship": "cargo ship",
                       "container ship": "container ship", "fishing boat": "fishing boat", "passenger ship": "passenger ship"}
SMD_TO_SUBTYPE = {"Vessel/ship": None, "Boat": None, "Speed boat": "speedboat", "Buoy": "buoy", "Sail boat": "sailboat",
                  "Ferry": "passenger ship", "Kayak": None, "Flying bird/plane": "aircraft", "Other": None}


CALIB_FILE = "unknown_calib.npz"


def load_classifier(embedder, embed_dir=None, calib=None, **kw) -> "ZeroShotClassifier":
    """ZeroShotClassifier that auto-loads ``<embed_dir>/unknown_calib.npz`` (dev-fitted sim_ref + kNN bank) when present."""
    from pathlib import Path

    clf = ZeroShotClassifier(embedder, **kw)
    p = Path(calib) if calib else (Path(embed_dir).expanduser() / CALIB_FILE if embed_dir else None)
    if p is not None and p.exists():
        clf.calibration_meta = clf.load_calibration(p)
        clf.calibration_path = str(p)
    else:
        clf.calibration_meta, clf.calibration_path = {}, None
    return clf


class ZeroShotClassifier:
    def __init__(self, embedder, subtypes: dict[str, list[str]] = SUBTYPES, unknown_prompts: list[str] = UNKNOWN_PROMPTS,
                 sim_ref: float = 0.30, sim_width: float = 0.10, image_prototypes: dict[str, np.ndarray] | None = None):
        self.embedder = embedder
        self.classes = list(subtypes)
        prompts = [p for c in self.classes for p in subtypes[c]]
        text = embedder.embed_text(prompts + unknown_prompts)
        protos, i = [], 0
        for c in self.classes:
            n = len(subtypes[c])
            v = text[i:i + n].mean(0)
            if image_prototypes and c in image_prototypes:
                v = v + image_prototypes[c]
            protos.append(v / (np.linalg.norm(v) + 1e-8))
            i += n
        self.prototypes = np.stack(protos)                       # (C, D)
        self.unknown_protos = text[i:]                           # (U, D)
        self.scale = embedder.logit_scale()
        self.sim_ref, self.sim_width = sim_ref, sim_width
        self.bank: np.ndarray | None = None
        self.knn_k, self.knn_ref, self.knn_width = 5, 0.0, 1.0
        self.w_knn = 0.5                                         # kNN weight when a bank is present

    # -- dev-fitted calibration artifact (sim_ref/sim_width + kNN bank), shipped next to the MobileCLIP export -------
    def save_calibration(self, path, meta: dict | None = None) -> None:
        """Write ``unknown_calib.npz``: scalars + the kNN bank (dev known crops only). ``meta`` is stored as JSON."""
        import json

        np.savez(str(path), sim_ref=self.sim_ref, sim_width=self.sim_width, scale=self.scale, w_knn=self.w_knn,
                 knn_k=self.knn_k, knn_ref=self.knn_ref, knn_width=self.knn_width,
                 bank=self.bank if self.bank is not None else np.zeros((0, self.prototypes.shape[1]), np.float32),
                 meta=json.dumps(meta or {}))

    def load_calibration(self, path) -> dict:
        import json

        z = np.load(str(path), allow_pickle=False)
        self.sim_ref, self.sim_width = float(z["sim_ref"]), float(z["sim_width"])
        self.scale, self.w_knn = float(z["scale"]), float(z["w_knn"])
        self.knn_k, self.knn_ref, self.knn_width = int(z["knn_k"]), float(z["knn_ref"]), float(z["knn_width"])
        bank = z["bank"].astype(np.float32)
        if bank.shape[1] != self.prototypes.shape[1]:
            raise ValueError(f"calibration bank dim {bank.shape[1]} != embedding dim {self.prototypes.shape[1]}")
        self.bank = bank if len(bank) else None
        return json.loads(str(z["meta"]))

    def calibrate(self, known_feats: np.ndarray) -> dict:
        """Set ``sim_ref``/``sim_width`` from the max-cosine distribution of known (dev) crops: p10 and (p50-p10)*2."""
        sims = (known_feats @ self.prototypes.T).max(1)
        p10, p50 = float(np.percentile(sims, 10)), float(np.percentile(sims, 50))
        self.sim_ref, self.sim_width = p10, max(2 * (p50 - p10), 1e-3)
        return {"sim_ref": self.sim_ref, "sim_width": self.sim_width}

    def fit_bank(self, known_feats: np.ndarray, k: int = 5) -> dict:
        """kNN-OOD bank of known dev crops. Reference distances are leave-one-out on the bank itself."""
        self.bank, self.knn_k = known_feats.astype(np.float32), k
        d = self._knn_dist(known_feats, exclude_self=True)
        p50, p90 = float(np.percentile(d, 50)), float(np.percentile(d, 90))
        self.knn_ref, self.knn_width = p50, max(2 * (p90 - p50), 1e-3)
        return {"n_bank": len(known_feats), "knn_ref": p50, "knn_width": self.knn_width}

    def _knn_dist(self, emb: np.ndarray, exclude_self: bool = False) -> np.ndarray:
        sims = emb @ self.bank.T
        if exclude_self:
            np.fill_diagonal(sims, -1.0)
        k = min(self.knn_k, sims.shape[1])
        top = -np.partition(-sims, k - 1, axis=1)[:, :k]
        return 1.0 - top.mean(1)

    def classify(self, emb: np.ndarray) -> list[dict]:
        """emb: (N, D) L2-normalised. Returns per-row {subtype, prob, probs{}, unknownness, max_sim, bg_sim}."""
        sims = emb @ self.prototypes.T                           # cosine
        logits = self.scale * sims
        logits -= logits.max(1, keepdims=True)
        probs = np.exp(logits)
        probs /= probs.sum(1, keepdims=True)
        bg = (emb @ self.unknown_protos.T).max(1) if len(self.unknown_protos) else np.zeros(len(emb))
        knn = np.clip((self._knn_dist(emb) - self.knn_ref) / self.knn_width, 0, 1) if self.bank is not None else None
        out = []
        for i in range(len(emb)):
            k = int(probs[i].argmax())
            max_sim = float(sims[i, k])
            dist_term = float(np.clip((self.sim_ref - max_sim) / self.sim_width, 0, 1))
            mcm = 0.5 * (1.0 - float(probs[i, k])) + 0.5 * dist_term
            unk = mcm if knn is None else (1.0 - self.w_knn) * mcm + self.w_knn * float(knn[i])
            # background-looking crops (sea/sky closer than any known class) are also "unknown"
            if bg[i] > max_sim:
                unk = max(unk, 0.5 + 0.5 * float(np.clip(bg[i] - max_sim, 0, 0.1) / 0.1))
            out.append({"subtype": self.classes[k], "prob": float(probs[i, k]), "probs": {c: float(p) for c, p in zip(self.classes, probs[i])},
                        "unknownness": float(np.clip(unk, 0, 1)), "mcm": float(np.clip(mcm, 0, 1)), "knn": None if knn is None else float(knn[i]),
                        "max_sim": max_sim, "bg_sim": float(bg[i])})
        return out
