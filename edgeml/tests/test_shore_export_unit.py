"""Exercise immutable freeze publishing against in-memory cloud SDK fakes."""
import base64
import hashlib
import importlib.util
import io
import json
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest
import yaml
from PIL import Image


@pytest.fixture
def freeze(tmp_path, monkeypatch):
    tools = Path(__file__).parents[1] / "tools"
    spec = importlib.util.spec_from_file_location("shore_export_unit", tools / "shore_export.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    policy = yaml.safe_load((tools.parent / "configs/splits.yaml").read_text())
    for kind in ("dev", "test"):
        policy[kind]["fvessel_clip10"] = [kind + "_vessel"]
        policy[kind]["ghost_fleet_5fps"] = [kind + "_fleet"]
    policy["dev"]["smd_vis_onboard_sample"] = ["MVI_0790"]
    policy["test"]["smd_vis_onboard_sample"] = ["MVI_0797"]
    splits = tmp_path / "splits.yaml"
    splits.write_text(yaml.safe_dump(policy))
    output, manifests = tmp_path / "output", tmp_path / "manifests"
    prefix = "edgeml/shore/unit/"
    objects, images, annotations, uploads, queries = {}, [], [], [], []

    def png(value):
        stream = io.BytesIO()
        Image.new("RGB", (64, 64), (value, value, value)).save(stream, format="PNG")
        return stream.getvalue()

    bright, dark = png(150), png(20)

    def add_image(dataset, source, upstream, raw=bright):
        iid = str(len(images))
        name = f"source/{iid}.png"
        objects[name] = raw
        images.append({"dataset_id": dataset, "image_id": iid, "source_video": source, "eval_split": upstream,
                       "gcs_uri": f"gs://{mod.BUCKET}/{name}", "sha256": mod.sha(raw)})
        annotations.append({"dataset_id": dataset, "image_id": iid, "label": "boat", "label_norm": "vessel", "box": [0, 0, 20, 20]})

    for source in sum(policy["singapore_maritime_v2"].values(), []):
        add_image("singapore_maritime", source, "quarantine", dark if not images else bright)
    for split in ("dev", "test"):
        add_image("seaships7000", "still_" + split, split)
    add_image("smd_vis_onboard_sample", "mvi0790", "dev")
    add_image("smd_vis_onboard_sample", "mvi0797", "test")
    annotations.extend([
        {"dataset_id": images[0]["dataset_id"], "image_id": "0", "label": "horizon", "label_norm": None, "box": []},
        {"dataset_id": images[0]["dataset_id"], "image_id": "0", "label": "person", "label_norm": "object", "box": [0, 0, 5, 5]}])
    quarantine = []
    for ds in ("fvessel_clip10", "ghost_fleet_5fps"):
        for seq in policy["dev"][ds] + policy["test"][ds]:
            base = mod.CLEAN + f"mot/{ds}/{seq}/"
            objects[base + "seqinfo.ini"] = b"[Sequence]\nframerate=5\n"
            objects[base + "gt/labels.json"] = mod.encode({"classes": {"1": "boat", "2": "person"}})
            objects[base + "gt/gt.txt"] = b"\n1,9,0,0,8,8,1,1,1\n2,10,0,0,5,5,1,2,1\n"
            imgbase = f"edgeml/eval/{ds}/{seq}/img1/"
            objects[imgbase + "000001.png"] = bright
            objects[imgbase + "000002.png"] = dark
            objects[imgbase + "README.txt"] = b"not a frame"
            quarantine.append({"status": "quarantine", "ref": f"gs://{mod.BUCKET}/{imgbase}000002.png"})
    objects[mod.CLEAN + "selection.jsonl"] = b"\n".join(mod.encode(row) for row in quarantine)

    class Blob:
        def __init__(self, name):
            self.name = name

        def exists(self):
            return self.name in objects

        def download_as_bytes(self):
            return objects[self.name]

        @property
        def md5_hash(self):
            return base64.b64encode(hashlib.md5(objects[self.name]).digest()).decode()

        def upload_from_string(self, data, **kwargs):
            assert kwargs == {"if_generation_match": 0, "timeout": 180}
            if self.name in objects:
                raise AssertionError("attempted overwrite: " + self.name)
            objects[self.name] = data
            uploads.append(self.name)

    bucket = SimpleNamespace(blob=Blob, get_blob=lambda name: Blob(name) if name in objects else None)

    class Storage:
        def __init__(self, **kwargs):
            assert kwargs == {"project": mod.PROJECT, "credentials": "fake-auth"}

        def bucket(self, name):
            assert name == mod.BUCKET
            return bucket

        def list_blobs(self, name, prefix, **kwargs):
            assert name == mod.BUCKET
            return [Blob(key) for key in sorted(objects) if key.startswith(prefix)][:kwargs.get("max_results")]

    class BigQuery:
        def __init__(self, **kwargs):
            assert kwargs == {"project": mod.PROJECT, "credentials": "fake-auth"}

        def query(self, sql):
            queries.append(sql)
            rows = images if "images_clean_v2" in sql else annotations
            return SimpleNamespace(result=lambda: [dict(row) for row in rows])

    google, cloud = ModuleType("google"), ModuleType("google.cloud")
    cloud.storage = SimpleNamespace(Client=Storage)
    cloud.bigquery = SimpleNamespace(Client=BigQuery)
    google.cloud = cloud
    monkeypatch.setitem(sys.modules, "google", google)
    monkeypatch.setitem(sys.modules, "google.cloud", cloud)
    inventory = ModuleType("inventory_gcs")
    inventory.credentials = lambda: "fake-auth"
    monkeypatch.setitem(sys.modules, "inventory_gcs", inventory)
    argv = ["shore_export", "--prefix", prefix, "--output", str(output), "--manifests", str(manifests),
            "--splits", str(splits), "--workers", "1"]
    monkeypatch.setattr(sys, "argv", argv)
    return SimpleNamespace(mod=mod, objects=objects, images=images, annotations=annotations, output=output,
                           manifests=manifests, prefix=prefix, argv=argv, uploads=uploads, queries=queries)


def test_freeze_partitions_hashes_labels_and_immutable_artifacts(freeze):
    f = freeze
    f.mod.main()
    assert len(f.queries) == 2
    complete = json.loads((f.output / "COMPLETE.json").read_text())
    assert set(complete["splits"]) == {"train", "valid", "test_sg", "test_smd", "test_seaships", "test_fvessel", "test_gf"}
    total = 0
    twilight = None
    dark = []
    ignored = []
    for split in complete["splits"]:
        manifest_path = f.manifests / (split + ".json")
        manifest = json.loads(manifest_path.read_text())
        destination = f.mod.destination(split)
        raw = (f.output / destination / "_annotations.coco.json").read_bytes()
        coco = json.loads(raw)
        assert f.mod.sha(raw) == manifest["coco_sha256"]
        assert manifest["uri"] == f"gs://{f.mod.BUCKET}/{f.prefix}{destination}/_annotations.coco.json"
        assert f.objects[f.prefix + "manifests/" + manifest_path.name] == manifest_path.read_bytes()
        assert manifest["images"] == len(coco["images"])
        assert coco["info"]["split"] == split
        total += manifest["images"]
        entries = []
        for shard in manifest["image_shards"]:
            data = (f.manifests / shard["file"]).read_bytes()
            assert f.mod.sha(data) == shard["sha256"]
            entries.extend(json.loads(data)["images"])
        assert len(entries) == manifest["images"]
        for entry in entries:
            assert f.mod.sha(f.objects[f.prefix + destination + "/" + entry[0]]) == entry[1]
        for image in coco["images"]:
            assert image["width"] == image["height"] == 64
            if image["source_video"] == "mvi1578vis":
                twilight = image
            if image["mean_y"] < 60:
                dark.append(image)
        ignored.extend(a for a in coco["annotations"] if a["ignore"])
        assert manifest["vessel_boxes"] == sum(not a["ignore"] for a in coco["annotations"])
    assert total == 44
    assert twilight["low_light"] and twilight["mean_y"] > 60
    assert twilight["low_light_method"] == "visual_twilight_source"
    assert len(dark) == 1 and dark[0]["low_light"]
    assert dark[0]["low_light_method"] == "mean_y_lt_60"
    assert len(ignored) == 1 and ignored[0]["iscrowd"] == 1
    assert len(f.uploads) == len(set(f.uploads))
    assert f.uploads[-1] == f.prefix + "COMPLETE.json"


def test_resume_only_verifies_existing_bytes_and_publishes_missing_completion(freeze):
    f = freeze
    f.mod.main()
    del f.objects[f.prefix + "COMPLETE.json"]
    before = dict(f.objects)
    count = len(f.uploads)
    f.argv.append("--resume-incomplete")
    f.mod.main()
    assert f.objects == {**before, f.prefix + "COMPLETE.json": (f.output / "COMPLETE.json").read_bytes()}
    assert f.uploads[count:] == [f.prefix + "COMPLETE.json"]
    assert len(f.queries) == 2


def test_resume_rejects_different_existing_image_bytes_without_overwrite(freeze):
    f = freeze
    f.mod.main()
    del f.objects[f.prefix + "COMPLETE.json"]
    image = next(name for name in f.uploads if name.endswith(".png"))
    f.objects[image] = b"different bytes"
    f.argv.append("--resume-incomplete")
    with pytest.raises(ValueError, match="Resume bytes differ"):
        f.mod.main()
    assert f.objects[image] == b"different bytes"
    assert f.prefix + "COMPLETE.json" not in f.objects


@pytest.mark.parametrize("failure", ["prefix", "existing", "completed", "empty", "sha", "unassigned", "bbox", "upstream"])
def test_rejected_freezes_never_publish_completion(freeze, failure):
    f = freeze
    expected, match = ValueError, None
    if failure == "prefix":
        f.argv[f.argv.index("--prefix") + 1] = "edgeml/shore/../escape"
        expected = SystemExit
    elif failure == "existing":
        f.objects[f.prefix + "marker"] = b"existing freeze"
        expected = SystemExit
    elif failure == "completed":
        f.objects[f.prefix + "COMPLETE.json"] = b"existing completion"
        f.argv.append("--resume-incomplete")
        expected = SystemExit
    elif failure == "empty":
        f.images.clear()
        expected = AssertionError
    elif failure == "sha":
        f.images[0]["sha256"] = "0" * 64
        match = "SHA changed"
    elif failure == "unassigned":
        f.images.append({**f.images[-1], "source_video": "unknown-source"})
        match = "Unassigned source"
    elif failure == "upstream":
        next(im for im in f.images if im["dataset_id"] == "seaships7000")["eval_split"] = "train"
        match = "Unexpected SeaShips split"
    else:
        f.annotations[0]["box"] = [0, 0, 0, 20]
        match = "Invalid bbox"
    with pytest.raises(expected, match=match):
        f.mod.main()
    if failure == "completed":
        assert f.objects[f.prefix + "COMPLETE.json"] == b"existing completion"
    else:
        assert f.prefix + "COMPLETE.json" not in f.objects
    assert not (f.output / "COMPLETE.json").exists()
