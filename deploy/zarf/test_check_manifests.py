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
            for rule in spec["ingress"]:
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
        self.assertEqual(self.validate(), 5)

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

    def test_empty_source_rule_and_egress_are_rejected(self):
        spec = self.policy("shore-ingress")["spec"]
        before = copy.deepcopy(spec)
        spec["ingress"][0]["from"] = []
        with self.assertRaisesRegex(ValueError, "restrict sources"):
            self.validate()
        spec.update(before)
        spec["policyTypes"].append("Egress")
        with self.assertRaisesRegex(ValueError, "ingress-only"):
            self.validate()

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


if __name__ == "__main__":
    unittest.main()
