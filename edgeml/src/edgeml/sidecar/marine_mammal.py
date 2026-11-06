"""Optional verifier wiring and track-local marine-mammal provenance."""
import logging

from edgeml.detectors.marine_mammal import MarineMammalConfig, MarineMammalFusionDetector, MarineMammalVerifier
from edgeml.detectors.thermal import ThermalFusionDetector


def detect_frame(detector, image, frame_index):
    if isinstance(detector, ThermalFusionDetector) and isinstance(detector.base, MarineMammalFusionDetector):
        return detector.detect(image, frame_index=frame_index)
    if isinstance(detector, MarineMammalFusionDetector):
        return detector.detect(image, frame_index=frame_index)
    return detector.detect(image)


def wrap_detector(detector, embedder, options, score_thr):
    cfg = MarineMammalConfig(**{'score_thr': score_thr, **(options or {})})
    if not cfg.enabled:
        return detector
    if embedder is None:
        logging.getLogger(__name__).info('marine mammal verification skipped: --embed is required')
        return detector
    if getattr(embedder.clip, 'short', None) != 'mobileclip-s1':
        logging.getLogger(__name__).info('marine mammal verification skipped: MobileCLIP-S1 is required')
        return detector
    return MarineMammalFusionDetector(detector, MarineMammalVerifier(embedder.clip), cfg)


class MarineTracker:
    def __init__(self, tracker):
        self.tracker = tracker
        self.subtypes = {}

    def update(self, detections, image, frame_idx, t_s):
        tracks = self.tracker.update(detections, image, frame_idx, t_s)
        for track in tracks:
            index = track.extra.get('det_ind', -1)
            subtype = getattr(detections[index], 'subtype', None) if 0 <= index < len(detections) else None
            if track.label == 'marine mammal':
                if subtype:
                    self.subtypes[track.track_id] = subtype
                track.extra['marine_mammal_subtype'] = self.subtypes.get(track.track_id)
            else:
                self.subtypes.pop(track.track_id, None)
        active = {track.track_id for track in tracks}
        self.subtypes = {key: value for key, value in self.subtypes.items() if key in active}
        return tracks


def make_aggregator(base, *args, **kwargs):
    class MarineAggregator(base):
        def _observe(self, st, track, image, frame_idx, t_s):
            super()._observe(st, track, image, frame_idx, t_s)
            subtype = track.extra.get('marine_mammal_subtype')
            if track.label == 'marine mammal' and subtype:
                st.mm_subtype = subtype
                st.s.label, st.s.subtype = 'marine mammal', subtype
            elif track.label != 'marine mammal':
                st.mm_subtype = None

        def _apply(self, st):
            if getattr(st, 'mm_subtype', None):
                st.s.label, st.s.subtype = 'marine mammal', st.mm_subtype
                st.s.unknownness = 0.0
                return
            super()._apply(st)

    return MarineAggregator(*args, **kwargs)
