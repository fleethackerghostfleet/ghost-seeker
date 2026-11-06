import copy
import itertools
import unittest
from unittest import mock

import yaml

import check_manifests as checker


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
