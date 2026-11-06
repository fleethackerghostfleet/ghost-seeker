"""MobileCLIP marine-mammal verification and bounded open-water proposals."""
from dataclasses import dataclass
from importlib.resources import files
import json
import time

import numpy as np

from ..types import Detection

PROMPTS = {
    'whale': ['a photo of a whale back surfacing', 'a photo of a whale body at sea', 'a photo of a breaching whale'],
    'blow': ['a photo of a whale spout above the sea', 'a photo of whale blow spray'],
    'fluke': ['a photo of a whale tail fluke above water', 'a photo of a diving whale tail'],
    'dolphin': ['a photo of a dolphin surfacing', 'a photo of a porpoise at sea', 'a photo of an orca dorsal fin breaking the surface'],
    'sea': ['a photo of empty open sea', 'a photo of an empty ocean horizon'],
    'waves': ['a photo of waves and whitecaps', 'a photo of sea spray'],
    'wake': ['a photo of a boat wake', 'a photo of foamy ship wake'],
    'vessel': ['a photo of a ship', 'a photo of a boat', 'a photo of a sailboat'],
    'bird': ['a photo of a seabird', 'a photo of a bird flying over water'],
    'shore': ['a photo of rocks in the sea', 'a photo of a rocky shoreline'],
    'glint': ['a photo of sun glint on water', 'a photo of sunlight reflecting on the ocean'],
    'person': ['a photo of a person swimming', 'a photo of a person in the water'],
    'buoy': ['a photo of a buoy', 'a photo of a navigation buoy at sea'],
    'debris': ['a photo of floating debris', 'a photo of floating rubbish in water'],
    'sky': ['a photo of the sky', 'a photo of clouds over the ocean'],
}
POSITIVES = ('whale', 'blow', 'fluke', 'dolphin')
MODEL = 'mobileclip-s1'


@dataclass
class MarineMammalConfig:
    enabled: bool = True
    candidate_labels: tuple = ('boat', 'bird', 'surfboard', 'kite', 'person', 'person in water', 'anomaly', 'unknown')
    candidate_min: float = 0.05
    top_k: int = 4
    context: float = 0.2
    crop_thr: float = 0.9
    scan_thr: float = 0.95
    score_thr: float = 0.35
    scan_stride: int = 5
    rows: int = 2
    cols: int = 3
    overlap: float = 0.2
    max_crops: int = 8

    def __post_init__(self):
        if min(self.scan_stride, self.rows, self.cols) < 1 or min(self.top_k, self.max_crops) < 0:
            raise ValueError('invalid marine mammal crop/grid budget')
        if not 0 <= self.overlap < 1 or self.context < 0:
            raise ValueError('invalid marine mammal padding')
        if any(not 0 <= x <= 1 for x in (self.crop_thr, self.scan_thr, self.score_thr, self.candidate_min)):
            raise ValueError('marine mammal thresholds must be in [0,1]')


@dataclass
class MarineMammalDetection(Detection):
    subtype: str = 'whale'


class MarineMammalVerifier:
    def __init__(self, embedder, bundle=None):
        self.embedder = embedder
        self.groups = list(PROMPTS)
        prompts = [p for group in PROMPTS.values() for p in group]
        try:
            text = embedder.embed_text(prompts)
        except RuntimeError:
            data = json.loads((bundle or files(__package__).joinpath('marine_mammal_text.json')).read_text())
            name = getattr(embedder, 'short', None)
            if data['model'] != MODEL or name != MODEL or data['dim'] != embedder.dim:
                raise ValueError('marine mammal text bundle model/dimension mismatch')
            text = np.asarray([data['embeddings'][p] for p in prompts], np.float32)
        text = np.asarray(text, np.float32)
        if text.shape != (len(prompts), embedder.dim) or not np.isfinite(text).all():
            raise ValueError('invalid marine mammal text embeddings')
        prototypes, offset = [], 0
        for group in PROMPTS.values():
            prototypes.append(text[offset:offset + len(group)].mean(0))
            offset += len(group)
        self.prototypes = np.asarray(prototypes)
        self.prototypes /= np.linalg.norm(self.prototypes, axis=1, keepdims=True) + 1e-8
        self.scale = float(embedder.logit_scale())

    def score(self, crops):
        if not crops:
            return []
        embeddings = self.embedder.embed_crops(crops)
        logits = embeddings @ self.prototypes.T * self.scale
        logits -= logits.max(axis=1, keepdims=True)
        probs = np.exp(logits)
        probs /= probs.sum(axis=1, keepdims=True)
        return [{'p': float(row[:4].sum()), 'subtype': POSITIVES[int(row[:4].argmax())]} for row in probs]


