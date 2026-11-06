#!/usr/bin/env python3
import ipaddress
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


def check_uds_wiring(documents: list[dict], variables: dict[str, str]) -> None:
    namespace = next(doc for doc in documents if doc["kind"] == "Namespace")
    require(namespace["metadata"].get("labels", {}).get("istio.io/dataplane-mode") == "ambient",
            "UDS namespace must enroll in the ambient mesh")
    packages = [doc for doc in documents if doc["kind"] == "Package"]
    require(len(packages) == 1 and packages[0]["apiVersion"] == "uds.dev/v1alpha1"
            and packages[0]["metadata"] == {"name": "ghost-seeker", "namespace": "ghost-seeker"},
            "one namespaced UDS Package is required")
    spec = packages[0]["spec"]
    network = spec["network"]
    require(network.get("serviceMesh") == {"mode": "ambient"}, "UDS mesh mode must be ambient")
    flows = [("edge", "shore", 8080), ("edge", "edgeml-sidecar", 9090), ("operator", "shore", 8080)]
    if variables["SHORE_INFER_URL"]:
        flows.append(("shore", "edgeml-sidecar", 9090))
    expected = []
    for source, target, port in flows:
        for direction, local, remote in (("Egress", source, target), ("Ingress", target, source)):
            expected.append({"direction": direction, "selector": {"app": local},
                             "remoteNamespace": "ghost-seeker", "remoteSelector": {"app": remote}, "port": port})
    if variables["UDS_CROSS_CLUSTER_SHORE"] == "true":
        cidr = ipaddress.ip_network(variables["SHORE_EGRESS_CIDR"], strict=True)
        require(cidr.prefixlen > 0 and str(cidr) not in {"192.0.2.0/32"},
                "UDS routed shore requires an explicit narrow site CIDR")
        expected.append({"direction": "Egress", "selector": {"app": "edge"},
                         "remoteCidr": str(cidr), "port": int(variables["SHORE_EGRESS_PORT"])})
    require(network.get("allow") == expected, "UDS allow rules must match the configured least-privilege flows")
    exposes = [{"service": "operator", "selector": {"app": "operator"}, "port": 8090,
                "gateway": "tenant", "host": variables["OPERATOR_HOST"]}]
    if variables["UDS_EXPOSE_SHORE"] == "true":
        exposes.append({"service": "shore", "selector": {"app": "shore"}, "port": 8080,
                        "gateway": "tenant", "host": variables["SHORE_HOST"]})
    require(network.get("expose") == exposes, "UDS gateway exposure must match selected services")
    require("monitor" not in spec, "no Prometheus endpoint is implemented")
    clients = spec.get("sso", [])
    require(len(clients) == 1 and clients[0].get("enabled") is (variables["UDS_SSO_CLIENT_ENABLED"] == "true")
            and "enableAuthserviceSelector" not in clients[0], "SSO is registration only, never trusted-header authentication")
    workloads = {doc["metadata"]["name"]: doc for doc in documents if doc["kind"] in WORKLOAD_KINDS}
    operator_pod, operator_containers = workload_containers(workloads["operator"])
    operator = next(item for item in operator_containers if item["name"] == "operator")
    require("-review-log=/data/reviews.jsonl" in operator["args"]
            and any(v.get("persistentVolumeClaim", {}).get("claimName") == "operator-data"
                    for v in operator_pod["volumes"])
            and {"name": "data", "mountPath": "/data"} in operator["volumeMounts"],
            "operator review log must use a writable persistent volume")
    for name, workload in workloads.items():
        pod, containers = workload_containers(workload)
        require(not any("hostPath" in volume for volume in pod.get("volumes", [])), "UDS forbids hostPath volumes")
        require(pod.get("securityContext", {}).get("runAsUser") == (10001 if name == "edgeml-sidecar" else 65532),
                "UDS workloads must retain numeric non-root UIDs")
        for container in containers:
            security = container.get("securityContext", {})
            require(security.get("allowPrivilegeEscalation") is False and not security.get("privileged", False)
                    and security.get("capabilities", {}).get("drop") == ["ALL"],
                    "UDS requires no privilege escalation and drop ALL")
    for name, volume, secret, filename, env_name in (
        ("shore", "policy-signing", variables["POLICY_SIGNING_SECRET"], "signing-key.json", "GS_POLICY_SIGNING_KEY"),
        ("edge", "policy-keys", variables["POLICY_KEYS_SECRET"], "keyring.json", "GS_POLICY_KEYS"),
    ):
        pod, containers = workload_containers(workloads[name])
        container = next(item for item in containers if item["name"] == name)
        check_secret_mount(pod, container, volume, secret, "/run/policy", {filename})
        environment = {entry["name"]: entry.get("value") for entry in container.get("env", [])}
        config = next(doc["data"] for doc in documents if doc["kind"] == "ConfigMap"
                      and doc["metadata"]["name"] == f"{name}-config")
        require((environment | config).get(env_name) == f"/run/policy/{filename}", "policy key env must reference mounted files")
        if name == "edge":
            require(config.get("GS_REQUIRE_SIGNED_POLICY") == variables["REQUIRE_SIGNED_POLICY"],
                    "edge must require signed policy as configured")
        host = f"{variables[f'{name.upper()}_HOST']}.{variables['UDS_DOMAIN']}"
        require(host in config.get("GS_PUBLIC_HOSTS", "").split(","), "gateway hostname missing from Host allowlist")
        require(config.get("GS_CORS_ORIGINS") == f"https://{variables['OPERATOR_HOST']}.{variables['UDS_DOMAIN']}",
                "CORS must allow only the operator gateway origin")


def check(overrides: dict[str, str] | None = None) -> dict[Path, list[dict]]:
    package_file = ROOT / "zarf.yaml"
    package = yaml.safe_load(package_file.read_text())
    require(package.get("apiVersion") == "zarf.dev/v1alpha1", "zarf.yaml must use zarf.dev/v1alpha1")
    require(package.get("kind") == "ZarfPackageConfig", "zarf.yaml must be a ZarfPackageConfig")
    overrides = overrides or {}
    variables = package_variables(package, overrides)
    require(next(entry for entry in package["variables"] if entry["name"] == "REQUIRE_SIGNED_POLICY")["default"] == "true",
            "package must require signed policy by default")
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
    check_uds_wiring([document for docs in parsed.values() for document in docs], variables)
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
    except (KeyError, TypeError, ValueError) as exc:
        print(f"manifest validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
