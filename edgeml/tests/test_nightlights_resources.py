import json
from pathlib import Path
import runpy
import shutil

import pytest

from edgeml.nightlights import core


@pytest.mark.parametrize("name", ["colregs_lights.json", "colregs_night_config.json"])
def test_packaged_catalog_matches_shared_catalog(name):
    root = Path(__file__).resolve().parents[2]
    packaged = Path(core.__file__).with_name(name)
    assert json.loads(packaged.read_text()) == json.loads((root / "internal/catalog" / name).read_text())


def test_catalog_loads_outside_repository(tmp_path, monkeypatch):
    installed = tmp_path / "site-packages/edgeml/nightlights"
    shutil.copytree(Path(core.__file__).parent, installed)
    monkeypatch.chdir(tmp_path)
    module = runpy.run_path(str(installed / "core.py"))
    assert module["load_rules"]() == core.load_rules()
    assert module["load_config"]() == core.load_config()


@pytest.mark.parametrize("loader", [core.load_rules, core.load_config])
def test_catalog_missing_file(loader, tmp_path):
    with pytest.raises(FileNotFoundError):
        loader(tmp_path / "missing.json")


@pytest.mark.parametrize("loader", [core.load_rules, core.load_config])
@pytest.mark.parametrize("contents", ["", "not json"])
def test_catalog_invalid_json(loader, tmp_path, contents):
    path = tmp_path / "invalid.json"
    path.write_text(contents)
    with pytest.raises(json.JSONDecodeError):
        loader(path)
