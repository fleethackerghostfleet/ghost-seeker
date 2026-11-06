import copy
import ipaddress
import itertools
import unittest
from unittest import mock
from unittest.mock import patch

import yaml

import check_manifests as validator
import check_manifests as checker


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
                                                      "-edge=", "-link-listen="])
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


if __name__ == "__main__":
    unittest.main()
