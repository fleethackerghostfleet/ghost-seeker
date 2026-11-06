import base64
import copy
import hashlib
import hmac
import ipaddress
import itertools
import json
import re
import subprocess
from urllib.parse import urlsplit
import unittest
from unittest import mock
from unittest.mock import patch

import yaml

import check_manifests as validator
import check_manifests as checker
import dev_auth
import pin_images


class SplitProductionTests(unittest.TestCase):
    def test_legacy_namespace_cannot_diverge_from_network_policies(self):
        package = yaml.safe_load((validator.ROOT / "zarf.yaml").read_text())
        with self.assertRaisesRegex(ValueError, "Zarf variable pattern"):
            validator.package_variables(package, {"NAMESPACE": "unprotected"})

    @classmethod
    def setUpClass(cls):
        cls.rendered = {role: validator.check_split(role) for role in ("ship", "shore")}

    def documents(self, role):
        return [d for docs in self.rendered[role].values() for d in docs]

    def test_role_separation_and_mandatory_uds(self):
        for role, expected in (("ship", {"edge", "edgeml-sidecar"}), ("shore", {"shore", "operator"})):
            self.assertEqual({d["metadata"]["name"] for d in self.documents(role)
                              if d["kind"] in validator.WORKLOAD_KINDS}, expected)
            bundle = yaml.safe_load((validator.ROOT.parents[1] / f"bundles/prod-uds/{role}/uds-bundle.yaml").read_text())
            self.assertEqual([p["name"] for p in bundle["packages"]], [f"ghost-seeker-{role}"])
        self.assertNotIn("dev_auth", (validator.ROOT.parents[1] / "tasks.yaml").read_text())

    def test_auth_names_and_keys_can_be_changed_without_values(self):
        for role, names in (("ship", ("EDGE",)), ("shore", ("SHORE", "OPERATOR"))):
            package = yaml.safe_load((validator.ROOT / role / "zarf.yaml").read_text())
            overrides = {v["name"]: "site-key" if v["name"].endswith("_KEY") else "site-auth"
                         for v in package["variables"] if any(v["name"].startswith(n + "_") for n in names)
                         and (v["name"].endswith("_AUTH_SECRET") or v["name"].endswith("_KEY"))}
            parsed = validator.check_split(role, overrides)
            self.assertFalse(any(d["kind"] == "Secret" for docs in parsed.values() for d in docs))

    def test_all_storage_and_inference_resources_are_configurable(self):
        validator.check_split("ship", {"EDGE_STORAGE": "12Gi", "EDGE_STORAGE_CLASS": "ship-ssd",
                                      "FRAMES_STORAGE": "21Gi", "FRAMES_STORAGE_CLASS": "camera-ssd",
                                      "INFERENCE_CPU_REQUEST": "1200m", "INFERENCE_MEMORY_REQUEST": "2Gi",
                                      "INFERENCE_CPU_LIMIT": "6", "INFERENCE_MEMORY_LIMIT": "8Gi"})
        validator.check_split("shore", {"SHORE_STORAGE": "40Gi", "SHORE_STORAGE_CLASS": "shore-ssd",
                                       "OPERATOR_STORAGE": "3Gi", "OPERATOR_STORAGE_CLASS": "review-ssd"})

    def test_external_shore_requires_verified_tls_port_443(self):
        for url in ("http://shore.example:443", "https://shore.example:8080", "https://a:b@shore.example:443",
                    "https://shore.example:443/path"):
            with self.subTest(url=url), self.assertRaises(ValueError):
                validator.check_split("ship", {"SHORE_URL": url})
        validator.check_split("ship", {"SHORE_URL": "https://shore.fleet.example:443", "SHORE_CA_SECRET": "fleet-ca"})
        validator.check_split("ship", {"SHORE_TRANSPORT": "in-mesh", "SHORE_URL": "http://shore.ghost-seeker-shore.svc.cluster.local:8080",
                                      "SHORE_EGRESS_PORT": "8080"})

    def test_broad_cidrs_listener_tls_hostpath_and_disabled_auth_rejected(self):
        for role, override in (("ship", {"SHORE_EGRESS_CIDR": "0.0.0.0/0"}),
                               ("ship", {"CAMERA_EGRESS_CIDR": "::/0"}),
                               ("ship", {"FRAMES_SOURCE": "hostPath"}),
                               ("shore", {"SHORE_REQUIRE_AUTH": "false"}),
                               ("shore", {"SHORE_TLS_SECRET": "listener-tls"})):
            with self.subTest(override=override), self.assertRaises(ValueError):
                validator.check_split(role, override)

    def test_domain_and_gateway_hosts_propagate(self):
        parsed = validator.check_split("shore", {"DOMAIN": "fleet.example", "SHORE_GATEWAY_HOST": "receive",
                                                 "OPERATOR_GATEWAY_HOST": "review"})
        docs = [d for group in parsed.values() for d in group]
        config = next(d for d in docs if d["kind"] == "ConfigMap")
        self.assertIn("receive.fleet.example", config["data"]["GS_PUBLIC_HOSTS"].split(","))

    def test_missing_resource_secret_pvc_and_protocol_are_rejected(self):
        mutations = (
            ("ship", lambda docs: next(d for d in docs if d["kind"] == "Deployment")["spec"]["template"]["spec"]["initContainers"][0]["resources"]["limits"].pop("cpu")),
            ("ship", lambda docs: next(d for d in docs if d["kind"] == "StatefulSet")["spec"]["template"]["spec"]["containers"][0]["env"][0].update({"valueFrom": {"secretKeyRef": {"name": "wrong", "key": "unit-token"}}})),
            ("shore", lambda docs: next(d for d in docs if d["kind"] == "PersistentVolumeClaim")["spec"]["resources"]["requests"].update({"storage": "99Gi"})),
            ("shore", lambda docs: next(d for d in docs if d["kind"] == "Service" and d["metadata"]["name"] == "shore")["spec"]["ports"][0].pop("appProtocol")),
        )
        for role, mutate in mutations:
            parsed = copy.deepcopy(self.rendered[role])
            mutate([d for docs in parsed.values() for d in docs])
            with self.subTest(role=role), patch.object(validator, "render_manifests", return_value=parsed), self.assertRaises(ValueError):
                validator.check_split(role)

    def test_immutable_image_lock_matches_all_packages_and_manifests(self):
        locked = pin_images.check_lock()
        self.assertEqual(len(locked), 5)
        self.assertTrue(all(re.fullmatch(pin_images.TAG_PATTERN, ref.rsplit(":", 1)[1]) for ref in locked.values()))

    def test_namespace_and_paired_flows_cannot_be_broadened(self):
        original = validator.read_documents
        for role in ("ship", "shore"):
            for mutation in (lambda rules: rules[0].pop("remoteNamespace"),
                             lambda rules: rules.pop(1),
                             lambda rules: rules[0].update({"remoteNamespace": "*"})):
                def read(path, text=None):
                    docs = original(path, text)
                    if path == validator.ROOT / role / "uds-package.yaml" and text is None:
                        mutation(docs[0]["spec"]["network"]["allow"])
                    return docs
                with self.subTest(role=role), patch.object(validator, "read_documents", side_effect=read), self.assertRaises(ValueError):
                    validator.check_split(role)


