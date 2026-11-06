"""InferenceService semantics on top of the edgeml pipeline (detect -> track -> SightingAggregator -> zero-shot).

One stateful pipeline per ``AnalyzeRequest.source``; the response carries the frame's per-object lifecycle as
``sightings`` (OPEN / UPDATE / CLOSE from the aggregator, DUPLICATE for every open sighting that was tracked in this
frame without a report) next to the stateless ``detections`` / ``embedding`` fields older clients read.
Payloads are protobuf-JSON dicts (lowerCamelCase, enums as names, int64 as strings), what the Go ``inference.Remote``
Connect client sends and expects.
"""
from __future__ import annotations

import hashlib
import json
import logging
import math
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

import numpy as np

from edgeml.nightlights import BurstController, frame_report

FORM_KINDS = {"fact": "FORM_KIND_FACT", "thumb": "FORM_KIND_THUMB", "crop": "FORM_KIND_CROP", "full": "FORM_KIND_FULL"}
EVENTS = {"open": "EVENT_OPEN", "update": "EVENT_UPDATE", "close": "EVENT_CLOSE", "duplicate": "EVENT_DUPLICATE"}
logger = logging.getLogger(__name__)


class RpcError(Exception):
    """Maps to a Connect error (code is the Connect code string, e.g. ``invalid_argument``)."""

    def __init__(self, code: str, message: str, http_status: int | None = None):
        super().__init__(message)
        self.code = code
        self.http_status = http_status


def _norm_box(box, w: int, h: int) -> dict:
    x, y, bw, bh = (float(v) for v in box[:4])
    return {"x": round(x / w, 5), "y": round(y / h, 5), "w": round(bw / w, 5), "h": round(bh / h, 5)}


def _form_hint(f) -> dict:
    get = f.get if isinstance(f, dict) else lambda k, d=None: getattr(f, k, d)
    return {"kind": FORM_KINDS.get(str(get("kind")), "FORM_KIND_UNSPECIFIED"), "bytes": str(int(get("bytes", 0) or 0)),
            "jpegQuality": int(get("quality", 0) or 0)}


def _f(v: Optional[float]) -> float:
    v = 0.0 if v is None else float(v)
    return 0.0 if math.isnan(v) else round(v, 5)


# ----------------------------------------------------------------------------- real engine
class _TextEmbedder:
    """D's ClipEmbedder exposes embed_text through the ORT text cache; ZeroShotClassifier only needs this trio."""

    def __init__(self, clip):
        self.clip, self.dim = clip, clip.dim
        self.calls, self.ms = 0, 0.0

    def embed_crops(self, crops):
        t0 = time.perf_counter()
        out = self.clip.embed_crops(crops)
        self.ms += (time.perf_counter() - t0) * 1e3
        self.calls += len(crops)
        return out

    def embed_text(self, texts):
        return self.clip.embed_text(texts)

    def logit_scale(self):
        return self.clip.logit_scale()


@dataclass
class _Source:
    tracker: object
    agg: object
    burst: BurstController = field(default_factory=BurstController.from_config)
    frame_idx: int = 0
    t0_ms: Optional[int] = None
    size: Optional[tuple[int, int]] = None
    lock: threading.Lock = field(default_factory=threading.Lock)


def _wire_subtype(ev: dict) -> str:
    """The proto has no tentative flag: a tentative sighting is tagged ``subtype="tentative"``; on confirmation the edge
    only retags on a non-empty subtype, so a confirmation without a classifier subtype retags with the coarse label."""
    if ev.get('label') == 'marine mammal' and ev.get('subtype') in ('whale', 'blow', 'fluke', 'dolphin'):
        return ev['subtype']
    if ev.get("tentative"):
        return "tentative"
    return ev.get("subtype") or ((ev.get("label") or "") if ev.get("reason") == "confirmed" else "")


