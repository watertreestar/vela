import pathlib
import sys
import tempfile
import unittest
from argparse import Namespace
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

import windows_runner_check_cli


def args(**overrides):
    defaults = {
        "live": False,
        "packager_command": r"iscc C:\packaging-runner\context\rendered\setup.iss",
        "signing_command": r"signtool sign /fd SHA256 /tr http://timestamp.example.test /td SHA256 %OUTPUT%",
        "runner_command": r"bun C:\packaging-runner\package-runner.js",
        "api_url": "http://api.test:8080",
        "node_class": "windows-packager",
        "bun_cache_dir": "",
        "artifact_root": "",
        "secrets_dir": r"C:\nomad\alloc\secrets",
        "token_filename": "vela-runner-auth",
        "execution_filename": "vela-runner-execution",
        "runner_env": "PACKAGING_API_URL=http://api.test",
        "runner_env_file": "",
        "allocation_id": "",
        "build_id": "",
        "nomad_job_id": "",
        "task_sid": "S-1-5-21-1000",
        "packager_kind": "auto",
        "require_signing": True,
        "allow_non_windows": False,
        "json": False,
    }
    defaults.update(overrides)
    return Namespace(**defaults)


def fake_which(command):
    executable = pathlib.Path(command).name.lower()
    known = {
        "iscc": r"C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
        "iscc.exe": r"C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
        "signtool": r"C:\Program Files (x86)\Windows Kits\10\bin\x64\signtool.exe",
        "signtool.exe": r"C:\Program Files (x86)\Windows Kits\10\bin\x64\signtool.exe",
        "wix": r"C:\Program Files\WiX Toolset\wix.exe",
        "wix.exe": r"C:\Program Files\WiX Toolset\wix.exe",
    }
    return known.get(executable)