class DevAuthTests(unittest.TestCase):
    def test_fresh_secrets_share_operator_token_and_valid_unit_signature(self):
        with patch.object(dev_auth, "apply") as apply:
            dev_auth.main()
        documents = [call.args[0] for call in apply.call_args_list]
        self.assertEqual(documents[0]["kind"], "Namespace")
        secrets = {d["metadata"]["name"]: d["stringData"] for d in documents[1:]}
        operator = secrets["operator-auth"]["operator-token"]
        self.assertGreaterEqual(len(operator), 32)
        self.assertEqual(secrets["shore-auth"]["operator-token"], operator)
        self.assertEqual(secrets["edge-auth"]["operator-token"], operator)
        token = secrets["edge-auth"]["unit-token"]
        header, claims, signature = token.split(".")
        expected = hmac.new(secrets["shore-auth"]["signing-key"].encode(),
                            (header + "." + claims).encode(), hashlib.sha256).digest()
        self.assertEqual(base64.urlsafe_b64decode(signature + "="), expected)
        self.assertEqual(json.loads(base64.urlsafe_b64decode(claims + "=" * (-len(claims) % 4)))["sub"], "edge-1")
        with patch.object(dev_auth, "apply") as again:
            dev_auth.main()
        self.assertNotEqual(again.call_args_list[1].args[0]["stringData"]["operator-token"], operator)

    def test_apply_does_not_echo_credentials_and_propagates_errors(self):
        with patch.object(dev_auth.subprocess, "run") as run:
            dev_auth.apply({})
            self.assertTrue(run.call_args.kwargs["check"])
            self.assertEqual(run.call_args.kwargs["stdout"], subprocess.DEVNULL)
            run.side_effect = subprocess.CalledProcessError(1, "kubectl")
            with self.assertRaises(subprocess.CalledProcessError):
                dev_auth.apply({})


class UDSPackageTests(unittest.TestCase):
    def test_example_config_preserves_sync_host(self):
        for role in ("ship", "shore"):
            config = yaml.safe_load((validator.ROOT.parents[1] / f"bundles/prod-uds/{role}/uds-config.yaml").read_text())
            overrides = config["variables"][f"ghost-seeker-{role}"]
            validator.check_split(role, overrides)

    def setUp(self):
        self.package = yaml.safe_load((validator.ROOT / "zarf.yaml").read_text())
        self.documents = [doc for path in (validator.ROOT / "manifests").glob("*.yaml")
                          if path.name.startswith("network-") or path.name == "uds-package.yaml"
                          for doc in validator.read_documents(path)]
        self.rendered = [doc for docs in validator.render_manifests(self.package, {}).values() for doc in docs]
        self.documents += [doc for doc in self.rendered if doc["kind"] not in {"Package", "NetworkPolicy"}]
        self.uds = next(d for d in self.documents if d["kind"] == "Package")

    def validate(self):
        validator.check_uds_package(self.documents, self.package, self.rendered)

    def test_valid(self):
        self.validate()

    def test_shore_sync_requires_http2_service(self):
        shore = next(d for d in self.documents if d["kind"] == "Service" and d["metadata"]["name"] == "shore")
        for protocol in (None, "http"):
            with self.subTest(protocol=protocol):
                shore["spec"]["ports"][0]["appProtocol"] = protocol
                with self.assertRaisesRegex(ValueError, "HTTP/2"):
                    self.validate()

    def test_invalid_package_contracts(self):
        cases = [
            ("apiVersion", "wrong", "identity"),
            ("kind", "Wrong", "exactly one"),
        ]
        for field, value, message in cases:
            with self.subTest(field=field):
                old = self.uds[field]
                self.uds[field] = value
                with self.assertRaisesRegex(ValueError, message):
                    self.validate()
                self.uds[field] = old

    def test_reject_network_drift(self):
        original = copy.deepcopy(self.uds["spec"]["network"])
        mutations = [
            (lambda n: n["allow"][0].update(remoteGenerated="Anywhere"), "forbids"),
            (lambda n: n["allow"][0].update(remoteNamespace="*"), "forbids"),
            (lambda n: n["allow"][0].update(remoteNamespace=""), "forbids"),
            (lambda n: n["allow"][2].update(remoteCidr="0.0.0.0/0"), "remoteCidr"),
            (lambda n: n["expose"][0].pop("host"), "host"),
            (lambda n: n["expose"][0].update(host="###ZARF_VAR_UNDECLARED###"), "host"),
            (lambda n: n["expose"][0].update(port=1234), "Service"),
            (lambda n: n["expose"][0].update(selector={"app": "edge"}), "Service"),
            (lambda n: n["expose"][0].update(gateway="admin"), "tenant"),
            (lambda n: n["expose"][1].pop("match"), "prefixes"),
            (lambda n: n["allow"].pop(), "parity"),
            (lambda n: n.update(allow=[]), "parity"),
            (lambda n: n.update(expose=[]), "exposes"),
            (lambda n: n.update(serviceMesh={"mode": "ambient"}), "sidecar"),
        ]
        for mutate, message in mutations:
            with self.subTest(message=message):
                self.uds["spec"]["network"] = copy.deepcopy(original)
                mutate(self.uds["spec"]["network"])
                with self.assertRaisesRegex(ValueError, message):
                    self.validate()

    def test_cidr_defaults_must_be_narrow(self):
        variable = next(v for v in self.package["variables"] if v["name"] == "SHORE_EGRESS_CIDR")
        for cidr in ("0.0.0.0/0", "::/0"):
            variable["default"] = cidr
            with self.assertRaisesRegex(ValueError, "unrestricted"):
                self.validate()

    def test_public_host_missing(self):
        shore = next(d for d in self.rendered if d["kind"] == "ConfigMap" and d["metadata"]["name"] == "shore-config")
        shore["data"]["GS_PUBLIC_HOSTS"] = "localhost"
        with self.assertRaisesRegex(ValueError, "public host"):
            self.validate()

    def test_empty_host_default(self):
        next(v for v in self.package["variables"] if v["name"] == "OPERATOR_GATEWAY_HOST")["default"] = ""
        with self.assertRaisesRegex(ValueError, "host"):
            self.validate()

    def test_sso_forbidden(self):
        self.uds["spec"]["sso"] = []
        with self.assertRaisesRegex(ValueError, "sso"):
            self.validate()

    def test_namespace_must_be_declared(self):
        self.uds["metadata"]["namespace"] = "other"
        with self.assertRaisesRegex(ValueError, "namespace"):
            self.validate()

    def test_component_contract(self):
        component = next(c for c in self.package["components"] if c["name"] == "uds-package")
        component["default"] = True
        with self.assertRaisesRegex(ValueError, "optional"):
            self.validate()
        component["default"] = False
        component.pop("actions")
        with self.assertRaisesRegex(ValueError, "TLS guard"):
            self.validate()


class NetworkPolicyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.package = yaml.safe_load((validator.ROOT / "zarf.yaml").read_text())
        cls.parsed = validator.render_manifests(cls.package, {})

    def setUp(self):
        self.documents = copy.deepcopy([doc for docs in self.parsed.values() for doc in docs
                                        if doc["kind"] != "NetworkPolicy"])
        self.documents += [doc for path in sorted((validator.ROOT / "manifests").glob("network-*.yaml"))
                           for doc in validator.read_documents(path)]
        self.variables = {entry["name"]: copy.deepcopy(entry) for entry in self.package["variables"]}
        self.policies = [doc for doc in self.documents if doc["kind"] == "NetworkPolicy"]

    def validate(self):
        return validator.check_network_policies(self.documents, self.variables)

    def policy(self, name):
        return next(policy for policy in self.policies if policy["metadata"]["name"] == name)

    def allowed(self, source, target, port, namespace="ghost-seeker", ip="198.51.100.99"):
        for policy in self.policies:
            spec = policy["spec"]
            if spec["podSelector"].get("matchLabels", {}).get("app") != target:
                continue
            for rule in spec.get("ingress", []):
                if not any(p["port"] == port and p["protocol"] == "TCP" for p in rule["ports"]):
                    continue
                for peer in rule["from"]:
                    if "podSelector" in peer and namespace == policy["metadata"]["namespace"]:
                        if peer["podSelector"]["matchLabels"]["app"] == source:
                            return True
                    if "ipBlock" in peer:
                        variable = validator.VARIABLE_PATTERN.fullmatch(peer["ipBlock"]["cidr"])[1]
                        if ipaddress.ip_address(ip) in ipaddress.ip_network(self.variables[variable]["default"]):
                            return True
        return False

    def test_package_is_valid(self):
        validator.check()
        self.assertEqual(self.validate(), 10)

    def test_internal_traffic_matrix(self):
        expected = {("operator", "shore", 8080), ("operator", "edge", 8081),
                    ("edge", "shore", 8080), ("edge", "edgeml-sidecar", 9090),
                    ("shore", "edgeml-sidecar", 9090)}
        for source in ("operator", "shore", "edge", "edgeml-sidecar", "unrelated"):
            for target, port in (("operator", 8090), ("shore", 8080), ("edge", 8081), ("edgeml-sidecar", 9090)):
                with self.subTest(source=source, target=target):
                    self.assertEqual(self.allowed(source, target, port), (source, target, port) in expected)
                    self.assertFalse(self.allowed(source, target, port + 1))
                    self.assertFalse(self.allowed(source, target, port, namespace="other-site"))

    def test_cross_site_cidrs_are_independently_configurable(self):
        for index, (target, port, variable) in enumerate([
            ("operator", 8090, "OPERATOR_INGRESS_CIDR"),
            ("shore", 8080, "SHORE_INGRESS_CIDR"),
            ("edge", 8081, "EDGE_INGRESS_CIDR"),
            ("edgeml-sidecar", 9090, "INFERENCE_INGRESS_CIDR"),
        ], start=1):
            self.variables[variable]["default"] = f"203.0.113.{index}/32"
        self.validate()
        for index, (target, port) in enumerate([("operator", 8090), ("shore", 8080),
                                               ("edge", 8081), ("edgeml-sidecar", 9090)], start=1):
            with self.subTest(target=target):
                self.assertTrue(self.allowed("remote", target, port, namespace="other-site", ip=f"203.0.113.{index}"))
                self.assertFalse(self.allowed("remote", target, port, ip="203.0.113.99"))

    def test_missing_default_deny_is_rejected(self):
        self.documents.remove(self.policy("default-deny-ingress"))
        with self.assertRaisesRegex(ValueError, "default-deny"):
            self.validate()

    def test_selector_typos_are_rejected(self):
        policy = self.policy("shore-ingress")
        for location in [policy["spec"]["podSelector"], policy["spec"]["ingress"][0]["from"][0]["podSelector"]]:
            before = copy.deepcopy(location)
            location["matchLabels"]["app"] = "nonexistent"
            with self.assertRaisesRegex(ValueError, "selector matches no workload"):
                self.validate()
            location.update(before)

    def test_wrong_port_is_rejected(self):
        self.policy("edge-ingress")["spec"]["ingress"][0]["ports"][0]["port"] = 8080
        with self.assertRaisesRegex(ValueError, "port not exposed"):
            self.validate()

    def test_missing_service_is_rejected(self):
        self.documents = [doc for doc in self.documents
                          if not (doc["kind"] == "Service" and doc["metadata"]["name"] == "edge")]
        with self.assertRaisesRegex(ValueError, "Service target"):
            self.validate()

    def test_empty_source_rule_and_mixed_directions_are_rejected(self):
        spec = self.policy("shore-ingress")["spec"]
        before = copy.deepcopy(spec)
        spec["ingress"][0]["from"] = []
        with self.assertRaisesRegex(ValueError, "restrict sources"):
            self.validate()
        spec.update(before)
        spec["policyTypes"].append("Egress")
        with self.assertRaisesRegex(ValueError, "exactly one direction"):
            self.validate()

    def egress_allowed(self, source, target, port, protocol="TCP", namespace="ghost-seeker", ip="198.51.100.99"):
        for policy in self.policies:
            spec = policy["spec"]
            if spec["podSelector"].get("matchLabels", {}).get("app") != source:
                continue
            for rule in spec.get("egress", []):
                if not any(validator.egress_port(p["port"], self.variables) == port and p["protocol"] == protocol
                           for p in rule["ports"]):
                    continue
                for peer in rule["to"]:
                    if "namespaceSelector" in peer:
                        if (peer["namespaceSelector"]["matchLabels"] == {"kubernetes.io/metadata.name": namespace}
                                and peer["podSelector"]["matchLabels"] == {"k8s-app": target}):
                            return True
                    elif "podSelector" in peer and namespace == policy["metadata"]["namespace"]:
                        if peer["podSelector"]["matchLabels"]["app"] == target:
                            return True
                    elif "ipBlock" in peer:
                        variable = validator.VARIABLE_PATTERN.fullmatch(peer["ipBlock"]["cidr"])[1]
                        if ipaddress.ip_address(ip) in ipaddress.ip_network(self.variables[variable]["default"]):
                            return True
        return False

    def test_egress_internal_traffic_matrix(self):
        expected = {("operator", "shore", 8080), ("operator", "edge", 8081),
                    ("edge", "shore", 8080), ("edge", "edgeml-sidecar", 9090),
                    ("shore", "edgeml-sidecar", 9090)}
        for source in ("operator", "shore", "edge", "edgeml-sidecar", "unrelated"):
            for target, port in (("operator", 8090), ("shore", 8080), ("edge", 8081), ("edgeml-sidecar", 9090)):
                with self.subTest(source=source, target=target):
                    self.assertEqual(self.egress_allowed(source, target, port), (source, target, port) in expected)
                    self.assertFalse(self.egress_allowed(source, target, port + 1))
                    self.assertFalse(self.egress_allowed(source, target, port, protocol="UDP"))
                    self.assertFalse(self.egress_allowed(source, target, port, namespace="other-site"))

    def test_dns_requires_both_namespace_and_pod_selectors(self):
        for source in ("operator", "shore", "edge", "edgeml-sidecar"):
            for protocol in ("TCP", "UDP"):
                with self.subTest(source=source, protocol=protocol):
                    self.assertTrue(self.egress_allowed(source, "kube-dns", 53, protocol, "kube-system"))
                    self.assertFalse(self.egress_allowed(source, "other", 53, protocol, "kube-system"))
                    self.assertFalse(self.egress_allowed(source, "kube-dns", 53, protocol, "other"))
                    self.assertFalse(self.egress_allowed(source, "kube-dns", 54, protocol, "kube-system"))
        self.assertFalse(self.egress_allowed("unrelated", "kube-dns", 53, namespace="kube-system"))

    def test_external_egress_is_independently_configurable(self):
        flows = {"SHORE": {"edge", "operator"}, "EDGE": {"operator"}, "INFERENCE": {"edge", "shore"},
                 "CAMERA": {"edge"}, "LLM": {"operator"}, "RESEARCH": {"operator"}, "OCSP": {"operator"},
                 "MODEL": {"edgeml-sidecar"}}
        for index, name in enumerate(flows, start=1):
            self.variables[name + "_EGRESS_CIDR"]["default"] = f"203.0.113.{index}/32"
            self.variables[name + "_EGRESS_PORT"]["default"] = str(10000 + index)
        self.validate()
        for index, (name, sources) in enumerate(flows.items(), start=1):
            for source in ("edge", "shore", "operator", "edgeml-sidecar", "unrelated"):
                with self.subTest(flow=name, source=source):
                    ip = f"203.0.113.{index}"
                    self.assertEqual(self.egress_allowed(source, "remote", 10000 + index, ip=ip), source in sources)
                    self.assertFalse(self.egress_allowed(source, "remote", 20000, ip=ip))
                    self.assertFalse(self.egress_allowed(source, "remote", 10000 + index, protocol="UDP", ip=ip))
                    self.assertFalse(self.egress_allowed(source, "remote", 10000 + index, ip="203.0.113.99"))

    def test_missing_egress_default_deny_is_rejected(self):
        self.documents.remove(self.policy("default-deny-egress"))
        with self.assertRaisesRegex(ValueError, "default-deny egress"):
            self.validate()

    def test_empty_namespace_also_requires_egress_default_deny(self):
        self.documents.append({"kind": "Namespace", "metadata": {"name": "empty"}})
        with self.assertRaisesRegex(ValueError, "default-deny egress"):
            validator.check_egress_policies(self.documents, self.variables)
        deny = copy.deepcopy(self.policy("default-deny-egress"))
        deny["metadata"]["namespace"] = "empty"
        self.documents.append(deny)
        validator.check_egress_policies(self.documents, self.variables)

    def test_egress_requires_tcp_and_udp_dns_for_each_source(self):
        for name in ("operator-egress", "shore-egress", "edge-egress", "inference-egress"):
            rules = self.policy(name)["spec"]["egress"]
            before = copy.deepcopy(rules)
            for ports in ([], [{"protocol": "UDP", "port": 53}], [{"protocol": "TCP", "port": 53}]):
                with self.subTest(policy=name, ports=ports):
                    if ports:
                        rules[0]["ports"] = ports
                    else:
                        rules.pop(0)
                    with self.assertRaisesRegex(ValueError, "must reach kube-dns"):
                        self.validate()
                    rules[:] = copy.deepcopy(before)

    def test_egress_rejects_broad_dns_peers(self):
        peer = self.policy("edge-egress")["spec"]["egress"][0]["to"][0]
        before = copy.deepcopy(peer)
        for key in ("namespaceSelector", "podSelector"):
            with self.subTest(selector=key):
                peer[key] = {}
                with self.assertRaisesRegex(ValueError, "DNS must select"):
                    self.validate()
                peer.update(copy.deepcopy(before))

    def test_egress_rejects_unbounded_rules_unknown_selectors_and_ports(self):
        spec = self.policy("edge-egress")["spec"]
        before = copy.deepcopy(spec)
        for mutation in (lambda: spec.update(egress=[{}]),
                         lambda: spec["egress"][1].update(to=[]),
                         lambda: spec["egress"][1].update(ports=[]),
                         lambda: spec["egress"][1]["to"][0]["podSelector"]["matchLabels"].update(app="missing"),
                         lambda: spec["egress"][1]["ports"][0].update(port=8090),
                         lambda: spec["egress"][1]["ports"][0].update(protocol="UDP"),
                         lambda: spec["egress"][1].update(to=[{}]),
                         lambda: spec.update(podSelector={})):
            mutation()
            with self.assertRaises(ValueError):
                self.validate()
            spec.clear()
            spec.update(copy.deepcopy(before))

    def test_egress_rejects_invalid_cidrs_and_ports(self):
        for value in ("invalid", "0.0.0.0/0", "::/0"):
            self.variables["SHORE_EGRESS_CIDR"]["default"] = value
            with self.assertRaises(ValueError):
                self.validate()
        self.variables["SHORE_EGRESS_CIDR"]["default"] = "192.0.2.0/32"
        for value in ("invalid", "0", "65536"):
            self.variables["SHORE_EGRESS_PORT"]["default"] = value
            with self.assertRaises(ValueError):
                self.validate()
        self.variables["SHORE_EGRESS_PORT"]["default"] = "8080"
        del self.variables["SHORE_EGRESS_PORT"]
        with self.assertRaisesRegex(ValueError, "declared Zarf variable"):
            self.validate()

    def test_egress_policy_component_pairing_is_required(self):
        safe_load = yaml.safe_load
        for filename in ("network-edge-egress.yaml", "network-shore-egress.yaml", "network-operator-egress.yaml",
                         "network-edge-inference-egress.yaml", "network-default-deny-egress.yaml"):
            def moved_policy(raw):
                package = safe_load(raw)
                path = "manifests/" + filename
                owner = next(c for c in package["components"]
                             if any(path in g.get("files", []) for g in c.get("manifests", [])))
                other = next(c for c in package["components"] if c is not owner and c.get("manifests"))
                for group in owner["manifests"]:
                    if path in group.get("files", []):
                        group["files"].remove(path)
                other["manifests"][0]["files"].append(path)
                return package

            with self.subTest(policy=filename), patch.object(validator.yaml, "safe_load", side_effect=moved_policy), \
                    patch.object(validator, "render_manifests", return_value=self.parsed):
                with self.assertRaisesRegex(ValueError, "packaged.*component"):
                    validator.check()

    def test_egress_policies_cannot_be_omitted_from_package(self):
        safe_load = yaml.safe_load
        for filename in ("network-edge-egress.yaml", "network-shore-egress.yaml", "network-operator-egress.yaml",
                         "network-edge-inference-egress.yaml", "network-default-deny-egress.yaml"):
            def without_policy(raw):
                package = safe_load(raw)
                for component in package["components"]:
                    for group in component.get("manifests", []):
                        if "manifests/" + filename in group.get("files", []):
                            group["files"].remove("manifests/" + filename)
                return package

            with self.subTest(policy=filename), patch.object(validator.yaml, "safe_load", side_effect=without_policy), \
                    patch.object(validator, "render_manifests", return_value=self.parsed):
                with self.assertRaisesRegex(ValueError, "not included in a component"):
                    validator.check()

    def test_invalid_broad_and_literal_cidrs_are_rejected(self):
        for cidr in ["invalid", "0.0.0.0/0", "::/0"]:
            self.variables["SHORE_INGRESS_CIDR"]["default"] = cidr
            with self.assertRaises(ValueError):
                self.validate()
        peer = self.policy("shore-ingress")["spec"]["ingress"][0]["from"][-1]
        peer["ipBlock"]["cidr"] = "203.0.113.0/24"
        with self.assertRaisesRegex(ValueError, "declared Zarf variable"):
            self.validate()

    def test_policy_missing_from_package_is_rejected(self):
        safe_load = yaml.safe_load

        def without_policy(raw):
            package = safe_load(raw)
            package["components"][0]["manifests"][0]["files"].remove("manifests/network-default-deny.yaml")
            return package

        with patch.object(validator.yaml, "safe_load", side_effect=without_policy), \
                patch.object(validator, "render_manifests", return_value=self.parsed):
            with self.assertRaisesRegex(ValueError, "not included in a component"):
                validator.check()

    def test_policy_in_other_component_is_rejected(self):
        safe_load = yaml.safe_load

        def moved_policy(raw):
            package = safe_load(raw)
            owner = next(c for c in package["components"]
                         if any("manifests/network-shore.yaml" in g.get("files", []) for g in c.get("manifests", [])))
            other = next(c for c in package["components"] if c is not owner and c.get("manifests"))
            for group in owner["manifests"]:
                if "manifests/network-shore.yaml" in group.get("files", []):
                    group["files"].remove("manifests/network-shore.yaml")
            other["manifests"][0]["files"].append("manifests/network-shore.yaml")
            return package

        with patch.object(validator.yaml, "safe_load", side_effect=moved_policy), \
                patch.object(validator, "render_manifests", return_value=self.parsed):
            with self.assertRaisesRegex(ValueError, "same component as its target workload"):
                validator.check()


class TLSManifestTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.package = yaml.safe_load((checker.ROOT / "zarf.yaml").read_text())
        cls.profiles = {}
        for modes in itertools.product((False, True), repeat=3):
            overrides = {name: f"test-{name.lower().replace('_', '-')}"
                         for name, enabled in zip(("SHORE_TLS_SECRET", "EDGE_TLS_SECRET", "SHORE_CA_SECRET"), modes)
                         if enabled}
            parsed = checker.render_manifests(cls.package, overrides)
            cls.profiles[modes] = ([doc for docs in parsed.values() for doc in docs],
                                   checker.package_variables(cls.package, overrides))

    def profile(self, modes=(True, True, True)):
        return copy.deepcopy(self.profiles[modes])

    def workload(self, documents, name):
        return next(doc for doc in documents if doc["kind"] in checker.WORKLOAD_KINDS
                    and doc["metadata"]["name"] == name)

    def container(self, documents, name):
        return self.workload(documents, name)["spec"]["template"]["spec"]["containers"][0]

    def volume(self, documents, workload, name):
        pod = self.workload(documents, workload)["spec"]["template"]["spec"]
        return next(volume for volume in pod["volumes"] if volume["name"] == name)

    def test_all_native_zarf_tls_combinations_validate(self):
        for modes, (documents, variables) in self.profiles.items():
            with self.subTest(modes=modes):
                checker.check_tls_wiring(documents, variables)

    def test_plaintext_default_preserves_flags_urls_and_probes(self):
        documents, _ = self.profile((False, False, False))
        for name in ("shore", "edge", "operator"):
            container = self.container(documents, name)
            if name != "operator":
                self.assertNotIn("args", container)
            else:
                self.assertEqual(container["args"], ["-addr=:8090", "-shore=http://shore:8080",
                                                      "-edge=", "-link-listen=",
                                                      "-review-log=/data/reviews.jsonl"])
            for probe in ("readinessProbe", "livenessProbe"):
                self.assertNotIn("scheme", container[probe]["httpGet"])
            pod = self.workload(documents, name)["spec"]["template"]["spec"]
            self.assertFalse({"listener-tls", "shore-ca"} & {volume["name"] for volume in pod["volumes"]})
        edge_config = next(doc for doc in documents if doc["metadata"]["name"] == "edge-config")
        self.assertEqual(edge_config["data"]["SHORE_URL"], "http://shore.ghost-seeker.svc.cluster.local:8080")

    def test_rollout_annotations_change_for_each_tls_and_ca_switch(self):
        plaintext, _ = self.profile((False, False, False))
        for modes, affected in (((True, False, False), {"shore", "edge", "operator"}),
                                ((False, True, False), {"edge"}),
                                ((False, False, True), {"edge", "operator"}),
                                ((True, True, True), {"shore", "edge", "operator"})):
            documents, _ = self.profile(modes)
            for name in ("shore", "edge", "operator"):
                with self.subTest(modes=modes, workload=name):
                    before = self.workload(plaintext, name)["spec"]["template"]["metadata"]["annotations"]
                    after = self.workload(documents, name)["spec"]["template"]["metadata"]["annotations"]
                    self.assertEqual(before != after, name in affected)

    def test_url_only_changes_roll_the_consuming_workload(self):
        plaintext, _ = self.profile((False, False, False))
        for variable, name in (("SHORE_URL", "edge"), ("OPERATOR_SHORE_URL", "operator")):
            with self.subTest(variable=variable):
                overrides = {variable: "https://shore.example:9443"}
                parsed = checker.render_manifests(self.package, overrides)
                documents = [doc for docs in parsed.values() for doc in docs]
                checker.check_tls_wiring(documents, checker.package_variables(self.package, overrides))
                before = self.workload(plaintext, name)["spec"]["template"]["metadata"]["annotations"]
                after = self.workload(documents, name)["spec"]["template"]["metadata"]["annotations"]
                self.assertNotEqual(before, after)
                self.assertEqual(after["ghost-seeker.io/shore-url"], overrides[variable])

    def test_rejects_missing_or_stale_rollout_annotations(self):
        for name, key in (("shore", "listener-tls-secret"), ("edge", "listener-tls-secret"),
                          ("edge", "shore-tls-secret"), ("operator", "shore-ca-secret"),
                          ("edge", "shore-url"), ("operator", "shore-url")):
            for stale in (False, True):
                with self.subTest(workload=name, key=key, stale=stale):
                    documents, variables = self.profile()
                    annotations = self.workload(documents, name)["spec"]["template"]["metadata"]["annotations"]
                    if stale:
                        annotations[f"ghost-seeker.io/{key}"] = "old-value"
                    else:
                        del annotations[f"ghost-seeker.io/{key}"]
                    with self.assertRaisesRegex(ValueError, "rollout annotation"):
                        checker.check_tls_wiring(documents, variables)

    def test_tls_mounts_cert_key_and_ca_read_only(self):
        documents, variables = self.profile()
        for name in ("shore", "edge"):
            container = self.container(documents, name)
            self.assertIn("-tls-cert=/run/listener-tls/tls.crt", container["args"])
            self.assertIn("-tls-key=/run/listener-tls/tls.key", container["args"])
            secret = self.volume(documents, name, "listener-tls")["secret"]
            self.assertEqual(secret["secretName"], variables[f"{name.upper()}_TLS_SECRET"])
            self.assertEqual(secret["defaultMode"], 0o440)
            self.assertFalse(secret.get("optional", False))
        for name in ("edge", "operator"):
            self.assertIn("-shore-ca=/run/shore-ca/ca.crt", self.container(documents, name)["args"])
            mounts = self.container(documents, name)["volumeMounts"]
            self.assertTrue(next(mount for mount in mounts if mount["name"] == "shore-ca")["readOnly"])

    def test_ca_only_does_not_turn_on_listener_tls(self):
        documents, _ = self.profile((False, False, True))
        self.assertNotIn("args", self.container(documents, "shore"))
        self.assertEqual(self.container(documents, "edge")["args"], ["-shore-ca=/run/shore-ca/ca.crt"])
        for name in ("edge", "shore"):
            self.assertNotIn("scheme", self.container(documents, name)["readinessProbe"]["httpGet"])

    def test_shore_tls_upgrades_default_clients_not_other_listeners(self):
        documents, _ = self.profile((True, False, False))
        self.assertIn("-shore=https://shore:8080", self.container(documents, "operator")["args"])
        edge_config = next(doc for doc in documents if doc["metadata"]["name"] == "edge-config")
        self.assertEqual(edge_config["data"]["SHORE_URL"], "https://shore.ghost-seeker.svc.cluster.local:8080")
        for name in ("edge", "operator"):
            self.assertNotIn("scheme", self.container(documents, name)["readinessProbe"]["httpGet"])

    def test_custom_upstream_urls_are_preserved(self):
        overrides = {"SHORE_TLS_SECRET": "shore-tls", "SHORE_CA_SECRET": "shore-ca",
                     "SHORE_URL": "https://shore.example:9443/sync?source=edge,live",
                     "OPERATOR_SHORE_URL": "https://operator-upstream.example:9443"}
        parsed = checker.render_manifests(self.package, overrides)
        documents = [doc for docs in parsed.values() for doc in docs]
        checker.check_tls_wiring(documents, checker.package_variables(self.package, overrides))
        edge_config = next(doc for doc in documents if doc["metadata"]["name"] == "edge-config")
        self.assertEqual(edge_config["data"]["SHORE_URL"], overrides["SHORE_URL"])
        self.assertIn(f"-shore={overrides['OPERATOR_SHORE_URL']}", self.container(documents, "operator")["args"])

    def test_system_roots_need_no_ca_mount(self):
        documents, _ = self.profile((True, True, False))
        for name in ("edge", "operator"):
            self.assertFalse(any(arg.startswith("-shore-ca") for arg in self.container(documents, name)["args"]))

    def test_apostrophe_upstream_urls_render_without_yaml_corruption(self):
        url = "https://shore.example/team's/sync"
        for tls in (False, True):
            with self.subTest(tls=tls):
                overrides = {"SHORE_URL": url, "OPERATOR_SHORE_URL": url}
                if tls:
                    overrides.update({"SHORE_TLS_SECRET": "shore-tls", "SHORE_CA_SECRET": "shore-ca"})
                parsed = checker.render_manifests(self.package, overrides)
                documents = [doc for docs in parsed.values() for doc in docs]
                checker.check_tls_wiring(documents, checker.package_variables(self.package, overrides))
                config = next(doc for doc in documents if doc["metadata"]["name"] == "edge-config")
                self.assertEqual(config["data"]["SHORE_URL"], url)
                self.assertIn(f"-shore={url}", self.container(documents, "operator")["args"])
                for name in ("edge", "operator"):
                    annotations = self.workload(documents, name)["spec"]["template"]["metadata"]["annotations"]
                    self.assertEqual(annotations["ghost-seeker.io/shore-url"], url)

    def test_rejects_invalid_or_drifted_upstream_urls(self):
        for value in ("not-a-url", "ftp://shore.example", "http://shore.ghost-seeker.svc:8080"):
            with self.subTest(value=value):
                documents, variables = self.profile()
                edge_config = next(doc for doc in documents if doc["metadata"]["name"] == "edge-config")
                variables["SHORE_URL"] = value
                edge_config["data"]["SHORE_URL"] = value
                with self.assertRaises(ValueError):
                    checker.check_tls_wiring(documents, variables)
        documents, variables = self.profile()
        self.container(documents, "operator")["args"][1] = "-shore=http://shore:8080"
        with self.assertRaisesRegex(ValueError, "rendered shore URL"):
            checker.check_tls_wiring(documents, variables)

    def test_rejects_missing_unpaired_duplicate_or_wrong_flags(self):
        for mode in ("missing", "unpaired", "duplicate", "wrong-path"):
            with self.subTest(mode=mode):
                documents, variables = self.profile()
                args = self.container(documents, "edge")["args"]
                if mode == "missing":
                    args.clear()
                elif mode == "unpaired":
                    args.remove("-tls-key=/run/listener-tls/tls.key")
                elif mode == "duplicate":
                    args.append(args[0])
                else:
                    args[0] = "-tls-cert=/wrong/cert"
                with self.assertRaisesRegex(ValueError, "TLS flags"):
                    checker.check_tls_wiring(documents, variables)

    def test_rejects_bad_secret_projections_and_permissions(self):
        mutations = [lambda secret: secret.update(secretName="wrong-secret"),
                     lambda secret: secret.update(optional=True),
                     lambda secret: secret.update(defaultMode=0o444),
                     lambda secret: secret.update(items=[]),
                     lambda secret: secret["items"][0].update(path="wrong-path")]
        for name, volume in (("shore", "listener-tls"), ("edge", "listener-tls"), ("operator", "shore-ca")):
            for mutation in mutations:
                with self.subTest(workload=name, volume=volume, mutation=mutation):
                    documents, variables = self.profile()
                    mutation(self.volume(documents, name, volume)["secret"])
                    with self.assertRaises(ValueError):
                        checker.check_tls_wiring(documents, variables)

    def test_rejects_missing_writable_relocated_or_partial_mount(self):
        for mode in ("missing", "writable", "relocated", "partial", "missing-volume", "missing-fsgroup"):
            with self.subTest(mode=mode):
                documents, variables = self.profile()
                container = self.container(documents, "shore")
                mount = next(mount for mount in container["volumeMounts"] if mount["name"] == "listener-tls")
                pod = self.workload(documents, "shore")["spec"]["template"]["spec"]
                if mode == "missing":
                    container["volumeMounts"].remove(mount)
                elif mode == "writable":
                    mount["readOnly"] = False
                elif mode == "relocated":
                    mount["mountPath"] = "/wrong"
                elif mode == "partial":
                    mount["subPath"] = "tls.crt"
                elif mode == "missing-volume":
                    pod["volumes"].remove(self.volume(documents, "shore", "listener-tls"))
                else:
                    del pod["securityContext"]["fsGroup"]
                with self.assertRaises(ValueError):
                    checker.check_tls_wiring(documents, variables)

    def test_rejects_probe_scheme_or_endpoint_mismatch(self):
        for modes in ((False, False, False), (True, True, True)):
            for name in ("shore", "edge", "operator"):
                for probe in ("readinessProbe", "livenessProbe"):
                    with self.subTest(modes=modes, workload=name, probe=probe):
                        documents, variables = self.profile(modes)
                        http_get = self.container(documents, name)[probe]["httpGet"]
                        http_get["scheme"] = "HTTP" if http_get.get("scheme") == "HTTPS" else "HTTPS"
                        with self.assertRaisesRegex(ValueError, "scheme"):
                            checker.check_tls_wiring(documents, variables)
        documents, variables = self.profile()
        self.container(documents, "shore")["readinessProbe"]["httpGet"]["path"] = "/wrong"
        with self.assertRaisesRegex(ValueError, "health endpoint"):
            checker.check_tls_wiring(documents, variables)

    def test_rejects_tls_environment_bypass(self):
        documents, variables = self.profile((False, False, False))
        self.container(documents, "edge")["env"].append({"name": "GS_TLS_CERT", "value": "/hidden/cert"})
        with self.assertRaisesRegex(ValueError, "environment"):
            checker.check_tls_wiring(documents, variables)
        documents, variables = self.profile((False, False, False))
        config = next(doc for doc in documents if doc["metadata"]["name"] == "shore-config")
        config["data"]["GS_TLS_KEY"] = "/hidden/key"
        with self.assertRaisesRegex(ValueError, "ConfigMaps"):
            checker.check_tls_wiring(documents, variables)

    def test_rejects_disabled_secret_mount_or_flags(self):
        for name in ("SHORE_TLS_SECRET", "EDGE_TLS_SECRET", "SHORE_CA_SECRET"):
            with self.subTest(variable=name):
                documents, variables = self.profile()
                variables[name] = ""
                with self.assertRaises(ValueError):
                    checker.check_tls_wiring(documents, variables)

    def test_rejects_missing_variable_opt_out_defaults_and_invalid_names(self):
        for change in ("missing", "enabled-default", "missing-pattern"):
            with self.subTest(change=change):
                package = copy.deepcopy(self.package)
                entry = next(entry for entry in package["variables"] if entry["name"] == "SHORE_TLS_SECRET")
                if change == "missing":
                    package["variables"].remove(entry)
                elif change == "enabled-default":
                    entry["default"] = "shore-tls"
                else:
                    del entry["pattern"]
                with self.assertRaises(ValueError):
                    checker.package_variables(package, {})
        for name in ("UPPER", "../../secret", 'injection"', "x" * 64):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, "pattern"):
                checker.package_variables(self.package, {"SHORE_TLS_SECRET": name})
        with self.assertRaisesRegex(ValueError, "undeclared"):
            checker.package_variables(self.package, {"UNKNOWN_SECRET": "test"})

    def test_rejects_tls_without_manifest_templating(self):
        package = copy.deepcopy(self.package)
        group = next(component for component in package["components"] if component["name"] == "shore")["manifests"][0]
        group["template"] = False
        with mock.patch.object(checker.yaml, "safe_load", return_value=package):
            with self.assertRaisesRegex(ValueError, "template: true"):
                checker.check()

    def test_missing_zarf_reports_required_renderer(self):
        with mock.patch.object(checker.subprocess, "run", side_effect=FileNotFoundError):
            with self.assertRaisesRegex(ValueError, "Zarf v0.89"):
                checker.render_manifests(self.package, {})

    def test_rendering_failure_and_missing_sources_are_errors(self):
        failure = checker.subprocess.CalledProcessError(1, "zarf", stderr="invalid template")
        with mock.patch.object(checker.subprocess, "run", side_effect=failure):
            with self.assertRaisesRegex(ValueError, "invalid template"):
                checker.render_manifests(self.package, {})
        with mock.patch.object(checker.subprocess, "run", return_value=mock.Mock(stdout="")):
            with self.assertRaisesRegex(ValueError, "every packaged manifest"):
                checker.render_manifests(self.package, {})


