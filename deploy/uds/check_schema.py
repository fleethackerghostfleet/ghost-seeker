"""Fetch the reviewed UDS Core CRD and extract its kubeconform schema."""
import hashlib
import json
from pathlib import Path
import sys
import urllib.request

import yaml


URL = "https://raw.githubusercontent.com/defenseunicorns/uds-core/ee479114762badb92eaf9b851779db55a76b0060/src/pepr/uds-cluster-crds/templates/packages.uds.dev.yaml"
SHA256 = "2e71fd4cb7befc21451bd2789acaf93b7c731f1dfaa41c248453d691f03f7d95"


def main():
    with urllib.request.urlopen(URL, timeout=30) as response:
        raw = response.read()
    if hashlib.sha256(raw).hexdigest() != SHA256:
        raise ValueError("pinned UDS Package CRD checksum mismatch")
    crd = yaml.safe_load(raw)
    schema = next(v["schema"]["openAPIV3Schema"] for v in crd["spec"]["versions"] if v["name"] == "v1alpha1")
    destination = Path(sys.argv[1])
    destination.mkdir(parents=True, exist_ok=True)
    (destination / "package_v1alpha1.json").write_text(json.dumps(schema))
    print("UDS Core 1.14.3 Package CRD checksum verified; kubeconform schema extracted")


if __name__ == "__main__":
    main()
