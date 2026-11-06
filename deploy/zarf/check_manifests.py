#!/usr/bin/env python3
import argparse
import csv
import io
import itertools
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlsplit

import yaml


ROOT = Path(__file__).resolve().parent
VARIABLE_PATTERN = re.compile(r"###ZARF_VAR_([A-Z0-9_]+)###")
WORKLOAD_KINDS = {"Deployment", "StatefulSet"}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def read_documents(path: Path, text: str | None = None) -> list[dict]:
    try:
        documents = [doc for doc in yaml.safe_load_all(path.read_text() if text is None else text) if doc is not None]
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


def package_variables(package: dict, overrides: dict[str, str]) -> dict[str, str]:
    entries = {entry["name"]: entry for entry in package.get("variables", [])}
    require(not (overrides.keys() - entries.keys()), "undeclared Zarf variable override")
    for name in ("SHORE_TLS_SECRET", "EDGE_TLS_SECRET", "SHORE_CA_SECRET"):
        require(name in entries and entries[name].get("default") == "",
                f"{name} must be declared with an empty opt-in default")
        require(bool(entries[name].get("pattern")), f"{name} must constrain Secret names")
    variables = {name: entry.get("default", "") for name, entry in entries.items()} | overrides
    for name, entry in entries.items():
        if entry.get("pattern"):
            require(re.fullmatch(entry["pattern"], variables[name]) is not None,
                    f"{name} does not match its Zarf variable pattern")
    return variables


def render_manifests(package: dict, overrides: dict[str, str]) -> dict[Path, list[dict]]:
    command = ["zarf", "dev", "inspect", "manifests", str(ROOT)]
    variables = package_variables(package, overrides)
    if variables:
        values = io.StringIO()
        csv.writer(values, lineterminator="").writerow(f"{key}={value}" for key, value in variables.items())
        command.append(f"--deploy-set-variables={values.getvalue()}")
    try:
        rendered = subprocess.run(command, check=True, capture_output=True, text=True).stdout
    except FileNotFoundError as exc:
        raise ValueError("Zarf v0.89+ is required to render conditional TLS manifests") from exc
    except subprocess.CalledProcessError as exc:
        raise ValueError(f"Zarf manifest rendering failed: {exc.stderr.strip()}") from exc
    sources = {
        (component["name"], f"{group['name']}-{index}.yaml"): ROOT / filename
        for component in package["components"]
        for group in component.get("manifests", [])
        for index, filename in enumerate(group.get("files", []))
    }
    headers = list(re.finditer(r"(?m)^#source: (.+)$", rendered))
    parsed = {}
    for index, header in enumerate(headers):
        source = Path(header[1])
        key = (source.parent.parent.name, source.name)
        require(key in sources, f"unexpected rendered manifest source: {source}")
        path = sources[key]
        end = headers[index + 1].start() if index + 1 < len(headers) else len(rendered)
        require(path not in parsed, f"duplicate rendered manifest source: {path}")
        parsed[path] = read_documents(path, rendered[header.end():end])
    require(set(parsed) == set(sources.values()), "Zarf did not render every packaged manifest")
    return parsed


def check_secret_mount(pod: dict, container: dict, name: str, secret_name: str,
                       mount_path: str, keys: set[str]) -> None:
    volumes = [volume for volume in pod.get("volumes", []) if volume["name"] == name]
    mounts = [mount for mount in container.get("volumeMounts", []) if mount["name"] == name]
    if not secret_name:
        require(not volumes and not mounts, f"disabled {name} must not mount a Secret")
        return
    require(len(volumes) == len(mounts) == 1, f"{name} requires one volume and mount")
    secret = volumes[0].get("secret", {})
    require(secret.get("secretName") == secret_name, f"{name} Secret must match its Zarf variable")
    require(secret.get("optional", False) is False, f"configured {name} Secret must be required")
    require(secret.get("defaultMode") == 0o440, f"{name} must use group-readable mode 0440")
    require({item["key"]: item["path"] for item in secret.get("items", [])} == {key: key for key in keys},
            f"{name} must project the required Secret keys at the flag paths")
    mount = mounts[0]
    require(mount.get("mountPath") == mount_path and mount.get("readOnly") is True
            and not mount.get("subPath") and not mount.get("subPathExpr"),
            f"{name} must mount its whole directory read-only at {mount_path}")
    require(type(pod.get("securityContext", {}).get("fsGroup")) is int,
            f"{name} requires fsGroup so the non-root process can read mode 0440")


