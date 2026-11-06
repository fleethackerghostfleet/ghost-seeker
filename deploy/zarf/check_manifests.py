#!/usr/bin/env python3
import ipaddress
import argparse
import csv
import io
import itertools
import hashlib
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
        # Preserve unquoted integer-port templates until Zarf substitution.
        raw = re.sub(r'(?m)^(\s+port: *)(###ZARF_VAR_[A-Z0-9_]+###) *$', r'\1"\2"', path.read_text() if text is None else text)
        documents = [doc for doc in yaml.safe_load_all(raw) if doc is not None]
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
    for policy in policies:
        require(policy["spec"].get("policyTypes") in (["Ingress"], ["Egress"]),
                f"{policy['metadata']['name']}: policies must declare exactly one direction")
    denied = set()
    for policy in policies:
        if policy["spec"]["policyTypes"] == ["Egress"]:
            continue
        name = policy["metadata"]["name"]
        namespace = policy["metadata"].get("namespace")
        require(namespace in namespaces, f"{name}: namespace is not declared")
        require(policy["apiVersion"] == "networking.k8s.io/v1", f"{name}: wrong NetworkPolicy API")
        spec = policy["spec"]
        require("egress" not in spec,
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
    check_egress_policies(documents, variables)
    return len(policies)


def egress_port(value: int | str, variables: dict[str, dict]) -> int:
    if isinstance(value, str):
        match = VARIABLE_PATTERN.fullmatch(value)
        require(match is not None and match[1] in variables,
                "egress port must use a declared Zarf variable")
        value = int(variables[match[1]]["default"])
    require(type(value) is int and 1 <= value <= 65535, "egress port must be in 1..65535")
    return value


def check_egress_service_port(target: dict, port: int, documents: list[dict]) -> None:
    _, containers = workload_containers(target)
    exposed = {entry["containerPort"] for container in containers for entry in container.get("ports", [])}
    require(port in exposed, "egress destination port not exposed by selected workload")
    services = [doc for doc in documents if doc["kind"] == "Service"
                and doc["metadata"].get("namespace") == target["metadata"]["namespace"]
                and doc["spec"].get("selector") == target["spec"]["selector"]["matchLabels"]]
    named_ports = {entry.get("name"): entry["containerPort"] for container in containers
                   for entry in container.get("ports", [])}
    require(any(named_ports.get(entry.get("targetPort"), entry.get("targetPort", entry["port"])) == port
                for service in services for entry in service["spec"]["ports"]),
            "egress destination port has no matching Service target")


def check_egress_policies(documents: list[dict], variables: dict[str, dict]) -> None:
    workloads = [doc for doc in documents if doc["kind"] in WORKLOAD_KINDS]
    namespaces = {doc["metadata"]["name"] for doc in documents if doc["kind"] == "Namespace"}
    denied, allowed = set(), set()
    dns: dict[tuple[str, str], set[str]] = {}
    for policy in (doc for doc in documents if doc["kind"] == "NetworkPolicy"
                   and doc["spec"].get("policyTypes") == ["Egress"]):
        name = policy["metadata"]["name"]
        namespace = policy["metadata"].get("namespace")
        require(namespace in namespaces, f"{name}: namespace is not declared")
        require(policy["apiVersion"] == "networking.k8s.io/v1", f"{name}: wrong NetworkPolicy API")
        spec = policy["spec"]
        require("ingress" not in spec, f"{name}: egress-only policy must not contain ingress")
        selector = spec.get("podSelector")
        targets = selected_workloads(selector, namespace, workloads)
        rules = spec.get("egress")
        require(isinstance(rules, list), f"{name}: egress must be a list")
        if selector == {} and rules == []:
            denied.add(namespace)
            continue
        require(bool(targets), f"{name}: target selector matches no workload")
        require(bool(selector.get("matchLabels")) and bool(rules),
                f"{name}: egress allow must select explicit workloads and rules")
        target_keys = {(namespace, target["metadata"]["name"]) for target in targets}
        allowed.update(target_keys)
        for rule in rules:
            require(set(rule) == {"to", "ports"} and bool(rule["to"]) and bool(rule["ports"]),
                    f"{name}: egress rule must restrict destinations and ports")
            ports = set()
            for port in rule["ports"]:
                require(set(port) == {"protocol", "port"} and port["protocol"] in {"TCP", "UDP"},
                        f"{name}: egress must use explicit TCP/UDP ports")
                ports.add((port["protocol"], egress_port(port["port"], variables)))
            for peer in rule["to"]:
                if set(peer) == {"namespaceSelector", "podSelector"}:
                    require(peer["namespaceSelector"] == {"matchLabels": {"kubernetes.io/metadata.name": "kube-system"}}
                            and peer["podSelector"] == {"matchLabels": {"k8s-app": "kube-dns"}}
                            and ports <= {("TCP", 53), ("UDP", 53)},
                            f"{name}: DNS must select kube-system kube-dns on TCP/UDP 53")
                    for key in target_keys:
                        dns.setdefault(key, set()).update(protocol for protocol, _ in ports)
                else:
                    require(all(protocol == "TCP" for protocol, _ in ports),
                            f"{name}: non-DNS egress must use TCP")
                    if set(peer) == {"podSelector"}:
                        require(bool(peer["podSelector"].get("matchLabels")), f"{name}: destination must select explicit pods")
                        destinations = selected_workloads(peer["podSelector"], namespace, workloads)
                        require(bool(destinations), f"{name}: destination selector matches no workload")
                        for destination in destinations:
                            for _, port in ports:
                                check_egress_service_port(destination, port, documents)
                    else:
                        require(set(peer) == {"ipBlock"} and set(peer["ipBlock"]) == {"cidr"},
                                f"{name}: destination must be a local pod selector, DNS or routed CIDR")
                        match = VARIABLE_PATTERN.fullmatch(peer["ipBlock"]["cidr"])
                        require(match is not None and match[1] in variables,
                                f"{name}: routed CIDR must use a declared Zarf variable")
                        network = ipaddress.ip_network(variables[match[1]]["default"], strict=True)
                        require(network.prefixlen > 0, f"{name}: unrestricted destination CIDR")
    require(namespaces <= denied, "every namespace must have a default-deny egress policy")
    require(all(dns.get(key) == {"TCP", "UDP"} for key in allowed),
            "every workload allowed egress must reach kube-dns over TCP and UDP 53")


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


def render_manifests(package: dict, overrides: dict[str, str], package_dir: Path = ROOT) -> dict[Path, list[dict]]:
    command = ["zarf", "dev", "inspect", "manifests", str(package_dir)]
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
        (component["name"], f"{group['name']}-{index}.yaml"): (package_dir / filename).resolve()
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
        annotations = workloads[name]["spec"]["template"]["metadata"].get("annotations", {})
        rollout = {"listener-tls-secret": listener_secret} if name != "operator" else {}
        if name != "shore":
            rollout.update({"shore-tls-secret": variables["SHORE_TLS_SECRET"], "shore-ca-secret": ca_secret})
        require(all(annotations.get(f"ghost-seeker.io/{key}") == value for key, value in rollout.items()),
                f"{name}: rollout annotations must track TLS/CA Secret variables")
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
    for name, variable, actual, default in (
        ("edge", "SHORE_URL", edge_config["data"]["SHORE_URL"], "http://shore.ghost-seeker.svc.cluster.local:8080"),
        ("operator", "OPERATOR_SHORE_URL", operator_urls[0], "http://shore:8080"),
    ):
        expected = variables[variable]
        if variables["SHORE_TLS_SECRET"] and expected == default:
            expected = expected.replace("http://", "https://", 1)
        require(actual == expected, f"{variable}: rendered shore URL must match the selected TLS mode")
        annotations = workloads[name]["spec"]["template"]["metadata"].get("annotations", {})
        require(annotations.get("ghost-seeker.io/shore-url") == actual,
                f"{name}: rollout annotation must track the effective shore URL")
        parsed = urlsplit(actual)
        require(parsed.scheme in {"http", "https"} and bool(parsed.hostname), f"{variable}: invalid HTTP(S) URL")
        local_hosts = {"shore", "shore.ghost-seeker", "shore.ghost-seeker.svc",
                       "shore.ghost-seeker.svc.cluster.local"}
        if variables["SHORE_TLS_SECRET"] and parsed.hostname in local_hosts:
            require(parsed.scheme == "https", f"{variable}: local TLS shore requires an HTTPS URL")


def check_models(package: dict, documents: list[dict]) -> None:
    entries = {entry["name"]: entry for entry in package.get("variables", [])}
    for name in ("MODEL_SHA256", "MODEL_DETECTOR", "MODEL_EMBED"):
        require(bool(entries.get(name, {}).get("pattern")), f"{name} must have a pattern")
    require(entries["MODEL_SHA256"].get("default") == hashlib.sha256(
        (ROOT.parent / "models/SHA256SUMS").read_bytes()).hexdigest(),
        "MODEL_SHA256 default must match committed model SHA256SUMS")
    sums = (ROOT.parent / "models/SHA256SUMS").read_text().splitlines()
    for filename in ("colregs_lights.json", "colregs_night_config.json"):
        digest = hashlib.sha256((ROOT.parents[1] / "internal/catalog" / filename).read_bytes()).hexdigest()
        require(f"{digest}  catalog/{filename}" in sums,
                f"catalog checksum must match internal/catalog/{filename}")
    component = next(c for c in package["components"] if c["name"] == "edge-inference")
    require(all(group.get("template") is True for group in component["manifests"]),
            "edge-inference must use template: true")
    workload = next(d for d in documents if d["kind"] == "Deployment"
                    and d["metadata"]["name"] == "edgeml-sidecar")
    pod, _ = workload_containers(workload)
    init = next((c for c in pod.get("initContainers", []) if c["name"] == "models"), {})
    image = init.get("image", "")
    require(image.startswith("ghost-seeker/edgeml-models:") and image in component["images"],
            "models initContainer must use a listed edgeml-models image")
    script = "\n".join(init.get("args", []))
    require('echo "$MODEL_SHA256  /models-src/SHA256SUMS" | sha256sum -c' in script
            and 'test -n "$MODEL_SHA256"' in script
            and 'sha256sum -c SHA256SUMS' in script,
            "models initContainer must verify MODEL_SHA256 and model checksums")
    sidecar = next(c for c in pod["containers"] if c["name"] == "edgeml-sidecar")
    args = sidecar.get("args", [])
    require("--mock" not in args and "--detector" in args
            and args[args.index("--detector") + 1].startswith("/models/"),
            "sidecar must use a real --detector under /models/")
    require("--embed" in args and args[args.index("--embed") + 1].startswith(("/models/", "none")),
            "sidecar must use the configured model embedding directory")
    require('if [ "$MODEL_EMBED" != none ]' in script
            and 'meta.json model.onnx text_cache.json unknown_calib.npz' in script,
            "models initContainer must check embedding files are listed")
    require(any(m.get("name") == "models" and m.get("mountPath") == "/models"
                and m.get("readOnly") is True for m in sidecar.get("volumeMounts", [])),
            "sidecar must mount /models readOnly")
    require(not any(v.get("name") == "catalog" for v in pod.get("volumes", []))
            and not any(m.get("name") == "catalog" for c in (sidecar, init)
                        for m in c.get("volumeMounts", [])),
            "sidecar must use the catalog packaged in its image without a catalog volume")


def check_uds_package(documents: list[dict], package: dict, rendered: list[dict]) -> None:
    variables = {entry["name"]: entry for entry in package["variables"]}
    packages = [doc for doc in documents if doc["kind"] == "Package"]
    require(len(packages) == 1, "exactly one UDS Package required")
    uds = packages[0]
    namespaces = {doc["metadata"]["name"] for doc in documents if doc["kind"] == "Namespace"}
    require(uds["apiVersion"] == "uds.dev/v1alpha1"
            and uds["metadata"]["name"] == "ghost-seeker"
            and uds["metadata"]["namespace"] in namespaces, "UDS Package identity/namespace invalid")
    require("sso" not in uds["spec"], "UDS Package must not configure sso")
    network = uds["spec"]["network"]
    require(network.get("serviceMesh") == {"mode": "sidecar"}, "UDS requires sidecar mode")
    exposes = network.get("expose", [])
    require({e.get("service") for e in exposes} == {"operator", "shore"} and len(exposes) == 2,
            "UDS exposes must be operator and shore only")
    for expose in exposes:
        require(expose.get("gateway") == "tenant", "UDS expose must use tenant gateway")
        host = VARIABLE_PATTERN.fullmatch(expose.get("host", ""))
        require(host is not None and host[1] in variables and variables[host[1]].get("default"),
                "UDS expose host must be a declared variable with non-empty default")
        services = [d for d in documents if d["kind"] == "Service"
                    and d["metadata"]["name"] == expose["service"]
                    and d["metadata"].get("namespace") == uds["metadata"]["namespace"]]
        require(len(services) == 1 and services[0]["spec"]["selector"] == expose.get("selector")
                and any(p["port"] == expose.get("port") for p in services[0]["spec"]["ports"]),
                "UDS expose Service/port/selector mismatch")
        require(bool(selected_workloads({"matchLabels": expose["selector"]},
                                       uds["metadata"]["namespace"],
                                       [d for d in documents if d["kind"] in WORKLOAD_KINDS])),
                "UDS expose selector matches no workload")
        if expose["service"] == "shore":
            require(any(p["port"] == expose["port"] and p.get("appProtocol") == "http2"
                        for p in services[0]["spec"]["ports"]),
                    "shore Service must declare HTTP/2 for gRPC sync trailers")
            require(expose.get("match") == [
                {"name": "versioned-api", "uri": {"prefix": "/api/v1/"}},
                {"name": "ship-sync", "uri": {"prefix": "/ghostseeker.v1.SyncService/"}}],
                "shore expose must restrict API and sync prefixes")
    expected = set()
    def flow(direction, selector, peer, port):
        return (direction, tuple(sorted(selector.items())), peer, str(port))
    for policy in (d for d in documents if d["kind"] == "NetworkPolicy"):
        spec = policy["spec"]
        direction = spec["policyTypes"][0]
        for rule in spec.get(direction.lower(), []):
            for peer in rule.get("from" if direction == "Ingress" else "to", []):
                if "namespaceSelector" in peer:
                    continue
                remote = ("selector", tuple(sorted(peer["podSelector"]["matchLabels"].items()))) if "podSelector" in peer else ("cidr", peer["ipBlock"]["cidr"])
                for port in rule["ports"]:
                    expected.add(flow(direction, spec["podSelector"]["matchLabels"], remote, port["port"]))
    actual = set()
    for allow in network.get("allow", []):
        require(set(allow) <= {"description", "direction", "selector", "port", "remoteSelector", "remoteCidr"},
                "UDS allow forbids Anywhere, wildcard namespaces and unsupported fields")
        require(bool(allow.get("description")), "UDS allow needs description")
        require(("remoteSelector" in allow) != ("remoteCidr" in allow), "UDS allow needs one remote")
        if "remoteCidr" in allow:
            match = VARIABLE_PATTERN.fullmatch(allow["remoteCidr"])
            require(match is not None and match[1].endswith("_CIDR") and match[1] in variables,
                    "UDS remoteCidr must use a declared *_CIDR variable")
            require(ipaddress.ip_network(variables[match[1]]["default"], strict=True).prefixlen > 0,
                    "UDS unrestricted remoteCidr default")
            remote = ("cidr", allow["remoteCidr"])
        else:
            remote = ("selector", tuple(sorted(allow["remoteSelector"].items())))
        actual.add(flow(allow["direction"], allow["selector"], remote, allow["port"]))
    require(actual == expected, "UDS allow parity drift from non-DNS NetworkPolicies")
    values = package_variables(package, {})
    expected_host = values["SHORE_GATEWAY_HOST"] + "." + values["DOMAIN"]
    shore = next(d for d in rendered if d["kind"] == "ConfigMap" and d["metadata"]["name"] == "shore-config")
    require(expected_host in shore["data"]["GS_PUBLIC_HOSTS"].split(","), "shore public host missing gateway host")
    components = package["components"]
    index = next(i for i, c in enumerate(components) if c["name"] == "uds-package")
    component = components[index]
    require(index == 1 and components[0]["name"] == "namespace"
            and component.get("required") is False and component.get("default") is False,
            "uds-package must be optional/default false immediately after namespace")
    actions = component.get("actions", {}).get("onDeploy", {}).get("before", [])
    guard = "\n".join(a.get("cmd", "") for a in actions)
    require(all(s in guard for s in ("ZARF_VAR_SHORE_TLS_SECRET", "ZARF_VAR_EDGE_TLS_SECRET",
                                    "exit 1", "STRICT mTLS")), "uds-package TLS guard missing")
    require('"${ZARF_VAR_FRAMES_SOURCE:-}" != pvc' in guard,
            "uds-package must require FRAMES_SOURCE=pvc")


def check_frames(documents: list[dict], variables: dict[str, str]) -> None:
    source = variables["FRAMES_SOURCE"]
    require(source in {"hostPath", "pvc"}, "invalid FRAMES_SOURCE")
    edge = next(d for d in documents if d["kind"] == "StatefulSet" and d["metadata"]["name"] == "edge")
    pod, containers = workload_containers(edge)
    mount = next(m for m in containers[0]["volumeMounts"] if m["name"] == "frames")
    require(mount.get("readOnly") is True and mount["mountPath"] == "/frames",
            "frames must remain read-only at /frames")
    if source == "pvc":
        def has_hostpath(value):
            if isinstance(value, dict):
                return "hostPath" in value or any(has_hostpath(v) for v in value.values())
            return isinstance(value, list) and any(has_hostpath(v) for v in value)
        require(not has_hostpath(documents), "PVC rendering must not contain any hostPath")
        claim = next((v for v in edge["spec"]["volumeClaimTemplates"] if v["metadata"]["name"] == "frames"), {})
        spec = claim.get("spec", {})
        require(spec.get("accessModes") == ["ReadWriteOnce"]
                and spec.get("resources", {}).get("requests", {}).get("storage") == variables["FRAMES_STORAGE"],
                "frames PVC must use the configured size and ReadWriteOnce")
        require(spec.get("storageClassName", "") == variables["FRAMES_STORAGE_CLASS"],
                "frames PVC must use the configured storage class")
        require(bool(variables["FRAMES_STORAGE_CLASS"]) or "storageClassName" not in spec,
                "empty frames storage class must use cluster default")
    else:
        volume = next(v for v in pod["volumes"] if v["name"] == "frames")
        require(volume.get("hostPath") == {"path": variables["FRAMES_HOSTPATH"], "type": "DirectoryOrCreate"},
                "non-UDS frames must use FRAMES_HOSTPATH")


def check_hardening(documents: list[dict], variables: dict[str, str]) -> None:
    require(not any(d["kind"] == "Secret" for d in documents), "authentication Secrets must pre-exist")
    for workload in (d for d in documents if d["kind"] in WORKLOAD_KINDS):
        name = workload["metadata"]["name"]
        pod, containers = workload_containers(workload)
        for container in containers:
            resources = container.get("resources", {})
            for budget in ("requests", "limits"):
                require(set(resources.get(budget, {})) >= {"cpu", "memory"},
                        f"{name}/{container['name']}: CPU and memory {budget} required")
                require(all(str(resources[budget][key]) not in {"", "0", "0m", "0Mi"} for key in ("cpu", "memory")),
                        f"{name}: resource budgets must be positive")
            if container["name"] == "edgeml-sidecar":
                for budget, suffix in (("requests", "REQUEST"), ("limits", "LIMIT")):
                    for key in ("cpu", "memory"):
                        require(str(resources[budget][key]) == variables[f"INFERENCE_{key.upper()}_{suffix}"],
                                "inference resources must use configured variables")
        if name in {"edge", "shore", "operator"}:
            container = next(c for c in containers if c["name"] == name)
            upper = name.upper()
            required = {"GS_OPERATOR_TOKEN": "OPERATOR_TOKEN"}
            if name == "shore":
                required["GS_SIGNING_KEY"] = "SIGNING_KEY"
            if name == "edge":
                required["GS_UNIT_TOKEN"] = "UNIT_TOKEN"
            env = {e["name"]: e for e in container.get("env", [])}
            for env_name, key in required.items():
                require(env.get(env_name, {}).get("valueFrom") == {"secretKeyRef": {
                    "name": variables[f"{upper}_AUTH_SECRET"], "key": variables[f"{upper}_{key}_KEY"]}},
                    f"{name}: {env_name} must use the configured pre-existing Secret name/key")
        claims = workload.get("spec", {}).get("volumeClaimTemplates", [])
        for claim in claims:
            prefix = "FRAMES" if claim["metadata"]["name"] == "frames" else name.upper()
            check_claim(claim, prefix, variables)
    for claim in (d for d in documents if d["kind"] == "PersistentVolumeClaim"):
        require(claim["metadata"]["name"] == "operator-data", "unknown PVC needs configuration")
        check_claim(claim, "OPERATOR", variables)


def check_claim(claim: dict, prefix: str, variables: dict[str, str]) -> None:
    spec = claim["spec"]
    require(spec.get("resources", {}).get("requests", {}).get("storage") == variables[f"{prefix}_STORAGE"],
            f"{prefix}: PVC size must be configurable")
    storage_class = variables[f"{prefix}_STORAGE_CLASS"]
    require(spec.get("storageClassName", "") == storage_class
            and (bool(storage_class) or "storageClassName" not in spec),
            f"{prefix}: PVC StorageClass must be configurable; empty uses cluster default")


def check_split(role: str, overrides: dict[str, str] | None = None) -> dict[Path, list[dict]]:
    package_dir = ROOT / role
    package = yaml.safe_load((package_dir / "zarf.yaml").read_text())
    values = package_variables(package, overrides or {})
    require(package["metadata"]["name"] == f"ghost-seeker-{role}", "split package identity")
    expected = {"edge", "edgeml-sidecar"} if role == "ship" else {"shore", "operator"}
    require([c["name"] for c in package["components"]][:2] == ["namespace", "uds-package"]
            and all(c.get("required") is True for c in package["components"]),
            "split UDS components must be mandatory, namespace and Package first")
    require(not any(s in str(package) for s in ("dev_auth", "mint-unit-token", "/dev/urandom")),
            "production package must never generate authentication")
    for name, value in values.items():
        if name.endswith("_CIDR"):
            require(ipaddress.ip_network(value, strict=True).prefixlen > 0, "unrestricted site CIDR")
        if name.endswith("_EGRESS_PORT"):
            require(1 <= int(value) <= 65535, "invalid egress port")
    if role == "ship":
        endpoint = urlsplit(values["SHORE_URL"])
        require(not endpoint.username and not endpoint.password and not endpoint.query and not endpoint.fragment
                and endpoint.path in {"", "/"}, "shore endpoint must be an origin without credentials")
        require(values["SHORE_TRANSPORT"] == "external", "split packages require gateway transport")
        require(endpoint.scheme == "https" and bool(endpoint.hostname) and endpoint.port == 443,
                "cross-cluster shore requires TLS on port 443 and HTTP/2")
        require(values["FRAMES_SOURCE"] == "pvc", "UDS requires PVC frames")
    else:
        require(values["SHORE_REQUIRE_AUTH"] == "true", "shore auth cannot be disabled")
    require(not values["SHORE_TLS_SECRET"] and not values["EDGE_TLS_SECRET"],
            "UDS uses gateway TLS and STRICT mTLS, not listener TLS")
    parsed = render_manifests(package, overrides or {}, package_dir)
    documents = [d for docs in parsed.values() for d in docs]
    require({d["metadata"]["name"] for d in documents if d["kind"] in WORKLOAD_KINDS} == expected,
            "split package contains the wrong workloads")
    require(all(d["metadata"].get("namespace") == values["NAMESPACE"] for d in documents if d["kind"] != "Namespace"),
            "all split resources must use configured namespace")
    check_hardening(documents, values)
    for workload in (d for d in documents if d["kind"] in WORKLOAD_KINDS and d["metadata"]["name"] in {"edge", "operator"}):
        pod, containers = workload_containers(workload)
        container = containers[0]
        ca_secret = values["SHORE_CA_SECRET"]
        check_secret_mount(pod, container, "shore-ca", ca_secret, "/run/shore-ca", {"ca.crt"})
        require(("-shore-ca=/run/shore-ca/ca.crt" in container.get("args", [])) == bool(ca_secret),
                "shore CA must configure the verified TLS client")
    if role == "ship":
        check_frames(documents, values)
        check_models(package, documents)
    uds = next(d for d in documents if d["kind"] == "Package")
    require(uds["metadata"]["name"] == f"ghost-seeker-{role}", "split Package identity")
    require(uds["spec"]["network"].get("serviceMesh") == {"mode": "sidecar"}, "UDS requires sidecar mesh")
    require("sso" not in uds["spec"], "no implicit SSO credential creation")
    network = uds["spec"]["network"]
    services = {d["metadata"]["name"]: d for d in documents if d["kind"] == "Service"}
    require({e["service"] for e in network.get("expose", [])} == ({"edge"} if role == "ship" else expected),
            "unexpected gateway exposure")
    for expose in network["expose"]:
        require(expose.get("gateway") == "tenant" and expose.get("selector") == {"app": expose["service"]}
                and any(p["port"] == expose["port"] for p in services[expose["service"]]["spec"]["ports"]),
                "gateway Service/port/selector drift")
    if role == "shore":
        require(services["shore"]["spec"]["ports"][0].get("appProtocol") == "http2",
                "shore Service must preserve HTTP/2 trailers")
        expose = next(e for e in network["expose"] if e["service"] == "shore")
        require(expose.get("match") == [
            {"name": "versioned-api", "uri": {"prefix": "/api/v1/"}},
            {"name": "ship-sync", "uri": {"prefix": "/ghostseeker.v1.SyncService/"}}],
            "shore gateway must restrict API and sync paths")
        shore = next(d for d in documents if d["kind"] == "ConfigMap" and d["metadata"]["name"] == "shore-config")
        require(values["SHORE_GATEWAY_HOST"] + "." + values["DOMAIN"] in shore["data"]["GS_PUBLIC_HOSTS"].split(","),
                "shore public hosts must include gateway")
    raw_network = read_documents(package_dir / "uds-package.yaml")[0]["spec"]["network"]
    local = set()
    for rule in raw_network["allow"]:
        require(set(rule) <= {"description", "direction", "selector", "remoteSelector", "remoteNamespace", "remoteCidr", "port"}
                and rule.get("selector", {}).get("app") in expected, "unscoped UDS allow")
        require(rule.get("direction") in {"Ingress", "Egress"} and bool(rule.get("description")), "invalid allow direction/description")
        if "remoteSelector" in rule:
            require(rule.get("remoteNamespace") == "###ZARF_VAR_NAMESPACE###"
                    and set(rule["remoteSelector"]) == {"app"}
                    and rule["remoteSelector"]["app"] in expected, "remoteNamespace must be explicitly scoped")
            local.add((rule["direction"], rule["selector"]["app"], rule["remoteSelector"]["app"], rule["port"]))
        else:
            match = VARIABLE_PATTERN.fullmatch(rule.get("remoteCidr", ""))
            require(match is not None and match[1].endswith("_CIDR") and match[1] in values
                    and "remoteNamespace" not in rule and rule["direction"] == "Egress",
                    "external traffic must use narrow declared CIDRs; ingress is gateway-only")
            egress_port(rule["port"], {n: {"default": v} for n, v in values.items()})
    source, target, port = ("edge", "edgeml-sidecar", 9090) if role == "ship" else ("operator", "shore", 8080)
    require(local == {("Egress", source, target, port), ("Ingress", target, source, port)},
            "every in-mesh flow requires the exact ingress/egress pair")
    listed = {i for c in package["components"] for i in c.get("images", [])}
    actual = {c["image"] for d in documents if d["kind"] in WORKLOAD_KINDS for c in workload_containers(d)[1]}
    require(listed == actual, "split package image list drift")
    from pin_images import check_lock
    require(actual <= set(check_lock().values()), "split package must use locked immutable images")
    return parsed


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
    check_hardening([doc for docs in parsed.values() for doc in docs], variables)
    check_frames([doc for docs in parsed.values() for doc in docs], variables)
    check_models(package, [doc for docs in parsed.values() for doc in docs])
    require(set(parsed) == set(all_manifest_paths), "every manifest must be packaged")
    check_tls_wiring([document for docs in parsed.values() for document in docs], variables)
    raw = [doc for path in all_manifest_paths if path.name.startswith("network-") or path.name == "uds-package.yaml"
           for doc in read_documents(path)]
    raw += [doc for docs in parsed.values() for doc in docs if doc["kind"] not in {"Package", "NetworkPolicy"}]
    check_uds_package(raw, package, [doc for docs in parsed.values() for doc in docs])
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
    owners: dict[Path, set[str]] = {}
    for component, files in component_files.items():
        for path in files:
            owners.setdefault(path, set()).add(component)
    for path, documents in parsed.items():
        for policy in (doc for doc in documents if doc["kind"] == "NetworkPolicy"):
            name = policy["metadata"]["name"]
            require(path in owners, f"{path.name}: NetworkPolicy not included in a component")
            labels = policy.get("spec", {}).get("podSelector", {}).get("matchLabels")
            if not labels:
                namespace_owners = {component for ns_path, ns_docs in parsed.items()
                                    for doc in ns_docs if doc["kind"] == "Namespace"
                                    and doc["metadata"]["name"] == policy["metadata"].get("namespace")
                                    for component in owners.get(ns_path, set())
                                    if any(c["name"] == component and c.get("required") is True
                                           for c in package["components"])}
                require(bool(namespace_owners & owners[path]),
                        f"{name}: default deny must be packaged with its required namespace component")
                continue
            workload_owners = {component for workload_path, workload_docs in parsed.items()
                               for doc in workload_docs if doc["kind"] in WORKLOAD_KINDS
                               and doc["metadata"].get("namespace") == policy["metadata"].get("namespace")
                               and all(doc["spec"]["template"]["metadata"]["labels"].get(k) == v
                                       for k, v in labels.items())
                               for component in owners.get(workload_path, set())}
            require(bool(workload_owners & owners[path]),
                    f"{name}: allow policy must be packaged in the same component as its target workload")
    network_documents = [doc for documents in parsed.values() for doc in documents
                         if doc["kind"] != "NetworkPolicy"]
    network_documents += [doc for path in all_manifest_paths if path.name.startswith("network-")
                          for doc in read_documents(path)]
    policy_count = check_network_policies(
        network_documents,
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
                    require(container in pod_spec.get("initContainers", []) or bool(container.get("readinessProbe")),
                            f"{component}: container {name} has no readinessProbe")
                    require(container in pod_spec.get("initContainers", []) or bool(container.get("livenessProbe")),
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
    print(f"validated {policy_count} ingress/egress NetworkPolicies: namespace default deny, DNS, selectors, Service ports and CIDR defaults")
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
        parsed = check({"FRAMES_SOURCE": "pvc"})
        if args.output_dir:
            profile = args.output_dir / "uds-pvc"
            profile.mkdir(parents=True, exist_ok=True)
            for path, documents in parsed.items():
                (profile / path.name).write_text(yaml.safe_dump_all(documents, sort_keys=False))
        print("validated UDS PVC rendering without hostPath")
        for role in ("ship", "shore"):
            parsed = check_split(role)
            if args.output_dir:
                profile = args.output_dir / role
                profile.mkdir(parents=True, exist_ok=True)
                for path, documents in parsed.items():
                    (profile / path.name).write_text(yaml.safe_dump_all(documents, sort_keys=False))
            print(f"validated hardened {role} UDS package")
    except (KeyError, TypeError, ValueError) as exc:
        print(f"manifest validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