def raw_scores(image, detections, verifier, cfg, scan=True):
    """Stateless, threshold-free scores; candidate and open-water gates still apply."""
    start = time.perf_counter()
    h, w = image.shape[:2]
    proposals = []
    candidates = sorted(enumerate(detections), key=lambda pair: -pair[1].score)
    for index, det in candidates:
        if det.label not in cfg.candidate_labels or det.score < cfg.candidate_min:
            continue
        x, y, bw, bh = det.box
        x0, y0 = max(0, int(x - bw * cfg.context)), max(0, int(y - bh * cfg.context))
        x1, y1 = min(w, int(np.ceil(x + bw * (1 + cfg.context)))), min(h, int(np.ceil(y + bh * (1 + cfg.context))))
        if x1 > x0 and y1 > y0:
            proposals.append(('crop', index, [x0, y0, x1 - x0, y1 - y0]))
        if len(proposals) >= cfg.top_k:
            break
    if cfg.top_k == 0:
        proposals = []
    if scan and not any(d.score >= cfg.score_thr for d in detections):
        proposals.append(('scan', None, [0, 0, w, h]))
        for row in range(cfg.rows):
            for col in range(cfg.cols):
                tw, th = w / cfg.cols, h / cfg.rows
                x0, y0 = max(0, int((col - cfg.overlap / 2) * tw)), max(0, int((row - cfg.overlap / 2) * th))
                x1, y1 = min(w, int(np.ceil((col + 1 + cfg.overlap / 2) * tw))), min(h, int(np.ceil((row + 1 + cfg.overlap / 2) * th)))
                proposals.append(('scan', None, [x0, y0, x1 - x0, y1 - y0]))
    proposals = proposals[:cfg.max_crops] if cfg.enabled else []
    crops = [image[y:y + bh, x:x + bw] for _, _, (x, y, bw, bh) in proposals]
    scored = [dict(result, kind=kind, index=index, box=box) for (kind, index, box), result in zip(proposals, verifier.score(crops))]
    best = lambda kind: max((r for r in scored if r['kind'] == kind), key=lambda r: r['p'], default=None)
    crop, tile = best('crop'), best('scan')
    winner = max(scored, key=lambda r: r['p'], default={})
    return {'best_crop_p': crop['p'] if crop else 0.0, 'best_scan_p': tile['p'] if tile else 0.0,
            'subtype': winner.get('subtype'), 'scores': scored, 'mm_ms': (time.perf_counter() - start) * 1000, 'crops': len(crops)}


class MarineMammalFusionDetector:
    def __init__(self, base, verifier, cfg=None):
        self.base, self.verifier, self.cfg = base, verifier, cfg or MarineMammalConfig()
        self.name = base.name + '+marine-mammal'
        self.model_bytes = getattr(base, 'model_bytes', 0)
        self.frame = 0
        self.last_timing_ms = {}
        self.last_scores = {}

    def detect(self, image):
        detections = self.base.detect(image)
        self.frame += 1
        self.last_scores = raw_scores(image, detections, self.verifier, self.cfg, scan=(self.frame - 1) % self.cfg.scan_stride == 0)
        self.last_timing_ms = dict(getattr(self.base, 'last_timing_ms', {}), mm=self.last_scores['mm_ms'], mm_crops=self.last_scores['crops'])
        for result in self.last_scores['scores']:
            if result['kind'] == 'crop' and result['p'] >= self.cfg.crop_thr:
                old = detections[result['index']]
                detections[result['index']] = MarineMammalDetection(old.box, result['p'], 'marine mammal', subtype=result['subtype'])
        scans = [r for r in self.last_scores['scores'] if r['kind'] == 'scan' and r['p'] >= self.cfg.scan_thr]
        if scans:
            hit = max(scans, key=lambda r: r['p'])
            detections.append(MarineMammalDetection(hit['box'], hit['p'], 'marine mammal', subtype=hit['subtype']))
        return detections

    def info(self):
        return {'name': self.name, 'base': self.base.info()}