class EdgemlEngine:
    """Detector + boxmot tracker + SightingAggregator (+ MobileCLIP zero-shot subtype) per source. CPU, no torch."""

    COARSE = {"aircraft": "aircraft", "buoy": "buoy", "marine mammal": "marine_mammal"}
    # demo defaults (C.md §9 + I_tracking.md §7); `--agg` / `--trk` JSON override individual keys.
    # I_tracking: tracks start at score >= score_thr (0.35) but continue through low-score detections (det_low_thr);
    # per-size association (centroid gate 0.04 of the frame diagonal, re-id on jumps > 1.5 box diagonals, re-link capped at
    # 2 diagonals); unmatched detections >= 0.5 become tentative tracks and are reported after 2 s if still unexplained.
    AGG_DEFAULTS: dict = {"relink_gap_s": 20.0, "min_update_interval_s": 10.0, "relink_max_diag": 2.0, "tentative_after_s": 2.0,
                          "gate_unknown_bonus_on_realness": True}
    TRK_DEFAULTS: dict = {"centroid_max_distance": 0.04, "max_jump_diag": 1.5, "tentative_thresh": 0.5}

    def __init__(self, detector: str | Path, embed: str | Path | None = None, *, threads: int = 4, fps: float = 5.0,
                 engine: str = "auto", tracker: str = "bytetrack", asso: str = "centroid", max_lost_s: float = 6.0,
                 score_thr: float = 0.35, det_low_thr: float = 0.05, agg: Optional[dict] = None, trk: Optional[dict] = None,
                 text_tower: str = "cache", anomaly_fallback: Optional[dict] = None, live_tiling: Optional[dict] = None,
                 marine_mammal: Optional[dict] = None):
        from edgeml.runtime.detectors import load_detector
        from edgeml.tracking import AggregatorParams

        self.threads, self.fps, self.tracker_variant, self.asso = threads, fps, tracker, asso
        self.max_lost_s, self.score_thr, self.det_low_thr = max_lost_s, score_thr, det_low_thr
        self.trk_params = {**self.TRK_DEFAULTS, **(trk or {})}
        self.det_low_thr = min(self.det_low_thr, float(self.trk_params.get("low_thresh", self.det_low_thr)))
        self.detector = load_detector(Path(detector).expanduser(), engine=engine, threads=threads, score_thr=min(score_thr, self.det_low_thr))
        from edgeml.detectors.live_tiled import MOB_LABEL, LiveTiledDetector, LiveTilingConfig

        self.detector = LiveTiledDetector(self.detector, LiveTilingConfig(**(live_tiling or {})), score_thr=score_thr)
        if anomaly_fallback is not None:
            # H: cheap "anything-on-water" proposer (horizon-band colour anomaly) fills in where YOLO11n is blind
            # (semi-submersibles, small debris); proposals carry label "anomaly" and feed the tracker like detections.
            from edgeml.detectors.anomaly import AnomalyFusionDetector, SeaAnomalyProposer

            self.detector = AnomalyFusionDetector(self.detector, SeaAnomalyProposer(**anomaly_fallback))
        self.embedder = self.classifier = None
        self.clip_name = None
        self._text_tower, self._torch_clip = text_tower, None
        if embed and str(embed).lower() != "none":
            from edgeml.embed.clip_embedder import ClipEmbedder
            from edgeml.embed.zeroshot import load_classifier

            d = Path(embed).expanduser()
            meta = json.loads((d / "meta.json").read_text())
            clip = ClipEmbedder(meta["model"], backend="ort", onnx_dir=d, threads=threads, precision=meta.get("precision", "fp32"))
            self.embedder = _TextEmbedder(clip)
            # H: dev-fitted sim_ref/sim_width + kNN bank (unknown_calib.npz next to the export) make `unknownness`
            # informative (AUROC known-vs-unknown 0.49 -> ~0.8 on synth_unknown dev); without the file: MCM defaults.
            self.classifier = load_classifier(self.embedder, embed_dir=d)
            self.unknown_calib = self.classifier.calibration_path
            self.clip_name = getattr(clip, "name", meta["model"])
        from .marine_mammal import wrap_detector
        self.detector = wrap_detector(self.detector, self.embedder, marine_mammal, score_thr)
        # H: realness-gated unknown flag by default (AGG_DEFAULTS) — on synth_unknown dev it removed every false "unknown" on
        # known vessels / sea-only frames while keeping the synthetic unusual contacts flagged (see reports/H_unknown.md).
        self.agg_params = AggregatorParams(**{**self.AGG_DEFAULTS, **(agg or {})})
        self.sources: dict[str, _Source] = {}
        self._lock = threading.Lock()
        self.model_id = self.detector.name + (f"+{self.clip_name}" if self.clip_name else "")
        self.embedding_dim = int(self.embedder.dim) if self.embedder else 0
        self.labels = list(self.classifier.classes) if self.classifier else []
        if MOB_LABEL not in self.labels:
            self.labels.append(MOB_LABEL)
        self.frames = 0
        self.ms_total = 0.0

        from .open_vocab import ONNXTextTower, OpenVocabulary, TextCache
        self._onnx_text = None
        if self.embedder and all((Path(embed).expanduser() / name).is_file() for name in ("text_model.onnx", "tokenizer.json")):
            self._onnx_text = ONNXTextTower(Path(embed).expanduser(), threads)
        self._cached_text = TextCache(self._encode_text)
        self.open_vocab = OpenVocabulary(self.embedder, self._cached_text) if self.embedder and (self._onnx_text or text_tower == "torch") else None

    # -- per-source state ------------------------------------------------------------------------------------------
    def _source(self, name: str) -> _Source:
        with self._lock:
            src = self.sources.get(name)
            if src is None:
                from edgeml.tracking import make_tracker

                kw = {"asso_func": self.asso, "max_lost_s": self.max_lost_s, "det_thresh": self.score_thr,
                      "low_thresh": self.det_low_thr, **self.trk_params}        # --trk may override any of these
                tracker = make_tracker(self.tracker_variant, fps=self.fps, **kw)
                from .marine_mammal import MarineTracker, make_aggregator
                tracker = MarineTracker(tracker)
                agg = make_aggregator(_make_zeroshot_aggregator_class(), name, self.agg_params, embedder=self.embedder, classifier=self.classifier, coarse=self.COARSE)
                src = self.sources[name] = _Source(tracker=tracker, agg=agg)
            return src

    def _frame_time(self, src: _Source, captured_ms: Optional[int]) -> float:
        if captured_ms:
            if src.t0_ms is None:
                src.t0_ms = captured_ms
            return max(0.0, (captured_ms - src.t0_ms) / 1000.0)
        return (src.frame_idx - 1) / self.fps

    # -- rpc -------------------------------------------------------------------------------------------------------
    def analyze(self, source: str, image_bgr: np.ndarray, captured_ms: Optional[int], known_labels: list[str], want_embedding: bool) -> dict:
        from .open_vocab import labels_from_wire
        try:
            open_labels = labels_from_wire(known_labels)
        except ValueError as exc:
            raise RpcError("invalid_argument", str(exc)) from exc
        if open_labels and not self.open_vocab:
            raise RpcError("failed_precondition", "open vocabulary requires text_model.onnx + tokenizer.json or --text-tower torch")
        src = self._source(source or "default")
        t_start = time.perf_counter()
        night_report = None
        with src.lock:
            src.frame_idx += 1
            fi = src.frame_idx
            t_s = self._frame_time(src, captured_ms)
            H, W = image_bgr.shape[:2]
            dets = [d for d in self.detector.detect(image_bgr) if d.score >= self.det_low_thr]
            kept = [d for d in dets if d.score >= self.score_thr]
            tracks = src.tracker.update(dets, image_bgr, fi, t_s)
            events = src.agg.update(tracks, image_bgr, fi, t_s)
            base_ms = (captured_ms - int(t_s * 1000)) if captured_ms else int(time.time() * 1000) - int(t_s * 1000)
            sightings = [self._sighting(ev, fi, W, H, base_ms, src.agg.states.get(ev["sighting_id"])) for ev in events]
            reported = {ev["sighting_id"] for ev in events}
            for tr in tracks:
                sid = src.agg.track_to_sid.get(tr.track_id)
                st = src.agg.states.get(sid) if sid else None
                if st is not None and st.status == "open" and sid not in reported:
                    reported.add(sid)
                    sightings.append(self._duplicate(st, fi, W, H, base_ms, getattr(tr, "box", None)))
            emb = next((src.agg.states[ev["sighting_id"]].emb for ev in events
                        if ev["event"] in ("open", "update") and ev["sighting_id"] in src.agg.states
                        and src.agg.states[ev["sighting_id"]].emb is not None), None) if want_embedding else None
            unknownness = max((float(ev.get("unknownness") or 0.0) for ev in events), default=0.0)
            open_dets = self.open_vocab.detect(image_bgr, dets, open_labels) if open_labels else []
            ms = (time.perf_counter() - t_start) * 1e3
            try:
                night_report = frame_report(image_bgr, [(d.label, d.score, d.box) for d in kept],
                                            burst=src.burst, t=t_s)
            except Exception:
                logger.exception("night-lights frame analysis failed; skipping optional results")
        self.frames += 1
        self.ms_total += ms
        reported_dets = [{"label": d.label, "confidence": _f(d.score), "box": _norm_box(d.box, W, H)} for d in kept]
        resp = {"detections": reported_dets,
                "unknownness": _f(unknownness), "modelId": self.model_id, "latencyMs": str(int(ms)), "sightings": sightings}
        if night_report is not None:
            for index, confidence in night_report["dark"]:
                box = dict(reported_dets[index]["box"])
                resp["detections"].append({"label": "dark contact", "confidence": confidence, "box": box})
            if night_report["burst"] is not None:
                burst_result = night_report["burst"]
                resp["burst"] = {"fps": burst_result["fps"], "durationS": burst_result["duration_s"],
                                 "reason": burst_result["reason"]}
        if emb is not None:
            resp["embedding"] = [round(float(v), 5) for v in np.asarray(emb).ravel()]
        resp["detections"].extend(open_dets)
        return resp

    def _sighting(self, ev: dict, fi: int, W: int, H: int, base_ms: int, st=None) -> dict:
        label = ev.get("label") or ""
        is_key = int(ev.get("best_frame_idx", -1)) == fi and ev["event"] != "close"
        # The edge crops the frame it holds (this one), so report the object's box in *this* frame unless this frame
        # is the key frame, where best_box is exactly that.
        box = ev["best_box"] if is_key or not ev.get("box") else ev["box"]
        out = {"id": ev["sighting_id"], "event": EVENTS[ev["event"]],
               "keyDetection": {"label": label, "confidence": _f(ev.get("best_score")), "box": _norm_box(box, W, H)},
               "isKeyFrame": is_key, "unknownness": _f(ev.get("unknownness")),
               "subtype": _wire_subtype(ev), "value": _f(ev.get("value")),
               "forms": [_form_hint(f) for f in (ev.get("forms") or [])],
               "firstSeenUnixMs": str(base_ms + int(float(ev.get("first_t_s", 0.0)) * 1000)),
               "lastSeenUnixMs": str(base_ms + int(float(ev.get("last_t_s", ev.get("t_s", 0.0))) * 1000)),
               "nFrames": int(ev.get("n_frames", 0))}
        if st is not None and st.emb is not None and ev["event"] in ("open", "update"):
            out["embedding"] = [round(float(v), 5) for v in np.asarray(st.emb).ravel()]
        return out

    def _duplicate(self, st, fi: int, W: int, H: int, base_ms: int, track_box=None) -> dict:
        s = st.s
        box = track_box if track_box is not None else s.best_box
        return {"id": s.sighting_id, "event": "EVENT_DUPLICATE",
                "keyDetection": {"label": s.label or "", "confidence": _f(s.best_score), "box": _norm_box(box, W, H)},
                "isKeyFrame": False, "subtype": s.subtype or "", "value": 0.0, "forms": [],
                "firstSeenUnixMs": str(base_ms + int(s.first_t_s * 1000)), "lastSeenUnixMs": str(base_ms + int(s.last_t_s * 1000)),
                "nFrames": int(s.n_frames)}

    def embed_text(self, texts: list[str]) -> list[list[float]]:
        return [[round(float(v), 5) for v in row] for row in self._cached_text(texts)]

    def _encode_text(self, texts):
        if self.embedder is None:
            raise RpcError("unimplemented", "sidecar started without --embed")
        if self._onnx_text is not None:
            return self._onnx_text(texts)
        try:
            vecs = self.embedder.embed_text(texts)
        except RuntimeError as e:
            if self._text_tower != "torch":
                raise RpcError("failed_precondition", f"text not in the ORT text cache and --text-tower torch not enabled: {e}") from e
            vecs = self._torch_text(texts)
        return [[round(float(v), 5) for v in row] for row in np.asarray(vecs, np.float32)]

    def _torch_text(self, texts):
        # Explicit opt-in fallback; offline ONNX is preferred for live use.
        if self._torch_clip is None:
            from edgeml.embed.clip_embedder import ClipEmbedder

            self._torch_clip = ClipEmbedder(self.embedder.clip.short, backend="torch")
        return self._torch_clip.embed_text(texts)

    def capabilities(self) -> dict:
        return {"modelId": self.model_id, "openVocabulary": self.open_vocab is not None, "fixedLabels": list(self.labels),
                "embeddingDim": self.embedding_dim}

    def stats(self) -> dict:
        return {"frames": self.frames, "ms_per_frame": (self.ms_total / self.frames) if self.frames else 0.0,
                "sources": {k: {"frames": v.frame_idx, "open": sum(st.status == "open" for st in v.agg.states.values())} for k, v in self.sources.items()}}