class FramesTests(unittest.TestCase):
    def setUp(self):
        self.package = yaml.safe_load((validator.ROOT / "zarf.yaml").read_text())

    def render(self, overrides):
        documents = [d for docs in validator.render_manifests(self.package, overrides).values() for d in docs]
        validator.check_frames(documents, validator.package_variables(self.package, overrides))
        return documents

    def test_default_hostpath_and_custom_path(self):
        self.render({})
        self.render({"FRAMES_HOSTPATH": "/sensor/live"})

    def test_pvc_default_and_custom_storage(self):
        self.render({"FRAMES_SOURCE": "pvc"})
        self.render({"FRAMES_SOURCE": "pvc", "FRAMES_STORAGE": "20Gi", "FRAMES_STORAGE_CLASS": "sensor"})

    def test_invalid_and_empty_source_rejected(self):
        for source in ("", "emptyDir", "PVC"):
            with self.assertRaisesRegex(ValueError, "FRAMES_SOURCE"):
                validator.package_variables(self.package, {"FRAMES_SOURCE": source})

    def test_uds_hostpath_anywhere_rejected(self):
        documents = self.render({"FRAMES_SOURCE": "pvc"})
        documents.append({"kind": "Pod", "spec": {"volumes": [{"hostPath": {"path": "/sensor"}}]}})
        with self.assertRaisesRegex(ValueError, "any hostPath"):
            validator.check_frames(documents, validator.package_variables(self.package, {"FRAMES_SOURCE": "pvc"}))

    def test_uds_guard(self):
        guard = next(c for c in self.package["components"] if c["name"] == "uds-package")["actions"]["onDeploy"]["before"][0]["cmd"]
        for source, expected in (("pvc", 0), ("hostPath", 1), ("", 1)):
            result = subprocess.run(["sh", "-c", guard], env={"ZARF_VAR_FRAMES_SOURCE": source}, capture_output=True)
            self.assertEqual(result.returncode, expected)


