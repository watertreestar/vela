import pathlib
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import nomad_check_cli


def parse_args(*values):
    return nomad_check_cli.build_parser().parse_args(["check", *values])


class NomadCheckCliTest(unittest.TestCase):
    def test_production_requires_tls_unless_explicitly_allowed(self):
        results = nomad_check_cli.validate(parse_args("--production", "--address", "http://nomad.internal:4646"))

        self.assertTrue(any(result.name == "Nomad address uses TLS" and result.status == "fail" for result in results))

    def test_validates_leader_and_required_node_classes(self):
        requests = []

        def fake_request_json(url, token, timeout):
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
        def fake_request_json(url, token, timeout):
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


if __name__ == "__main__":
    unittest.main()
