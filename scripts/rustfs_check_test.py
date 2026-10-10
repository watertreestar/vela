import pathlib
import sys
import unittest
from argparse import Namespace
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import rustfs_check


class RustFSCheckTest(unittest.TestCase):
    def test_production_mode_requires_object_lock_and_explicit_bucket(self):
        args = Namespace(
            production=True,
            object_lock=False,
            object_lock_bucket="",
            bucket="",
            access_key="rustfsadmin",
            secret_key="rustfsadmin",
            retention_mode="",
            retention_days=0,
        )

        results = rustfs_check.validate_production_inputs(args)

        self.assertTrue(any(result.name == "Production object-lock mode" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Production object-lock bucket" and result.status == "fail" for result in results))

    def test_production_mode_accepts_named_object_lock_bucket(self):
        args = Namespace(
            production=True,
            object_lock=True,
            object_lock_bucket="packaging-audit-lock",
            bucket="",
            access_key="prod-access",
            secret_key="prod-secret",
            retention_mode="COMPLIANCE",
            retention_days=3650,
        )

        results = rustfs_check.validate_production_inputs(args)

        self.assertEqual(rustfs_check.light("ok"), "[OK]")
        self.assertFalse(any(result.status == "fail" for result in results), results)

    def test_object_lock_integration_passes_expected_environment(self):
        captured = {}

        def fake_run(command, cwd, timeout=60, env=None):
            captured["command"] = command
            captured["cwd"] = cwd
            captured["timeout"] = timeout
            captured["env"] = env

            class Result:
                returncode = 0
                stdout = "ok"
                stderr = ""

            return Result()

        args = Namespace(
            endpoint="127.0.0.1:9000",
            integration_endpoint="",
            remote_docker_host="",
            skip_container_check=True,
            access_key="rustfsadmin",
            secret_key="rustfsadmin",
            bucket="packaging-default",
            object_lock_bucket="packaging-lock",
            retention_mode="COMPLIANCE",
            retention_days=3650,
            legal_hold=True,
            use_ssl=True,
            test_timeout=123,
        )
        with patch.object(rustfs_check, "run_command", fake_run):
            result = rustfs_check.run_object_lock_integration(args, pathlib.Path("repo"))

        self.assertEqual(result.status, "ok")
        self.assertEqual(captured["command"][-2:], ["TestAuditArchiveObjectLockWithRustFSIntegration", "-v"])
        self.assertEqual(captured["timeout"], 123)
        self.assertEqual(captured["env"]["PACKAGING_RUSTFS_OBJECT_LOCK_INTEGRATION"], "1")
        self.assertEqual(captured["env"]["RUSTFS_BUCKET"], "packaging-lock")
        self.assertEqual(captured["env"]["RUSTFS_USE_SSL"], "true")
        self.assertEqual(captured["env"]["RUSTFS_OBJECT_LOCK_RETENTION_MODE"], "COMPLIANCE")
        self.assertEqual(captured["env"]["RUSTFS_OBJECT_LOCK_RETENTION_DAYS"], "3650")
        self.assertEqual(captured["env"]["RUSTFS_OBJECT_LOCK_LEGAL_HOLD"], "true")

    def test_local_container_uses_service_endpoint_for_integration(self):
        args = Namespace(integration_endpoint="", endpoint="127.0.0.1:9000")
        self.assertEqual(rustfs_check.integration_endpoint(args), "127.0.0.1:9000")

    def test_explicit_integration_endpoint_overrides_container_default(self):
        args = Namespace(integration_endpoint="rustfs.internal:9000", endpoint="127.0.0.1:9000")
        self.assertEqual(rustfs_check.integration_endpoint(args), "rustfs.internal:9000")

    def test_remote_docker_context_uses_ssh_host(self):
        with patch.object(rustfs_check, "run_command") as run:
            run.side_effect = [
                type("Result", (), {"returncode": 0, "stdout": "remote\n", "stderr": ""})(),
                type("Result", (), {"returncode": 0, "stdout": "ssh://ubuntu-a\n", "stderr": ""})(),
            ]
            self.assertEqual(rustfs_check.docker_context_host(pathlib.Path("repo")), "ubuntu-a")

    def test_remote_go_tests_use_unique_workspaces_and_writable_cleanup(self):
        calls = []

        def fake_subprocess_run(command, **kwargs):
            calls.append(command)
            return type("Result", (), {"returncode": 0, "stdout": "ok", "stderr": ""})()

        with (
            patch.object(rustfs_check, "docker_context_host", return_value="ubuntu-a"),
            patch.object(rustfs_check.time, "time_ns", side_effect=[101, 202]),
            patch.object(rustfs_check.subprocess, "run", side_effect=fake_subprocess_run),
        ):
            rustfs_check.remote_go_test(["go", "test"], {}, pathlib.Path("repo"), 30)
            rustfs_check.remote_go_test(["go", "test"], {}, pathlib.Path("repo"), 30)

        setup_commands = [call[2] for call in calls if call[:2] == ["ssh", "ubuntu-a"] and call[2].startswith("mkdir -p")]
        cleanup_commands = [call[2] for call in calls if call[:2] == ["ssh", "ubuntu-a"] and "chmod -R u+w" in call[2]]
        self.assertEqual(len(setup_commands), 2)
        self.assertNotEqual(setup_commands[0], setup_commands[1])
        self.assertEqual(len(cleanup_commands), 2)
        self.assertTrue(all("rm -rf /tmp/vela-rustfs-check-" in command for command in cleanup_commands))

    def test_production_mode_requires_valid_retention_policy(self):
        args = Namespace(
            production=True,
            object_lock=True,
            object_lock_bucket="packaging-audit-lock",
            bucket="",
            access_key="prod-access",
            secret_key="prod-secret",
            retention_mode="",
            retention_days=0,
        )

        results = rustfs_check.validate_production_inputs(args)

        self.assertTrue(any(result.name == "Production object-lock retention mode" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Production object-lock retention days" and result.status == "fail" for result in results))


if __name__ == "__main__":
    unittest.main()
