#!/usr/bin/env python3
import ipaddress
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


def selected_workloads(selector: dict, namespace: str, workloads: list[dict]) -> list[dict]:
    require(isinstance(selector, dict) and set(selector) <= {"matchLabels"},
            "network policy selectors must use matchLabels")
    labels = selector.get("matchLabels", {})
    require(isinstance(labels, dict), "network policy matchLabels must be a mapping")
    return [workload for workload in workloads
            if workload["metadata"]["namespace"] == namespace
            and all(workload["spec"]["template"]["metadata"]["labels"].get(key) == value
                    for key, value in labels.items())]


def check_network_policies(documents: list[dict], variables: dict[str, dict]) -> int:
    workloads = [doc for doc in documents if doc["kind"] in WORKLOAD_KINDS]
    namespaces = {doc["metadata"]["name"] for doc in documents if doc["kind"] == "Namespace"}
    policies = [doc for doc in documents if doc["kind"] == "NetworkPolicy"]
    denied = set()
    for policy in policies:
        name = policy["metadata"]["name"]
        namespace = policy["metadata"].get("namespace")
        require(namespace in namespaces, f"{name}: namespace is not declared")
        require(policy["apiVersion"] == "networking.k8s.io/v1", f"{name}: wrong NetworkPolicy API")
        spec = policy["spec"]
        require(spec.get("policyTypes") == ["Ingress"] and "egress" not in spec,
                f"{name}: policies must remain ingress-only")
        selector = spec.get("podSelector")
        targets = selected_workloads(selector, namespace, workloads)
        require(bool(targets), f"{name}: target selector matches no workload")
        ingress = spec.get("ingress")
        require(isinstance(ingress, list), f"{name}: ingress must be a list")
        if selector == {} and ingress == []:
            denied.add(namespace)
            continue
        require(bool(selector.get("matchLabels")) and bool(ingress),
                f"{name}: allow policy must select explicit workloads and rules")
        for rule in ingress:
            require(bool(rule.get("from")) and bool(rule.get("ports")),
                    f"{name}: ingress rule must restrict sources and ports")
            for peer in rule["from"]:
                require(set(peer) in ({"podSelector"}, {"ipBlock"}),
                        f"{name}: source must be a local pod selector or routed CIDR")
                if "podSelector" in peer:
                    require(bool(peer["podSelector"].get("matchLabels")),
                            f"{name}: source selector must not match all pods")
                    require(bool(selected_workloads(peer["podSelector"], namespace, workloads)),
                            f"{name}: source selector matches no workload")
                else:
                    require(set(peer["ipBlock"]) == {"cidr"}, f"{name}: unexpected ipBlock fields")
                    cidr = peer["ipBlock"]["cidr"]
                    match = VARIABLE_PATTERN.fullmatch(cidr)
                    require(match is not None and match[1] in variables,
                            f"{name}: routed CIDR must use a declared Zarf variable")
                    network = ipaddress.ip_network(variables[match[1]]["default"], strict=True)
                    require(network.prefixlen > 0, f"{name}: unrestricted source CIDR")
            for port in rule["ports"]:
                require(set(port) == {"protocol", "port"} and port["protocol"] == "TCP"
                        and isinstance(port["port"], int) and 1 <= port["port"] <= 65535,
                        f"{name}: ingress port must be an explicit TCP port")
                for target in targets:
                    _, containers = workload_containers(target)
                    exposed = {entry["containerPort"] for container in containers
                               for entry in container.get("ports", [])}
                    require(port["port"] in exposed, f"{name}: port not exposed by selected workload")
                    services = [doc for doc in documents if doc["kind"] == "Service"
                                and doc["metadata"].get("namespace") == namespace
                                and doc["spec"].get("selector") == target["spec"]["selector"]["matchLabels"]]
                    named_ports = {entry.get("name"): entry["containerPort"] for container in containers
                                   for entry in container.get("ports", [])}
                    require(any(named_ports.get(entry.get("targetPort"), entry.get("targetPort", entry["port"])) == port["port"]
                                for service in services for entry in service["spec"]["ports"]),
                            f"{name}: port has no matching Service target")
    require(namespaces <= denied, "every namespace must have a default-deny ingress policy")
    return len(policies)


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
    undeclared = used_variables - variables
    require(not undeclared, f"undeclared Zarf variables used: {', '.join(sorted(undeclared))}")
    packaged_paths = {path for files in component_files.values() for path in files}
    for path, documents in parsed.items():
        if any(doc["kind"] == "NetworkPolicy" for doc in documents):
            require(path in packaged_paths, f"{path.name}: NetworkPolicy not included in a component")
    policy_count = check_network_policies(
        [doc for documents in parsed.values() for doc in documents],
        {entry["name"]: entry for entry in package["variables"]},
    )

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
    print(f"validated {policy_count} ingress-only NetworkPolicies: namespace default deny, selectors, Service ports and CIDR defaults")


if __name__ == "__main__":
    try:
        check()
    except (KeyError, TypeError, ValueError) as exc:
        print(f"manifest validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
