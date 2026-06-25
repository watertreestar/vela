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
