"""Registry of zero-shot candidates. `build(name, imgsz=..., device=..., score_thr=...)` returns a Detector.

Every entry notes its training data so we never evaluate a model on its own training set
(the Argus checkpoints include SeaShips -> excluded from the SeaShips leaderboard)."""
from __future__ import annotations

from typing import Callable

from .labels import MARITIME_PROMPTS

_REG: dict[str, Callable] = {}
#: dataset_ids a model must NOT be scored on (trained on them)
EXCLUDE: dict[str, set[str]] = {}
TIER: dict[str, str] = {}


def _reg(name: str, tier: str, exclude: set[str] | None = None):
    def deco(fn):
        _REG[name] = fn
        TIER[name] = tier
        if exclude:
            EXCLUDE[name] = exclude
        return fn

    return deco


def names() -> list[str]:
    return list(_REG)


def build(name: str, **kw):
    if name not in _REG:
        raise KeyError(f"unknown detector {name}; known: {names()}")
    return _REG[name](**kw)


def _ul(weights, name, training="COCO"):
    def f(imgsz=640, **kw):
        from .ultralytics_det import UltralyticsDetector

        return UltralyticsDetector(weights, name=name, imgsz=imgsz, training_data=training, **kw)

    return f


# ---- closed-set COCO ("boat") -----------------------------------------------------------------
_reg("yolov8n", "nano")(_ul("yolov8n.pt", "yolov8n"))
_reg("yolov8s", "small")(_ul("yolov8s.pt", "yolov8s"))
_reg("yolo11n", "nano")(_ul("yolo11n.pt", "yolo11n"))
_reg("yolo11s", "small")(_ul("yolo11s.pt", "yolo11s"))
_reg("yolo26n", "nano")(_ul("yolo26n.pt", "yolo26n"))
_reg("yolo26s", "small")(_ul("yolo26s.pt", "yolo26s"))
_reg("yolo11m", "medium")(_ul("yolo11m.pt", "yolo11m"))
_reg("rtdetr-l", "large")(_ul("rtdetr-l.pt", "rtdetr-l"))


@_reg("rtdetrv2-r18", "small")
def _rtdetrv2(imgsz=640, **kw):
    from .hf_det import HFClosedSetDetector

    return HFClosedSetDetector("PekingU/rtdetr_v2_r18vd", name="rtdetrv2-r18", imgsz=imgsz, **kw)


@_reg("dfine-s", "small")
def _dfine_s(imgsz=640, **kw):
    from .hf_det import HFClosedSetDetector

    return HFClosedSetDetector("ustc-community/dfine-small-coco", name="dfine-s", imgsz=imgsz, **kw)


@_reg("dfine-n", "nano")
def _dfine_n(imgsz=640, **kw):
    from .hf_det import HFClosedSetDetector

    return HFClosedSetDetector("ustc-community/dfine-nano-coco", name="dfine-n", imgsz=imgsz, **kw)


@_reg("yolox-tiny", "nano")
def _yolox_tiny(imgsz=416, **kw):
    from .onnx_det import YoloxOnnxDetector

    return YoloxOnnxDetector("yolox_tiny.onnx", name="yolox-tiny", imgsz=416, **kw)


@_reg("nanodet-plus", "nano")
def _nanodet(imgsz=416, **kw):
    from .onnx_det import NanodetOnnxDetector

    return NanodetOnnxDetector(name="nanodet-plus-m1.5x", imgsz=416, **kw)


# ---- open-vocabulary with maritime prompts -------------------------------------------------------
@_reg("yolov8s-world", "small")
def _yworld(imgsz=640, prompts=None, **kw):
    from .ultralytics_det import UltralyticsDetector

    return UltralyticsDetector("yolov8s-worldv2.pt", name="yolov8s-worldv2", imgsz=imgsz,
                               prompts=prompts or MARITIME_PROMPTS, training_data="O365+GoldG (YOLO-World)", **kw)


@_reg("owlv2-b16", "large")
def _owlv2(imgsz=960, prompts=None, **kw):
    from .hf_det import HFOpenVocabDetector

    return HFOpenVocabDetector("google/owlv2-base-patch16-ensemble", name="owlv2-b16", prompts=prompts, imgsz=960, **kw)


@_reg("gdino-t", "large")
def _gdino(imgsz=800, prompts=None, **kw):
    from .hf_det import HFOpenVocabDetector

    return HFOpenVocabDetector("IDEA-Research/grounding-dino-tiny", name="gdino-t", prompts=prompts, imgsz=imgsz, **kw)


# ---- maritime-pretrained public checkpoints (note training data!) -------------------------------
def _hf_pt(repo, fname):
    def get():
        from huggingface_hub import hf_hub_download

        return hf_hub_download(repo, fname)

    return get


@_reg("argus-yolov8s", "small", exclude={"seaships7000"})
def _argus_v8s(imgsz=640, **kw):
    from .ultralytics_det import UltralyticsDetector

    return UltralyticsDetector(_hf_pt("alimkacar/argus-maritime-yolov8s", "best.pt")(), name="argus-yolov8s", imgsz=imgsz,
                               training_data="Marine Surveillance (Roboflow) + SeaShips (trained on SeaShips!)", **kw)


@_reg("argus-yolo11l", "large", exclude={"seaships7000"})
def _argus_11l(imgsz=1280, **kw):
    from .ultralytics_det import UltralyticsDetector

    return UltralyticsDetector(_hf_pt("alimkacar/argus-maritime-yolo11l", "argus-v2.pt")(), name="argus-yolo11l", imgsz=imgsz,
                               training_data="Marine Surveillance + Ship Detection (Roboflow) + SeaShips7000 + WaterScenes", **kw)