def _make_zeroshot_aggregator_class():
    from edgeml.tracking import SightingAggregator

    class ZeroShotAggregator(SightingAggregator):
        """SightingAggregator whose ``subtype`` / ``unknownness`` / coarse ``label`` come from zero-shot MobileCLIP on
        the key-frame embedding the aggregator already computes (the E demo's DemoAggregator, minus the stats)."""

        def __init__(self, *a, classifier=None, coarse=None, **kw):
            super().__init__(*a, **kw)
            self.classifier, self.coarse = classifier, coarse or {}
            self.zs: dict[str, dict] = {}

        def _apply(self, st):
            if st.s.label in {"person", "person in water"}:
                return
            if self.classifier is None or st.emb is None:
                return
            z = self.zs.get(st.s.sighting_id)
            if z is None or z["key"] is not st.emb:
                r = self.classifier.classify(np.asarray(st.emb, np.float32)[None])[0]
                z = self.zs[st.s.sighting_id] = {"key": st.emb, "subtype": r["subtype"], "unknownness": float(r["unknownness"])}
            st.s.subtype, st.s.unknownness = z["subtype"], z["unknownness"]
            st.s.label = self.coarse.get(z["subtype"], "boat")

        def _snapshot(self, st):
            self._apply(st)
            return super()._snapshot(st) | {"subtype": st.s.subtype}

        def _material_change(self, st, t_s):
            r = super()._material_change(st, t_s)
            if r or t_s - st.last_report_t < self.p.min_update_interval_s or st.n_updates >= self.p.max_updates or not st.last_report:
                return r
            return "subtype_flip" if st.s.subtype != st.last_report.get("subtype") else None

        def _report(self, st, kind, frame_idx, t_s, reason=None):
            self._apply(st)
            return super()._report(st, kind, frame_idx, t_s, reason)

        def _close(self, st, frame_idx, t_s):
            self._apply(st)
            ev = super()._close(st, frame_idx, t_s)
            self.zs.pop(st.s.sighting_id, None)
            return ev

    return ZeroShotAggregator


