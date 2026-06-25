import pathlib
import subprocess
import sys
import tempfile
import unittest
import json
from io import StringIO
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import production_readiness_cli


def parse_args(*values):
    return production_readiness_cli.build_parser().parse_args(["check", *values])


def ok_result(command):
    return subprocess.CompletedProcess(command, 0, stdout="ok", stderr="")


class ProductionReadinessCliTest(unittest.TestCase):
    def test_default_local_governance_passes_and_live_gates_warn(self):
        commands = []

        def fake_run(command, cwd, timeout=240):
            commands.append(command)
            return ok_result(command)

        with patch.object(production_readiness_cli, "run_command", fake_run):
            results = production_readiness_cli.run_checks(parse_args())

        self.assertEqual(production_readiness_cli.result_exit_code(results), 0, results)
        self.assertEqual(len(commands), 4)
        self.assertTrue(any("fixture_golden_cli.py" in " ".join(command) for command in commands))
        self.assertTrue(any(result.name == "Production IdP live acceptance" and result.status == "warn" for result in results))
        self.assertTrue(any(result.name == "Object-lock bucket live acceptance" and result.status == "warn" for result in results))
        self.assertTrue(any(result.name == "GitLab package live acceptance" and result.status == "warn" for result in results))
        self.assertTrue(any(result.name == "Windows runner production readiness" and result.status == "warn" for result in results))
        self.assertTrue(any(result.name == "Nomad cluster live acceptance" and result.status == "warn" for result in results))
        self.assertTrue(any(result.name == "Artifact retention live acceptance" and result.status == "warn" for result in results))
        self.assertTrue(any(result.name == "Notification routing live acceptance" and result.status == "warn" for result in results))

    def test_required_live_gate_fails_when_not_enabled(self):
        with patch.object(production_readiness_cli, "run_command", return_value=ok_result(["check"])):
            results = production_readiness_cli.run_checks(
                parse_args(
                    "--require-idp-live",
                    "--require-object-lock-live",
                    "--require-gitlab-live",
                    "--require-windows-live",
                    "--require-nomad-live",
                    "--require-artifact-retention-live",
                    "--require-notification-routing-live",
                )
            )

        self.assertEqual(production_readiness_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Production IdP live acceptance" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Object-lock bucket live acceptance" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "GitLab package live acceptance" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Windows runner production readiness" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Nomad cluster live acceptance" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Artifact retention live acceptance" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Notification routing live acceptance" and result.status == "fail" for result in results))

    def test_required_broker_fails_when_idp_live_is_not_enabled(self):
        with patch.object(production_readiness_cli, "run_command", return_value=ok_result(["check"])):
            results = production_readiness_cli.run_checks(parse_args("--require-broker"))

        self.assertEqual(production_readiness_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Production IdP live acceptance" and result.status == "warn" for result in results))
        self.assertTrue(any(result.name == "OIDC broker live acceptance" and result.status == "fail" for result in results))

    def test_live_gates_delegate_to_specialized_check_scripts(self):
        commands = []

        def fake_run(command, cwd, timeout=240):
            commands.append(command)
            return ok_result(command)

        args = parse_args(
            "--skip-local-governance",
            "--idp-live",
            "--broker-live",
            "--broker-logout",
            "--require-session-store",
            "--api-url",
            "https://api.example.test",
            "--oidc-issuer",
            "https://idp.example.test",
            "--oidc-audience",
            "packaging-platform",
            "--oidc-jwks-url",
            "https://idp.example.test/certs",
            "--oidc-discovery-url",
            "https://idp.example.test/.well-known/openid-configuration",
            "--oidc-authorization-url",
            "https://idp.example.test/auth",
            "--oidc-token-url",
            "https://idp.example.test/token",
            "--oidc-expected-role",
            "packaging-operator",
            "--oidc-token",
            "jwt",
            "--broker-code",
            "code",
            "--broker-code-verifier",
            "verifier",
            "--broker-redirect-uri",
            "https://admin.example.test/callback",
            "--object-lock-live",
            "--rustfs-endpoint",
            "rustfs.example.test:9000",
            "--rustfs-access-key",
            "access",
            "--rustfs-secret-key",
            "secret",
            "--rustfs-object-lock-bucket",
            "packaging-audit-lock",
            "--rustfs-retention-mode",
            "COMPLIANCE",
            "--rustfs-retention-days",
            "3650",
            "--gitlab-live",
            "--gitlab-base-url",
            "https://gitlab.example.test",
            "--gitlab-token",
            "gitlab-token",
            "--gitlab-path",
            "42/engine/1.2.3/engine.tar.gz",
            "--gitlab-expected-sha256",
            "1" * 64,
            "--gitlab-require-token",
            "--windows-live",
            "--windows-packager-command",
            r"iscc C:\setup.iss",
            "--windows-signing-command",
            r"signtool sign %OUTPUT%",
            "--allow-non-windows",
            "--nomad-live",
            "--nomad-address",
            "https://nomad.example.test:4646",
            "--nomad-token",
            "nomad-token",
            "--require-nomad-linux-node",
            "--require-nomad-windows-node",
            "--artifact-retention-live",
            "--artifact-retention-token",
            "api-token",
            "--artifact-retention-max-expired",
            "1",
            "--notification-routing-live",
            "--notification-routing-token",
            "api-token",
            "--notification-route",
            "critical=https://hooks.example.test/critical",
            "--notification-route",
            "error=https://hooks.example.test/error",
            "--notification-send",
        )

        with patch.object(production_readiness_cli, "run_command", fake_run):
            results = production_readiness_cli.run_checks(args)

        self.assertEqual(production_readiness_cli.result_exit_code(results), 0, results)
        self.assertEqual(len(commands), 7)
        self.assertTrue(any("oidc_check_cli.py" in " ".join(command) and "--broker-live" in command for command in commands))
        self.assertTrue(any("rustfs_check.py" in " ".join(command) and "--object-lock" in command for command in commands))
        self.assertTrue(any("gitlab_package_check_cli.py" in " ".join(command) and "--require-token" in command for command in commands))
        self.assertTrue(any("windows_runner_check_cli.py" in " ".join(command) and "--allow-non-windows" in command for command in commands))
        self.assertTrue(any("nomad_check_cli.py" in " ".join(command) and "--require-linux-node" in command and "--require-windows-node" in command for command in commands))
        self.assertTrue(any("artifact_retention_cli.py" in " ".join(command) and "--max-expired-artifacts" in command for command in commands))
        self.assertTrue(any("notification_routing_cli.py" in " ".join(command) and "--send" in command for command in commands))

    def test_child_gate_failure_fails_aggregate_gate(self):
        def fake_run(command, cwd, timeout=240):
            if any("oidc_check_cli.py" in item for item in command):
                return subprocess.CompletedProcess(command, 1, stdout="Confirmation lights:\n[FAIL] OIDC", stderr="")
            return ok_result(command)

        with patch.object(production_readiness_cli, "run_command", fake_run):
            results = production_readiness_cli.run_checks(parse_args("--skip-local-governance", "--idp-live"))

        self.assertEqual(production_readiness_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Production IdP live acceptance" and result.status == "fail" for result in results))

    def test_json_output_prints_machine_readable_report(self):
        with patch.object(production_readiness_cli, "run_command", return_value=ok_result(["check"])):
            with patch.object(sys, "argv", ["production_readiness_cli.py", "check", "--skip-local-governance", "--json"]):
                stdout = StringIO()
                with patch("sys.stdout", stdout):
                    exit_code = production_readiness_cli.main()

        payload = json.loads(stdout.getvalue())
        self.assertEqual(exit_code, 0)
        self.assertEqual(payload["schema"], "packaging.production_readiness.report/v1")
        self.assertEqual(payload["status"], "warn")
        self.assertEqual(payload["exitCode"], 0)
        self.assertEqual(payload["summary"], {"total": 7, "ok": 0, "warn": 7, "fail": 0})
        self.assertEqual([gate["status"] for gate in payload["gates"]], ["warn", "warn", "warn", "warn", "warn", "warn", "warn"])
        self.assertNotIn("Confirmation lights", stdout.getvalue())

    def test_report_file_writes_json_and_keeps_confirmation_lights(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            report_path = pathlib.Path(temp_dir) / "readiness.json"
            with patch.object(production_readiness_cli, "run_command", return_value=ok_result(["check"])):
                with patch.object(
                    sys,
                    "argv",
                    [
                        "production_readiness_cli.py",
                        "check",
                        "--skip-local-governance",
                        "--require-idp-live",
                        "--report-file",
                        str(report_path),
                    ],
                ):
                    stdout = StringIO()
                    with patch("sys.stdout", stdout):
                        exit_code = production_readiness_cli.main()

            payload = json.loads(report_path.read_text(encoding="utf-8"))
            self.assertEqual(exit_code, 1)
            self.assertIn("Confirmation lights", stdout.getvalue())
            self.assertEqual(payload["status"], "fail")
            self.assertEqual(payload["exitCode"], 1)
            self.assertEqual(payload["summary"]["fail"], 1)
            self.assertTrue(any(gate["name"] == "Production IdP live acceptance" and gate["status"] == "fail" for gate in payload["gates"]))

    def test_report_masks_secret_like_details(self):
        results = [
            production_readiness_cli.CheckResult(
                "Production IdP live acceptance",
                "fail",
                "token=secret-jwt client_secret=top-secret password=hunter2 api_key=abc123 Authorization: Bearer bearer-token --token flag-token",
            )
        ]

        payload = production_readiness_cli.build_report(results, exit_code=1)
        text = json.dumps(payload)

        self.assertNotIn("secret-jwt", text)
        self.assertNotIn("top-secret", text)
        self.assertNotIn("hunter2", text)
        self.assertNotIn("abc123", text)
        self.assertNotIn("bearer-token", text)
        self.assertNotIn("flag-token", text)
        self.assertIn("<redacted>", text)


if __name__ == "__main__":
    unittest.main()
