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
        "packager_command": r"iscc C:\packaging-runner\context\rendered\setup.iss",
        "signing_command": r"signtool sign /fd SHA256 /tr http://timestamp.example.test /td SHA256 %OUTPUT%",
        "runner_command": r"bun C:\packaging-runner\package-runner.js",
        "api_url": "http://api.test:8080",
        "node_class": "windows-packager",
        "bun_cache_dir": "",
        "artifact_root": "",
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
    def test_production_inno_and_signtool_configuration_passes(self):
        with tempfile.TemporaryDirectory() as bun_cache, tempfile.TemporaryDirectory() as artifact_root:
            with patch("platform.system", return_value="Windows"):
                results = windows_runner_check_cli.validate_readiness(
                    args(bun_cache_dir=bun_cache, artifact_root=artifact_root),
                    which=fake_which,
                )

        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "Inno Setup compiler" and result.status == "ok" for result in results))
        self.assertTrue(any(result.name == "SignTool" and result.status == "ok" for result in results))

    def test_missing_packager_command_fails(self):
        with patch("platform.system", return_value="Windows"):
            results = windows_runner_check_cli.validate_readiness(
                args(packager_command="", signing_command="signtool sign %OUTPUT%"),
                which=fake_which,
            )

        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Windows packager command" and result.status == "fail" for result in results))

    def test_fixture_packager_and_signing_are_rejected_for_production(self):
        with patch("platform.system", return_value="Windows"):
            results = windows_runner_check_cli.validate_readiness(
                args(
                    packager_command="python3 /app/fixtures/windows_setup_packager.py",
                    signing_command="python3 /app/fixtures/windows_signing_fixture.py",
                ),
                which=fake_which,
            )

        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 1)
        self.assertTrue(any(result.name == "Windows packager command" and result.status == "fail" for result in results))
        self.assertTrue(any(result.name == "Windows signing command" and result.status == "fail" for result in results))

    def test_unsigned_mode_warns_without_failing(self):
        with patch("platform.system", return_value="Windows"):
            results = windows_runner_check_cli.validate_readiness(
                args(signing_command="", require_signing=False),
                which=fake_which,
            )

        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "Windows signing command" and result.status == "warn" for result in results))

    def test_wix_detection_checks_wix_toolchain(self):
        with patch("platform.system", return_value="Windows"):
            results = windows_runner_check_cli.validate_readiness(
                args(packager_command=r"wix build C:\packaging-runner\context\rendered\setup.wxs"),
                which=fake_which,
            )

        self.assertEqual(windows_runner_check_cli.result_exit_code(results), 0, results)
        self.assertTrue(any(result.name == "WiX toolchain" and result.status == "ok" for result in results))


if __name__ == "__main__":
    unittest.main()