def _ZeroShotAggregator(*a, **kw):
    return _make_zeroshot_aggregator_class()(*a, **kw)


# ----------------------------------------------------------------------------- mock engine
@dataclass
class MockSchedule:
    """Per-source script: frame ``open_at`` opens one object (key frame), ``key_at`` is a better key frame (UPDATE,
    is_key_frame), every other frame while open is a DUPLICATE, ``close_at`` closes it; then the cycle repeats with a
    new id. Frames before ``open_at`` carry no sightings (the Go edge falls back to hash behaviour for them)."""

    open_at: int = 2
    key_at: int = 5
    close_at: int = 9
    label: str = "boat"
    subtype: str = "fishing vessel"
    box: tuple = (0.3, 0.3, 0.4, 0.4)


class NoopEngine:
    """No detector: used when the sidecar runs --prioritizer tomer without a model."""

    model_id = "none"

    def __init__(self):
        self.frames = 0

    def analyze(self, source, img, captured_at_ms, known_labels, want_embedding):
        self.frames += 1
        return {"detections": [], "unknownness": 0.0, "modelId": self.model_id, "latencyMs": "0", "sightings": []}

    def stats(self) -> dict:
        return {"frames": self.frames, "ms_per_frame": 0.0, "sources": {}}

    def embed_text(self, texts):
        raise RpcError("unimplemented", "no text tower in prioritizer-only mode")

    def capabilities(self):
        return {"modelId": self.model_id, "openVocabulary": False, "fixedLabels": [], "embeddingDim": 0}


