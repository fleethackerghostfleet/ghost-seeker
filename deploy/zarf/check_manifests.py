#!/usr/bin/env python3
import re
import sys
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parent
VARIABLE_PATTERN = re.compile(r"###ZARF_VAR_([A-Z0-9_]+)###")
WORKLOAD_KINDS = {"Deployment", "StatefulSet"}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def read_documents(path: Path) -> list[dict]:
    try:
        documents = [doc for doc in yaml.safe_load_all(path.read_text()) if doc is not None]
    except yaml.YAMLError as exc:
        raise ValueError(f"{path.relative_to(ROOT)}: invalid YAML: {exc}") from exc
    for index, document in enumerate(documents, start=1):
        require(isinstance(document, dict), f"{path.relative_to(ROOT)} document {index} is not a mapping")
        for key in ("apiVersion", "kind"):
            require(bool(document.get(key)), f"{path.relative_to(ROOT)} document {index} missing {key}")
        require(bool(document.get("metadata", {}).get("name")),
                f"{path.relative_to(ROOT)} document {index} missing metadata.name")
    return documents


def workload_containers(document: dict) -> tuple[dict, list[dict]]:
    pod_spec = document.get("spec", {}).get("template", {}).get("spec", {})
    containers = list(pod_spec.get("containers", [])) + list(pod_spec.get("initContainers", []))
    return pod_spec, containers


def check() -> None:
    package_file = ROOT / "zarf.yaml"
    package = yaml.safe_load(package_file.read_text())
    require(package.get("apiVersion") == "zarf.dev/v1alpha1", "zarf.yaml must use zarf.dev/v1alpha1")
    require(package.get("kind") == "ZarfPackageConfig", "zarf.yaml must be a ZarfPackageConfig")
    variables = {entry["name"] for entry in package.get("variables", [])}
    declared = {
        component["name"]: set(component.get("images", []))
        for component in package.get("components", [])
    }
    require(bool(declared), "zarf.yaml has no components")

    component_files: dict[str, list[Path]] = {}
    for component in package["components"]:
        files = []
        for manifest_group in component.get("manifests", []):
            for filename in manifest_group.get("files", []):
                path = ROOT / filename
                require(path.is_file(), f"{component['name']}: manifest file missing: {filename}")
                files.append(path)
        require(bool(files), f"{component['name']}: no manifest files declared")
        component_files[component["name"]] = files

    all_manifest_paths = sorted((ROOT / "manifests").glob("*.yaml"))
    require(bool(all_manifest_paths), "no manifests/*.yaml files found")
    parsed = {path: read_documents(path) for path in all_manifest_paths}
    used_variables: set[str] = set()
    for path in all_manifest_paths:
        used_variables.update(VARIABLE_PATTERN.findall(path.read_text()))
    undeclared = used_variables - variables
    require(not undeclared, f"undeclared Zarf variables used: {', '.join(sorted(undeclared))}")

    for component, files in component_files.items():
        manifest_images: set[str] = set()
        for path in files:
            for document in parsed[path]:
                if document["kind"] not in WORKLOAD_KINDS:
                    continue
                pod_spec, containers = workload_containers(document)
                require(bool(containers), f"{component}: {document['kind']}/{document['metadata']['name']} has no containers")
                for container in containers:
                    name = container.get("name", "<unnamed>")
                    image = container.get("image")
                    require(bool(image), f"{component}: container {name} has no image")
                    manifest_images.add(image)
                    require(bool(container.get("resources", {}).get("limits")),
                            f"{component}: container {name} has no resource limits")
                    require(bool(container.get("readinessProbe")),
                            f"{component}: container {name} has no readinessProbe")
                    require(bool(container.get("livenessProbe")),
                            f"{component}: container {name} has no livenessProbe")
                    container_security = container.get("securityContext", {})
                    pod_security = pod_spec.get("securityContext", {})
                    require(container_security.get("runAsNonRoot") is True or pod_security.get("runAsNonRoot") is True,
                            f"{component}: container {name} does not set runAsNonRoot")
        listed_images = declared[component]
        require(manifest_images <= listed_images,
                f"{component}: manifest images missing from images list: {', '.join(sorted(manifest_images - listed_images))}")
        require(listed_images <= manifest_images,
                f"{component}: images list has no matching manifest container: {', '.join(sorted(listed_images - manifest_images))}")

    print(
        f"validated {len(all_manifest_paths)} manifest files across {len(component_files)} components; "
        f"{len(used_variables)} Zarf variables declared"
    )


if __name__ == "__main__":
    try:
        check()
    except (KeyError, TypeError, ValueError) as exc:
        print(f"manifest validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