class ModelTests(unittest.TestCase):
    def setUp(self):
        self.package = yaml.safe_load((validator.ROOT / "zarf.yaml").read_text())
        self.documents = validator.render_manifests(self.package, {})[validator.ROOT / "manifests/edge-inference.yaml"]
        self.pod = self.documents[0]["spec"]["template"]["spec"]

    def validate(self):
        validator.check_models(self.package, self.documents)

    def test_valid_models(self):
        self.validate()

    def test_missing_init_and_unlisted_image(self):
        self.pod["initContainers"] = []
        with self.assertRaisesRegex(ValueError, "listed edgeml-models"):
            self.validate()
        self.setUp()
        self.package["components"][-1]["images"] = []
        with self.assertRaisesRegex(ValueError, "listed edgeml-models"):
            self.validate()

    def test_checksum_verification_required(self):
        self.pod["initContainers"][0]["args"] = ["cp -R /models-src/. /models/"]
        with self.assertRaisesRegex(ValueError, "verify MODEL_SHA256"):
            self.validate()

    def test_readonly_mount_required(self):
        self.pod["containers"][0]["volumeMounts"][0]["readOnly"] = False
        with self.assertRaisesRegex(ValueError, "mount /models readOnly"):
            self.validate()

    def test_mock_and_external_detector_rejected(self):
        for args in (["--mock"], ["--detector", "/tmp/model.onnx"]):
            self.pod["containers"][0]["args"] = args
            with self.assertRaisesRegex(ValueError, "real --detector"):
                self.validate()

    def test_missing_patterns(self):
        for name in ("MODEL_SHA256", "MODEL_DETECTOR", "MODEL_EMBED"):
            self.setUp()
            next(v for v in self.package["variables"] if v["name"] == name).pop("pattern")
            with self.assertRaisesRegex(ValueError, "must have a pattern"):
                self.validate()

    def test_variable_values(self):
        for name, values in {"MODEL_SHA256": ["", "a" * 64],
                             "MODEL_DETECTOR": ["yolo11n-640/model.onnx", "custom/detector.onnx"]}.items():
            for value in values:
                validator.package_variables(self.package, {name: value})
        for name, values in {"MODEL_SHA256": ["bad", "g" * 64],
                             "MODEL_DETECTOR": ["", "/model.onnx", "../model.onnx", "a/../b", "a..b"]}.items():
            for value in values:
                with self.assertRaisesRegex(ValueError, "Zarf variable pattern"):
                    validator.package_variables(self.package, {name: value})

    def test_wrong_default_hash_rejected(self):
        next(v for v in self.package["variables"] if v["name"] == "MODEL_SHA256")["default"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "match committed"):
            self.validate()

    def test_catalog_volume_rejected(self):
        self.pod["volumes"].append({"name": "catalog", "emptyDir": {}})
        with self.assertRaisesRegex(ValueError, "catalog packaged in its image"):
            self.validate()

    def test_catalog_mount_rejected(self):
        self.pod["containers"][0]["volumeMounts"].append({"name": "catalog", "mountPath": "/catalog"})
        with self.assertRaisesRegex(ValueError, "catalog packaged in its image"):
            self.validate()

    def test_catalog_pins_match_source(self):
        original = validator.hashlib.sha256
        with mock.patch.object(validator.hashlib, "sha256", side_effect=lambda data: original(data + b"changed")
                               if data.startswith(b"{") else original(data)):
            with self.assertRaisesRegex(ValueError, "catalog checksum must match"):
                self.validate()


if __name__ == "__main__":
    unittest.main()
