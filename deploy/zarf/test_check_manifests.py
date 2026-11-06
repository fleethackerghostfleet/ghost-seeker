import copy
import ipaddress
import unittest
from unittest.mock import patch

import yaml

import check_manifests as validator


class NetworkPolicyTests(unittest.TestCase):
    def setUp(self):
        self.documents = [doc for path in sorted((validator.ROOT / "manifests").glob("*.yaml"))
                          for doc in validator.read_documents(path)]
        package = yaml.safe_load((validator.ROOT / "zarf.yaml").read_text())
        self.variables = {entry["name"]: entry for entry in package["variables"]}
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

            with self.subTest(policy=filename), patch.object(validator.yaml, "safe_load", side_effect=moved_policy):
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

            with self.subTest(policy=filename), patch.object(validator.yaml, "safe_load", side_effect=without_policy):
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

        with patch.object(validator.yaml, "safe_load", side_effect=without_policy):
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

        with patch.object(validator.yaml, "safe_load", side_effect=moved_policy):
            with self.assertRaisesRegex(ValueError, "same component as its target workload"):
                validator.check()


if __name__ == "__main__":
    unittest.main()