class WindowsRunnerCheckCliTest(unittest.TestCase):
    def test_non_live_preflight_does_not_claim_windows_live_success(self):
        with patch("platform.system", return_value="Linux"):
            results = windows_runner_check_cli.validate_readiness(args(allow_non_windows=True), which=fake_which)
        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(item.name == "Windows host" and item.status == "warn" for item in results))
        self.assertTrue(any(item.name == "Nomad allocation secrets" and item.status == "warn" for item in results))

    def test_live_non_windows_fails_even_with_allow_flag(self):
        with patch("platform.system", return_value="Linux"):
            results = windows_runner_check_cli.validate_readiness(args(live=True, allow_non_windows=True), which=fake_which)
        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(item.name == "Windows host" and item.status == "fail" for item in results))

    def test_fixture_packager_and_signing_are_rejected(self):
        with patch("platform.system", return_value="Windows"):
            results = windows_runner_check_cli.validate_readiness(
                args(packager_command="python3 /app/fixtures/windows_setup_packager.py", signing_command="python3 /app/fixtures/windows_signing_fixture.py"),
                which=fake_which,
            )
        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(item.name == "Windows packager command" and item.status == "fail" for item in results))

    def test_runner_env_file_is_scanned_even_when_inline_env_is_present(self):
        with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", delete=False) as handle:
            handle.write("AUTH_RUNNER_TOKEN=runner-secret-sentinel\n")
            env_file = handle.name
        try:
            result = windows_runner_check_cli.runner_env_check("PACKAGING_API_URL=http://api.test", env_file)
        finally:
            pathlib.Path(env_file).unlink(missing_ok=True)
        self.assertEqual(result.status, "fail")
        self.assertNotIn("runner-secret-sentinel", result.detail)

    def test_rejects_secret_filename_traversal(self):
        result = windows_runner_check_cli.static_secrets_checks(r"C:\nomad\alloc\secrets", "..\\runner.token", "vela-runner-execution")
        self.assertTrue(any(item.status == "fail" for item in result))

    def test_live_input_requires_current_allocation_and_evidence(self):
        with patch.dict("os.environ", {"NOMAD_ALLOC_ID": "alloc-1"}, clear=False):
            results = windows_runner_check_cli.live_input_checks("wrong", "", "", "", "")
        self.assertTrue(all(item.status == "fail" for item in results))

    def test_live_secret_checks_require_regular_nonempty_files_and_restricted_acl(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "vela-runner-auth").write_text("opaque", encoding="utf-8")
            (root / "vela-runner-execution").write_text("opaque", encoding="utf-8")
            verdicts = [{"reparse": False, "nullDacl": False, "ownerAllowed": True, "broadAccess": 0, "unexpectedAccess": 0, "taskRead": 1}] * 3
            results = windows_runner_check_cli.live_secret_checks(str(root), "vela-runner-auth", "vela-runner-execution", "S-1-5-21-1000", lambda *_: verdicts)
        self.assertEqual(results[0].status, "ok")

    def test_live_secret_checks_accepts_unresolved_owner_when_dacl_is_restricted(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "vela-runner-auth").write_text("opaque", encoding="utf-8")
            (root / "vela-runner-execution").write_text("opaque", encoding="utf-8")
            verdicts = [{"reparse": False, "nullDacl": False, "ownerAllowed": None, "broadAccess": 0, "unexpectedAccess": 0, "taskRead": 1}] * 3
            results = windows_runner_check_cli.live_secret_checks(str(root), "vela-runner-auth", "vela-runner-execution", "S-1-5-21-1000", lambda *_: verdicts)
        self.assertEqual(results[0].status, "ok")

    def test_live_secret_checks_reject_broad_acl(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "vela-runner-auth").write_text("opaque", encoding="utf-8")
            (root / "vela-runner-execution").write_text("opaque", encoding="utf-8")
            verdicts = [{"reparse": False, "nullDacl": False, "ownerAllowed": True, "broadAccess": 1, "unexpectedAccess": 0, "taskRead": 1}] * 3
            results = windows_runner_check_cli.live_secret_checks(str(root), "vela-runner-auth", "vela-runner-execution", "S-1-5-21-1000", lambda *_: verdicts)
        self.assertEqual(results[0].status, "fail")

    def test_live_secret_checks_allows_restricted_inheritance(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "vela-runner-auth").write_text("opaque", encoding="utf-8")
            (root / "vela-runner-execution").write_text("opaque", encoding="utf-8")
            verdicts = [{"reparse": False, "nullDacl": False, "ownerAllowed": True, "broadAccess": 0, "unexpectedAccess": 0, "taskRead": 1}] * 3
            results = windows_runner_check_cli.live_secret_checks(str(root), "vela-runner-auth", "vela-runner-execution", "S-1-5-21-1000", lambda *_: verdicts)
        self.assertEqual(results[0].status, "ok")

    def test_live_secret_checks_reject_unexpected_acl_principal(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / "vela-runner-auth").write_text("opaque", encoding="utf-8")
            (root / "vela-runner-execution").write_text("opaque", encoding="utf-8")
            verdicts = [{"reparse": False, "nullDacl": False, "ownerAllowed": True, "broadAccess": 0, "unexpectedAccess": 1, "taskRead": 1}] * 3
            results = windows_runner_check_cli.live_secret_checks(str(root), "vela-runner-auth", "vela-runner-execution", "S-1-5-21-1000", lambda *_: verdicts)
        self.assertEqual(results[0].status, "fail")

    def test_nomad_allocation_binding_rejects_another_job_without_leaking_credentials(self):
        payload = {
            "ID": "alloc-1",
            "JobID": "other-job",
            "TaskGroup": "builder",
            "DesiredStatus": "run",
            "TaskStates": {"package": {"Failed": False}},
            "Job": {"Meta": {"packaging.build_id": "build-1"}},
        }
        response = type("Response", (), {"__enter__": lambda self: self, "__exit__": lambda self, *args: None, "read": lambda self: __import__("json").dumps(payload).encode("utf-8")})()
        with patch("urllib.request.urlopen", return_value=response):
            result = windows_runner_check_cli.nomad_allocation_check("https://nomad.example.test", "nomad-token-sentinel", "alloc-1", "build-1", "job-1")
        self.assertEqual(result.status, "fail")
        self.assertNotIn("nomad-token-sentinel", result.detail)

    def test_wix_detection_checks_wix_toolchain(self):
        results = windows_runner_check_cli.toolchain_checks(r"wix build C:\setup.wxs", "", "wix", fake_which)
        self.assertTrue(any(item.name == "WiX toolchain" and item.status == "ok" for item in results))


if __name__ == "__main__":
    unittest.main()
