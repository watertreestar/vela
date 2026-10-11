import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import nomad_check_cli


def parse_args(*values):
    return nomad_check_cli.build_parser().parse_args(["check", *values])


class NomadCheckCliTest(unittest.TestCase):
    def test_client_certificate_rejection_requires_tls_handshake_evidence(self):
        self.assertTrue(
            nomad_check_cli.client_certificate_rejected(
                0,
                "[SSL: TLSV13_ALERT_CERTIFICATE_REQUIRED] tlsv13 alert certificate required",
            )
        )
        self.assertFalse(nomad_check_cli.client_certificate_rejected(0, "connection refused"))
        self.assertFalse(nomad_check_cli.client_certificate_rejected(403, "HTTP 403"))

    def test_production_requires_tls_unless_explicitly_allowed(self):
        results = nomad_check_cli.validate(parse_args("--production", "--address", "http://nomad.internal:4646"))

        self.assertTrue(any(result.name == "Nomad address uses TLS" and result.status == "fail" for result in results))

    def test_validates_leader_and_required_node_classes(self):
        requests = []

        def fake_request_json(url, token, timeout, context=None):
            requests.append((url, token))
            if url.endswith("/v1/status/leader"):
                return '"10.0.0.10:4647"'
            if url.endswith("/v1/nodes"):
                return [
                    {"ID": "linux-1", "Status": "ready", "Meta": {"node_class": "linux-packager"}, "Attributes": {"kernel.name": "linux"}},
                    {"ID": "windows-1", "Status": "ready", "Meta": {"node_class": "windows-packager"}, "Attributes": {"kernel.name": "windows"}},
                ]
            raise AssertionError(url)

        with patch.object(nomad_check_cli, "request_json", fake_request_json):
            results = nomad_check_cli.validate(
                parse_args(
                    "--production",
                    "--allow-http",
                    "--address",
                    "http://nomad.internal:4646",
                    "--token",
                    "nomad-secret",
                    "--require-linux-node",
                    "--require-windows-node",
                )
            )

        self.assertEqual(nomad_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "Nomad leader" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Nomad Linux node class" and result.status == "ok" and "linux-packager=1" in result.detail for result in results))
        self.assertTrue(any(result.name == "Nomad Windows node class" and result.status == "ok" and "windows-packager=1" in result.detail for result in results))
        self.assertTrue(all(token == "nomad-secret" for _, token in requests))
        self.assertNotIn("nomad-secret", "\n".join(result.detail for result in results))

    def test_required_node_class_fails_when_missing(self):
        def fake_request_json(url, token, timeout, context=None):
            if url.endswith("/v1/status/leader"):
                return '"10.0.0.10:4647"'
            if url.endswith("/v1/nodes"):
                return [
                    {"ID": "linux-1", "Status": "ready", "Meta": {"node_class": "linux-packager"}, "Attributes": {"kernel.name": "linux"}},
                ]
            raise AssertionError(url)

        with patch.object(nomad_check_cli, "request_json", fake_request_json):
            results = nomad_check_cli.validate(parse_args("--address", "http://nomad.internal:4646", "--require-windows-node"))

        self.assertEqual(nomad_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Nomad Windows node class" and result.status == "fail" for result in results))

    def test_hydrates_node_list_stubs_before_class_checks(self):
        requests = []

        def fake_request_json(url, token, timeout, context=None):
            requests.append(url)
            if url.endswith("/v1/status/leader"):
                return '"10.0.0.10:4647"'
            if url.endswith("/v1/nodes"):
                return [{"ID": "linux-1", "Status": "ready"}]
            if url.endswith("/v1/node/linux-1"):
                return {
                    "ID": "linux-1",
                    "Status": "ready",
                    "Meta": {"node_class": "linux-packager"},
                    "Attributes": {"kernel.name": "linux"},
                }
            raise AssertionError(url)

        with patch.object(nomad_check_cli, "request_json", fake_request_json):
            results = nomad_check_cli.validate(
                parse_args("--address", "http://nomad.internal:4646", "--require-linux-node")
            )

        self.assertEqual(nomad_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(url.endswith("/v1/node/linux-1") for url in requests))
        self.assertTrue(any(result.name == "Nomad Linux node class" and result.status == "ok" for result in results))

    def test_acl_gate_requires_client_token_and_denials(self):
        def fake_request_json(url, token, timeout, context=None):
            if url.endswith("/v1/status/leader"):
                return '"10.0.0.10:4647"'
            if url.endswith("/v1/nodes") and token:
                return [
                    {"ID": "linux-1", "Status": "ready", "Meta": {"node_class": "linux-packager"}, "Attributes": {"kernel.name": "linux"}},
                ]
            if url.endswith("/v1/acl/token/self"):
                return {"Type": "client", "Name": "vela-read"}
            raise AssertionError((url, token))

        def fake_request_status(url, token, timeout, context=None):
            if url.endswith("/v1/nodes") and not token:
                return 403, "HTTP 403"
            if url.endswith("/v1/acl/tokens"):
                return 403, "HTTP 403"
            raise AssertionError((url, token))

        with (
            patch.object(nomad_check_cli, "request_json", fake_request_json),
            patch.object(nomad_check_cli, "request_status", fake_request_status),
        ):
            results = nomad_check_cli.validate(
                parse_args("--address", "http://nomad.internal:4646", "--token", "client-token", "--require-acl")
            )

        self.assertEqual(nomad_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "Nomad anonymous ACL denial" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "Nomad ACL administration denial" and result.status == "ok" for result in results))

    def test_acl_gate_rejects_management_token_and_open_anonymous_access(self):
        def fake_request_json(url, token, timeout, context=None):
            if url.endswith("/v1/status/leader"):
                return '"10.0.0.10:4647"'
            if url.endswith("/v1/nodes"):
                return [
                    {"ID": "linux-1", "Status": "ready", "Meta": {}, "NodeClass": "linux-packager", "Attributes": {"kernel.name": "linux"}},
                ]
            if url.endswith("/v1/acl/token/self"):
                return {"Type": "management"}
            raise AssertionError(url)

        with (
            patch.object(nomad_check_cli, "request_json", fake_request_json),
            patch.object(nomad_check_cli, "request_status", return_value=(200, "request accepted")),
        ):
            results = nomad_check_cli.validate(
                parse_args("--address", "http://nomad.internal:4646", "--token", "management-token", "--require-acl")
            )

        self.assertEqual(nomad_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Nomad low-privilege ACL token" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Nomad anonymous ACL denial" and result.status == "fail" for result in results))

    def test_mtls_gate_requires_certificate_files(self):
        results = nomad_check_cli.validate(
            parse_args("--production", "--address", "https://nomad.internal:4646", "--require-mtls")
        )

        self.assertEqual(nomad_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Nomad CA certificate" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Nomad client certificate" and result.status == "fail" for result in results))


if __name__ == "__main__":
    unittest.main()
