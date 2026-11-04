"""Export zoo detectors to ONNX at a fixed input size for CPU benchmarking (workstream D).

Writes <out>/<name>/model.onnx + export.json (settings, opset, input/output names, source checkpoint, sha256)
and optionally syncs to gs://.../edgeml/models/<name>/.

  python edgeml/tools/export_onnx.py --models yolo11n,yolo26n,dfine-n,yolov8s-world --imgsz 640 \
      --out ~/exports --gcs gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from edgeml.detectors import build

OPSET = 17


def sha256(p: Path) -> str:
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def export_ultralytics(det, imgsz: int, dst: Path) -> dict:
    # ultralytics exporter: static shape, fp32, simplified with onnxslim; YOLO-World keeps the prompt classes set on the model
    path = det.model.export(format="onnx", imgsz=imgsz, dynamic=False, half=False, simplify=True, opset=OPSET, nms=False)
    shutil.move(path, dst)
    return {"exporter": "ultralytics.export", "weights": det.weights, "nms_in_graph": False,
            "preprocess": "letterbox to imgsz (pad 114), BGR->RGB, /255, NCHW fp32",
            "postprocess": "ultralytics head output [1, 4+nc, anchors] (xywh+cls); NMS outside the graph",
            "prompts": getattr(det, "prompts", None)}


def _freeze_sincos(model, x):
    """RT-DETR/D-FINE build their encoder position embedding in float64 at every forward; ORT has no float64
    Cos/Sin kernel. With a fixed input size the embedding is a constant, so cache it from one eager forward and
    return the cached tensor while tracing -> it becomes an initializer."""
    import importlib

    import torch

    for modname in ("transformers.models.d_fine.modeling_d_fine", "transformers.models.rt_detr_v2.modeling_rt_detr_v2",
                    "transformers.models.rt_detr.modeling_rt_detr"):
        try:
            mod = importlib.import_module(modname)
        except Exception:  # noqa: BLE001
            continue
        orig = getattr(mod, "build_2d_sincos_position_embedding", None)
        if orig is None or getattr(orig, "_frozen", False):
            continue
        cache = {}

        def patched(*a, _orig=orig, _cache=cache, **k):
            key = (tuple(int(v) if isinstance(v, (int, torch.Tensor)) and not isinstance(v, bool) else str(v) for v in a),
                   tuple(sorted((kk, str(vv)) for kk, vv in k.items())))
            if key not in _cache:
                _cache[key] = _orig(*a, **k).detach().clone()
            return _cache[key]

        patched._frozen = True
        mod.build_2d_sincos_position_embedding = patched
        for cls in vars(mod).values():  # the encoder calls it as a staticmethod on the class
            if isinstance(cls, type) and "build_2d_sincos_position_embedding" in vars(cls):
                cls.build_2d_sincos_position_embedding = staticmethod(patched)
    # transformers>=5 wraps the same function in an nn.Module (`encoder.aifi.N.position_embedding`): cache its output too
    for name, sub in model.named_modules():
        if name.endswith("position_embedding") and "encoder" in name and not getattr(sub, "_frozen", False):
            cache = {}
            orig_fwd = sub.forward

            def fwd(*a, _o=orig_fwd, _c=cache, **k):
                key = (tuple(str(getattr(v, "shape", v)) for v in a), tuple(sorted((kk, str(getattr(vv, "shape", vv))) for kk, vv in k.items())))
                if key not in _c:
                    _c[key] = _o(*a, **k).detach().clone()
                return _c[key]

            sub.forward = fwd
            sub._frozen = True
    with torch.no_grad():
        model(pixel_values=x)


def _trig_to_f32_if_needed(path: Path):
    """ORT CPU has no float64 Cos/Sin kernel; if the graph still has them, compute them in float32."""
    import onnx
    import onnxruntime as ort

    try:
        ort.InferenceSession(str(path), providers=["CPUExecutionProvider"])
        return
    except Exception as e:
        if "Cos" not in str(e) and "Sin" not in str(e):
            raise
    m = onnx.load(str(path))
    nodes = []
    for n in m.graph.node:
        if n.op_type in ("Cos", "Sin"):
            i, o = n.input[0], n.output[0]
            nodes += [onnx.helper.make_node("Cast", [i], [o + "_in_f32"], to=onnx.TensorProto.FLOAT, name=n.name + "_ci"),
                      onnx.helper.make_node(n.op_type, [o + "_in_f32"], [o + "_f32"], name=n.name),
                      onnx.helper.make_node("Cast", [o + "_f32"], [o], to=onnx.TensorProto.DOUBLE, name=n.name + "_co")]
        else:
            nodes.append(n)
    del m.graph.node[:]
    m.graph.node.extend(nodes)
    onnx.save(m, str(path))
    print("rewrote float64 Cos/Sin to float32 in", path)


def export_hf(det, imgsz: int, dst: Path) -> dict:
    import torch

    model = det.model.eval().float().cpu()
    x = torch.randn(1, 3, imgsz, imgsz)
    _freeze_sincos(model, x)
    torch.onnx.export(model, (x,), str(dst), input_names=["pixel_values"], output_names=["logits", "pred_boxes"],
                      opset_version=OPSET, dynamo=False, do_constant_folding=True)
    try:
        import onnxslim

        onnxslim.slim(str(dst), str(dst))
    except Exception as e:  # noqa: BLE001
        print("onnxslim skipped:", e)
    _trig_to_f32_if_needed(dst)
    import onnxruntime as ort

    with torch.no_grad():
        ref = model(pixel_values=x)
    sess = ort.InferenceSession(str(dst), providers=["CPUExecutionProvider"])
    out = sess.run(None, {"pixel_values": x.numpy()})
    print("onnx vs torch max|d|: logits", float(abs(out[0] - ref.logits.numpy()).max()), "boxes", float(abs(out[1] - ref.pred_boxes.numpy()).max()))
    return {"exporter": "torch.onnx.export", "hf_model_id": det.model_id, "nms_in_graph": False,
            "preprocess": f"resize to {imgsz}x{imgsz} (no letterbox), BGR->RGB, /255, ImageNet-free (RT-DETR/D-FINE use raw 0-1), NCHW fp32",
            "postprocess": "logits [1,Q,nc] sigmoid; pred_boxes [1,Q,4] cxcywh normalized; no NMS needed (set-prediction)"}


def export_copy(det, dst: Path) -> dict:
    shutil.copy(det.onnx_path, dst)
    return {"exporter": "upstream onnx (copied)", "source": det.onnx_path, "nms_in_graph": False}


RAW_ONNX_META = {
    "yolox-tiny": ("resize+pad (bottom/right, 114) to 416x416, BGR, no normalisation (YOLOX takes 0-255 BGR), NCHW fp32",
                   "raw head [1, 3549, 85] = (xy, wh, obj, 80 cls) per anchor on strides 8/16/32; grid/stride decode + sigmoid obj*cls + NMS outside the graph"),
    "nanodet-plus": ("resize (letterbox) to 416x416, BGR->RGB, ImageNet mean/std normalisation, NCHW fp32",
                     "raw head per stride [1, anchors, 80 cls + 4*(reg_max+1) DFL] (outputs 792/814/...); DFL softmax decode + NMS outside the graph"),
}


def _fixup(meta: dict, path: Path) -> dict:
    """Make export.json self-describing even for graphs we did not trace ourselves."""
    import onnx

    m = onnx.load(str(path), load_external_data=False)
    meta["opset"] = max((o.version for o in m.opset_import if o.domain in ("", "ai.onnx")), default=meta.get("opset"))
    if meta["name"] in RAW_ONNX_META and (not meta.get("preprocess") or not meta.get("postprocess")):
        meta["preprocess"], meta["postprocess"] = RAW_ONNX_META[meta["name"]]
    outs = meta.get("outputs") or []
    if outs and len(outs[0][1]) == 3 and outs[0][1][2] == 6:
        meta["postprocess"] = "end-to-end (NMS-free) head: output [1, 300, 6] = xyxy (input px), score, class; already top-300, no NMS needed"
        meta["nms_in_graph"] = "not needed (NMS-free head)"
    meta["head_scope"] = _head_scope(path)
    return meta


def _head_scope(path: Path) -> str:
    """Name prefix of the detection-head nodes (what to keep FP32 when quantising). Ultralytics: last `/model.N/`
    module; HF RT-DETR/D-FINE: `/model/decoder/`; raw ONNX zoo graphs: no scope info (head = everything after the
    last Concat that produces the output)."""
    import re

    import onnx

    m = onnx.load(str(path), load_external_data=False)
    idx = [int(x) for n in m.graph.node for x in re.findall(r"^/model\.(\d+)/", n.name)]
    if idx:
        return f"/model.{max(idx)}/"
    if any(n.name.startswith("/model/decoder") for n in m.graph.node):
        return "/model/decoder/ (+ /model/encoder/enc_output, enc_score_head, enc_bbox_head)"
    return "n/a (no module scopes in graph)"


HEAD_NOTE = {
    "ultralytics": "Detection head IS in the graph: output is decoded boxes (xywh, input-pixel units) + per-class scores, "
                   "no NMS. Keep the head scope (below) in FP32 when quantising (Workstream D saw 4-7 AP50 loss otherwise).",
    "hf": "Full model incl. transformer decoder/head in the graph; set prediction, no NMS. Boxes cxcywh normalised 0-1.",
    "onnx": "Upstream ONNX graph; head is in the graph but outputs are RAW (grid/DFL decode + NMS done outside; see postprocess).",
}


def readme(meta: dict) -> str:
    kind = "ultralytics" if "ultralytics" in str(meta.get("exporter", "")) or "letterbox" in str(meta.get("preprocess", "")) else "hf" if "logits" in str(meta.get("postprocess", "")) else "onnx"
    info = meta.get("detector_info", {})
    lines = [
        f"# {meta['name']} (ONNX export for CPU benchmarking, Workstream B -> D)", "",
        f"- source checkpoint: `{meta.get('source') or meta.get('hf_model_id') or meta.get('weights') or info.get('name')}`",
        f"- training data: {info.get('training_data')}; params: {info.get('params_m')} M; GFLOPs@imgsz: {info.get('gflops')}",
        f"- opset: {meta.get('opset')}; IR: {meta.get('ir_version')}; fixed input: `{meta.get('inputs')}` (batch 1, NCHW fp32)",
        f"- outputs: `{meta.get('outputs')}`",
        f"- preprocess: {meta.get('preprocess')}",
        f"- postprocess: {meta.get('postprocess')}",
        f"- head: {HEAD_NOTE[kind]}",
        f"- head node-name scope: `{meta.get('head_scope')}`",
        f"- label map: {meta.get('label_map')}" + (f"; prompts: {info.get('prompts')}" if info.get("prompts") else ""),
        f"- file: model.onnx {meta.get('size_mb')} MB, sha256 `{meta.get('sha256')}`", "",
        "Export command (edgeml/tools/export_onnx.py):", "",
        f"    python edgeml/tools/export_onnx.py --models {meta['name']} --imgsz {meta.get('imgsz')} --out ~/exports --gcs gs://qwiklabs-gcp-02-34a870014566-ghost-seeker/edgeml/models", "",
        "Accuracy reference (zero-shot, GPU fp16/fp32, same preprocessing): see edgeml/reports/B.md. Any INT8/FP16 variant should be",
        "re-scored with `edgeml/tools/bench_detectors.py` on the same dev sets to measure the AP50 delta.",
    ]
    return "\n".join(lines) + "\n"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--models", required=True)
    ap.add_argument("--imgsz", type=int, default=640)
    ap.add_argument("--out", default=os.path.expanduser("~/exports"))
    ap.add_argument("--gcs", default=None)
    ap.add_argument("--readme-only", action="store_true", help="only (re)write README.md from an existing export.json")
    args = ap.parse_args()
    for name in args.models.split(","):
        try:
            if args.readme_only:
                d = Path(args.out) / name
                meta = _fixup(json.loads((d / "export.json").read_text()), d / "model.onnx")
                (d / "export.json").write_text(json.dumps(meta, indent=1, default=str))
                (d / "README.md").write_text(readme(meta))
                if args.gcs:
                    subprocess.run(["gcloud", "storage", "rsync", "-r", str(d), f"{args.gcs.rstrip('/')}/{name}"], check=False)
                continue
            _export_one(name, args)
        except Exception:  # noqa: BLE001
            import traceback

            traceback.print_exc()


def _export_one(name, args):
    if True:
        det = build(name, imgsz=args.imgsz, device="cpu", half=False) if name not in ("yolox-tiny", "nanodet-plus") else build(name)
        d = Path(args.out) / name
        d.mkdir(parents=True, exist_ok=True)
        dst = d / "model.onnx"
        t0 = time.time()
        if hasattr(det, "onnx_path"):
            meta = export_copy(det, dst)
        elif hasattr(det, "model_id"):
            meta = export_hf(det, args.imgsz, dst)
        else:
            meta = export_ultralytics(det, args.imgsz, dst)
        import onnx
        import onnxruntime as ort

        m = onnx.load(str(dst))
        sess = ort.InferenceSession(str(dst), providers=["CPUExecutionProvider"])
        meta.update({
            "name": name, "detector_info": det.info(), "imgsz": det.info().get("imgsz", args.imgsz), "opset": OPSET,
            "inputs": [(i.name, i.shape) for i in sess.get_inputs()], "outputs": [(o.name, o.shape) for o in sess.get_outputs()],
            "ir_version": m.ir_version, "size_mb": round(dst.stat().st_size / 1e6, 2), "sha256": sha256(dst),
            "export_seconds": round(time.time() - t0, 1), "label_map": "COCO-80" if "COCO" in str(det.info().get("training_data")) else "see prompts",
        })
        meta = _fixup(meta, dst)
        (d / "export.json").write_text(json.dumps(meta, indent=1, default=str))
        (d / "README.md").write_text(readme(meta))
        print(name, meta["inputs"], meta["outputs"], meta["size_mb"], "MB")
        if args.gcs:
            subprocess.run(["gcloud", "storage", "rsync", "-r", str(d), f"{args.gcs.rstrip('/')}/{name}"], check=False)


if __name__ == "__main__":
    main()
