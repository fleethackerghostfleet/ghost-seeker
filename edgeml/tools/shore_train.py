#!/usr/bin/env python3
"""Budgeted RF-DETR-Small fine-tuning; all validation is frozen DEV only."""
import argparse
import hashlib
import json
import math
import copy
import time
from pathlib import Path


def stop_before_next_epoch(elapsed, last_epoch_seconds, budget_seconds):
    if min(elapsed, last_epoch_seconds, budget_seconds) < 0:
        raise ValueError("Negative wall-clock budget")
    return elapsed + last_epoch_seconds >= budget_seconds


def verify_split(directory, manifests, split):
    m = json.loads((manifests / f"{split}.json").read_text())
    raw = (directory / "_annotations.coco.json").read_bytes()
    if hashlib.sha256(raw).hexdigest() != m["coco_sha256"]:
        raise ValueError(f"Frozen {split} annotation hash mismatch")
    coco = json.loads(raw)
    if coco["info"]["split"] != split:
        raise ValueError("Wrong data partition")
    hashes = {}
    for shard in m["image_shards"]:
        raw = (manifests / shard["file"]).read_bytes()
        if hashlib.sha256(raw).hexdigest() != shard["sha256"]:
            raise ValueError("Image manifest changed")
        hashes.update({r[0]: r[1] for r in json.loads(raw)["images"]})
    if set(hashes) != {im["file_name"] for im in coco["images"]}:
        raise ValueError("Image membership changed")
    for name, digest in hashes.items():
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != digest:
            raise ValueError(f"Image changed: {name}")
    return m["coco_sha256"]


def training_view(coco, image_directory):
    """RF-DETR 1.3's reinitialized one-logit head requires zero-based labels."""
    if coco["categories"] != [{"id": 1, "name": "vessel", "supercategory": "vessel"}]:
        raise ValueError("Expected canonical single-vessel category")
    out = copy.deepcopy(coco)
    out["categories"][0]["id"] = 0
    for a in out["annotations"]:
        if a["category_id"] != 1:
            raise ValueError("Unexpected annotation category")
        a["category_id"] = 0
    for im in out["images"]:
        im["file_name"] = str((image_directory / im["file_name"]).resolve())
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--dataset", type=Path, required=True)
    ap.add_argument("--manifests", type=Path, required=True)
    ap.add_argument("--output", type=Path, required=True)
    ap.add_argument("--resolution", type=int, required=True)
    ap.add_argument("--batch-size", type=int, required=True)
    ap.add_argument("--epochs", type=int, required=True)
    ap.add_argument("--budget-minutes", type=float, required=True)
    ap.add_argument("--gradient-checkpointing", action="store_true")
    args = ap.parse_args()
    if args.resolution % 64 or args.resolution < 512 or args.batch_size < 1 or 16 % args.batch_size or args.epochs < 1 or args.budget_minutes <= 0:
        ap.error("Require resolution multiple of 64 >=512; batch size divides 16; positive epochs/budget")
    if (args.dataset / "test").resolve() != (args.dataset / "valid").resolve():
        ap.error("RF-DETR test loader must alias valid, never locked data")
    hashes = {s: verify_split(args.dataset / s, args.manifests, s) for s in ("train", "valid")}
    args.output.mkdir(parents=True, exist_ok=False)
    adapter = args.output / "roboflow"
    for split in ("train", "valid"):
        folder = adapter / split
        folder.mkdir(parents=True)
        original = json.loads((args.dataset / split / "_annotations.coco.json").read_text())
        (folder / "_annotations.coco.json").write_text(json.dumps(training_view(original, args.dataset / split)))
    (adapter / "test").symlink_to("valid", target_is_directory=True)
    import torch
    from rfdetr import RFDETRSmall

    torch.set_num_threads(4)
    model = RFDETRSmall(device="cuda", resolution=args.resolution, gradient_checkpointing=args.gradient_checkpointing)
    config = {"dataset_dir": str(adapter), "output_dir": str(args.output), "epochs": args.epochs,
              "batch_size": args.batch_size, "grad_accum_steps": 16 // args.batch_size,
              "lr": 1e-4, "lr_encoder": 1.5e-5, "lr_drop": max(1, args.epochs // 2), "warmup_epochs": .25,
              "use_ema": True, "early_stopping": True, "early_stopping_patience": 3,
              "early_stopping_min_delta": .001, "early_stopping_use_ema": False,
              "run_test": False, "tensorboard": False, "wandb": False, "num_workers": 4,
              "multi_scale": False, "expanded_scales": False, "checkpoint_interval": args.epochs}
    (args.output / "shore_config.json").write_text(json.dumps({"training": config, "resolution": args.resolution,
        "gradient_checkpointing": args.gradient_checkpointing, "budget_minutes": args.budget_minutes,
        "initial_weights": "COCO RF-DETR-Small", "partition_sha256": hashes, "seed": 42}, indent=2))
    start = time.monotonic()
    previous = start

    def epoch_end(stats):
        nonlocal previous
        now = time.monotonic()
        loss = float(stats["train_loss"])
        if not math.isfinite(loss):
            raise RuntimeError("Non-finite epoch loss")
        row = {"epoch": stats["epoch"], "loss": loss, "valid_coco": stats.get("test_coco_eval_bbox"),
               "valid_ema_coco": stats.get("ema_test_coco_eval_bbox"), "elapsed_seconds": now - start,
               "epoch_seconds": now - previous, "peak_gpu_gb": torch.cuda.max_memory_allocated() / 2**30}
        with (args.output / "shore_epochs.jsonl").open("a") as stream:
            stream.write(json.dumps(row) + "\n")
        print("SHORE_EPOCH " + json.dumps(row), flush=True)
        if stop_before_next_epoch(now - start, now - previous, args.budget_minutes * 60):
            print("SHORE_BUDGET_STOP before another complete epoch", flush=True)
            model.model.request_early_stop()
        previous = now

    model.callbacks["on_fit_epoch_end"].append(epoch_end)
    model.train(**config)
    (args.output / "SHORE_COMPLETE.json").write_text(json.dumps({"wall_seconds": time.monotonic() - start,
        "checkpoint": "checkpoint_best_total.pth", "selection": "library best valid COCO mAP regular/EMA"}, indent=2))


if __name__ == "__main__":
    main()
