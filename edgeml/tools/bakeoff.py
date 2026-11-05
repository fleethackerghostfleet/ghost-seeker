#!/usr/bin/env python
"""Additional model registrations; reuse B/D's runners and evaluation metrics unchanged.

python edgeml/tools/bakeoff.py detectors <bench_detectors.py flags>
python edgeml/tools/bakeoff.py classifiers <d_eval_zeroshot.py flags>
"""
from __future__ import annotations

import runpy
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))


def register_detectors():
    from edgeml.detectors.zoo import _reg, _ul

    for name in ("yolov9t", "yolov9s", "yolov9c", "yolov10n", "yolov10s", "yolo12n", "yolo12s"):
        _reg(name, "bakeoff")(_ul(f"{name}.pt", name))

    closed = {
        "detr-r50": "facebook/detr-resnet-50",
        "conditional-detr-r50": "microsoft/conditional-detr-resnet-50",
        "deformable-detr": "SenseTime/deformable-detr",
        "rtdetrv2-r34": "PekingU/rtdetr_v2_r34vd",
    }
    for name, model_id in closed.items():
        def build_closed(imgsz=640, _id=model_id, _name=name, **kw):
            from edgeml.detectors.hf_det import HFClosedSetDetector

            return HFClosedSetDetector(_id, name=_name, imgsz=imgsz, **kw)

        _reg(name, "bakeoff")(build_closed)

    for name, model_id in {
        "owlvit-b32": "google/owlvit-base-patch32",
        "owlvit-b16": "google/owlvit-base-patch16",
        "gdino-base": "IDEA-Research/grounding-dino-base",
    }.items():
        def build_open(imgsz=640, _id=model_id, _name=name, **kw):
            from edgeml.detectors.hf_det import HFOpenVocabDetector

            detector = HFOpenVocabDetector(_id, name=_name, imgsz=imgsz, **kw)
            if _name.startswith("owlvit"):
                detector.imgsz = detector.processor.image_processor.size
                detector.training_data = "CLIP-style web image/text + public detection fine-tuning; overlap not independently audited"
            return detector

        _reg(name, "bakeoff")(build_open)

    for name, variant in {"rfdetr-nano": "RFDETRNano", "rfdetr-small": "RFDETRSmall", "rfdetr-base": "RFDETRBase"}.items():
        def build_rf(imgsz=640, _variant=variant, _name=name, **kw):
            from edgeml.detectors.bakeoff import RFDetector

            return RFDetector(_variant, name=_name, **kw)

        _reg(name, "bakeoff")(build_rf)

    def build_florence(imgsz=640, device=None, **kw):
        from edgeml.detectors.bakeoff import FlorenceDetector

        return FlorenceDetector(device=device, **kw)

    _reg("florence2-base-ft", "bakeoff")(build_florence)
    for name in ("gemini-2.5-flash", "gemini-2.5-pro", "cloud-vision"):
        def build_hosted(imgsz=640, device=None, _name=name, **kw):
            from edgeml.detectors.bakeoff import HostedDetector

            return HostedDetector(name=_name, device=device, **kw)

        _reg(name, "hosted")(build_hosted)


def register_classifiers():
    from edgeml.embed.clip_embedder import MODELS

    MODELS.update({
        "clip-b32-openai": ("ViT-B-32-quickgelu", "openai", 224),
        "clip-b16-openai": ("ViT-B-16-quickgelu", "openai", 224),
        "openclip-b16-laion": ("ViT-B-16", "laion2b_s34b_b88k", 224),
        "openclip-b32-datacomp": ("ViT-B-32", "datacomp_xl_s13b_b90k", 224),
        "siglip2-b16": ("ViT-B-16-SigLIP2", "webli", 224),
        "mobileclip-s2": ("MobileCLIP-S2", "datacompdr", 256),
    })


def main():
    from edgeml.runtime.threads import pin_threads

    pin_threads(4)
    # Ultralytics otherwise overwrites torch's CPU thread limit on device selection.
    try:
        import ultralytics.utils.torch_utils as torch_utils

        torch_utils.NUM_THREADS = 4
    except ImportError:
        pass
    pin_threads(4)
    mode = sys.argv.pop(1)
    if mode == "detectors":
        register_detectors()
        script = "bench_detectors.py"
    elif mode == "classifiers":
        register_classifiers()
        script = "d_eval_zeroshot.py"
    else:
        raise SystemExit("mode must be detectors or classifiers")
    runpy.run_path(str(Path(__file__).with_name(script)), run_name="__main__")


if __name__ == "__main__":
    main()