def check_tls_wiring(documents: list[dict], variables: dict[str, str]) -> None:
    workloads = {doc["metadata"]["name"]: doc for doc in documents if doc["kind"] in WORKLOAD_KINDS}
    for name in ("shore", "edge", "operator"):
        require(name in workloads, f"TLS wiring: missing {name} workload")
        pod, containers = workload_containers(workloads[name])
        container = next(item for item in containers if item["name"] == name)
        listener_secret = variables[f"{name.upper()}_TLS_SECRET"] if name != "operator" else ""
        ca_secret = variables["SHORE_CA_SECRET"] if name != "shore" else ""
        expected = []
        if listener_secret:
            expected.extend(["-tls-cert=/run/listener-tls/tls.crt", "-tls-key=/run/listener-tls/tls.key"])
        if ca_secret:
            expected.append("-shore-ca=/run/shore-ca/ca.crt")
        flags = [arg for arg in container.get("args", [])
                 if arg.split("=", 1)[0] in {"-tls-cert", "-tls-key", "-shore-ca"}]
        require(sorted(flags) == sorted(expected), f"{name}: TLS flags must match the Secret variables")
        require(not any(entry["name"] in {"GS_TLS_CERT", "GS_TLS_KEY", "GS_SHORE_CA"}
                        for entry in container.get("env", [])),
                f"{name}: TLS environment must not bypass the conditional flags")
        check_secret_mount(pod, container, "listener-tls", listener_secret,
                           "/run/listener-tls", {"tls.crt", "tls.key"})
        check_secret_mount(pod, container, "shore-ca", ca_secret, "/run/shore-ca", {"ca.crt"})
        for probe_name in ("readinessProbe", "livenessProbe"):
            probe = container.get(probe_name, {}).get("httpGet", {})
            require(probe.get("scheme", "HTTP") == ("HTTPS" if listener_secret else "HTTP"),
                    f"{name}: {probe_name} scheme must match listener TLS")
            require(probe.get("port") == "http" and probe.get("path") == ("/" if name == "operator" else "/healthz"),
                    f"{name}: {probe_name} must retain its health endpoint")
    for doc in documents:
        if doc["kind"] == "ConfigMap":
            require(not ({"GS_TLS_CERT", "GS_TLS_KEY", "GS_SHORE_CA"} & doc.get("data", {}).keys()),
                    "ConfigMaps must not bypass the conditional TLS flags")
    edge_config = next(doc for doc in documents if doc["kind"] == "ConfigMap"
                       and doc["metadata"]["name"] == "edge-config")
    _, operator_containers = workload_containers(workloads["operator"])
    operator = next(item for item in operator_containers if item["name"] == "operator")
    operator_urls = [arg.removeprefix("-shore=") for arg in operator["args"] if arg.startswith("-shore=")]
    require(len(operator_urls) == 1, "operator must have one shore URL")
    for variable, actual, default in (
        ("SHORE_URL", edge_config["data"]["SHORE_URL"], "http://shore.ghost-seeker.svc.cluster.local:8080"),
        ("OPERATOR_SHORE_URL", operator_urls[0], "http://shore:8080"),
    ):
        expected = variables[variable]
        if variables["SHORE_TLS_SECRET"] and expected == default:
            expected = expected.replace("http://", "https://", 1)
        require(actual == expected, f"{variable}: rendered shore URL must match the selected TLS mode")
        parsed = urlsplit(actual)
        require(parsed.scheme in {"http", "https"} and bool(parsed.hostname), f"{variable}: invalid HTTP(S) URL")
        local_hosts = {"shore", "shore.ghost-seeker", "shore.ghost-seeker.svc",
                       "shore.ghost-seeker.svc.cluster.local"}
        if variables["SHORE_TLS_SECRET"] and parsed.hostname in local_hosts:
            require(parsed.scheme == "https", f"{variable}: local TLS shore requires an HTTPS URL")


def check(overrides: dict[str, str] | None = None) -> dict[Path, list[dict]]:
    package_file = ROOT / "zarf.yaml"
    package = yaml.safe_load(package_file.read_text())
    require(package.get("apiVersion") == "zarf.dev/v1alpha1", "zarf.yaml must use zarf.dev/v1alpha1")
    require(package.get("kind") == "ZarfPackageConfig", "zarf.yaml must be a ZarfPackageConfig")
    overrides = overrides or {}
    variables = package_variables(package, overrides)
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
                if path.name in {"shore.yaml", "edge.yaml", "operator.yaml"}:
                    require(manifest_group.get("template") is True,
                            f"{filename}: conditional TLS manifests require template: true")
                files.append(path)
        require(bool(files), f"{component['name']}: no manifest files declared")
        component_files[component["name"]] = files

    all_manifest_paths = sorted((ROOT / "manifests").glob("*.yaml"))
    require(bool(all_manifest_paths), "no manifests/*.yaml files found")
    parsed = render_manifests(package, overrides)
    require(set(parsed) == set(all_manifest_paths), "every manifest must be packaged")
    check_tls_wiring([document for docs in parsed.values() for document in docs], variables)
    for filename, config_name in (("shore.yaml", "shore-config"), ("edge.yaml", "edge-config")):
        documents = parsed[ROOT / "manifests" / filename]
        config = next(
            (document for document in documents
             if document["kind"] == "ConfigMap" and document["metadata"]["name"] == config_name),
            None,
        )
        require(config is not None, f"{filename}: ConfigMap/{config_name} is missing")
        require(bool(config.get("data", {}).get("GS_PUBLIC_HOSTS", "").strip()),
                f"{filename}: ConfigMap/{config_name} must set non-empty GS_PUBLIC_HOSTS")
    used_variables: set[str] = set()
    for path in all_manifest_paths:
        used_variables.update(VARIABLE_PATTERN.findall(path.read_text()))
    undeclared = used_variables - variables.keys()
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
    return parsed


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--output-dir", type=Path, help="save the validated HTTP/TLS renders for kubeconform")
    args = parser.parse_args()
    try:
        names = ("SHORE_TLS_SECRET", "EDGE_TLS_SECRET", "SHORE_CA_SECRET")
        for modes in itertools.product((False, True), repeat=3):
            overrides = {name: f"check-{name.lower().replace('_', '-')}" for name, enabled in zip(names, modes) if enabled}
            parsed = check(overrides)
            if args.output_dir:
                profile = args.output_dir / "-".join("on" if mode else "off" for mode in modes)
                profile.mkdir(parents=True, exist_ok=True)
                for path, documents in parsed.items():
                    (profile / path.name).write_text(yaml.safe_dump_all(documents, sort_keys=False))
        print("validated all 8 plaintext/listener TLS/CA combinations with Zarf's native renderer")
    except (KeyError, TypeError, ValueError) as exc:
        print(f"manifest validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