class MockEngine:
    """Deterministic engine for Go tests / CI: no models, no downloads. Embeddings hash words like the Go mock."""

    model_id = "edgeml-mock-v1"
    embedding_dim = 64

    def __init__(self, schedule: MockSchedule | None = None):
        self.schedule = schedule or MockSchedule()
        self.counts: dict[str, int] = {}
        self.frames = 0
        self._lock = threading.Lock()
        self.labels = [self.schedule.label]

    def analyze(self, source: str, image_bgr: np.ndarray, captured_ms: Optional[int], known_labels: list[str], want_embedding: bool) -> dict:
        source = source or "default"
        sc = self.schedule
        with self._lock:
            n = self.counts.get(source, 0) + 1
            self.counts[source] = n
            self.frames += 1
        period = sc.close_at - sc.open_at + 1
        cycle, pos = divmod(n - sc.open_at, period) if n >= sc.open_at else (0, -1)
        sid = f"{source}-mock-{cycle + 1}"
        now = captured_ms or int(time.time() * 1000)
        x, y, w, h = sc.box
        det = {"label": sc.label, "confidence": 0.9, "box": {"x": x, "y": y, "w": w, "h": h}}
        resp = {"detections": [], "unknownness": 0.1, "modelId": self.model_id, "latencyMs": "1", "sightings": []}
        if pos < 0:
            return resp
        resp["detections"] = [det]
        resp["unknownness"] = 0.0
        if want_embedding:
            resp["embedding"] = self._vec(sc.label)
        frame = sc.open_at + pos
        if frame == sc.open_at:
            event, key = "EVENT_OPEN", True
        elif frame == sc.key_at:
            event, key = "EVENT_UPDATE", True
            det = dict(det, confidence=0.97)
        elif frame == sc.close_at:
            event, key = "EVENT_CLOSE", False
        else:
            event, key = "EVENT_DUPLICATE", False
        forms = [] if event in ("EVENT_CLOSE", "EVENT_DUPLICATE") else [
            {"kind": "FORM_KIND_FACT", "bytes": "180", "jpegQuality": 0}, {"kind": "FORM_KIND_CROP", "bytes": "6000", "jpegQuality": 75},
            {"kind": "FORM_KIND_THUMB", "bytes": "4000", "jpegQuality": 55}, {"kind": "FORM_KIND_FULL", "bytes": str(int(image_bgr.nbytes // 20) or 1), "jpegQuality": 80}]
        resp["sightings"] = [{"id": sid, "event": event, "keyDetection": det, "isKeyFrame": key, "subtype": sc.subtype,
                              "value": 0.8 if key else 0.0, "forms": forms, "firstSeenUnixMs": str(now - pos * 200),
                              "lastSeenUnixMs": str(now), "nFrames": pos + 1, "unknownness": 0.0,
                              **({"embedding": self._vec(sc.subtype)} if key else {})}]
        return resp

    def _vec(self, text: str) -> list[float]:
        v = np.zeros(self.embedding_dim, np.float32)
        for w in text.lower().split():
            seed = int.from_bytes(hashlib.sha256(w.encode()).digest()[:8], "little")
            v += np.random.default_rng(seed).standard_normal(self.embedding_dim).astype(np.float32)
        n = float(np.linalg.norm(v)) or 1.0
        return [round(float(x / n), 5) for x in v]

    def embed_text(self, texts: list[str]) -> list[list[float]]:
        return [self._vec(t) for t in texts]

    def capabilities(self) -> dict:
        return {"modelId": self.model_id, "openVocabulary": True, "fixedLabels": self.labels, "embeddingDim": self.embedding_dim}

    def stats(self) -> dict:
        return {"frames": self.frames, "ms_per_frame": 0.0, "sources": dict(self.counts)}


# ----------------------------------------------------------------------------- service (JSON in / JSON out)
def _get(d: dict, camel: str, snake: str, default=None):
    return d.get(camel, d.get(snake, default))



# Resource caps: a crafted header can declare a huge frame that a decoder
# would allocate in full, and EmbedText runs the text tower once per string.
MAX_PIXELS = 50_000_000
MAX_TEXTS = 64
MAX_TEXT_CHARS = 512


def image_dims(data: bytes):
    """Width/height from the container header (PNG, JPEG, GIF, BMP, WebP) without decoding.
    Returns None for formats this sidecar does not accept."""
    import struct

    if data[:8] == b"\x89PNG\r\n\x1a\n" and len(data) >= 24:
        return struct.unpack(">II", data[16:24])
    if data[:6] in (b"GIF87a", b"GIF89a") and len(data) >= 10:
        return struct.unpack("<HH", data[6:10])
    if data[:2] == b"BM" and len(data) >= 26:
        w, h = struct.unpack("<ii", data[18:26])
        return abs(w), abs(h)
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP" and len(data) >= 30:
        chunk = data[12:16]
        if chunk == b"VP8 ":
            return struct.unpack("<HH", data[26:30])[0] & 0x3FFF, struct.unpack("<HH", data[26:30])[1] & 0x3FFF
        if chunk == b"VP8L":
            b = data[21:25]
            return 1 + (((b[1] & 0x3F) << 8) | b[0]), 1 + (((b[3] & 0xF) << 10) | (b[2] << 2) | ((b[1] & 0xC0) >> 6))
        if chunk == b"VP8X":
            return 1 + int.from_bytes(data[24:27], "little"), 1 + int.from_bytes(data[27:30], "little")
        return None
    if data[:2] == b"\xff\xd8":
        i, n = 2, len(data)
        while i + 9 < n:
            if data[i] != 0xFF:
                i += 1
                continue
            marker = data[i + 1]
            if marker == 0xFF:  # fill bytes before a marker
                i += 1
                continue
            if marker in (0xD8, 0x01) or 0xD0 <= marker <= 0xD7:
                i += 2
                continue
            seg = struct.unpack(">H", data[i + 2:i + 4])[0]
            if marker in (0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF):
                h, w = struct.unpack(">HH", data[i + 5:i + 9])
                return w, h
            i += 2 + seg
        return None
    return None


def check_image_dims(data: bytes, max_pixels: int = MAX_PIXELS) -> tuple:
    dims = image_dims(data)
    if dims is None:
        raise RpcError("invalid_argument", "image must be a JPEG, PNG, GIF, BMP or WebP with a readable header")
    w, h = dims
    if w <= 0 or h <= 0 or w * h > max_pixels:
        raise RpcError("resource_exhausted", f"image {w}x{h} exceeds the {max_pixels} pixel cap", http_status=413)
    return dims


class InferenceService:
    """Decodes protobuf-JSON requests, dispatches to an engine, returns protobuf-JSON responses."""

    SERVICE = "ghostseeker.v1.InferenceService"

    def __init__(self, engine, prioritizer=None, mode: str = "edgeml"):
        """mode: edgeml (engine only) | tomer (prioritizer only; engine is a no-op) | both."""
        if mode not in ("edgeml", "tomer", "both"):
            raise ValueError(f"unknown prioritizer mode {mode!r}")
        if mode != "edgeml" and prioritizer is None:
            raise ValueError(f"prioritizer mode {mode!r} needs a prioritizer")
        self.engine = engine
        self.prioritizer = prioritizer
        self.mode = mode

    def Analyze(self, req: dict) -> dict:
        import base64

        import cv2

        raw = _get(req, "image", "image")
        if not raw:
            raise RpcError("invalid_argument", "image is required")
        try:
            buf = np.frombuffer(base64.b64decode(raw, validate=True), np.uint8)
        except (ValueError, TypeError) as e:
            raise RpcError("invalid_argument", f"image is not valid base64: {e}") from None
        check_image_dims(buf.tobytes())
        img = cv2.imdecode(buf, cv2.IMREAD_COLOR)
        if img is None:
            raise RpcError("invalid_argument", "image could not be decoded")
        captured = _get(req, "capturedAtUnixMs", "captured_at_unix_ms")
        captured = int(captured) if captured not in (None, "", 0, "0") else None
        source = str(_get(req, "source", "source", "") or "")
        resp = self.engine.analyze(source, img, captured, list(_get(req, "knownLabels", "known_labels", []) or []),
                                   bool(_get(req, "wantEmbedding", "want_embedding", False)))
        if self.prioritizer is not None:
            resp["imagePriority"] = self.prioritizer.score(source, img)
        return resp

    def EmbedText(self, req: dict) -> dict:
        texts = req.get("texts") or []
        if not isinstance(texts, list) or len(texts) > MAX_TEXTS:
            raise RpcError("invalid_argument", f"texts must be a list of at most {MAX_TEXTS} strings")
        texts = [str(t) for t in texts]
        if any(len(t) > MAX_TEXT_CHARS for t in texts):
            raise RpcError("invalid_argument", f"each text must be at most {MAX_TEXT_CHARS} characters")
        return {"embeddings": [{"values": v} for v in self.engine.embed_text(texts)], "modelId": getattr(self.engine, "model_id", "")}

    def Capabilities(self, req: dict) -> dict:
        caps = dict(self.engine.capabilities())
        caps["prioritizer"] = self.mode
        return caps

    def dispatch(self, method: str, req: dict) -> dict:
        fn = getattr(self, method, None) if method in ("Analyze", "EmbedText", "Capabilities") else None
        if fn is None:
            raise RpcError("unimplemented", f"{self.SERVICE}/{method} is not implemented")
        return fn(req)
